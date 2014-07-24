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

func (s *APISuite) TestArchive(c *gc.C) {
	assertNotImplemented(c, s.srv, "precise/wordpress-23/archive")
}

func (s *APISuite) TestMetaCharmConfig(c *gc.C) {
	url, wordpress := s.addCharm(c, "wordpress", "cs:precise/wordpress-23")
	storetesting.AssertJSONCall(c, s.srv, "GET", "http://0.1.2.3/v4/precise/wordpress-23/meta/charm-config", "", http.StatusOK, wordpress.Config())

	type includeMetadata struct {
		Id   *charm.URL
		Meta map[string]*charm.Config
	}
	storetesting.AssertJSONCall(c, s.srv, "GET", "http://0.1.2.3/v4/precise/wordpress-23/meta/any?include=charm-config", "", http.StatusOK, &includeMetadata{
		Id: url,
		Meta: map[string]*charm.Config{
			"charm-config": wordpress.Config(),
		},
	})
}

func (s *APISuite) TestMetaCharmConfigFails(c *gc.C) {
	expected := params.Error{Message: router.ErrNotFound.Error()}
	storetesting.AssertJSONCall(c, s.srv, "GET", "http://0.1.2.3/v4/precise/wordpress-23/meta/charm-config", "", http.StatusInternalServerError, expected)
}

func (s *APISuite) TestMetaCharmMetadata(c *gc.C) {
	url, wordpress := s.addCharm(c, "wordpress", "cs:precise/wordpress-23")
	storetesting.AssertJSONCall(c, s.srv, "GET", "http://0.1.2.3/v4/precise/wordpress-23/meta/charm-metadata", "", http.StatusOK, wordpress.Meta())

	type includeMetadata struct {
		Id   *charm.URL
		Meta map[string]*charm.Meta
	}
	storetesting.AssertJSONCall(c, s.srv, "GET", "http://0.1.2.3/v4/precise/wordpress-23/meta/any?include=charm-metadata", "", http.StatusOK, &includeMetadata{
		Id: url,
		Meta: map[string]*charm.Meta{
			"charm-metadata": wordpress.Meta(),
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
