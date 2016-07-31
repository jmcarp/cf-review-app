package config

import (
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/postgres"
	_ "github.com/lib/pq"
)

func Connect(databaseUrl string) (*gorm.DB, error) {
	return gorm.Open("postgres", databaseUrl)
}
