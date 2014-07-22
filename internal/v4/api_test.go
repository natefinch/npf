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

func (s *APISuite) TestMetaCharmConfigFails(c *gc.C) {
	expected := params.Error{Message: router.ErrNotFound.Error()}
	storetesting.AssertJSONCall(c, s.srv, "GET", "http://0.1.2.3/v4/precise/wordpress-23/meta/charm-config", "", http.StatusInternalServerError, expected)
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

func (s *APISuite) TestMetaCharmMetadataFails(c *gc.C) {
	expected := params.Error{Message: router.ErrNotFound.Error()}
	storetesting.AssertJSONCall(c, s.srv, "GET", "http://0.1.2.3/v4/precise/wordpress-23/meta/charm-metadata", "", http.StatusInternalServerError, expected)
}

func assertNotImplemented(c *gc.C, h http.Handler, path string) {
	storetesting.AssertJSONCall(c, h, "GET", "http://0.1.2.3/v4/"+path, "", http.StatusInternalServerError, params.Error{
		Message: "method not implemented",
	})
}
