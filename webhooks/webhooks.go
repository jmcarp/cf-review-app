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
	"gopkg.in/yaml.v2"

	"github.com/jmcarp/cf-review-app/cloudfoundry"
	"github.com/jmcarp/cf-review-app/models"
	"github.com/jmcarp/cf-review-app/utils"
)

func String(s string) *string {
	return &s
}

func Bool(b bool) *bool {
	return &b
}

type RepoHandler struct {
	client *github.Client
}

// NewRepoHandler creates a new RepoHandler
func NewRepoHandler(client *github.Client) *RepoHandler {
	return &RepoHandler{client}
}

// Bind creates a GitHub webhook
func (rh *RepoHandler) Bind(owner, repo, instance, secret string) (int, error) {
	// TODO: Pass from config
	u, err := url.Parse(os.Getenv("URL"))
	if err != nil {
		return 0, err
	}
	u.Path = path.Join(u.Path, "hook", instance)

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

	hook, _, err = rh.client.Repositories.CreateHook(owner, repo, hook)
	if err != nil {
		return 0, err
	}

	return *hook.ID, nil
}

// Unbind deletes a GitHub webhook
func (rh *RepoHandler) Unbind(owner, repo string, hookID int) error {
	_, err := rh.client.Repositories.DeleteHook(owner, repo, hookID)
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
		Name  string
		Owner struct {
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

func getSpace(owner, repo string, number int) string {
	return fmt.Sprintf("%s-%s-pull-%d", owner, repo, number)
}

type PullHandler struct {
	client   *github.Client
	cfClient *cloudfoundry.CloudFoundry
}

func NewPullHandler(client *github.Client, cfClient *cloudfoundry.CloudFoundry) PullHandler {
	return PullHandler{client: client, cfClient: cfClient}
}

func (ph *PullHandler) Open(payload PullPayload) error {
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
	err = ph.cfClient.Target(os.Getenv("CF_ORG"))
	err = ph.cfClient.Create(app, space)
	if err != nil {
		return err
	}

	// TODO: Mark deploy as failed on error
	_, _, err = ph.client.Repositories.CreateDeploymentStatus(
		payload.Owner(), payload.Repo(),
		*deployment.ID,
		&github.DeploymentStatusRequest{
			State:       String("success"),
			TargetURL:   String(""),
			Description: String("Deployed review app"),
		},
	)
	if err != nil {
		return err
	}

	return nil
}

func (ph *PullHandler) Close(payload PullPayload) error {
	space := getSpace(payload.Owner(), payload.Repo(), payload.Number)

	err := ph.cfClient.Login()
	err = ph.cfClient.Target(os.Getenv("CF_ORG"))
	err = ph.cfClient.Delete(space)

	return err
}

func (ph *PullHandler) getUrl(user, repo, sha string) (string, error) {
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

	url, err := ph.getUrl(payload.Owner(), payload.Repo(), payload.PullRequest.Head.Sha)
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
