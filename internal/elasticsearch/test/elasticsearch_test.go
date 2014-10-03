// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package elasticsearch_test

import (
	"testing"

	"github.com/juju/charmstore/internal/elasticsearch"
	"github.com/juju/charmstore/internal/storetesting"
	jujutesting "github.com/juju/testing"
	gc "gopkg.in/check.v1"
)

func TestPackage(t *testing.T) {
	storetesting.ElasticSearchTestPackage(t, nil)
}

type Suite struct {
	jujutesting.IsolationSuite
	storetesting.ElasticSearchSuite
}

func (s *Suite) SetUpSuite(c *gc.C) {
	s.IsolationSuite.SetUpSuite(c)
	s.ElasticSearchSuite.SetUpSuite(c)
}
func (s *Suite) TearDownSuite(c *gc.C) {
	s.ElasticSearchSuite.TearDownSuite(c)
	s.IsolationSuite.TearDownSuite(c)
}
func (s *Suite) SetUpTest(c *gc.C) {
	s.IsolationSuite.SetUpTest(c)
	s.ElasticSearchSuite.SetUpTest(c)
}
func (s *Suite) TearDownTest(c *gc.C) {
	s.ElasticSearchSuite.TearDownTest(c)
	s.IsolationSuite.TearDownTest(c)
}

var _ = gc.Suite(&Suite{})

// checkTestIndex makes sure that testindex does not already exist
// in the elasticsearch database. That way we won't overwrite any
// important data.
func (s *Suite) checkTestIndex(c *gc.C) {
	indexes, err := s.ES.ListAllIndexes()
	c.Assert(err, gc.IsNil)
	for _, index := range indexes {
		c.Assert(index, gc.Not(gc.Equals), "testindex")
	}
	s.Remove = append(s.Remove, "testindex")
}

func (s *Suite) TestSuccessfulPostDocument(c *gc.C) {
	s.checkTestIndex(c)
	doc := map[string]string{
		"a": "b",
	}
	id, err := s.ES.PostDocument("testindex", "testtype", doc)
	c.Assert(err, gc.IsNil)
	c.Assert(id, gc.NotNil)
	var result map[string]string
	err = s.ES.GetDocument("testindex", "testtype", id, &result)
	c.Assert(err, gc.IsNil)
}

func (s *Suite) TestSuccessfulPutNewDocument(c *gc.C) {
	s.checkTestIndex(c)
	doc := map[string]string{
		"a": "b",
	}
	err := s.ES.PutDocument("testindex", "testtype", "a", doc)
	c.Assert(err, gc.IsNil)
	var result map[string]string
	err = s.ES.GetDocument("testindex", "testtype", "a", &result)
	c.Assert(result["a"], gc.Equals, "b")
}

func (s *Suite) TestSuccessfulPutUpdatedDocument(c *gc.C) {
	s.checkTestIndex(c)
	doc := map[string]string{
		"a": "b",
	}
	err := s.ES.PutDocument("testindex", "testtype", "a", doc)
	c.Assert(err, gc.IsNil)
	doc["a"] = "c"
	err = s.ES.PutDocument("testindex", "testtype", "a", doc)
	c.Assert(err, gc.IsNil)
	var result map[string]string
	err = s.ES.GetDocument("testindex", "testtype", "a", &result)
	c.Assert(result["a"], gc.Equals, "c")
}

func (s *Suite) TestDelete(c *gc.C) {
	s.checkTestIndex(c)
	doc := map[string]string{
		"a": "b",
	}
	_, err := s.ES.PostDocument("testindex", "testtype", doc)
	c.Assert(err, gc.IsNil)
	err = s.ES.DeleteIndex("testindex")
	c.Assert(err, gc.IsNil)
}

func (s *Suite) TestDeleteErrorOnNonExistingIndex(c *gc.C) {
	s.checkTestIndex(c)
	err := s.ES.DeleteIndex("nope")
	terr := err.(*elasticsearch.ErrNotFound)
	c.Assert(terr.Message(), gc.Equals, "index nope not found")
}

func (s *Suite) TestIndexesCreatedAutomatically(c *gc.C) {
	s.checkTestIndex(c)
	doc := map[string]string{"a": "b"}
	_, err := s.ES.PostDocument("testindex", "testtype", doc)
	c.Assert(err, gc.IsNil)
	indexes, err := s.ES.ListAllIndexes()
	c.Assert(err, gc.IsNil)
	c.Assert(indexes, gc.Not(gc.HasLen), 0)
	found := false
	for _, index := range indexes {
		if index == "testindex" {
			found = true
		}
	}
	c.Assert(found, gc.Equals, true)
}
