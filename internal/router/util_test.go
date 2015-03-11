// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package router_test

import (
	"net/url"

	gc "gopkg.in/check.v1"

	"gopkg.in/juju/charmstore.v4/internal/router"
)

type utilSuite struct{}

var _ = gc.Suite(&utilSuite{})
var relativeURLTests = []struct {
	base        string
	target      string
	expect      string
	expectError string
}{{
	expectError: "non-absolute base URL",
}, {
	base:        "/foo",
	expectError: "non-absolute target URL",
}, {
	base:        "foo",
	expectError: "non-absolute base URL",
}, {
	base:        "/foo",
	target:      "foo",
	expectError: "non-absolute target URL",
}, {
	base:   "/foo",
	target: "/bar",
	expect: "bar",
}, {
	base:   "/foo/",
	target: "/bar",
	expect: "../bar",
}, {
	base:   "/foo/",
	target: "/bar/",
	expect: "../bar/",
}, {
	base:   "/foo/bar",
	target: "/bar/",
	expect: "../bar/",
}, {
	base:   "/foo/bar/",
	target: "/bar/",
	expect: "../../bar/",
}, {
	base:   "/foo/bar/baz",
	target: "/foo/targ",
	expect: "../targ",
}, {
	base:   "/foo/bar/baz/frob",
	target: "/foo/bar/one/two/",
	expect: "../one/two/",
}, {
	base:   "/foo/bar/baz/",
	target: "/foo/targ",
	expect: "../../targ",
}, {
	base:   "/foo/bar/baz/frob/",
	target: "/foo/bar/one/two/",
	expect: "../../one/two/",
}, {
	base:   "/foo/bar",
	target: "/foot/bar",
	expect: "../foot/bar",
}, {
	base:   "/foo/bar/baz/frob",
	target: "/foo/bar",
	expect: "../../bar",
}, {
	base:   "/foo/bar/baz/frob/",
	target: "/foo/bar",
	expect: "../../../bar",
}, {
	base:   "/foo/bar/baz/frob/",
	target: "/foo/bar/",
	expect: "../../",
}, {
	base:   "/foo/bar/baz",
	target: "/foo/bar/other",
	expect: "other",
}, {
	base:   "/foo/bar/",
	target: "/foo/bar/",
	expect: "",
}, {
	base:   "/foo/bar",
	target: "/foo/bar",
	expect: "bar",
}, {
	base:   "/foo/bar/",
	target: "/foo/bar/",
	expect: "",
}}

func (*utilSuite) TestRelativeURL(c *gc.C) {
	for i, test := range relativeURLTests {
		c.Logf("test %d: %q %q", i, test.base, test.target)
		// Sanity check the test itself.
		if test.expectError == "" {
			baseURL := &url.URL{Path: test.base}
			expectURL := &url.URL{Path: test.expect}
			targetURL := baseURL.ResolveReference(expectURL)
			c.Check(targetURL.Path, gc.Equals, test.target, gc.Commentf("resolve reference failure"))
		}

		result, err := router.RelativeURLPath(test.base, test.target)
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			c.Assert(result, gc.Equals, "")
		} else {
			c.Assert(err, gc.IsNil)
			c.Check(result, gc.Equals, test.expect)
		}
	}
}
