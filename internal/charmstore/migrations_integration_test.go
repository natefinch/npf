package charmstore

import (
	"flag"
	"net/http"

	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	jujutesting "github.com/juju/testing"

	"gopkg.in/juju/charmstore.v5-unstable/internal/storetesting"
)

type migrationsIntegrationSuite struct {
	commonSuite
}

var _ = gc.Suite(&migrationsIntegrationSuite{})

const earliestDeployedVersion = "4.4.3"

var dumpMigrationHistoryFlag = flag.Bool("dump-migration-history", false, "dump migration history to file")

func (s *migrationsIntegrationSuite) SetUpSuite(c *gc.C) {
	if *dumpMigrationHistoryFlag {
		s.dump(c)
	}
	s.commonSuite.SetUpSuite(c)
}

func (s *migrationsIntegrationSuite) dump(c *gc.C) {
	// We can't use the usual s.Session because we're using
	// commonSuite which uses IsolationSuite which hides the
	// environment variables which are needed for
	// dumpMigrationHistory to run.
	session, err := jujutesting.MgoServer.Dial()
	c.Assert(err, gc.IsNil)
	defer session.Close()
	err = dumpMigrationHistory(session, earliestDeployedVersion, migrationHistory)
	c.Assert(err, gc.IsNil)
}

var migrationHistory = []versionSpec{{
	version: "4.1.5",
	update: func(db *mgo.Database, csv *charmStoreVersion) error {
		err := csv.Upload("v4", []uploadSpec{{
			id:     "0 ~charmers/precise/promulgated-0",
			entity: storetesting.NewCharm(nil),
		}, {
			id:     "~bob/trusty/nonpromulgated-0",
			entity: storetesting.NewCharm(nil),
		}, {
			id: "0 ~charmers/bundle/promulgatedbundle-0",
			entity: storetesting.NewBundle(&charm.BundleData{
				Services: map[string]*charm.ServiceSpec{
					"promulgated": {
						Charm: "promulgated",
					},
				},
			}),
		}, {
			id: "~charmers/bundle/nonpromulgatedbundle-0",
			entity: storetesting.NewBundle(&charm.BundleData{
				Services: map[string]*charm.ServiceSpec{
					"promulgated": {
						Charm: "promulgated",
					},
				},
			}),
		}})
		if err != nil {
			return errgo.Mask(err)
		}
		if err := csv.Put("/v4/promulgated/meta/perm", params.PermRequest{
			Read:  []string{"everyone"},
			Write: []string{"alice", "bob", "charmers"},
		}); err != nil {
			return errgo.Mask(err)
		}
		if err := csv.Put("/v4/~bob/nonpromulgated/meta/perm", params.PermRequest{
			Read:  []string{"bobgroup"},
			Write: []string{"bob", "someoneelse"},
		}); err != nil {
			return errgo.Mask(err)
		}
		// Expected contents:
		//	~charmers/precise/promulgated-0 (precise/promulgated-0)
		//		ACLs:
		//			read: everyone
		//			write: alice, bob, charmers
		//	~bob/trusty/nonpromulgated-0
		//		ACLs:
		//			read: bob
		//			write: bob, someoneelse
		//	~charmers/bundle/promulgatedbundle-0 (bundle/promulgatedbundle-0)
		//		ACLs:
		//			read: charmers
		//			write: charmers
		//	~charmers/bundle/nonpromulgatedbundle-0
		//		ACLs:
		//			read: charmers
		//			write: charmers
		return nil
	},
}, {
	// Multi-series charms.
	// Development channel + ACLs
	version: "4.3.0",
	update: func(db *mgo.Database, csv *charmStoreVersion) error {
		err := csv.Upload("v4", []uploadSpec{{
			id:      "~charmers/multiseries",
			usePost: true, // Note: PUT doesn't work on multi-series.
			entity: storetesting.NewCharm(&charm.Meta{
				Series: []string{"precise", "trusty"},
			}),
		}, {
			// This triggers the bug where we created a base
			// entity with a bogus "development" channel in the URL.
			id:      "~charmers/development/precise/promulgated",
			usePost: true,
			entity: storetesting.NewCharm(&charm.Meta{
				Name: "different",
			}),
		}})
		if err != nil {
			return errgo.Mask(err)
		}

		// Sanity check that we really did trigger the bug.
		err = db.C("entities").Find(bson.D{{
			"promulgated-url", "cs:development/precise/promulgated-1",
		}}).One(new(interface{}))
		if err != nil {
			return errgo.Notef(err, "we don't seem to have triggered the bug")
		}

		if err := csv.Put("/v4/development/promulgated/meta/perm", params.PermRequest{
			Read:  []string{"charmers"},
			Write: []string{"charmers"},
		}); err != nil {
			return errgo.Mask(err)
		}
		// Expected contents:
		//
		// From previous version:
		//
		//	~charmers/precise/promulgated-0 (precise/promulgated-0)
		//		development: false
		//		ACLs:
		//			read: everyone
		//			write: alice, bob, charmers
		//		DevelopmentACLs:
		//			read: charmers
		//			write: charmers
		//	~bob/trusty/nonpromulgated-0
		//		development: false
		//		ACLs:
		//			read: bobgroup
		//			write: bob, someoneelse
		//		DevelopmentACLs:
		//			read: bob
		//			write: bob, someoneelse
		//	~charmers/bundle/promulgatedbundle-0 (bundle/promulgatedbundle-0)
		//		development: false
		//		ACLs:
		//			read: charmers
		//			write: charmers
		//		DevelopmentACLs:
		//			read: charmers
		//			write: charmers
		//	~charmers/bundle/nonpromulgatedbundle-0
		//		development: false
		//		ACLs:
		//			read: charmers
		//			write: charmers
		//		DevelopmentACLs:
		//			read: charmers
		//			write: charmers
		//
		// Added in this update:
		//
		//	~charmers/multiseries-0
		//		development: true
		//		ACLs:
		//			read: charmers
		//			write: charmers
		//		DevelopmentACLs:
		//			read: charmers
		//			write: charmers
		//	~charmers/precise/promulgated-1 (cs:development/precise/promulgated-1)
		//		development: true
		//		ACLs:
		//			read: charmers
		//			write: charmers
		//		DevelopmentACLs:
		//			read: charmers
		//			write: charmers
		//
		return nil
	},
}, {
	// V5 API.
	// Fix bogus promulgated URL.
	// V4 multi-series compatibility (this didn't work).
	version: "4.4.3",
	update: func(db *mgo.Database, csv *charmStoreVersion) error {
		err := csv.Upload("v5", []uploadSpec{{
			id:      "~charmers/multiseries",
			usePost: true,
			entity: storetesting.NewCharm(&charm.Meta{
				Series: []string{"precise", "trusty", "wily"},
			}),
		}})
		if err != nil {
			return errgo.Mask(err)
		}
		// Expected contents:
		//
		// From previous version:
		//
		//	~charmers/precise/promulgated-0 (precise/promulgated-0)
		//		development: false
		//		has compatibility blob: false
		//		ACLs:
		//			read: everyone
		//			write: alice, bob, charmers
		//		DevelopmentACLs:
		//			read: charmers
		//			write: charmers
		//	~bob/trusty/nonpromulgated-0
		//		development: false
		//		has compatibility blob: false
		//		ACLs:
		//			read: bobgroup
		//			write: bob, someoneelse
		//		DevelopmentACLs:
		//			read: bob
		//			write: bob, someoneelse
		//	~charmers/bundle/promulgatedbundle-0 (bundle/promulgatedbundle-0)
		//		development: false
		//		has compatibility blob: false
		//		ACLs:
		//			read: charmers
		//			write: charmers
		//		DevelopmentACLs:
		//			read: charmers
		//			write: charmers
		//	~charmers/bundle/nonpromulgatedbundle-0
		//		development: false
		//		has compatibility blob: false
		//		ACLs:
		//			read: charmers
		//			write: charmers
		//		DevelopmentACLs:
		//			read: charmers
		//			write: charmers
		//	~charmers/multiseries-0
		//		development: true
		//		has compatibility blob: false
		//		ACLs:
		//			read: charmers
		//			write: charmers
		//		DevelopmentACLs:
		//			read: charmers
		//			write: charmers
		//	~charmers/precise/promulgated-1 (cs:precise/promulgated-1)
		//		development: true
		//		has compatibility blob: false
		//		ACLs:
		//			read: charmers
		//			write: charmers
		//		DevelopmentACLs:
		//			read: charmers
		//			write: charmers
		//
		// Added in this update:
		//
		//	~charmers/multiseries-1
		//		development: true
		//		has compatibility blob: true
		//		ACLs:
		//			read: charmers
		//			write: charmers
		//		DevelopmentACLs:
		//			read: charmers
		//			write: charmers
		return nil
	},
}}

func (s *migrationsIntegrationSuite) TestMigrationFromDump(c *gc.C) {
	db := s.Session.DB("juju_test")
	err := createDatabaseAtVersion(db, migrationHistory[len(migrationHistory)-1].version)
	c.Assert(err, gc.IsNil)
	err = s.runMigrations(db)
	c.Assert(err, gc.IsNil)

	store := s.newStore(c, false)
	defer store.Close()

	// TODO: lots more checks for all the properties we expect. This is
	// just an initial smoke test.
	entity, err := store.FindBestEntity(charm.MustParseURL("development/precise/promulgated"), nil)
	c.Assert(err, gc.IsNil)
	c.Assert(entity.PromulgatedURL.String(), gc.Equals, "cs:precise/promulgated-1")
}

// runMigrations starts a new server which will cause all migrations
// to be triggered.
func (s *migrationsIntegrationSuite) runMigrations(db *mgo.Database) error {
	apiHandler := func(p *Pool, config ServerParams, _ string) HTTPCloseHandler {
		return nopCloseHandler{http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {})}
	}
	srv, err := NewServer(db, nil, serverParams, map[string]NewAPIHandlerFunc{
		"version1": apiHandler,
	})
	if err == nil {
		srv.Close()
	}
	return err
}
