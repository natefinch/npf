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
	"github.com/juju/charmstore/internal/storetesting"
	"github.com/juju/charmstore/internal/v4"
	"github.com/juju/charmstore/params"
)

func TestPackage(t *testing.T) {
	jujutesting.MgoTestPackage(t, nil)
}

type APISuite struct {
	storetesting.IsolatedMgoSuite
}

var _ = gc.Suite(&APISuite{})

func (s *APISuite) TestArchive(c *gc.C) {
	db := s.Session.DB("charmstore")
	srv, err := charmstore.NewServer(db, map[string]charmstore.NewAPIHandler{"v4": v4.New})
	c.Assert(err, gc.IsNil)
	assertNotImplemented(c, srv, "precise/wordpress-23/archive")
}

func (s *APISuite) TestCharmMetadata(c *gc.C) {
	db := s.Session.DB("charmstore")
	store := charmstore.NewStore(db)

	wordpress := charmtesting.Charms.CharmDir("wordpress")
	url := charm.MustParseURL("cs:precise/wordpress-23")
	err := store.AddCharm(url, wordpress)
	c.Assert(err, gc.IsNil)

	srv, err := charmstore.NewServer(db, map[string]charmstore.NewAPIHandler{"v4": v4.New})
	c.Assert(err, gc.IsNil)

	storetesting.AssertJSONCall(c, srv, "GET", "http://0.1.2.3/v4/precise/wordpress-23/meta/charm-metadata", "", http.StatusOK, wordpress.Meta())

	type includeMetadata struct {
		Id   *charm.URL
		Meta map[string]*charm.Meta
	}
	storetesting.AssertJSONCall(c, srv, "GET", "http://0.1.2.3/v4/precise/wordpress-23/meta/any?include=charm-metadata", "", http.StatusOK, &includeMetadata{
		Id: url,
		Meta: map[string]*charm.Meta{
			"charm-metadata": wordpress.Meta(),
		},
	})
}

func assertNotImplemented(c *gc.C, h http.Handler, path string) {
	storetesting.AssertJSONCall(c, h, "GET", "http://0.1.2.3/v4/"+path, "", http.StatusInternalServerError, params.Error{
		Message: "method not implemented",
	})
}
