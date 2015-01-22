// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore

import (
	"gopkg.in/errgo.v1"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/params"
)

// migrations holds all the migration functions that are executed in the order
// they are defined when the charm store server is started. Each migration is
// associated with a name that is used to check whether the migration has been
// already run. To introduce a new database migration, add the corresponding
// migration name and function to this list, and update the
// TestMigrateMigrationList test in migration_test.go adding the new name(s).
// Note that migration names must be unique across the list.
var migrations = []migration{{
	name:    "entity ids denormalization",
	migrate: denormalizeEntityIds,
}, {
	name:    "base entities creation",
	migrate: createBaseEntities,
}, {
	name:    "read acl creation",
	migrate: populateReadACL,
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
			return nil, errgo.Newf("found unknown migration %q; running old charm store code on newer charm store database?", name)
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
	defer iter.Close()

	for iter.Next(&entity) {
		logger.Infof("updating %s", entity.URL)
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

// createBaseEntities creates base entities for each entity in the database.
func createBaseEntities(db StoreDatabase) error {
	baseEntities := db.BaseEntities()
	counter := 0

	var entity mongodoc.Entity
	iter := db.Entities().Find(nil).Select(bson.D{{"baseurl", 1}}).Iter()
	defer iter.Close()

	for iter.Next(&entity) {
		baseEntity := &mongodoc.BaseEntity{
			URL:    entity.BaseURL,
			Name:   entity.BaseURL.Name,
			User:   entity.BaseURL.User,
			Public: true,
		}
		err := baseEntities.Insert(baseEntity)
		if err == nil {
			counter++
		} else if !mgo.IsDup(err) {
			return errgo.Notef(err, "cannot create base entity %s", entity.BaseURL)
		}

	}
	if err := iter.Close(); err != nil {
		return errgo.Notef(err, "cannot create base entities")
	}
	logger.Infof("%d base entities created", counter)
	return nil
}

// populateReadACL adds the read ACL to base entities not having it.
func populateReadACL(db StoreDatabase) error {
	baseEntities := db.BaseEntities()
	var entity mongodoc.BaseEntity
	iter := baseEntities.Find(bson.D{{
		"acls.read", bson.D{{"$size", 0}},
	}}).Select(bson.D{{"_id", 1}}).Iter()
	defer iter.Close()

	counter := 0
	for iter.Next(&entity) {
		readPerm := everyonePerm
		if entity.URL.User != "" {
			readPerm = []string{params.Everyone, entity.URL.User}
		}
		if err := baseEntities.UpdateId(entity.URL, bson.D{{
			"$set", bson.D{{"acls.read", readPerm}},
		}}); err != nil {
			return errgo.Notef(err, "cannot populate ACL for base entity %s", entity.URL)
		}
		counter++
	}
	if err := iter.Close(); err != nil {
		return errgo.Notef(err, "cannot populate ACLs for base entities")
	}
	logger.Infof("%d base entities updated", counter)
	return nil
}
