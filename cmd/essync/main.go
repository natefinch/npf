// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/juju/loggo"
	"gopkg.in/errgo.v1"
	"gopkg.in/mgo.v2"

	"github.com/juju/charmstore/config"
	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/elasticsearch"
)

var logger = loggo.GetLogger("essync")

var (
	index         = flag.String("index", "charmstore", "Name of index to populate.")
	loggingConfig = flag.String("logging-config", "", "specify log levels for modules e.g. <root>=TRACE")
	mapping       = flag.String("mapping", "", "File to use to configure the entity mapping.")
	settings      = flag.String("settings", "", "File to use to configure the index.")
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [options] <config path>\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
		os.Exit(2)
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
	}
	if *loggingConfig != "" {
		if err := loggo.ConfigureLoggers(*loggingConfig); err != nil {
			fmt.Fprintf(os.Stderr, "cannot configure loggers: %v", err)
			os.Exit(1)
		}
	}
	if err := populate(flag.Arg(0)); err != nil {
		logger.Errorf("cannot populate elasticsearch: %v", err)
		os.Exit(1)
	}
}

func populate(confPath string) error {
	logger.Debugf("reading config file %q", confPath)
	conf, err := config.Read(confPath)
	if err != nil {
		return errgo.Notef(err, "cannot read config file %q", confPath)
	}
	if conf.ESAddr == "" {
		return errgo.Newf("no elasticsearch-addr specified in config file %q", confPath)
	}
	es := &elasticsearch.Database{conf.ESAddr}
	session, err := mgo.Dial(conf.MongoURL)
	if err != nil {
		return errgo.Notef(err, "cannot dial mongo at %q", conf.MongoURL)
	}
	defer session.Close()
	db := session.DB("juju")
	store, err := charmstore.NewStore(db, &charmstore.StoreElasticSearch{es.Index(*index)})
	if err != nil {
		return errgo.Notef(err, "unable to create store for ESSync")
	}
	if *settings != "" {
		if err := writeSettings(es, *index, *settings); err != nil {
			return err
		}
	}
	if *mapping != "" {
		err = writeMapping(es, *index, "entity", *mapping)
		if err != nil {
			return err
		}
	}
	logger.Debugf("starting export to Elastic Search")
	return store.ExportToElasticSearch()
}

// writeSetttings creates a new index with the settings loaded from path. An error
// will be returned from elasticsearch if the index already exists.
func writeSettings(es *elasticsearch.Database, index, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return errgo.Notef(err, "cannot read index settings")
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	var data map[string]interface{}
	if err := dec.Decode(&data); err != nil {
		return errgo.Notef(err, "cannot read index settings")
	}
	if err := es.PutIndex(index, data); err != nil {
		return errgo.Notef(err, "cannot set index settings")
	}
	return nil
}

// writeMapping writes the mapping loaded from path as documentType on index.
func writeMapping(es *elasticsearch.Database, index, documentType, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return errgo.Notef(err, "cannot read %s mapping", documentType)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	var data map[string]interface{}
	if err := dec.Decode(&data); err != nil {
		return errgo.Notef(err, "cannot read %s mapping", documentType)
	}
	if err := es.PutMapping(index, documentType, data); err != nil {
		return errgo.Notef(err, "cannot set %s mapping", documentType)
	}
	return nil
}
