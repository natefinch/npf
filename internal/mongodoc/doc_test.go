// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package mongodoc_test

import (
	"testing"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v4"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/internal/mongodoc"
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

func (s *DocSuite) TestPreferredURL(c *gc.C) {
	e1 := &mongodoc.Entity{
		URL: charm.MustParseReference("~ken/trusty/b-1"),
	}
	e2 := &mongodoc.Entity{
		URL:            charm.MustParseReference("~dmr/trusty/c-1"),
		PromulgatedURL: charm.MustParseReference("trusty/c-1"),
	}

	c.Assert(e1.PreferredURL(false), gc.Equals, e1.URL)
	c.Assert(e1.PreferredURL(true), gc.Equals, e1.URL)
	c.Assert(e2.PreferredURL(false), gc.Equals, e2.URL)
	c.Assert(e2.PreferredURL(true), gc.Equals, e2.PromulgatedURL)
}
