// Copyright 2012, 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/mgo.v2"
	"gopkg.in/yaml.v1"

	"github.com/juju/charmstore"
	"github.com/juju/charmstore/params"
)

func main() {
	err := serve()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func serve() error {
	var confPath string
	if len(os.Args) == 2 {
		if _, err := os.Stat(os.Args[1]); err == nil {
			confPath = os.Args[1]
		}
	}
	if confPath == "" {
		return fmt.Errorf("usage: %s <config path>", filepath.Base(os.Args[0]))
	}
	conf, err := readConfig(confPath)
	if err != nil {
		return err
	}
	if err := conf.validate(); err != nil {
		return err
	}
	session, err := mgo.Dial(conf.MongoURL)
	if err != nil {
		return err
	}
	defer session.Close()
	db := session.DB("juju")
	cfg := &params.HandlerConfig{
		AuthUsername: conf.AuthUsername,
		AuthPassword: conf.AuthPassword,
	}
	server, err := charmstore.NewServer(db, cfg, charmstore.V4)
	if err != nil {
		return err
	}
	return http.ListenAndServe(conf.APIAddr, server)
}

type config struct {
	MongoURL     string `yaml:"mongo-url"`
	APIAddr      string `yaml:"api-addr"`
	AuthUsername string `yaml:"auth-username"`
	AuthPassword string `yaml:"auth-password"`
}

func (c *config) validate() error {
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
	if c.AuthPassword == "" {
		missing = append(missing, "auth-password")
	}
	if len(missing) != 0 {
		return fmt.Errorf("missing fields %s in config file", strings.Join(missing, ", "))
	}
	return nil
}

func readConfig(path string) (*config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening config file: %v", err)
	}
	defer f.Close()
	data, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %v", err)
	}
	var conf config
	err = yaml.Unmarshal(data, &conf)
	if err != nil {
		return nil, fmt.Errorf("processing config file: %v", err)
	}
	return &conf, nil
}
