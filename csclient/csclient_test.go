// Copyright 2015 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package csclient_test

import (
	"bytes"
	"crypto/sha512"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"time"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"github.com/juju/utils"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v5"
	"gopkg.in/macaroon-bakery.v0/bakery/checkers"
	"gopkg.in/macaroon-bakery.v0/bakerytest"
	"gopkg.in/macaroon-bakery.v0/httpbakery"
	"gopkg.in/mgo.v2"

	"gopkg.in/juju/charmstore.v4"
	"gopkg.in/juju/charmstore.v4/csclient"
	"gopkg.in/juju/charmstore.v4/internal/storetesting"
	"gopkg.in/juju/charmstore.v4/params"
)

var charmRepo = storetesting.Charms

// Define fake attributes to be used in tests.
var fakeReader, fakeHash, fakeSize = func() (io.ReadSeeker, string, int64) {
	content := []byte("fake content")
	h := sha512.New384()
	h.Write(content)
	return bytes.NewReader(content), fmt.Sprintf("%x", h.Sum(nil)), int64(len(content))
}()

type suite struct {
	jujutesting.IsolatedMgoSuite
	client       *csclient.Client
	srv          *httptest.Server
	serverParams charmstore.ServerParams
	discharge    func(cond, arg string) ([]checkers.Caveat, error)
}

var _ = gc.Suite(&suite{})

func (s *suite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	s.startServer(c, s.Session)
	s.client = csclient.New(csclient.Params{
		URL:      s.srv.URL,
		User:     s.serverParams.AuthUsername,
		Password: s.serverParams.AuthPassword,
	})
}

func (s *suite) TearDownTest(c *gc.C) {
	s.srv.Close()
	s.IsolatedMgoSuite.TearDownTest(c)
}

func (s *suite) startServer(c *gc.C, session *mgo.Session) {
	s.discharge = func(cond, arg string) ([]checkers.Caveat, error) {
		return nil, fmt.Errorf("no discharge")
	}

	discharger := bakerytest.NewDischarger(nil, func(_ *http.Request, cond, arg string) ([]checkers.Caveat, error) {
		return s.discharge(cond, arg)
	})

	serverParams := charmstore.ServerParams{
		AuthUsername:     "test-user",
		AuthPassword:     "test-password",
		IdentityLocation: discharger.Service.Location(),
		PublicKeyLocator: discharger,
	}

	db := session.DB("charmstore")
	handler, err := charmstore.NewServer(db, nil, "", serverParams, charmstore.V4)
	c.Assert(err, gc.IsNil)
	s.srv = httptest.NewServer(handler)
	s.serverParams = serverParams

}

func (s *suite) TestDefaultServerURL(c *gc.C) {
	// Add a charm used for tests.
	err := s.client.UploadCharmWithRevision(
		charm.MustParseReference("~charmers/vivid/testing-wordpress-42"),
		charmRepo.CharmDir("wordpress"),
		42,
	)
	c.Assert(err, gc.IsNil)

	// Patch the default server URL.
	s.PatchValue(&csclient.ServerURL, s.srv.URL)

	// Instantiate a client using the default server URL.
	client := csclient.New(csclient.Params{
		User:     s.serverParams.AuthUsername,
		Password: s.serverParams.AuthPassword,
	})
	c.Assert(client.ServerURL(), gc.Equals, s.srv.URL)

	// Check that the request succeeds.
	err = client.Get("/vivid/testing-wordpress-42/expand-id", nil)
	c.Assert(err, gc.IsNil)
}

func (s *suite) TestSetHTTPHeader(c *gc.C) {
	var header http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		header = req.Header
	}))
	defer srv.Close()

	sendRequest := func(client *csclient.Client) {
		req, err := http.NewRequest("GET", "", nil)
		c.Assert(err, jc.ErrorIsNil)
		_, err = client.Do(req, "/")
		c.Assert(err, jc.ErrorIsNil)
	}
	client := csclient.New(csclient.Params{
		URL: srv.URL,
	})

	// Make a first request without custom headers.
	sendRequest(client)
	defaultHeaderLen := len(header)

	// Make a second request adding a couple of custom headers.
	h := make(http.Header)
	h.Set("k1", "v1")
	h.Add("k2", "v2")
	h.Add("k2", "v3")
	client.SetHTTPHeader(h)
	sendRequest(client)
	c.Assert(header, gc.HasLen, defaultHeaderLen+len(h))
	c.Assert(header.Get("k1"), gc.Equals, "v1")
	c.Assert(header[http.CanonicalHeaderKey("k2")], jc.DeepEquals, []string{"v2", "v3"})

	// Make a third request without custom headers.
	client.SetHTTPHeader(nil)
	sendRequest(client)
	c.Assert(header, gc.HasLen, defaultHeaderLen)
}

var getTests = []struct {
	about           string
	path            string
	nilResult       bool
	expectResult    interface{}
	expectError     string
	expectErrorCode params.ErrorCode
}{{
	about: "success",
	path:  "/wordpress/expand-id",
	expectResult: []params.ExpandedId{{
		Id: "cs:utopic/wordpress-42",
	}},
}, {
	about:     "success with nil result",
	path:      "/wordpress/expand-id",
	nilResult: true,
}, {
	about:       "non-absolute path",
	path:        "wordpress",
	expectError: `path "wordpress" is not absolute`,
}, {
	about:       "URL parse error",
	path:        "/wordpress/%zz",
	expectError: `parse .*: invalid URL escape "%zz"`,
}, {
	about:           "result with error code",
	path:            "/blahblah",
	expectError:     "not found",
	expectErrorCode: params.ErrNotFound,
}}

func (s *suite) TestGet(c *gc.C) {
	ch := charmRepo.CharmDir("wordpress")
	url := charm.MustParseReference("~charmers/utopic/wordpress-42")
	err := s.client.UploadCharmWithRevision(url, ch, 42)
	c.Assert(err, gc.IsNil)

	for i, test := range getTests {
		c.Logf("test %d: %s", i, test.about)

		// Send the request.
		var result json.RawMessage
		var resultPtr interface{}
		if !test.nilResult {
			resultPtr = &result
		}
		err = s.client.Get(test.path, resultPtr)

		// Check the response.
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError, gc.Commentf("error is %T; %#v", err, err))
			c.Assert(result, gc.IsNil)
			cause := errgo.Cause(err)
			if code, ok := cause.(params.ErrorCode); ok {
				c.Assert(code, gc.Equals, test.expectErrorCode)
			} else {
				c.Assert(test.expectErrorCode, gc.Equals, params.ErrorCode(""))
			}
			continue
		}
		c.Assert(err, gc.IsNil)
		if test.expectResult != nil {
			c.Assert(string(result), jc.JSONEquals, test.expectResult)
		}
	}
}

var putErrorTests = []struct {
	about           string
	path            string
	val             interface{}
	expectError     string
	expectErrorCode params.ErrorCode
}{{
	about:       "bad JSON val",
	path:        "/~charmers/utopic/wordpress-42/meta/extra-info/foo",
	val:         make(chan int),
	expectError: `cannot marshal PUT body: json: unsupported type: chan int`,
}, {
	about:       "non-absolute path",
	path:        "wordpress",
	expectError: `path "wordpress" is not absolute`,
}, {
	about:       "URL parse error",
	path:        "/wordpress/%zz",
	expectError: `parse .*: invalid URL escape "%zz"`,
}, {
	about:           "result with error code",
	path:            "/blahblah",
	expectError:     "not found",
	expectErrorCode: params.ErrNotFound,
}}

func (s *suite) TestPutError(c *gc.C) {
	err := s.client.UploadCharmWithRevision(
		charm.MustParseReference("~charmers/utopic/wordpress-42"),
		charmRepo.CharmDir("wordpress"),
		42)
	c.Assert(err, gc.IsNil)

	for i, test := range putErrorTests {
		c.Logf("test %d: %s", i, test.about)
		err := s.client.Put(test.path, test.val)
		c.Assert(err, gc.ErrorMatches, test.expectError)
		cause := errgo.Cause(err)
		if code, ok := cause.(params.ErrorCode); ok {
			c.Assert(code, gc.Equals, test.expectErrorCode)
		} else {
			c.Assert(test.expectErrorCode, gc.Equals, params.ErrorCode(""))
		}
	}
}

func (s *suite) TestPutSuccess(c *gc.C) {
	err := s.client.UploadCharmWithRevision(
		charm.MustParseReference("~charmers/utopic/wordpress-42"),
		charmRepo.CharmDir("wordpress"),
		42)
	c.Assert(err, gc.IsNil)

	perms := []string{"bob"}
	err = s.client.Put("/~charmers/utopic/wordpress-42/meta/perm/read", perms)
	c.Assert(err, gc.IsNil)
	var got []string
	err = s.client.Get("/~charmers/utopic/wordpress-42/meta/perm/read", &got)
	c.Assert(err, gc.IsNil)
	c.Assert(got, jc.DeepEquals, perms)
}

func (s *suite) TestGetArchive(c *gc.C) {
	key := s.checkGetArchive(c)

	// Check that the downloads count for the entity has been updated.
	s.checkCharmDownloads(c, key, 1)
}

func (s *suite) TestGetArchiveWithStatsDisabled(c *gc.C) {
	s.client.DisableStats()
	key := s.checkGetArchive(c)

	// Check that the downloads count for the entity has not been updated.
	s.checkCharmDownloads(c, key, 0)
}

var checkDownloadsAttempt = utils.AttemptStrategy{
	Total: 1 * time.Second,
	Delay: 100 * time.Millisecond,
}

func (s *suite) checkCharmDownloads(c *gc.C, key string, expect int64) {
	stableCount := 0
	for a := checkDownloadsAttempt.Start(); a.Next(); {
		count := s.statsForKey(c, key)
		if count == expect {
			// Wait for a couple of iterations to make sure that it's stable.
			if stableCount++; stableCount >= 2 {
				return
			}
		} else {
			stableCount = 0
		}
		if !a.HasNext() {
			c.Errorf("unexpected download count for %s, got %d, want %d", key, count, expect)
		}
	}
}

func (s *suite) statsForKey(c *gc.C, key string) int64 {
	var result []params.Statistic
	err := s.client.Get("/stats/counter/"+key, &result)
	c.Assert(err, gc.IsNil)
	c.Assert(result, gc.HasLen, 1)
	return result[0].Count
}

func (s *suite) checkGetArchive(c *gc.C) string {
	ch := charmRepo.CharmArchive(c.MkDir(), "wordpress")

	// Open the archive and calculate its hash and size.
	r, expectHash, expectSize := archiveHashAndSize(c, ch.Path)
	r.Close()

	url := charm.MustParseReference("~charmers/utopic/wordpress-42")
	err := s.client.UploadCharmWithRevision(url, ch, 42)
	c.Assert(err, gc.IsNil)

	rb, id, hash, size, err := s.client.GetArchive(url)
	c.Assert(err, gc.IsNil)
	defer rb.Close()
	c.Assert(id, jc.DeepEquals, url)
	c.Assert(hash, gc.Equals, expectHash)
	c.Assert(size, gc.Equals, expectSize)

	h := sha512.New384()
	size, err = io.Copy(h, rb)
	c.Assert(err, gc.IsNil)
	c.Assert(size, gc.Equals, expectSize)
	c.Assert(fmt.Sprintf("%x", h.Sum(nil)), gc.Equals, expectHash)

	// Return the stats key for the archive download.
	keys := []string{params.StatsArchiveDownload, "utopic", "wordpress", "charmers", "42"}
	return strings.Join(keys, ":")
}

func (s *suite) TestGetArchiveErrorNotFound(c *gc.C) {
	url := charm.MustParseReference("no-such")
	r, id, hash, size, err := s.client.GetArchive(url)
	c.Assert(err, gc.ErrorMatches, `cannot get archive: no matching charm or bundle for "cs:no-such"`)
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrNotFound)
	c.Assert(r, gc.IsNil)
	c.Assert(id, gc.IsNil)
	c.Assert(hash, gc.Equals, "")
	c.Assert(size, gc.Equals, int64(0))
}

var getArchiveWithBadResponseTests = []struct {
	about       string
	response    *http.Response
	error       error
	expectError string
}{{
	about:       "http client Get failure",
	error:       errgo.New("round trip failure"),
	expectError: "cannot get archive: Get .*: round trip failure",
}, {
	about: "no entity id header",
	response: &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Proto:      "HTTP/1.0",
		ProtoMajor: 1,
		ProtoMinor: 0,
		Header: http.Header{
			params.ContentHashHeader: {fakeHash},
		},
		Body:          ioutil.NopCloser(strings.NewReader("")),
		ContentLength: fakeSize,
	},
	expectError: "no " + params.EntityIdHeader + " header found in response",
}, {
	about: "invalid entity id header",
	response: &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Proto:      "HTTP/1.0",
		ProtoMajor: 1,
		ProtoMinor: 0,
		Header: http.Header{
			params.ContentHashHeader: {fakeHash},
			params.EntityIdHeader:    {"no:such"},
		},
		Body:          ioutil.NopCloser(strings.NewReader("")),
		ContentLength: fakeSize,
	},
	expectError: `invalid entity id found in response: charm URL has invalid schema: "no:such"`,
}, {
	about: "partial entity id header",
	response: &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Proto:      "HTTP/1.0",
		ProtoMajor: 1,
		ProtoMinor: 0,
		Header: http.Header{
			params.ContentHashHeader: {fakeHash},
			params.EntityIdHeader:    {"django-42"},
		},
		Body:          ioutil.NopCloser(strings.NewReader("")),
		ContentLength: fakeSize,
	},
	expectError: `archive get returned not fully qualified entity id "cs:django-42"`,
}, {
	about: "no hash header",
	response: &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Proto:      "HTTP/1.0",
		ProtoMajor: 1,
		ProtoMinor: 0,
		Header: http.Header{
			params.EntityIdHeader: {"cs:utopic/django-42"},
		},
		Body:          ioutil.NopCloser(strings.NewReader("")),
		ContentLength: fakeSize,
	},
	expectError: "no " + params.ContentHashHeader + " header found in response",
}, {
	about: "no content length",
	response: &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Proto:      "HTTP/1.0",
		ProtoMajor: 1,
		ProtoMinor: 0,
		Header: http.Header{
			params.ContentHashHeader: {fakeHash},
			params.EntityIdHeader:    {"cs:utopic/django-42"},
		},
		Body:          ioutil.NopCloser(strings.NewReader("")),
		ContentLength: -1,
	},
	expectError: "no content length found in response",
}}

func (s *suite) TestGetArchiveWithBadResponse(c *gc.C) {
	id := charm.MustParseReference("wordpress")
	for i, test := range getArchiveWithBadResponseTests {
		c.Logf("test %d: %s", i, test.about)
		cl := csclient.New(csclient.Params{
			URL: "http://0.1.2.3",
			HTTPClient: &http.Client{
				Transport: &cannedRoundTripper{
					resp:  test.response,
					error: test.error,
				},
			},
		})
		_, _, _, _, err := cl.GetArchive(id)
		c.Assert(err, gc.ErrorMatches, test.expectError)
	}
}

func (s *suite) TestUploadArchiveWithCharm(c *gc.C) {
	path := charmRepo.CharmArchivePath(c.MkDir(), "wordpress")

	// Post the archive.
	s.checkUploadArchive(c, path, "~charmers/utopic/wordpress", "cs:~charmers/utopic/wordpress-0")

	// Posting the same archive a second time does not change its resulting id.
	s.checkUploadArchive(c, path, "~charmers/utopic/wordpress", "cs:~charmers/utopic/wordpress-0")

	// Posting a different archive to the same URL increases the resulting id
	// revision.
	path = charmRepo.CharmArchivePath(c.MkDir(), "mysql")
	s.checkUploadArchive(c, path, "~charmers/utopic/wordpress", "cs:~charmers/utopic/wordpress-1")
}

func (s *suite) prepareBundleCharms(c *gc.C) {
	// Add the charms required by the wordpress-simple bundle to the store.
	err := s.client.UploadCharmWithRevision(
		charm.MustParseReference("~charmers/utopic/wordpress-42"),
		charmRepo.CharmArchive(c.MkDir(), "wordpress"),
		42,
	)
	c.Assert(err, gc.IsNil)
	err = s.client.UploadCharmWithRevision(
		charm.MustParseReference("~charmers/utopic/mysql-47"),
		charmRepo.CharmArchive(c.MkDir(), "mysql"),
		47,
	)
	c.Assert(err, gc.IsNil)
}

func (s *suite) TestUploadArchiveWithBundle(c *gc.C) {
	s.prepareBundleCharms(c)
	path := charmRepo.BundleArchivePath(c.MkDir(), "wordpress-simple")
	// Post the archive.
	s.checkUploadArchive(c, path, "~charmers/bundle/wordpress-simple", "cs:~charmers/bundle/wordpress-simple-0")
}

var uploadArchiveWithBadResponseTests = []struct {
	about       string
	response    *http.Response
	error       error
	expectError string
}{{
	about:       "http client Post failure",
	error:       errgo.New("round trip failure"),
	expectError: "cannot post archive: Post .*: round trip failure",
}, {
	about: "invalid JSON in body",
	response: &http.Response{
		Status:        "200 OK",
		StatusCode:    200,
		Proto:         "HTTP/1.0",
		ProtoMajor:    1,
		ProtoMinor:    0,
		Body:          ioutil.NopCloser(strings.NewReader("no id here")),
		ContentLength: 0,
	},
	expectError: `cannot unmarshal response "no id here": .*`,
}}

func (s *suite) TestUploadArchiveWithBadResponse(c *gc.C) {
	id := charm.MustParseReference("trusty/wordpress")
	for i, test := range uploadArchiveWithBadResponseTests {
		c.Logf("test %d: %s", i, test.about)
		cl := csclient.New(csclient.Params{
			URL:  "http://0.1.2.3",
			User: "bob",
			HTTPClient: &http.Client{
				Transport: &cannedRoundTripper{
					resp:  test.response,
					error: test.error,
				},
			},
		})
		id, err := csclient.UploadArchive(cl, id, fakeReader, fakeHash, fakeSize, -1)
		c.Assert(id, gc.IsNil)
		c.Assert(err, gc.ErrorMatches, test.expectError)
	}
}

func (s *suite) TestUploadArchiveWithNoSeries(c *gc.C) {
	id, err := csclient.UploadArchive(
		s.client,
		charm.MustParseReference("wordpress"),
		fakeReader, fakeHash, fakeSize, -1)
	c.Assert(id, gc.IsNil)
	c.Assert(err, gc.ErrorMatches, `no series specified in "cs:wordpress"`)
}

func (s *suite) TestUploadArchiveWithServerError(c *gc.C) {
	path := charmRepo.CharmArchivePath(c.MkDir(), "wordpress")
	body, hash, size := archiveHashAndSize(c, path)
	defer body.Close()

	// Send an invalid hash so that the server returns an error.
	url := charm.MustParseReference("~charmers/trusty/wordpress")
	id, err := csclient.UploadArchive(s.client, url, body, hash+"mismatch", size, -1)
	c.Assert(id, gc.IsNil)
	c.Assert(err, gc.ErrorMatches, "cannot post archive: cannot put archive blob: hash mismatch")
}

func (s *suite) checkUploadArchive(c *gc.C, path, url, expectId string) {
	// Open the archive and calculate its hash and size.
	body, hash, size := archiveHashAndSize(c, path)
	defer body.Close()

	// Post the archive.
	id, err := csclient.UploadArchive(s.client, charm.MustParseReference(url), body, hash, size, -1)
	c.Assert(err, gc.IsNil)
	c.Assert(id.String(), gc.Equals, expectId)

	// Ensure the entity has been properly added to the db.
	r, resultingId, resultingHash, resultingSize, err := s.client.GetArchive(id)
	c.Assert(err, gc.IsNil)
	defer r.Close()
	c.Assert(resultingId, gc.DeepEquals, id)
	c.Assert(resultingHash, gc.Equals, hash)
	c.Assert(resultingSize, gc.Equals, size)
}

func archiveHashAndSize(c *gc.C, path string) (r csclient.ReadSeekCloser, hash string, size int64) {
	f, err := os.Open(path)
	c.Assert(err, gc.IsNil)
	h := sha512.New384()
	size, err = io.Copy(h, f)
	c.Assert(err, gc.IsNil)
	_, err = f.Seek(0, 0)
	c.Assert(err, gc.IsNil)
	return f, fmt.Sprintf("%x", h.Sum(nil)), size
}

func (s *suite) TestUploadCharmDir(c *gc.C) {
	ch := charmRepo.CharmDir("wordpress")
	id, err := s.client.UploadCharm(charm.MustParseReference("~charmers/utopic/wordpress"), ch)
	c.Assert(err, gc.IsNil)
	c.Assert(id.String(), gc.Equals, "cs:~charmers/utopic/wordpress-0")
	s.checkUploadCharm(c, id, ch)
}

func (s *suite) TestUploadCharmArchive(c *gc.C) {
	ch := charmRepo.CharmArchive(c.MkDir(), "wordpress")
	id, err := s.client.UploadCharm(charm.MustParseReference("~charmers/trusty/wordpress"), ch)
	c.Assert(err, gc.IsNil)
	c.Assert(id.String(), gc.Equals, "cs:~charmers/trusty/wordpress-0")
	s.checkUploadCharm(c, id, ch)
}

func (s *suite) TestUploadCharmArchiveWithRevision(c *gc.C) {
	id := charm.MustParseReference("~charmers/trusty/wordpress-42")
	err := s.client.UploadCharmWithRevision(
		id,
		charmRepo.CharmDir("wordpress"),
		10,
	)
	c.Assert(err, gc.IsNil)
	ch := charmRepo.CharmArchive(c.MkDir(), "wordpress")
	s.checkUploadCharm(c, id, ch)
	id.User = ""
	id.Revision = 10
	s.checkUploadCharm(c, id, ch)
}

func (s *suite) TestUploadCharmArchiveWithUnwantedRevision(c *gc.C) {
	ch := charmRepo.CharmDir("wordpress")
	_, err := s.client.UploadCharm(charm.MustParseReference("~charmers/bundle/wp-20"), ch)
	c.Assert(err, gc.ErrorMatches, `revision specified in "cs:~charmers/bundle/wp-20", but should not be specified`)
}

func (s *suite) TestUploadCharmErrorUnknownType(c *gc.C) {
	ch := charmRepo.CharmDir("wordpress")
	unknown := struct {
		charm.Charm
	}{ch}
	id, err := s.client.UploadCharm(charm.MustParseReference("~charmers/trusty/wordpress"), unknown)
	c.Assert(err, gc.ErrorMatches, `cannot open charm archive: cannot get the archive for entity type .*`)
	c.Assert(id, gc.IsNil)
}

func (s *suite) TestUploadCharmErrorOpenArchive(c *gc.C) {
	// Since the internal code path is shared between charms and bundles, just
	// using a charm for this test also exercises the same failure for bundles.
	ch := charmRepo.CharmArchive(c.MkDir(), "wordpress")
	ch.Path = "no-such-file"
	id, err := s.client.UploadCharm(charm.MustParseReference("trusty/wordpress"), ch)
	c.Assert(err, gc.ErrorMatches, `cannot open charm archive: open no-such-file: no such file or directory`)
	c.Assert(id, gc.IsNil)
}

func (s *suite) TestUploadCharmErrorArchiveTo(c *gc.C) {
	// Since the internal code path is shared between charms and bundles, just
	// using a charm for this test also exercises the same failure for bundles.
	id, err := s.client.UploadCharm(charm.MustParseReference("trusty/wordpress"), failingArchiverTo{})
	c.Assert(err, gc.ErrorMatches, `cannot open charm archive: cannot create entity archive: bad wolf`)
	c.Assert(id, gc.IsNil)
}

type failingArchiverTo struct {
	charm.Charm
}

func (failingArchiverTo) ArchiveTo(io.Writer) error {
	return errgo.New("bad wolf")
}

func (s *suite) checkUploadCharm(c *gc.C, id *charm.Reference, ch charm.Charm) {
	r, _, _, _, err := s.client.GetArchive(id)
	c.Assert(err, gc.IsNil)
	data, err := ioutil.ReadAll(r)
	c.Assert(err, gc.IsNil)
	result, err := charm.ReadCharmArchiveBytes(data)
	c.Assert(err, gc.IsNil)
	// Comparing the charm metadata is sufficient for ensuring the result is
	// the same charm previously uploaded.
	c.Assert(result.Meta(), jc.DeepEquals, ch.Meta())
}

func (s *suite) TestUploadBundleDir(c *gc.C) {
	s.prepareBundleCharms(c)
	b := charmRepo.BundleDir("wordpress-simple")
	id, err := s.client.UploadBundle(charm.MustParseReference("~charmers/bundle/wordpress-simple"), b)
	c.Assert(err, gc.IsNil)
	c.Assert(id.String(), gc.Equals, "cs:~charmers/bundle/wordpress-simple-0")
	s.checkUploadBundle(c, id, b)
}

func (s *suite) TestUploadBundleArchive(c *gc.C) {
	s.prepareBundleCharms(c)
	path := charmRepo.BundleArchivePath(c.MkDir(), "wordpress-simple")
	b, err := charm.ReadBundleArchive(path)
	c.Assert(err, gc.IsNil)
	id, err := s.client.UploadBundle(charm.MustParseReference("~charmers/bundle/wp"), b)
	c.Assert(err, gc.IsNil)
	c.Assert(id.String(), gc.Equals, "cs:~charmers/bundle/wp-0")
	s.checkUploadBundle(c, id, b)
}

func (s *suite) TestUploadBundleArchiveWithUnwantedRevision(c *gc.C) {
	s.prepareBundleCharms(c)
	path := charmRepo.BundleArchivePath(c.MkDir(), "wordpress-simple")
	b, err := charm.ReadBundleArchive(path)
	c.Assert(err, gc.IsNil)
	_, err = s.client.UploadBundle(charm.MustParseReference("~charmers/bundle/wp-20"), b)
	c.Assert(err, gc.ErrorMatches, `revision specified in "cs:~charmers/bundle/wp-20", but should not be specified`)
}

func (s *suite) TestUploadBundleArchiveWithRevision(c *gc.C) {
	s.prepareBundleCharms(c)
	path := charmRepo.BundleArchivePath(c.MkDir(), "wordpress-simple")
	b, err := charm.ReadBundleArchive(path)
	c.Assert(err, gc.IsNil)
	id := charm.MustParseReference("~charmers/bundle/wp-22")
	err = s.client.UploadBundleWithRevision(id, b, 34)
	c.Assert(err, gc.IsNil)
	s.checkUploadBundle(c, id, b)
	id.User = ""
	id.Revision = 34
	s.checkUploadBundle(c, id, b)
}

func (s *suite) TestUploadBundleErrorUploading(c *gc.C) {
	// Uploading without specifying the series should return an error.
	// Note that the possible upload errors are already extensively exercised
	// as part of the client.uploadArchive tests.
	id, err := s.client.UploadBundle(
		charm.MustParseReference("~charmers/wordpress-simple"),
		charmRepo.BundleDir("wordpress-simple"),
	)
	c.Assert(err, gc.ErrorMatches, `no series specified in "cs:~charmers/wordpress-simple"`)
	c.Assert(id, gc.IsNil)
}

func (s *suite) TestUploadBundleErrorUnknownType(c *gc.C) {
	b := charmRepo.BundleDir("wordpress-simple")
	unknown := struct {
		charm.Bundle
	}{b}
	id, err := s.client.UploadBundle(charm.MustParseReference("bundle/wordpress"), unknown)
	c.Assert(err, gc.ErrorMatches, `cannot open bundle archive: cannot get the archive for entity type .*`)
	c.Assert(id, gc.IsNil)
}

func (s *suite) checkUploadBundle(c *gc.C, id *charm.Reference, b charm.Bundle) {
	r, _, _, _, err := s.client.GetArchive(id)
	c.Assert(err, gc.IsNil)
	data, err := ioutil.ReadAll(r)
	c.Assert(err, gc.IsNil)
	result, err := charm.ReadBundleArchiveBytes(data)
	c.Assert(err, gc.IsNil)
	// Comparing the bundle data is sufficient for ensuring the result is
	// the same bundle previously uploaded.
	c.Assert(result.Data(), jc.DeepEquals, b.Data())
}

func (s *suite) TestDoAuthorization(c *gc.C) {
	// Add a charm to be deleted.
	err := s.client.UploadCharmWithRevision(
		charm.MustParseReference("~charmers/utopic/wordpress-42"),
		charmRepo.CharmArchive(c.MkDir(), "wordpress"),
		42,
	)
	c.Assert(err, gc.IsNil)

	// Check that when we use incorrect authorization,
	// we get an error trying to delete the charm
	client := csclient.New(csclient.Params{
		URL:      s.srv.URL,
		User:     s.serverParams.AuthUsername,
		Password: "bad password",
	})
	req, err := http.NewRequest("DELETE", "", nil)
	c.Assert(err, gc.IsNil)
	_, err = client.Do(req, "/~charmers/utopic/wordpress-42/archive")
	c.Assert(err, gc.ErrorMatches, "invalid user name or password")
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrUnauthorized)

	// Check that it's still there.
	err = s.client.Get("/~charmers/utopic/wordpress-42/expand-id", nil)
	c.Assert(err, gc.IsNil)

	// Then check that when we use the correct authorization,
	// the delete succeeds.
	client = csclient.New(csclient.Params{
		URL:      s.srv.URL,
		User:     s.serverParams.AuthUsername,
		Password: s.serverParams.AuthPassword,
	})
	req, err = http.NewRequest("DELETE", "", nil)
	c.Assert(err, gc.IsNil)
	resp, err := client.Do(req, "/~charmers/utopic/wordpress-42/archive")
	c.Assert(err, gc.IsNil)
	resp.Body.Close()

	// Check that it's now really gone.
	err = s.client.Get("/utopic/wordpress-42/expand-id", nil)
	c.Assert(err, gc.ErrorMatches, `no matching charm or bundle for "cs:utopic/wordpress-42"`)
}

var getWithBadResponseTests = []struct {
	about       string
	error       error
	response    *http.Response
	responseErr error
	expectError string
}{{
	about:       "http client Get failure",
	error:       errgo.New("round trip failure"),
	expectError: "Get .*: round trip failure",
}, {
	about: "body read error",
	response: &http.Response{
		Status:        "200 OK",
		StatusCode:    200,
		Proto:         "HTTP/1.0",
		ProtoMajor:    1,
		ProtoMinor:    0,
		Body:          ioutil.NopCloser(&errorReader{"body read error"}),
		ContentLength: -1,
	},
	expectError: "cannot read response body: body read error",
}, {
	about: "badly formatted json response",
	response: &http.Response{
		Status:        "200 OK",
		StatusCode:    200,
		Proto:         "HTTP/1.0",
		ProtoMajor:    1,
		ProtoMinor:    0,
		Body:          ioutil.NopCloser(strings.NewReader("bad")),
		ContentLength: -1,
	},
	expectError: `cannot unmarshal response "bad": .*`,
}, {
	about: "badly formatted json error",
	response: &http.Response{
		Status:        "404 Not found",
		StatusCode:    404,
		Proto:         "HTTP/1.0",
		ProtoMajor:    1,
		ProtoMinor:    0,
		Body:          ioutil.NopCloser(strings.NewReader("bad")),
		ContentLength: -1,
	},
	expectError: `cannot unmarshal error response "bad": .*`,
}, {
	about: "error response with empty message",
	response: &http.Response{
		Status:     "404 Not found",
		StatusCode: 404,
		Proto:      "HTTP/1.0",
		ProtoMajor: 1,
		ProtoMinor: 0,
		Body: ioutil.NopCloser(bytes.NewReader(mustMarshalJSON(&params.Error{
			Code: "foo",
		}))),
		ContentLength: -1,
	},
	expectError: "error response with empty message .*",
}}

func (s *suite) TestGetWithBadResponse(c *gc.C) {
	for i, test := range getWithBadResponseTests {
		c.Logf("test %d: %s", i, test.about)
		cl := csclient.New(csclient.Params{
			URL: "http://0.1.2.3",
			HTTPClient: &http.Client{
				Transport: &cannedRoundTripper{
					resp:  test.response,
					error: test.error,
				},
			},
		})
		var result interface{}
		err := cl.Get("/foo", &result)
		c.Assert(err, gc.ErrorMatches, test.expectError)
	}
}

var hyphenateTests = []struct {
	val    string
	expect string
}{{
	val:    "Hello",
	expect: "hello",
}, {
	val:    "HelloThere",
	expect: "hello-there",
}, {
	val:    "HelloHTTP",
	expect: "hello-http",
}, {
	val:    "helloHTTP",
	expect: "hello-http",
}, {
	val:    "hellothere",
	expect: "hellothere",
}, {
	val:    "Long4Camel32WithDigits45",
	expect: "long4-camel32-with-digits45",
}, {
	// The result here is equally dubious, but Go identifiers
	// should not contain underscores.
	val:    "With_Dubious_Underscore",
	expect: "with_-dubious_-underscore",
}}

func (s *suite) TestHyphenate(c *gc.C) {
	for i, test := range hyphenateTests {
		c.Logf("test %d. %q", i, test.val)
		c.Assert(csclient.Hyphenate(test.val), gc.Equals, test.expect)
	}
}

func (s *suite) TestDo(c *gc.C) {
	// Do is tested fairly comprehensively (but indirectly)
	// in TestGet, so just a trivial smoke test here.
	url := charm.MustParseReference("~charmers/utopic/wordpress-42")
	err := s.client.UploadCharmWithRevision(
		url,
		charmRepo.CharmArchive(c.MkDir(), "wordpress"),
		42,
	)
	c.Assert(err, gc.IsNil)
	err = s.client.PutExtraInfo(url, map[string]interface{}{
		"foo": "bar",
	})
	c.Assert(err, gc.IsNil)

	req, _ := http.NewRequest("GET", "", nil)
	resp, err := s.client.Do(req, "/wordpress/meta/extra-info/foo")
	c.Assert(err, gc.IsNil)
	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	c.Assert(err, gc.IsNil)
	c.Assert(string(data), gc.Equals, `"bar"`)
}

var metaBadTypeTests = []struct {
	result      interface{}
	expectError string
}{{
	result:      "",
	expectError: "expected pointer, not string",
}, {
	result:      new(string),
	expectError: `expected pointer to struct, not \*string`,
}, {
	result:      new(struct{ Embed }),
	expectError: "anonymous fields not supported",
}, {
	expectError: "expected valid result pointer, not nil",
}}

func (s *suite) TestMetaBadType(c *gc.C) {
	id := charm.MustParseReference("wordpress")
	for _, test := range metaBadTypeTests {
		_, err := s.client.Meta(id, test.result)
		c.Assert(err, gc.ErrorMatches, test.expectError)
	}
}

type Embed struct{}
type embed struct{}

func (s *suite) TestMeta(c *gc.C) {
	ch := charmRepo.CharmDir("wordpress")
	url := charm.MustParseReference("~charmers/utopic/wordpress-42")
	purl := charm.MustParseReference("utopic/wordpress-42")
	err := s.client.UploadCharmWithRevision(url, ch, 42)
	c.Assert(err, gc.IsNil)

	// Put some extra-info.
	err = s.client.PutExtraInfo(url, map[string]interface{}{
		"attr": "value",
	})
	c.Assert(err, gc.IsNil)

	tests := []struct {
		about           string
		id              string
		expectResult    interface{}
		expectError     string
		expectErrorCode params.ErrorCode
	}{{
		about:        "no fields",
		id:           "utopic/wordpress",
		expectResult: &struct{}{},
	}, {
		about: "single field",
		id:    "utopic/wordpress",
		expectResult: &struct {
			CharmMetadata *charm.Meta
		}{
			CharmMetadata: ch.Meta(),
		},
	}, {
		about: "three fields",
		id:    "wordpress",
		expectResult: &struct {
			CharmMetadata *charm.Meta
			CharmConfig   *charm.Config
			ExtraInfo     map[string]string
		}{
			CharmMetadata: ch.Meta(),
			CharmConfig:   ch.Config(),
			ExtraInfo:     map[string]string{"attr": "value"},
		},
	}, {
		about: "tagged field",
		id:    "wordpress",
		expectResult: &struct {
			Foo  *charm.Meta `csclient:"charm-metadata"`
			Attr string      `csclient:"extra-info/attr"`
		}{
			Foo:  ch.Meta(),
			Attr: "value",
		},
	}, {
		about:           "id not found",
		id:              "bogus",
		expectResult:    &struct{}{},
		expectError:     `cannot get "/bogus/meta/any": no matching charm or bundle for "cs:bogus"`,
		expectErrorCode: params.ErrNotFound,
	}, {
		about: "unmarshal into invalid type",
		id:    "wordpress",
		expectResult: new(struct {
			CharmMetadata []string
		}),
		expectError: `cannot unmarshal charm-metadata: json: cannot unmarshal object into Go value of type \[]string`,
	}, {
		about: "unmarshal into struct with unexported fields",
		id:    "wordpress",
		expectResult: &struct {
			unexported    int
			CharmMetadata *charm.Meta
			// Embedded anonymous fields don't get tagged as unexported
			// due to https://code.google.com/p/go/issues/detail?id=7247
			// TODO fix in go 1.5.
			// embed
		}{
			CharmMetadata: ch.Meta(),
		},
	}, {
		about: "metadata not appropriate for charm",
		id:    "wordpress",
		expectResult: &struct {
			CharmMetadata  *charm.Meta
			BundleMetadata *charm.BundleData
		}{
			CharmMetadata: ch.Meta(),
		},
	}}
	for i, test := range tests {
		c.Logf("test %d: %s", i, test.about)
		// Make a result value of the same type as the expected result,
		// but empty.
		result := reflect.New(reflect.TypeOf(test.expectResult).Elem()).Interface()
		id, err := s.client.Meta(charm.MustParseReference(test.id), result)
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			if code, ok := errgo.Cause(err).(params.ErrorCode); ok {
				c.Assert(code, gc.Equals, test.expectErrorCode)
			} else {
				c.Assert(test.expectErrorCode, gc.Equals, params.ErrorCode(""))
			}
			c.Assert(id, gc.IsNil)
			continue
		}
		c.Assert(err, gc.IsNil)
		c.Assert(id, jc.DeepEquals, purl)
		c.Assert(result, jc.DeepEquals, test.expectResult)
	}
}

func (s *suite) TestPutExtraInfo(c *gc.C) {
	ch := charmRepo.CharmDir("wordpress")
	url := charm.MustParseReference("~charmers/utopic/wordpress-42")
	err := s.client.UploadCharmWithRevision(url, ch, 42)
	c.Assert(err, gc.IsNil)

	// Put some info in.
	info := map[string]interface{}{
		"attr1": "value1",
		"attr2": []interface{}{"one", "two"},
	}
	err = s.client.PutExtraInfo(url, info)
	c.Assert(err, gc.IsNil)

	// Verify that we get it back OK.
	var val struct {
		ExtraInfo map[string]interface{}
	}
	_, err = s.client.Meta(url, &val)
	c.Assert(err, gc.IsNil)
	c.Assert(val.ExtraInfo, jc.DeepEquals, info)

	// Put some more in.
	err = s.client.PutExtraInfo(url, map[string]interface{}{
		"attr3": "three",
	})
	c.Assert(err, gc.IsNil)

	// Verify that we get all the previous results and the new value.
	info["attr3"] = "three"
	_, err = s.client.Meta(url, &val)
	c.Assert(err, gc.IsNil)
	c.Assert(val.ExtraInfo, jc.DeepEquals, info)
}

func (s *suite) TestPutExtraInfoWithError(c *gc.C) {
	err := s.client.PutExtraInfo(charm.MustParseReference("wordpress"), map[string]interface{}{"attr": "val"})
	c.Assert(err, gc.ErrorMatches, `no matching charm or bundle for "cs:wordpress"`)
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrNotFound)
}

type errorReader struct {
	error string
}

func (e *errorReader) Read(buf []byte) (int, error) {
	return 0, errgo.New(e.error)
}

type cannedRoundTripper struct {
	resp  *http.Response
	error error
}

func (r *cannedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return r.resp, r.error
}

func mustMarshalJSON(x interface{}) []byte {
	data, err := json.Marshal(x)
	if err != nil {
		panic(err)
	}
	return data
}

func (s *suite) TestLog(c *gc.C) {
	logs := []struct {
		typ     params.LogType
		level   params.LogLevel
		message string
		urls    []*charm.Reference
	}{{
		typ:     params.IngestionType,
		level:   params.InfoLevel,
		message: "ingestion info",
		urls:    nil,
	}, {
		typ:     params.LegacyStatisticsType,
		level:   params.ErrorLevel,
		message: "statistics error",
		urls: []*charm.Reference{
			charm.MustParseReference("cs:mysql"),
			charm.MustParseReference("cs:wordpress"),
		},
	}}

	for _, log := range logs {
		err := s.client.Log(log.typ, log.level, log.message, log.urls...)
		c.Assert(err, gc.IsNil)
	}
	var result []*params.LogResponse
	err := s.client.Get("/log", &result)
	c.Assert(err, gc.IsNil)
	c.Assert(result, gc.HasLen, len(logs))
	for i, l := range result {
		c.Assert(l.Type, gc.Equals, logs[len(logs)-(1+i)].typ)
		c.Assert(l.Level, gc.Equals, logs[len(logs)-(1+i)].level)
		var msg string
		err := json.Unmarshal([]byte(l.Data), &msg)
		c.Assert(err, gc.IsNil)
		c.Assert(msg, gc.Equals, logs[len(logs)-(1+i)].message)
		c.Assert(l.URLs, jc.DeepEquals, logs[len(logs)-(1+i)].urls)
	}
}

func (s *suite) TestMacaroonAuthorization(c *gc.C) {
	ch := charmRepo.CharmDir("wordpress")
	curl := charm.MustParseReference("~charmers/utopic/wordpress-42")
	purl := charm.MustParseReference("utopic/wordpress-42")
	err := s.client.UploadCharmWithRevision(curl, ch, 42)
	c.Assert(err, gc.IsNil)

	err = s.client.Put("/"+curl.Path()+"/meta/perm/read", []string{"bob"})
	c.Assert(err, gc.IsNil)

	// Create a client without basic auth credentials
	client := csclient.New(csclient.Params{
		URL: s.srv.URL,
	})

	var result struct{ IdRevision struct{ Revision int } }
	// TODO 2015-01-23: once supported, rewrite the test using POST requests.
	_, err = client.Meta(purl, &result)
	c.Assert(err, gc.ErrorMatches, `cannot get "/utopic/wordpress-42/meta/any\?include=id-revision": cannot get discharge from ".*": third party refused discharge: cannot discharge: no discharge`)
	c.Assert(httpbakery.IsDischargeError(errgo.Cause(err)), gc.Equals, true)

	s.discharge = func(cond, arg string) ([]checkers.Caveat, error) {
		return []checkers.Caveat{checkers.DeclaredCaveat("username", "bob")}, nil
	}
	_, err = client.Meta(curl, &result)
	c.Assert(err, gc.IsNil)
	c.Assert(result.IdRevision.Revision, gc.Equals, curl.Revision)

	visitURL := "http://0.1.2.3/visitURL"
	s.discharge = func(cond, arg string) ([]checkers.Caveat, error) {
		return nil, &httpbakery.Error{
			Code:    httpbakery.ErrInteractionRequired,
			Message: "interaction required",
			Info: &httpbakery.ErrorInfo{
				VisitURL: visitURL,
				WaitURL:  "http://0.1.2.3/waitURL",
			}}
	}

	client = csclient.New(csclient.Params{
		URL: s.srv.URL,
		VisitWebPage: func(vurl *url.URL) error {
			c.Check(vurl.String(), gc.Equals, visitURL)
			return fmt.Errorf("stopping interaction")
		}})

	_, err = client.Meta(purl, &result)
	c.Assert(err, gc.ErrorMatches, `cannot get "/utopic/wordpress-42/meta/any\?include=id-revision": cannot get discharge from ".*": cannot start interactive session: stopping interaction`)
	c.Assert(result.IdRevision.Revision, gc.Equals, curl.Revision)
	c.Assert(httpbakery.IsInteractionError(errgo.Cause(err)), gc.Equals, true)
}

func (s *suite) TestLogin(c *gc.C) {
	ch := charmRepo.CharmDir("wordpress")
	url := charm.MustParseReference("~charmers/utopic/wordpress-42")
	purl := charm.MustParseReference("utopic/wordpress-42")
	err := s.client.UploadCharmWithRevision(url, ch, 42)
	c.Assert(err, gc.IsNil)

	err = s.client.Put("/"+url.Path()+"/meta/perm/read", []string{"bob"})
	c.Assert(err, gc.IsNil)
	client := csclient.New(csclient.Params{
		URL: s.srv.URL,
	})

	var result struct{ IdRevision struct{ Revision int } }
	_, err = client.Meta(purl, &result)
	c.Assert(err, gc.NotNil)

	// Try logging in when the discharger fails.
	err = client.Login()
	c.Assert(err, gc.ErrorMatches, `cannot discharge login macaroon: cannot get discharge from ".*": third party refused discharge: cannot discharge: no discharge`)

	// Allow the discharge.
	s.discharge = func(cond, arg string) ([]checkers.Caveat, error) {
		return []checkers.Caveat{checkers.DeclaredCaveat("username", "bob")}, nil
	}
	err = client.Login()
	c.Assert(err, gc.IsNil)

	// Change discharge so that we're sure the cookies are being
	// used rather than the discharge mechanism.
	s.discharge = func(cond, arg string) ([]checkers.Caveat, error) {
		return nil, fmt.Errorf("no discharge")
	}

	// Check that the request still works.
	_, err = client.Meta(purl, &result)
	c.Assert(err, gc.IsNil)
	c.Assert(result.IdRevision.Revision, gc.Equals, url.Revision)
}
