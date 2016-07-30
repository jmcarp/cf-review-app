package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/postgres"
	_ "github.com/lib/pq"

	"github.com/gorilla/mux"

	"code.cloudfoundry.org/lager"
	"github.com/pivotal-cf/brokerapi"

	"github.com/jmcarp/cf-review-app/broker"
	"github.com/jmcarp/cf-review-app/handlers"
	"github.com/jmcarp/cf-review-app/webhooks"
)

func Connect(databaseUrl string) (*gorm.DB, error) {
	return gorm.Open("postgres", databaseUrl)
}

func main() {
	logger := lager.NewLogger("review-broker")
	logger.RegisterSink(lager.NewWriterSink(os.Stderr, lager.INFO))

	db, err := Connect(os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal("Failed to connect to database")
	}

	credentials := brokerapi.BrokerCredentials{
		Username: os.Getenv("BROKER_USER"),
		Password: os.Getenv("BROKER_PASS"),
	}

	// Attach webhook routes
	router := mux.NewRouter()
	handler := handlers.NewHookHandler(db)
	router.HandleFunc("/hook/{instance}", handler.Handle).Methods("POST")
	http.Handle("/hook/", router)

	// Attach service broker routes
	manager := webhooks.NewManager(db, webhooks.NewClient)
	broker := broker.New(manager)
	brokerAPI := brokerapi.New(&broker, logger, credentials)
	http.Handle("/", brokerAPI)

	http.ListenAndServe(fmt.Sprintf(":%s", os.Getenv("PORT")), nil)
}
