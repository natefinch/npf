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

func (s *APISuite) addCharmToStore(c *gc.C) (*charm.URL, charm.Charm) {
	url := charm.MustParseURL("cs:precise/wordpress-23")
	wordpress := charmtesting.Charms.CharmDir("wordpress")
	err := s.store.AddCharm(url, wordpress)
	c.Assert(err, gc.IsNil)
	return url, wordpress
}

func (s *APISuite) addBundleToStore(c *gc.C) (*charm.URL, charm.Bundle) {
	url := charm.MustParseURL("cs:bundle/wordpress-simple-42")
	bundle := charmtesting.Charms.BundleDir("wordpress")
	err := s.store.AddBundle(url, bundle)
	c.Assert(err, gc.IsNil)
	return url, bundle
}

func (s *APISuite) TestArchive(c *gc.C) {
	assertNotImplemented(c, s.srv, "precise/wordpress-23/archive")
}

func (s *APISuite) TestMetaCharmConfig(c *gc.C) {
	url, wordpress := s.addCharmToStore(c)
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

func (s *APISuite) TestMetaCharmMetadata(c *gc.C) {
	url, wordpress := s.addCharmToStore(c)
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

func (s *APISuite) TestMetaBundleMetadata(c *gc.C) {
	url, bundle := s.addBundleToStore(c)
	storetesting.AssertJSONCall(c, s.srv, "GET",
		"http://0.1.2.3/v4/bundle/wordpress-simple-42/meta/bundle-metadata",
		"", http.StatusOK, bundle.Data())

	type includeMetadata struct {
		Id   *charm.URL
		Meta map[string]*charm.BundleData
	}
	storetesting.AssertJSONCall(c, s.srv, "GET",
		"http://0.1.2.3/v4/bundle/wordpress-simple-42/meta/any?include=bundle-metadata",
		"", http.StatusOK, &includeMetadata{
			Id: url,
			Meta: map[string]*charm.BundleData{
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

func assertNotImplemented(c *gc.C, h http.Handler, path string) {
	storetesting.AssertJSONCall(c, h, "GET", "http://0.1.2.3/v4/"+path, "", http.StatusInternalServerError, params.Error{
		Message: "method not implemented",
	})
}
