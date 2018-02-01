package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"strings"

	"github.com/pivotal-cf/brokerapi"

	"github.com/jmcarp/cf-review-app/webhooks"
)

type ProvisionOptions struct {
	Token string
	Owner string
	Repo  string
}

func (o ProvisionOptions) Validate() error {
	missing := []string{}

	if o.Token == "" {
		missing = append(missing, "token")
	}
	if o.Owner == "" {
		missing = append(missing, "owner")
	}
	if o.Repo == "" {
		missing = append(missing, "repo")
	}

	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("Missing required fields: %s", strings.Join(missing, ", "))
}

type ReviewBroker struct {
	hookManager webhooks.HookManager
}

func New(m webhooks.HookManager) ReviewBroker {
	return ReviewBroker{hookManager: m}
}

func (b *ReviewBroker) Services(ctx context.Context) []brokerapi.Service {
	service := brokerapi.Service{}

	buf, err := ioutil.ReadFile("./catalog.json")
	if err != nil {
		return []brokerapi.Service{}
	}

	err = json.Unmarshal(buf, &service)
	if err != nil {
		return []brokerapi.Service{}
	}

	return []brokerapi.Service{service}
}

func (b *ReviewBroker) Provision(
	ctx context.Context,
	instanceID string,
	details brokerapi.ProvisionDetails,
	asyncAllowed bool,
) (brokerapi.ProvisionedServiceSpec, error) {
	spec := brokerapi.ProvisionedServiceSpec{}

	options := ProvisionOptions{}
	err := json.Unmarshal(details.RawParameters, &options)
	if err != nil {
		return spec, errors.New("Invalid configuration options")
	}

	err = options.Validate()
	if err != nil {
		return spec, err
	}

	_, err = b.hookManager.Create(
		details.OrganizationGUID, instanceID,
		options.Token, options.Owner, options.Repo,
	)

	return spec, nil
}

func (b *ReviewBroker) LastOperation(ctx context.Context, instanceID, operationData string) (brokerapi.LastOperation, error) {
	return brokerapi.LastOperation{}, nil
}

func (b *ReviewBroker) Deprovision(ctx context.Context, instanceID string, details brokerapi.DeprovisionDetails, asyncAllowed bool) (brokerapi.DeprovisionServiceSpec, error) {
	return brokerapi.DeprovisionServiceSpec{}, b.hookManager.Delete(instanceID)
}

func (b *ReviewBroker) Bind(ctx context.Context, instanceID, bindingID string, details brokerapi.BindDetails) (brokerapi.Binding, error) {
	return brokerapi.Binding{}, errors.New("Service does not support bind")
}

func (b *ReviewBroker) Unbind(ctx context.Context, instanceID, bindingID string, details brokerapi.UnbindDetails) error {
	return errors.New("Service does not support bind")
}

func (b *ReviewBroker) Update(ctx context.Context, instanceID string, details brokerapi.UpdateDetails, asyncAllowed bool) (brokerapi.UpdateServiceSpec, error) {
	return brokerapi.UpdateServiceSpec{}, errors.New("Service does not support update")
}
