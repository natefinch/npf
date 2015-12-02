// Copyright 2012, 2013, 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main // import "gopkg.in/juju/charmstore.v5-unstable/cmd/charmd"

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/juju/loggo"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/httpbakery"
	"gopkg.in/mgo.v2"
	"gopkg.in/natefinch/lumberjack.v2"

	"gopkg.in/juju/charmstore.v5-unstable"
	"gopkg.in/juju/charmstore.v5-unstable/config"
	"gopkg.in/juju/charmstore.v5-unstable/elasticsearch"
	"gopkg.in/juju/charmstore.v5-unstable/internal/debug"
)

var (
	logger        = loggo.GetLogger("charmd")
	loggingConfig = flag.String("logging-config", "", "specify log levels for modules e.g. <root>=TRACE")
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
	if err := serve(flag.Arg(0)); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func serve(confPath string) error {
	logger.Infof("reading configuration")
	conf, err := config.Read(confPath)
	if err != nil {
		return errgo.Notef(err, "cannot read config file %q", confPath)
	}

	logger.Infof("connecting to mongo")
	session, err := mgo.Dial(conf.MongoURL)
	if err != nil {
		return errgo.Notef(err, "cannot dial mongo at %q", conf.MongoURL)
	}
	defer session.Close()
	db := session.DB("juju")

	var es *elasticsearch.Database
	if conf.ESAddr != "" {
		es = &elasticsearch.Database{
			conf.ESAddr,
		}
	}

	logger.Infof("setting up the API server")
	cfg := charmstore.ServerParams{
		AuthUsername:     conf.AuthUsername,
		AuthPassword:     conf.AuthPassword,
		IdentityLocation: conf.IdentityLocation,
		IdentityAPIURL:   conf.IdentityAPIURL,
		TermsLocation:    conf.TermsLocation,

		AgentUsername:           conf.AgentUsername,
		AgentKey:                conf.AgentKey,
		StatsCacheMaxAge:        conf.StatsCacheMaxAge.Duration,
		MaxMgoSessions:          conf.MaxMgoSessions,
		HTTPRequestWaitDuration: conf.RequestTimeout.Duration,
		SearchCacheMaxAge:       conf.SearchCacheMaxAge.Duration,
	}

	if conf.AuditLogFile != "" {
		cfg.AuditLogger = &lumberjack.Logger{
			Filename: conf.AuditLogFile,
			MaxSize:  conf.AuditLogMaxSize,
			MaxAge:   conf.AuditLogMaxAge,
		}
	}

	ring := bakery.NewPublicKeyRing()
	if conf.IdentityPublicKey != nil {
		err = ring.AddPublicKeyForLocation(cfg.IdentityLocation, false, conf.IdentityPublicKey)
		if err != nil {
			return errgo.Mask(err)
		}
	} else {
		pubKey, err := httpbakery.PublicKeyForLocation(http.DefaultClient, cfg.IdentityLocation)
		if err != nil {
			return errgo.Mask(err)
		}
		err = ring.AddPublicKeyForLocation(cfg.IdentityLocation, false, pubKey)
		if err != nil {
			return errgo.Mask(err)
		}
	}
	if conf.TermsPublicKey != nil {
		ring.AddPublicKeyForLocation(cfg.TermsLocation, false, conf.TermsPublicKey)
	} else if cfg.TermsLocation != "" {
		pubKey, err := httpbakery.PublicKeyForLocation(http.DefaultClient, cfg.TermsLocation)
		if err != nil {
			return errgo.Mask(err)
		}
		err = ring.AddPublicKeyForLocation(cfg.TermsLocation, false, pubKey)
		if err != nil {
			return errgo.Mask(err)
		}
	}

	cfg.PublicKeyLocator = ring
	server, err := charmstore.NewServer(db, es, "cs", cfg, charmstore.Legacy, charmstore.V4)
	if err != nil {
		return errgo.Notef(err, "cannot create new server at %q", conf.APIAddr)
	}

	logger.Infof("starting the API server")
	return http.ListenAndServe(conf.APIAddr, debug.Handler("", server))
}

var mgoLogger = loggo.GetLogger("mgo")

func init() {
	mgo.SetLogger(mgoLog{})
}

type mgoLog struct{}

func (mgoLog) Output(calldepth int, s string) error {
	mgoLogger.LogCallf(calldepth+1, loggo.DEBUG, "%s", s)
	return nil
}
