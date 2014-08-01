// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/juju/errgo"
	jujutesting "github.com/juju/testing"
	"gopkg.in/juju/charm.v3"
	charmtesting "gopkg.in/juju/charm.v3/testing"
	"gopkg.in/mgo.v2/bson"
	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/router"
	"github.com/juju/charmstore/internal/storetesting"
	"github.com/juju/charmstore/internal/v4"
	"github.com/juju/charmstore/params"
)

func TestPackage(t *testing.T) {
	jujutesting.MgoTestPackage(t, nil)
}

type APISuite struct {
	storetesting.IsolatedMgoSuite
	srv   http.Handler
	store *charmstore.Store
}

var _ = gc.Suite(&APISuite{})

func (s *APISuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	db := s.Session.DB("charmstore")
	s.store = charmstore.NewStore(db)
	srv, err := charmstore.NewServer(db, map[string]charmstore.NewAPIHandler{"v4": v4.New})
	c.Assert(err, gc.IsNil)
	s.srv = srv
}

type metaEndpoint struct {
	name       string
	exclusive  int
	bundleOnly bool
	get        func(*charmstore.Store, *charm.Reference) (interface{}, error)

	// checkURL holds one URL to sanity check data against.
	checkURL string
	// assertCheckData holds a function that will be used to check that
	// the get function returns sane data for checkURL.
	assertCheckData func(c *gc.C, data interface{})
}

const (
	charmOnly = iota + 1
	bundleOnly
)

var metaEndpoints = []metaEndpoint{{
	name:      "charm-config",
	exclusive: charmOnly,
	get:       entityFieldGetter("CharmConfig"),
	checkURL:  "cs:precise/wordpress-23",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data.(*charm.Config).Options["blog-title"].Default, gc.Equals, "My Title")
	},
}, {
	name:      "charm-metadata",
	exclusive: charmOnly,
	get:       entityFieldGetter("CharmMeta"),
	checkURL:  "cs:precise/wordpress-23",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data.(*charm.Meta).Summary, gc.Equals, "Blog engine")
	},
}, {
	name:      "bundle-metadata",
	exclusive: bundleOnly,
	get:       entityFieldGetter("BundleData"),
	checkURL:  "cs:bundle/wordpress-42",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data.(*charm.BundleData).Services["wordpress"].Charm, gc.Equals, "wordpress")
	},
}, {
	name:      "charm-actions",
	exclusive: charmOnly,
	get:       entityFieldGetter("CharmActions"),
	checkURL:  "cs:precise/dummy-10",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data.(*charm.Actions).ActionSpecs["snapshot"].Description, gc.Equals, "Take a snapshot of the database.")
	},
}}

// TestEndpointGet tries to ensure that the endpoint
// test data getters correspond with reality.
func (s *APISuite) TestEndpointGet(c *gc.C) {
	s.addTestEntities(c)
	for i, ep := range metaEndpoints {
		c.Logf("test %d: %s\n", i, ep.name)
		data, err := ep.get(s.store, mustParseReference(ep.checkURL))
		c.Assert(err, gc.IsNil)
		ep.assertCheckData(c, data)
	}
}

func (s *APISuite) TestArchive(c *gc.C) {
	assertNotImplemented(c, s.srv, "precise/wordpress-23/archive")
}

var testEntities = []string{
	// A stock charm.
	"cs:precise/wordpress-23",
	// A stock bundle.
	"cs:bundle/wordpress-42",
	// A charm with some actions.
	"cs:precise/dummy-10",
}

func (s *APISuite) addTestEntities(c *gc.C) []*charm.Reference {
	urls := make([]*charm.Reference, len(testEntities))
	for i, e := range testEntities {
		url := mustParseReference(e)
		if url.Series == "bundle" {
			s.addBundle(c, url.Name, e)
		} else {
			s.addCharm(c, url.Name, e)
		}
		urls[i] = url
	}
	return urls
}

func (s *APISuite) TestMetaEndpointsSingle(c *gc.C) {
	urls := s.addTestEntities(c)
	for i, ep := range metaEndpoints {
		c.Logf("test %d. %s", i, ep.name)
		tested := false
		for _, url := range urls {
			charmId := strings.TrimPrefix(url.String(), "cs:")
			storeURL := "http://0.1.2.3/v4/" + charmId + "/meta/" + ep.name
			expectData, err := ep.get(s.store, url)
			c.Assert(err, gc.IsNil)
			c.Logf("	expected data for %q: %#v", url, expectData)
			if isNull(expectData) {
				storetesting.AssertJSONCall(c, s.srv, "GET", storeURL, "", http.StatusInternalServerError, params.Error{
					Message: router.ErrDataNotFound.Error(),
				})
				continue
			}
			tested = true
			storetesting.AssertJSONCall(c, s.srv, "GET", storeURL, "", http.StatusOK, expectData)
		}
		if !tested {
			c.Errorf("endpoint %q is null for all endpoints, so is not properly tested", ep.name)
		}
	}
}

func isNull(v interface{}) bool {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(data) == "null"
}

func (s *APISuite) TestMetaEndpointsAny(c *gc.C) {
	urls := s.addTestEntities(c)
	for _, url := range urls {
		charmId := strings.TrimPrefix(url.String(), "cs:")
		var flags []string
		expectData := params.MetaAnyResponse{
			Id:   url,
			Meta: make(map[string]interface{}),
		}
		for _, ep := range metaEndpoints {
			flags = append(flags, "include="+ep.name)
			isBundle := url.Series == "bundle"
			if ep.exclusive != 0 && isBundle != (ep.exclusive == bundleOnly) {
				// endpoint not relevant.
				continue
			}
			val, err := ep.get(s.store, url)
			c.Assert(err, gc.IsNil)
			if val != nil {
				expectData.Meta[ep.name] = val
			}
		}
		storeURL := "http://0.1.2.3/v4/" + charmId + "/meta/any?" + strings.Join(flags, "&")
		storetesting.AssertJSONCall(c, s.srv, "GET", storeURL, "",
			http.StatusOK, expectData)
	}
}

// In this test we rely on the charm.v2 testing repo package and
// dummy charm that has actions included.
func (s *APISuite) TestMetaCharmActions(c *gc.C) {
	url, dummy := s.addCharm(c, "dummy", "cs:precise/dummy-10")
	storetesting.AssertJSONCall(c, s.srv,
		"GET", "http://0.1.2.3/v4/precise/dummy-10/meta/charm-actions", "",
		http.StatusOK, dummy.Actions())

	storetesting.AssertJSONCall(c, s.srv,
		"GET", "http://0.1.2.3/v4/precise/dummy-10/meta/any?include=charm-actions", "",
		http.StatusOK, params.MetaAnyResponse{
			Id: url,
			Meta: map[string]interface{}{
				"charm-actions": dummy.Actions(),
			},
		})
}

func (s *APISuite) TestBulkMeta(c *gc.C) {
	// We choose an arbitrary set of ids and metadata here, just to smoke-test
	// whether the meta/any logic is hooked up correctly.
	// Detailed tests for this feature are in the router package.

	_, wordpress := s.addCharm(c, "wordpress", "cs:precise/wordpress-23")
	_, mysql := s.addCharm(c, "mysql", "cs:precise/mysql-10")
	storetesting.AssertJSONCall(c, s.srv, "GET", "http://0.1.2.3/v4/meta/charm-metadata?id=precise/wordpress-23&id=precise/mysql-10", "", http.StatusOK, map[string]*charm.Meta{
		"precise/wordpress-23": wordpress.Meta(),
		"precise/mysql-10":     mysql.Meta(),
	})
}

func (s *APISuite) TestBulkMetaAny(c *gc.C) {
	// We choose an arbitrary set of metadata here, just to smoke-test
	// whether the meta/any logic is hooked up correctly.
	// Detailed tests for this feature are in the router package.

	wordpressURL, wordpress := s.addCharm(c, "wordpress", "cs:precise/wordpress-23")
	mysqlURL, mysql := s.addCharm(c, "mysql", "cs:precise/mysql-10")
	storetesting.AssertJSONCall(c, s.srv, "GET", "http://0.1.2.3/v4/meta/any?include=charm-metadata&include=charm-config&id=precise/wordpress-23&id=precise/mysql-10", "", http.StatusOK, map[string]params.MetaAnyResponse{
		"precise/wordpress-23": {
			Id: wordpressURL,
			Meta: map[string]interface{}{
				"charm-config":   wordpress.Config(),
				"charm-metadata": wordpress.Meta(),
			},
		},
		"precise/mysql-10": {
			Id: mysqlURL,
			Meta: map[string]interface{}{
				"charm-config":   mysql.Config(),
				"charm-metadata": mysql.Meta(),
			},
		},
	})
}

func (s *APISuite) TestIdsAreResolved(c *gc.C) {
	// This is just testing that ResolveURL is actually
	// passed to the router. Given how Router is
	// defined, and the ResolveURL tests, this should
	// be sufficient to "join the dots".
	_, wordpress := s.addCharm(c, "wordpress", "cs:precise/wordpress-23")
	storetesting.AssertJSONCall(c, s.srv, "GET", "http://0.1.2.3/v4/wordpress/meta/charm-metadata", "", http.StatusOK, wordpress.Meta())
}

func (s *APISuite) TestMetaCharmNotFound(c *gc.C) {
	expected := params.Error{Message: router.ErrNotFound.Error()}
	for _, ep := range metaEndpoints {
		storetesting.AssertJSONCall(c, s.srv, "GET", "http://0.1.2.3/v4/precise/wordpress-23/meta/"+ep.name, "", http.StatusInternalServerError, expected)
	}
}

var resolveURLTests = []struct {
	url      string
	expect   string
	notFound bool
}{{
	url:    "wordpress",
	expect: "cs:trusty/wordpress-25",
}, {
	url:    "precise/wordpress",
	expect: "cs:precise/wordpress-24",
}, {
	url:    "utopic/bigdata",
	expect: "cs:utopic/bigdata-10",
}, {
	url:    "bigdata",
	expect: "cs:utopic/bigdata-10",
}, {
	url:    "wordpress-24",
	expect: "cs:trusty/wordpress-24",
}, {
	url:    "bundlelovin",
	expect: "cs:bundle/bundlelovin-10",
}, {
	url:      "wordpress-26",
	notFound: true,
}, {
	url:      "foo",
	notFound: true,
}, {
	url:      "trusty/bigdata",
	notFound: true,
}}

func (s *APISuite) TestResolveURL(c *gc.C) {
	s.addCharm(c, "wordpress", "cs:precise/wordpress-23")
	s.addCharm(c, "wordpress", "cs:precise/wordpress-24")
	s.addCharm(c, "wordpress", "cs:trusty/wordpress-24")
	s.addCharm(c, "wordpress", "cs:trusty/wordpress-25")
	s.addCharm(c, "wordpress", "cs:utopic/wordpress-10")
	s.addCharm(c, "wordpress", "cs:saucy/bigdata-99")
	s.addCharm(c, "wordpress", "cs:utopic/bigdata-10")
	s.addCharm(c, "wordpress", "cs:bundle/bundlelovin-10")
	s.addCharm(c, "wordpress", "cs:bundle/wordpress-10")

	for i, test := range resolveURLTests {
		c.Logf("test %d: %s", i, test.url)
		url := mustParseReference(test.url)
		err := v4.ResolveURL(s.store, url)
		if test.notFound {
			c.Assert(err, gc.ErrorMatches, `no matching charm or bundle for ".*"`)
			continue
		}
		c.Assert(err, gc.IsNil)
		c.Assert(url.String(), gc.Equals, test.expect)
	}
}

func assertNotImplemented(c *gc.C, h http.Handler, path string) {
	storetesting.AssertJSONCall(c, h, "GET", "http://0.1.2.3/v4/"+path, "", http.StatusInternalServerError, params.Error{
		Message: "method not implemented",
	})
}

func entityFieldGetter(fieldName string) func(*charmstore.Store, *charm.Reference) (interface{}, error) {
	return entityGetter(func(entity *mongodoc.Entity) interface{} {
		field := reflect.ValueOf(entity).Elem().FieldByName(fieldName)
		if !field.IsValid() {
			panic(errgo.Newf("entity has no field %q", fieldName))
		}
		return field.Interface()
	})
}

func entityGetter(get func(*mongodoc.Entity) interface{}) func(*charmstore.Store, *charm.Reference) (interface{}, error) {
	return func(store *charmstore.Store, url *charm.Reference) (interface{}, error) {
		var doc mongodoc.Entity
		err := store.DB.Entities().Find(bson.D{{"_id", url}}).One(&doc)
		if err != nil {
			return nil, errgo.Mask(err)
		}
		return get(&doc), nil
	}
}

func (s *APISuite) addCharm(c *gc.C, charmName, curl string) (*charm.Reference, charm.Charm) {
	url := mustParseReference(curl)
	wordpress := charmtesting.Charms.CharmDir(charmName)
	err := s.store.AddCharm(url, wordpress)
	c.Assert(err, gc.IsNil)
	return url, wordpress
}

func (s *APISuite) addBundle(c *gc.C, bundleName string, curl string) (*charm.Reference, charm.Bundle) {
	url := mustParseReference(curl)
	bundle := charmtesting.Charms.BundleDir(bundleName)
	err := s.store.AddBundle(url, bundle)
	c.Assert(err, gc.IsNil)
	return url, bundle
}

func mustParseReference(url string) *charm.Reference {
	ref, err := charm.ParseReference(url)
	if err != nil {
		panic(err)
	}
	return ref
}
