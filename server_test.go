// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore_test

import (
	"net/http"
	"testing"

	jujutesting "github.com/juju/testing"
	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore"
	"github.com/juju/charmstore/internal/storetesting"
	"github.com/juju/charmstore/params"
)

// These tests are copied (almost) verbatim from internal/charmstore/server_test.go

func TestPackage(t *testing.T) {
	jujutesting.MgoTestPackage(t, nil)
}

type ServerSuite struct {
	storetesting.IsolatedMgoSuite
	config charmstore.ServerParams
}

var _ = gc.Suite(&ServerSuite{})

func (s *ServerSuite) SetUpSuite(c *gc.C) {
	s.IsolatedMgoSuite.SetUpSuite(c)
	s.config = charmstore.ServerParams{
		AuthUsername: "test-user",
		AuthPassword: "test-password",
	}
}

func (s *ServerSuite) TestNewServerWithNoVersions(c *gc.C) {
	h, err := charmstore.NewServer(s.Session.DB("foo"), s.config)
	c.Assert(err, gc.ErrorMatches, `charm store server must serve at least one version of the API`)
	c.Assert(h, gc.IsNil)
}

func (s *ServerSuite) TestNewServerWithUnregisteredVersion(c *gc.C) {
	h, err := charmstore.NewServer(s.Session.DB("foo"), s.config, "wrong")
	c.Assert(err, gc.ErrorMatches, `unknown version "wrong"`)
	c.Assert(h, gc.IsNil)
}

type versionResponse struct {
	Version string
	Path    string
}

func (s *ServerSuite) TestVersions(c *gc.C) {
	c.Assert(charmstore.Versions(), gc.DeepEquals, []string{"v4"})
}

func (s *ServerSuite) TestNewServerWithVersions(c *gc.C) {
	db := s.Session.DB("foo")

	h, err := charmstore.NewServer(db, s.config, charmstore.V4)
	c.Assert(err, gc.IsNil)

	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:    h,
		URL:        "/v4/debug",
		ExpectCode: http.StatusInternalServerError,
		ExpectBody: params.Error{
			Message: "method not implemented",
		},
	})
	assertDoesNotServeVersion(c, h, "v3")
}

func assertServesVersion(c *gc.C, h http.Handler, vers string) {
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler: h,
		URL:     "/" + vers + "/some/path",
		ExpectBody: versionResponse{
			Version: vers,
			Path:    "/some/path",
		},
	})
}

func assertDoesNotServeVersion(c *gc.C, h http.Handler, vers string) {
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:    h,
		URL:        "/" + vers + "/debug",
		ExpectCode: http.StatusNotFound,
		ExpectBody: params.Error{
			Message: "not found",
			Code:    params.ErrNotFound,
		},
	})
}
