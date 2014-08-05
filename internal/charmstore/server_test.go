// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore

import (
	"net/http"
	"testing"

	jujutesting "github.com/juju/testing"
	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore/internal/router"
	"github.com/juju/charmstore/internal/storetesting"
)

func TestPackage(t *testing.T) {
	jujutesting.MgoTestPackage(t, nil)
}

type ServerSuite struct {
	storetesting.IsolatedMgoSuite
}

var _ = gc.Suite(&ServerSuite{})

func (s *ServerSuite) TestNewServerWithNoVersions(c *gc.C) {
	h, err := NewServer(s.Session.DB("foo"), nil)
	c.Assert(err, gc.ErrorMatches, `charm store server must serve at least one version of the API`)
	c.Assert(h, gc.IsNil)
}

type versionResponse struct {
	Version string
	Path    string
}

func (s *ServerSuite) TestNewServerWithVersions(c *gc.C) {
	db := s.Session.DB("foo")
	serveVersion := func(vers string) func(store *Store) http.Handler {
		return func(store *Store) http.Handler {
			c.Assert(store.DB.Database, gc.Equals, db)
			return router.HandleJSON(func(w http.ResponseWriter, req *http.Request) (interface{}, error) {
				return versionResponse{
					Version: vers,
					Path:    req.URL.Path,
				}, nil
			})
		}
	}

	h, err := NewServer(db, map[string]NewAPIHandler{
		"version1": serveVersion("version1"),
	})
	c.Assert(err, gc.IsNil)
	assertServesVersion(c, h, "version1")
	assertDoesNotServeVersion(c, h, "version2")
	assertDoesNotServeVersion(c, h, "version3")

	h, err = NewServer(db, map[string]NewAPIHandler{
		"version1": serveVersion("version1"),
		"version2": serveVersion("version2"),
	})
	c.Assert(err, gc.IsNil)
	assertServesVersion(c, h, "version1")
	assertServesVersion(c, h, "version2")
	assertDoesNotServeVersion(c, h, "version3")

	h, err = NewServer(db, map[string]NewAPIHandler{
		"version1": serveVersion("version1"),
		"version2": serveVersion("version2"),
		"version3": serveVersion("version3"),
	})
	c.Assert(err, gc.IsNil)
	assertServesVersion(c, h, "version1")
	assertServesVersion(c, h, "version2")
	assertServesVersion(c, h, "version3")
}

func assertServesVersion(c *gc.C, h http.Handler, vers string) {
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler: h,
		URL:     "http://0.1.2.3/" + vers + "/some/path",
		ExpectBody: versionResponse{
			Version: vers,
			Path:    "/some/path",
		},
	})
}

func assertDoesNotServeVersion(c *gc.C, h http.Handler, vers string) {
	rec := storetesting.DoRequest(c, h, "GET", "http://0.1.2.3/"+vers+"/some/path", "", "", nil)
	c.Assert(rec.Code, gc.Equals, http.StatusNotFound)
}
