// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore // import "gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"

import (
	"errors"
	"net/http"

	"github.com/juju/testing/httptesting"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charmrepo.v0/csclient/params"

	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
	appver "gopkg.in/juju/charmstore.v5-unstable/version"
)

type debugSuite struct{}

var _ = gc.Suite(&debugSuite{})

var debugCheckTests = []struct {
	about        string
	checks       map[string]func() error
	expectStatus int
	expectBody   interface{}
}{{
	about:        "no checks",
	expectStatus: http.StatusOK,
	expectBody:   map[string]string{},
}, {
	about: "passing check",
	checks: map[string]func() error{
		"pass": func() error { return nil },
	},
	expectStatus: http.StatusOK,
	expectBody: map[string]string{
		"pass": "OK",
	},
}, {
	about: "failing check",
	checks: map[string]func() error{
		"fail": func() error { return errors.New("test fail") },
	},
	expectStatus: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: "check failure: [fail: test fail]",
	},
}, {
	about: "many pass",
	checks: map[string]func() error{
		"pass1": func() error { return nil },
		"pass2": func() error { return nil },
	},
	expectStatus: http.StatusOK,
	expectBody: map[string]string{
		"pass1": "OK",
		"pass2": "OK",
	},
}, {
	about: "many fail",
	checks: map[string]func() error{
		"fail1": func() error { return errors.New("test fail1") },
		"fail2": func() error { return errors.New("test fail2") },
	},
	expectStatus: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: "check failure: [fail1: test fail1] [fail2: test fail2]",
	},
}, {
	about: "pass and fail",
	checks: map[string]func() error{
		"pass": func() error { return nil },
		"fail": func() error { return errors.New("test fail") },
	},
	expectStatus: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: "check failure: [fail: test fail] [pass: OK]",
	},
}}

func (s *debugSuite) TestDebugCheck(c *gc.C) {
	for i, test := range debugCheckTests {
		c.Logf("%d. %s", i, test.about)
		hnd := debugCheck(test.checks)
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      hnd,
			ExpectStatus: test.expectStatus,
			ExpectBody:   test.expectBody,
		})
	}
}

func (s *debugSuite) TestDebugInfo(c *gc.C) {
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      router.HandleJSON(serveDebugInfo),
		ExpectStatus: http.StatusOK,
		ExpectBody:   appver.VersionInfo,
	})
}
