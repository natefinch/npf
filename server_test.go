// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore_test // import "gopkg.in/juju/charmstore.v5-unstable"

import (
	"fmt"
	"net/http"
	"testing"

	jujutesting "github.com/juju/testing"
	"github.com/juju/testing/httptesting"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"

	"gopkg.in/juju/charmstore.v5-unstable"
	"gopkg.in/juju/charmstore.v5-unstable/internal/storetesting"
)

// These tests are copied (almost) verbatim from internal/charmstore/server_test.go

func TestPackage(t *testing.T) {
	jujutesting.MgoTestPackage(t, nil)
}

type ServerSuite struct {
	jujutesting.IsolatedMgoSuite
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
	h, err := charmstore.NewServer(s.Session.DB("foo"), nil, "", s.config)
	c.Assert(err, gc.ErrorMatches, `charm store server must serve at least one version of the API`)
	c.Assert(h, gc.IsNil)
}

func (s *ServerSuite) TestNewServerWithUnregisteredVersion(c *gc.C) {
	h, err := charmstore.NewServer(s.Session.DB("foo"), nil, "", s.config, "wrong")
	c.Assert(err, gc.ErrorMatches, `unknown version "wrong"`)
	c.Assert(h, gc.IsNil)
}

type versionResponse struct {
	Version string
	Path    string
}

func (s *ServerSuite) TestVersions(c *gc.C) {
	c.Assert(charmstore.Versions(), gc.DeepEquals, []string{"", "v4", "v5"})
}

func (s *ServerSuite) TestNewServerWithVersions(c *gc.C) {
	db := s.Session.DB("foo")

	h, err := charmstore.NewServer(db, nil, "", s.config, charmstore.V4)
	c.Assert(err, gc.IsNil)
	defer h.Close()

	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      h,
		URL:          "/v4/debug",
		ExpectStatus: http.StatusInternalServerError,
		ExpectBody: params.Error{
			Message: "method not implemented",
		},
	})
	assertDoesNotServeVersion(c, h, "v3")
}

func assertServesVersion(c *gc.C, h http.Handler, vers string) {
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler: h,
		URL:     "/" + vers + "/some/path",
		ExpectBody: versionResponse{
			Version: vers,
			Path:    "/some/path",
		},
	})
}

func assertDoesNotServeVersion(c *gc.C, h http.Handler, vers string) {
	url := "/" + vers + "/debug"
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      h,
		URL:          url,
		ExpectStatus: http.StatusNotFound,
		ExpectBody: params.Error{
			Message: fmt.Sprintf("no handler for %q", url),
			Code:    params.ErrNotFound,
		},
	})
}

type ServerESSuite struct {
	storetesting.IsolatedMgoESSuite
	config charmstore.ServerParams
}

var _ = gc.Suite(&ServerESSuite{})

func (s *ServerESSuite) SetUpSuite(c *gc.C) {
	s.IsolatedMgoESSuite.SetUpSuite(c)
	s.config = charmstore.ServerParams{
		AuthUsername: "test-user",
		AuthPassword: "test-password",
	}
}

func (s *ServerESSuite) TestNewServerWithElasticsearch(c *gc.C) {
	db := s.Session.DB("foo")

	srv, err := charmstore.NewServer(db, s.ES, s.TestIndex, s.config, charmstore.V4)
	c.Assert(err, gc.IsNil)
	srv.Close()
}
