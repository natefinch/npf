// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore

import (
	"net/http"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v4"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/storetesting"
)

type migrationsSuite struct {
	storetesting.IsolatedMgoSuite
	db StoreDatabase
}

var _ = gc.Suite(&migrationsSuite{})

func (s *migrationsSuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	s.db = StoreDatabase{s.Session.DB("migration-testing")}
}

func (s *migrationsSuite) newServer(c *gc.C) {
	apiHandler := func(store *Store, config ServerParams) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {})
	}
	_, err := NewServer(s.db.Database, nil, serverParams, map[string]NewAPIHandlerFunc{
		"version1": apiHandler,
	})
	c.Assert(err, gc.IsNil)
}

func (s *migrationsSuite) TestDenormalizeEntityIds(c *gc.C) {
	// Store entities with missing name in the db.
	id1 := charm.MustParseReference("trusty/django-42")
	id2 := charm.MustParseReference("~who/utopic/rails-47")
	s.insertEntity(c, id1, "", 12)
	s.insertEntity(c, id2, "", 13)

	// Start the server.
	s.newServer(c)

	// Ensure entities have been updated correctly.
	s.checkCount(c, 2)
	s.checkEntity(c, &mongodoc.Entity{
		URL:      id1,
		User:     "",
		Name:     "django",
		Revision: 42,
		Series:   "trusty",
		Size:     12,
	})
	s.checkEntity(c, &mongodoc.Entity{
		URL:      id2,
		User:     "who",
		Name:     "rails",
		Revision: 47,
		Series:   "utopic",
		Size:     13,
	})
}

func (s *migrationsSuite) TestDenormalizeEntityIdsNoEntities(c *gc.C) {
	// Start the server.
	s.newServer(c)

	// Ensure no new entities are added in the process.
	s.checkCount(c, 0)
}

func (s *migrationsSuite) TestDenormalizeEntityIdsNoUpdates(c *gc.C) {
	// Store entities with a name in the db.
	id1 := charm.MustParseReference("trusty/django-42")
	id2 := charm.MustParseReference("~who/utopic/rails-47")
	s.insertEntity(c, id1, "django", 21)
	s.insertEntity(c, id2, "rails2", 22)

	// Start the server.
	s.newServer(c)

	// Ensure entities have been updated correctly.
	s.checkCount(c, 2)
	s.checkEntity(c, &mongodoc.Entity{
		URL:  id1,
		User: "",
		Name: "django",
		// Since the name field already existed, the Revision and Series fields
		// have not been populated.
		Size: 21,
	})
	s.checkEntity(c, &mongodoc.Entity{
		URL: id2,
		// The name is left untouched (even if it's obviously wrong).
		Name: "rails2",
		// Since the name field already existed, the User, Revision and Series
		// fields have not been populated.
		Size: 22,
	})
}

func (s *migrationsSuite) TestDenormalizeEntityIdsSomeUpdates(c *gc.C) {
	// Store entities with and without names in the db
	id1 := charm.MustParseReference("~dalek/utopic/django-42")
	id2 := charm.MustParseReference("~dalek/utopic/django-47")
	id3 := charm.MustParseReference("precise/postgres-0")
	s.insertEntity(c, id1, "", 1)
	s.insertEntity(c, id2, "django", 2)
	s.insertEntity(c, id3, "", 3)

	// Start the server.
	s.newServer(c)

	// Ensure entities have been updated correctly.
	s.checkCount(c, 3)
	s.checkEntity(c, &mongodoc.Entity{
		URL:      id1,
		User:     "dalek",
		Name:     "django",
		Revision: 42,
		Series:   "utopic",
		Size:     1,
	})
	s.checkEntity(c, &mongodoc.Entity{
		URL:  id2,
		Name: "django",
		Size: 2,
	})
	s.checkEntity(c, &mongodoc.Entity{
		URL:      id3,
		User:     "",
		Name:     "postgres",
		Revision: 0,
		Series:   "precise",
		Size:     3,
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

func (s *migrationsSuite) checkCount(c *gc.C, expectCount int) {
	count, err := s.db.Entities().Count()
	c.Assert(err, gc.IsNil)
	c.Assert(count, gc.Equals, expectCount)
}

func (s *migrationsSuite) insertEntity(c *gc.C, id *charm.Reference, name string, size int64) {
	entity := &mongodoc.Entity{
		URL:  id,
		Name: name,
		Size: size,
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
