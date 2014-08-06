// Copyright 2012, 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"

	"gopkg.in/mgo.v2"
	"launchpad.net/goyaml"

	"github.com/juju/charmstore"
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
	if conf.MongoURL == "" || conf.APIAddr == "" {
		return fmt.Errorf("missing mongo-url or api-addr in config file")
	}
	session, err := mgo.Dial(conf.MongoURL)
	if err != nil {
		return err
	}
	defer session.Close()
	db := session.DB("juju")
	server, err := charmstore.NewServer(db, charmstore.V4)
	if err != nil {
		return err
	}
	return http.ListenAndServe(conf.APIAddr, server)
}

type config struct {
	MongoURL string `yaml:"mongo-url"`
	APIAddr  string `yaml:"api-addr"`
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
	err = goyaml.Unmarshal(data, &conf)
	if err != nil {
		return nil, fmt.Errorf("processing config file: %v", err)
	}
	return &conf, nil
}
