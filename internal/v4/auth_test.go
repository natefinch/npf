// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
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
		return nil, dischargeError
	})
	defer discharger.Close()

	// Create a charmstore server that will use the test third party for
	// its third party caveat.
	srv, _ := newServer(c, session, nil, charmstore.ServerParams{
		AuthUsername:     "test-user",
		AuthPassword:     "test-password",
		AuthLocation:     discharger.Location(),
		PublicKeyLocator: discharger,
	})
	p.Handler = srv

	client1 := *httpbakery.NewHTTPClient()
	client1.Jar = nil
	p.Do = func(req *http.Request) (*http.Response, error) {
		return httpbakery.Do(&client1, req, noInteraction)
	}

	// Check that the call succeeds with simple auth.
	c.Log("simple auth sucess")
	p.Username = "test-user"
	p.Password = "test-password"
	httptesting.AssertJSONCall(c, p)
	c.Assert(checkedCaveats, gc.HasLen, 0)

	// Check that the call succeeds and uses the third party checker.
	c.Log("macaroon unauthorized error")
	p.Username, p.Password = "", ""
	p.ExpectStatus = http.StatusUnauthorized
	p.ExpectBody = params.Error{
		Code:    params.ErrUnauthorized,
		Message: "unauthorized: no username declared",
	}
	httptesting.AssertJSONCall(c, p)
	sort.Strings(checkedCaveats)
	c.Assert(checkedCaveats, jc.DeepEquals, []string{
		"is-authenticated-user ",
	})
	checkedCaveats = nil

	// Check that the call fails with simple auth that's bad.
	c.Log("simple auth error")
	p.Password = "bad-password"
	p.ExpectStatus = http.StatusUnauthorized
	p.ExpectBody = params.Error{
		Message: "authentication failed: missing HTTP auth header",
		Code:    params.ErrUnauthorized,
	}

	// Check that it fails when the discharger refuses the discharge.
	c.Log("macaroon discharge error")
	dischargeError = fmt.Errorf("go away")
	p.Password = ""
	p.Username = ""
	p.ExpectError = `cannot get discharge from "http://[^"]*": cannot discharge: go away`
	httptesting.AssertJSONCall(c, p)
}

func noInteraction(*url.URL) error {
	return fmt.Errorf("unexpected interaction required")
}

func newServerWithDischarger(c *gc.C, session *mgo.Session, username string) (http.Handler, *charmstore.Store, *bakerytest.Discharger) {
	discharger := bakerytest.NewDischarger(nil, func(cond string, arg string) ([]checkers.Caveat, error) {
		if username == "" {
			return nil, nil
		}
		return []checkers.Caveat{checkers.DeclaredCaveat("username", username)}, nil
	})
	// Create a charm store server that will use the test third party for
	// its third party caveat.
	srv, store := newServer(c, session, nil, charmstore.ServerParams{
		AuthUsername:     serverParams.AuthUsername,
		AuthPassword:     serverParams.AuthPassword,
		AuthLocation:     discharger.Location(),
		PublicKeyLocator: discharger,
	})
	return srv, store, discharger
}

// macaroonCookie retrieves and discharges an authentication macaroon cookie.
func macaroonCookie(c *gc.C, srv http.Handler) *http.Cookie {
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

var readAuthorizationTests = []struct {
	// about holds the test description.
	about string
	// username holds the authenticated user name returned by the discharger.
	// If empty, an anonymous user is returned.
	username string
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
}}

type authSuite struct {
	storetesting.IsolatedMgoSuite
}

var _ = gc.Suite(&authSuite{})

func (s *authSuite) TestReadAuthorization(c *gc.C) {
	for i, test := range readAuthorizationTests {
		c.Logf("test %d: %s", i, test.about)

		// Create a new server with a third party discharger.
		srv, store, discharger := newServerWithDischarger(c, s.Session, test.username)
		defer discharger.Close()

		// Retrieve the macaroon cookie.
		cookies := []*http.Cookie{macaroonCookie(c, srv)}

		// Add a charm to the store, used for testing.
		err := store.AddCharmWithArchive(
			charm.MustParseReference("utopic/wordpress-42"),
			storetesting.Charms.CharmDir("wordpress"))
		c.Assert(err, gc.IsNil)
		baseUrl := charm.MustParseReference("wordpress")

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
		makeRequest("wordpress/meta/archive-size")

		// Perform an id request.
		makeRequest("wordpress/expand-id")

		// Remove all entities from the store.
		_, err = store.DB.Entities().RemoveAll(nil)
		c.Assert(err, gc.IsNil)
	}
}
