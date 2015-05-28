// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// The config package defines configuration parameters for
// the charm store.
package config // import "gopkg.in/juju/charmstore.v5-unstable/config"

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/yaml.v2"
)

type Config struct {
	// TODO(rog) rename this to MongoAddr - it's not a URL.
	MongoURL          string            `yaml:"mongo-url"`
	APIAddr           string            `yaml:"api-addr"`
	AuthUsername      string            `yaml:"auth-username"`
	AuthPassword      string            `yaml:"auth-password"`
	ESAddr            string            `yaml:"elasticsearch-addr"` // elasticsearch is optional
	IdentityPublicKey *bakery.PublicKey `yaml:"identity-public-key"`
	IdentityLocation  string            `yaml:"identity-location"`
	// The identity API is optional
	IdentityAPIURL string          `yaml:"identity-api-url"`
	AgentUsername  string          `yaml:"agent-username"`
	AgentKey       *bakery.KeyPair `yaml:"agent-key"`
}

func (c *Config) validate() error {
	var missing []string
	if c.MongoURL == "" {
		missing = append(missing, "mongo-url")
	}
	if c.APIAddr == "" {
		missing = append(missing, "api-addr")
	}
	if c.AuthUsername == "" {
		missing = append(missing, "auth-username")
	}
	if strings.Contains(c.AuthUsername, ":") {
		return fmt.Errorf("invalid user name %q (contains ':')", c.AuthUsername)
	}
	if c.AuthPassword == "" {
		missing = append(missing, "auth-password")
	}
	if len(missing) != 0 {
		return fmt.Errorf("missing fields %s in config file", strings.Join(missing, ", "))
	}
	return nil
}

// Read reads a charm store configuration file from the
// given path.
func Read(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errgo.Notef(err, "cannot open config file")
	}
	defer f.Close()
	data, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, errgo.Notef(err, "cannot read %q", path)
	}
	var conf Config
	err = yaml.Unmarshal(data, &conf)
	if err != nil {
		return nil, errgo.Notef(err, "cannot parse %q", path)
	}
	if err := conf.validate(); err != nil {
		return nil, errgo.Mask(err)
	}
	return &conf, nil
}
