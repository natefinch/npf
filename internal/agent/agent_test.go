// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package agent_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/macaroon-bakery.v1/bakery"

	"gopkg.in/juju/charmstore.v5-unstable/internal/agent"
	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
)

type agentSuite struct {
	idM *idM
}

var _ = gc.Suite(&agentSuite{})

func (s *agentSuite) SetUpSuite(c *gc.C) {
	s.idM = newIdM(c)
}

func (s *agentSuite) TearDownSuite(c *gc.C) {
	s.idM.Close()
}

var agentLoginTests = []struct {
	about       string
	condition   string
	expectBody  interface{}
	expectError string
}{{
	about:      "no login required",
	condition:  "allow",
	expectBody: map[string]string{},
}, {
	about:      "successful agent login",
	condition:  "agent",
	expectBody: map[string]string{},
}, {
	about:       "interactive",
	condition:   "interactive",
	expectError: `cannot get discharge from ".*": cannot start interactive session: cannot get login methods: unexpected content type "text/plain"`,
}, {
	about:       "agent not supported",
	condition:   "no-agent",
	expectError: `cannot get discharge from "http://.*": cannot start interactive session: agent login not supported`,
}, {
	about:       "agent fail",
	condition:   "agent-fail",
	expectError: `cannot get discharge from "http://.*": cannot start interactive session: cannot log in: forced failure`,
}}

func (s *agentSuite) TestAgentLogin(c *gc.C) {
	key, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	for i, test := range agentLoginTests {
		c.Logf("%d. %s", i, test.about)
		client := agent.NewClient("testuser", key)
		u := fmt.Sprintf("%s/protected?test=%d&c=%s", s.idM.URL, i, url.QueryEscape(test.condition))
		req, err := http.NewRequest("GET", u, nil)
		c.Assert(err, gc.IsNil)
		resp, err := client.Do(req)
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			continue
		}
		c.Assert(err, gc.IsNil)
		defer resp.Body.Close()
		var v json.RawMessage
		err = router.UnmarshalJSONResponse(resp, &v, nil)
		c.Assert(err, gc.IsNil)
		c.Assert(string(v), jc.JSONEquals, test.expectBody)
	}
}
