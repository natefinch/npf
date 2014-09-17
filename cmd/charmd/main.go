// Copyright 2012, 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"gopkg.in/mgo.v2"

	"github.com/juju/charmstore"
	"github.com/juju/charmstore/config"
	"github.com/juju/charmstore/internal/debug"
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
	conf, err := config.Read(confPath)
	if err != nil {
		return err
	}
	session, err := mgo.Dial(conf.MongoURL)
	if err != nil {
		return err
	}
	defer session.Close()
	db := session.DB("juju")
	cfg := charmstore.ServerParams{
		AuthUsername: conf.AuthUsername,
		AuthPassword: conf.AuthPassword,
	}
	server, err := charmstore.NewServer(db, cfg, charmstore.V4)
	if err != nil {
		return err
	}
	return http.ListenAndServe(conf.APIAddr, debug.Handler("", server))
}
