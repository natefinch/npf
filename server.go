// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore

import (
	"net/http"

	"labix.org/v2/mgo"

	"github.com/juju/charmstore/internal/charmstore"
)

// NewServer returns a new handler that handles
// charm store requests and stores its data in the given database.
func NewServer(db *mgo.Database, versions ...string) (http.Handler, error) {
	return charmstore.NewServer(db, versions...)
}
