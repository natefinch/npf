// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// TODO frankban: remove this package after updating entities in the production
// db with their SHA256 hash value. Entities are updated by running the
// cshash256 command.

package hashtesting

import (
	"time"

	jujutesting "github.com/juju/testing"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v5-unstable"
	"gopkg.in/mgo.v2/bson"

	"gopkg.in/juju/charmstore.v4/internal/charmstore"
	"gopkg.in/juju/charmstore.v4/internal/router"
)

func CheckSHA256Laziness(c *gc.C, store *charmstore.Store, id *charm.Reference, check func()) {
	updated := make(chan struct{}, 1)

	// Patch charmstore.UpdateEntitySHA256 so that we can know whether it has
	// been called or not.
	original := charmstore.UpdateEntitySHA256
	restore := jujutesting.PatchValue(
		&charmstore.UpdateEntitySHA256,
		func(store *charmstore.Store, id *router.ResolvedURL, sum256 string) {
			original(store, id, sum256)
			updated <- struct{}{}
		})
	defer restore()

	// Update the entity removing the SHA256 hash.
	store.DB.Entities().UpdateId(id, bson.D{{
		"$set", bson.D{{"blobhash256", ""}},
	}})

	// Run the code under test.
	check()

	// Ensure the db is updated asynchronously.
	select {
	case <-updated:
	case <-time.After(5 * time.Second):
		c.Fatalf("timed out waiting for update")
	}

	// Run the code under test. again.
	check()

	// We should not update the SHA256 the second time.
	select {
	case <-updated:
		c.Fatalf("update called twice")
	case <-time.After(10 * time.Millisecond):
	}
}
