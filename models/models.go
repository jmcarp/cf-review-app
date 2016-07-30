package models

type Hook struct {
	ID         uint   `gorm:"primary_key"`
	Token      string `gorm:"not null"`
	Secret     string `gorm:"not null"`
	InstanceID string `gorm:"not null;unique_index"`
	OrgID      string `gorm:"not null;unique_index:idx_org_owner_repo"`
	Owner      string `gorm:"not null;unique_index:idx_org_owner_repo"`
	Repo       string `gorm:"not null;unique_index:idx_org_owner_repo"`
	HookID     int
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
