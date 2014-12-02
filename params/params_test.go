// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package params_test

import (
	"net/textproto"

	gc "gopkg.in/check.v1"

	"github.com/juju/charmstore/params"
)

type suite struct{}

var _ = gc.Suite(&suite{})

func (*suite) TestContentHashHeaderCanonicalized(c *gc.C) {
	// The header key should be canonicalized, because otherwise
	// the actually produced header will be different from that
	// specified.
	canon := textproto.CanonicalMIMEHeaderKey(params.ContentHashHeader)
	c.Assert(canon, gc.Equals, params.ContentHashHeader)
}
