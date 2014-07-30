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
)

// NewAPIHandler returns a new API handler that
// uses the given Store.
type NewAPIHandler func(*Store) http.Handler

// NewServer returns a handler that serves the given charm store API
// versions using db to store that charm store data.
// The key of the versions map is the version name.
func NewServer(db *mgo.Database, versions map[string]NewAPIHandler) (http.Handler, error) {
	if len(versions) == 0 {
		return nil, errgo.Newf("charm store server must serve at least one version of the API")
	}
	store := NewStore(db)
	mux := http.NewServeMux()
	for vers, newAPI := range versions {
		handle(mux, "/"+vers, newAPI(store))
	}
	return mux, nil
}

func handle(mux *http.ServeMux, path string, handler http.Handler) {
	mux.Handle(path+"/", http.StripPrefix(path, handler))
}
