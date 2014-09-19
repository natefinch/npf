// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/juju/errgo"
	"github.com/juju/loggo"
	"launchpad.net/lpad"

	"github.com/juju/charmstore/config"
	"github.com/juju/charmstore/lppublish"
)

var logger = loggo.GetLogger("charmload")
var failsLogger = loggo.GetLogger("charmload_v4.loadfails")

var (
	staging       = flag.Bool("staging", false, "use the launchpad staging server")
	storeAddr     = flag.String("storeaddr", "localhost:8080", "the address of the charmstore; overrides configuration file")
	loggingConfig = flag.String("logging-config", "", "specify log levels for modules e.g. <root>=TRACE")
	storeUser     = flag.String("u", "", "the colon separated user:password for charmstore; overrides configuration file")
	configPath    = flag.String("config", "", "path to charm store configuration file")
	limit         = flag.Int("limit", 0, "limit the number of charms/bundles to process")
	numPublishers = flag.Int("p", 10, "the number of publishers that can be run in parallel")
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: charmload [flags]\n")
		flag.PrintDefaults()
		os.Exit(2)
	}
	flag.Parse()
	if err := load(); err != nil {
		fmt.Fprintf(os.Stderr, "charmload: %v\n", err)
		os.Exit(1)
	}
}

func load() error {
	if *loggingConfig != "" {
		if err := loggo.ConfigureLoggers(*loggingConfig); err != nil {
			return errgo.Notef(err, "cannot configure loggers")
		}
	}
	writer, err := os.OpenFile("charmload.err", os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0666)
	if err != nil {
		return err
	}
	defer writer.Close()
	registerLoadFailsWriter(writer)
	var params lppublish.Params

	params.LaunchpadServer = lpad.Production
	if *staging {
		params.LaunchpadServer = lpad.Staging
	}

	// Validate the command line arguments.
	if *limit < 0 {
		return errgo.Newf("invalid limit %d", *limit)
	}
	params.Limit = *limit
	if *numPublishers < 1 {
		return errgo.Newf("invalid number of publishers %d", *numPublishers)
	}
	params.NumPublishers = *numPublishers

	var cfg *config.Config
	if *configPath != "" {
		var err error
		cfg, err = config.Read(*configPath)
		if err != nil {
			return errgo.Notef(err, "cannot read config file")
		}
		logger.Infof("config: %#v", cfg)
	}
	if *storeUser != "" {
		parts := strings.SplitN(*storeUser, ":", 2)
		if len(parts) != 2 || len(parts[0]) == 0 {
			return errgo.Newf("invalid user name:password %q", *storeUser)
		}
		params.StoreUser, params.StorePassword = parts[0], parts[1]
	} else if cfg != nil {
		params.StoreUser, params.StorePassword = cfg.AuthUsername, cfg.AuthPassword
	}
	if *storeAddr == "" {
		*storeAddr = cfg.APIAddr
	}

	params.StoreURL = "http://" + *storeAddr + "/v4/"

	// Start the ingestion.
	if err := lppublish.PublishCharmsDistro(params); err != nil {
		return errgo.Mask(err)
	}
	return nil
}

func registerLoadFailsWriter(writer io.Writer) error {
	loadFailsWriter := &LoadFailsWriter{writer}
	err := loggo.RegisterWriter("loadfails", loadFailsWriter, loggo.TRACE)
	if err != nil {
		return err
	}
	return err
}

type LoadFailsWriter struct {
	w io.Writer
}

func (writer *LoadFailsWriter) Write(level loggo.Level, name, filename string, line int, timestamp time.Time, message string) {
	if name == failsLogger.Name() {
		logLine := (&loggo.DefaultFormatter{}).Format(level, name, filename, line, timestamp, message)
		fmt.Fprintf(writer.w, "%s\n", logLine)
	}
}
