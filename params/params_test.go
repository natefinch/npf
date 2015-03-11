// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package params_test

import (
	"encoding/json"
	"net/textproto"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/macaroon-bakery.v0/httpbakery"

	"gopkg.in/juju/charmstore.v4/params"
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

func (*suite) TestBakeryErrorCompatibility(c *gc.C) {
	err1 := httpbakery.Error{
		Code:    httpbakery.ErrBadRequest,
		Message: "some request",
	}
	err2 := params.Error{
		Code:    params.ErrBadRequest,
		Message: "some request",
	}
	data1, err := json.Marshal(err1)
	c.Assert(err, gc.IsNil)
	c.Assert(string(data1), jc.JSONEquals, err2)
}
