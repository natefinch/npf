// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore

import (
	"gopkg.in/errgo.v1"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/internal/mongodoc"
)

// migrations holds all the migration functions that are executed in the order
// they are defined when the charm store server is started.
// To introduce a new database migration, just add the corresponding migration
// function to this list.
var migrations = []func(db StoreDatabase) error{
	denormalizeEntityIds,
}

// Migrate starts the migration process using the given database.
func migrate(db StoreDatabase) error {
	for _, f := range migrations {
		if err := f(db); err != nil {
			return errgo.Mask(err)
		}
	}
	return nil
}

// denormalizeEntityIds adds the user, name, revision and series fields to
// entities where those fields are missing.
// This function is not supposed to be called directly.
func denormalizeEntityIds(db StoreDatabase) error {
	logger.Debugf("starting entity ids migration")
	entities := db.Entities()
	var entity mongodoc.Entity
	iter := entities.Find(bson.D{{
		// Use the name field to collect not migrated entities.
		"name", bson.D{{"$exists", false}},
	}}).Select(bson.D{{"_id", 1}}).Iter()

	for iter.Next(&entity) {
		logger.Debugf("updating %s", entity.URL)
		if err := entities.UpdateId(entity.URL, bson.D{{
			"$set", bson.D{
				{"user", entity.URL.User},
				{"name", entity.URL.Name},
				{"revision", entity.URL.Revision},
				{"series", entity.URL.Series},
			},
		}}); err != nil {
			return errgo.Notef(err, "cannot denormalize entity id %s", entity.URL)
		}
	}
	if err := iter.Close(); err != nil {
		return errgo.Notef(err, "cannot denormalize entity ids")
	}

	logger.Debugf("entity ids migration successfully completed")
	return nil
}
