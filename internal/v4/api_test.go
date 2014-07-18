// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4_test

import (
	"net/http"
	"testing"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/storetesting"
	_ "github.com/juju/charmstore/internal/v4"
	"github.com/juju/charmstore/params"
	jujutesting "github.com/juju/testing"
	gc "launchpad.net/gocheck"
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
	srv, err := charmstore.NewServer(db, "v4")
	c.Assert(err, gc.IsNil)
	assertNotImplemented(c, srv, "precise/wordpress-23/archive")
}

func assertNotImplemented(c *gc.C, h http.Handler, path string) {
	storetesting.AssertJSONCall(c, h, "GET", "http://0.1.2.3/v4/"+path, "", http.StatusInternalServerError, params.Error{
		Message: "method not implemented",
	})
}
