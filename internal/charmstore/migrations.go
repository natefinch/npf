// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore

import (
	"gopkg.in/errgo.v1"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/internal/mongodoc"
)

// migrations holds all the migration functions that are executed in the order
// they are defined when the charm store server is started. Each migration is
// associated with a name that is used to check whether the migration has been
// already run. To introduce a new database migration, just add the
// corresponding migration name and function to this list, and update the
// TestMigrateMigrationList test in migration_test.go adding the new name(s).
// Note that migration names must be unique across the list.
var migrations = []migration{{
	name:    "entity ids denormalization",
	migrate: denormalizeEntityIds,
}}

// migration holds a migration function with its corresponding name.
type migration struct {
	name    string
	migrate func(StoreDatabase) error
}

// Migrate starts the migration process using the given database.
func migrate(db StoreDatabase) error {
	// Retrieve already executed migrations.
	executed, err := getExecuted(db)
	if err != nil {
		return errgo.Mask(err)
	}

	// Execute required migrations.
	for _, m := range migrations {
		if executed[m.name] {
			logger.Debugf("skipping already executed migration: %s", m.name)
			continue
		}
		logger.Infof("starting migration: %s", m.name)
		if err := m.migrate(db); err != nil {
			return errgo.Notef(err, "error executing migration: %s", m.name)
		}
		if err := setExecuted(db, m.name); err != nil {
			return errgo.Mask(err)
		}
		logger.Infof("migration completed: %s", m.name)
	}
	return nil
}

func getExecuted(db StoreDatabase) (map[string]bool, error) {
	// Retrieve the already executed migration names.
	executed := make(map[string]bool)
	var doc mongodoc.Migration
	if err := db.Migrations().Find(nil).Select(bson.D{{"executed", 1}}).One(&doc); err != nil {
		if err == mgo.ErrNotFound {
			return executed, nil
		}
		return nil, errgo.Notef(err, "cannot retrieve executed migrations")
	}

	names := make(map[string]bool, len(migrations))
	for _, m := range migrations {
		names[m.name] = true
	}
	for _, name := range doc.Executed {
		// Check that the already executed migrations are known.
		if !names[name] {
			return nil, errgo.Newf("unexpected already executed migration: %s", name)
		}
		// Collect the name of the executed migration.
		executed[name] = true
	}
	return executed, nil
}

func setExecuted(db StoreDatabase, name string) error {
	if _, err := db.Migrations().Upsert(nil, bson.D{{
		"$addToSet", bson.D{{"executed", name}},
	}}); err != nil {
		return errgo.Notef(err, "cannot add %s to executed migrations", name)
	}
	return nil
}

// denormalizeEntityIds adds the user, name, revision and series fields to
// entities where those fields are missing.
// This function is not supposed to be called directly.
func denormalizeEntityIds(db StoreDatabase) error {
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
	return nil
}
