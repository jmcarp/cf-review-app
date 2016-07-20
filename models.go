package main

type Hook struct {
	ID     uint   `gorm:"primary_key"`
	Token  string `gorm:"not null"`
	OrgId  string `gorm:"not null;unique_index:idx_org_owner_repo"`
	Owner  string `gorm:"not null;unique_index:idx_org_owner_repo"`
	Repo   string `gorm:"not null;unique_index:idx_org_owner_repo"`
	HookId int
}
