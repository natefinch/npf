// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/mgo.v2"

	"github.com/juju/charmstore/config"
	"github.com/juju/charmstore/internal/charmstore"
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
	if err := populate(flag.Arg(0)); err != nil {
		fmt.Fprintf(os.Stderr, "cannot populate elastic search: %v", err)
		os.Exit(1)
	}
}

func populate(confPath string) error {
	conf, err := config.Read(confPath)
	if err != nil {
		return err
	}
	if conf.ESAddr == "" {
		return fmt.Errorf("no elasticsearch-addr specified in %s", confPath)
	}
	es := &elasticsearch.Database{conf.ESAddr}
	session, err := mgo.Dial(conf.MongoURL)
	if err != nil {
		return err
	}
	defer session.Close()
	db := session.DB("juju")
	store, err := charmstore.NewStore(db, es)
	if err != nil {
		return err
	}
	return store.ExportToElasticSearch()
}
