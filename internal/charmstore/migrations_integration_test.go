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
			id:            "~charmers/precise/promulgated-0",
			promulgatedId: "precise/promulgated-0",
			entity:        storetesting.NewCharm(nil),
		}, {
			id:     "~bob/trusty/nonpromulgated-0",
			entity: storetesting.NewCharm(nil),
		}, {
			id:            "~charmers/bundle/promulgatedbundle-0",
			promulgatedId: "bundle/promulgatedbundle-0",
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
			id: "~charmers/multiseries",
			//  Note: PUT doesn't work on multi-series.
			usePost: true,
			entity: storetesting.NewCharm(&charm.Meta{
				Series: []string{"precise", "trusty", "utopic"},
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
		}, {
			id:     "~someone/precise/southerncharm-0",
			entity: storetesting.NewCharm(nil),
		}, {
			id:     "~someone/development/precise/southerncharm-3",
			entity: storetesting.NewCharm(nil),
		}, {
			id:     "~someone/development/trusty/southerncharm-5",
			entity: storetesting.NewCharm(nil),
		}, {
			id:     "~someone/trusty/southerncharm-6",
			entity: storetesting.NewCharm(nil),
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
		isDevelopment(true),
		isStable(true),
	},
}, {
	id: "~charmers/precise/promulgated-1",
	checkers: []entityChecker{
		hasPromulgatedRevision(1),
		hasCompatibilityBlob(false),
		isDevelopment(true),
		isStable(false),
	},
}, {
	id: "~bob/trusty/nonpromulgated-0",
	checkers: []entityChecker{
		hasPromulgatedRevision(-1),
		hasCompatibilityBlob(false),
		isDevelopment(true),
		isStable(true),
	},
}, {
	id: "~charmers/bundle/promulgatedbundle-0",
	checkers: []entityChecker{
		hasPromulgatedRevision(0),
		hasCompatibilityBlob(false),
		isDevelopment(true),
		isStable(true),
	},
}, {
	id: "~charmers/bundle/nonpromulgatedbundle-0",
	checkers: []entityChecker{
		hasPromulgatedRevision(-1),
		hasCompatibilityBlob(false),
		isDevelopment(true),
		isStable(true),
	},
}, {
	id: "~charmers/multiseries-0",
	checkers: []entityChecker{
		hasPromulgatedRevision(-1),
		hasCompatibilityBlob(true),
		isDevelopment(true),
		isStable(true),
	},
}, {
	id: "~charmers/multiseries-1",
	checkers: []entityChecker{
		hasPromulgatedRevision(-1),
		hasCompatibilityBlob(true),
		isDevelopment(true),
		isStable(true),
	},
}, {
	id: "~someone/precise/southerncharm-0",
	checkers: []entityChecker{
		hasPromulgatedRevision(-1),
		hasCompatibilityBlob(false),
		isDevelopment(true),
		isStable(true),
	},
}, {
	id: "~someone/precise/southerncharm-3",
	checkers: []entityChecker{
		hasPromulgatedRevision(-1),
		hasCompatibilityBlob(false),
		isDevelopment(true),
		isStable(false),
	},
}, {
	id: "~someone/trusty/southerncharm-5",
	checkers: []entityChecker{
		hasPromulgatedRevision(-1),
		hasCompatibilityBlob(false),
		isDevelopment(true),
		isStable(false),
	},
}, {
	id: "~someone/trusty/southerncharm-6",
	checkers: []entityChecker{
		hasPromulgatedRevision(-1),
		hasCompatibilityBlob(false),
		isDevelopment(true),
		isStable(true),
	},
}}

var migrationFromDumpBaseEntityTests = []struct {
	id       string
	checkers []baseEntityChecker
}{{
	id: "cs:~charmers/promulgated",
	checkers: []baseEntityChecker{
		isPromulgated(true),
		hasACLs(map[params.Channel]mongodoc.ACL{
			params.UnpublishedChannel: {
				Read:  []string{"charmers"},
				Write: []string{"charmers"},
			},
			params.DevelopmentChannel: {
				Read:  []string{"charmers"},
				Write: []string{"charmers"},
			},
			params.StableChannel: {
				Read:  []string{"everyone"},
				Write: []string{"alice", "bob", "charmers"},
			},
		}),
		hasChannelEntities(map[params.Channel]map[string]*charm.URL{
			params.DevelopmentChannel: {
				"precise": charm.MustParseURL("~charmers/precise/promulgated-1"),
			},
			params.StableChannel: {
				"precise": charm.MustParseURL("~charmers/precise/promulgated-0"),
			},
		}),
	},
}, {
	id: "cs:~bob/nonpromulgated",
	checkers: []baseEntityChecker{
		isPromulgated(false),
		hasACLs(map[params.Channel]mongodoc.ACL{
			params.UnpublishedChannel: {
				Read:  []string{"bobgroup"},
				Write: []string{"bob", "someoneelse"},
			},
			params.DevelopmentChannel: {
				Read:  []string{"bobgroup"},
				Write: []string{"bob", "someoneelse"},
			},
			params.StableChannel: {
				Read:  []string{"bobgroup"},
				Write: []string{"bob", "someoneelse"},
			},
		}),
		hasChannelEntities(map[params.Channel]map[string]*charm.URL{
			params.DevelopmentChannel: {
				"trusty": charm.MustParseURL("~bob/trusty/nonpromulgated-0"),
			},
			params.StableChannel: {
				"trusty": charm.MustParseURL("~bob/trusty/nonpromulgated-0"),
			},
		}),
	},
}, {
	id: "~charmers/promulgatedbundle",
	checkers: []baseEntityChecker{
		isPromulgated(true),
		hasAllACLs("charmers"),
		hasChannelEntities(map[params.Channel]map[string]*charm.URL{
			params.DevelopmentChannel: {
				"bundle": charm.MustParseURL("~charmers/bundle/promulgatedbundle-0"),
			},
			params.StableChannel: {
				"bundle": charm.MustParseURL("~charmers/bundle/promulgatedbundle-0"),
			},
		}),
	},
}, {
	id: "cs:~charmers/nonpromulgatedbundle",
	checkers: []baseEntityChecker{
		isPromulgated(false),
		hasAllACLs("charmers"),
		hasChannelEntities(map[params.Channel]map[string]*charm.URL{
			params.DevelopmentChannel: {
				"bundle": charm.MustParseURL("~charmers/bundle/nonpromulgatedbundle-0"),
			},
			params.StableChannel: {
				"bundle": charm.MustParseURL("~charmers/bundle/nonpromulgatedbundle-0"),
			},
		}),
	},
}, {
	id: "cs:~charmers/multiseries",
	checkers: []baseEntityChecker{
		isPromulgated(false),
		hasAllACLs("charmers"),
		hasChannelEntities(map[params.Channel]map[string]*charm.URL{
			params.DevelopmentChannel: {
				"precise": charm.MustParseURL("~charmers/multiseries-1"),
				"trusty":  charm.MustParseURL("~charmers/multiseries-1"),
				"utopic":  charm.MustParseURL("~charmers/multiseries-0"),
				"wily":    charm.MustParseURL("~charmers/multiseries-1"),
			},
			params.StableChannel: {
				"precise": charm.MustParseURL("~charmers/multiseries-1"),
				"trusty":  charm.MustParseURL("~charmers/multiseries-1"),
				"utopic":  charm.MustParseURL("~charmers/multiseries-0"),
				"wily":    charm.MustParseURL("~charmers/multiseries-1"),
			},
		}),
	},
}, {
	id: "cs:~someone/southerncharm",
	checkers: []baseEntityChecker{
		isPromulgated(false),
		hasAllACLs("someone"),
		hasChannelEntities(map[params.Channel]map[string]*charm.URL{
			params.DevelopmentChannel: {
				"precise": charm.MustParseURL("~someone/precise/southerncharm-3"),
				"trusty":  charm.MustParseURL("~someone/trusty/southerncharm-6"),
			},
			params.StableChannel: {
				"precise": charm.MustParseURL("~someone/precise/southerncharm-0"),
				"trusty":  charm.MustParseURL("~someone/trusty/southerncharm-6"),
			},
		}),
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

func stringInSlice(s string, ss []string) bool {
	for _, t := range ss {
		if s == t {
			return true
		}
	}
	return false
}

func checkBaseEntityInvariants(c *gc.C, e *mongodoc.BaseEntity, store *Store) {
	c.Assert(e.URL.Name, gc.Not(gc.Equals), "")
	c.Assert(e.URL.User, gc.Not(gc.Equals), "")

	c.Assert(e.URL, jc.DeepEquals, mongodoc.BaseURL(e.URL))
	c.Assert(e.User, gc.Equals, e.URL.User)
	c.Assert(e.Name, gc.Equals, e.URL.Name)

	// Check that each entity mentioned in ChannelEntities exists and has the
	// correct channel.
	for ch, seriesEntities := range e.ChannelEntities {
		c.Assert(ch, gc.Not(gc.Equals), params.UnpublishedChannel)
		for series, url := range seriesEntities {
			if url.Series != "" {
				c.Assert(url.Series, gc.Equals, series)
			}
			ce, err := store.FindEntity(MustParseResolvedURL(url.String()), nil)
			c.Assert(err, gc.IsNil)
			switch ch {
			case params.DevelopmentChannel:
				c.Assert(ce.Development, gc.Equals, true)
			case params.StableChannel:
				c.Assert(ce.Stable, gc.Equals, true)
			default:
				c.Fatalf("unknown channel %q found", ch)
			}
			if series != "bundle" && !stringInSlice(series, ce.SupportedSeries) {
				c.Fatalf("series %q not found in supported series %q", series, ce.SupportedSeries)
			}

		}
	}
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

func isStable(isStable bool) entityChecker {
	return func(c *gc.C, entity *mongodoc.Entity) {
		c.Assert(entity.Stable, gc.Equals, isStable)
	}
}

type baseEntityChecker func(c *gc.C, entity *mongodoc.BaseEntity)

func isPromulgated(isProm bool) baseEntityChecker {
	return func(c *gc.C, entity *mongodoc.BaseEntity) {
		c.Assert(entity.Promulgated, gc.Equals, mongodoc.IntBool(isProm))
	}
}

func hasACLs(acls map[params.Channel]mongodoc.ACL) baseEntityChecker {
	return func(c *gc.C, entity *mongodoc.BaseEntity) {
		c.Assert(entity.ChannelACLs, jc.DeepEquals, acls)
	}
}

func hasAllACLs(user string) baseEntityChecker {
	userACL := mongodoc.ACL{
		Read:  []string{user},
		Write: []string{user},
	}
	return hasACLs(map[params.Channel]mongodoc.ACL{
		params.UnpublishedChannel: userACL,
		params.DevelopmentChannel: userACL,
		params.StableChannel:      userACL,
	})
}

func hasChannelEntities(ce map[params.Channel]map[string]*charm.URL) baseEntityChecker {
	return func(c *gc.C, entity *mongodoc.BaseEntity) {
		c.Assert(entity.ChannelEntities, jc.DeepEquals, ce)
	}
}
