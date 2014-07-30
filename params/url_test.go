// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package params_test

import (
	"encoding/json"

	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"gopkg.in/juju/charm.v2"
	"gopkg.in/mgo.v2/bson"
	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore/params"
)

type urlSuite struct {
	testing.IsolationSuite
}

var _ = gc.Suite(&urlSuite{})

type urlDoc struct {
	URL *params.CharmURL `json:"url" bson:"url"`
}

var marshalTests = []struct {
	name      string
	url       string
	data      map[string]interface{}
	marshal   func(interface{}) ([]byte, error)
	unmarshal func([]byte, interface{}) error
}{{
	name:      "bson",
	url:       "cs:trusty/wordpress-42",
	data:      map[string]interface{}{"url": "cs:trusty/wordpress-42"},
	marshal:   bson.Marshal,
	unmarshal: bson.Unmarshal,
}, {
	name:      "json",
	url:       "cs:trusty/wordpress-42",
	data:      map[string]interface{}{"url": "cs:trusty/wordpress-42"},
	marshal:   json.Marshal,
	unmarshal: json.Unmarshal,
}, {
	name:      "bson no series",
	url:       "django",
	data:      map[string]interface{}{"url": "cs:django"},
	marshal:   bson.Marshal,
	unmarshal: bson.Unmarshal,
}, {
	name:      "json no series",
	url:       "django",
	data:      map[string]interface{}{"url": "cs:django"},
	marshal:   json.Marshal,
	unmarshal: json.Unmarshal,
}}

func (s *urlSuite) TestMarshal(c *gc.C) {
	for i, test := range marshalTests {
		c.Logf("%d: codec %s", i, test.name)

		charmURL, err := params.ParseURL(test.url)
		c.Assert(err, gc.IsNil)

		// Check serialization.
		serialized, err := test.marshal(urlDoc{charmURL})
		c.Assert(err, gc.IsNil)

		// Check de-serialization.
		var gotData map[string]interface{}
		err = test.unmarshal(serialized, &gotData)
		c.Assert(err, gc.IsNil)
		c.Assert(gotData, jc.DeepEquals, test.data)
	}
}

func (s *urlSuite) TestParseURLFull(c *gc.C) {
	url, err := params.ParseURL("cs:precise/django-42")
	c.Assert(err, gc.IsNil)
	c.Assert(url.Schema, gc.Equals, "cs")
	c.Assert(url.Series, gc.Equals, "precise")
	c.Assert(url.Name, gc.Equals, "django")
	c.Assert(url.Revision, gc.Equals, 42)
}

func (s *urlSuite) TestParseURLPartial(c *gc.C) {
	url, err := params.ParseURL("wordpress")
	c.Assert(err, gc.IsNil)
	c.Assert(url.Schema, gc.Equals, "cs")
	c.Assert(url.Series, gc.Equals, "")
	c.Assert(url.Name, gc.Equals, "wordpress")
	c.Assert(url.Revision, gc.Equals, -1)
}

func (s *urlSuite) TestParseURLError(c *gc.C) {
	url, err := params.ParseURL("bad:wordpress")
	c.Assert(url, gc.IsNil)
	c.Assert(err, gc.ErrorMatches, `charm URL has invalid schema: "bad:wordpress"`)
}

func (s *urlSuite) TestIsBundle(c *gc.C) {
	c.Assert(params.IsBundle(charm.MustParseURL("cs:trusty/wordpress-42")), jc.IsFalse)
	c.Assert(params.IsBundle(charm.MustParseURL("cs:bundle/wordpress-simple-2")), jc.IsTrue)
}
