// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4_test // import "gopkg.in/juju/charmstore.v5-unstable/internal/v4"

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
	"time"

	jc "github.com/juju/testing/checkers"
	"github.com/juju/testing/httptesting"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v1/csclient/params"
	"gopkg.in/macaroon-bakery.v1/bakery/checkers"
	"gopkg.in/macaroon-bakery.v1/httpbakery"
	"gopkg.in/macaroon.v1"

	"gopkg.in/juju/charmstore.v5-unstable/internal/storetesting"
	"gopkg.in/juju/charmstore.v5-unstable/internal/v4"
)

func (s *commonSuite) AssertEndpointAuth(c *gc.C, p httptesting.JSONCallParams) {
	s.testNonMacaroonAuth(c, p)
	s.testMacaroonAuth(c, p)
}

func (s *commonSuite) testNonMacaroonAuth(c *gc.C, p httptesting.JSONCallParams) {
	p.Handler = s.noMacaroonSrv
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

func (s *commonSuite) testMacaroonAuth(c *gc.C, p httptesting.JSONCallParams) {
	// Make a test third party caveat discharger.
	var checkedCaveats []string
	var mu sync.Mutex
	var dischargeError error
	s.discharge = func(cond string, arg string) ([]checkers.Caveat, error) {
		mu.Lock()
		defer mu.Unlock()
		checkedCaveats = append(checkedCaveats, cond+" "+arg)
		if dischargeError != nil {
			return nil, dischargeError
		}
		return []checkers.Caveat{
			checkers.DeclaredCaveat("username", "bob"),
		}, nil
	}
	p.Handler = s.srv

	client := httpbakery.NewHTTPClient()
	cookieJar := &cookieJar{CookieJar: client.Jar}
	client.Jar = cookieJar
	p.Do = bakeryDo(client)

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
	p.Do = bakeryDo(client) // clear cookies
	p.Password = ""
	p.Username = ""
	p.ExpectError = `cannot get discharge from "https://[^"]*": third party refused discharge: cannot discharge: go away`
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

// dischargedAuthCookie retrieves and discharges an authentication macaroon cookie. It adds the provided
// first-party caveats before discharging the macaroon.
func dischargedAuthCookie(c *gc.C, srv http.Handler, caveats ...string) *http.Cookie {
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: srv,
		URL:     storeURL("macaroon"),
		Method:  "GET",
	})
	var m macaroon.Macaroon
	err := json.Unmarshal(rec.Body.Bytes(), &m)
	c.Assert(err, gc.IsNil)
	for _, cav := range caveats {
		err := m.AddFirstPartyCaveat(cav)
		c.Assert(err, gc.IsNil)
	}
	client := httpbakery.NewClient()
	ms, err := client.DischargeAll(&m)
	c.Assert(err, gc.IsNil)
	macaroonCookie, err := httpbakery.NewCookie(ms)
	c.Assert(err, gc.IsNil)
	return macaroonCookie
}

type authSuite struct {
	commonSuite
}

var _ = gc.Suite(&authSuite{})

func (s *authSuite) SetUpSuite(c *gc.C) {
	s.enableIdentity = true
	s.commonSuite.SetUpSuite(c)
}

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

func dischargeForUser(username string) func(_, _ string) ([]checkers.Caveat, error) {
	return func(_, _ string) ([]checkers.Caveat, error) {
		return []checkers.Caveat{
			checkers.DeclaredCaveat(v4.UsernameAttr, username),
		}, nil
	}
}

func (s *authSuite) TestReadAuthorization(c *gc.C) {
	for i, test := range readAuthorizationTests {
		c.Logf("test %d: %s", i, test.about)

		s.discharge = dischargeForUser(test.username)
		s.idM.groups = map[string][]string{
			test.username: test.groups,
		}

		// Add a charm to the store, used for testing.
		err := s.store.AddCharmWithArchive(
			newResolvedURL("~charmers/utopic/wordpress-42", -1),
			storetesting.Charms.CharmDir("wordpress"),
		)
		c.Assert(err, gc.IsNil)
		baseURL := charm.MustParseReference("~charmers/wordpress")

		// Change the ACLs for the testing charm.
		err = s.store.SetPerms(baseURL, "read", test.readPerm...)
		c.Assert(err, gc.IsNil)

		// Prepare the expected status.
		expectStatus := test.expectStatus
		if expectStatus == 0 {
			expectStatus = http.StatusOK
		}

		// Define an helper function used to send requests and check responses.
		makeRequest := func(path string) {
			rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
				Handler: s.srv,
				Do:      bakeryDo(nil),
				URL:     storeURL(path),
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
		_, err = s.store.DB.Entities().RemoveAll(nil)
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

		s.discharge = dischargeForUser(test.username)
		s.idM.groups = map[string][]string{
			test.username: test.groups,
		}

		// Add a charm to the store, used for testing.
		err := s.store.AddCharmWithArchive(
			newResolvedURL("~charmers/utopic/wordpress-42", -1),
			storetesting.Charms.CharmDir("wordpress"))
		c.Assert(err, gc.IsNil)
		baseURL := charm.MustParseReference("~charmers/wordpress")

		// Change the ACLs for the testing charm.
		err = s.store.SetPerms(baseURL, "write", test.writePerm...)
		c.Assert(err, gc.IsNil)

		// Prepare the expected status.
		expectStatus := test.expectStatus
		if expectStatus == 0 {
			expectStatus = http.StatusOK
		}

		client := httpbakery.NewHTTPClient()
		// Perform a meta PUT request.
		rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler: s.srv,
			Do:      bakeryDo(client),
			URL:     storeURL("~charmers/wordpress/meta/extra-info/key"),
			Method:  "PUT",
			Header: http.Header{
				"Content-Type": {"application/json"},
			},
			Body: strings.NewReader("42"),
		})
		c.Assert(rec.Code, gc.Equals, expectStatus, gc.Commentf("body: %s", rec.Body))
		if test.expectBody != nil {
			c.Assert(rec.Body.String(), jc.JSONEquals, test.expectBody)
		}

		// Remove all entities from the store.
		_, err = s.store.DB.Entities().RemoveAll(nil)
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
	id:           "~charmers/utopic/django",
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
	id:           "~charmers/utopic/django",
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
	id:           "~charmers/utopic/django",
	expectStatus: http.StatusUnauthorized,
	expectBody: params.Error{
		Code:    params.ErrUnauthorized,
		Message: `unauthorized: access denied for user "janeway"`,
	},
}}

func (s *authSuite) TestUploadEntityAuthorization(c *gc.C) {
	for i, test := range uploadEntityAuthorizationTests {
		c.Logf("test %d: %s", i, test.about)

		s.discharge = dischargeForUser(test.username)
		s.idM.groups = map[string][]string{
			test.username: test.groups,
		}

		// Prepare the expected status.
		expectStatus := test.expectStatus
		if expectStatus == 0 {
			expectStatus = http.StatusOK
		}

		// Try to upload the entity.
		body, hash, size := s.archiveInfo(c)
		defer body.Close()

		client := httpbakery.NewHTTPClient()
		rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler:       s.srv,
			Do:            bakeryDo(client),
			URL:           storeURL(test.id + "/archive?hash=" + hash),
			Method:        "POST",
			ContentLength: size,
			Header: http.Header{
				"Content-Type": {"application/zip"},
			},
			Body: body,
		})
		c.Assert(rec.Code, gc.Equals, expectStatus, gc.Commentf("body: %s", rec.Body))
		if test.expectBody != nil {
			c.Assert(rec.Body.String(), jc.JSONEquals, test.expectBody)
		}

		// Remove all entities from the store.
		_, err := s.store.DB.Entities().RemoveAll(nil)
		c.Assert(err, gc.IsNil)
	}
}

type readSeekCloser interface {
	io.ReadCloser
	io.Seeker
}

// archiveInfo prepares a zip archive of an entity and return a reader for the
// archive, its blob hash and size.
func (s *authSuite) archiveInfo(c *gc.C) (r readSeekCloser, hashSum string, size int64) {
	ch := storetesting.Charms.CharmArchive(c.MkDir(), "wordpress")
	f, err := os.Open(ch.Path)
	c.Assert(err, gc.IsNil)
	hash, size := hashOf(f)
	_, err = f.Seek(0, 0)
	c.Assert(err, gc.IsNil)
	return f, hash, size
}

var isEntityCaveatTests = []struct {
	url         string
	expectError string
}{{
	url: "~charmers/utopic/wordpress-42/archive",
}, {
	url: "~charmers/utopic/wordpress-42/meta/hash",
}, {
	url: "wordpress/archive",
}, {
	url: "wordpress/meta/hash",
}, {
	url: "utopic/wordpress-10/archive",
}, {
	url: "utopic/wordpress-10/meta/hash",
}, {
	url:         "~charmers/utopic/wordpress-41/archive",
	expectError: `verification failed: caveat "is-entity cs:~charmers/utopic/wordpress-42" not satisfied: API operation on entity cs:~charmers/utopic/wordpress-41, want cs:~charmers/utopic/wordpress-42`,
}, {
	url:         "~charmers/utopic/wordpress-41/meta/hash",
	expectError: `verification failed: caveat "is-entity cs:~charmers/utopic/wordpress-42" not satisfied: API operation on entity cs:~charmers/utopic/wordpress-41, want cs:~charmers/utopic/wordpress-42`,
}, {
	url:         "utopic/wordpress-9/archive",
	expectError: `verification failed: caveat "is-entity cs:~charmers/utopic/wordpress-42" not satisfied: API operation on entity cs:utopic/wordpress-9, want cs:~charmers/utopic/wordpress-42`,
}, {
	url:         "utopic/wordpress-9/meta/hash",
	expectError: `verification failed: caveat "is-entity cs:~charmers/utopic/wordpress-42" not satisfied: API operation on entity cs:utopic/wordpress-9, want cs:~charmers/utopic/wordpress-42`,
}, {
	url:         "log",
	expectError: `verification failed: caveat "is-entity cs:~charmers/utopic/wordpress-42" not satisfied: API operation does not involve expected entity cs:~charmers/utopic/wordpress-42`,
}}

func (s *authSuite) TestIsEntityCaveat(c *gc.C) {
	s.discharge = func(_, _ string) ([]checkers.Caveat, error) {
		return []checkers.Caveat{{
			Condition: "is-entity cs:~charmers/utopic/wordpress-42",
		},
			checkers.DeclaredCaveat(v4.UsernameAttr, "bob"),
		}, nil
	}

	// Add a charm to the store, used for testing.
	err := s.store.AddCharmWithArchive(
		newResolvedURL("~charmers/utopic/wordpress-41", 9),
		storetesting.Charms.CharmDir("wordpress"))
	c.Assert(err, gc.IsNil)
	err = s.store.AddCharmWithArchive(
		newResolvedURL("~charmers/utopic/wordpress-42", 10),
		storetesting.Charms.CharmDir("wordpress"))
	c.Assert(err, gc.IsNil)
	// Change the ACLs for the testing charm.
	err = s.store.SetPerms(charm.MustParseReference("cs:~charmers/wordpress"), "read", "bob")
	c.Assert(err, gc.IsNil)

	for i, test := range isEntityCaveatTests {
		c.Logf("test %d: %s", i, test.url)
		rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler: s.srv,
			Do:      bakeryDo(nil),
			URL:     storeURL(test.url),
			Method:  "GET",
		})
		if test.expectError != "" {
			c.Assert(rec.Code, gc.Equals, http.StatusProxyAuthRequired)
			var respErr httpbakery.Error
			err := json.Unmarshal(rec.Body.Bytes(), &respErr)
			c.Assert(err, gc.IsNil)
			c.Assert(respErr.Message, gc.Matches, test.expectError)
			continue
		}
		c.Assert(rec.Code, gc.Equals, http.StatusOK, gc.Commentf("body: %s", rec.Body.Bytes()))
	}
}

func (s *authSuite) TestDelegatableMacaroon(c *gc.C) {
	// Create a new server with a third party discharger.
	s.discharge = dischargeForUser("bob")

	// First check that we get a macaraq error when using a vanilla http do
	// request.
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler: s.srv,
		URL:     storeURL("delegatable-macaroon"),
		ExpectBody: httptesting.BodyAsserter(func(c *gc.C, m json.RawMessage) {
			// Allow any body - the next check will check that it's a valid macaroon.
		}),
		ExpectStatus: http.StatusProxyAuthRequired,
	})

	client := httpbakery.NewHTTPClient()

	now := time.Now()
	var gotBody json.RawMessage
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler: s.srv,
		URL:     storeURL("delegatable-macaroon"),
		ExpectBody: httptesting.BodyAsserter(func(c *gc.C, m json.RawMessage) {
			gotBody = m
		}),
		Do:           bakeryDo(client),
		ExpectStatus: http.StatusOK,
	})

	c.Assert(gotBody, gc.NotNil)
	var m macaroon.Macaroon
	err := json.Unmarshal(gotBody, &m)
	c.Assert(err, gc.IsNil)

	caveats := m.Caveats()
	foundExpiry := false
	for _, cav := range caveats {
		cond, arg, err := checkers.ParseCaveat(cav.Id)
		c.Assert(err, gc.IsNil)
		switch cond {
		case checkers.CondTimeBefore:
			t, err := time.Parse(time.RFC3339Nano, arg)
			c.Assert(err, gc.IsNil)
			c.Assert(t, jc.TimeBetween(now.Add(v4.DelegatableMacaroonExpiry), now.Add(v4.DelegatableMacaroonExpiry+time.Second)))
			foundExpiry = true
		}
	}
	c.Assert(foundExpiry, jc.IsTrue)

	// Now check that we can use the obtained macaroon to do stuff
	// as the declared user.

	err = s.store.AddCharmWithArchive(
		newResolvedURL("~charmers/utopic/wordpress-41", 9),
		storetesting.Charms.CharmDir("wordpress"))
	c.Assert(err, gc.IsNil)
	// Change the ACLs for the testing charm.
	err = s.store.SetPerms(charm.MustParseReference("cs:~charmers/wordpress"), "read", "bob")
	c.Assert(err, gc.IsNil)

	// First check that we require authorization to access the charm.
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("~charmers/utopic/wordpress/meta/id-name"),
		Method:  "GET",
	})
	c.Assert(rec.Code, gc.Equals, http.StatusProxyAuthRequired)

	// Then check that the request succeeds if we provide the delegatable
	// macaroon.

	client = httpbakery.NewHTTPClient()
	u, err := url.Parse("http://127.0.0.1")
	c.Assert(err, gc.IsNil)
	err = httpbakery.SetCookie(client.Jar, u, macaroon.Slice{&m})
	c.Assert(err, gc.IsNil)

	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler: s.srv,
		URL:     storeURL("~charmers/utopic/wordpress/meta/id-name"),
		ExpectBody: params.IdNameResponse{
			Name: "wordpress",
		},

		ExpectStatus: http.StatusOK,
		Do:           bakeryDo(client),
	})
}

func (s *authSuite) TestDelegatableMacaroonWithBasicAuth(c *gc.C) {
	// First check that we get a macaraq error when using a vanilla http do
	// request.
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:  s.srv,
		Username: testUsername,
		Password: testPassword,
		URL:      storeURL("delegatable-macaroon"),
		ExpectBody: params.Error{
			Code:    params.ErrForbidden,
			Message: "delegatable macaroon is not obtainable using admin credentials",
		},
		ExpectStatus: http.StatusForbidden,
	})
}

func (s *authSuite) TestGroupsForUserSuccess(c *gc.C) {
	h := s.handler(c)
	defer h.Close()
	s.idM.groups = map[string][]string{
		"bob": {"one", "two"},
	}
	groups, err := v4.GroupsForUser(h, "bob")
	c.Assert(err, gc.IsNil)
	c.Assert(groups, jc.DeepEquals, []string{"one", "two"})
}

func (s *authSuite) TestGroupsForUserWithNoIdentity(c *gc.C) {
	h := s.handler(c)
	defer h.Close()
	groups, err := v4.GroupsForUser(h, "someone")
	c.Assert(err, gc.IsNil)
	c.Assert(groups, gc.HasLen, 0)
}

func (s *authSuite) TestGroupsForUserWithInvalidIdentityURL(c *gc.C) {
	s.PatchValue(&s.srvParams.IdentityAPIURL, ":::::")
	h := s.handler(c)
	defer h.Close()
	groups, err := v4.GroupsForUser(h, "someone")
	c.Assert(err, gc.ErrorMatches, `cannot get groups for someone: cannot GET \"/v1/u/someone/groups\": cannot create request for \":::::/v1/u/someone/groups\": parse :::::/v1/u/someone/groups: missing protocol scheme`)
	c.Assert(groups, gc.HasLen, 0)
}

func (s *authSuite) TestGroupsForUserWithInvalidBody(c *gc.C) {
	h := s.handler(c)
	defer h.Close()
	s.idM.body = "bad"
	s.idM.contentType = "application/json"
	groups, err := v4.GroupsForUser(h, "someone")
	c.Assert(err, gc.ErrorMatches, `cannot get groups for someone: cannot unmarshal response: invalid character 'b' looking for beginning of value`)
	c.Assert(groups, gc.HasLen, 0)
}

func (s *authSuite) TestGroupsForUserWithErrorResponse(c *gc.C) {
	h := s.handler(c)
	defer h.Close()
	s.idM.body = `{"message":"some error","code":"some code"}`
	s.idM.status = http.StatusUnauthorized
	s.idM.contentType = "application/json"
	groups, err := v4.GroupsForUser(h, "someone")
	c.Assert(err, gc.ErrorMatches, `cannot get groups for someone: some error`)
	c.Assert(groups, gc.HasLen, 0)
}

func (s *authSuite) TestGroupsForUserWithBadErrorResponse(c *gc.C) {
	h := s.handler(c)
	defer h.Close()
	s.idM.body = `{"message":"some error"`
	s.idM.status = http.StatusUnauthorized
	s.idM.contentType = "application/json"
	groups, err := v4.GroupsForUser(h, "someone")
	c.Assert(err, gc.ErrorMatches, `cannot get groups for someone: bad status "401 Unauthorized"`)
	c.Assert(groups, gc.HasLen, 0)
}

type errorTransport string

func (e errorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errgo.New(string(e))
}
