// Copyright 2012, 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"gopkg.in/mgo.v2"

	"github.com/juju/charmstore"
	"github.com/juju/charmstore/config"
	"github.com/juju/charmstore/internal/debug"
	"github.com/juju/charmstore/internal/elasticsearch"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s <config path>\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
		os.Exit(2)
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
	}
	if err := serve(flag.Arg(0)); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func serve(confPath string) error {
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
	var es *elasticsearch.Index
	if conf.ESAddr != "" {
		es = (&elasticsearch.Database{conf.ESAddr}).Index("charmstore")
	}
	cfg := charmstore.ServerParams{
		AuthUsername: conf.AuthUsername,
		AuthPassword: conf.AuthPassword,
	}
	server, err := charmstore.NewServer(db, es, cfg, charmstore.Legacy, charmstore.V4)
	if err != nil {
		return err
	}
	return http.ListenAndServe(conf.APIAddr, debug.Handler("", server))
}
