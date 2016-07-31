package config

import (
	"github.com/kelseyhightower/envconfig"
)

type Settings struct {
	Port           string `envconfig:"port" default:"3000"`
	CFURL          string `envconfig:"cf_url" required:"true"`
	CFUsername     string `envconfig:"cf_username" required:"true"`
	CFPassword     string `envconfig:"cf_password" required:"true"`
	BrokerUsername string `envconfig:"broker_username" required:"true"`
	BrokerPassword string `envconfig:"broker_password" required:"true"`
	DatabaseURL    string `envconfig:"database_url" required:"true"`
	BaseURL        string `envconfig:"base_url" required:"true"`
}

func NewSettings() (Settings, error) {
	settings := Settings{}
	err := envconfig.Process("review-app", &settings)
	return settings, err
}
