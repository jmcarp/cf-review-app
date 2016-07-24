package utils

import (
	"fmt"
	"io/ioutil"

	"github.com/cloudfoundry/cli/cf/manifest"
	"github.com/cloudfoundry/cli/utils/generic"
	"gopkg.in/yaml.v2"
)

// MakeManifest finds an app in an existing manifest and prepares it as a review
// app, setting `random-route` and unsetting route-related options.
func MakeManifest(name, source, dest string) error {
	notFound := fmt.Errorf("Application %s not found", name)

	repository := manifest.NewDiskRepository()
	manifest, err := repository.ReadManifest(source)
	if err != nil {
		return err
	}

	if !manifest.Data.Has("applications") {
		return notFound
	}
	apps, ok := manifest.Data.Get("applications").([]interface{})
	if !ok {
		return notFound
	}

	globals := manifest.Data.Except([]interface{}{"applications"})

	for _, app := range apps {
		if !generic.IsMappable(app) {
			continue
		}

		appMap := generic.NewMap(app)
		if !appMap.Has("name") {
			continue
		}

		appName, ok := appMap.Get("name").(string)
		if ok && appName == name {
			merged := generic.DeepMerge(globals, appMap)
			merged.Set("random-route", true)
			for _, key := range []string{"host", "hosts", "domain", "domains"} {
				merged.Delete(key)
			}
			return writeManifest(dest, merged)
		}
	}

	return notFound
}

func writeManifest(path string, manifest generic.Map) error {
	raw, err := yaml.Marshal(manifest)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, raw, 0644)
}
