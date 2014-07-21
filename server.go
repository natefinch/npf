// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore

import (
	"fmt"
	"net/http"
	"sort"

	"labix.org/v2/mgo"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/v4"
)

// Versions of the API that can be served.
const (
	V4 = "v4"
)

var versions = map[string]func(*charmstore.Store) http.Handler{
	V4: v4.New,
}

// Versions returns all known API versions.
func Versions() []string {
	vs := make([]string, 0, len(versions))
	for v := range versions {
		vs = append(vs, v)
	}
	sort.Strings(vs)
	return vs
}

// NewServer returns a new handler that handles
// charm store requests and stores its data in the given database.
func NewServer(db *mgo.Database, serveVersions ...string) (http.Handler, error) {
	newAPIs := make(map[string]charmstore.NewAPIHandler)
	for _, vers := range serveVersions {
		newAPI := versions[vers]
		if newAPI == nil {
			return nil, fmt.Errorf("unknown version %q", vers)
		}
		newAPIs[vers] = newAPI
	}

	return charmstore.NewServer(db, newAPIs)
}
