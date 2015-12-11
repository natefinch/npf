// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package mongodoc_test // import "gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"

import (
	"testing"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/mgo.v2/bson"

	"gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"
)

func TestPackage(t *testing.T) {
	gc.TestingT(t)
}

type DocSuite struct{}

var _ = gc.Suite(&DocSuite{})

func (s *DocSuite) TestIntBoolGetBSON(c *gc.C) {
	test := bson.D{{"true", mongodoc.IntBool(true)}, {"false", mongodoc.IntBool(false)}}
	b, err := bson.Marshal(test)
	c.Assert(err, gc.IsNil)
	result := make(map[string]int, 2)
	err = bson.Unmarshal(b, &result)
	c.Assert(err, gc.IsNil)
	c.Assert(result["true"], gc.Equals, 1)
	c.Assert(result["false"], gc.Equals, -1)
}

func (s *DocSuite) TestIntBoolSetBSON(c *gc.C) {
	test := bson.D{{"true", 1}, {"false", -1}}
	b, err := bson.Marshal(test)
	c.Assert(err, gc.IsNil)
	var result map[string]mongodoc.IntBool
	err = bson.Unmarshal(b, &result)
	c.Assert(err, gc.IsNil)
	c.Assert(result, jc.DeepEquals, map[string]mongodoc.IntBool{"true": true, "false": false})
}

func (s *DocSuite) TestIntBoolSetBSONIncorrectType(c *gc.C) {
	test := bson.D{{"test", "true"}}
	b, err := bson.Marshal(test)
	c.Assert(err, gc.IsNil)
	var result map[string]mongodoc.IntBool
	err = bson.Unmarshal(b, &result)
	c.Assert(err, gc.ErrorMatches, "cannot unmarshal value: BSON kind 0x02 isn't compatible with type int")
}

func (s *DocSuite) TestIntBoolSetBSONInvalidValue(c *gc.C) {
	test := bson.D{{"test", 2}}
	b, err := bson.Marshal(test)
	c.Assert(err, gc.IsNil)
	var result map[string]mongodoc.IntBool
	err = bson.Unmarshal(b, &result)
	c.Assert(err, gc.ErrorMatches, `invalid value 2`)
}

var preferredURLTests = []struct {
	entity         *mongodoc.Entity
	usePromulgated bool
	expectURLFalse string
	expectURLTrue  string
}{{
	entity: &mongodoc.Entity{
		URL: charm.MustParseURL("~ken/trusty/b-1"),
	},
	expectURLFalse: "cs:~ken/trusty/b-1",
	expectURLTrue:  "cs:~ken/trusty/b-1",
}, {
	entity: &mongodoc.Entity{
		URL:            charm.MustParseURL("~dmr/trusty/c-1"),
		PromulgatedURL: charm.MustParseURL("trusty/c-2"),
	},
	expectURLFalse: "cs:~dmr/trusty/c-1",
	expectURLTrue:  "cs:trusty/c-2",
}, {
	entity: &mongodoc.Entity{
		URL:            charm.MustParseURL("~dmr/trusty/c-1"),
		PromulgatedURL: charm.MustParseURL("trusty/c-2"),
		Development:    true,
	},
	expectURLFalse: "cs:~dmr/development/trusty/c-1",
	expectURLTrue:  "cs:development/trusty/c-2",
}, {
	entity: &mongodoc.Entity{
		URL:         charm.MustParseURL("~dmr/trusty/c-1"),
		Development: true,
	},
	expectURLFalse: "cs:~dmr/development/trusty/c-1",
	expectURLTrue:  "cs:~dmr/development/trusty/c-1",
}}

func (s *DocSuite) TestPreferredURL(c *gc.C) {
	for i, test := range preferredURLTests {
		c.Logf("test %d: %#v", i, test.entity)
		c.Assert(test.entity.PreferredURL(false).String(), gc.Equals, test.expectURLFalse)
		c.Assert(test.entity.PreferredURL(true).String(), gc.Equals, test.expectURLTrue)
		// Ensure no aliasing
		test.entity.PreferredURL(false).Series = "foo"
		c.Assert(test.entity.PreferredURL(false).Series, gc.Not(gc.Equals), "foo")
	}
}
