// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4

import (
	"net/http"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/charmstore/internal/storetesting"
)

type SearchSuite struct {
	storetesting.IsolatedMgoESSuite
}

var _ = gc.Suite(&SearchSuite{})

func (s *SearchSuite) TestParseSearchParamsText(c *gc.C) {
	var req http.Request
	req.Form = map[string][]string{"text": {"test text"}}
	sp, err := parseSearchParams(&req)
	c.Assert(err, gc.IsNil)
	c.Assert(sp.Text, gc.Equals, "test text")
}

func (s *SearchSuite) TestParseSearchParamsAutocompete(c *gc.C) {
	var req http.Request
	req.Form = map[string][]string{"autocomplete": {"1"}}
	sp, err := parseSearchParams(&req)
	c.Assert(err, gc.IsNil)
	c.Assert(sp.AutoComplete, gc.Equals, true)
}

func (s *SearchSuite) TestParseSearchParamsFilters(c *gc.C) {
	var req http.Request
	req.Form = map[string][]string{
		"f1": {"f11", "f12"},
		"f2": {"f21"},
	}
	sp, err := parseSearchParams(&req)
	c.Assert(err, gc.IsNil)
	c.Assert(sp.Filters["f1"], jc.DeepEquals, []string{"f11", "f12"})
	c.Assert(sp.Filters["f2"], jc.DeepEquals, []string{"f21"})
}

func (s *SearchSuite) TestParseSearchParamsLimit(c *gc.C) {
	var req http.Request
	req.Form = map[string][]string{"limit": {"20"}}
	sp, err := parseSearchParams(&req)
	c.Assert(err, gc.IsNil)
	c.Assert(sp.Limit, gc.Equals, 20)
}

func (s *SearchSuite) TestParseSearchParamsInclude(c *gc.C) {
	var req http.Request
	req.Form = map[string][]string{"include": {"meta1", "meta2"}}
	sp, err := parseSearchParams(&req)
	c.Assert(err, gc.IsNil)
	c.Assert(sp.Include, jc.DeepEquals, []string{"meta1", "meta2"})
}
