package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/postgres"
	_ "github.com/lib/pq"

	"github.com/google/go-github/github"
	"github.com/gorilla/mux"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v2"

	"github.com/jmcarp/cf-review-app/utils"
	// "github.com/jmcarp/cf-review-app/models"
)

func String(s string) *string {
	return &s
}

func Bool(b bool) *bool {
	return &b
}

func clientFromToken(token string) *RepoHandler {
	return NewRepoHandler(
		github.NewClient(
			oauth2.NewClient(oauth2.NoContext,
				oauth2.StaticTokenSource(
					&oauth2.Token{AccessToken: token},
				),
			),
		),
	)
}

func BindHook(db *gorm.DB, orgId, token, owner, repo string) (Hook, error) {
	handler := clientFromToken(token)

	hookId, err := handler.Bind(owner, repo)
	if err != nil {
		return Hook{}, err
	}

	hook := Hook{
		Token:  token,
		OrgId:  orgId,
		Owner:  owner,
		Repo:   repo,
		HookId: hookId,
	}

	err = db.Create(&hook).Error
	if err != nil {
		handler.Unbind(owner, repo, hookId)
		return Hook{}, err
	}

	return hook, nil
}

func UnbindHook(db *gorm.DB, orgId, owner, repo string) error {
	hook := Hook{
		OrgId: orgId,
		Owner: owner,
		Repo:  repo,
	}

	err := db.Where(hook).Find(&hook).Error
	if err != nil {
		return err
	}

	handler := clientFromToken(hook.Token)

	err = handler.Unbind(hook.Owner, hook.Repo, hook.HookId)
	if err != nil {
		return err
	}

	return db.Delete(&hook).Error
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
			"secret":       os.Getenv("HOOK_SECRET"),
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

//

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
		FullName string `json:"full_name"`
	}
}

func getSpace(owner, repo string, number int) string {
	return fmt.Sprintf("%s-%s-pull-%d", owner, repo, number)
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

	space := getSpace(parts[0], parts[1], payload.Number)

	deployment, _, err := ph.client.Repositories.CreateDeployment(
		parts[0], parts[1],
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
		parts[0], parts[1],
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
	parts := strings.Split(payload.PullRequest.Head.Repo.FullName, "/")

	space := getSpace(parts[0], parts[1], payload.Number)

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
	Services []Service
}

type Service struct {
	Name    string
	Service string
	Plan    string
	Tags    []string
	Config  map[string]interface{}
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

func (cfc *CloudFoundryClient) Create(app App, space string) error {
	err := cfc.createSpace(space)
	if err != nil {
		return err
	}

	err = cfc.createServices(app)
	if err != nil {
		return err
	}

	err = cfc.createApp(app.Name, app.Manifest)
	if err != nil {
		return err
	}

	return nil
}

func (cfc *CloudFoundryClient) Delete(space string) error {
	return cfc.deleteSpace(space)
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

func (cfc *CloudFoundryClient) createServices(app App) error {
	for _, service := range app.Services {
		err := cfc.createService(service)
		if err != nil {
			return err
		}
	}
	return nil
}

func (cfc *CloudFoundryClient) createService(service Service) error {
	args := []string{"create-service", service.Service, service.Plan, service.Name}
	if len(service.Tags) > 0 {
		args = append(args, "-t", strings.Join(service.Tags, ","))
	}
	if len(service.Config) > 0 {
		config, err := json.Marshal(service.Config)
		if err != nil {
			return err
		}
		args = append(args, "-c", string(config))
	}

	err := cfc.cf(args...).Run()
	if err != nil {
		return err
	}

	return cfc.checkService(service, 30)
}

func (cfc *CloudFoundryClient) checkService(service Service, timeout int) error {
	args := []string{"service", service.Name}
	elapsed := 0

	for {
		var buf bytes.Buffer
		cmd := cfc.cf(args...)
		cmd.Stdout = io.MultiWriter(os.Stdout, &buf)
		err := cmd.Run()

		if err == nil {
			output := buf.String()
			for _, line := range strings.Split(output, "\n") {
				if line == "Status: create succeeded" {
					return nil
				}
			}
		}

		elapsed += 5
		if elapsed > timeout {
			return fmt.Errorf("Service %s incomplete", service.Name)
		}

		time.Sleep(5 * time.Second)
	}
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
		writeError(res, http.StatusBadRequest, "Invalid payload")
		return
	}

	payload := PullPayload{}
	err = json.Unmarshal(body, &payload)
	if err != nil {
		writeError(res, http.StatusBadRequest, "Invalid payload")
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
	case "opened", "reopened", "synchronize":
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

func Connect(databaseUrl string) (*gorm.DB, error) {
	return gorm.Open("postgres", databaseUrl)
}

// func main() {
// 	db, _ := Connect(os.Getenv("DATABASE_URL"))
// 	db.AutoMigrate(&Hook{})
// 	hook, err := BindHook(db, os.Getenv("CF_ORG"), os.Getenv("GH_TOKEN"), os.Getenv("GH_OWNER"), os.Getenv("GH_REPO"))
// 	fmt.Println(hook, err)
// }
