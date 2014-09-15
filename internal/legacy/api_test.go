// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package legacy_test

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	jc "github.com/juju/testing/checkers"
	"gopkg.in/juju/charm.v3"
	charmtesting "gopkg.in/juju/charm.v3/testing"
	"gopkg.in/mgo.v2"
	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/legacy"
	"github.com/juju/charmstore/internal/storetesting"
	"github.com/juju/charmstore/internal/storetesting/stats"
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

func (s *APISuite) TestCharmInfoNotFound(c *gc.C) {
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:      s.srv,
		URL:          "/charm-info?charms=cs:precise/something-23",
		ExpectStatus: http.StatusOK,
		ExpectBody: map[string]charm.InfoResponse{
			"cs:precise/something-23": {
				Errors: []string{"entry not found"},
			},
		},
	})
}

func (s *APISuite) TestServerCharmInfo(c *gc.C) {
	wordpressURL, wordpress := s.addCharm(c, "wordpress", "cs:precise/wordpress-1")
	hashSum := fileSHA256(c, wordpress.Path)
	tests := []struct {
		url       string
		canonical string
		sha       string
		digest    string
		revision  int
		err       string
	}{{
		url:       wordpressURL.String(),
		canonical: "cs:precise/wordpress-1",
		sha:       hashSum,
		revision:  1,
	}, {
		url: "cs:oneiric/non-existent",
		err: "entry not found",
	}, {
		url:       "cs:wordpress",
		canonical: "cs:precise/wordpress-1",
		sha:       hashSum,
		revision:  1,
	}, {
		url: "cs:/bad",
		err: `entry not found`,
	}, {
		url: "gopher:archie-server",
		err: `entry not found`,
	}, {
		url: "/charm-info?charms=cs:not-found",
		err: "entry not found",
	}}

	for i, test := range tests {
		c.Logf("test %d: %s", i, test.url)
		expectInfo := charm.InfoResponse{
			CanonicalURL: test.canonical,
			Sha256:       test.sha,
			Revision:     test.revision,
			Digest:       test.digest,
		}
		if test.err != "" {
			expectInfo.Errors = []string{test.err}
		}
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
			Handler:      s.srv,
			URL:          "/charm-info?charms=" + test.url,
			ExpectStatus: http.StatusOK,
			ExpectBody: map[string]charm.InfoResponse{
				test.url: expectInfo,
			},
		})
	}
}

func (s *APISuite) TestCharmInfoCounters(c *gc.C) {
	if !storetesting.MongoJSEnabled() {
		c.Skip("MongoDB JavaScript not available")
	}

	// Add two charms to the database, a promulgated one and a user owned one.
	s.addCharm(c, "wordpress", "cs:utopic/wordpress-42")
	s.addCharm(c, "wordpress", "cs:~who/trusty/wordpress-47")

	requestInfo := func(id string, times int) {
		for i := 0; i < times; i++ {
			rec := storetesting.DoRequest(c, storetesting.DoRequestParams{
				Handler: s.srv,
				URL:     "/charm-info?charms=" + id,
			})
			c.Assert(rec.Code, gc.Equals, http.StatusOK)
		}
	}

	// Request charm info several times for the promulgated charm,
	// the user owned one and a missing charm.
	requestInfo("utopic/wordpress-42", 4)
	requestInfo("~who/trusty/wordpress-47", 3)
	requestInfo("precise/django-0", 2)

	// The charm-info count for the promulgated charm has been updated.
	key := []string{params.StatsCharmInfo, "utopic", "wordpress"}
	stats.CheckCounterSum(c, s.store, key, false, 4)

	// The charm-info count for the user owned charm has been updated.
	key = []string{params.StatsCharmInfo, "trusty", "wordpress", "who"}
	stats.CheckCounterSum(c, s.store, key, false, 3)

	// The charm-missing count for the missing charm has been updated.
	key = []string{params.StatsCharmMissing, "precise", "django"}
	stats.CheckCounterSum(c, s.store, key, false, 2)

	// The charm-info count for the missing charm is still zero.
	key = []string{params.StatsCharmInfo, "precise", "django"}
	stats.CheckCounterSum(c, s.store, key, false, 0)
}

func fileSHA256(c *gc.C, path string) string {
	f, err := os.Open(path)
	c.Assert(err, gc.IsNil)
	hash := sha256.New()
	_, err = io.Copy(hash, f)
	c.Assert(err, gc.IsNil)
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func (s *APISuite) TestCharmPackageGet(c *gc.C) {
	wordpressURL, wordpress := s.addCharm(c, "wordpress", "cs:precise/wordpress-0")
	archiveBytes, err := ioutil.ReadFile(wordpress.Path)
	c.Assert(err, gc.IsNil)

	srv := httptest.NewServer(s.srv)
	defer srv.Close()

	s.PatchValue(&charm.CacheDir, c.MkDir())
	s.PatchValue(&charm.Store.BaseURL, srv.URL)

	url, _ := wordpressURL.URL("")
	ch, err := charm.Store.Get(url)
	c.Assert(err, gc.IsNil)
	chArchive := ch.(*charm.CharmArchive)

	data, err := ioutil.ReadFile(chArchive.Path)
	c.Assert(err, gc.IsNil)
	c.Assert(data, gc.DeepEquals, archiveBytes)
}

func (s *APISuite) TestCharmPackageCharmInfo(c *gc.C) {
	wordpressURL, wordpress := s.addCharm(c, "wordpress", "cs:precise/wordpress-0")
	wordpressSHA256 := fileSHA256(c, wordpress.Path)
	mysqlURL, mySQL := s.addCharm(c, "wordpress", "cs:precise/mysql-2")
	mysqlSHA256 := fileSHA256(c, mySQL.Path)
	notFoundURL := mustParseReference("cs:oneiric/not-found-3")

	srv := httptest.NewServer(s.srv)
	defer srv.Close()
	s.PatchValue(&charm.Store.BaseURL, srv.URL)

	resp, err := charm.Store.Info(wordpressURL, mysqlURL, notFoundURL)
	c.Assert(err, gc.IsNil)
	c.Assert(resp, gc.HasLen, 3)
	c.Assert(resp, jc.DeepEquals, []*charm.InfoResponse{{
		CanonicalURL: wordpressURL.String(),
		Sha256:       wordpressSHA256,
	}, {
		CanonicalURL: mysqlURL.String(),
		Sha256:       mysqlSHA256,
		Revision:     2,
	}, {
		Errors: []string{"charm not found: " + notFoundURL.String()},
	}})
}

func (s *APISuite) TestSHA256Laziness(c *gc.C) {
	updated := make(chan struct{}, 1)
	// Patch updateEntitySHA256 so that we can know whether
	// it has been called or not.
	oldUpdate := *legacy.UpdateEntitySHA256
	s.PatchValue(legacy.UpdateEntitySHA256, func(store *charmstore.Store, url *charm.Reference, sum256 string) {
		oldUpdate(store, url, sum256)
		updated <- struct{}{}
	})

	wordpressURL, wordpress := s.addCharm(c, "wordpress", "cs:precise/wordpress-0")
	sum256 := fileSHA256(c, wordpress.Path)

	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:      s.srv,
		URL:          "/charm-info?charms=" + wordpressURL.String(),
		ExpectStatus: http.StatusOK,
		ExpectBody: map[string]charm.InfoResponse{
			wordpressURL.String(): {
				CanonicalURL: wordpressURL.String(),
				Sha256:       sum256,
				Revision:     0,
			},
		},
	})

	select {
	case <-updated:
	case <-time.After(5 * time.Second):
		c.Fatalf("timed out waiting for update")
	}

	// Try again - we should not update the SHA256 the second time.

	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:      s.srv,
		URL:          "/charm-info?charms=" + wordpressURL.String(),
		ExpectStatus: http.StatusOK,
		ExpectBody: map[string]charm.InfoResponse{
			wordpressURL.String(): {
				CanonicalURL: wordpressURL.String(),
				Sha256:       sum256,
				Revision:     0,
			},
		},
	})

	select {
	case <-updated:
		c.Fatalf("update called twice")
	case <-time.After(10 * time.Millisecond):
	}
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
