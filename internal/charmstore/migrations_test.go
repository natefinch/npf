// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore // import "gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"

import (
	"net/http"
	"sync"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"
)

type migrationsSuite struct {
	jujutesting.IsolatedMgoSuite
	db       StoreDatabase
	executed []mongodoc.MigrationName
}

var _ = gc.Suite(&migrationsSuite{})

func (s *migrationsSuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	s.db = StoreDatabase{s.Session.DB("migration-testing")}
	s.executed = nil
}

const (
	beforeAllMigrations mongodoc.MigrationName = "start"
	afterAllMigrations  mongodoc.MigrationName = "end"
)

var (
	// migrationEntityFields holds the fields added to mongodoc.Entity,
	// keyed by the migration step that added them.
	migrationEntityFields = map[mongodoc.MigrationName][]string{
		migrationAddSupportedSeries: {"supportedseries"},
		migrationAddDevelopment:     {"development"},
	}

	// migrationBaseEntityFields holds the fields added to mongodoc.BaseEntity,
	// keyed by the migration step that added them.
	migrationBaseEntityFields = map[mongodoc.MigrationName][]string{
		migrationAddDevelopmentACLs: {"developmentacls"},
	}

	// initialFields holds all the mongodoc.Entity fields
	// at the dawn of migration time.
	initialEntityFields = []string{
		"_id",
		"baseurl",
		"user",
		"name",
		"revision",
		"series",
		"blobhash",
		"blobhash256",
		"size",
		"blobname",
		"uploadtime",
		"extrainfo",
		"charmmeta",
		"charmconfig",
		"charmactions",
		"charmprovidedinterfaces",
		"charmrequiredinterfaces",
		"bundledata",
		"bundlereadme",
		"bundlemachinecount",
		"bundleunitcount",
		"contents",
		"promulgated-url",
		"promulgated-revision",
	}

	// initialBaseEntityFields holds all the mongodoc.BaseEntity fields
	// at the dawn of migration time.
	initialBaseEntityFields = []string{
		"_id",
		"user",
		"name",
		"public",
		"acls",
		"promulgated",
	}

	// entityFields holds all the fields in mongodoc.Entity just
	// before the named migration (the key) has been applied.
	entityFields = make(map[mongodoc.MigrationName][]string)

	// baseEntityFields holds all the fields in mongodoc.Entity just
	// before the named migration (the key) has been applied.
	baseEntityFields = make(map[mongodoc.MigrationName][]string)

	// postMigrationEntityFields holds all the fields in mongodoc.Entity just
	// after the named migration (the key) has been applied.
	postMigrationEntityFields = make(map[mongodoc.MigrationName][]string)

	// postMigrationBaseEntityFields holds all the fields in
	// mongodoc.BaseEntity just after the named migration (the key) has been
	// applied.
	postMigrationBaseEntityFields = make(map[mongodoc.MigrationName][]string)
)

func init() {
	// Initialize entityFields and baseEntityFields using the information
	// specified in migrationEntityFields and migrationBaseEntityFields.
	allEntityFields := initialEntityFields
	allBaseEntityFields := initialBaseEntityFields
	entityFields[beforeAllMigrations] = allEntityFields
	baseEntityFields[beforeAllMigrations] = allBaseEntityFields
	postMigrationEntityFields[beforeAllMigrations] = allEntityFields
	postMigrationBaseEntityFields[beforeAllMigrations] = allBaseEntityFields
	for _, m := range migrations {
		entityFields[m.name] = allEntityFields
		allEntityFields = append(allEntityFields, migrationEntityFields[m.name]...)
		postMigrationEntityFields[m.name] = allEntityFields
		baseEntityFields[m.name] = allBaseEntityFields
		allBaseEntityFields = append(allBaseEntityFields, migrationBaseEntityFields[m.name]...)
		postMigrationBaseEntityFields[m.name] = allBaseEntityFields
	}
	entityFields[afterAllMigrations] = allEntityFields
	baseEntityFields[afterAllMigrations] = allBaseEntityFields
	postMigrationEntityFields[afterAllMigrations] = allEntityFields
	postMigrationBaseEntityFields[afterAllMigrations] = allBaseEntityFields
}

func (s *migrationsSuite) newServer(c *gc.C) error {
	apiHandler := func(p *Pool, config ServerParams) HTTPCloseHandler {
		return nopCloseHandler{http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {})}
	}
	srv, err := NewServer(s.db.Database, nil, serverParams, map[string]NewAPIHandlerFunc{
		"version1": apiHandler,
	})
	if err == nil {
		srv.Close()
	}
	return err
}

// patchMigrations patches the charm store migration list with the given migrations.
func (s *migrationsSuite) patchMigrations(c *gc.C, ms []migration) {
	original := migrations
	s.AddCleanup(func(*gc.C) {
		migrations = original
	})
	migrations = ms
}

// makeMigrations generates default migrations using the given names, and then
// patches the charm store migration list with the generated ones.
func (s *migrationsSuite) makeMigrations(c *gc.C, names ...mongodoc.MigrationName) {
	ms := make([]migration, len(names))
	for i, name := range names {
		name := name
		ms[i] = migration{
			name: name,
			migrate: func(StoreDatabase) error {
				s.executed = append(s.executed, name)
				return nil
			},
		}
	}
	s.patchMigrations(c, ms)
}

func (s *migrationsSuite) TestMigrate(c *gc.C) {
	// Create migrations.
	names := []mongodoc.MigrationName{"migr-1", "migr-2"}
	s.makeMigrations(c, names...)

	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.IsNil)

	// The two migrations have been correctly executed in order.
	c.Assert(s.executed, jc.DeepEquals, names)

	// The migration document in the db reports that the execution is done.
	s.checkExecuted(c, names...)

	// Restart the server again and check migrations this time are not run.
	err = s.newServer(c)
	c.Assert(err, gc.IsNil)
	c.Assert(s.executed, jc.DeepEquals, names)
	s.checkExecuted(c, names...)
}

func (s *migrationsSuite) TestMigrateNoMigrations(c *gc.C) {
	// Empty the list of migrations.
	s.makeMigrations(c)

	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.IsNil)

	// No migrations were executed.
	c.Assert(s.executed, gc.HasLen, 0)
	s.checkExecuted(c)
}

func (s *migrationsSuite) TestMigrateNewMigration(c *gc.C) {
	// Simulate two migrations were already run.
	err := setExecuted(s.db, "migr-1")
	c.Assert(err, gc.IsNil)
	err = setExecuted(s.db, "migr-2")
	c.Assert(err, gc.IsNil)

	// Create migrations.
	s.makeMigrations(c, "migr-1", "migr-2", "migr-3")

	// Start the server.
	err = s.newServer(c)
	c.Assert(err, gc.IsNil)

	// Only one migration has been executed.
	c.Assert(s.executed, jc.DeepEquals, []mongodoc.MigrationName{"migr-3"})

	// The migration document in the db reports that the execution is done.
	s.checkExecuted(c, "migr-1", "migr-2", "migr-3")
}

func (s *migrationsSuite) TestMigrateErrorUnknownMigration(c *gc.C) {
	// Simulate that a migration was already run.
	err := setExecuted(s.db, "migr-1")
	c.Assert(err, gc.IsNil)

	// Create migrations, without including the already executed one.
	s.makeMigrations(c, "migr-2", "migr-3")

	// Start the server.
	err = s.newServer(c)
	c.Assert(err, gc.ErrorMatches, `database migration failed: found unknown migration "migr-1"; running old charm store code on newer charm store database\?`)

	// No new migrations were executed.
	c.Assert(s.executed, gc.HasLen, 0)
	s.checkExecuted(c, "migr-1")
}

func (s *migrationsSuite) TestMigrateErrorExecutingMigration(c *gc.C) {
	ms := []migration{{
		name: "migr-1",
		migrate: func(StoreDatabase) error {
			return nil
		},
	}, {
		name: "migr-2",
		migrate: func(StoreDatabase) error {
			return errgo.New("bad wolf")
		},
	}, {
		name: "migr-3",
		migrate: func(StoreDatabase) error {
			return nil
		},
	}}
	s.patchMigrations(c, ms)

	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.ErrorMatches, "database migration failed: error executing migration: migr-2: bad wolf")

	// Only one migration has been executed.
	s.checkExecuted(c, "migr-1")
}

func (s *migrationsSuite) TestMigrateMigrationNames(c *gc.C) {
	names := make(map[mongodoc.MigrationName]bool, len(migrations))
	for _, m := range migrations {
		c.Assert(names[m.name], jc.IsFalse, gc.Commentf("multiple migrations named %q", m.name))
		names[m.name] = true
	}
}

func (s *migrationsSuite) TestMigrateMigrationList(c *gc.C) {
	// When adding migration, update the list below, but never remove existing
	// migrations.
	existing := []string{}
	for i, name := range existing {
		m := migrations[i]
		c.Assert(m.name, gc.Equals, name)
	}
}

func (s *migrationsSuite) TestMigrateParallelMigration(c *gc.C) {
	// This test uses real migrations to check they are idempotent and works
	// well when done in parallel, for example when multiple charm store units
	// are deployed together.

	// Prepare a database for the migration.
	e1 := &mongodoc.Entity{
		URL:            charm.MustParseReference("~charmers/trusty/django-42"),
		PromulgatedURL: charm.MustParseReference("trusty/django-3"),
		Size:           12,
	}
	denormalizeEntity(e1)
	s.insertEntity(c, e1, beforeAllMigrations)

	e2 := &mongodoc.Entity{
		URL:  charm.MustParseReference("~who/utopic/rails-47"),
		Size: 13,
	}
	denormalizeEntity(e2)
	s.insertEntity(c, e2, beforeAllMigrations)

	// Run the migrations in parallel.
	var wg sync.WaitGroup
	wg.Add(5)
	errors := make(chan error, 5)
	for i := 0; i < 5; i++ {
		go func() {
			errors <- s.newServer(c)
			wg.Done()
		}()
	}
	wg.Wait()
	close(errors)

	// Check the server is correctly started in all the units.
	for err := range errors {
		c.Assert(err, gc.IsNil)
	}

	// Ensure entities have been updated correctly by all the migrations.
	// TODO when there are migrations, update e1 and e2 accordingly.
	s.checkCount(c, s.db.Entities(), 2)
	s.checkEntity(c, e1, afterAllMigrations)
	s.checkEntity(c, e2, afterAllMigrations)
}

func (s *migrationsSuite) TestMigrateAddSupportedSeries(c *gc.C) {
	s.patchMigrations(c, getMigrations(migrationAddSupportedSeries))

	entities := []*mongodoc.Entity{{
		URL:            charm.MustParseReference("~charmers/trusty/django-42"),
		PromulgatedURL: charm.MustParseReference("trusty/django-3"),
		Size:           12,
	}, {
		URL:  charm.MustParseReference("~who/utopic/rails-47"),
		Size: 13,
	}, {
		URL:  charm.MustParseReference("~who/bundle/something-47"),
		Size: 13,
	}}
	for _, e := range entities {
		denormalizeEntity(e)
		s.insertEntity(c, e, migrationAddSupportedSeries)
	}

	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.IsNil)

	// Ensure entities have been updated correctly.
	s.checkCount(c, s.db.Entities(), len(entities))
	for _, e := range entities {
		s.checkEntity(c, e, migrationAddSupportedSeries)
	}
}

func (s *migrationsSuite) TestMigrateAddDevelopment(c *gc.C) {
	s.patchMigrations(c, getMigrations(migrationAddDevelopment))

	// Populate the database with some entities.
	entities := []*mongodoc.Entity{{
		URL:            charm.MustParseReference("~charmers/trusty/django-42"),
		PromulgatedURL: charm.MustParseReference("trusty/django-3"),
		Size:           47,
	}, {
		URL:  charm.MustParseReference("~who/utopic/rails-47"),
		Size: 48,
	}, {
		URL:  charm.MustParseReference("~who/bundle/solution-0"),
		Size: 1,
	}}
	for _, e := range entities {
		denormalizeEntity(e)
		s.insertEntity(c, e, migrationAddDevelopment)
	}

	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.IsNil)

	// Ensure entities have been updated correctly.
	s.checkCount(c, s.db.Entities(), len(entities))
	for _, e := range entities {
		var rawEntity map[string]interface{}
		err := s.db.Entities().FindId(e.URL).One(&rawEntity)
		c.Assert(err, gc.IsNil)
		v, ok := rawEntity["development"]
		c.Assert(ok, jc.IsTrue, gc.Commentf("development field not present in entity %s", rawEntity["_id"]))
		c.Assert(v, jc.IsFalse, gc.Commentf("development field unexpectedly not false in entity %s", rawEntity["_id"]))
	}
}

func (s *migrationsSuite) TestMigrateAddDevelopmentACLs(c *gc.C) {
	s.patchMigrations(c, getMigrations(migrationAddDevelopmentACLs))

	// Populate the database with some entities.
	entities := []*mongodoc.BaseEntity{{
		URL:  charm.MustParseReference("~charmers/django"),
		Name: "django",
		ACLs: mongodoc.ACL{
			Read:  []string{"user", "group"},
			Write: []string{"user"},
		},
	}, {
		URL:  charm.MustParseReference("~who/rails"),
		Name: "rails",
		ACLs: mongodoc.ACL{
			Read:  []string{"everyone"},
			Write: []string{},
		},
	}, {
		URL:  charm.MustParseReference("~who/mediawiki-scalable"),
		Name: "mediawiki-scalable",
		ACLs: mongodoc.ACL{
			Read:  []string{"who"},
			Write: []string{"dalek"},
		},
	}}
	for _, e := range entities {
		s.insertBaseEntity(c, e, migrationAddDevelopmentACLs)
	}

	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.IsNil)

	// Ensure base entities have been updated correctly.
	s.checkCount(c, s.db.BaseEntities(), len(entities))
	for _, e := range entities {
		e.DevelopmentACLs = e.ACLs
		s.checkBaseEntity(c, e, migrationAddDevelopmentACLs)
	}
}

func (s *migrationsSuite) checkExecuted(c *gc.C, expected ...mongodoc.MigrationName) {
	var obtained []mongodoc.MigrationName
	var doc mongodoc.Migration
	if err := s.db.Migrations().Find(nil).One(&doc); err != mgo.ErrNotFound {
		c.Assert(err, gc.IsNil)
		obtained = doc.Executed
	}
	c.Assert(obtained, jc.SameContents, expected)
}

func getMigrations(names ...mongodoc.MigrationName) (ms []migration) {
	for _, name := range names {
		for _, m := range migrations {
			if m.name == name {
				ms = append(ms, m)
			}
		}
	}
	return ms
}

func (s *migrationsSuite) checkCount(c *gc.C, coll *mgo.Collection, expectCount int) {
	count, err := coll.Count()
	c.Assert(err, gc.IsNil)
	c.Assert(count, gc.Equals, expectCount)
}

// checkEntity checks the entity stored in the database with the ID
// expectEntity.URL is the same as expectEntity for all fields that exist
// in the database following completion of the given migration.
func (s *migrationsSuite) checkEntity(c *gc.C, expectEntity *mongodoc.Entity, name mongodoc.MigrationName) {
	var entity mongodoc.Entity
	err := s.db.Entities().FindId(expectEntity.URL).One(&entity)
	c.Assert(err, gc.IsNil)
	obtained := entityWithFields(c, &entity, postMigrationEntityFields[name])
	expected := entityWithFields(c, expectEntity, postMigrationEntityFields[name])
	c.Assert(obtained, jc.DeepEquals, expected)
}

// checkBaseEntity checks the base entity stored in the database with the ID
// expectEntity.URL is the same as expectEntity for all fields that exist
// in the database following completion of the given migration.
func (s *migrationsSuite) checkBaseEntity(c *gc.C, expectEntity *mongodoc.BaseEntity, name mongodoc.MigrationName) {
	var entity mongodoc.BaseEntity
	err := s.db.BaseEntities().FindId(expectEntity.URL).One(&entity)
	c.Assert(err, gc.IsNil)
	obtained := baseEntityWithFields(c, &entity, postMigrationBaseEntityFields[name])
	expected := baseEntityWithFields(c, expectEntity, postMigrationBaseEntityFields[name])
	c.Assert(obtained, jc.DeepEquals, expected)
}

// insertEntity inserts the given entity. The migration that the entity
// is to be inserted for is specified in name; only fields that existed
// prior to that migration will be inserted.
func (s *migrationsSuite) insertEntity(c *gc.C, e *mongodoc.Entity, name mongodoc.MigrationName) {
	err := s.db.Entities().Insert(entityWithFields(c, e, entityFields[name]))
	c.Assert(err, gc.IsNil)
}

// insertBaseEntity inserts the given base entity. The migration that the
// entity is to be inserted for is specified in name; only fields that existed
// prior to that migration will be inserted.
func (s *migrationsSuite) insertBaseEntity(c *gc.C, e *mongodoc.BaseEntity, name mongodoc.MigrationName) {
	err := s.db.BaseEntities().Insert(baseEntityWithFields(c, e, baseEntityFields[name]))
	c.Assert(err, gc.IsNil)
}

// entityWithFields creates a version of the specified mongodoc.Entity as
// it would appear if it only contained the specified fields. This is to
// simulate previous versions of documents in the database.
func entityWithFields(c *gc.C, e *mongodoc.Entity, includeFields []string) map[string]interface{} {
	data, err := bson.Marshal(e)
	c.Assert(err, gc.IsNil)
	return withFields(c, data, includeFields)
}

// baseEntityWithFields creates a version of the specified mongodoc.BaseEntity
// as it would appear if it only contained the specified fields. This is to
// simulate previous versions of documents in the database.
func baseEntityWithFields(c *gc.C, e *mongodoc.BaseEntity, includeFields []string) map[string]interface{} {
	data, err := bson.Marshal(e)
	c.Assert(err, gc.IsNil)
	return withFields(c, data, includeFields)
}

func withFields(c *gc.C, data []byte, includeFields []string) (rawEntity map[string]interface{}) {
	err := bson.Unmarshal(data, &rawEntity)
	c.Assert(err, gc.IsNil)
loop:
	for k := range rawEntity {
		for _, inc := range includeFields {
			if inc == k {
				continue loop
			}
		}
		delete(rawEntity, k)
	}
	return rawEntity
}
