// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package legacy_test // import "gopkg.in/juju/charmstore.v5-unstable/internal/legacy"

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"github.com/juju/testing/httptesting"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v2-unstable"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"
	"gopkg.in/juju/charmstore.v5-unstable/internal/legacy"
	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
	"gopkg.in/juju/charmstore.v5-unstable/internal/storetesting"
	"gopkg.in/juju/charmstore.v5-unstable/internal/storetesting/hashtesting"
	"gopkg.in/juju/charmstore.v5-unstable/internal/storetesting/stats"
)

var serverParams = charmstore.ServerParams{
	AuthUsername: "test-user",
	AuthPassword: "test-password",
}

type APISuite struct {
	jujutesting.IsolatedMgoSuite
	srv   *charmstore.Server
	store *charmstore.Store
}

var _ = gc.Suite(&APISuite{})

func (s *APISuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	s.srv, s.store = newServer(c, s.Session, serverParams)
}

func (s *APISuite) TearDownTest(c *gc.C) {
	s.store.Close()
	s.store.Pool().Close()
	s.srv.Close()
	s.IsolatedMgoSuite.TearDownTest(c)
}

func newServer(c *gc.C, session *mgo.Session, config charmstore.ServerParams) (*charmstore.Server, *charmstore.Store) {
	db := session.DB("charmstore")
	pool, err := charmstore.NewPool(db, nil, nil, config)
	c.Assert(err, gc.IsNil)
	srv, err := charmstore.NewServer(db, nil, config, map[string]charmstore.NewAPIHandlerFunc{"": legacy.NewAPIHandler})
	c.Assert(err, gc.IsNil)
	return srv, pool.Store()
}

func (s *APISuite) TestCharmArchive(c *gc.C) {
	_, wordpress := s.addPublicCharm(c, "wordpress", "cs:precise/wordpress-0")
	archiveBytes, err := ioutil.ReadFile(wordpress.Path)
	c.Assert(err, gc.IsNil)

	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     "/charm/precise/wordpress-0",
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	c.Assert(rec.Body.Bytes(), gc.DeepEquals, archiveBytes)
	c.Assert(rec.Header().Get("Content-Length"), gc.Equals, fmt.Sprint(len(rec.Body.Bytes())))

	// Test with unresolved URL.
	rec = httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     "/charm/wordpress",
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	c.Assert(rec.Body.Bytes(), gc.DeepEquals, archiveBytes)
	c.Assert(rec.Header().Get("Content-Length"), gc.Equals, fmt.Sprint(len(rec.Body.Bytes())))

	// Check that the HTTP range logic is plugged in OK. If this
	// is working, we assume that the whole thing is working OK,
	// as net/http is well-tested.
	rec = httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     "/charm/precise/wordpress-0",
		Header:  http.Header{"Range": {"bytes=10-100"}},
	})
	c.Assert(rec.Code, gc.Equals, http.StatusPartialContent, gc.Commentf("body: %q", rec.Body.Bytes()))
	c.Assert(rec.Body.Bytes(), gc.HasLen, 100-10+1)
	c.Assert(rec.Body.Bytes(), gc.DeepEquals, archiveBytes[10:101])
}

func (s *APISuite) TestPostNotAllowed(c *gc.C) {
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
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
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
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
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          "/charm-info?charms=cs:precise/something-23",
		ExpectStatus: http.StatusOK,
		ExpectBody: map[string]charmrepo.InfoResponse{
			"cs:precise/something-23": {
				Errors: []string{"entry not found"},
			},
		},
	})
}

func (s *APISuite) TestServeCharmInfo(c *gc.C) {
	wordpressURL, wordpress := s.addPublicCharm(c, "wordpress", "cs:precise/wordpress-1")
	hashSum := fileSHA256(c, wordpress.Path)
	digest, err := json.Marshal("who@canonical.com-bzr-digest")
	c.Assert(err, gc.IsNil)

	tests := []struct {
		about     string
		url       string
		extrainfo map[string][]byte
		canonical string
		sha       string
		digest    string
		revision  int
		err       string
	}{{
		about: "full charm URL with digest extra info",
		url:   wordpressURL.String(),
		extrainfo: map[string][]byte{
			params.BzrDigestKey: digest,
		},
		canonical: "cs:precise/wordpress-1",
		sha:       hashSum,
		digest:    "who@canonical.com-bzr-digest",
		revision:  1,
	}, {
		about:     "full charm URL without digest extra info",
		url:       wordpressURL.String(),
		canonical: "cs:precise/wordpress-1",
		sha:       hashSum,
		revision:  1,
	}, {
		about: "partial charm URL with digest extra info",
		url:   "cs:wordpress",
		extrainfo: map[string][]byte{
			params.BzrDigestKey: digest,
		},
		canonical: "cs:precise/wordpress-1",
		sha:       hashSum,
		digest:    "who@canonical.com-bzr-digest",
		revision:  1,
	}, {
		about:     "partial charm URL without extra info",
		url:       "cs:wordpress",
		canonical: "cs:precise/wordpress-1",
		sha:       hashSum,
		revision:  1,
	}, {
		about: "invalid digest extra info",
		url:   "cs:wordpress",
		extrainfo: map[string][]byte{
			params.BzrDigestKey: []byte("[]"),
		},
		canonical: "cs:precise/wordpress-1",
		sha:       hashSum,
		revision:  1,
		err:       `cannot unmarshal digest: json: cannot unmarshal array into Go value of type string`,
	}, {
		about: "charm not found",
		url:   "cs:precise/non-existent",
		err:   "entry not found",
	}, {
		about: "invalid charm URL",
		url:   "cs:/bad",
		err:   `entry not found`,
	}, {
		about: "invalid charm schema",
		url:   "gopher:archie-server",
		err:   `entry not found`,
	}, {
		about: "invalid URL",
		url:   "/charm-info?charms=cs:not-found",
		err:   "entry not found",
	}}

	for i, test := range tests {
		c.Logf("test %d: %s", i, test.about)
		err = s.store.UpdateEntity(wordpressURL, bson.D{{
			"$set", bson.D{{"extrainfo", test.extrainfo}},
		}})
		c.Assert(err, gc.IsNil)
		expectInfo := charmrepo.InfoResponse{
			CanonicalURL: test.canonical,
			Sha256:       test.sha,
			Revision:     test.revision,
			Digest:       test.digest,
		}
		if test.err != "" {
			expectInfo.Errors = []string{test.err}
		}
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      s.srv,
			URL:          "/charm-info?charms=" + test.url,
			ExpectStatus: http.StatusOK,
			ExpectBody: map[string]charmrepo.InfoResponse{
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
	s.addPublicCharm(c, "wordpress", "cs:utopic/wordpress-42")
	s.addPublicCharm(c, "wordpress", "cs:~who/trusty/wordpress-47")

	requestInfo := func(id string, times int) {
		for i := 0; i < times; i++ {
			rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
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

func (s *APISuite) TestAPIInfoWithGatedCharm(c *gc.C) {
	wordpressURL, _ := s.addPublicCharm(c, "wordpress", "cs:precise/wordpress-0")
	s.store.SetPerms(&wordpressURL.URL, "read", "bob")
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          "/charm-info?charms=" + wordpressURL.URL.String(),
		ExpectStatus: http.StatusOK,
		ExpectBody: map[string]charmrepo.InfoResponse{
			wordpressURL.URL.String(): {
				Errors: []string{"entry not found"},
			},
		},
	})
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
	wordpressURL, wordpress := s.addPublicCharm(c, "wordpress", "cs:precise/wordpress-0")
	archiveBytes, err := ioutil.ReadFile(wordpress.Path)
	c.Assert(err, gc.IsNil)

	srv := httptest.NewServer(s.srv)
	defer srv.Close()

	s.PatchValue(&charmrepo.CacheDir, c.MkDir())
	s.PatchValue(&charmrepo.LegacyStore.BaseURL, srv.URL)

	ch, err := charmrepo.LegacyStore.Get(&wordpressURL.URL)
	c.Assert(err, gc.IsNil)
	chArchive := ch.(*charm.CharmArchive)

	data, err := ioutil.ReadFile(chArchive.Path)
	c.Assert(err, gc.IsNil)
	c.Assert(data, gc.DeepEquals, archiveBytes)
}

func (s *APISuite) TestCharmPackageCharmInfo(c *gc.C) {
	wordpressURL, wordpress := s.addPublicCharm(c, "wordpress", "cs:precise/wordpress-0")
	wordpressSHA256 := fileSHA256(c, wordpress.Path)
	mysqlURL, mySQL := s.addPublicCharm(c, "wordpress", "cs:precise/mysql-2")
	mysqlSHA256 := fileSHA256(c, mySQL.Path)
	notFoundURL := charm.MustParseURL("cs:precise/not-found-3")

	srv := httptest.NewServer(s.srv)
	defer srv.Close()
	s.PatchValue(&charmrepo.LegacyStore.BaseURL, srv.URL)

	resp, err := charmrepo.LegacyStore.Info(wordpressURL.PreferredURL(), mysqlURL.PreferredURL(), notFoundURL)
	c.Assert(err, gc.IsNil)
	c.Assert(resp, gc.HasLen, 3)
	c.Assert(resp, jc.DeepEquals, []*charmrepo.InfoResponse{{
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
	// TODO frankban: remove this test after updating entities in the
	// production db with their SHA256 hash value. Entities are updated by
	// running the cshash256 command.
	id, ch := s.addPublicCharm(c, "wordpress", "cs:~who/precise/wordpress-0")
	url := id.String()
	sum256 := fileSHA256(c, ch.Path)

	hashtesting.CheckSHA256Laziness(c, s.store, &id.URL, func() {
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      s.srv,
			URL:          "/charm-info?charms=" + url,
			ExpectStatus: http.StatusOK,
			ExpectBody: map[string]charmrepo.InfoResponse{
				url: {
					CanonicalURL: url,
					Sha256:       sum256,
					Revision:     0,
				},
			},
		})
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
		resp := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler: s.srv,
			URL:     test.path,
		})
		c.Assert(resp.Code, gc.Equals, test.code, gc.Commentf("body: %s", resp.Body))
	}
}

func (s *APISuite) addPublicCharm(c *gc.C, charmName, curl string) (*router.ResolvedURL, *charm.CharmArchive) {
	rurl := &router.ResolvedURL{
		URL:                 *charm.MustParseURL(curl),
		PromulgatedRevision: -1,
	}
	if rurl.URL.User == "" {
		rurl.URL.User = "charmers"
		rurl.PromulgatedRevision = rurl.URL.Revision
	}
	archive := storetesting.Charms.CharmArchive(c.MkDir(), charmName)
	err := s.store.AddCharmWithArchive(rurl, archive)
	c.Assert(err, gc.IsNil)
	err = s.store.SetPerms(&rurl.URL, "read", params.Everyone, rurl.URL.User)
	c.Assert(err, gc.IsNil)
	return rurl, archive
}

var serveCharmEventErrorsTests = []struct {
	about       string
	url         string
	responseUrl string
	err         string
}{{
	about: "invalid charm URL",
	url:   "no-such:charm",
	err:   `invalid charm URL: charm or bundle URL has invalid schema: "no-such:charm"`,
}, {
	about: "revision specified",
	url:   "cs:utopic/django-42",
	err:   "got charm URL with revision: cs:utopic/django-42",
}, {
	about: "charm not found",
	url:   "cs:trusty/django",
	err:   "entry not found",
}, {
	about:       "ignoring digest",
	url:         "precise/django-47@a-bzr-digest",
	responseUrl: "precise/django-47",
	err:         "got charm URL with revision: cs:precise/django-47",
}}

func (s *APISuite) TestServeCharmEventErrors(c *gc.C) {
	for i, test := range serveCharmEventErrorsTests {
		c.Logf("test %d: %s", i, test.about)
		if test.responseUrl == "" {
			test.responseUrl = test.url
		}
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      s.srv,
			URL:          "/charm-event?charms=" + test.url,
			ExpectStatus: http.StatusOK,
			ExpectBody: map[string]charmrepo.EventResponse{
				test.responseUrl: {
					Errors: []string{test.err},
				},
			},
		})
	}
}

func (s *APISuite) TestServeCharmEvent(c *gc.C) {
	// Add three charms to the charm store.
	mysqlUrl, _ := s.addPublicCharm(c, "mysql", "cs:trusty/mysql-2")
	riakUrl, _ := s.addPublicCharm(c, "riak", "cs:utopic/riak-3")

	// Update the mysql charm with a valid digest extra-info.
	s.addExtraInfoDigest(c, mysqlUrl, "who@canonical.com-bzr-digest")

	// Update the riak charm with an invalid digest extra-info.
	err := s.store.UpdateEntity(riakUrl, bson.D{{
		"$set", bson.D{{"extrainfo", map[string][]byte{
			params.BzrDigestKey: []byte(":"),
		}}},
	}})
	c.Assert(err, gc.IsNil)

	// Retrieve the entities.
	mysql, err := s.store.FindEntity(mysqlUrl, nil)
	c.Assert(err, gc.IsNil)
	riak, err := s.store.FindEntity(riakUrl, nil)
	c.Assert(err, gc.IsNil)

	tests := []struct {
		about  string
		query  string
		expect map[string]*charmrepo.EventResponse
	}{{
		about: "valid digest",
		query: "?charms=cs:trusty/mysql",
		expect: map[string]*charmrepo.EventResponse{
			"cs:trusty/mysql": {
				Kind:     "published",
				Revision: mysql.Revision,
				Time:     mysql.UploadTime.UTC().Format(time.RFC3339),
				Digest:   "who@canonical.com-bzr-digest",
			},
		},
	}, {
		about: "invalid digest",
		query: "?charms=cs:utopic/riak",
		expect: map[string]*charmrepo.EventResponse{
			"cs:utopic/riak": {
				Kind:     "published",
				Revision: riak.Revision,
				Time:     riak.UploadTime.UTC().Format(time.RFC3339),
				Errors:   []string{"cannot unmarshal digest: invalid character ':' looking for beginning of value"},
			},
		},
	}, {
		about: "partial charm URL",
		query: "?charms=cs:mysql",
		expect: map[string]*charmrepo.EventResponse{
			"cs:mysql": {
				Kind:     "published",
				Revision: mysql.Revision,
				Time:     mysql.UploadTime.UTC().Format(time.RFC3339),
				Digest:   "who@canonical.com-bzr-digest",
			},
		},
	}, {
		about: "digest in request",
		query: "?charms=cs:trusty/mysql@my-digest",
		expect: map[string]*charmrepo.EventResponse{
			"cs:trusty/mysql": {
				Kind:     "published",
				Revision: mysql.Revision,
				Time:     mysql.UploadTime.UTC().Format(time.RFC3339),
				Digest:   "who@canonical.com-bzr-digest",
			},
		},
	}, {
		about: "multiple charms",
		query: "?charms=cs:mysql&charms=utopic/riak",
		expect: map[string]*charmrepo.EventResponse{
			"cs:mysql": {
				Kind:     "published",
				Revision: mysql.Revision,
				Time:     mysql.UploadTime.UTC().Format(time.RFC3339),
				Digest:   "who@canonical.com-bzr-digest",
			},
			"utopic/riak": {
				Kind:     "published",
				Revision: riak.Revision,
				Time:     riak.UploadTime.UTC().Format(time.RFC3339),
				Errors:   []string{"cannot unmarshal digest: invalid character ':' looking for beginning of value"},
			},
		},
	}}

	for i, test := range tests {
		c.Logf("test %d: %s", i, test.about)
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      s.srv,
			URL:          "/charm-event" + test.query,
			ExpectStatus: http.StatusOK,
			ExpectBody:   test.expect,
		})
	}
}

func (s *APISuite) TestServeCharmEventDigestNotFound(c *gc.C) {
	// Add a charm without a Bazaar digest.
	url, _ := s.addPublicCharm(c, "wordpress", "cs:trusty/wordpress-42")

	// Pretend the entity has been uploaded right now, and assume the test does
	// not take more than two minutes to run.
	s.updateUploadTime(c, url, time.Now())
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          "/charm-event?charms=cs:trusty/wordpress",
		ExpectStatus: http.StatusOK,
		ExpectBody: map[string]charmrepo.EventResponse{
			"cs:trusty/wordpress": {
				Errors: []string{"entry not found"},
			},
		},
	})

	// Now change the entity upload time to be more than 2 minutes ago.
	s.updateUploadTime(c, url, time.Now().Add(-121*time.Second))
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          "/charm-event?charms=cs:trusty/wordpress",
		ExpectStatus: http.StatusOK,
		ExpectBody: map[string]charmrepo.EventResponse{
			"cs:trusty/wordpress": {
				Errors: []string{"digest not found: this can be due to an error while ingesting the entity"},
			},
		},
	})
}

func (s *APISuite) TestServeCharmEventLastRevision(c *gc.C) {
	// Add two revisions of the same charm.
	url1, _ := s.addPublicCharm(c, "wordpress", "cs:trusty/wordpress-1")
	url2, _ := s.addPublicCharm(c, "wordpress", "cs:trusty/wordpress-2")

	// Update the resulting entities with Bazaar digests.
	s.addExtraInfoDigest(c, url1, "digest-1")
	s.addExtraInfoDigest(c, url2, "digest-2")

	// Retrieve the most recent revision of the entity.
	entity, err := s.store.FindEntity(url2, nil)
	c.Assert(err, gc.IsNil)

	// Ensure the last revision is correctly returned.
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          "/charm-event?charms=wordpress",
		ExpectStatus: http.StatusOK,
		ExpectBody: map[string]*charmrepo.EventResponse{
			"wordpress": {
				Kind:     "published",
				Revision: 2,
				Time:     entity.UploadTime.UTC().Format(time.RFC3339),
				Digest:   "digest-2",
			},
		},
	})
}

func (s *APISuite) addExtraInfoDigest(c *gc.C, id *router.ResolvedURL, digest string) {
	b, err := json.Marshal(digest)
	c.Assert(err, gc.IsNil)
	err = s.store.UpdateEntity(id, bson.D{{
		"$set", bson.D{{"extrainfo", map[string][]byte{
			params.BzrDigestKey: b,
		}}},
	}})
	c.Assert(err, gc.IsNil)
}

func (s *APISuite) updateUploadTime(c *gc.C, id *router.ResolvedURL, uploadTime time.Time) {
	err := s.store.UpdateEntity(id, bson.D{{
		"$set", bson.D{{"uploadtime", uploadTime}},
	}})
	c.Assert(err, gc.IsNil)
}
