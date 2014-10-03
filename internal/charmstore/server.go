// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// This is the internal version of the charmstore package.
// It exposes details to the various API packages
// that we do not wish to expose to the world at large.
package charmstore

import (
	"net/http"

	"github.com/juju/errgo"
	"gopkg.in/mgo.v2"

	"github.com/juju/charmstore/internal/elasticsearch"
	"github.com/juju/charmstore/internal/router"
)

// NewAPIHandlerFunc is a function that returns a new API handler that uses
// the given Store.
type NewAPIHandlerFunc func(*Store, ServerParams) http.Handler

// ServerParams holds configuration for a new internal API server.
type ServerParams struct {
	AuthUsername string
	AuthPassword string
}

// NewServer returns a handler that serves the given charm store API
// versions using db to store that charm store data.
// The key of the versions map is the version name.
// The handler configuration is provided to all version handlers.
func NewServer(db *mgo.Database, es *elasticsearch.Database, config ServerParams, versions map[string]NewAPIHandlerFunc) (http.Handler, error) {
	if len(versions) == 0 {
		return nil, errgo.Newf("charm store server must serve at least one version of the API")
	}
	store, err := NewStore(db, es)
	if err != nil {
		return nil, errgo.Notef(err, "cannot make store")
	}
	mux := router.NewServeMux()
	for vers, newAPI := range versions {
		handle(mux, "/"+vers, newAPI(store, config))
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
