package cloudfoundry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/cloudfoundry/cli/cf/api/resources"

	"github.com/jmcarp/cf-review-app/models"
)

type CloudFoundry struct {
	api      string
	username string
	password string
}

func NewCloudFoundry(api, username, password string) *CloudFoundry {
	return &CloudFoundry{api, username, password}
}

func (cf *CloudFoundry) Login() error {
	args := []string{"api", cf.api}

	err := cf.cf(args...).Run()
	if err != nil {
		return err
	}

	return cf.cf("auth", cf.username, cf.password).Run()
}

func (cf *CloudFoundry) Target(orgID string) error {
	org, err := cf.getOrg(orgID)
	if err != nil {
		return err
	}

	args := []string{"target", "-o", org}
	return cf.cf(args...).Run()
}

func (cf *CloudFoundry) Create(app models.App, space string) (string, error) {
	err := cf.createSpace(space)
	if err != nil {
		return "", err
	}

	err = cf.createServices(app)
	if err != nil {
		return "", err
	}

	err = cf.createApp(app.Name, app.Manifest)
	if err != nil {
		return "", err
	}

	return cf.getRoute(app.Name)
}

func (cf *CloudFoundry) Delete(space string) error {
	return cf.deleteSpace(space)
}

func (cf *CloudFoundry) createSpace(space string) error {
	args := []string{"create-space", space}
	err := cf.cf(args...).Run()
	if err != nil {
		return err
	}

	args = []string{"target", "-s", space}
	return cf.cf(args...).Run()
}

func (cf *CloudFoundry) deleteSpace(space string) error {
	args := []string{"delete-space", space, "-f"}
	return cf.cf(args...).Run()
}

func (cf *CloudFoundry) createServices(app models.App) error {
	for _, service := range app.Services {
		err := cf.createService(service)
		if err != nil {
			return err
		}
	}
	return nil
}

func (cf *CloudFoundry) getOrg(orgID string) (string, error) {
	buf := bytes.Buffer{}
	cmd := cf.cf("curl", fmt.Sprintf("/v2/organizations/%s", orgID))
	cmd.Stdout = io.MultiWriter(os.Stdout, &buf)
	err := cmd.Run()
	if err != nil {
		return "", err
	}

	resource := resources.OrganizationResource{}
	err = json.Unmarshal(buf.Bytes(), &resource)
	return resource.Entity.Name, err
}

func (cf *CloudFoundry) createService(service models.Service) error {
	args := []string{"create-service", service.Service, service.Plan, service.Name}
	if len(service.Tags) > 0 {
		args = append(args, "-t", strings.Join(service.Tags, ","))
	}
	if len(service.Config) > 0 {
		config, err := json.Marshal(service.Config)
		if err != nil {
			return err
		}
		args = append(args, "-c", string(config))
	}

	err := cf.cf(args...).Run()
	if err != nil {
		return err
	}

	return cf.checkService(service, 30)
}

func (cf *CloudFoundry) checkService(service models.Service, timeout int) error {
	args := []string{"service", service.Name}
	elapsed := 0

	for {
		buf := bytes.Buffer{}
		cmd := cf.cf(args...)
		cmd.Stdout = io.MultiWriter(os.Stdout, &buf)
		err := cmd.Run()

		if err == nil {
			output := buf.String()
			for _, line := range strings.Split(output, "\n") {
				if line == "Status: create succeeded" {
					return nil
				}
			}
		}

		elapsed += 5
		if elapsed > timeout {
			return fmt.Errorf("Service %s incomplete", service.Name)
		}

		time.Sleep(5 * time.Second)
	}
}

func (cf *CloudFoundry) createApp(app, manifest string) error {
	args := []string{"push", app, "-f", manifest}
	return cf.cf(args...).Run()
}

func (cf *CloudFoundry) cf(args ...string) *exec.Cmd {
	cmd := exec.Command("cf", args...)

	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	// TODO: Handle per-pull CF_HOME
	cmd.Env = append(os.Environ(), "CF_COLOR=true")

	return cmd
}

func (cf *CloudFoundry) getRoute(name string) (string, error) {
	buf := bytes.Buffer{}
	cmd := cf.cf("app", name)
	cmd.Stdout = io.MultiWriter(os.Stdout, &buf)
	err := cmd.Run()
	if err != nil {
		return "", err
	}

	output := strings.Split(buf.String(), "\n")
	for _, line := range output {
		if strings.Index(line, "urls: ") == 0 {
			return strings.Replace(line, "urls: ", "", 1), nil
		}
	}

	return "", fmt.Errorf("No URL found for app %s", name)
}
