// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4_test // import "gopkg.in/juju/charmstore.v5-unstable/internal/v4"

import (
	"strings"

	gc "gopkg.in/check.v1"

	"gopkg.in/juju/charmstore.v5-unstable/internal/v4"
)

type iconSuite struct{}

var _ = gc.Suite(&iconSuite{})

func (s *iconSuite) TestValidXML(c *gc.C) {
	// The XML declaration must be included in the first line of the icon.
	hasXMLPrefix := strings.HasPrefix(v4.DefaultIcon, "<?xml")
	c.Assert(hasXMLPrefix, gc.Equals, true)
}
