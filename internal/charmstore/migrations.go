// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore // import "gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"

import (
	"gopkg.in/errgo.v1"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"
)

const (
	migrationAddSupportedSeries mongodoc.MigrationName = "add supported series"
	migrationAddDevelopment     mongodoc.MigrationName = "add development"
	migrationAddDevelopmentACLs mongodoc.MigrationName = "add development acls"
)

// migrations holds all the migration functions that are executed in the order
// they are defined when the charm store server is started. Each migration is
// associated with a name that is used to check whether the migration has been
// already run. To introduce a new database migration, add the corresponding
// migration name and function to this list, and update the
// TestMigrateMigrationList test in migration_test.go adding the new name(s).
// Note that migration names must be unique across the list.
//
// A migration entry may have a nil migration function if the migration
// is obsolete. Obsolete migrations should never be removed entirely,
// otherwise the charmstore will see the old migrations in the table
// and refuse to start up because it thinks that it's running an old
// version of the charm store on a newer version of the database.
var migrations = []migration{{
	name: "entity ids denormalization",
}, {
	name: "base entities creation",
}, {
	name: "read acl creation",
}, {
	name: "write acl creation",
}, {
	name:    migrationAddSupportedSeries,
	migrate: addSupportedSeries,
}, {
	name:    migrationAddDevelopment,
	migrate: addDevelopment,
}, {
	name:    migrationAddDevelopmentACLs,
	migrate: addDevelopmentACLs,
}}

// migration holds a migration function with its corresponding name.
type migration struct {
	name    mongodoc.MigrationName
	migrate func(StoreDatabase) error
}

// Migrate starts the migration process using the given database.
func migrate(db StoreDatabase) error {
	// Retrieve already executed migrations.
	executed, err := getExecuted(db)
	if err != nil {
		return errgo.Mask(err)
	}

	// Explicitly create the collection in case there are no migrations
	// so that the tests that expect the migrations collection to exist
	// will pass. We ignore the error because we'll get one if the
	// collection already exists and there's no special type or value
	// for that (and if it's a genuine error, we'll catch the problem later
	// anyway).
	db.Migrations().Create(&mgo.CollectionInfo{})
	// Execute required migrations.
	for _, m := range migrations {
		if executed[m.name] || m.migrate == nil {
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

func getExecuted(db StoreDatabase) (map[mongodoc.MigrationName]bool, error) {
	// Retrieve the already executed migration names.
	executed := make(map[mongodoc.MigrationName]bool)
	var doc mongodoc.Migration
	if err := db.Migrations().Find(nil).Select(bson.D{{"executed", 1}}).One(&doc); err != nil {
		if err == mgo.ErrNotFound {
			return executed, nil
		}
		return nil, errgo.Notef(err, "cannot retrieve executed migrations")
	}

	names := make(map[mongodoc.MigrationName]bool, len(migrations))
	for _, m := range migrations {
		names[m.name] = true
	}
	for _, name := range doc.Executed {
		name := mongodoc.MigrationName(name)
		// Check that the already executed migrations are known.
		if !names[name] {
			return nil, errgo.Newf("found unknown migration %q; running old charm store code on newer charm store database?", name)
		}
		// Collect the name of the executed migration.
		executed[name] = true
	}
	return executed, nil
}

// addSupportedSeries adds the supported-series field
// to entities that don't have it. Note that it does not
// need to work for multi-series charms because support
// for those has not been implemented before this migration.
func addSupportedSeries(db StoreDatabase) error {
	entities := db.Entities()
	var entity mongodoc.Entity
	iter := entities.Find(bson.D{{
		// Use the supportedseries field to collect not migrated entities.
		"supportedseries", bson.D{{"$exists", false}},
	}, {
		"series", bson.D{{"$ne", "bundle"}},
	}}).Select(bson.D{{"_id", 1}}).Iter()
	defer iter.Close()

	for iter.Next(&entity) {
		logger.Infof("updating %s", entity.URL)
		if err := entities.UpdateId(entity.URL, bson.D{{
			"$set", bson.D{
				{"supportedseries", []string{entity.URL.Series}},
			},
		}}); err != nil {
			return errgo.Notef(err, "cannot denormalize entity id %s", entity.URL)
		}
	}
	if err := iter.Close(); err != nil {
		return errgo.Notef(err, "cannot iterate entities")
	}
	return nil
}

// addDevelopment adds the Development field to all entities on which that
// field is not present.
func addDevelopment(db StoreDatabase) error {
	logger.Infof("adding development field to all entities")
	if _, err := db.Entities().UpdateAll(bson.D{{
		"development", bson.D{{"$exists", false}},
	}}, bson.D{{
		"$set", bson.D{{"development", false}},
	}}); err != nil {
		return errgo.Notef(err, "cannot add development field to all entities")
	}
	return nil
}

// addDevelopmentACLs sets up ACLs on base entities for development revisions.
func addDevelopmentACLs(db StoreDatabase) error {
	logger.Infof("adding development ACLs to all base entities")
	baseEntities := db.BaseEntities()
	var baseEntity mongodoc.BaseEntity
	iter := baseEntities.Find(bson.D{{
		"developmentacls", bson.D{{"$exists", false}},
	}}).Select(bson.D{{"_id", 1}, {"acls", 1}}).Iter()
	defer iter.Close()
	for iter.Next(&baseEntity) {
		if err := baseEntities.UpdateId(baseEntity.URL, bson.D{{
			"$set", bson.D{{"developmentacls", baseEntity.ACLs}},
		}}); err != nil {
			return errgo.Notef(err, "cannot add development ACLs to base entity id %s", baseEntity.URL)
		}
	}
	if err := iter.Close(); err != nil {
		return errgo.Notef(err, "cannot iterate base entities")
	}
	return nil
}

func setExecuted(db StoreDatabase, name mongodoc.MigrationName) error {
	if _, err := db.Migrations().Upsert(nil, bson.D{{
		"$addToSet", bson.D{{"executed", name}},
	}}); err != nil {
		return errgo.Notef(err, "cannot add %s to executed migrations", name)
	}
	return nil
}
