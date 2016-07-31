package webhooks

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v2"

	"github.com/jmcarp/cf-review-app/cloudfoundry"
	"github.com/jmcarp/cf-review-app/config"
	"github.com/jmcarp/cf-review-app/models"
	"github.com/jmcarp/cf-review-app/utils"
)

func String(s string) *string {
	return &s
}

func Bool(b bool) *bool {
	return &b
}

type WebhookClient interface {
	Bind(owner, repo, instanceID, secret string) (int, error)
	Unbind(owner, repo string, hookID int) error
}

type Client struct {
	client   *github.Client
	settings config.Settings
}

// NewClient creates a new Client
func NewClient(token string, settings config.Settings) WebhookClient {
	client := github.NewClient(
		oauth2.NewClient(
			oauth2.NoContext,
			oauth2.StaticTokenSource(
				&oauth2.Token{AccessToken: token},
			),
		),
	)
	return &Client{client: client, settings: settings}
}

// Bind creates a GitHub webhook
func (c *Client) Bind(owner, repo, instanceID, secret string) (int, error) {
	u, err := url.Parse(c.settings.BaseURL)
	if err != nil {
		return 0, err
	}
	u.Path = path.Join(u.Path, "hook", instanceID)

	hook := &github.Hook{
		Name:   String("web"),
		Active: Bool(true),
		Events: []string{"pull_request"},
		Config: map[string]interface{}{
			"url":          u.String(),
			"secret":       secret,
			"content_type": "application/json",
		},
	}

	hook, _, err = c.client.Repositories.CreateHook(owner, repo, hook)
	if err != nil {
		return 0, err
	}

	return *hook.ID, nil
}

// Unbind deletes a GitHub webhook
func (c *Client) Unbind(owner, repo string, hookID int) error {
	_, err := c.client.Repositories.DeleteHook(owner, repo, hookID)
	return err
}

// https://developer.github.com/v3/activity/events/types/#pullrequestevent
type PullPayload struct {
	Action      string
	Number      int
	PullRequest struct {
		Head RefPayload
		Base RefPayload
	} `json:"pull_request"`
}

type RefPayload struct {
	Sha  string
	Repo struct {
		Name     string
		FullName string `json:"full_name"`
		Owner    struct {
			Login string
		}
	}
}

func (p PullPayload) Owner() string {
	return p.PullRequest.Base.Repo.Owner.Login
}

func (p PullPayload) Repo() string {
	return p.PullRequest.Base.Repo.Name
}

func (p PullPayload) IsFork() bool {
	return p.PullRequest.Base.Repo.FullName != p.PullRequest.Head.Repo.FullName
}

func getSpace(owner, repo string, number int) string {
	return fmt.Sprintf("%s-%s-pull-%d", owner, repo, number)
}

type PullHandler struct {
	client   *github.Client
	cfClient *cloudfoundry.CloudFoundry
}

func NewPullHandler(client *github.Client, cfClient *cloudfoundry.CloudFoundry) *PullHandler {
	return &PullHandler{client: client, cfClient: cfClient}
}

func (ph *PullHandler) Open(orgID string, payload PullPayload) error {
	sha := payload.PullRequest.Head.Sha

	path, err := ph.download(payload)
	if err != nil {
		return err
	}
	defer os.RemoveAll(path)

	dir := fmt.Sprintf("%s-%s-%s", payload.Owner(), payload.Repo(), sha[:7])
	appPath := filepath.Join(path, dir)
	appYmlPath := filepath.Join(appPath, "app.yml")

	app, err := ph.getAppYml(appYmlPath)
	if err != nil {
		return err
	}

	here, err := os.Getwd()
	if err != nil {
		return err
	}
	os.Chdir(appPath)
	defer os.Chdir(here)

	space := getSpace(payload.Owner(), payload.Repo(), payload.Number)

	deployment, _, err := ph.client.Repositories.CreateDeployment(
		payload.Owner(), payload.Repo(),
		&github.DeploymentRequest{
			Ref:         String(payload.PullRequest.Head.Sha),
			Task:        String("deploy:review"),
			Environment: String("review"),
		},
	)
	if err != nil {
		return err
	}

	dest := filepath.Join(appPath, fmt.Sprintf("manifest-review-%s.yml", payload.PullRequest.Head.Sha))
	err = utils.MakeManifest(app.Name, app.Manifest, dest)
	if err != nil {
		return err
	}
	app.Manifest = dest

	err = ph.cfClient.Login()
	err = ph.cfClient.Target(orgID)
	route, err := ph.cfClient.Create(app, space)
	if err != nil {
		ph.client.Repositories.CreateDeploymentStatus(
			payload.Owner(), payload.Repo(),
			*deployment.ID,
			&github.DeploymentStatusRequest{
				State: String("error"),
			},
		)
		return err
	}

	_, _, err = ph.client.Repositories.CreateDeploymentStatus(
		payload.Owner(), payload.Repo(),
		*deployment.ID,
		&github.DeploymentStatusRequest{
			State:       String("success"),
			TargetURL:   String(fmt.Sprintf("https://%s", route)),
			Description: String("Deployed review app"),
		},
	)
	return err
}

func (ph *PullHandler) Close(orgID string, payload PullPayload) error {
	space := getSpace(payload.Owner(), payload.Repo(), payload.Number)

	err := ph.cfClient.Login()
	err = ph.cfClient.Target(orgID)
	err = ph.cfClient.Delete(space)

	deployments, _, err := ph.client.Repositories.ListDeployments(
		payload.Owner(), payload.Repo(),
		&github.DeploymentsListOptions{
			Ref:         payload.PullRequest.Head.Sha,
			Task:        "deploy:review",
			Environment: "review",
		},
	)
	if err != nil {
		return err
	}

	if len(deployments) == 0 {
		return nil
	}

	_, _, err = ph.client.Repositories.CreateDeploymentStatus(
		payload.Owner(), payload.Repo(),
		*deployments[0].ID,
		&github.DeploymentStatusRequest{
			State:       String("inactive"),
			Description: String("Deleted review app"),
		},
	)
	return err
}

func (ph *PullHandler) getArchiveURL(user, repo, sha string) (string, error) {
	ref := &github.RepositoryContentGetOptions{Ref: sha}
	url, _, err := ph.client.Repositories.GetArchiveLink(user, repo, "tarball", ref)
	if err != nil {
		return "", err
	}
	return url.String(), nil
}

func (ph *PullHandler) download(payload PullPayload) (string, error) {
	path, err := ioutil.TempDir("", "")
	if err != nil {
		return "", err
	}

	url, err := ph.getArchiveURL(payload.Owner(), payload.Repo(), payload.PullRequest.Head.Sha)
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	err = utils.Untar(resp.Body, path)
	if err != nil {
		return "", err
	}

	return path, nil
}

func (ph *PullHandler) getAppYml(path string) (models.App, error) {
	content, err := ioutil.ReadFile(path)
	if err != nil {
		return models.App{}, err
	}

	app := models.App{}
	err = yaml.Unmarshal(content, &app)
	if err != nil {
		return models.App{}, err
	}

	return app, nil
}
