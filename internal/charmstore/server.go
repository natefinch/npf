// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// This is the internal version of the charmstore package.
// It exposes details to the various API packages
// that we do not wish to expose to the world at large.
package charmstore // import "gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"

import (
	"net/http"
	"strings"

	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/mgo.v2"

	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
)

// NewAPIHandlerFunc is a function that returns a new API handler that uses
// the given Store.
type NewAPIHandlerFunc func(*Pool, ServerParams) http.Handler

// ServerParams holds configuration for a new internal API server.
type ServerParams struct {
	// AuthUsername and AuthPassword hold the credentials
	// used for HTTP basic authentication.
	AuthUsername string
	AuthPassword string

	// IdentityLocation holds the location of the third party authorization
	// service to use when creating third party caveats,
	// for example: http://api.jujucharms.com/identity/v1/discharger
	// If it is empty, IdentityURL+"/v1/discharger" will be used.
	IdentityLocation string

	// PublicKeyLocator holds a public key store.
	// It may be nil.
	PublicKeyLocator bakery.PublicKeyLocator

	// IdentityAPIURL holds the URL of the identity manager,
	// for example http://api.jujucharms.com/identity
	IdentityAPIURL string

	// IdentityAPIUsername and IdentityAPIPassword hold the credentials
	// to be used when querying the identity manager API.
	IdentityAPIUsername string
	IdentityAPIPassword string
}

// NewServer returns a handler that serves the given charm store API
// versions using db to store that charm store data.
// An optional elasticsearch configuration can be specified in si. If
// elasticsearch is not being used then si can be set to nil.
// The key of the versions map is the version name.
// The handler configuration is provided to all version handlers.
func NewServer(db *mgo.Database, si *SearchIndex, config ServerParams, versions map[string]NewAPIHandlerFunc) (http.Handler, error) {
	if len(versions) == 0 {
		return nil, errgo.Newf("charm store server must serve at least one version of the API")
	}
	config.IdentityLocation = strings.Trim(config.IdentityLocation, "/")
	config.IdentityAPIURL = strings.Trim(config.IdentityAPIURL, "/")
	if config.IdentityLocation == "" && config.IdentityAPIURL != "" {
		config.IdentityLocation = config.IdentityAPIURL + "/v1/discharger"
	}
	logger.Infof("identity discharge location: %s", config.IdentityLocation)
	logger.Infof("identity API location: %s", config.IdentityAPIURL)
	bparams := bakery.NewServiceParams{
		// TODO The location is attached to any macaroons that we
		// mint. Currently we don't know the location of the current
		// service. We potentially provide a way to configure this,
		// but it probably doesn't matter, as nothing currently uses
		// the macaroon location for anything.
		Location: "charmstore",
		Locator:  config.PublicKeyLocator,
	}
	pool, err := NewPool(db, si, &bparams)
	if err != nil {
		return nil, errgo.Notef(err, "cannot make store")
	}
	store := pool.Store()
	defer store.Close()
	if err := migrate(store.DB); err != nil {
		return nil, errgo.Notef(err, "database migration failed")
	}
	store.Go(func(store *Store) {
		if err := store.syncSearch(); err != nil {
			logger.Errorf("Cannot populate elasticsearch: %v", err)
		}
	})
	mux := router.NewServeMux()
	// Version independent API.
	handle(mux, "/debug", newServiceDebugHandler(pool, config, mux))
	for vers, newAPI := range versions {
		handle(mux, "/"+vers, newAPI(pool, config))
	}
	return mux, nil
}

func handle(mux *router.ServeMux, path string, handler http.Handler) {
	if path != "/" {
		handler = http.StripPrefix(path, handler)
		path += "/"
	}
	mux.Handle(path, handler)
}
