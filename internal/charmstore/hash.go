// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// TODO frankban: remove this file after updating entities in the production db
// with their SHA256 hash value. Entities are updated by running the cshash256
// command.

package charmstore	// import "gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"

import (
	"crypto/sha256"
	"fmt"
	"io"

	"gopkg.in/errgo.v1"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
)

// UpdateEntitySHA256 calculates and return the SHA256 hash of the archive of
// the given entity id. The entity document is then asynchronously updated with
// the resulting hash. This method will be removed soon.
func (s *Store) UpdateEntitySHA256(id *router.ResolvedURL) (string, error) {
	r, _, _, err := s.OpenBlob(id)
	defer r.Close()
	hash := sha256.New()
	_, err = io.Copy(hash, r)
	if err != nil {
		return "", errgo.Notef(err, "cannot calculate sha256 of archive")
	}
	sum256 := fmt.Sprintf("%x", hash.Sum(nil))

	// Update the entry asynchronously because it doesn't matter if it succeeds
	// or fails, or if several instances of the charm store do it concurrently,
	// and it doesn't need to be on the critical path for API endpoints.
	s.Go(func(s *Store) {
		UpdateEntitySHA256(s, id, sum256)
	})

	return sum256, nil
}

// UpdateEntitySHA256 updates the BlobHash256 entry for the entity.
// It is defined as a variable so that it can be mocked in tests.
// This function will be removed soon.
var UpdateEntitySHA256 = func(store *Store, id *router.ResolvedURL, sum256 string) {
	err := store.DB.Entities().UpdateId(&id.URL, bson.D{{"$set", bson.D{{"blobhash256", sum256}}}})
	if err != nil && err != mgo.ErrNotFound {
		logger.Errorf("cannot update sha256 of archive: %v", err)
	}
}
