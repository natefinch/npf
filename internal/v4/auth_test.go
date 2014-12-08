// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4_test

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"sync"

	jc "github.com/juju/testing/checkers"
	"github.com/juju/testing/httptesting"
	gc "gopkg.in/check.v1"
	"gopkg.in/macaroon-bakery.v0/bakery/checkers"
	"gopkg.in/macaroon-bakery.v0/bakerytest"
	"gopkg.in/macaroon-bakery.v0/httpbakery"
	"gopkg.in/mgo.v2"

	"github.com/juju/charmstore/internal/charmstore"
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

	client1 := *httpbakery.DefaultHTTPClient
	client1.Jar = nil
	p.Do = func(req *http.Request) (*http.Response, error) {
		return httpbakery.Do(&client1, req, noInteraction)
	}

	// Check that the call succeeds and uses the third party checker.
	httptesting.AssertJSONCall(c, p)
	sort.Strings(checkedCaveats)
	c.Assert(checkedCaveats, jc.DeepEquals, []string{
		"is-authenticated-user ",
	})
	checkedCaveats = nil

	// Check that the call succeeds with simple auth.
	p.Username = "test-user"
	p.Password = "test-password"
	httptesting.AssertJSONCall(c, p)
	c.Assert(checkedCaveats, gc.HasLen, 0)

	// Check that the call fails with simple auth that's bad.
	p.Password = "bad-password"
	p.ExpectStatus = http.StatusUnauthorized
	p.ExpectBody = params.Error{
		Message: "authentication failed: missing HTTP auth header",
		Code:    params.ErrUnauthorized,
	}

	// Check that it fails when the discharger refuses the discharge.
	dischargeError = fmt.Errorf("go away")
	p.Password = ""
	p.Username = ""
	p.ExpectError = `cannot get discharge from "http://[^"]*": cannot discharge: go away`
	httptesting.AssertJSONCall(c, p)
}

func noInteraction(*url.URL) error {
	return fmt.Errorf("unexpected interaction required")
}
