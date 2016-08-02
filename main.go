package main

import (
	"fmt"
	"net/http"
	"os"

	"code.cloudfoundry.org/lager"
	"github.com/gorilla/mux"
	"github.com/pivotal-cf/brokerapi"

	"github.com/jmcarp/cf-review-app/broker"
	"github.com/jmcarp/cf-review-app/config"
	"github.com/jmcarp/cf-review-app/handlers"
	"github.com/jmcarp/cf-review-app/models"
	"github.com/jmcarp/cf-review-app/webhooks"
)

func main() {
	logger := lager.NewLogger("review-broker")
	logger.RegisterSink(lager.NewWriterSink(os.Stderr, lager.INFO))

	settings, err := config.NewSettings()
	if err != nil {
		logger.Fatal("settings", err)
	}

	db, err := config.Connect(settings.DatabaseURL)
	if err != nil {
		logger.Fatal("connect", err)
	}

	err = db.AutoMigrate(&models.Hook{}).Error
	if err != nil {
		logger.Fatal("migrate", err)
	}

	credentials := brokerapi.BrokerCredentials{
		Username: settings.BrokerUsername,
		Password: settings.BrokerPassword,
	}

	// Attach webhook routes
	router := mux.NewRouter()
	handler := handlers.NewHookHandler(db, settings)
	router.HandleFunc("/hook/{instance}", handler.Handle).Methods("POST")
	http.Handle("/hook/", router)

	// Attach service broker routes
	manager := webhooks.NewManager(db, settings, webhooks.NewClient)
	broker := broker.New(manager)
	brokerAPI := brokerapi.New(&broker, logger, credentials)
	http.Handle("/", brokerAPI)

	http.ListenAndServe(fmt.Sprintf(":%s", settings.Port), nil)
}
