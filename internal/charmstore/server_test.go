// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore

import (
	"net/http"

	gc "gopkg.in/check.v1"

	"github.com/juju/charmstore/internal/router"
	"github.com/juju/charmstore/internal/storetesting"
)

var serverParams = ServerParams{
	AuthUsername: "test-user",
	AuthPassword: "test-password",
}

type ServerSuite struct {
	storetesting.IsolatedMgoESSuite
}

var _ = gc.Suite(&ServerSuite{})

func (s *ServerSuite) TestNewServerWithNoVersions(c *gc.C) {
	h, err := NewServer(s.Session.DB("foo"), nil, serverParams, nil)
	c.Assert(err, gc.ErrorMatches, `charm store server must serve at least one version of the API`)
	c.Assert(h, gc.IsNil)
}

type versionResponse struct {
	Version string
	Path    string
}

func (s *ServerSuite) TestNewServerWithVersions(c *gc.C) {
	db := s.Session.DB("foo")
	serveVersion := func(vers string) NewAPIHandlerFunc {
		return func(store *Store, config ServerParams) http.Handler {
			c.Assert(store.DB.Database, gc.Equals, db)
			return router.HandleJSON(func(w http.ResponseWriter, req *http.Request) (interface{}, error) {
				return versionResponse{
					Version: vers,
					Path:    req.URL.Path,
				}, nil
			})
		}
	}

	h, err := NewServer(db, nil, serverParams, map[string]NewAPIHandlerFunc{
		"version1": serveVersion("version1"),
	})
	c.Assert(err, gc.IsNil)
	assertServesVersion(c, h, "version1")
	assertDoesNotServeVersion(c, h, "version2")
	assertDoesNotServeVersion(c, h, "version3")

	h, err = NewServer(db, nil, serverParams, map[string]NewAPIHandlerFunc{
		"version1": serveVersion("version1"),
		"version2": serveVersion("version2"),
	})
	c.Assert(err, gc.IsNil)
	assertServesVersion(c, h, "version1")
	assertServesVersion(c, h, "version2")
	assertDoesNotServeVersion(c, h, "version3")

	h, err = NewServer(db, nil, serverParams, map[string]NewAPIHandlerFunc{
		"version1": serveVersion("version1"),
		"version2": serveVersion("version2"),
		"version3": serveVersion("version3"),
	})
	c.Assert(err, gc.IsNil)
	assertServesVersion(c, h, "version1")
	assertServesVersion(c, h, "version2")
	assertServesVersion(c, h, "version3")

	h, err = NewServer(db, nil, serverParams, map[string]NewAPIHandlerFunc{
		"version1": serveVersion("version1"),
		"":         serveVersion(""),
	})
	c.Assert(err, gc.IsNil)
	assertServesVersion(c, h, "")
	assertServesVersion(c, h, "version1")
}

func (s *ServerSuite) TestNewServerWithConfig(c *gc.C) {
	serveConfig := func(store *Store, config ServerParams) http.Handler {
		return router.HandleJSON(func(w http.ResponseWriter, req *http.Request) (interface{}, error) {
			return config, nil
		})
	}
	h, err := NewServer(s.Session.DB("foo"), nil, serverParams, map[string]NewAPIHandlerFunc{
		"version1": serveConfig,
	})
	c.Assert(err, gc.IsNil)
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:    h,
		URL:        "/version1/some/path",
		ExpectBody: serverParams,
	})
}

func (s *ServerSuite) TestNewServerWithElasticSearch(c *gc.C) {
	serveConfig := func(store *Store, config ServerParams) http.Handler {
		return router.HandleJSON(func(w http.ResponseWriter, req *http.Request) (interface{}, error) {
			return config, nil
		})
	}
	h, err := NewServer(s.Session.DB("foo"), s.ES, serverParams, map[string]NewAPIHandlerFunc{
		"version1": serveConfig,
	})
	c.Assert(err, gc.IsNil)
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:    h,
		URL:        "/version1/some/path",
		ExpectBody: serverParams,
	})
}

func assertServesVersion(c *gc.C, h http.Handler, vers string) {
	path := vers
	if path != "" {
		path = "/" + path
	}
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler: h,
		URL:     path + "/some/path",
		ExpectBody: versionResponse{
			Version: vers,
			Path:    "/some/path",
		},
	})
}

func assertDoesNotServeVersion(c *gc.C, h http.Handler, vers string) {
	rec := storetesting.DoRequest(c, storetesting.DoRequestParams{
		Handler: h,
		URL:     "/" + vers + "/some/path",
	})
	c.Assert(rec.Code, gc.Equals, http.StatusNotFound)
}
