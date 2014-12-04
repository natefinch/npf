// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"gopkg.in/errgo.v1"
	"gopkg.in/yaml.v1"

	"github.com/juju/charmstore/params"
)

type VersionInfo struct {
	Version string `yaml:"version"`
	Id      string `yaml:"id"`
}

// GET /debug/info .
func (h *Handler) serveDebugInfo(_ http.Header, req *http.Request) (interface{}, error) {
	debugInfo := params.DebugInfo{}
	version, err := ReadVersion("cmd/charmd/version.yaml")
	if err != nil {
		logger.Infof("cannot read version file: %v", err)
		debugInfo.Version = "Unknown"
		debugInfo.Id = "Unknown"
	} else {
		debugInfo.Version = version.Version
		debugInfo.Id = version.Id
	}
	return debugInfo, nil
}

// Read reads a version file from the given path.
func ReadVersion(path string) (*VersionInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		a := errgo.Notef(err, "cannot open version file")
		return nil, a
	}
	defer f.Close()
	data, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, errgo.Notef(err, "cannot read %q", path)
	}
	var version VersionInfo
	err = yaml.Unmarshal(data, &version)
	if err != nil {
		return nil, errgo.Notef(err, "cannot parse %q", path)
	}
	if err := version.validate(); err != nil {
		return nil, errgo.Mask(err)
	}
	return &version, nil
}

func (v *VersionInfo) validate() (error) {
	var missing []string
	if v.Version == "" {
		missing = append(missing, "version")
	}
	if v.Id == "" {
		missing = append(missing, "id")
	}
	if len(missing) != 0 {
		return fmt.Errorf("missing fields %s in version file", strings.Join(missing, ", "))
	}
	return nil
}
