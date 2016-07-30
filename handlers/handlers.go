package handlers

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"os"

	"github.com/google/go-github/github"
	"github.com/gorilla/mux"
	"github.com/jinzhu/gorm"
	"golang.org/x/oauth2"

	"github.com/jmcarp/cf-review-app/cloudfoundry"
	"github.com/jmcarp/cf-review-app/models"
	"github.com/jmcarp/cf-review-app/utils"
	"github.com/jmcarp/cf-review-app/webhooks"
)

type HookHandler struct {
	db *gorm.DB
}

func NewHookHandler(db *gorm.DB) HookHandler {
	return HookHandler{db: db}
}

func (h *HookHandler) Handle(res http.ResponseWriter, req *http.Request) {
	body, err := ioutil.ReadAll(req.Body)
	if err != nil {
		writeError(res, http.StatusBadRequest, "Invalid payload")
		return
	}

	payload := webhooks.PullPayload{}
	err = json.Unmarshal(body, &payload)
	if err != nil {
		writeError(res, http.StatusBadRequest, "Invalid payload")
		return
	}

	hook := models.Hook{}
	result := h.db.Where(
		models.Hook{InstanceID: mux.Vars(req)["instance"]},
	).Find(&hook)
	if result.RecordNotFound() {
		writeError(res, http.StatusNotFound, "")
		return
	}
	if result.Error != nil {
		writeError(res, http.StatusInternalServerError, "")
		return
	}

	signature := req.Header.Get("X-Hub-Signature")
	signed := utils.CheckSignature([]byte(hook.Secret), body, signature)
	if !signed {
		writeError(res, http.StatusUnauthorized, "Invalid signature")
		return
	}

	err = handleHook(payload, hook)
	if err != nil {
		writeError(res, http.StatusInternalServerError, "")
	}
}

func handleHook(payload webhooks.PullPayload, hook models.Hook) error {
	handler := webhooks.NewPullHandler(
		github.NewClient(
			oauth2.NewClient(oauth2.NoContext,
				oauth2.StaticTokenSource(
					&oauth2.Token{AccessToken: hook.Token},
				),
			),
		),
		cloudfoundry.NewCloudFoundry(
			os.Getenv("CF_API"),
			os.Getenv("CF_USERNAME"),
			os.Getenv("CF_PASSWORD"),
		),
	)

	switch payload.Action {
	case "opened", "reopened", "synchronize":
		return handler.Open(payload)
	case "closed":
		return handler.Close(payload)
	}
	return nil
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