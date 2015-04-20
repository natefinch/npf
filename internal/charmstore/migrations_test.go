// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore

import (
	"net/http"
	"sort"
	"sync"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v5"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"gopkg.in/juju/charmstore.v4/internal/mongodoc"
	"gopkg.in/juju/charmstore.v4/internal/storetesting"
	"gopkg.in/juju/charmstore.v4/params"
)

type migrationsSuite struct {
	storetesting.IsolatedMgoSuite
	db       StoreDatabase
	executed []string
}

var _ = gc.Suite(&migrationsSuite{})

func (s *migrationsSuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	s.db = StoreDatabase{s.Session.DB("migration-testing")}
	s.executed = []string{}
}

func (s *migrationsSuite) newServer(c *gc.C) error {
	apiHandler := func(p *Pool, config ServerParams) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {})
	}
	_, err := NewServer(s.db.Database, nil, serverParams, map[string]NewAPIHandlerFunc{
		"version1": apiHandler,
	})
	return err
}

// patchMigrations patches the charm store migration list with the given ms.
func (s *migrationsSuite) patchMigrations(c *gc.C, ms []migration) {
	original := migrations
	s.AddCleanup(func(*gc.C) {
		migrations = original
	})
	migrations = ms
}

// makeMigrations generates default migrations using the given names, and then
// patches the charm store migration list with the generated ones.
func (s *migrationsSuite) makeMigrations(c *gc.C, names ...string) {
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
	names := []string{"migr-1", "migr-2"}
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
	c.Assert(s.executed, jc.DeepEquals, []string{"migr-3"})

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
	names := make(map[string]bool, len(migrations))
	for _, m := range migrations {
		c.Assert(names[m.name], jc.IsFalse, gc.Commentf("multiple migrations named %q", m.name))
		names[m.name] = true
	}
}

func (s *migrationsSuite) TestMigrateMigrationList(c *gc.C) {
	// When adding migration, update the list below, but never remove existing
	// migrations.
	existing := []string{
		"entity ids denormalization",
		"base entities creation",
		"read acl creation",
		"write acl creation",
	}
	for i, name := range existing {
		m := migrations[i]
		c.Assert(m.name, gc.Equals, name)
	}
}

func (s *migrationsSuite) TestMigrateParallelMigration(c *gc.C) {
	// This test uses real migrations to check they are idempotent and works
	// well when done in parallel, for example when multiple charm store units
	// are deployed together.

	// Prepare a database for the denormalizeEntityIds migration.
	id1 := charm.MustParseReference("trusty/django-42")
	id2 := charm.MustParseReference("~who/utopic/rails-47")
	s.insertEntity(c, id1, "", 12)
	s.insertEntity(c, id2, "", 13)

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

	// Ensure entities have been updated correctly by denormalizeEntityIds.
	s.checkCount(c, s.db.Entities(), 2)
	s.checkEntity(c, &mongodoc.Entity{
		URL:      id1,
		BaseURL:  baseURL(id1),
		User:     "",
		Name:     "django",
		Revision: 42,
		Series:   "trusty",
		Size:     12,
	})
	s.checkEntity(c, &mongodoc.Entity{
		URL:      id2,
		BaseURL:  baseURL(id2),
		User:     "who",
		Name:     "rails",
		Revision: 47,
		Series:   "utopic",
		Size:     13,
	})
}

func (s *migrationsSuite) checkExecuted(c *gc.C, expected ...string) {
	var obtained []string
	var doc mongodoc.Migration
	if err := s.db.Migrations().Find(nil).One(&doc); err != mgo.ErrNotFound {
		c.Assert(err, gc.IsNil)
		obtained = doc.Executed
		sort.Strings(obtained)
	}
	sort.Strings(expected)
	c.Assert(obtained, jc.DeepEquals, expected)
}

func getMigrations(names ...string) (ms []migration) {
	for _, name := range names {
		for _, m := range migrations {
			if m.name == name {
				ms = append(ms, m)
			}
		}
	}
	return ms
}

func (s *migrationsSuite) TestDenormalizeEntityIds(c *gc.C) {
	s.patchMigrations(c, getMigrations("entity ids denormalization"))
	// Store entities with missing name in the db.
	id1 := charm.MustParseReference("trusty/django-42")
	id2 := charm.MustParseReference("~who/utopic/rails-47")
	s.insertEntity(c, id1, "", 12)
	s.insertEntity(c, id2, "", 13)

	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.IsNil)

	// Ensure entities have been updated correctly.
	s.checkCount(c, s.db.Entities(), 2)
	s.checkEntity(c, &mongodoc.Entity{
		URL:      id1,
		BaseURL:  baseURL(id1),
		User:     "",
		Name:     "django",
		Revision: 42,
		Series:   "trusty",
		Size:     12,
	})
	s.checkEntity(c, &mongodoc.Entity{
		URL:      id2,
		BaseURL:  baseURL(id2),
		User:     "who",
		Name:     "rails",
		Revision: 47,
		Series:   "utopic",
		Size:     13,
	})
}

func (s *migrationsSuite) TestDenormalizeEntityIdsNoEntities(c *gc.C) {
	s.patchMigrations(c, getMigrations("entity ids denormalization"))
	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.IsNil)

	// Ensure no new entities are added in the process.
	s.checkCount(c, s.db.Entities(), 0)
}

func (s *migrationsSuite) TestDenormalizeEntityIdsNoUpdates(c *gc.C) {
	s.patchMigrations(c, getMigrations("entity ids denormalization"))
	// Store entities with a name in the db.
	id1 := charm.MustParseReference("trusty/django-42")
	id2 := charm.MustParseReference("~who/utopic/rails-47")
	s.insertEntity(c, id1, "django", 21)
	s.insertEntity(c, id2, "rails2", 22)

	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.IsNil)

	// Ensure entities have been updated correctly.
	s.checkCount(c, s.db.Entities(), 2)
	s.checkEntity(c, &mongodoc.Entity{
		URL:     id1,
		BaseURL: baseURL(id1),
		User:    "",
		Name:    "django",
		// Since the name field already existed, the Revision and Series fields
		// have not been populated.
		Size: 21,
	})
	s.checkEntity(c, &mongodoc.Entity{
		URL:     id2,
		BaseURL: baseURL(id2),
		// The name is left untouched (even if it's obviously wrong).
		Name: "rails2",
		// Since the name field already existed, the User, Revision and Series
		// fields have not been populated.
		Size: 22,
	})
}

func (s *migrationsSuite) TestDenormalizeEntityIdsSomeUpdates(c *gc.C) {
	s.patchMigrations(c, getMigrations("entity ids denormalization"))
	// Store entities with and without names in the db
	id1 := charm.MustParseReference("~dalek/utopic/django-42")
	id2 := charm.MustParseReference("~dalek/utopic/django-47")
	id3 := charm.MustParseReference("precise/postgres-0")
	s.insertEntity(c, id1, "", 1)
	s.insertEntity(c, id2, "django", 2)
	s.insertEntity(c, id3, "", 3)

	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.IsNil)

	// Ensure entities have been updated correctly.
	s.checkCount(c, s.db.Entities(), 3)
	s.checkEntity(c, &mongodoc.Entity{
		URL:      id1,
		BaseURL:  baseURL(id1),
		User:     "dalek",
		Name:     "django",
		Revision: 42,
		Series:   "utopic",
		Size:     1,
	})
	s.checkEntity(c, &mongodoc.Entity{
		URL:     id2,
		BaseURL: baseURL(id2),
		Name:    "django",
		Size:    2,
	})
	s.checkEntity(c, &mongodoc.Entity{
		URL:      id3,
		BaseURL:  baseURL(id3),
		User:     "",
		Name:     "postgres",
		Revision: 0,
		Series:   "precise",
		Size:     3,
	})
}

func (s *migrationsSuite) TestCreateBaseEntities(c *gc.C) {
	s.patchMigrations(c, getMigrations("base entities creation"))
	// Store entities with missing base in the db.
	id1 := charm.MustParseReference("trusty/django-42")
	id2 := charm.MustParseReference("trusty/django-47")
	id3 := charm.MustParseReference("~who/utopic/rails-47")
	s.insertEntity(c, id1, "django", 12)
	s.insertEntity(c, id2, "django", 12)
	s.insertEntity(c, id3, "rails", 13)

	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.IsNil)

	// Ensure base entities have been created correctly.
	s.checkCount(c, s.db.BaseEntities(), 2)
	s.checkBaseEntity(c, &mongodoc.BaseEntity{
		URL:    baseURL(id1),
		Name:   "django",
		Public: true,
	})
	s.checkBaseEntity(c, &mongodoc.BaseEntity{
		URL:    baseURL(id3),
		User:   "who",
		Name:   "rails",
		Public: true,
	})
}

func (s *migrationsSuite) TestCreateBaseEntitiesNoEntities(c *gc.C) {
	s.patchMigrations(c, getMigrations("base entities creation"))
	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.IsNil)

	// Ensure no new base entities are added in the process.
	s.checkCount(c, s.db.BaseEntities(), 0)
}

func (s *migrationsSuite) TestCreateBaseEntitiesNoUpdates(c *gc.C) {
	s.patchMigrations(c, getMigrations("base entities creation"))
	// Store entities with their corresponding base in the db.
	id1 := charm.MustParseReference("trusty/django-42")
	id2 := charm.MustParseReference("~who/utopic/rails-47")
	s.insertEntity(c, id1, "django", 21)
	s.insertEntity(c, id2, "rails2", 22)
	s.insertBaseEntity(c, baseURL(id1), nil)
	s.insertBaseEntity(c, baseURL(id2), nil)

	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.IsNil)

	// Ensure no new base entities are added in the process.
	s.checkCount(c, s.db.BaseEntities(), 2)
}

func (s *migrationsSuite) TestCreateBaseEntitiesSomeUpdates(c *gc.C) {
	s.patchMigrations(c, getMigrations("base entities creation"))
	// Store entities with and without bases in the db
	id1 := charm.MustParseReference("~dalek/utopic/django-42")
	id2 := charm.MustParseReference("~dalek/utopic/django-47")
	id3 := charm.MustParseReference("precise/postgres-0")
	s.insertEntity(c, id1, "django", 1)
	s.insertEntity(c, id2, "django", 2)
	s.insertEntity(c, id3, "postgres", 3)
	s.insertBaseEntity(c, baseURL(id2), nil)

	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.IsNil)

	// Ensure missing base entities have been created correctly.
	s.checkCount(c, s.db.BaseEntities(), 2)
	s.checkBaseEntity(c, &mongodoc.BaseEntity{
		URL:    baseURL(id1),
		User:   "dalek",
		Name:   "django",
		Public: true,
	})
	s.checkBaseEntity(c, &mongodoc.BaseEntity{
		URL:    baseURL(id3),
		Name:   "postgres",
		Public: true,
	})
}

func (s *migrationsSuite) TestPopulateReadACL(c *gc.C) {
	s.patchMigrations(c, getMigrations("read acl creation"))
	// Store entities with their base in the db.
	// The base entities will not include any read permission.
	id1 := charm.MustParseReference("trusty/django-42")
	id2 := charm.MustParseReference("trusty/django-47")
	id3 := charm.MustParseReference("~who/utopic/rails-47")
	s.insertEntity(c, id1, "django", 12)
	s.insertEntity(c, id2, "django", 12)
	s.insertEntity(c, id3, "rails", 13)
	baseId1 := baseURL(id1)
	baseId3 := baseURL(id3)
	s.insertBaseEntity(c, baseId1, nil)
	s.insertBaseEntity(c, baseId3, nil)

	// Ensure read permission is empty.
	s.checkBaseEntity(c, &mongodoc.BaseEntity{
		URL:    baseId1,
		Name:   "django",
		Public: true,
	})
	s.checkBaseEntity(c, &mongodoc.BaseEntity{
		URL:    baseId3,
		User:   "who",
		Name:   "rails",
		Public: true,
	})

	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.IsNil)

	// Ensure read permission has been correctly set.
	s.checkBaseEntity(c, &mongodoc.BaseEntity{
		URL:    baseId1,
		Name:   "django",
		Public: true,
		ACLs: mongodoc.ACL{
			Read: []string{params.Everyone},
		},
	})
	s.checkBaseEntity(c, &mongodoc.BaseEntity{
		URL:    baseId3,
		User:   "who",
		Name:   "rails",
		Public: true,
		ACLs: mongodoc.ACL{
			Read: []string{params.Everyone, "who"},
		},
	})
}

func (s *migrationsSuite) TestCreateBaseEntitiesAndPopulateReadACL(c *gc.C) {
	s.patchMigrations(c, getMigrations("base entities creation", "read acl creation"))
	// Store entities with missing base in the db.
	id1 := charm.MustParseReference("trusty/django-42")
	id2 := charm.MustParseReference("trusty/django-47")
	id3 := charm.MustParseReference("~who/utopic/rails-47")
	s.insertEntity(c, id1, "django", 12)
	s.insertEntity(c, id2, "django", 12)
	s.insertEntity(c, id3, "rails", 13)

	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.IsNil)

	// Ensure base entities have been created correctly.
	s.checkCount(c, s.db.BaseEntities(), 2)
	s.checkBaseEntity(c, &mongodoc.BaseEntity{
		URL:    baseURL(id1),
		Name:   "django",
		Public: true,
		ACLs: mongodoc.ACL{
			Read: []string{params.Everyone},
		},
	})
	s.checkBaseEntity(c, &mongodoc.BaseEntity{
		URL:    baseURL(id3),
		User:   "who",
		Name:   "rails",
		Public: true,
		ACLs: mongodoc.ACL{
			Read: []string{params.Everyone, "who"},
		},
	})
}

func (s *migrationsSuite) TestPopulateReadACLNoEntities(c *gc.C) {
	s.patchMigrations(c, getMigrations("read acl creation"))
	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.IsNil)

	// Ensure no new base entities are added in the process.
	s.checkCount(c, s.db.BaseEntities(), 0)
}

func (s *migrationsSuite) TestPopulateReadACLNoUpdates(c *gc.C) {
	s.patchMigrations(c, getMigrations("read acl creation"))
	// Store entities with their corresponding base in the db.
	id1 := charm.MustParseReference("trusty/django-42")
	id2 := charm.MustParseReference("~who/utopic/rails-47")
	s.insertEntity(c, id1, "django", 21)
	s.insertEntity(c, id2, "rails2", 22)
	s.insertBaseEntity(c, baseURL(id1), &mongodoc.ACL{
		Read: []string{"jean-luc"},
	})
	s.insertBaseEntity(c, baseURL(id2), &mongodoc.ACL{
		Read: []string{"who"},
	})

	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.IsNil)

	// Ensure no new base entities are added in the process, and read
	// permissions were not changed.
	s.checkCount(c, s.db.BaseEntities(), 2)
	s.checkBaseEntity(c, &mongodoc.BaseEntity{
		URL:    baseURL(id1),
		Name:   "django",
		Public: true,
		ACLs: mongodoc.ACL{
			Read: []string{"jean-luc"},
		},
	})
	s.checkBaseEntity(c, &mongodoc.BaseEntity{
		URL:    baseURL(id2),
		User:   "who",
		Name:   "rails",
		Public: true,
		ACLs: mongodoc.ACL{
			Read: []string{"who"},
		},
	})
}

func (s *migrationsSuite) TestPopulateReadACLSomeUpdates(c *gc.C) {
	s.patchMigrations(c, getMigrations("read acl creation"))
	// Store entities with and without bases in the db
	id1 := charm.MustParseReference("~dalek/utopic/django-42")
	id2 := charm.MustParseReference("~dalek/utopic/django-47")
	id3 := charm.MustParseReference("precise/postgres-0")
	s.insertEntity(c, id1, "django", 1)
	s.insertEntity(c, id2, "django", 2)
	s.insertEntity(c, id3, "postgres", 3)
	baseId1 := baseURL(id1)
	baseId3 := baseURL(id3)
	s.insertBaseEntity(c, baseId1, nil)
	s.insertBaseEntity(c, baseId3, &mongodoc.ACL{
		Read: []string{"benjamin"},
	})

	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.IsNil)

	// Ensure missing read permissions have been populated correctly.
	s.checkCount(c, s.db.BaseEntities(), 2)
	s.checkBaseEntity(c, &mongodoc.BaseEntity{
		URL:    baseId1,
		User:   "dalek",
		Name:   "django",
		Public: true,
		ACLs: mongodoc.ACL{
			Read: []string{params.Everyone, "dalek"},
		},
	})
	s.checkBaseEntity(c, &mongodoc.BaseEntity{
		URL:    baseId3,
		Name:   "postgres",
		Public: true,
		ACLs: mongodoc.ACL{
			Read: []string{"benjamin"},
		},
	})
}

func (s *migrationsSuite) TestPopulateWriteACL(c *gc.C) {
	s.patchMigrations(c, getMigrations("write acl creation"))
	// Store entities with their base in the db.
	// The base entities will not include any write permission.
	id1 := charm.MustParseReference("~who/trusty/django-42")
	id2 := charm.MustParseReference("~who/django-47")
	id3 := charm.MustParseReference("~dalek/utopic/rails-47")
	s.insertEntity(c, id1, "django", 12)
	s.insertEntity(c, id2, "django", 12)
	s.insertEntity(c, id3, "rails", 13)
	baseId1 := baseURL(id1)
	baseId3 := baseURL(id3)
	s.insertBaseEntity(c, baseId1, nil)
	s.insertBaseEntity(c, baseId3, nil)

	// Ensure write permission is empty.
	s.checkBaseEntity(c, &mongodoc.BaseEntity{
		URL:    baseId1,
		User:   "who",
		Name:   "django",
		Public: true,
	})
	s.checkBaseEntity(c, &mongodoc.BaseEntity{
		URL:    baseId3,
		User:   "dalek",
		Name:   "rails",
		Public: true,
	})

	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.IsNil)

	// Ensure write permission has been correctly set.
	s.checkBaseEntity(c, &mongodoc.BaseEntity{
		URL:    baseId1,
		User:   "who",
		Name:   "django",
		Public: true,
		ACLs: mongodoc.ACL{
			Write: []string{"who"},
		},
	})
	s.checkBaseEntity(c, &mongodoc.BaseEntity{
		URL:    baseId3,
		User:   "dalek",
		Name:   "rails",
		Public: true,
		ACLs: mongodoc.ACL{
			Write: []string{"dalek"},
		},
	})
}

func (s *migrationsSuite) TestPopulateWriteACLNoEntities(c *gc.C) {
	s.patchMigrations(c, getMigrations("write acl creation"))
	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.IsNil)

	// Ensure no new base entities are added in the process.
	s.checkCount(c, s.db.BaseEntities(), 0)
}

func (s *migrationsSuite) TestPopulateWriteACLNoUpdates(c *gc.C) {
	s.patchMigrations(c, getMigrations("write acl creation"))
	// Store entities with their corresponding base in the db.
	id1 := charm.MustParseReference("trusty/django-42")
	id2 := charm.MustParseReference("~who/utopic/rails-47")
	s.insertEntity(c, id1, "django", 21)
	s.insertEntity(c, id2, "rails2", 22)
	s.insertBaseEntity(c, baseURL(id1), nil)
	s.insertBaseEntity(c, baseURL(id2), &mongodoc.ACL{
		Write: []string{"dalek"},
	})

	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.IsNil)

	// Ensure no new base entities are added in the process, and write
	// permissions were not changed.
	s.checkCount(c, s.db.BaseEntities(), 2)
	s.checkBaseEntity(c, &mongodoc.BaseEntity{
		URL:    baseURL(id1),
		Name:   "django",
		Public: true,
	})
	s.checkBaseEntity(c, &mongodoc.BaseEntity{
		URL:    baseURL(id2),
		User:   "who",
		Name:   "rails",
		Public: true,
		ACLs: mongodoc.ACL{
			Write: []string{"dalek"},
		},
	})
}

func (s *migrationsSuite) TestPopulateWriteACLSomeUpdates(c *gc.C) {
	s.patchMigrations(c, getMigrations("write acl creation"))
	// Store entities with and without bases in the db
	id1 := charm.MustParseReference("~dalek/utopic/django-42")
	id2 := charm.MustParseReference("~dalek/utopic/django-47")
	id3 := charm.MustParseReference("~jean-luc/precise/postgres-0")
	s.insertEntity(c, id1, "django", 1)
	s.insertEntity(c, id2, "django", 2)
	s.insertEntity(c, id3, "postgres", 3)
	baseId1 := baseURL(id1)
	baseId3 := baseURL(id3)
	s.insertBaseEntity(c, baseId1, nil)
	s.insertBaseEntity(c, baseId3, &mongodoc.ACL{
		Write: []string{"benjamin"},
	})

	// Start the server.
	err := s.newServer(c)
	c.Assert(err, gc.IsNil)

	// Ensure missing write permissions have been populated correctly.
	s.checkCount(c, s.db.BaseEntities(), 2)
	s.checkBaseEntity(c, &mongodoc.BaseEntity{
		URL:    baseId1,
		User:   "dalek",
		Name:   "django",
		Public: true,
		ACLs: mongodoc.ACL{
			Write: []string{"dalek"},
		},
	})
	s.checkBaseEntity(c, &mongodoc.BaseEntity{
		URL:    baseId3,
		User:   "jean-luc",
		Name:   "postgres",
		Public: true,
		ACLs: mongodoc.ACL{
			Write: []string{"benjamin"},
		},
	})
}

func (s *migrationsSuite) checkEntity(c *gc.C, expectEntity *mongodoc.Entity) {
	var entity mongodoc.Entity
	err := s.db.Entities().FindId(expectEntity.URL).One(&entity)
	c.Assert(err, gc.IsNil)

	// Ensure that the denormalized fields are now present, and the previously
	// existing fields are still there.
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

func (s *migrationsSuite) insertEntity(c *gc.C, id *charm.Reference, name string, size int64) {
	entity := &mongodoc.Entity{
		URL:     id,
		BaseURL: baseURL(id),
		Name:    name,
		Size:    size,
	}
	err := s.db.Entities().Insert(entity)
	c.Assert(err, gc.IsNil)

	// Remove the denormalized fields if required.
	if name != "" {
		return
	}
	err = s.db.Entities().UpdateId(id, bson.D{{
		"$unset", bson.D{
			{"user", true},
			{"name", true},
			{"revision", true},
			{"series", true},
		},
	}})
	c.Assert(err, gc.IsNil)
}

func (s *migrationsSuite) insertBaseEntity(c *gc.C, id *charm.Reference, acls *mongodoc.ACL) {
	entity := &mongodoc.BaseEntity{
		URL:    id,
		Name:   id.Name,
		User:   id.User,
		Public: true,
	}
	if acls != nil {
		entity.ACLs = *acls
	}
	err := s.db.BaseEntities().Insert(entity)
	c.Assert(err, gc.IsNil)

	// Unset the ACL fields if required to simulate a migration.
	if acls == nil {
		err = s.db.BaseEntities().UpdateId(id, bson.D{{"$unset",
			bson.D{{"acls", true}},
		}})
		c.Assert(err, gc.IsNil)
	}
}
