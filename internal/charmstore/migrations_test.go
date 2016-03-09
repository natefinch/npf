// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore // import "gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"

import (
	"net/http"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/mgo.v2"

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

func (s *migrationsSuite) newServer(c *gc.C) error {
	apiHandler := func(p *Pool, config ServerParams, _ string) HTTPCloseHandler {
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
	s.PatchValue(&migrations, ms)
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

func (s *migrationsSuite) checkExecuted(c *gc.C, expected ...mongodoc.MigrationName) {
	var obtained []mongodoc.MigrationName
	var doc mongodoc.Migration
	if err := s.db.Migrations().Find(nil).One(&doc); err != mgo.ErrNotFound {
		c.Assert(err, gc.IsNil)
		obtained = doc.Executed
	}
	c.Assert(obtained, jc.SameContents, expected)
}
