// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore

import (
	"fmt"
	"net/http"
	"sort"

	"gopkg.in/mgo.v2"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/elasticsearch"
	"github.com/juju/charmstore/internal/legacy"
	"github.com/juju/charmstore/internal/v4"
)

// Versions of the API that can be served.
const (
	V4     = "v4"
	Legacy = ""
)

var versions = map[string]charmstore.NewAPIHandlerFunc{
	V4:     v4.NewAPIHandler,
	Legacy: legacy.NewAPIHandler,
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

// ServerParams holds configuration for a new API server.
type ServerParams struct {
	AuthUsername string
	AuthPassword string
}

// NewServer returns a new handler that handles charm store requests and stores
// its data in the given database. The handler will serve the specified
// versions of the API using the given configuration.
func NewServer(db *mgo.Database, es *elasticsearch.Database, idx string, config ServerParams, serveVersions ...string) (http.Handler, error) {
	newAPIs := make(map[string]charmstore.NewAPIHandlerFunc)
	for _, vers := range serveVersions {
		newAPI := versions[vers]
		if newAPI == nil {
			return nil, fmt.Errorf("unknown version %q", vers)
		}
		newAPIs[vers] = newAPI
	}
	var si *charmstore.SearchIndex
	if es != nil {
		si = &charmstore.SearchIndex{
			Database: es,
			Index:    idx,
		}
	}
	return charmstore.NewServer(db, si, charmstore.ServerParams(config), newAPIs)
}
