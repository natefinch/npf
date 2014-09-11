// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package legacy_test

import (
	"fmt"
	"io/ioutil"
	"net/http"

	"gopkg.in/juju/charm.v3"
	charmtesting "gopkg.in/juju/charm.v3/testing"
	"gopkg.in/mgo.v2"
	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/legacy"
	"github.com/juju/charmstore/internal/storetesting"
	"github.com/juju/charmstore/params"
)

var serverParams = charmstore.ServerParams{
	AuthUsername: "test-user",
	AuthPassword: "test-password",
}

type APISuite struct {
	storetesting.IsolatedMgoSuite
	srv   http.Handler
	store *charmstore.Store
}

var _ = gc.Suite(&APISuite{})

func (s *APISuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	s.srv, s.store = newServer(c, s.Session, serverParams)
}

func newServer(c *gc.C, session *mgo.Session, config charmstore.ServerParams) (http.Handler, *charmstore.Store) {
	db := session.DB("charmstore")
	store, err := charmstore.NewStore(db)
	c.Assert(err, gc.IsNil)
	srv, err := charmstore.NewServer(db, config, map[string]charmstore.NewAPIHandlerFunc{"": legacy.NewAPIHandler})
	c.Assert(err, gc.IsNil)
	return srv, store
}

func (s *APISuite) TestCharmArchive(c *gc.C) {
	_, wordpress := s.addCharm(c, "wordpress", "cs:precise/wordpress-0")
	archiveBytes, err := ioutil.ReadFile(wordpress.Path)
	c.Assert(err, gc.IsNil)

	rec := storetesting.DoRequest(c, storetesting.DoRequestParams{
		Handler: s.srv,
		URL:     "/charm/precise/wordpress-0",
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	c.Assert(rec.Body.Bytes(), gc.DeepEquals, archiveBytes)
	c.Assert(rec.Header().Get("Content-Length"), gc.Equals, fmt.Sprint(len(rec.Body.Bytes())))

	// Test with unresolved URL.
	rec = storetesting.DoRequest(c, storetesting.DoRequestParams{
		Handler: s.srv,
		URL:     "/charm/wordpress",
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	c.Assert(rec.Body.Bytes(), gc.DeepEquals, archiveBytes)
	c.Assert(rec.Header().Get("Content-Length"), gc.Equals, fmt.Sprint(len(rec.Body.Bytes())))

	// Check that the HTTP range logic is plugged in OK. If this
	// is working, we assume that the whole thing is working OK,
	// as net/http is well-tested.
	rec = storetesting.DoRequest(c, storetesting.DoRequestParams{
		Handler: s.srv,
		URL:     "/charm/precise/wordpress-0",
		Header:  http.Header{"Range": {"bytes=10-100"}},
	})
	c.Assert(rec.Code, gc.Equals, http.StatusPartialContent, gc.Commentf("body: %q", rec.Body.Bytes()))
	c.Assert(rec.Body.Bytes(), gc.HasLen, 100-10+1)
	c.Assert(rec.Body.Bytes(), gc.DeepEquals, archiveBytes[10:101])
}

func (s *APISuite) TestPostNotAllowed(c *gc.C) {
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:      s.srv,
		Method:       "POST",
		URL:          "/charm/precise/wordpress",
		ExpectStatus: http.StatusMethodNotAllowed,
		ExpectBody: params.Error{
			Code:    params.ErrMethodNotAllowed,
			Message: params.ErrMethodNotAllowed.Error(),
		},
	})
}

func (s *APISuite) TestCharmArchiveUnresolvedURL(c *gc.C) {
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:      s.srv,
		URL:          "/charm/wordpress",
		ExpectStatus: http.StatusNotFound,
		ExpectBody: params.Error{
			Code:    params.ErrNotFound,
			Message: `no matching charm or bundle for "cs:wordpress"`,
		},
	})
}

func (s *APISuite) TestCharmInfo(c *gc.C) {
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:      s.srv,
		URL:          "/charm-info",
		ExpectStatus: http.StatusInternalServerError,
		ExpectBody: params.Error{
			Message: "charm-info not implemented",
		},
	})
}

var serverStatusTests = []struct {
	path string
	code int
}{
	{"/charm-info/any", 404},
	{"/charm/bad-url", 404},
	{"/charm/bad-series/wordpress", 404},
}

func (s *APISuite) TestServerStatus(c *gc.C) {
	// TODO(rog) add tests from old TestServerStatus tests
	// when we implement charm-info.
	for i, test := range serverStatusTests {
		c.Logf("test %d: %s", i, test.path)
		resp := storetesting.DoRequest(c, storetesting.DoRequestParams{
			Handler: s.srv,
			URL:     test.path,
		})
		c.Assert(resp.Code, gc.Equals, test.code, gc.Commentf("body: %s", resp.Body))
	}
}

func (s *APISuite) addCharm(c *gc.C, charmName, curl string) (*charm.Reference, *charm.CharmArchive) {
	url := mustParseReference(curl)
	wordpress := charmtesting.Charms.CharmArchive(c.MkDir(), charmName)
	err := s.store.AddCharmWithArchive(url, wordpress)
	c.Assert(err, gc.IsNil)
	return url, wordpress
}

func mustParseReference(url string) *charm.Reference {
	ref, err := charm.ParseReference(url)
	if err != nil {
		panic(err)
	}
	return ref
}
