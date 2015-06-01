// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package identity_test

import (
	"encoding/json"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/macaroon-bakery.v1/httpbakery"

	"gopkg.in/juju/charmstore.v5-unstable/internal/identity"
)

type clientSuite struct {
	idM    *idM
	client *identity.Client
}

var _ = gc.Suite(&clientSuite{})

func (s *clientSuite) SetUpSuite(c *gc.C) {
	s.idM = newIdM(c)
}

func (s *clientSuite) TearDownSuite(c *gc.C) {
	s.idM.Close()
}

func (s *clientSuite) SetUpTest(c *gc.C) {
	s.client = identity.NewClient(&identity.Params{
		URL:    s.idM.URL,
		Client: httpbakery.NewClient(),
	})
}

var getJSONTests = []struct {
	about       string
	path        string
	expectBody  interface{}
	expectError string
}{{
	about: "GET",
	path:  "/test",
	expectBody: map[string]string{
		"method": "GET",
	},
}, {
	about:       "GET bad URL",
	path:        "/%fg",
	expectError: `cannot GET "/%fg": cannot create request for ".*/%fg": parse .*/%fg: invalid URL escape "%fg"`,
}, {
	about:       "GET bad request",
	path:        "5/test",
	expectError: `cannot GET "5/test": cannot GET ".*5/test": .*`,
}, {
	about:       "GET error",
	path:        `/test?s=500&b=%7B%22message%22%3A%22an+error%22%7D`,
	expectError: `an error`,
}, {
	about:       "GET unparsable content type",
	path:        `/test?ct=bad+content+type`,
	expectError: `cannot parse content type: mime: expected slash after first token`,
}, {
	about:       "GET unexpected content type",
	path:        `/test?ct=application/xml`,
	expectError: `unexpected content type "application/xml"`,
}, {
	about:       "GET unmarshal error",
	path:        `/test?b=tru`,
	expectError: `cannot unmarshal response: invalid character ' ' in literal true \(expecting 'e'\)`,
}, {
	about:       "GET error cannot unmarshal",
	path:        `/test?b=fals&s=502`,
	expectError: `bad status "502 Bad Gateway"`,
}}

func (s *clientSuite) TestGetJSON(c *gc.C) {
	for i, test := range getJSONTests {
		c.Logf("%d. %s", i, test.about)
		var v json.RawMessage
		err := s.client.GetJSON(test.path, &v)
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			continue
		}
		c.Assert(err, gc.IsNil)
		c.Assert(string(v), jc.JSONEquals, test.expectBody)
	}
}

var groupsForUserTests = []struct {
	user         string
	expectGroups []string
	expectError  string
}{{
	user:         "user1",
	expectGroups: []string{"g1", "g2"},
}, {
	user:         "user2",
	expectGroups: []string{},
}, {
	user:        "user3",
	expectError: "cannot get groups for user3: /v1/u/user3/groups not found",
}}

func (s *clientSuite) TestGroupsForUser(c *gc.C) {
	for i, test := range groupsForUserTests {
		c.Logf("%d. %s", i, test.user)
		groups, err := s.client.GroupsForUser(test.user)
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			continue
		}
		c.Assert(err, gc.IsNil)
		c.Assert(groups, jc.DeepEquals, test.expectGroups)
	}
}
