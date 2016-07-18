package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/google/go-github/github"
	"github.com/gorilla/mux"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v2"

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
func (rh *RepoHandler) Bind(owner, repo string) (int, error) {
	// TODO: Pass from config
	u, err := url.Parse(os.Getenv("URL"))
	if err != nil {
		return 0, err
	}
	u.Path = path.Join(u.Path, "/hook")

	hook := &github.Hook{
		Name:   String("web"),
		Active: Bool(true),
		Events: []string{"pull_request"},
		Config: map[string]interface{}{
			"url":          u.String(),
			"secret":       os.Getenv("SECRET"),
			"content-type": "json",
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

//

type PullPayload struct {
	Action      string
	PullRequest struct {
		Head RefPayload
		Base RefPayload
	} `json:"pull_request"`
}

type RefPayload struct {
	Sha  string
	Repo struct {
		FullName string `json:"full_name"`
	}
}

type PullHandler struct {
	client   *github.Client
	cfClient *CloudFoundryClient
}

func (ph *PullHandler) Open(payload PullPayload) error {
	parts := strings.Split(payload.PullRequest.Head.Repo.FullName, "/")

	path, err := ph.download(payload)
	if err != nil {
		return err
	}
	defer os.RemoveAll(path)

	dir := fmt.Sprintf("%s-%s-%s", parts[0], parts[1], payload.PullRequest.Head.Sha[:7])
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

	err = ph.cfClient.Login()
	err = ph.cfClient.Target(os.Getenv("CF_ORG"))
	err = ph.cfClient.Create(app)
	if err != nil {
		return err
	}

	_, _, err = ph.client.Repositories.CreateStatus(
		parts[0], parts[1],
		payload.PullRequest.Head.Sha,
		&github.RepoStatus{
			State:       String("success"),
			Description: String("Deployed review app"),
		},
	)
	if err != nil {
		return err
	}

	return nil
}

func (ph *PullHandler) Close(payload PullPayload) error {
	parts := strings.Split(payload.PullRequest.Head.Repo.FullName, "/")

	path, err := ph.download(payload)
	if err != nil {
		return err
	}
	defer os.RemoveAll(path)

	dir := fmt.Sprintf("%s-%s-%s", parts[0], parts[1], payload.PullRequest.Head.Sha[:7])
	appPath := filepath.Join(path, dir)
	appYmlPath := filepath.Join(appPath, "app.yml")

	app, err := ph.getAppYml(appYmlPath)
	if err != nil {
		return err
	}

	err = ph.cfClient.Login()
	err = ph.cfClient.Target(os.Getenv("CF_ORG"))
	err = ph.cfClient.Delete(app)

	return nil
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

	parts := strings.Split(payload.PullRequest.Head.Repo.FullName, "/")
	url, err := ph.getUrl(parts[0], parts[1], payload.PullRequest.Head.Sha)
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

func (ph *PullHandler) getAppYml(path string) (App, error) {
	content, err := ioutil.ReadFile(path)
	if err != nil {
		return App{}, err
	}

	var app App
	err = yaml.Unmarshal(content, &app)
	if err != nil {
		return App{}, err
	}

	return app, nil
}

type App struct {
	Name     string
	Manifest string
	Space    string
}

type CloudFoundryClient struct {
	api      string
	username string
	password string
}

func (cfc *CloudFoundryClient) Login() error {
	args := []string{"api", cfc.api}

	err := cfc.cf(args...).Run()
	if err != nil {
		return err
	}

	return cfc.cf("auth", cfc.username, cfc.password).Run()
}

func (cfc *CloudFoundryClient) Target(org string) error {
	args := []string{"target", "-o", org}
	return cfc.cf(args...).Run()
}

func (cfc *CloudFoundryClient) Create(app App) error {
	err := cfc.createSpace(app.Space)
	if err != nil {
		return err
	}

	err = cfc.createServices()
	if err != nil {
		return err
	}

	err = cfc.createApp(app.Name, app.Manifest)
	if err != nil {
		return err
	}

	return nil
}

func (cfc *CloudFoundryClient) Delete(app App) error {
	return cfc.deleteSpace(app.Space)
}

func (cfc *CloudFoundryClient) createSpace(space string) error {
	args := []string{"create-space", space}
	err := cfc.cf(args...).Run()
	if err != nil {
		return err
	}

	args = []string{"target", "-s", space}
	return cfc.cf(args...).Run()
}

func (cfc *CloudFoundryClient) deleteSpace(space string) error {
	args := []string{"delete-space", space, "-f"}
	return cfc.cf(args...).Run()
}

func (cfc *CloudFoundryClient) createServices() error {
	return nil
}

func (cfc *CloudFoundryClient) createApp(app, manifest string) error {
	args := []string{"push", app, "-f", manifest}
	return cfc.cf(args...).Run()
}

func (cfc *CloudFoundryClient) cf(args ...string) *exec.Cmd {
	cmd := exec.Command("cf", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	// TODO: Handle per-pull CF_HOME
	cmd.Env = append(os.Environ(), "CF_COLOR=true")

	return cmd
}

type HTTPError struct {
	Status  int
	Message string `json:",omitempty"`
}

func writeError(res http.ResponseWriter, status int, message string) {
	res.WriteHeader(status)
	httpError := HTTPError{
		Status:  status,
		Message: message,
	}
	json.NewEncoder(res).Encode(httpError)
}

func handlePullHook(res http.ResponseWriter, req *http.Request) {
	body, err := ioutil.ReadAll(req.Body)
	if err != nil {
		writeError(res, http.StatusInternalServerError, "Invalid payload")
		return
	}

	var payload PullPayload
	err = json.Unmarshal(body, &payload)
	if err != nil {
		writeError(res, http.StatusInternalServerError, "Invalid payload")
		return
	}

	signature := req.Header.Get("X-Hub-Signature")
	signed := utils.CheckSignature([]byte(os.Getenv("HOOK_SECRET")), body, signature)
	if !signed {
		writeError(res, http.StatusForbidden, "Invalid signature")
		return
	}

	handler := PullHandler{
		client: github.NewClient(
			oauth2.NewClient(oauth2.NoContext,
				oauth2.StaticTokenSource(
					&oauth2.Token{AccessToken: os.Getenv("GH_TOKEN")},
				),
			),
		),
		cfClient: &CloudFoundryClient{
			api:      os.Getenv("CF_API"),
			username: os.Getenv("CF_USERNAME"),
			password: os.Getenv("CF_PASSWORD"),
		},
	}

	switch payload.Action {
	case "opened", "reopened", "edited":
		err = handler.Open(payload)
		if err != nil {
			writeError(res, http.StatusInternalServerError, "")
		}
	case "closed":
		err = handler.Close(payload)
		if err != nil {
			writeError(res, http.StatusInternalServerError, "")
		}
	}
}

func main() {
	router := mux.NewRouter()
	router.HandleFunc("/hook", handlePullHook).Methods("POST")
	http.ListenAndServe(":8080", router)
}
