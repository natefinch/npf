// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore

import (
	"fmt"
	"net/http"
	"sort"

	"gopkg.in/mgo.v2"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/v4"
	"github.com/juju/charmstore/params"
)

// Versions of the API that can be served.
const (
	V4 = "v4"
)

var versions = map[string]charmstore.NewAPIHandler{
	V4: v4.New,
}

// Versions returns all known API version strings in alphabetical order.
func Versions() []string {
	vs := make([]string, 0, len(versions))
	for v := range versions {
		vs = append(vs, v)
	}
	sort.Strings(vs)
	return vs
}

// NewServer returns a new handler that handles charm store requests and stores
// its data in the given database. The handler will serve the specified
// versions of the API using the given configuration.
func NewServer(db *mgo.Database, config *params.HandlerConfig, serveVersions ...string) (http.Handler, error) {
	newAPIs := make(map[string]charmstore.NewAPIHandler)
	for _, vers := range serveVersions {
		newAPI := versions[vers]
		if newAPI == nil {
			return nil, fmt.Errorf("unknown version %q", vers)
		}
		newAPIs[vers] = newAPI
	}

	return charmstore.NewServer(db, config, newAPIs)
}
