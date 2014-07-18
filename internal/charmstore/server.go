// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// This is the internal version of the charmstore package.
// It exposes details to the various API packages
// that we do not wish to expose to the world at large.
package charmstore

import (
	"fmt"
	"net/http"
	"sync"

	"labix.org/v2/mgo"
)

var (
	versionMutex sync.Mutex
	versions     = make(map[string]func(*Store) http.Handler)
)

// RegisterAPIVersion registers a version of the API so
// that it can be used with NewServer.
// The newAPI function will be called with the
// charm store's Store to create the handler for
// the API version, which will be served under
// the /<version> path.
//
// The URL paths of requests made to the API's handler
// will have their initial /<version> prefix stripped off.
func RegisterAPIVersion(version string, newAPI func(s *Store) http.Handler) {
	versionMutex.Lock()
	defer versionMutex.Unlock()
	versions[version] = newAPI
}

// NewServer returns a handler that serves the given charm store API
// versions using db to store that charm store data.
func NewServer(db *mgo.Database, serveVersions ...string) (http.Handler, error) {
	if len(serveVersions) == 0 {
		return nil, fmt.Errorf("charm store server must serve at least one version of the API")
	}
	store := newStore(db)
	mux := http.NewServeMux()
	for _, version := range serveVersions {
		newAPI, ok := versions[version]
		if !ok {
			return nil, fmt.Errorf("API version %q not registered", version)
		}
		handle(mux, "/"+version, newAPI(store))
	}

	return mux, nil
}

func handle(mux *http.ServeMux, path string, handler http.Handler) {
	mux.Handle(path+"/", http.StripPrefix(path, handler))
}
