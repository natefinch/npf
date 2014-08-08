// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"

	jc "github.com/juju/testing/checkers"
	"gopkg.in/juju/charm.v3"
	"gopkg.in/juju/charm.v3/testing"
	charmtesting "gopkg.in/juju/charm.v3/testing"
	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore/internal/blobstore"
	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/storetesting"
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
	s.srv, s.store = newServer(c, s.Session)
}

func (s *ArchiveSuite) TestArchiveGet(c *gc.C) {
	wordpress := s.assertUploadCharm(c, mustParseReference("cs:precise/wordpress-0"), "wordpress")

	archiveBytes, err := ioutil.ReadFile(wordpress.Path)
	c.Assert(err, gc.IsNil)

	rec := storetesting.DoRequest(c, s.srv, "GET", storeURL("precise/wordpress-0/archive"), nil, 0, nil)
	c.Assert(err, gc.IsNil)
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	c.Assert(rec.Body.Bytes(), gc.DeepEquals, archiveBytes)

	// Check that the HTTP range logic is plugged in OK. If this
	// is working, we assume that the whole thing is working OK,
	// as net/http is well-tested.
	rec = storetesting.DoRequest(c, s.srv, "GET", storeURL("precise/wordpress-0/archive"), nil, 0, http.Header{"Range": {"bytes=10-100"}})
	c.Assert(err, gc.IsNil)
	c.Assert(rec.Code, gc.Equals, http.StatusPartialContent, gc.Commentf("body: %q", rec.Body.Bytes()))
	c.Assert(rec.Body.Bytes(), gc.HasLen, 100-10+1)
	c.Assert(rec.Body.Bytes(), gc.DeepEquals, archiveBytes[10:101])
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

func (s *ArchiveSuite) TestArchivePostErrors(c *gc.C) {
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
			Body:       body,
			ExpectCode: test.expectStatus,
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
		resp, err := http.Post(url, "application/zip", body)
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
			close(try)
			try = nil
			foundError = true
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

func (s *ArchiveSuite) TestArchivePostCharm(c *gc.C) {
	// A charm that did not exist before should get revision 0.
	s.assertUploadCharm(c, mustParseReference("precise/wordpress-0"), "wordpress")

	// Subsequent charm uploads should increment the
	// revision by 1.
	s.assertUploadCharm(c, mustParseReference("precise/wordpress-1"), "wordpress")
}

func (s *ArchiveSuite) TestArchivePostBundle(c *gc.C) {
	// A bundle that did not exist before should get revision 0.
	s.assertUploadBundle(c, mustParseReference("bundle/wordpress-0"), "wordpress")

	// Subsequent bundle uploads should increment the
	// revision by 1.
	s.assertUploadBundle(c, mustParseReference("bundle/wordpress-1"), "wordpress")
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
	expectErr := `bundle verification failed: relation ["foo:db" "mysql:server"] refers to service "foo" not defined in this bundle`
	s.assertCannotUpload(c, "bundle/wordpress", f, expectErr)
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
		Body:       content,
		ExpectCode: http.StatusInternalServerError,

		ExpectBody: params.Error{
			Message: errorMessage,
		},
	})

	// Check that the uploaded blob has been deleted.
	_, _, err = s.store.BlobStore.Open(hash)
	c.Assert(err, gc.ErrorMatches, "resource.*not found")
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
		Body: f,
		ExpectBody: params.ArchivePostResponse{
			Id: url,
		},
	})

	// Test that the expected entry has been created
	// in the blob store.
	r, _, err := s.store.BlobStore.Open(hash)
	c.Assert(err, gc.IsNil)
	r.Close()
	return size
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
