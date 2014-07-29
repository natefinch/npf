// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4_test

import (
	"net/http"
	"testing"

	jujutesting "github.com/juju/testing"
	"gopkg.in/juju/charm.v2"
	charmtesting "gopkg.in/juju/charm.v2/testing"
	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore/internal/charmstore"
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

func (s *APISuite) addCharm(c *gc.C, charmName, curl string) (*charm.URL, charm.Charm) {
	url, err := charm.ParseURL(curl)
	c.Assert(err, gc.IsNil)
	wordpress := charmtesting.Charms.CharmDir(charmName)
	err = s.store.AddCharm(url, wordpress)
	c.Assert(err, gc.IsNil)
	return url, wordpress
}

func (s *APISuite) addBundle(c *gc.C, bundleName string, curl string) (*charm.URL, charm.Bundle) {
	url := charm.MustParseURL(curl)
	bundle := charmtesting.Charms.BundleDir(bundleName)
	err := s.store.AddBundle(url, bundle)
	c.Assert(err, gc.IsNil)
	return url, bundle
}

func (s *APISuite) TestArchive(c *gc.C) {
	assertNotImplemented(c, s.srv, "precise/wordpress-23/archive")
}

func (s *APISuite) TestMetaCharmConfig(c *gc.C) {
	url, wordpress := s.addCharm(c, "wordpress", "cs:precise/wordpress-23")
	storetesting.AssertJSONCall(c, s.srv, "GET", "http://0.1.2.3/v4/precise/wordpress-23/meta/charm-config", "", http.StatusOK, wordpress.Config())

	storetesting.AssertJSONCall(c, s.srv, "GET", "http://0.1.2.3/v4/precise/wordpress-23/meta/any?include=charm-config", "", http.StatusOK, &params.MetaAnyResponse{
		Id: url,
		Meta: map[string]interface{}{
			"charm-config": wordpress.Config(),
		},
	})
}

func (s *APISuite) TestMetaCharmMetadata(c *gc.C) {
	url, wordpress := s.addCharm(c, "wordpress", "cs:precise/wordpress-23")
	storetesting.AssertJSONCall(c, s.srv, "GET", "http://0.1.2.3/v4/precise/wordpress-23/meta/charm-metadata", "", http.StatusOK, wordpress.Meta())

	storetesting.AssertJSONCall(c, s.srv, "GET", "http://0.1.2.3/v4/precise/wordpress-23/meta/any?include=charm-metadata", "", http.StatusOK, params.MetaAnyResponse{
		Id: url,
		Meta: map[string]interface{}{
			"charm-metadata": wordpress.Meta(),
		},
	})
}

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
	_, wordpress := s.addCharm(c, "wordpress", "cs:precise/wordpress-23")
	_, mysql := s.addCharm(c, "mysql", "cs:precise/mysql-10")
	storetesting.AssertJSONCall(c, s.srv, "GET", "http://0.1.2.3/v4/meta/charm-metadata?id=precise/wordpress-23&id=precise/mysql-10", "", http.StatusOK, map[string]*charm.Meta{
		"precise/wordpress-23": wordpress.Meta(),
		"precise/mysql-10":     mysql.Meta(),
	})
}

func (s *APISuite) TestBulkMetaAny(c *gc.C) {
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

func (s *APISuite) TestMetaCharmMetadataFails(c *gc.C) {
	expected := params.Error{Message: router.ErrNotFound.Error()}
	storetesting.AssertJSONCall(c, s.srv, "GET", "http://0.1.2.3/v4/precise/wordpress-23/meta/charm-metadata", "", http.StatusInternalServerError, expected)
}

func (s *APISuite) TestMetaBundleMetadata(c *gc.C) {
	url, bundle := s.addBundle(c, "wordpress", "cs:bundle/wordpress-simple-42")
	storetesting.AssertJSONCall(c, s.srv, "GET",
		"http://0.1.2.3/v4/bundle/wordpress-simple-42/meta/bundle-metadata",
		"", http.StatusOK, bundle.Data())

	storetesting.AssertJSONCall(c, s.srv, "GET",
		"http://0.1.2.3/v4/bundle/wordpress-simple-42/meta/any?include=bundle-metadata",
		"", http.StatusOK, params.MetaAnyResponse{
			Id: url,
			Meta: map[string]interface{}{
				"bundle-metadata": bundle.Data(),
			},
		})
}

var errorTests = []struct {
	name     string
	expected error
	path     string
}{{
	name:     "MetaCharmConfig: charm not found",
	expected: router.ErrNotFound,
	path:     "/precise/wordpress-23/meta/charm-config",
}, {
	name:     "MetaCharmConfig: not relevant",
	expected: v4.ErrMetadataNotRelevant,
	path:     "/bundle/wordpress-simple-42/meta/charm-config",
}, {
	name:     "MetaCharmMetadata: charm not found",
	expected: router.ErrNotFound,
	path:     "/precise/wordpress-23/meta/charm-metadata",
}, {
	name:     "MetaCharmMetadata: not relevant",
	expected: v4.ErrMetadataNotRelevant,
	path:     "/bundle/wordpress-simple-42/meta/charm-config",
}, {
	name:     "MetaBundleMetadata: bundle not found",
	expected: router.ErrNotFound,
	path:     "/bundle/django-app-23/meta/bundle-metadata",
}, {
	name:     "MetaBundleMetadata: not relevant",
	expected: v4.ErrMetadataNotRelevant,
	path:     "/trusty/django-42/meta/bundle-metadata",
}}

func (s *APISuite) TestError(c *gc.C) {
	for i, test := range errorTests {
		c.Logf("%d: %s", i, test.name)
		expectedError := params.Error{Message: test.expected.Error()}
		storetesting.AssertJSONCall(c, s.srv, "GET", "http://0.1.2.3/v4"+test.path,
			"", http.StatusInternalServerError, expectedError)
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
		url := mustParseURL(test.url)
		err := v4.ResolveURL(s.store, url)
		if test.notFound {
			c.Assert(err, gc.ErrorMatches, `no matching charm or bundle for ".*"`)
			continue
		}
		c.Assert(err, gc.IsNil)
		c.Assert(url.String(), gc.Equals, test.expect)
	}
}

func mustParseURL(s string) *charm.URL {
	ref, series, err := charm.ParseReference(s)
	if err != nil {
		panic(err)
	}
	return &charm.URL{
		Reference: ref,
		Series:    series,
	}
}

func assertNotImplemented(c *gc.C, h http.Handler, path string) {
	storetesting.AssertJSONCall(c, h, "GET", "http://0.1.2.3/v4/"+path, "", http.StatusInternalServerError, params.Error{
		Message: "method not implemented",
	})
}
