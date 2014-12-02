// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package elasticsearch_test

import (
	"testing"
	"time"

	jujutesting "github.com/juju/testing"
	"github.com/juju/utils"
	gc "gopkg.in/check.v1"

	es "github.com/juju/charmstore/internal/elasticsearch"
	"github.com/juju/charmstore/internal/storetesting"
)

func TestPackage(t *testing.T) {
	gc.TestingT(t)
}

type Suite struct {
	jujutesting.IsolationSuite
	storetesting.ElasticSearchSuite
	Indexes   []string
	TestIndex string
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
	s.TestIndex = s.NewIndex(c)
	err := s.ES.PutIndex(s.TestIndex, map[string]interface{}{"settings": map[string]interface{}{"number_of_shards": 1}})
	c.Assert(err, gc.Equals, nil)
	err = s.ES.PutDocument(s.TestIndex, "testtype", s.TestIndex, struct{}{})
	c.Assert(err, gc.Equals, nil)
	err = s.ES.RefreshIndex(s.TestIndex)
	c.Assert(err, gc.Equals, nil)
}
func (s *Suite) TearDownTest(c *gc.C) {
	for _, i := range s.Indexes {
		s.ES.DeleteIndex(i)
	}
	s.ElasticSearchSuite.TearDownTest(c)
	s.IsolationSuite.TearDownTest(c)
}

func (s *Suite) NewIndex(c *gc.C) string {
	uuid, err := utils.NewUUID()
	c.Assert(err, gc.Equals, nil)
	idx := time.Now().Format("20060102150405") + "-" + uuid.String()
	s.Indexes = append(s.Indexes, idx)
	return idx
}

var _ = gc.Suite(&Suite{})

func (s *Suite) TestSuccessfulPostDocument(c *gc.C) {
	doc := map[string]string{
		"a": "b",
	}
	id, err := s.ES.PostDocument(s.TestIndex, "testtype", doc)
	c.Assert(err, gc.IsNil)
	c.Assert(id, gc.NotNil)
	var result map[string]string
	err = s.ES.GetDocument(s.TestIndex, "testtype", id, &result)
	c.Assert(err, gc.IsNil)
}

func (s *Suite) TestSuccessfulPutNewDocument(c *gc.C) {
	doc := map[string]string{
		"a": "b",
	}
	// Show that no document with this id exists.
	exists, err := s.ES.HasDocument(s.TestIndex, "testtype", "a")
	c.Assert(err, gc.IsNil)
	c.Assert(exists, gc.Equals, false)
	err = s.ES.PutDocument(s.TestIndex, "testtype", "a", doc)
	c.Assert(err, gc.IsNil)
	var result map[string]string
	err = s.ES.GetDocument(s.TestIndex, "testtype", "a", &result)
	c.Assert(result["a"], gc.Equals, "b")
	exists, err = s.ES.HasDocument(s.TestIndex, "testtype", "a")
	c.Assert(err, gc.IsNil)
	c.Assert(exists, gc.Equals, true)
}

func (s *Suite) TestSuccessfulPutUpdatedDocument(c *gc.C) {
	doc := map[string]string{
		"a": "b",
	}
	err := s.ES.PutDocument(s.TestIndex, "testtype", "a", doc)
	c.Assert(err, gc.IsNil)
	doc["a"] = "c"
	err = s.ES.PutDocument(s.TestIndex, "testtype", "a", doc)
	c.Assert(err, gc.IsNil)
	var result map[string]string
	err = s.ES.GetDocument(s.TestIndex, "testtype", "a", &result)
	c.Assert(result["a"], gc.Equals, "c")
}

func (s *Suite) TestPutVersionWithTypeNewDocument(c *gc.C) {
	doc := map[string]string{
		"a": "b",
	}
	// Show that no document with this id exists.
	exists, err := s.ES.HasDocument(s.TestIndex, "testtype", "a")
	c.Assert(err, gc.IsNil)
	c.Assert(exists, gc.Equals, false)
	err = s.ES.PutDocumentVersionWithType(s.TestIndex, "testtype", "a", 1, es.ExternalGTE, doc)
	c.Assert(err, gc.IsNil)
	var result map[string]string
	err = s.ES.GetDocument(s.TestIndex, "testtype", "a", &result)
	c.Assert(result["a"], gc.Equals, "b")
	exists, err = s.ES.HasDocument(s.TestIndex, "testtype", "a")
	c.Assert(err, gc.IsNil)
	c.Assert(exists, gc.Equals, true)
}

func (s *Suite) TestPutVersionWithTypeUpdateCurrentDocumentVersion(c *gc.C) {
	doc := map[string]string{
		"a": "b",
	}
	// Show that no document with this id exists.
	exists, err := s.ES.HasDocument(s.TestIndex, "testtype", "a")
	c.Assert(err, gc.IsNil)
	c.Assert(exists, gc.Equals, false)
	err = s.ES.PutDocumentVersionWithType(s.TestIndex, "testtype", "a", 1, es.ExternalGTE, doc)
	c.Assert(err, gc.IsNil)
	doc["a"] = "c"
	err = s.ES.PutDocumentVersionWithType(s.TestIndex, "testtype", "a", 1, es.ExternalGTE, doc)
	c.Assert(err, gc.IsNil)
	var result map[string]string
	err = s.ES.GetDocument(s.TestIndex, "testtype", "a", &result)
	c.Assert(result["a"], gc.Equals, "c")
	exists, err = s.ES.HasDocument(s.TestIndex, "testtype", "a")
	c.Assert(err, gc.IsNil)
	c.Assert(exists, gc.Equals, true)
}

func (s *Suite) TestPutVersionWithTypeUpdateLaterDocumentVersion(c *gc.C) {
	doc := map[string]string{
		"a": "b",
	}
	// Show that no document with this id exists.
	exists, err := s.ES.HasDocument(s.TestIndex, "testtype", "a")
	c.Assert(err, gc.IsNil)
	c.Assert(exists, gc.Equals, false)
	err = s.ES.PutDocumentVersionWithType(s.TestIndex, "testtype", "a", 1, es.ExternalGTE, doc)
	c.Assert(err, gc.IsNil)
	doc["a"] = "c"
	err = s.ES.PutDocumentVersionWithType(s.TestIndex, "testtype", "a", 3, es.ExternalGTE, doc)
	c.Assert(err, gc.IsNil)
	var result map[string]string
	err = s.ES.GetDocument(s.TestIndex, "testtype", "a", &result)
	c.Assert(result["a"], gc.Equals, "c")
	exists, err = s.ES.HasDocument(s.TestIndex, "testtype", "a")
	c.Assert(err, gc.IsNil)
	c.Assert(exists, gc.Equals, true)
}

func (s *Suite) TestPutVersionWithTypeUpdateEarlierDocumentVersion(c *gc.C) {
	doc := map[string]string{
		"a": "b",
	}
	// Show that no document with this id exists.
	exists, err := s.ES.HasDocument(s.TestIndex, "testtype", "a")
	c.Assert(err, gc.IsNil)
	c.Assert(exists, gc.Equals, false)
	err = s.ES.PutDocumentVersionWithType(s.TestIndex, "testtype", "a", 3, es.ExternalGTE, doc)
	c.Assert(err, gc.IsNil)
	doc["a"] = "c"
	err = s.ES.PutDocumentVersionWithType(s.TestIndex, "testtype", "a", 1, es.ExternalGTE, doc)
	c.Assert(err, gc.Equals, es.ErrConflict)
	var result map[string]string
	err = s.ES.GetDocument(s.TestIndex, "testtype", "a", &result)
	c.Assert(result["a"], gc.Equals, "b")
	exists, err = s.ES.HasDocument(s.TestIndex, "testtype", "a")
	c.Assert(err, gc.IsNil)
	c.Assert(exists, gc.Equals, true)
}

func (s *Suite) TestDelete(c *gc.C) {
	doc := map[string]string{
		"a": "b",
	}
	_, err := s.ES.PostDocument(s.TestIndex, "testtype", doc)
	c.Assert(err, gc.IsNil)
	err = s.ES.DeleteIndex(s.TestIndex)
	c.Assert(err, gc.IsNil)
}

func (s *Suite) TestDeleteErrorOnNonExistingIndex(c *gc.C) {
	err := s.ES.DeleteIndex("nope")
	c.Assert(err, gc.NotNil)
	c.Assert(err.Error(), gc.Equals, "elasticsearch document not found")
}

func (s *Suite) TestIndexesCreatedAutomatically(c *gc.C) {
	doc := map[string]string{"a": "b"}
	_, err := s.ES.PostDocument(s.TestIndex, "testtype", doc)
	c.Assert(err, gc.IsNil)
	indexes, err := s.ES.ListAllIndexes()
	c.Assert(err, gc.IsNil)
	c.Assert(indexes, gc.Not(gc.HasLen), 0)
	found := false
	for _, index2 := range indexes {
		if index2 == s.TestIndex {
			found = true
		}
	}
	c.Assert(found, gc.Equals, true)
}

func (s *Suite) TestSearch(c *gc.C) {
	doc := map[string]string{"foo": "bar"}
	_, err := s.ES.PostDocument(s.TestIndex, "testtype", doc)
	c.Assert(err, gc.IsNil)
	doc["foo"] = "baz"
	id2, err := s.ES.PostDocument(s.TestIndex, "testtype", doc)
	c.Assert(err, gc.IsNil)
	s.ES.RefreshIndex(s.TestIndex)
	q := es.QueryDSL{
		Query:  es.TermQuery{Field: "foo", Value: "baz"},
		Fields: []string{"foo"},
	}
	results, err := s.ES.Search(s.TestIndex, "testtype", q)
	c.Assert(err, gc.IsNil)
	c.Assert(results.Hits.Total, gc.Equals, 1)
	c.Assert(results.Hits.Hits[0].ID, gc.Equals, id2)
	c.Assert(results.Hits.Hits[0].Fields.GetString("foo"), gc.Equals, "baz")
}

func (s *Suite) TestPutMapping(c *gc.C) {
	var mapping = map[string]interface{}{
		"testtype": map[string]interface{}{
			"properties": map[string]interface{}{
				"foo": map[string]interface{}{
					"stored": true,
					"type":   "string",
				},
			},
		},
	}
	err := s.ES.PutMapping(s.TestIndex, "testtype", mapping)
	c.Assert(err, gc.IsNil)
}

func (s *Suite) TestEscapeRegexp(c *gc.C) {
	var tests = []struct {
		about    string
		original string
		expected string
	}{{
		about:    `plain string`,
		original: `foo`,
		expected: `foo`,
	}, {
		about:    `escape .`,
		original: `foo.bar`,
		expected: `foo\.bar`,
	}, {
		about:    `escape ?`,
		original: `foo?bar`,
		expected: `foo\?bar`,
	}, {
		about:    `escape +`,
		original: `foo+bar`,
		expected: `foo\+bar`,
	}, {
		about:    `escape *`,
		original: `foo*bar`,
		expected: `foo\*bar`,
	}, {
		about:    `escape |`,
		original: `foo|bar`,
		expected: `foo\|bar`,
	}, {
		about:    `escape {`,
		original: `foo{bar`,
		expected: `foo\{bar`,
	}, {
		about:    `escape }`,
		original: `foo}bar`,
		expected: `foo\}bar`,
	}, {
		about:    `escape [`,
		original: `foo[bar`,
		expected: `foo\[bar`,
	}, {
		about:    `escape ]`,
		original: `foo]bar`,
		expected: `foo\]bar`,
	}, {
		about:    `escape (`,
		original: `foo(bar`,
		expected: `foo\(bar`,
	}, {
		about:    `escape )`,
		original: `foo)bar`,
		expected: `foo\)bar`,
	}, {
		about:    `escape "`,
		original: `foo"bar`,
		expected: `foo\"bar`,
	}, {
		about:    `escape \`,
		original: `foo\bar`,
		expected: `foo\\bar`,
	}, {
		about:    `escape #`,
		original: `foo#bar`,
		expected: `foo\#bar`,
	}, {
		about:    `escape @`,
		original: `foo@bar`,
		expected: `foo\@bar`,
	}, {
		about:    `escape &`,
		original: `foo&bar`,
		expected: `foo\&bar`,
	}, {
		about:    `escape <`,
		original: `foo<bar`,
		expected: `foo\<bar`,
	}, {
		about:    `escape >`,
		original: `foo>bar`,
		expected: `foo\>bar`,
	}, {
		about:    `escape ~`,
		original: `foo~bar`,
		expected: `foo\~bar`,
	}, {
		about:    `escape start`,
		original: `*foo`,
		expected: `\*foo`,
	}, {
		about:    `escape end`,
		original: `foo\`,
		expected: `foo\\`,
	}, {
		about:    `escape many`,
		original: `\"*\`,
		expected: `\\\"\*\\`,
	}}
	for i, test := range tests {
		c.Logf("%d: %s", i, test.about)
		c.Assert(es.EscapeRegexp(test.original), gc.Equals, test.expected)
	}
}

func (s *Suite) TestAlias(c *gc.C) {
	uuid, err := utils.NewUUID()
	c.Assert(err, gc.Equals, nil)
	alias := uuid.String()
	index1 := alias + "-1"
	index2 := alias + "-2"

	// Create first index
	err = s.ES.PutIndex(index1, struct{}{})
	c.Assert(err, gc.Equals, nil)
	defer s.ES.DeleteIndex(index1)

	// Create second index
	err = s.ES.PutIndex(index2, struct{}{})
	c.Assert(err, gc.Equals, nil)
	defer s.ES.DeleteIndex(index2)

	// Check alias is not aliased to anything
	indexes, err := s.ES.ListIndexesForAlias(alias)
	c.Assert(err, gc.Equals, nil)
	c.Assert(indexes, gc.HasLen, 0)

	// Associate alias with index 1
	err = s.ES.Alias(index1, alias)
	c.Assert(err, gc.Equals, nil)
	indexes, err = s.ES.ListIndexesForAlias(alias)
	c.Assert(err, gc.Equals, nil)
	c.Assert(indexes, gc.HasLen, 1)
	c.Assert(indexes[0], gc.Equals, index1)

	// Associate alias with index 2, removing it from index 1
	err = s.ES.Alias(index2, alias)
	c.Assert(err, gc.Equals, nil)
	indexes, err = s.ES.ListIndexesForAlias(alias)
	c.Assert(err, gc.Equals, nil)
	c.Assert(indexes, gc.HasLen, 1)
	c.Assert(indexes[0], gc.Equals, index2)
}
