// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4_test

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"

	jc "github.com/juju/testing/checkers"
	"gopkg.in/juju/charm.v3"
	"gopkg.in/juju/charm.v3/testing"
	charmtesting "gopkg.in/juju/charm.v3/testing"
	"gopkg.in/mgo.v2/bson"
	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore/internal/blobstore"
	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/storetesting"
	"github.com/juju/charmstore/internal/storetesting/stats"
	"github.com/juju/charmstore/internal/v4"
	"github.com/juju/charmstore/params"
)

type ArchiveSuite struct {
	storetesting.IsolatedMgoSuite
	srv   http.Handler
	store *charmstore.Store
}

var _ = gc.Suite(&ArchiveSuite{})

func (s *ArchiveSuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	s.srv, s.store = newServer(c, s.Session, serverParams)
}

func (s *ArchiveSuite) TestGet(c *gc.C) {
	wordpress := s.assertUploadCharm(c, mustParseReference("cs:precise/wordpress-0"), "wordpress")

	archiveBytes, err := ioutil.ReadFile(wordpress.Path)
	c.Assert(err, gc.IsNil)

	archiveUrl := storeURL("precise/wordpress-0/archive")
	rec := storetesting.DoRequest(c, storetesting.DoRequestParams{
		Handler: s.srv,
		URL:     archiveUrl,
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	c.Assert(rec.Body.Bytes(), gc.DeepEquals, archiveBytes)

	// Check that the HTTP range logic is plugged in OK. If this
	// is working, we assume that the whole thing is working OK,
	// as net/http is well-tested.
	rec = storetesting.DoRequest(c, storetesting.DoRequestParams{
		Handler: s.srv,
		URL:     archiveUrl,
		Header:  http.Header{"Range": {"bytes=10-100"}},
	})
	c.Assert(rec.Code, gc.Equals, http.StatusPartialContent, gc.Commentf("body: %q", rec.Body.Bytes()))
	c.Assert(rec.Body.Bytes(), gc.HasLen, 100-10+1)
	c.Assert(rec.Body.Bytes(), gc.DeepEquals, archiveBytes[10:101])
}

func (s *ArchiveSuite) TestGetCounters(c *gc.C) {
	if !storetesting.MongoJSEnabled() {
		c.Skip("MongoDB JavaScript not available")
	}

	for i, id := range []string{"utopic/mysql-42", "~who/utopic/mysql-42"} {
		c.Logf("test %d: %s", i, id)
		url := mustParseReference(id)

		// Add a charm to the database (including the archive).
		err := s.store.AddCharmWithArchive(url, charmtesting.Charms.CharmArchive(c.MkDir(), "mysql"))
		c.Assert(err, gc.IsNil)

		// Download the charm archive using the API.
		rec := storetesting.DoRequest(c, storetesting.DoRequestParams{
			Handler: s.srv,
			URL:     storeURL(id + "/archive"),
		})
		c.Assert(rec.Code, gc.Equals, http.StatusOK)

		// Check that the downloads count for the entity has been updated.
		key := []string{params.StatsArchiveDownload, "utopic", "mysql", url.User, "42"}
		stats.CheckCounterSum(c, s.store, key, false, 1)
	}
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
	path:          "wordpress/archive",
	expectStatus:  http.StatusBadRequest,
	expectMessage: "series not specified",
	expectCode:    params.ErrBadRequest,
}, {
	about:         "revision specified",
	path:          "precise/wordpress-23/archive",
	expectStatus:  http.StatusBadRequest,
	expectMessage: "revision specified, but should not be specified",
	expectCode:    params.ErrBadRequest,
}, {
	about:         "no hash given",
	path:          "precise/wordpress/archive",
	expectStatus:  http.StatusBadRequest,
	expectMessage: "hash parameter not specified",
	expectCode:    params.ErrBadRequest,
}, {
	about:           "no content length",
	path:            "precise/wordpress/archive?hash=1234563",
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
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
			Handler: s.srv,
			URL:     storeURL(test.path),
			Method:  "POST",
			Header: http.Header{
				"Content-Type": {"application/zip"},
			},
			Body:         body,
			Username:     serverParams.AuthUsername,
			Password:     serverParams.AuthPassword,
			ExpectStatus: test.expectStatus,
			ExpectBody: params.Error{
				Message: test.expectMessage,
				Code:    test.expectCode,
			},
		})
	}
}

func (s *ArchiveSuite) TestConcurrentUploads(c *gc.C) {
	wordpress := charmtesting.Charms.CharmArchive(c.MkDir(), "wordpress")
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
		url := srv.URL + storeURL("precise/wordpress/archive?hash="+hash)
		req, err := http.NewRequest("POST", url, body)
		c.Assert(err, gc.IsNil)
		req.Header.Set("Content-Type", "application/zip")
		req.SetBasicAuth(serverParams.AuthUsername, serverParams.AuthPassword)
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
	go func() {
		for {
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
		}
	}()

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
	s.assertUploadCharm(c, mustParseReference("precise/wordpress-0"), "wordpress")

	// Subsequent charm uploads should increment the
	// revision by 1.
	s.assertUploadCharm(c, mustParseReference("precise/wordpress-1"), "wordpress")
}

func (s *ArchiveSuite) TestPostBundle(c *gc.C) {
	// Upload the required charms.
	err := s.store.AddCharmWithArchive(
		mustParseReference("cs:utopic/mysql-42"),
		charmtesting.Charms.CharmArchive(c.MkDir(), "mysql"))
	c.Assert(err, gc.IsNil)
	err = s.store.AddCharmWithArchive(
		mustParseReference("cs:utopic/wordpress-47"),
		charmtesting.Charms.CharmArchive(c.MkDir(), "wordpress"))
	c.Assert(err, gc.IsNil)

	// A bundle that did not exist before should get revision 0.
	s.assertUploadBundle(c, mustParseReference("bundle/wordpress-0"), "wordpress")

	// Subsequent bundle uploads should increment the
	// revision by 1.
	s.assertUploadBundle(c, mustParseReference("bundle/wordpress-1"), "wordpress")
}

func (s *ArchiveSuite) TestPostHashMismatch(c *gc.C) {
	content := []byte("some content")
	hash, _ := hashOf(bytes.NewReader(content))

	// Corrupt the content.
	copy(content, "bogus")
	path := fmt.Sprintf("precise/wordpress/archive?hash=%s", hash)
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler: s.srv,
		URL:     storeURL(path),
		Method:  "POST",
		Header: http.Header{
			"Content-Type": {"application/zip"},
		},
		Body:         bytes.NewReader(content),
		Username:     serverParams.AuthUsername,
		Password:     serverParams.AuthPassword,
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
	s.assertCannotUpload(c, "precise/wordpress", invalidZip(), "cannot read charm archive: zip: not a valid zip file")
}

func (s *ArchiveSuite) TestPostInvalidBundleZip(c *gc.C) {
	s.assertCannotUpload(c, "bundle/wordpress", invalidZip(), "cannot read bundle archive: zip: not a valid zip file")
}

func (s *ArchiveSuite) TestPostInvalidBundleData(c *gc.C) {
	path := testing.Charms.BundleArchivePath(c.MkDir(), "bad")
	f, err := os.Open(path)
	c.Assert(err, gc.IsNil)
	defer f.Close()
	// Here we exercise both bundle internal verification (bad relation) and
	// validation with respect to charms (wordpress and mysql are missing).
	expectErr := `bundle verification failed: [` +
		`"relation [\"foo:db\" \"mysql:server\"] refers to service \"foo\" not defined in this bundle",` +
		`"service \"mysql\" refers to non-existent charm \"mysql\"",` +
		`"service \"wordpress\" refers to non-existent charm \"wordpress\""]`
	s.assertCannotUpload(c, "bundle/wordpress", f, expectErr)
}

func (s *ArchiveSuite) TestPostCounters(c *gc.C) {
	if !storetesting.MongoJSEnabled() {
		c.Skip("MongoDB JavaScript not available")
	}

	s.assertUploadCharm(c, mustParseReference("precise/wordpress-0"), "wordpress")

	// Check that the upload count for the entity has been updated.
	key := []string{params.StatsArchiveUpload, "precise", "wordpress", ""}
	stats.CheckCounterSum(c, s.store, key, false, 1)
}

func (s *ArchiveSuite) TestPostFailureCounters(c *gc.C) {
	if !storetesting.MongoJSEnabled() {
		c.Skip("MongoDB JavaScript not available")
	}

	hash, _ := hashOf(invalidZip())
	doPost := func(url string, expectCode int) {
		rec := storetesting.DoRequest(c, storetesting.DoRequestParams{
			Handler: s.srv,
			URL:     storeURL(url),
			Method:  "POST",
			Header: http.Header{
				"Content-Type": {"application/zip"},
			},
			Body:     invalidZip(),
			Username: serverParams.AuthUsername,
			Password: serverParams.AuthPassword,
		})
		c.Assert(rec.Code, gc.Equals, expectCode)
	}

	// Send a first invalid request (revision specified).
	doPost("utopic/wordpress-42/archive", http.StatusBadRequest)
	// Send a second invalid request (no hash).
	doPost("utopic/wordpress/archive", http.StatusBadRequest)
	// Send a third invalid request (invalid zip).
	doPost("utopic/wordpress/archive?hash="+hash, http.StatusInternalServerError)

	// Check that the failed upload count for the entity has been updated.
	key := []string{params.StatsArchiveFailedUpload, "utopic", "wordpress", ""}
	stats.CheckCounterSum(c, s.store, key, false, 3)
}

func (s *ArchiveSuite) assertCannotUpload(c *gc.C, id string, content io.ReadSeeker, errorMessage string) {
	hash, size := hashOf(content)
	_, err := content.Seek(0, 0)
	c.Assert(err, gc.IsNil)

	path := fmt.Sprintf("%s/archive?hash=%s", id, hash)
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:       s.srv,
		URL:           storeURL(path),
		Method:        "POST",
		ContentLength: size,
		Header: http.Header{
			"Content-Type": {"application/zip"},
		},
		Body:         content,
		Username:     serverParams.AuthUsername,
		Password:     serverParams.AuthPassword,
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
func (s *ArchiveSuite) assertUploadCharm(c *gc.C, url *charm.Reference, charmName string) *charm.CharmArchive {
	ch := testing.Charms.CharmArchive(c.MkDir(), charmName)
	size := s.assertUpload(c, url, ch.Path)
	s.assertEntityInfo(c, url, entityInfo{
		Id: url,
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
func (s *ArchiveSuite) assertUploadBundle(c *gc.C, url *charm.Reference, bundleName string) {
	path := testing.Charms.BundleArchivePath(c.MkDir(), bundleName)
	b, err := charm.ReadBundleArchive(path)
	c.Assert(err, gc.IsNil)
	size := s.assertUpload(c, url, path)
	s.assertEntityInfo(c, url, entityInfo{
		Id: url,
		Meta: entityMetaInfo{
			ArchiveSize: &params.ArchiveSizeResponse{Size: size},
			BundleMeta:  b.Data(),
		},
	},
	)
}

func (s *ArchiveSuite) assertUpload(c *gc.C, url *charm.Reference, fileName string) (size int64) {
	f, err := os.Open(fileName)
	c.Assert(err, gc.IsNil)
	defer f.Close()
	hash, size := hashOf(f)
	_, err = f.Seek(0, 0)
	c.Assert(err, gc.IsNil)

	uploadURL := *url
	uploadURL.Revision = -1

	path := fmt.Sprintf("%s/archive?hash=%s", strings.TrimPrefix(uploadURL.String(), "cs:"), hash)
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:       s.srv,
		URL:           storeURL(path),
		Method:        "POST",
		ContentLength: size,
		Header: http.Header{
			"Content-Type": {"application/zip"},
		},
		Body:     f,
		Username: serverParams.AuthUsername,
		Password: serverParams.AuthPassword,
		ExpectBody: params.ArchivePostResponse{
			Id: url,
		},
	})

	var entity mongodoc.Entity
	err = s.store.DB.Entities().FindId(url).One(&entity)
	c.Assert(err, gc.IsNil)
	// Test that the expected entry has been created
	// in the blob store.
	r, _, err := s.store.BlobStore.Open(entity.BlobName)
	c.Assert(err, gc.IsNil)
	r.Close()
	return size
}

var archiveFileErrorsTests = []struct {
	about         string
	path          string
	expectStatus  int
	expectMessage string
	expectCode    params.ErrorCode
}{{
	about:         "entity not found",
	path:          "trusty/no-such-42/archive/icon.svg",
	expectStatus:  http.StatusNotFound,
	expectMessage: "entity not found",
	expectCode:    params.ErrNotFound,
}, {
	about:         "directory listing",
	path:          "utopic/wordpress-0/archive/hooks",
	expectStatus:  http.StatusForbidden,
	expectMessage: "directory listing not allowed",
	expectCode:    params.ErrForbidden,
}, {
	about:         "file not found",
	path:          "utopic/wordpress-0/archive/no-such",
	expectStatus:  http.StatusNotFound,
	expectMessage: `file "no-such" not found in the archive`,
	expectCode:    params.ErrNotFound,
}}

func (s *ArchiveSuite) TestArchiveFileErrors(c *gc.C) {
	wordpress := charmtesting.Charms.CharmArchive(c.MkDir(), "wordpress")
	url := mustParseReference("cs:utopic/wordpress-0")
	err := s.store.AddCharmWithArchive(url, wordpress)
	c.Assert(err, gc.IsNil)
	for i, test := range archiveFileErrorsTests {
		c.Logf("test %d: %s", i, test.about)
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
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
	ch := charmtesting.Charms.CharmArchive(c.MkDir(), "all-hooks")
	err := s.store.AddCharmWithArchive(mustParseReference("cs:utopic/all-hooks-0"), ch)
	c.Assert(err, gc.IsNil)
	zipFile, err := zip.OpenReader(ch.Path)
	c.Assert(err, gc.IsNil)
	defer zipFile.Close()

	// Check a file in the root directory.
	s.assertArchiveFileContents(c, zipFile, "utopic/all-hooks-0/archive/metadata.yaml")
	// Check a file in a subdirectory.
	s.assertArchiveFileContents(c, zipFile, "utopic/all-hooks-0/archive/hooks/install")
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
	rec := storetesting.DoRequest(c, storetesting.DoRequestParams{
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
}

func (s *ArchiveSuite) TestBundleCharms(c *gc.C) {
	// Populate the store with some testing charms.
	mysql := charmtesting.Charms.CharmArchive(c.MkDir(), "mysql")
	err := s.store.AddCharmWithArchive(
		mustParseReference("cs:saucy/mysql-0"), mysql)
	c.Assert(err, gc.IsNil)
	riak := charmtesting.Charms.CharmArchive(c.MkDir(), "riak")
	err = s.store.AddCharmWithArchive(
		mustParseReference("cs:trusty/riak-42"), riak)
	c.Assert(err, gc.IsNil)
	wordpress := charmtesting.Charms.CharmArchive(c.MkDir(), "wordpress")
	err = s.store.AddCharmWithArchive(
		mustParseReference("cs:utopic/wordpress-47"), wordpress)
	c.Assert(err, gc.IsNil)

	// Retrieve the bundleCharms method.
	handler := v4.New(s.store, serverParams)
	bundleCharms := v4.BundleCharms(handler)

	tests := []struct {
		about  string
		ids    []string
		charms map[string]charm.Charm
	}{{
		about: "no ids",
	}, {
		about: "fully qualified ids",
		ids: []string{
			"cs:saucy/mysql-0",
			"cs:trusty/riak-42",
			"cs:utopic/wordpress-47",
		},
		charms: map[string]charm.Charm{
			"cs:saucy/mysql-0":       mysql,
			"cs:trusty/riak-42":      riak,
			"cs:utopic/wordpress-47": wordpress,
		},
	}, {
		about: "partial ids",
		ids:   []string{"utopic/wordpress", "mysql-0", "riak"},
		charms: map[string]charm.Charm{
			"mysql-0":          mysql,
			"riak":             riak,
			"utopic/wordpress": wordpress,
		},
	}, {
		about: "charm not found",
		ids:   []string{"utopic/no-such", "mysql"},
		charms: map[string]charm.Charm{
			"mysql": mysql,
		},
	}, {
		about: "no charms found",
		ids: []string{
			"cs:saucy/mysql-99",   // Revision not present.
			"cs:precise/riak-42",  // Series not present.
			"cs:utopic/django-47", // Name not present.
		},
	}, {
		about: "repeated charms",
		ids: []string{
			"cs:saucy/mysql",
			"cs:trusty/riak-42",
			"mysql",
		},
		charms: map[string]charm.Charm{
			"cs:saucy/mysql":    mysql,
			"cs:trusty/riak-42": riak,
			"mysql":             mysql,
		},
	}}

	// Run the tests.
	for i, test := range tests {
		c.Logf("test %d: %s", i, test.about)
		charms, err := bundleCharms(test.ids)
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
	id := "utopic/mysql-42"
	url := mustParseReference(id)
	err := s.store.AddCharmWithArchive(url, charmtesting.Charms.CharmArchive(c.MkDir(), "mysql"))
	c.Assert(err, gc.IsNil)

	// Retrieve the corresponding entity.
	var entity mongodoc.Entity
	err = s.store.DB.Entities().FindId(url).Select(bson.D{{"blobname", 1}}).One(&entity)
	c.Assert(err, gc.IsNil)

	// Delete the charm using the API.
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL(id + "/archive"),
		Method:       "DELETE",
		Username:     serverParams.AuthUsername,
		Password:     serverParams.AuthPassword,
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
	for _, id := range []string{"trusty/mysql-42", "utopic/mysql-42", "utopic/mysql-47"} {
		err := s.store.AddCharmWithArchive(
			mustParseReference(id),
			charmtesting.Charms.CharmArchive(c.MkDir(), "mysql"))
		c.Assert(err, gc.IsNil)
	}

	// Delete the second charm using the API.
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("utopic/mysql-42/archive"),
		Method:       "DELETE",
		Username:     serverParams.AuthUsername,
		Password:     serverParams.AuthPassword,
		ExpectStatus: http.StatusOK,
	})

	// The other two charms are still present in the database.
	urls := []*charm.Reference{
		mustParseReference("trusty/mysql-42"),
		mustParseReference("utopic/mysql-47"),
	}
	count, err := s.store.DB.Entities().Find(bson.D{{
		"_id", bson.D{{"$in", urls}},
	}}).Count()
	c.Assert(err, gc.IsNil)
	c.Assert(count, gc.Equals, 2)
}

func (s *ArchiveSuite) TestDeleteNotFound(c *gc.C) {
	// Try to delete a non existing charm using the API.
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("utopic/no-such-0/archive"),
		Method:       "DELETE",
		Username:     serverParams.AuthUsername,
		Password:     serverParams.AuthPassword,
		ExpectStatus: http.StatusNotFound,
		ExpectBody: params.Error{
			Message: "entity not found",
			Code:    params.ErrNotFound,
		},
	})
}

func (s *ArchiveSuite) TestDeleteError(c *gc.C) {
	// Add a charm to the database (not including the archive).
	id := "utopic/mysql-42"
	url := mustParseReference(id)
	err := s.store.AddCharm(url, charmtesting.Charms.CharmArchive(c.MkDir(), "mysql"), "no-such-name", fakeBlobHash, fakeBlobSize)
	c.Assert(err, gc.IsNil)

	// Try to delete the charm using the API.
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL(id + "/archive"),
		Method:       "DELETE",
		Username:     serverParams.AuthUsername,
		Password:     serverParams.AuthPassword,
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
	id := "utopic/mysql-42"
	err := s.store.AddCharmWithArchive(
		mustParseReference(id),
		charmtesting.Charms.CharmArchive(c.MkDir(), "mysql"))
	c.Assert(err, gc.IsNil)

	// Delete the charm using the API.
	rec := storetesting.DoRequest(c, storetesting.DoRequestParams{
		Handler:  s.srv,
		Method:   "DELETE",
		URL:      storeURL(id + "/archive"),
		Username: serverParams.AuthUsername,
		Password: serverParams.AuthPassword,
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)

	// Check that the delete count for the entity has been updated.
	key := []string{params.StatsArchiveDelete, "utopic", "mysql", "", "42"}
	stats.CheckCounterSum(c, s.store, key, false, 1)
}

func (s *ArchiveSuite) TestPostAuthErrors(c *gc.C) {
	checkAuthErrors(c, s.srv, "POST", "utopic/django/archive")
}

func (s *ArchiveSuite) TestDeleteAuthErrors(c *gc.C) {
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
	expectMessage: "authentication failed: invalid or missing HTTP auth header",
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
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
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

func (s *ArchiveSuite) assertEntityInfo(c *gc.C, url *charm.Reference, expect entityInfo) {
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler: s.srv,
		URL: storeURL(
			strings.TrimPrefix(url.String(), "cs:") + "/meta/any" +
				"?include=archive-size" +
				"&include=charm-metadata" +
				"&include=charm-config" +
				"&include=charm-actions" +
				"&include=bundle-metadata",
		),
		ExpectBody: expect,
	})
}

func hashOf(r io.Reader) (hashSum string, size int64) {
	hash := blobstore.NewHash()
	n, err := io.Copy(hash, r)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), n
}
