// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package params_test

import (
	"encoding/json"

	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"labix.org/v2/mgo/bson"
	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore/params"
)

type urlSuite struct {
	testing.IsolationSuite
	charmURL *params.CharmURL
}

var _ = gc.Suite(&urlSuite{})

type urlDoc struct {
	URL *params.CharmURL
}

var urlCodecs = []struct {
	name      string
	data      string
	marshal   func(interface{}) ([]byte, error)
	unmarshal func([]byte, interface{}) error
}{{
	name:      "bson",
	data:      "%\x00\x00\x00\x02url\x00\x17\x00\x00\x00cs:trusty/wordpress-42\x00\x00",
	marshal:   bson.Marshal,
	unmarshal: bson.Unmarshal,
}, {
	name:      "json",
	data:      `{"URL":"cs:trusty/wordpress-42"}`,
	marshal:   json.Marshal,
	unmarshal: json.Unmarshal,
}}

func (s *urlSuite) SetUpSuite(c *gc.C) {
	s.IsolationSuite.SetUpSuite(c)
	charmURL, err := params.ParseURL("cs:trusty/wordpress-42")
	c.Assert(err, gc.IsNil)
	s.charmURL = charmURL
}

func (s *urlSuite) TestMarshal(c *gc.C) {
	for _, codec := range urlCodecs {
		c.Logf("codec %s", codec.name)
		serialized, err := codec.marshal(urlDoc{s.charmURL})
		c.Assert(err, gc.IsNil)
		c.Assert(string(serialized), gc.Equals, codec.data)
	}
}

func (s *urlSuite) TestUnmarshal(c *gc.C) {
	for _, codec := range urlCodecs {
		c.Logf("codec %s", codec.name)
		var doc urlDoc
		err := codec.unmarshal([]byte(codec.data), &doc)
		c.Assert(err, gc.IsNil)
		c.Assert(doc.URL, jc.DeepEquals, s.charmURL)
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
