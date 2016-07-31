package webhooks

import (
	"github.com/jinzhu/gorm"

	"github.com/jmcarp/cf-review-app/config"
	"github.com/jmcarp/cf-review-app/models"
	"github.com/jmcarp/cf-review-app/utils"
)

type HookManager interface {
	Get(instanceID string) (models.Hook, error)
	Create(orgID, instanceID, token, owner, repo string) (models.Hook, error)
	Delete(instanceID string) error
}

type Manager struct {
	db            *gorm.DB
	settings      config.Settings
	clientFactory func(token string, settings config.Settings) WebhookClient
}

func NewManager(db *gorm.DB, settings config.Settings, factory func(token string, settings config.Settings) WebhookClient) HookManager {
	return &Manager{
		db:            db,
		settings:      settings,
		clientFactory: factory,
	}
}

func (m *Manager) Get(instanceID string) (models.Hook, error) {
	hook := models.Hook{InstanceID: instanceID}
	err := m.db.Where(hook).Find(&hook).Error
	return hook, err
}

func (m *Manager) Create(orgID, instanceID, token, owner, repo string) (models.Hook, error) {
	client := m.clientFactory(token, m.settings)

	secret, err := utils.SecureRandom(32)
	if err != nil {
		return models.Hook{}, err
	}

	hookID, err := client.Bind(owner, repo, instanceID, secret)
	if err != nil {
		return models.Hook{}, err
	}

	hook := models.Hook{
		Token:      token,
		Secret:     secret,
		InstanceID: instanceID,
		OrgID:      orgID,
		Owner:      owner,
		Repo:       repo,
		HookID:     hookID,
	}

	err = m.db.Create(&hook).Error
	if err != nil {
		client.Unbind(owner, repo, hookID)
		return models.Hook{}, err
	}

	return hook, nil
}

func (m *Manager) Delete(instanceID string) error {
	hook := models.Hook{InstanceID: instanceID}
	err := m.db.Where(hook).Find(&hook).Error
	if err != nil {
		return err
	}

	client := m.clientFactory(hook.Token, m.settings)

	err = client.Unbind(hook.Owner, hook.Repo, hook.HookID)
	if err != nil {
		return err
	}

	return m.db.Delete(&hook).Error
}
