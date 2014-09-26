// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package elasticsearch

import (
	"testing"

	jujutesting "github.com/juju/testing"
	gc "gopkg.in/check.v1"
)

func TestPackage(t *testing.T) {
	ElasticSearchTestPackage(t)
}

type IsolatedElasticSearchSuite struct {
	jujutesting.IsolationSuite
	ElasticSearchSuite
}

func (s *IsolatedElasticSearchSuite) SetUpSuite(c *gc.C) {
	s.IsolationSuite.SetUpSuite(c)
	s.ElasticSearchSuite.SetUpSuite(c)
}
func (s *IsolatedElasticSearchSuite) TearDownSuite(c *gc.C) {
	s.ElasticSearchSuite.TearDownSuite(c)
	s.IsolationSuite.TearDownSuite(c)
}
func (s *IsolatedElasticSearchSuite) SetUpTest(c *gc.C) {
	s.IsolationSuite.SetUpTest(c)
	s.ElasticSearchSuite.SetUpTest(c)
}
func (s *IsolatedElasticSearchSuite) TearDownTest(c *gc.C) {
	s.ElasticSearchSuite.TearDownTest(c)
	s.IsolationSuite.TearDownTest(c)
}

var _ = gc.Suite(&IsolatedElasticSearchSuite{})

func (s *IsolatedElasticSearchSuite) TestSuccessfulPostDocument(c *gc.C) {
	doc := map[string]string{
		"a": "b",
	}
	err := s.db.PostDocument("testindex", "testtype", doc)
	c.Assert(err, gc.IsNil)
}

func (s *IsolatedElasticSearchSuite) TestDelete(c *gc.C) {
	doc := map[string]string{
		"a": "b",
	}
	s.db.PostDocument("testindex", "testtype", doc)
	err := s.db.DeleteIndex("testindex")
	c.Assert(err, gc.IsNil)
}

func (s *IsolatedElasticSearchSuite) TestDeleteErrorOnNonExistingIndex(c *gc.C) {
	err := s.db.DeleteIndex("nope")
	c.Assert(err, gc.NotNil)
	//CHECK THE ERORR
}

func (s *IsolatedElasticSearchSuite) TestIndexesEmpty(c *gc.C) {
	indexes, _ := s.db.CatIndices()
	//CHECK THE ERORR
	c.Assert(indexes, gc.HasLen, 0)
}

func (s *IsolatedElasticSearchSuite) TestIndexesCreatedAutomatically(c *gc.C) {
	doc := map[string]string{"a": "b"}
	s.db.PostDocument("testindex", "testtype", doc)
	indexes, _ := s.db.CatIndices()
	c.Assert(indexes[0], gc.Equals, "testindex")
}
