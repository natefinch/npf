package charmstore

import (
	"flag"
	"net/http"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"
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
		if err := csv.Put("/v4/~charmers/precise/promulgated/meta/perm", params.PermRequest{
			Read:  []string{"everyone"},
			Write: []string{"alice", "bob", "charmers"},
		}); err != nil {
			return errgo.Mask(err)
		}
		if err := csv.Put("/v4/~bob/trusty/nonpromulgated/meta/perm", params.PermRequest{
			Read:  []string{"bobgroup"},
			Write: []string{"bob", "someoneelse"},
		}); err != nil {
			return errgo.Mask(err)
		}

		return nil
	},
}, {
	// Multi-series charms.
	// Development channel + ACLs
	version: "4.3.0",
	update: func(db *mgo.Database, csv *charmStoreVersion) error {
		err := csv.Upload("v4", []uploadSpec{{
			// Uploads to ~charmers/multiseries-0
			id:      "~charmers/multiseries",
			usePost: true, // Note: PUT doesn't work on multi-series.
			entity: storetesting.NewCharm(&charm.Meta{
				Series: []string{"precise", "trusty"},
			}),
		}, {
			// This triggers the bug where we created a base
			// entity with a bogus "development" channel in the URL.
			// Uploads to ~charmers/precise/promulgated-1
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
		return nil
	},
}, {
	// V5 API.
	// Fix bogus promulgated URL.
	// V4 multi-series compatibility (this didn't work).
	version: "4.4.3",
	update: func(db *mgo.Database, csv *charmStoreVersion) error {
		err := csv.Upload("v5", []uploadSpec{{
			// Uploads to ~charmers/multiseries-1
			id:      "~charmers/multiseries",
			usePost: true,
			entity: storetesting.NewCharm(&charm.Meta{
				Series: []string{"precise", "trusty", "wily"},
			}),
		}})
		if err != nil {
			return errgo.Mask(err)
		}
		return nil
	},
}}

var migrationFromDumpEntityTests = []struct {
	id       string
	checkers []entityChecker
}{{
	id: "~charmers/precise/promulgated-0",
	checkers: []entityChecker{
		hasPromulgatedRevision(0),
		hasCompatibilityBlob(false),
		isDevelopment(false),
	},
}, {
	id: "~bob/trusty/nonpromulgated-0",
	checkers: []entityChecker{
		hasPromulgatedRevision(-1),
		hasCompatibilityBlob(false),
		isDevelopment(false),
	},
}, {
	id: "~charmers/bundle/promulgatedbundle-0",
	checkers: []entityChecker{
		hasPromulgatedRevision(0),
		hasCompatibilityBlob(false),
		isDevelopment(false),
	},
}, {
	id: "~charmers/bundle/nonpromulgatedbundle-0",
	checkers: []entityChecker{
		hasPromulgatedRevision(-1),
		hasCompatibilityBlob(false),
		isDevelopment(false),
	},
}, {
	id: "~charmers/multiseries-0",
	checkers: []entityChecker{
		hasPromulgatedRevision(-1),
		hasCompatibilityBlob(true),
		isDevelopment(false),
	},
}, {
	id: "~charmers/precise/promulgated-1",
	checkers: []entityChecker{
		hasPromulgatedRevision(1),
		hasCompatibilityBlob(false),
		isDevelopment(true),
	},
}, {
	id: "~charmers/multiseries-1",
	checkers: []entityChecker{
		hasPromulgatedRevision(-1),
		hasCompatibilityBlob(true),
		isDevelopment(false),
	},
}}

var migrationFromDumpBaseEntityTests = []struct {
	id       string
	checkers []baseEntityChecker
}{{
	id: "cs:~charmers/promulgated",
	checkers: []baseEntityChecker{
		isPromulgated(true),
		hasACLs(mongodoc.ACL{
			Read:  []string{"everyone"},
			Write: []string{"alice", "bob", "charmers"},
		}),
		hasDevelopmentACLs(mongodoc.ACL{
			Read:  []string{"charmers"},
			Write: []string{"charmers"},
		}),
	},
}, {
	id: "cs:~bob/nonpromulgated",
	checkers: []baseEntityChecker{
		isPromulgated(false),
		hasACLs(mongodoc.ACL{
			Read:  []string{"bobgroup"},
			Write: []string{"bob", "someoneelse"},
		}),
		hasDevelopmentACLs(mongodoc.ACL{
			Read:  []string{"bobgroup"},
			Write: []string{"bob", "someoneelse"},
		}),
	},
}, {
	id: "~charmers/promulgatedbundle",
	checkers: []baseEntityChecker{
		isPromulgated(true),
		hasAllACLs("charmers"),
	},
}, {
	id: "cs:~charmers/nonpromulgatedbundle",
	checkers: []baseEntityChecker{
		isPromulgated(false),
		hasAllACLs("charmers"),
	},
}, {
	id: "cs:~charmers/multiseries",
	checkers: []baseEntityChecker{
		isPromulgated(false),
		hasAllACLs("charmers"),
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

	checkAllEntityInvariants(c, store)

	for i, test := range migrationFromDumpEntityTests {
		c.Logf("test %d: entity %v", i, test.id)

		e, err := store.FindEntity(MustParseResolvedURL(test.id), nil)
		c.Assert(err, gc.IsNil)
		for j, check := range test.checkers {
			c.Logf("test %d: entity %v; check %d", i, test.id, j)
			check(c, e)
		}
	}

	for i, test := range migrationFromDumpBaseEntityTests {
		c.Logf("test %d: base entity %v", i, test.id)

		e, err := store.FindBaseEntity(charm.MustParseURL(test.id), nil)
		c.Assert(err, gc.IsNil)
		for j, check := range test.checkers {
			c.Logf("test %d: base entity %v; check %d", i, test.id, j)
			check(c, e)
		}
	}
}

func checkAllEntityInvariants(c *gc.C, store *Store) {
	var entities []*mongodoc.Entity

	err := store.DB.Entities().Find(nil).All(&entities)
	c.Assert(err, gc.IsNil)
	for _, e := range entities {
		c.Logf("check entity invariants %v", e.URL)
		checkEntityInvariants(c, e, store)
	}

	var baseEntities []*mongodoc.BaseEntity
	err = store.DB.BaseEntities().Find(nil).All(&baseEntities)
	c.Assert(err, gc.IsNil)
	for _, e := range baseEntities {
		c.Logf("check base entity invariants %v", e.URL)
		checkBaseEntityInvariants(c, e, store)
	}
}

func checkEntityInvariants(c *gc.C, e *mongodoc.Entity, store *Store) {
	// Basic "this must have some non-zero value" checks.
	c.Assert(e.URL.Name, gc.Not(gc.Equals), "")
	c.Assert(e.URL.Revision, gc.Not(gc.Equals), -1)
	c.Assert(e.URL.User, gc.Not(gc.Equals), "")

	c.Assert(e.PreV5BlobHash, gc.Not(gc.Equals), "")
	c.Assert(e.PreV5BlobHash256, gc.Not(gc.Equals), "")
	c.Assert(e.BlobHash, gc.Not(gc.Equals), "")
	c.Assert(e.BlobHash256, gc.Not(gc.Equals), "")
	c.Assert(e.Size, gc.Not(gc.Equals), 0)
	c.Assert(e.BlobName, gc.Not(gc.Equals), "")

	if e.UploadTime.IsZero() {
		c.Fatalf("zero upload time")
	}

	// URL denormalization checks.
	c.Assert(e.BaseURL, jc.DeepEquals, mongodoc.BaseURL(e.URL))
	c.Assert(e.URL.Name, gc.Equals, e.Name)
	c.Assert(e.URL.User, gc.Equals, e.User)
	c.Assert(e.URL.Revision, gc.Equals, e.Revision)
	c.Assert(e.URL.Series, gc.Equals, e.Series)

	if e.PromulgatedRevision != -1 {
		expect := *e.URL
		expect.User = ""
		expect.Revision = e.PromulgatedRevision
		c.Assert(e.PromulgatedURL, jc.DeepEquals, &expect)
	} else {
		c.Assert(e.PromulgatedURL, gc.IsNil)
	}

	// Multi-series vs single-series vs bundle checks.
	if e.URL.Series == "bundle" {
		c.Assert(e.BundleData, gc.NotNil)
		c.Assert(e.BundleCharms, gc.NotNil)
		c.Assert(e.BundleMachineCount, gc.NotNil)
		c.Assert(e.BundleUnitCount, gc.NotNil)

		c.Assert(e.SupportedSeries, gc.HasLen, 0)
		c.Assert(e.BlobHash, gc.Equals, e.PreV5BlobHash)
		c.Assert(e.Size, gc.Equals, e.PreV5BlobSize)
		c.Assert(e.BlobHash256, gc.Equals, e.PreV5BlobHash256)
	} else {
		c.Assert(e.CharmMeta, gc.NotNil)
		if e.URL.Series == "" {
			c.Assert(e.SupportedSeries, jc.DeepEquals, e.CharmMeta.Series)
			c.Assert(e.BlobHash, gc.Not(gc.Equals), e.PreV5BlobHash)
			c.Assert(e.Size, gc.Not(gc.Equals), e.PreV5BlobSize)
			c.Assert(e.BlobHash256, gc.Not(gc.Equals), e.PreV5BlobHash256)
		} else {
			c.Assert(e.SupportedSeries, jc.DeepEquals, []string{e.URL.Series})
			c.Assert(e.BlobHash, gc.Equals, e.PreV5BlobHash)
			c.Assert(e.Size, gc.Equals, e.PreV5BlobSize)
			c.Assert(e.BlobHash256, gc.Equals, e.PreV5BlobHash256)
		}
	}

	// Check that the blobs exist.
	r, err := store.OpenBlob(EntityResolvedURL(e))
	c.Assert(err, gc.IsNil)
	r.Close()
	r, err = store.OpenBlobPreV5(EntityResolvedURL(e))
	c.Assert(err, gc.IsNil)
	r.Close()

	// Check that the base entity exists.
	_, err = store.FindBaseEntity(e.URL, nil)
	c.Assert(err, gc.IsNil)
}

func checkBaseEntityInvariants(c *gc.C, e *mongodoc.BaseEntity, store *Store) {
	c.Assert(e.URL.Name, gc.Not(gc.Equals), "")
	c.Assert(e.URL.User, gc.Not(gc.Equals), "")

	c.Assert(e.URL, jc.DeepEquals, mongodoc.BaseURL(e.URL))
	c.Assert(e.User, gc.Equals, e.URL.User)
	c.Assert(e.Name, gc.Equals, e.URL.Name)
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

type entityChecker func(c *gc.C, entity *mongodoc.Entity)

func hasPromulgatedRevision(rev int) entityChecker {
	return func(c *gc.C, entity *mongodoc.Entity) {
		c.Assert(entity.PromulgatedRevision, gc.Equals, rev)
	}
}

func hasCompatibilityBlob(hasBlob bool) entityChecker {
	return func(c *gc.C, entity *mongodoc.Entity) {
		if hasBlob {
			c.Assert(entity.PreV5BlobHash, gc.Not(gc.Equals), entity.BlobHash)
		} else {
			c.Assert(entity.PreV5BlobHash, gc.Equals, entity.BlobHash)
		}
	}
}

func isDevelopment(isDev bool) entityChecker {
	return func(c *gc.C, entity *mongodoc.Entity) {
		c.Assert(entity.Development, gc.Equals, isDev)
	}
}

type baseEntityChecker func(c *gc.C, entity *mongodoc.BaseEntity)

func isPromulgated(isProm bool) baseEntityChecker {
	return func(c *gc.C, entity *mongodoc.BaseEntity) {
		c.Assert(entity.Promulgated, gc.Equals, mongodoc.IntBool(isProm))
	}
}

func hasACLs(acls mongodoc.ACL) baseEntityChecker {
	return func(c *gc.C, entity *mongodoc.BaseEntity) {
		c.Assert(entity.ACLs, jc.DeepEquals, acls)
	}
}

func hasDevelopmentACLs(acls mongodoc.ACL) baseEntityChecker {
	return func(c *gc.C, entity *mongodoc.BaseEntity) {
		c.Assert(entity.DevelopmentACLs, jc.DeepEquals, acls)
	}
}

func hasAllACLs(user string) baseEntityChecker {
	return hasDevelopmentACLs(mongodoc.ACL{
		Read:  []string{user},
		Write: []string{user},
	})
}
