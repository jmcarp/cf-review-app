package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/postgres"
	_ "github.com/lib/pq"

	"github.com/google/go-github/github"
	"github.com/gorilla/mux"
	"golang.org/x/oauth2"

	"github.com/jmcarp/cf-review-app/handlers"
	"github.com/jmcarp/cf-review-app/models"
	"github.com/jmcarp/cf-review-app/utils"
	"github.com/jmcarp/cf-review-app/webhooks"
)

func clientFromToken(token string) *webhooks.RepoHandler {
	return webhooks.NewRepoHandler(
		github.NewClient(
			oauth2.NewClient(oauth2.NoContext,
				oauth2.StaticTokenSource(
					&oauth2.Token{AccessToken: token},
				),
			),
		),
	)
}

func BindHook(db *gorm.DB, orgId, instanceId, token, owner, repo string) (models.Hook, error) {
	handler := clientFromToken(token)

	secret, err := utils.SecureRandom(32)
	if err != nil {
		return models.Hook{}, err
	}

	hookId, err := handler.Bind(owner, repo, instanceId, secret)
	if err != nil {
		return models.Hook{}, err
	}

	hook := models.Hook{
		Token:      token,
		Secret:     secret,
		InstanceId: instanceId,
		OrgId:      orgId,
		Owner:      owner,
		Repo:       repo,
		HookId:     hookId,
	}

	err = db.Create(&hook).Error
	if err != nil {
		handler.Unbind(owner, repo, hookId)
		return models.Hook{}, err
	}

	return hook, nil
}

func UnbindHook(db *gorm.DB, orgId, owner, repo string) error {
	hook := models.Hook{
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

func Connect(databaseUrl string) (*gorm.DB, error) {
	return gorm.Open("postgres", databaseUrl)
}

func main() {
	db, err := Connect(os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal("Failed to connect to database")
	}

	router := mux.NewRouter()

	handler := handlers.NewHookHandler(db)
	router.HandleFunc("/hook/{instance}", handler.Handle).Methods("POST")

	http.ListenAndServe(fmt.Sprintf(":%s", os.Getenv("PORT")), router)
}

// func main() {
// 	db, _ := Connect(os.Getenv("DATABASE_URL"))
// 	db.AutoMigrate(&models.Hook{})
// 	hook, err := BindHook(db, os.Getenv("CF_ORG"), os.Getenv("CF_INSTANCE"), os.Getenv("GH_TOKEN"), os.Getenv("GH_OWNER"), os.Getenv("GH_REPO"))
// 	fmt.Println(hook, err)
// }
