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
	"github.com/juju/errgo"
	"github.com/juju/loggo"
)

var logger = loggo.GetLogger("essync")
var failsLogger = loggo.GetLogger("charmload_v4.loadfails")

var (
	index         = flag.String("index", "charmstore", "Name of index to populate.")
	loggingConfig = flag.String("logging-config", "", "specify log levels for modules e.g. <root>=TRACE")
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
		logger.Errorf("cannot populate elasticsearch: %v", err)
		os.Exit(1)
	}
}

func populate(confPath string) error {
	if *loggingConfig != "" {
		if err := loggo.ConfigureLoggers(*loggingConfig); err != nil {
			return errgo.Notef(err, "cannot configure loggers")
		}
	}
	conf, err := config.Read(confPath)
	if err != nil {
		return errgo.Notef(err, "cannot read config: %s", confPath)
	}
	if conf.ESAddr == "" {
		return errgo.Newf("no elasticsearch-addr specified in %s", confPath)
	}
	es := &elasticsearch.Database{conf.ESAddr}

	if conf.MongoURL == "" {
		return errgo.Newf("no mongodb-addr specified in %s", confPath)
	}
	logger.Infof("config: %#v", conf)

	session, err := mgo.Dial(conf.MongoURL)
	if err != nil {
		return errgo.Notef(err, "cannot dial mongo at: %s", conf.MongoURL)
	}
	defer session.Close()
	db := session.DB("juju")
	store, err := charmstore.NewStore(db, &charmstore.StoreElasticSearch{es, *index})
	if err != nil {
		return errgo.Notef(err, "unable to create store for ESSync")
	}
	logger.Infof("start export to Elastic Search")
	return store.ExportToElasticSearch()
}
