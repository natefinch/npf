// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4_test // import "gopkg.in/juju/charmstore.v5-unstable/internal/v4"

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	jc "github.com/juju/testing/checkers"
	"github.com/juju/testing/httptesting"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v0/csclient/params"
	charmtesting "gopkg.in/juju/charmrepo.v0/testing"
	"gopkg.in/mgo.v2/bson"

	"gopkg.in/juju/charmstore.v5-unstable/internal/blobstore"
	"gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"
	"gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"
	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
	"gopkg.in/juju/charmstore.v5-unstable/internal/storetesting"
	"gopkg.in/juju/charmstore.v5-unstable/internal/storetesting/stats"
	"gopkg.in/juju/charmstore.v5-unstable/internal/v4"
)

type ArchiveSuite struct {
	commonSuite
}

var _ = gc.Suite(&ArchiveSuite{})

func (s *ArchiveSuite) TestGet(c *gc.C) {
	patchArchiveCacheAges(s)
	id := newResolvedURL("cs:~charmers/precise/wordpress-0", -1)
	wordpress := s.assertUploadCharm(c, "POST", id, "wordpress")
	err := s.store.SetPerms(&id.URL, "read", params.Everyone, id.URL.User)
	c.Assert(err, gc.IsNil)

	archiveBytes, err := ioutil.ReadFile(wordpress.Path)
	c.Assert(err, gc.IsNil)

	archiveUrl := storeURL("~charmers/precise/wordpress-0/archive")
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     archiveUrl,
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	c.Assert(rec.Body.Bytes(), gc.DeepEquals, archiveBytes)
	c.Assert(rec.Header().Get(params.ContentHashHeader), gc.Equals, hashOfBytes(archiveBytes))
	c.Assert(rec.Header().Get(params.EntityIdHeader), gc.Equals, "cs:~charmers/precise/wordpress-0")
	assertCacheControl(c, rec.Header(), true)

	// Check that the HTTP range logic is plugged in OK. If this
	// is working, we assume that the whole thing is working OK,
	// as net/http is well-tested.
	rec = httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     archiveUrl,
		Header:  http.Header{"Range": {"bytes=10-100"}},
	})
	c.Assert(rec.Code, gc.Equals, http.StatusPartialContent, gc.Commentf("body: %q", rec.Body.Bytes()))
	c.Assert(rec.Body.Bytes(), gc.HasLen, 100-10+1)
	c.Assert(rec.Body.Bytes(), gc.DeepEquals, archiveBytes[10:101])
	c.Assert(rec.Header().Get(params.ContentHashHeader), gc.Equals, hashOfBytes(archiveBytes))
	c.Assert(rec.Header().Get(params.EntityIdHeader), gc.Equals, "cs:~charmers/precise/wordpress-0")
	assertCacheControl(c, rec.Header(), true)
}

func (s *ArchiveSuite) TestGetWithPartialId(c *gc.C) {
	id := newResolvedURL("cs:~charmers/utopic/wordpress-42", -1)
	err := s.store.AddCharmWithArchive(
		id,
		storetesting.Charms.CharmArchive(c.MkDir(), "wordpress"))
	c.Assert(err, gc.IsNil)
	err = s.store.SetPerms(&id.URL, "read", params.Everyone, id.URL.User)
	c.Assert(err, gc.IsNil)
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("~charmers/wordpress/archive"),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	// The complete entity id can be retrieved from the response header.
	c.Assert(rec.Header().Get(params.EntityIdHeader), gc.Equals, id.URL.String())
}

func (s *ArchiveSuite) TestGetPromulgatedWithPartialId(c *gc.C) {
	id := newResolvedURL("cs:~charmers/utopic/wordpress-42", 42)
	err := s.store.AddCharmWithArchive(
		id,
		storetesting.Charms.CharmArchive(c.MkDir(), "wordpress"))
	c.Assert(err, gc.IsNil)
	err = s.store.SetPerms(&id.URL, "read", params.Everyone, id.URL.User)
	c.Assert(err, gc.IsNil)
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("wordpress/archive"),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	// The complete entity id can be retrieved from the response header.
	c.Assert(rec.Header().Get(params.EntityIdHeader), gc.Equals, id.PromulgatedURL().String())
}

func (s *ArchiveSuite) TestGetCounters(c *gc.C) {
	if !storetesting.MongoJSEnabled() {
		c.Skip("MongoDB JavaScript not available")
	}

	for i, id := range []*router.ResolvedURL{
		newResolvedURL("~who/utopic/mysql-42", 42),
	} {
		c.Logf("test %d: %s", i, id)

		// Add a charm to the database (including the archive).
		err := s.store.AddCharmWithArchive(id, storetesting.Charms.CharmArchive(c.MkDir(), "mysql"))
		c.Assert(err, gc.IsNil)
		err = s.store.SetPerms(&id.URL, "read", params.Everyone, id.URL.User)
		c.Assert(err, gc.IsNil)

		// Download the charm archive using the API.
		rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler: s.srv,
			URL:     storeURL(id.URL.Path() + "/archive"),
		})
		c.Assert(rec.Code, gc.Equals, http.StatusOK)

		// Check that the downloads count for the entity has been updated.
		key := []string{params.StatsArchiveDownload, "utopic", "mysql", id.URL.User, "42"}
		stats.CheckCounterSum(c, s.store, key, false, 1)
		// Check that the promulgated download count for the entity has also been updated
		key = []string{params.StatsArchiveDownload, "utopic", "mysql", "", "42"}
		stats.CheckCounterSum(c, s.store, key, false, 1)
	}
}

func (s *ArchiveSuite) TestGetCountersDisabled(c *gc.C) {
	url := newResolvedURL("~charmers/utopic/mysql-42", 42)
	// Add a charm to the database (including the archive).
	err := s.store.AddCharmWithArchive(url, storetesting.Charms.CharmArchive(c.MkDir(), "mysql"))
	c.Assert(err, gc.IsNil)
	err = s.store.SetPerms(&url.URL, "read", params.Everyone, url.URL.User)
	c.Assert(err, gc.IsNil)

	// Download the charm archive using the API, passing stats=0.
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL(url.URL.Path() + "/archive?stats=0"),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)

	// Check that the downloads count for the entity has not been updated.
	key := []string{params.StatsArchiveDownload, "utopic", "mysql", "", "42"}
	stats.CheckCounterSum(c, s.store, key, false, 0)
}

var archivePostErrorsTests = []struct {
	about           string
	path            string
	noContentLength bool
	expectStatus    int
	expectMessage   string
	expectCode      params.ErrorCode
}{{
	about:         "no series",
	path:          "~charmers/wordpress/archive",
	expectStatus:  http.StatusBadRequest,
	expectMessage: "series not specified",
	expectCode:    params.ErrBadRequest,
}, {
	about:         "revision specified",
	path:          "~charmers/precise/wordpress-23/archive",
	expectStatus:  http.StatusBadRequest,
	expectMessage: "revision specified, but should not be specified",
	expectCode:    params.ErrBadRequest,
}, {
	about:         "no hash given",
	path:          "~charmers/precise/wordpress/archive",
	expectStatus:  http.StatusBadRequest,
	expectMessage: "hash parameter not specified",
	expectCode:    params.ErrBadRequest,
}, {
	about:           "no content length",
	path:            "~charmers/precise/wordpress/archive?hash=1234563",
	noContentLength: true,
	expectStatus:    http.StatusBadRequest,
	expectMessage:   "Content-Length not specified",
	expectCode:      params.ErrBadRequest,
}}

func (s *ArchiveSuite) TestPostErrors(c *gc.C) {
	type exoticReader struct {
		io.Reader
	}
	for i, test := range archivePostErrorsTests {
		c.Logf("test %d: %s", i, test.about)
		var body io.Reader = strings.NewReader("bogus")
		if test.noContentLength {
			// net/http will automatically add a Content-Length header
			// if it sees *strings.Reader, but not if it's a type it doesn't
			// know about.
			body = exoticReader{body}
		}
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler: s.srv,
			URL:     storeURL(test.path),
			Method:  "POST",
			Header: http.Header{
				"Content-Type": {"application/zip"},
			},
			Body:         body,
			Username:     testUsername,
			Password:     testPassword,
			ExpectStatus: test.expectStatus,
			ExpectBody: params.Error{
				Message: test.expectMessage,
				Code:    test.expectCode,
			},
		})
	}
}

func (s *ArchiveSuite) TestConcurrentUploads(c *gc.C) {
	wordpress := storetesting.Charms.CharmArchive(c.MkDir(), "wordpress")
	f, err := os.Open(wordpress.Path)
	c.Assert(err, gc.IsNil)

	var buf bytes.Buffer
	_, err = io.Copy(&buf, f)
	c.Assert(err, gc.IsNil)

	hash, _ := hashOf(bytes.NewReader(buf.Bytes()))

	srv := httptest.NewServer(s.srv)
	defer srv.Close()

	// Our strategy for testing concurrent uploads is as follows: We
	// repeat uploading a bunch of simultaneous uploads to the same
	// charm. Each upload should either succeed, or fail with an
	// ErrDuplicateUpload error. We make sure that all replies are
	// like this, and that at least one duplicate upload error is
	// found, so that we know we've tested that error path.

	errorBodies := make(chan io.ReadCloser)

	// upload performs one upload of the testing charm.
	// It sends the response body on the errorBodies channel when
	// it finds an error response.
	upload := func() {
		c.Logf("uploading")
		body := bytes.NewReader(buf.Bytes())
		url := srv.URL + storeURL("~charmers/precise/wordpress/archive?hash="+hash)
		req, err := http.NewRequest("POST", url, body)
		c.Assert(err, gc.IsNil)
		req.Header.Set("Content-Type", "application/zip")
		req.SetBasicAuth(testUsername, testPassword)
		resp, err := http.DefaultClient.Do(req)
		if !c.Check(err, gc.IsNil) {
			return
		}
		if resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return
		}
		errorBodies <- resp.Body
	}

	// The try loop continues concurrently uploading
	// charms until it is told to stop (by closing the try
	// channel). It then signals that it has terminated
	// by closing errorBodies.
	try := make(chan struct{})
	go func(try chan struct{}) {
		for _ = range try {
			var wg sync.WaitGroup
			for p := 0; p < 5; p++ {
				wg.Add(1)
				go func() {
					upload()
					wg.Done()
				}()
			}
			wg.Wait()
		}
		close(errorBodies)
	}(try)

	// We continue the loop until we have found an
	// error (or the maximum iteration count has
	// been exceeded).
	foundError := false
	count := 0
loop:
	for {
		select {
		case body, ok := <-errorBodies:
			if !ok {
				// The try loop has terminated,
				// so we need to stop too.
				break loop
			}
			dec := json.NewDecoder(body)
			var errResp params.Error
			err := dec.Decode(&errResp)
			body.Close()
			c.Assert(err, gc.IsNil)
			c.Assert(errResp, jc.DeepEquals, params.Error{
				Message: "duplicate upload",
				Code:    params.ErrDuplicateUpload,
			})
			// We've found the error we're looking for,
			// so we signal to the try loop that it can stop.
			// We will process any outstanding error bodies,
			// before seeing errorBodies closed and exiting
			// the loop.
			foundError = true
			if try != nil {
				close(try)
				try = nil
			}
		case try <- struct{}{}:
			// In cases we've seen, the actual maximum value of
			// count is 1, but let's allow for serious scheduler vagaries.
			if count++; count > 200 {
				c.Fatalf("200 tries with no duplicate error")
			}
		}
	}
	if !foundError {
		c.Errorf("no duplicate-upload errors found")
	}
}

func (s *ArchiveSuite) TestPostCharm(c *gc.C) {
	// A charm that did not exist before should get revision 0.
	s.assertUploadCharm(c, "POST", newResolvedURL("~charmers/precise/wordpress-0", -1), "wordpress")

	// Subsequent charm uploads should increment the
	// revision by 1.
	s.assertUploadCharm(c, "POST", newResolvedURL("~charmers/precise/wordpress-1", -1), "mysql")
}

func (s *ArchiveSuite) TestPostCurrentVersion(c *gc.C) {
	s.assertUploadCharm(c, "POST", newResolvedURL("~charmers/precise/wordpress-0", -1), "wordpress")

	// Subsequent charm uploads should not increment the revision by
	// 1.
	s.assertUploadCharm(c, "POST", newResolvedURL("~charmers/precise/wordpress-0", -1), "wordpress")
}

func (s *ArchiveSuite) TestPutCharm(c *gc.C) {
	s.assertUploadCharm(
		c,
		"PUT",
		newResolvedURL("~charmers/precise/wordpress-3", 3),
		"wordpress",
	)

	s.assertUploadCharm(
		c,
		"PUT",
		newResolvedURL("~charmers/precise/wordpress-1", -1),
		"wordpress",
	)

	// Check that we get a duplicate-upload error if we try to
	// upload to the same revision again.
	s.assertUploadCharmError(
		c,
		"PUT",
		charm.MustParseReference("~charmers/precise/wordpress-3"),
		nil,
		"mysql",
		http.StatusInternalServerError,
		params.Error{
			Message: "duplicate upload",
			Code:    params.ErrDuplicateUpload,
		},
	)

	// Check we get an error if promulgated url already uploaded.
	s.assertUploadCharmError(
		c,
		"PUT",
		charm.MustParseReference("~charmers/precise/wordpress-4"),
		charm.MustParseReference("precise/wordpress-3"),
		"wordpress",
		http.StatusInternalServerError,
		params.Error{
			Message: "duplicate upload",
			Code:    params.ErrDuplicateUpload,
		},
	)

	// Check we get an error if promulgated url has user.
	s.assertUploadCharmError(
		c,
		"PUT",
		charm.MustParseReference("~charmers/precise/wordpress-4"),
		charm.MustParseReference("~charmers/precise/wordpress-4"),
		"mysql",
		http.StatusBadRequest,
		params.Error{
			Message: "promulgated URL cannot have a user",
			Code:    params.ErrBadRequest,
		},
	)

	// Check we get an error if promulgated url has different name.
	s.assertUploadCharmError(
		c,
		"PUT",
		charm.MustParseReference("~charmers/precise/wordpress-4"),
		charm.MustParseReference("precise/mysql-4"),
		"mysql",
		http.StatusBadRequest,
		params.Error{
			Message: "promulgated URL has incorrect charm name",
			Code:    params.ErrBadRequest,
		},
	)
}

func (s *ArchiveSuite) TestPostBundle(c *gc.C) {
	// Upload the required charms.
	err := s.store.AddCharmWithArchive(
		newResolvedURL("cs:~charmers/utopic/mysql-42", 42),
		storetesting.Charms.CharmArchive(c.MkDir(), "mysql"))
	c.Assert(err, gc.IsNil)
	err = s.store.AddCharmWithArchive(
		newResolvedURL("cs:~charmers/utopic/wordpress-47", 47),
		storetesting.Charms.CharmArchive(c.MkDir(), "wordpress"))
	c.Assert(err, gc.IsNil)
	err = s.store.AddCharmWithArchive(
		newResolvedURL("cs:~charmers/utopic/logging-1", 1),
		storetesting.Charms.CharmArchive(c.MkDir(), "logging"))
	c.Assert(err, gc.IsNil)

	// A bundle that did not exist before should get revision 0.
	s.assertUploadBundle(c, "POST", newResolvedURL("~charmers/bundle/wordpress-simple-0", -1), "wordpress-simple")

	// Subsequent bundle uploads should increment the
	// revision by 1.
	s.assertUploadBundle(c, "POST", newResolvedURL("~charmers/bundle/wordpress-simple-1", -1), "wordpress-with-logging")

	// Uploading the same archive twice should not increment the revision...
	s.assertUploadBundle(c, "POST", newResolvedURL("~charmers/bundle/wordpress-simple-1", -1), "wordpress-with-logging")

	// ... but uploading an archive used by a previous revision should.
	s.assertUploadBundle(c, "POST", newResolvedURL("~charmers/bundle/wordpress-simple-2", -1), "wordpress-simple")
}

func (s *ArchiveSuite) TestPostHashMismatch(c *gc.C) {
	content := []byte("some content")
	hash, _ := hashOf(bytes.NewReader(content))

	// Corrupt the content.
	copy(content, "bogus")
	path := fmt.Sprintf("~charmers/precise/wordpress/archive?hash=%s", hash)
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler: s.srv,
		URL:     storeURL(path),
		Method:  "POST",
		Header: http.Header{
			"Content-Type": {"application/zip"},
		},
		Body:         bytes.NewReader(content),
		Username:     testUsername,
		Password:     testPassword,
		ExpectStatus: http.StatusInternalServerError,
		ExpectBody: params.Error{
			Message: "cannot put archive blob: hash mismatch",
		},
	})
}

func invalidZip() io.ReadSeeker {
	return strings.NewReader("invalid zip content")
}

func (s *ArchiveSuite) TestPostInvalidCharmZip(c *gc.C) {
	s.assertCannotUpload(c, "~charmers/precise/wordpress", invalidZip(), "cannot read charm archive: zip: not a valid zip file")
}

func (s *ArchiveSuite) TestPostInvalidBundleZip(c *gc.C) {
	s.assertCannotUpload(c, "~charmers/bundle/wordpress", invalidZip(), "cannot read bundle archive: zip: not a valid zip file")
}

var postInvalidCharmMetadataTests = []struct {
	about       string
	spec        charmtesting.CharmSpec
	expectError string
}{{
	about: "bad provider relation name",
	spec: charmtesting.CharmSpec{
		Meta: `
name: foo
summary: bar
description: d
provides:
    relation-name:
        interface: baz
`,
	},
	expectError: "relation relation-name has almost certainly not been changed from the template",
}, {
	about: "bad provider interface name",
	spec: charmtesting.CharmSpec{
		Meta: `
name: foo
summary: bar
description: d
provides:
    baz:
        interface: interface-name
`,
	},
	expectError: "interface interface-name in relation baz has almost certainly not been changed from the template",
}, {
	about: "bad requirer relation name",
	spec: charmtesting.CharmSpec{
		Meta: `
name: foo
summary: bar
description: d
requires:
    relation-name:
        interface: baz
`,
	},
	expectError: "relation relation-name has almost certainly not been changed from the template",
}, {
	about: "bad requirer interface name",
	spec: charmtesting.CharmSpec{
		Meta: `
name: foo
summary: bar
description: d
requires:
    baz:
        interface: interface-name
`,
	},
	expectError: "interface interface-name in relation baz has almost certainly not been changed from the template",
}, {
	about: "bad peer relation name",
	spec: charmtesting.CharmSpec{
		Meta: `
name: foo
summary: bar
description: d
peers:
    relation-name:
        interface: baz
`,
	},
	expectError: "relation relation-name has almost certainly not been changed from the template",
}, {
	about: "bad peer interface name",
	spec: charmtesting.CharmSpec{
		Meta: `
name: foo
summary: bar
description: d
peers:
    baz:
        interface: interface-name
`,
	},
	expectError: "interface interface-name in relation baz has almost certainly not been changed from the template",
}}

func (s *ArchiveSuite) TestPostInvalidCharmMetadata(c *gc.C) {
	for i, test := range postInvalidCharmMetadataTests {
		c.Logf("test %d: %s", i, test.about)
		ch := charmtesting.NewCharm(c, test.spec)
		r := bytes.NewReader(ch.ArchiveBytes())
		s.assertCannotUpload(c, "~charmers/trusty/wordpress", r, test.expectError)
	}
}

func (s *ArchiveSuite) TestPostInvalidBundleData(c *gc.C) {
	path := storetesting.Charms.BundleArchivePath(c.MkDir(), "bad")
	f, err := os.Open(path)
	c.Assert(err, gc.IsNil)
	defer f.Close()
	// Here we exercise both bundle internal verification (bad relation) and
	// validation with respect to charms (wordpress and mysql are missing).
	expectErr := `bundle verification failed: [` +
		`"relation [\"foo:db\" \"mysql:server\"] refers to service \"foo\" not defined in this bundle",` +
		`"service \"mysql\" refers to non-existent charm \"mysql\"",` +
		`"service \"wordpress\" refers to non-existent charm \"wordpress\""]`
	s.assertCannotUpload(c, "~charmers/bundle/wordpress", f, expectErr)
}

func (s *ArchiveSuite) TestPostCounters(c *gc.C) {
	if !storetesting.MongoJSEnabled() {
		c.Skip("MongoDB JavaScript not available")
	}

	s.assertUploadCharm(c, "POST", newResolvedURL("~charmers/precise/wordpress-0", -1), "wordpress")

	// Check that the upload count for the entity has been updated.
	key := []string{params.StatsArchiveUpload, "precise", "wordpress", "charmers"}
	stats.CheckCounterSum(c, s.store, key, false, 1)
}

func (s *ArchiveSuite) TestPostFailureCounters(c *gc.C) {
	if !storetesting.MongoJSEnabled() {
		c.Skip("MongoDB JavaScript not available")
	}

	hash, _ := hashOf(invalidZip())
	doPost := func(url string, expectCode int) {
		rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler: s.srv,
			URL:     storeURL(url),
			Method:  "POST",
			Header: http.Header{
				"Content-Type": {"application/zip"},
			},
			Body:     invalidZip(),
			Username: testUsername,
			Password: testPassword,
		})
		c.Assert(rec.Code, gc.Equals, expectCode, gc.Commentf("body: %s", rec.Body.Bytes()))
	}

	// Send a first invalid request (revision specified).
	doPost("~charmers/utopic/wordpress-42/archive", http.StatusBadRequest)
	// Send a second invalid request (no hash).
	doPost("~charmers/utopic/wordpress/archive", http.StatusBadRequest)
	// Send a third invalid request (invalid zip).
	doPost("~charmers/utopic/wordpress/archive?hash="+hash, http.StatusInternalServerError)

	// Check that the failed upload count for the entity has been updated.
	key := []string{params.StatsArchiveFailedUpload, "utopic", "wordpress", "charmers"}
	stats.CheckCounterSum(c, s.store, key, false, 3)
}

func (s *ArchiveSuite) TestPostErrorReadsFully(c *gc.C) {
	h := s.handler(c)
	defer h.Close()
	id := charm.MustParseReference("~charmers/trusty/wordpress")
	u, err := url.Parse("http://127.0.0.1/v4/" + id.Path() + "/archive")
	c.Assert(err, gc.IsNil)
	b := bytes.NewBuffer([]byte("test body"))
	r := &http.Request{
		Method: "POST",
		URL:    u,
		Body:   ioutil.NopCloser(b),
		Header: http.Header{},
	}
	r.SetBasicAuth(testUsername, testPassword)
	err = v4.ServeArchive(h, id, nil, r)
	c.Assert(err, gc.ErrorMatches, "hash parameter not specified")
	c.Assert(b.Len(), gc.Equals, 0)
}

func (s *ArchiveSuite) TestPostAuthErrorReadsFully(c *gc.C) {
	h := s.handler(c)
	defer h.Close()
	id := charm.MustParseReference("~charmers/trusty/wordpress")
	u, err := url.Parse("http://127.0.0.1/v4/" + id.Path() + "/archive")
	c.Assert(err, gc.IsNil)
	b := bytes.NewBuffer([]byte("test body"))
	r := &http.Request{
		Method: "POST",
		URL:    u,
		Body:   ioutil.NopCloser(b),
	}
	err = v4.ServeArchive(h, id, nil, r)
	c.Assert(err, gc.ErrorMatches, "authentication failed: missing HTTP auth header")
	c.Assert(b.Len(), gc.Equals, 0)
}

func (s *ArchiveSuite) TestUploadOfCurrentCharmReadsFully(c *gc.C) {
	s.assertUploadCharm(c, "POST", newResolvedURL("~charmers/precise/wordpress-0", -1), "wordpress")

	ch := storetesting.Charms.CharmArchive(c.MkDir(), "wordpress")
	f, err := os.Open(ch.Path)
	c.Assert(err, gc.IsNil)
	defer f.Close()

	// Calculate blob hashes.
	hash := blobstore.NewHash()
	_, err = io.Copy(hash, f)
	c.Assert(err, gc.IsNil)
	hashSum := fmt.Sprintf("%x", hash.Sum(nil))

	// Simulate upload of current version
	h := s.handler(c)
	defer h.Close()
	id := charm.MustParseReference("~charmers/precise/wordpress")
	u, err := url.Parse("http://127.0.0.1/v4/" + id.Path() + "/archive?hash=" + hashSum)
	c.Assert(err, gc.IsNil)
	b := bytes.NewBuffer([]byte("test body"))
	r := &http.Request{
		Method: "POST",
		URL:    u,
		Body:   ioutil.NopCloser(b),
		Header: http.Header{},
	}
	r.ParseForm()
	r.SetBasicAuth(testUsername, testPassword)
	rec := httptest.NewRecorder()
	err = v4.ServeArchive(h, id, rec, r)
	c.Assert(err, gc.IsNil)
	expectId := charm.MustParseReference("~charmers/precise/wordpress-0")
	httptesting.AssertJSONResponse(
		c,
		rec,
		http.StatusOK,
		params.ArchiveUploadResponse{
			Id: expectId,
		},
	)
	c.Assert(b.Len(), gc.Equals, 0)
}

func (s *ArchiveSuite) assertCannotUpload(c *gc.C, id string, content io.ReadSeeker, errorMessage string) {
	hash, size := hashOf(content)
	_, err := content.Seek(0, 0)
	c.Assert(err, gc.IsNil)

	path := fmt.Sprintf("%s/archive?hash=%s", id, hash)
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:       s.srv,
		URL:           storeURL(path),
		Method:        "POST",
		ContentLength: size,
		Header: http.Header{
			"Content-Type": {"application/zip"},
		},
		Body:         content,
		Username:     testUsername,
		Password:     testPassword,
		ExpectStatus: http.StatusInternalServerError,
		ExpectBody: params.Error{
			Message: errorMessage,
		},
	})

	// TODO(rog) check that the uploaded blob has been deleted,
	// by checking that no new blobs have been added to the blob store.
}

// assertUploadCharm uploads the testing charm with the given name
// through the API. The URL must hold the expected revision
// that the charm will be given when uploaded.
func (s *ArchiveSuite) assertUploadCharm(c *gc.C, method string, url *router.ResolvedURL, charmName string) *charm.CharmArchive {
	ch := storetesting.Charms.CharmArchive(c.MkDir(), charmName)
	size := s.assertUpload(c, method, url, ch.Path)
	s.assertEntityInfo(c, url, entityInfo{
		Id: &url.URL,
		Meta: entityMetaInfo{
			ArchiveSize:  &params.ArchiveSizeResponse{Size: size},
			CharmMeta:    ch.Meta(),
			CharmConfig:  ch.Config(),
			CharmActions: ch.Actions(),
		},
	})
	return ch
}

// assertUploadBundle uploads the testing bundle with the given name
// through the API. The URL must hold the expected revision
// that the bundle will be given when uploaded.
func (s *ArchiveSuite) assertUploadBundle(c *gc.C, method string, url *router.ResolvedURL, bundleName string) {
	path := storetesting.Charms.BundleArchivePath(c.MkDir(), bundleName)
	b, err := charm.ReadBundleArchive(path)
	c.Assert(err, gc.IsNil)
	size := s.assertUpload(c, method, url, path)
	s.assertEntityInfo(c, url, entityInfo{
		Id: &url.URL,
		Meta: entityMetaInfo{
			ArchiveSize: &params.ArchiveSizeResponse{Size: size},
			BundleMeta:  b.Data(),
		},
	},
	)
}

func (s *ArchiveSuite) assertUpload(c *gc.C, method string, url *router.ResolvedURL, fileName string) (size int64) {
	f, err := os.Open(fileName)
	c.Assert(err, gc.IsNil)
	defer f.Close()

	// Calculate blob hashes.
	hash := blobstore.NewHash()
	hash256 := sha256.New()
	size, err = io.Copy(io.MultiWriter(hash, hash256), f)
	c.Assert(err, gc.IsNil)
	hashSum := fmt.Sprintf("%x", hash.Sum(nil))
	hash256Sum := fmt.Sprintf("%x", hash256.Sum(nil))
	_, err = f.Seek(0, 0)
	c.Assert(err, gc.IsNil)

	uploadURL := url.URL
	if method == "POST" {
		uploadURL.Revision = -1
	}

	path := fmt.Sprintf("%s/archive?hash=%s", uploadURL.Path(), hashSum)
	purl := url.PromulgatedURL()
	if purl != nil {
		path += fmt.Sprintf("&promulgated=%s", purl.String())
	}
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:       s.srv,
		URL:           storeURL(path),
		Method:        method,
		ContentLength: size,
		Header: http.Header{
			"Content-Type": {"application/zip"},
		},
		Body:     f,
		Username: testUsername,
		Password: testPassword,
		ExpectBody: params.ArchiveUploadResponse{
			Id:            &url.URL,
			PromulgatedId: url.PromulgatedURL(),
		},
	})

	var entity mongodoc.Entity
	err = s.store.DB.Entities().FindId(&url.URL).One(&entity)
	c.Assert(err, gc.IsNil)
	c.Assert(entity.BlobHash, gc.Equals, hashSum)
	c.Assert(entity.BlobHash256, gc.Equals, hash256Sum)
	c.Assert(entity.PromulgatedURL, gc.DeepEquals, purl)
	// Test that the expected entry has been created
	// in the blob store.
	r, _, err := s.store.BlobStore.Open(entity.BlobName)
	c.Assert(err, gc.IsNil)
	r.Close()

	return size
}

// assertUploadCharmError attempts to upload the testing charm with the
// given name through the API, checking that the attempt fails with the
// specified error. The URL must hold the expected revision that the
// charm will be given when uploaded.
func (s *ArchiveSuite) assertUploadCharmError(c *gc.C, method string, url, purl *charm.Reference, charmName string, expectStatus int, expectBody interface{}) {
	ch := storetesting.Charms.CharmArchive(c.MkDir(), charmName)
	s.assertUploadError(c, method, url, purl, ch.Path, expectStatus, expectBody)
}

// assertUploadError asserts that we get an error when uploading
// the contents of the given file to the given url and promulgated URL.
// The reason this method does not take a *router.ResolvedURL
// is so that we can test what happens when an inconsistent promulgated URL
// is passed in.
func (s *ArchiveSuite) assertUploadError(c *gc.C, method string, url, purl *charm.Reference, fileName string, expectStatus int, expectBody interface{}) {
	f, err := os.Open(fileName)
	c.Assert(err, gc.IsNil)
	defer f.Close()

	// Calculate blob hashes.
	hash := blobstore.NewHash()
	size, err := io.Copy(hash, f)
	c.Assert(err, gc.IsNil)
	hashSum := fmt.Sprintf("%x", hash.Sum(nil))
	_, err = f.Seek(0, 0)
	c.Assert(err, gc.IsNil)

	uploadURL := *url
	if method == "POST" {
		uploadURL.Revision = -1
	}

	path := fmt.Sprintf("%s/archive?hash=%s", uploadURL.Path(), hashSum)
	if purl != nil {
		path += fmt.Sprintf("&promulgated=%s", purl.String())
	}
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:       s.srv,
		URL:           storeURL(path),
		Method:        method,
		ContentLength: size,
		Header: http.Header{
			"Content-Type": {"application/zip"},
		},
		Body:         f,
		Username:     testUsername,
		Password:     testPassword,
		ExpectStatus: expectStatus,
		ExpectBody:   expectBody,
	})
}

var archiveFileErrorsTests = []struct {
	about         string
	path          string
	expectStatus  int
	expectMessage string
	expectCode    params.ErrorCode
}{{
	about:         "entity not found",
	path:          "~charmers/trusty/no-such-42/archive/icon.svg",
	expectStatus:  http.StatusNotFound,
	expectMessage: `entity "cs:~charmers/trusty/no-such-42" not found`,
	expectCode:    params.ErrNotFound,
}, {
	about:         "directory listing",
	path:          "~charmers/utopic/wordpress-0/archive/hooks",
	expectStatus:  http.StatusForbidden,
	expectMessage: "directory listing not allowed",
	expectCode:    params.ErrForbidden,
}, {
	about:         "file not found",
	path:          "~charmers/utopic/wordpress-0/archive/no-such",
	expectStatus:  http.StatusNotFound,
	expectMessage: `file "no-such" not found in the archive`,
	expectCode:    params.ErrNotFound,
}}

func (s *ArchiveSuite) TestArchiveFileErrors(c *gc.C) {
	wordpress := storetesting.Charms.CharmArchive(c.MkDir(), "wordpress")
	url := newResolvedURL("cs:~charmers/utopic/wordpress-0", 0)
	err := s.store.AddCharmWithArchive(url, wordpress)
	c.Assert(err, gc.IsNil)
	err = s.store.SetPerms(&url.URL, "read", params.Everyone, url.URL.User)
	c.Assert(err, gc.IsNil)
	for i, test := range archiveFileErrorsTests {
		c.Logf("test %d: %s", i, test.about)
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      s.srv,
			URL:          storeURL(test.path),
			Method:       "GET",
			ExpectStatus: test.expectStatus,
			ExpectBody: params.Error{
				Message: test.expectMessage,
				Code:    test.expectCode,
			},
		})
	}
}

func (s *ArchiveSuite) TestArchiveFileGet(c *gc.C) {
	ch := storetesting.Charms.CharmArchive(c.MkDir(), "all-hooks")
	id := newResolvedURL("cs:~charmers/utopic/all-hooks-0", 0)
	err := s.store.AddCharmWithArchive(id, ch)
	c.Assert(err, gc.IsNil)
	err = s.store.SetPerms(&id.URL, "read", params.Everyone, id.URL.User)
	c.Assert(err, gc.IsNil)
	zipFile, err := zip.OpenReader(ch.Path)
	c.Assert(err, gc.IsNil)
	defer zipFile.Close()

	patchArchiveCacheAges(s)

	// Check a file in the root directory.
	s.assertArchiveFileContents(c, zipFile, "~charmers/utopic/all-hooks-0/archive/metadata.yaml")
	// Check a file in a subdirectory.
	s.assertArchiveFileContents(c, zipFile, "~charmers/utopic/all-hooks-0/archive/hooks/install")
}

// assertArchiveFileContents checks that the response returned by the
// serveArchiveFile endpoint is correct for the given archive and URL path.
func (s *ArchiveSuite) assertArchiveFileContents(c *gc.C, zipFile *zip.ReadCloser, path string) {
	// For example: trusty/django/archive/hooks/install -> hooks/install.
	filePath := strings.SplitN(path, "/archive/", 2)[1]

	// Retrieve the expected bytes.
	var expectBytes []byte
	for _, file := range zipFile.File {
		if file.Name == filePath {
			r, err := file.Open()
			c.Assert(err, gc.IsNil)
			defer r.Close()
			expectBytes, err = ioutil.ReadAll(r)
			c.Assert(err, gc.IsNil)
			break
		}
	}
	c.Assert(expectBytes, gc.Not(gc.HasLen), 0)

	// Make the request.
	url := storeURL(path)
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     url,
	})

	// Ensure the response is what we expect.
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	c.Assert(rec.Body.Bytes(), gc.DeepEquals, expectBytes)
	headers := rec.Header()
	c.Assert(headers.Get("Content-Length"), gc.Equals, strconv.Itoa(len(expectBytes)))
	// We only have text files in the charm repository used for tests.
	c.Assert(headers.Get("Content-Type"), gc.Equals, "text/plain; charset=utf-8")
	assertCacheControl(c, rec.Header(), true)
}

func (s *ArchiveSuite) TestBundleCharms(c *gc.C) {
	// Populate the store with some testing charms.
	mysql := storetesting.Charms.CharmArchive(c.MkDir(), "mysql")
	err := s.store.AddCharmWithArchive(
		newResolvedURL("cs:~charmers/saucy/mysql-0", 0),
		mysql,
	)
	c.Assert(err, gc.IsNil)
	riak := storetesting.Charms.CharmArchive(c.MkDir(), "riak")
	err = s.store.AddCharmWithArchive(
		newResolvedURL("cs:~charmers/trusty/riak-42", 42),
		riak,
	)
	c.Assert(err, gc.IsNil)
	wordpress := storetesting.Charms.CharmArchive(c.MkDir(), "wordpress")
	err = s.store.AddCharmWithArchive(
		newResolvedURL("cs:~charmers/utopic/wordpress-47", 47),
		wordpress,
	)
	c.Assert(err, gc.IsNil)

	// Retrieve the base handler so that we can invoke the
	// bundleCharms method on it.
	handler := s.handler(c)
	defer handler.Close()

	tests := []struct {
		about  string
		ids    []string
		charms map[string]charm.Charm
	}{{
		about: "no ids",
	}, {
		about: "fully qualified ids",
		ids: []string{
			"cs:~charmers/saucy/mysql-0",
			"cs:~charmers/trusty/riak-42",
			"cs:~charmers/utopic/wordpress-47",
		},
		charms: map[string]charm.Charm{
			"cs:~charmers/saucy/mysql-0":       mysql,
			"cs:~charmers/trusty/riak-42":      riak,
			"cs:~charmers/utopic/wordpress-47": wordpress,
		},
	}, {
		about: "partial ids",
		ids:   []string{"~charmers/utopic/wordpress", "~charmers/mysql-0", "~charmers/riak"},
		charms: map[string]charm.Charm{
			"~charmers/mysql-0":          mysql,
			"~charmers/riak":             riak,
			"~charmers/utopic/wordpress": wordpress,
		},
	}, {
		about: "charm not found",
		ids:   []string{"utopic/no-such", "~charmers/mysql"},
		charms: map[string]charm.Charm{
			"~charmers/mysql": mysql,
		},
	}, {
		about: "no charms found",
		ids: []string{
			"cs:~charmers/saucy/mysql-99",   // Revision not present.
			"cs:~charmers/precise/riak-42",  // Series not present.
			"cs:~charmers/utopic/django-47", // Name not present.
		},
	}, {
		about: "repeated charms",
		ids: []string{
			"cs:~charmers/saucy/mysql",
			"cs:~charmers/trusty/riak-42",
			"~charmers/mysql",
		},
		charms: map[string]charm.Charm{
			"cs:~charmers/saucy/mysql":    mysql,
			"cs:~charmers/trusty/riak-42": riak,
			"~charmers/mysql":             mysql,
		},
	}}

	// Run the tests.
	for i, test := range tests {
		c.Logf("test %d: %s", i, test.about)
		charms, err := v4.BundleCharms(handler, test.ids)
		c.Assert(err, gc.IsNil)
		// Ensure the charms returned are what we expect.
		c.Assert(charms, gc.HasLen, len(test.charms))
		for i, ch := range charms {
			expectCharm := test.charms[i]
			c.Assert(ch.Meta(), jc.DeepEquals, expectCharm.Meta())
			c.Assert(ch.Config(), jc.DeepEquals, expectCharm.Config())
			c.Assert(ch.Actions(), jc.DeepEquals, expectCharm.Actions())
			// Since the charm archive and the charm entity have a slightly
			// different concept of what a revision is, and since the revision
			// is not used for bundle validation, we can safely avoid checking
			// the charm revision.
		}
	}
}

func (s *ArchiveSuite) TestDelete(c *gc.C) {
	// Add a charm to the database (including the archive).
	id := "~charmers/utopic/mysql-42"
	url := newResolvedURL(id, -1)
	err := s.store.AddCharmWithArchive(url, storetesting.Charms.CharmArchive(c.MkDir(), "mysql"))
	c.Assert(err, gc.IsNil)

	// Retrieve the corresponding entity.
	var entity mongodoc.Entity
	err = s.store.DB.Entities().FindId(&url.URL).Select(bson.D{{"blobname", 1}}).One(&entity)
	c.Assert(err, gc.IsNil)

	// Delete the charm using the API.
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL(id + "/archive"),
		Method:       "DELETE",
		Username:     testUsername,
		Password:     testPassword,
		ExpectStatus: http.StatusOK,
	})

	// The entity has been deleted.
	count, err := s.store.DB.Entities().FindId(url).Count()
	c.Assert(err, gc.IsNil)
	c.Assert(count, gc.Equals, 0)

	// The blob has been deleted.
	_, _, err = s.store.BlobStore.Open(entity.BlobName)
	c.Assert(err, gc.ErrorMatches, "resource.*not found")
}

func (s *ArchiveSuite) TestDeleteSpecificCharm(c *gc.C) {
	// Add a couple of charms to the database.
	for _, id := range []string{"~charmers/trusty/mysql-42", "~charmers/utopic/mysql-42", "~charmers/utopic/mysql-47"} {
		err := s.store.AddCharmWithArchive(
			newResolvedURL(id, -1),
			storetesting.Charms.CharmArchive(c.MkDir(), "mysql"))
		c.Assert(err, gc.IsNil)
	}

	// Delete the second charm using the API.
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("~charmers/utopic/mysql-42/archive"),
		Method:       "DELETE",
		Username:     testUsername,
		Password:     testPassword,
		ExpectStatus: http.StatusOK,
	})

	// The other two charms are still present in the database.
	urls := []*charm.Reference{
		charm.MustParseReference("~charmers/trusty/mysql-42"),
		charm.MustParseReference("~charmers/utopic/mysql-47"),
	}
	count, err := s.store.DB.Entities().Find(bson.D{{
		"_id", bson.D{{"$in", urls}},
	}}).Count()
	c.Assert(err, gc.IsNil)
	c.Assert(count, gc.Equals, 2)
}

func (s *ArchiveSuite) TestDeleteNotFound(c *gc.C) {
	// Try to delete a non existing charm using the API.
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("~charmers/utopic/no-such-0/archive"),
		Method:       "DELETE",
		Username:     testUsername,
		Password:     testPassword,
		ExpectStatus: http.StatusNotFound,
		ExpectBody: params.Error{
			Message: `entity "cs:~charmers/utopic/no-such-0" not found`,
			Code:    params.ErrNotFound,
		},
	})
}

func (s *ArchiveSuite) TestDeleteError(c *gc.C) {
	// Add a charm to the database (not including the archive).
	id := "~charmers/utopic/mysql-42"
	url := newResolvedURL(id, -1)
	err := s.store.AddCharm(storetesting.Charms.CharmArchive(c.MkDir(), "mysql"),
		charmstore.AddParams{
			URL:      url,
			BlobName: "no-such-name",
			BlobHash: fakeBlobHash,
			BlobSize: fakeBlobSize,
		})
	c.Assert(err, gc.IsNil)

	// Try to delete the charm using the API.
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL(id + "/archive"),
		Method:       "DELETE",
		Username:     testUsername,
		Password:     testPassword,
		ExpectStatus: http.StatusInternalServerError,
		ExpectBody: params.Error{
			Message: `cannot remove blob no-such-name: resource at path "global/no-such-name" not found`,
		},
	})
}

func (s *ArchiveSuite) TestDeleteCounters(c *gc.C) {
	if !storetesting.MongoJSEnabled() {
		c.Skip("MongoDB JavaScript not available")
	}

	// Add a charm to the database (including the archive).
	id := "~charmers/utopic/mysql-42"
	err := s.store.AddCharmWithArchive(
		newResolvedURL(id, -1),
		storetesting.Charms.CharmArchive(c.MkDir(), "mysql"))
	c.Assert(err, gc.IsNil)

	// Delete the charm using the API.
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler:  s.srv,
		Method:   "DELETE",
		URL:      storeURL(id + "/archive"),
		Username: testUsername,
		Password: testPassword,
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)

	// Check that the delete count for the entity has been updated.
	key := []string{params.StatsArchiveDelete, "utopic", "mysql", "charmers", "42"}
	stats.CheckCounterSum(c, s.store, key, false, 1)
}

func (s *ArchiveSuite) TestPostAuthErrors(c *gc.C) {
	checkAuthErrors(c, s.srv, "POST", "~charmers/utopic/django/archive")
}

func (s *ArchiveSuite) TestDeleteAuthErrors(c *gc.C) {
	err := s.store.AddCharmWithArchive(
		newResolvedURL("~charmers/utopic/django-42", 42),
		storetesting.Charms.CharmArchive(c.MkDir(), "wordpress"),
	)
	c.Assert(err, gc.IsNil)
	checkAuthErrors(c, s.srv, "DELETE", "utopic/django-42/archive")
}

var archiveAuthErrorsTests = []struct {
	about         string
	header        http.Header
	username      string
	password      string
	expectMessage string
}{{
	about:         "no credentials",
	expectMessage: "authentication failed: missing HTTP auth header",
}, {
	about: "invalid encoding",
	header: http.Header{
		"Authorization": {"Basic not-a-valid-base64"},
	},
	expectMessage: "authentication failed: invalid HTTP auth encoding",
}, {
	about: "invalid header",
	header: http.Header{
		"Authorization": {"Basic " + base64.StdEncoding.EncodeToString([]byte("invalid"))},
	},
	expectMessage: "authentication failed: invalid HTTP auth contents",
}, {
	about:         "invalid credentials",
	username:      "no-such",
	password:      "exterminate!",
	expectMessage: "invalid user name or password",
}}

func checkAuthErrors(c *gc.C, handler http.Handler, method, url string) {
	archiveURL := storeURL(url)
	for i, test := range archiveAuthErrorsTests {
		c.Logf("test %d: %s", i, test.about)
		if test.header == nil {
			test.header = http.Header{}
		}
		if method == "POST" {
			test.header.Add("Content-Type", "application/zip")
		}
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      handler,
			URL:          archiveURL,
			Method:       method,
			Header:       test.header,
			Username:     test.username,
			Password:     test.password,
			ExpectStatus: http.StatusUnauthorized,
			ExpectBody: params.Error{
				Message: test.expectMessage,
				Code:    params.ErrUnauthorized,
			},
		})
	}
}

// entityInfo holds all the information we want to find
// out about a charm or bundle uploaded to the store.
type entityInfo struct {
	Id   *charm.Reference
	Meta entityMetaInfo
}

type entityMetaInfo struct {
	ArchiveSize  *params.ArchiveSizeResponse `json:"archive-size,omitempty"`
	CharmMeta    *charm.Meta                 `json:"charm-metadata,omitempty"`
	CharmConfig  *charm.Config               `json:"charm-config,omitempty"`
	CharmActions *charm.Actions              `json:"charm-actions,omitempty"`
	BundleMeta   *charm.BundleData           `json:"bundle-metadata,omitempty"`
}

func (s *ArchiveSuite) assertEntityInfo(c *gc.C, url *router.ResolvedURL, expect entityInfo) {
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler: s.srv,
		URL: storeURL(
			url.URL.Path() + "/meta/any" +
				"?include=archive-size" +
				"&include=charm-metadata" +
				"&include=charm-config" +
				"&include=charm-actions" +
				"&include=bundle-metadata",
		),
		Username:   testUsername,
		Password:   testPassword,
		ExpectBody: expect,
	})
}

func (s *ArchiveSuite) TestArchiveFileGetHasCORSHeaders(c *gc.C) {
	id := "~charmers/precise/wordpress-0"
	s.assertUploadCharm(c, "POST", newResolvedURL(id, -1), "wordpress")
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL(fmt.Sprintf("%s/archive/metadata.yaml", id)),
	})
	headers := rec.Header()
	c.Assert(len(headers["Access-Control-Allow-Origin"]), gc.Equals, 1)
	c.Assert(len(headers["Access-Control-Allow-Headers"]), gc.Equals, 1)
	c.Assert(headers["Access-Control-Allow-Origin"][0], gc.Equals, "*")
	c.Assert(headers["Access-Control-Allow-Headers"][0], gc.Equals, "X-Requested-With")
}

var getNewPromulgatedRevisionTests = []struct {
	about     string
	id        *charm.Reference
	expectRev int
}{{
	about:     "no base entity",
	id:        charm.MustParseReference("cs:~mmouse/trusty/mysql-14"),
	expectRev: -1,
}, {
	about:     "not promulgated",
	id:        charm.MustParseReference("cs:~dduck/trusty/mysql-14"),
	expectRev: -1,
}, {
	about:     "not yet promulgated",
	id:        charm.MustParseReference("cs:~goofy/trusty/mysql-14"),
	expectRev: 0,
}, {
	about:     "existing promulgated",
	id:        charm.MustParseReference("cs:~pluto/trusty/mariadb-14"),
	expectRev: 4,
}, {
	about:     "previous promulgated by different user",
	id:        charm.MustParseReference("cs:~tom/trusty/sed-1"),
	expectRev: 5,
}, {
	about:     "many previous promulgated revisions",
	id:        charm.MustParseReference("cs:~tom/trusty/awk-5"),
	expectRev: 5,
}}

func (s *ArchiveSuite) TestGetNewPromulgatedRevision(c *gc.C) {
	err := s.store.DB.BaseEntities().Insert(&mongodoc.BaseEntity{
		URL:    charm.MustParseReference("cs:~dduck/mysql"),
		User:   "dduck",
		Name:   "mysql",
		Public: true,
		ACLs: mongodoc.ACL{
			Read: []string{"everyone", "dduck"},
		},
		Promulgated: false,
	})
	c.Assert(err, gc.IsNil)
	err = s.store.DB.BaseEntities().Insert(&mongodoc.BaseEntity{
		URL:    charm.MustParseReference("cs:~goofy/mysql"),
		User:   "goofy",
		Name:   "mysql",
		Public: true,
		ACLs: mongodoc.ACL{
			Read: []string{"everyone", "goofy"},
		},
		Promulgated: true,
	})
	c.Assert(err, gc.IsNil)
	err = s.store.DB.BaseEntities().Insert(&mongodoc.BaseEntity{
		URL:    charm.MustParseReference("cs:~pluto/mariadb"),
		User:   "pluto",
		Name:   "mariadb",
		Public: true,
		ACLs: mongodoc.ACL{
			Read: []string{"everyone", "pluto"},
		},
		Promulgated: true,
	})
	c.Assert(err, gc.IsNil)
	err = s.store.DB.BaseEntities().Insert(&mongodoc.BaseEntity{
		URL:    charm.MustParseReference("cs:~tom/sed"),
		User:   "tom",
		Name:   "sed",
		Public: true,
		ACLs: mongodoc.ACL{
			Read: []string{"everyone", "tom"},
		},
		Promulgated: true,
	})
	c.Assert(err, gc.IsNil)
	err = s.store.DB.BaseEntities().Insert(&mongodoc.BaseEntity{
		URL:    charm.MustParseReference("cs:~jerry/sed"),
		User:   "jerry",
		Name:   "sed",
		Public: true,
		ACLs: mongodoc.ACL{
			Read: []string{"everyone", "jerry"},
		},
		Promulgated: false,
	})
	c.Assert(err, gc.IsNil)
	err = s.store.DB.BaseEntities().Insert(&mongodoc.BaseEntity{
		URL:    charm.MustParseReference("cs:~tom/awk"),
		User:   "tom",
		Name:   "awk",
		Public: true,
		ACLs: mongodoc.ACL{
			Read: []string{"everyone", "tom"},
		},
		Promulgated: true,
	})
	c.Assert(err, gc.IsNil)
	err = s.store.DB.Entities().Insert(&mongodoc.Entity{
		URL:                 charm.MustParseReference("cs:~pluto/trusty/mariadb-5"),
		User:                "pluto",
		Name:                "mariadb",
		Series:              "trusty",
		Revision:            5,
		PromulgatedURL:      charm.MustParseReference("cs:trusty/mariadb-3"),
		PromulgatedRevision: 3,
	})
	c.Assert(err, gc.IsNil)
	err = s.store.DB.Entities().Insert(&mongodoc.Entity{
		URL:                 charm.MustParseReference("cs:~tom/trusty/sed-0"),
		User:                "tom",
		Name:                "sed",
		Series:              "trusty",
		Revision:            0,
		PromulgatedURL:      charm.MustParseReference("cs:trusty/sed-0"),
		PromulgatedRevision: 0,
	})
	c.Assert(err, gc.IsNil)
	err = s.store.DB.Entities().Insert(&mongodoc.Entity{
		URL:                 charm.MustParseReference("cs:~jerry/trusty/sed-3"),
		User:                "jerry",
		Name:                "sed",
		Series:              "trusty",
		Revision:            3,
		PromulgatedURL:      charm.MustParseReference("cs:trusty/sed-4"),
		PromulgatedRevision: 4,
	})
	c.Assert(err, gc.IsNil)
	id := charm.MustParseReference("cs:~tom/trusty/awk")
	pid := charm.MustParseReference("cs:trusty/awk")
	for i := 0; i < 5; i++ {
		id.Revision = i
		pid.Revision = i
		err = s.store.DB.Entities().Insert(&mongodoc.Entity{
			URL:                 id,
			User:                "tom",
			Name:                "awk",
			Series:              "trusty",
			Revision:            i,
			PromulgatedURL:      pid,
			PromulgatedRevision: i,
		})
		c.Assert(err, gc.IsNil)
	}
	handler := s.handler(c)
	defer handler.Close()
	for i, test := range getNewPromulgatedRevisionTests {
		c.Logf("%d. %s", i, test.about)
		rev, err := v4.GetNewPromulgatedRevision(handler, test.id)
		c.Assert(err, gc.IsNil)
		c.Assert(rev, gc.Equals, test.expectRev)
	}
}

func hashOfBytes(data []byte) string {
	hash := blobstore.NewHash()
	hash.Write(data)
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func hashOf(r io.Reader) (hashSum string, size int64) {
	hash := blobstore.NewHash()
	n, err := io.Copy(hash, r)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), n
}

func patchArchiveCacheAges(s interface {
	PatchValue(interface{}, interface{})
}) {
	s.PatchValue(v4.ArchiveCacheVersionedMaxAge, 20*time.Second)
	s.PatchValue(v4.ArchiveCacheNonVersionedMaxAge, 5*time.Second)
}

// assertCacheControl asserts that the cache control headers are
// appropriately set. The isFullySpecified parameter specifies
// whether the id in the request was fully specified.
// It assumes that patchArchiveCacheAges has been called
// for the current test.
func assertCacheControl(c *gc.C, h http.Header, idFullySpecified bool) {
	seconds := 5
	if idFullySpecified {
		seconds = 20
	}
	c.Assert(h.Get("Cache-Control"), gc.Equals, fmt.Sprintf("public, max-age=%d", seconds))
}

type ArchiveSearchSuite struct {
	commonSuite
}

var _ = gc.Suite(&ArchiveSearchSuite{})

func (s *ArchiveSearchSuite) SetUpSuite(c *gc.C) {
	s.enableES = true
	s.commonSuite.SetUpSuite(c)
}

func (s *ArchiveSearchSuite) SetUpTest(c *gc.C) {
	s.commonSuite.SetUpTest(c)
	// TODO (frankban): remove this call when removing the legacy counts logic.
	patchLegacyDownloadCountsEnabled(s.AddCleanup, false)
}

func (s *ArchiveSearchSuite) TestGetSearchUpdate(c *gc.C) {
	if !storetesting.MongoJSEnabled() {
		c.Skip("MongoDB JavaScript not available")
	}

	for i, id := range []string{"~charmers/utopic/mysql-42", "~who/utopic/mysql-42"} {
		c.Logf("test %d: %s", i, id)
		url := newResolvedURL(id, -1)

		// Add a charm to the database (including the archive).
		err := s.store.AddCharmWithArchive(url, storetesting.Charms.CharmArchive(c.MkDir(), "mysql"))
		c.Assert(err, gc.IsNil)
		err = s.store.SetPerms(&url.URL, "read", params.Everyone, url.URL.User)
		c.Assert(err, gc.IsNil)

		// Download the charm archive using the API.
		rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler: s.srv,
			URL:     storeURL(id + "/archive"),
		})
		c.Assert(rec.Code, gc.Equals, http.StatusOK)

		// Check that the search record for the entity has been updated.
		stats.CheckSearchTotalDownloads(c, s.store, &url.URL, 1)
	}
}
