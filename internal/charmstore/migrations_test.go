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

var (
	// migrationFields holds the fields added to mongodoc.Entity,
	// keyed by the migration step that added them.
	migrationEntityFields = map[mongodoc.MigrationName][]string{}

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

	// finalEntityFields holds all the entity fields after all the migrations
	// have taken place.
	finalEntityFields []string

	// entityFields holds all the fields in mongodoc.Entity just
	// before the named migration (the key) has been applied.
	entityFields = make(map[mongodoc.MigrationName][]string)
)

func init() {
	// Initialize entityFields using the information specified in migrationFields.
	allFields := initialEntityFields
	for _, m := range migrations {
		entityFields[m.name] = allFields
		allFields = append(allFields, migrationEntityFields[m.name]...)
	}
	finalEntityFields = allFields
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
	e1 := denormalizeEntity(&mongodoc.Entity{
		URL:            charm.MustParseReference("~charmers/trusty/django-42"),
		PromulgatedURL: charm.MustParseReference("trusty/django-3"),
		Size:           12,
	})
	e2 := denormalizeEntity(&mongodoc.Entity{
		URL:  charm.MustParseReference("~who/utopic/rails-47"),
		Size: 13,
	})
	s.insertEntity(c, e1, initialEntityFields)
	s.insertEntity(c, e2, initialEntityFields)

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
	s.checkEntity(c, e1)
	s.checkEntity(c, e2)
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

func (s *migrationsSuite) checkEntity(c *gc.C, expectEntity *mongodoc.Entity) {
	var entity mongodoc.Entity
	err := s.db.Entities().FindId(expectEntity.URL).One(&entity)
	c.Assert(err, gc.IsNil)

	c.Assert(&entity, jc.DeepEquals, expectEntity)
}

func (s *migrationsSuite) checkCount(c *gc.C, coll *mgo.Collection, expectCount int) {
	count, err := coll.Count()
	c.Assert(err, gc.IsNil)
	c.Assert(count, gc.Equals, expectCount)
}

func (s *migrationsSuite) checkBaseEntity(c *gc.C, expectEntity *mongodoc.BaseEntity) {
	var entity mongodoc.BaseEntity
	err := s.db.BaseEntities().FindId(expectEntity.URL).One(&entity)
	c.Assert(err, gc.IsNil)
	c.Assert(&entity, jc.DeepEquals, expectEntity)
}

func (s *migrationsSuite) checkBaseEntitiesCount(c *gc.C, expectCount int) {
	count, err := s.db.Entities().Count()
	c.Assert(err, gc.IsNil)
	c.Assert(count, gc.Equals, expectCount)
}

// denormalizeEntity returns a copy of e0 with all denormalized fields
// filled out if they are zero.
func denormalizeEntity(e0 *mongodoc.Entity) *mongodoc.Entity {
	e := *e0
	if e.BaseURL == nil {
		e.BaseURL = baseURL(e.URL)
	}
	if e.Name == "" {
		e.Name = e.URL.Name
	}
	if e.User == "" {
		e.User = e.URL.User
	}
	if e.Revision == 0 {
		e.Revision = e.URL.Revision
	}
	if e.Series == "" {
		e.Series = e.URL.Series
	}
	if e.PromulgatedRevision == 0 {
		if e.PromulgatedURL == nil {
			e.PromulgatedRevision = -1
		} else {
			e.PromulgatedRevision = e.PromulgatedURL.Revision
		}
	}
	return &e
}

// insertEntity inserts the given entity. If any include fields are specified,
// only those given entity fields will be inserted.
func (s *migrationsSuite) insertEntity(c *gc.C, e *mongodoc.Entity, includeFields []string) {
	data, err := bson.Marshal(e)
	c.Assert(err, gc.IsNil)
	var rawEntity map[string]interface{}
	err = bson.Unmarshal(data, &rawEntity)
	c.Assert(err, gc.IsNil)

	if len(includeFields) > 0 {
	loop:
		for k := range rawEntity {
			for _, inc := range includeFields {
				if inc == k {
					continue loop
				}
			}
			delete(rawEntity, k)
		}
	}
	err = s.db.Entities().Insert(rawEntity)
	c.Assert(err, gc.IsNil)
}
