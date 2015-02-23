// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"

	jc "github.com/juju/testing/checkers"
	"github.com/juju/testing/httptesting"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v4"
	"gopkg.in/macaroon-bakery.v0/bakery/checkers"
	"gopkg.in/macaroon-bakery.v0/bakerytest"
	"gopkg.in/macaroon-bakery.v0/httpbakery"
	"gopkg.in/macaroon.v1"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/storetesting"
	"github.com/juju/charmstore/internal/v4"
	"github.com/juju/charmstore/params"
)

func AssertEndpointAuth(c *gc.C, session *mgo.Session, p httptesting.JSONCallParams) {
	testNonMacaroonAuth(c, session, p)
	testMacaroonAuth(c, session, p)
}

func testNonMacaroonAuth(c *gc.C, session *mgo.Session, p httptesting.JSONCallParams) {
	srv, _ := newServer(c, session, nil, charmstore.ServerParams{
		AuthUsername: "test-user",
		AuthPassword: "test-password",
	})
	p.Handler = srv
	// Check that the request succeeds when provided with the
	// correct credentials.
	p.Username = "test-user"
	p.Password = "test-password"
	httptesting.AssertJSONCall(c, p)

	// Check that auth fails with no creds provided.
	p.Username = ""
	p.Password = ""
	p.ExpectStatus = http.StatusUnauthorized
	p.ExpectBody = params.Error{
		Message: "authentication failed: missing HTTP auth header",
		Code:    params.ErrUnauthorized,
	}
	httptesting.AssertJSONCall(c, p)

	// Check that auth fails with the wrong username provided.
	p.Username = "wrong"
	p.Password = "test-password"
	p.ExpectStatus = http.StatusUnauthorized
	p.ExpectBody = params.Error{
		Message: "invalid user name or password",
		Code:    params.ErrUnauthorized,
	}
	httptesting.AssertJSONCall(c, p)

	// Check that auth fails with the wrong password provided.
	p.Username = "test-user"
	p.Password = "test-password-wrong"
	p.ExpectStatus = http.StatusUnauthorized
	p.ExpectBody = params.Error{
		Message: "invalid user name or password",
		Code:    params.ErrUnauthorized,
	}
	httptesting.AssertJSONCall(c, p)
}

func testMacaroonAuth(c *gc.C, session *mgo.Session, p httptesting.JSONCallParams) {
	// Make a test third party caveat discharger.
	var checkedCaveats []string
	var mu sync.Mutex
	var dischargeError error
	discharger := bakerytest.NewDischarger(nil, func(cond string, arg string) ([]checkers.Caveat, error) {
		mu.Lock()
		defer mu.Unlock()
		checkedCaveats = append(checkedCaveats, cond+" "+arg)
		if dischargeError != nil {
			return nil, dischargeError
		}
		return []checkers.Caveat{
			checkers.DeclaredCaveat("username", "bob"),
		}, nil
	})
	defer discharger.Close()

	// Create a charmstore server that will use the test third party for
	// its third party caveat.
	srv, _ := newServer(c, session, nil, charmstore.ServerParams{
		AuthUsername:     "test-user",
		AuthPassword:     "test-password",
		IdentityLocation: discharger.Location(),
		PublicKeyLocator: discharger,
	})
	p.Handler = srv

	client := httpbakery.NewHTTPClient()
	cookieJar := &cookieJar{CookieJar: client.Jar}
	client.Jar = cookieJar
	p.Do = func(req *http.Request) (*http.Response, error) {
		return httpbakery.Do(client, req, noInteraction)
	}

	// Check that the call succeeds with simple auth.
	c.Log("simple auth sucess")
	p.Username = "test-user"
	p.Password = "test-password"
	httptesting.AssertJSONCall(c, p)
	c.Assert(checkedCaveats, gc.HasLen, 0)
	c.Assert(cookieJar.cookieURLs, gc.HasLen, 0)

	// Check that the call gives us the correct
	// "authentication denied response" without simple auth
	// and uses the third party checker
	// and that a cookie is stored at the correct location.
	// TODO when we allow admin access via macaroon creds,
	// change this test to expect success.
	c.Log("macaroon unauthorized error")
	p.Username, p.Password = "", ""
	p.ExpectStatus = http.StatusUnauthorized
	p.ExpectBody = params.Error{
		Message: `unauthorized: access denied for user "bob"`,
		Code:    params.ErrUnauthorized,
	}
	httptesting.AssertJSONCall(c, p)
	sort.Strings(checkedCaveats)
	c.Assert(checkedCaveats, jc.DeepEquals, []string{
		"is-authenticated-user ",
	})
	checkedCaveats = nil
	c.Assert(cookieJar.cookieURLs, gc.DeepEquals, []string{"http://somehost/"})

	// Check that the call fails with incorrect simple auth info.
	c.Log("simple auth error")
	p.Password = "bad-password"
	p.ExpectStatus = http.StatusUnauthorized
	p.ExpectBody = params.Error{
		Message: "authentication failed: missing HTTP auth header",
		Code:    params.ErrUnauthorized,
	}

	// Check that it fails when the discharger refuses the discharge.
	c.Log("macaroon discharge error")
	client = httpbakery.NewHTTPClient()
	dischargeError = fmt.Errorf("go away")
	p.Password = ""
	p.Username = ""
	p.ExpectError = `cannot get discharge from "http://[^"]*": cannot discharge: go away`
	httptesting.AssertJSONCall(c, p)
}

type cookieJar struct {
	cookieURLs []string
	http.CookieJar
}

func (j *cookieJar) SetCookies(url *url.URL, cookies []*http.Cookie) {
	url1 := *url
	url1.Host = "somehost"
	j.cookieURLs = append(j.cookieURLs, url1.String())
	j.CookieJar.SetCookies(url, cookies)
}

func noInteraction(*url.URL) error {
	return fmt.Errorf("unexpected interaction required")
}

func newServerWithDischarger(c *gc.C, session *mgo.Session, username string, groups []string) (http.Handler, *charmstore.Store, *bakerytest.Discharger) {
	discharger := bakerytest.NewDischarger(nil, func(cond string, arg string) ([]checkers.Caveat, error) {
		if username == "" {
			return nil, nil
		}
		return []checkers.Caveat{
			checkers.DeclaredCaveat(v4.UsernameAttr, username),
			checkers.DeclaredCaveat(v4.GroupsAttr, strings.Join(groups, " ")),
		}, nil
	})
	// Create a charm store server that will use the test third party for
	// its third party caveat.
	srv, store := newServer(c, session, nil, charmstore.ServerParams{
		AuthUsername:     serverParams.AuthUsername,
		AuthPassword:     serverParams.AuthPassword,
		IdentityLocation: discharger.Location(),
		PublicKeyLocator: discharger,
	})
	return srv, store, discharger
}

// dischargedAuthCookie retrieves and discharges an authentication macaroon cookie.
func dischargedAuthCookie(c *gc.C, srv http.Handler) *http.Cookie {
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: srv,
		URL:     storeURL("macaroon"),
		Method:  "GET",
	})
	var m macaroon.Macaroon
	err := json.Unmarshal(rec.Body.Bytes(), &m)
	c.Assert(err, gc.IsNil)
	ms, err := httpbakery.DischargeAll(&m, httpbakery.NewHTTPClient(), noInteraction)
	c.Assert(err, gc.IsNil)
	macaroonCookie, err := httpbakery.NewCookie(ms)
	c.Assert(err, gc.IsNil)
	return macaroonCookie
}

type authSuite struct {
	storetesting.IsolatedMgoSuite
}

var _ = gc.Suite(&authSuite{})

var readAuthorizationTests = []struct {
	// about holds the test description.
	about string
	// username holds the authenticated user name returned by the discharger.
	// If empty, an anonymous user is returned.
	username string
	// groups holds group names the user is member of, as returned by the
	// discharger.
	groups []string
	// readPerm stores a list of users with read permissions.
	readPerm []string
	// expectStatus is the expected HTTP response status.
	// Defaults to 200 status OK.
	expectStatus int
	// expectBody holds the expected body of the HTTP response. If nil,
	// the body is not checked and the response is assumed to be ok.
	expectBody interface{}
}{{
	about:    "anonymous users are authorized",
	readPerm: []string{params.Everyone},
}, {
	about:    "everyone is authorized",
	username: "dalek",
	readPerm: []string{params.Everyone},
}, {
	about:    "everyone and a specific user",
	username: "dalek",
	readPerm: []string{params.Everyone, "janeway"},
}, {
	about:    "specific user authorized",
	username: "who",
	readPerm: []string{"who"},
}, {
	about:    "multiple specific users authorized",
	username: "picard",
	readPerm: []string{"kirk", "picard", "sisko"},
}, {
	about:        "nobody authorized",
	username:     "picard",
	expectStatus: http.StatusUnauthorized,
	expectBody: params.Error{
		Code:    params.ErrUnauthorized,
		Message: `unauthorized: access denied for user "picard"`,
	},
}, {
	about:        "access denied for user",
	username:     "kirk",
	readPerm:     []string{"picard", "sisko"},
	expectStatus: http.StatusUnauthorized,
	expectBody: params.Error{
		Code:    params.ErrUnauthorized,
		Message: `unauthorized: access denied for user "kirk"`,
	},
}, {
	about:    "everyone is authorized (user is member of groups)",
	username: "dalek",
	groups:   []string{"group1", "group2"},
	readPerm: []string{params.Everyone},
}, {
	about:    "everyone and a specific group",
	username: "dalek",
	groups:   []string{"group2", "group3"},
	readPerm: []string{params.Everyone, "group1"},
}, {
	about:    "specific group authorized",
	username: "who",
	groups:   []string{"group1", "group42", "group2"},
	readPerm: []string{"group42"},
}, {
	about:    "multiple specific groups authorized",
	username: "picard",
	groups:   []string{"group2"},
	readPerm: []string{"kirk", "group0", "group2"},
}, {
	about:        "no group authorized",
	username:     "picard",
	groups:       []string{"group1", "group2"},
	expectStatus: http.StatusUnauthorized,
	expectBody: params.Error{
		Code:    params.ErrUnauthorized,
		Message: `unauthorized: access denied for user "picard"`,
	},
}, {
	about:        "access denied for group",
	username:     "kirk",
	groups:       []string{"group1", "group2", "group3"},
	readPerm:     []string{"picard", "sisko", "group42", "group47"},
	expectStatus: http.StatusUnauthorized,
	expectBody: params.Error{
		Code:    params.ErrUnauthorized,
		Message: `unauthorized: access denied for user "kirk"`,
	},
}}

func (s *authSuite) TestReadAuthorization(c *gc.C) {
	for i, test := range readAuthorizationTests {
		c.Logf("test %d: %s", i, test.about)

		// Create a new server with a third party discharger.
		srv, store, discharger := newServerWithDischarger(c, s.Session, test.username, test.groups)
		defer discharger.Close()

		// Retrieve the macaroon cookie.
		cookies := []*http.Cookie{dischargedAuthCookie(c, srv)}

		// Add a charm to the store, used for testing.
		err := store.AddCharmWithArchive(
			charm.MustParseReference("~charmers/utopic/wordpress-42"),
			nil,
			storetesting.Charms.CharmDir("wordpress"))
		c.Assert(err, gc.IsNil)
		baseUrl := charm.MustParseReference("~charmers/wordpress")

		// Change the ACLs for the testing charm.
		store.DB.BaseEntities().UpdateId(baseUrl, bson.D{{"$set",
			bson.D{{"acls.read", test.readPerm}},
		}})

		// Prepare the expected status.
		expectStatus := test.expectStatus
		if expectStatus == 0 {
			expectStatus = http.StatusOK
		}

		// Define an helper function used to send requests and check responses.
		makeRequest := func(path string) {
			rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
				Handler: srv,
				URL:     storeURL(path),
				Cookies: cookies,
			})
			c.Assert(rec.Code, gc.Equals, expectStatus, gc.Commentf("body: %s", rec.Body))
			if test.expectBody != nil {
				c.Assert(rec.Body.String(), jc.JSONEquals, test.expectBody)
			}
		}

		// Perform a meta request.
		makeRequest("~charmers/wordpress/meta/archive-size")

		// Perform an id request.
		makeRequest("~charmers/wordpress/expand-id")

		// Remove all entities from the store.
		_, err = store.DB.Entities().RemoveAll(nil)
		c.Assert(err, gc.IsNil)
	}
}

var writeAuthorizationTests = []struct {
	// about holds the test description.
	about string
	// username holds the authenticated user name returned by the discharger.
	// If empty, an anonymous user is returned.
	username string
	// groups holds group names the user is member of, as returned by the
	// discharger.
	groups []string
	// writePerm stores a list of users with write permissions.
	writePerm []string
	// expectStatus is the expected HTTP response status.
	// Defaults to 200 status OK.
	expectStatus int
	// expectBody holds the expected body of the HTTP response. If nil,
	// the body is not checked and the response is assumed to be ok.
	expectBody interface{}
}{{
	about:        "anonymous users are not authorized",
	writePerm:    []string{"who"},
	expectStatus: http.StatusUnauthorized,
	expectBody: params.Error{
		Code:    params.ErrUnauthorized,
		Message: "unauthorized: no username declared",
	},
}, {
	about:     "specific user authorized to write",
	username:  "dalek",
	writePerm: []string{"dalek"},
}, {
	about:     "multiple users authorized",
	username:  "sisko",
	writePerm: []string{"kirk", "picard", "sisko"},
}, {
	about:        "no users authorized",
	username:     "who",
	expectStatus: http.StatusUnauthorized,
	expectBody: params.Error{
		Code:    params.ErrUnauthorized,
		Message: `unauthorized: access denied for user "who"`,
	},
}, {
	about:        "specific user unauthorized",
	username:     "kirk",
	writePerm:    []string{"picard", "sisko", "janeway"},
	expectStatus: http.StatusUnauthorized,
	expectBody: params.Error{
		Code:    params.ErrUnauthorized,
		Message: `unauthorized: access denied for user "kirk"`,
	},
}, {
	about:     "access granted for group",
	username:  "picard",
	groups:    []string{"group1", "group2"},
	writePerm: []string{"group2"},
}, {
	about:     "multiple groups authorized",
	username:  "picard",
	groups:    []string{"group1", "group2"},
	writePerm: []string{"kirk", "group0", "group1", "group2"},
}, {
	about:        "no group authorized",
	username:     "picard",
	groups:       []string{"group1", "group2"},
	expectStatus: http.StatusUnauthorized,
	expectBody: params.Error{
		Code:    params.ErrUnauthorized,
		Message: `unauthorized: access denied for user "picard"`,
	},
}, {
	about:        "access denied for group",
	username:     "kirk",
	groups:       []string{"group1", "group2", "group3"},
	writePerm:    []string{"picard", "sisko", "group42", "group47"},
	expectStatus: http.StatusUnauthorized,
	expectBody: params.Error{
		Code:    params.ErrUnauthorized,
		Message: `unauthorized: access denied for user "kirk"`,
	},
}}

func (s *authSuite) TestWriteAuthorization(c *gc.C) {
	for i, test := range writeAuthorizationTests {
		c.Logf("test %d: %s", i, test.about)

		// Create a new server with a third party discharger.
		srv, store, discharger := newServerWithDischarger(c, s.Session, test.username, test.groups)
		defer discharger.Close()

		// Retrieve the macaroon cookie.
		cookies := []*http.Cookie{dischargedAuthCookie(c, srv)}

		// Add a charm to the store, used for testing.
		err := store.AddCharmWithArchive(
			charm.MustParseReference("~charmers/utopic/wordpress-42"),
			nil,
			storetesting.Charms.CharmDir("wordpress"))
		c.Assert(err, gc.IsNil)
		baseUrl := charm.MustParseReference("~charmers/wordpress")

		// Change the ACLs for the testing charm.
		store.DB.BaseEntities().UpdateId(baseUrl, bson.D{{"$set",
			bson.D{{"acls.write", test.writePerm}},
		}})

		// Prepare the expected status.
		expectStatus := test.expectStatus
		if expectStatus == 0 {
			expectStatus = http.StatusOK
		}

		// Perform a meta PUT request.
		rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler: srv,
			URL:     storeURL("~charmers/wordpress/meta/extra-info/key"),
			Method:  "PUT",
			Header: http.Header{
				"Content-Type": {"application/json"},
			},
			Cookies: cookies,
			Body:    strings.NewReader("42"),
		})
		c.Assert(rec.Code, gc.Equals, expectStatus, gc.Commentf("body: %s", rec.Body))
		if test.expectBody != nil {
			c.Assert(rec.Body.String(), jc.JSONEquals, test.expectBody)
		}

		// Remove all entities from the store.
		_, err = store.DB.Entities().RemoveAll(nil)
		c.Assert(err, gc.IsNil)
	}
}

var uploadEntityAuthorizationTests = []struct {
	// about holds the test description.
	about string
	// username holds the authenticated user name returned by the discharger.
	// If empty, an anonymous user is returned.
	username string
	// groups holds group names the user is member of, as returned by the
	// discharger.
	groups []string
	// id holds the id of the entity to be uploaded.
	id string
	// expectStatus is the expected HTTP response status.
	// Defaults to 200 status OK.
	expectStatus int
	// expectBody holds the expected body of the HTTP response. If nil,
	// the body is not checked and the response is assumed to be ok.
	expectBody interface{}
}{{
	about:    "user owned entity",
	username: "who",
	id:       "~who/utopic/django",
}, {
	about:    "group owned entity",
	username: "dalek",
	groups:   []string{"group1", "group2"},
	id:       "~group1/utopic/django",
}, {
	about:    "specific group",
	username: "dalek",
	groups:   []string{"group42"},
	id:       "~group42/utopic/django",
}, {
	about:        "promulgated entity",
	username:     "sisko",
	groups:       []string{"group1", "group2"},
	id:           "utopic/django",
	expectStatus: http.StatusUnauthorized,
	expectBody: params.Error{
		Code:    params.ErrUnauthorized,
		Message: `unauthorized: access denied for user "sisko"`,
	},
}, {
	about:        "anonymous user",
	id:           "~who/utopic/django",
	expectStatus: http.StatusUnauthorized,
	expectBody: params.Error{
		Code:    params.ErrUnauthorized,
		Message: "unauthorized: no username declared",
	},
}, {
	about:        "anonymous user and promulgated entity",
	id:           "utopic/django",
	expectStatus: http.StatusUnauthorized,
	expectBody: params.Error{
		Code:    params.ErrUnauthorized,
		Message: "unauthorized: no username declared",
	},
}, {
	about:        "user does not match",
	username:     "kirk",
	id:           "~picard/utopic/django",
	expectStatus: http.StatusUnauthorized,
	expectBody: params.Error{
		Code:    params.ErrUnauthorized,
		Message: `unauthorized: access denied for user "kirk"`,
	},
}, {
	about:        "group does not match",
	username:     "kirk",
	groups:       []string{"group1", "group2", "group3"},
	id:           "~group0/utopic/django",
	expectStatus: http.StatusUnauthorized,
	expectBody: params.Error{
		Code:    params.ErrUnauthorized,
		Message: `unauthorized: access denied for user "kirk"`,
	},
}, {
	about:        "specific group and promulgated entity",
	username:     "janeway",
	groups:       []string{"group1"},
	id:           "utopic/django",
	expectStatus: http.StatusUnauthorized,
	expectBody: params.Error{
		Code:    params.ErrUnauthorized,
		Message: `unauthorized: access denied for user "janeway"`,
	},
}}

func (s *authSuite) TestUploadEntityAuthorization(c *gc.C) {
	for i, test := range uploadEntityAuthorizationTests {
		c.Logf("test %d: %s", i, test.about)

		// Create a new server with a third party discharger.
		srv, store, discharger := newServerWithDischarger(c, s.Session, test.username, test.groups)
		defer discharger.Close()

		// Retrieve the macaroon cookie.
		cookies := []*http.Cookie{dischargedAuthCookie(c, srv)}

		// Prepare the expected status.
		expectStatus := test.expectStatus
		if expectStatus == 0 {
			expectStatus = http.StatusOK
		}

		// Try to upload the entity.
		body, hash, size := s.archiveInfo(c)
		defer body.Close()
		rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler:       srv,
			URL:           storeURL(test.id + "/archive?hash=" + hash),
			Method:        "POST",
			ContentLength: size,
			Header: http.Header{
				"Content-Type": {"application/zip"},
			},
			Body:    body,
			Cookies: cookies,
		})
		c.Assert(rec.Code, gc.Equals, expectStatus, gc.Commentf("body: %s", rec.Body))
		if test.expectBody != nil {
			c.Assert(rec.Body.String(), jc.JSONEquals, test.expectBody)
		}

		// Remove all entities from the store.
		_, err := store.DB.Entities().RemoveAll(nil)
		c.Assert(err, gc.IsNil)
	}
}

// archiveInfo prepares a zip archive of an entity and return a reader for the
// archive, its blob hash and size.
func (s *authSuite) archiveInfo(c *gc.C) (r io.ReadCloser, hashSum string, size int64) {
	ch := storetesting.Charms.CharmArchive(c.MkDir(), "wordpress")
	f, err := os.Open(ch.Path)
	c.Assert(err, gc.IsNil)
	hash, size := hashOf(f)
	_, err = f.Seek(0, 0)
	c.Assert(err, gc.IsNil)
	return f, hash, size
}
