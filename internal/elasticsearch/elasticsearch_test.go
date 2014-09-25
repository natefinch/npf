// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package elasticsearch

import (
	"testing"

	jujutesting "github.com/juju/testing"
	gc "launchpad.net/gocheck"
)

func TestPackage(t *testing.T) {
	gc.TestingT(t)
}

type ElasticSearchSuite struct {
	jujutesting.IsolationSuite
	db Database
}

var _ = gc.Suite(&ElasticSearchSuite{
	db: Database{
		server: "127.0.0.1",
		port:   9200,
		index:  "fakeindex",
	},
})

func (s *ElasticSearchSuite) TestSuccessfulAdd(c *gc.C) {
	doc := map[string]string{
		"a": "b",
	}
	err := s.db.AddNewEntity(doc)
	c.Assert(err, gc.IsNil)
}
