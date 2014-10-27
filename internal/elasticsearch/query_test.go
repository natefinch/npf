// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package elasticsearch_test

import (
	gc "gopkg.in/check.v1"

	. "github.com/juju/charmstore/internal/elasticsearch"
	"github.com/juju/charmstore/internal/storetesting"
)

type QuerySuite struct{}

var _ = gc.Suite(&QuerySuite{})

func (s *QuerySuite) TestJSONEncodings(c *gc.C) {
	var tests = []struct {
		about string
		query interface{}
		json  string
	}{{
		about: "term query",
		query: TermQuery{Field: "foo", Value: "bar"},
		json:  `{"term": {"foo": "bar"}}`,
	}, {
		about: "match all query",
		query: MatchAllQuery{},
		json:  `{"match_all": {}}`,
	}, {
		about: "match query",
		query: MatchQuery{Field: "foo", Query: "bar"},
		json:  `{"match": {"foo": {"query": "bar"}}}`,
	}, {
		about: "match query with type",
		query: MatchQuery{Field: "foo", Query: "bar", Type: "baz"},
		json:  `{"match": {"foo": {"query": "bar", "type": "baz"}}}`,
	}, {
		about: "multi match query",
		query: MultiMatchQuery{Query: "foo", Fields: []string{BoostField("bar", 2), "baz"}},
		json:  `{"multi_match": {"query": "foo", "fields": ["bar^2.000000", "baz"]}}`,
	}, {
		about: "filtered query",
		query: FilteredQuery{
			Query:  TermQuery{Field: "foo", Value: "bar"},
			Filter: TermFilter{Field: "baz", Value: "quz"}},
		json: `{"filtered": {"query": {"term": {"foo": "bar"}}, "filter": {"term": {"baz": "quz"}}}}`,
	}, {
		about: "function score query",
		query: FunctionScoreQuery{
			Query: TermQuery{Field: "foo", Value: "bar"},
			Functions: []Function{
				DecayFunction{
					Function: "baz",
					Field:    "foo",
					Scale:    "quz",
				},
			},
		},
		json: `{"function_score": {"query": {"term": {"foo": "bar"}}, "functions": [{"baz": {"foo":{"scale": "quz"}}}]}}`,
	}, {
		about: "term filter",
		query: TermFilter{Field: "foo", Value: "bar"},
		json:  `{"term": {"foo": "bar"}}`,
	}, {
		about: "and filter",
		query: AndFilter{
			TermFilter{Field: "foo", Value: "bar"},
			TermFilter{Field: "baz", Value: "quz"},
		},
		json: `{"and": {"filters": [{"term": {"foo": "bar"}}, {"term": {"baz": "quz"}}]}}`,
	}, {
		about: "or filter",
		query: OrFilter{
			TermFilter{Field: "foo", Value: "bar"},
			TermFilter{Field: "baz", Value: "quz"},
		},
		json: `{"or": {"filters": [{"term": {"foo": "bar"}}, {"term": {"baz": "quz"}}]}}`,
	}, {
		about: "not filter",
		query: NotFilter{TermFilter{Field: "foo", Value: "bar"}},
		json:  `{"not": {"term": {"foo": "bar"}}}`,
	}, {
		about: "query filter",
		query: QueryFilter{Query: TermQuery{Field: "foo", Value: "bar"}},
		json:  `{"query": {"term": {"foo": "bar"}}}`,
	}, {
		about: "regexp filter",
		query: RegexpFilter{Field: "foo", Regexp: ".*"},
		json:  `{"regexp": {"foo": ".*"}}`,
	}, {
		about: "query dsl",
		query: QueryDSL{
			Fields: []string{"foo", "bar"},
			Size:   10,
			Query:  TermQuery{Field: "baz", Value: "quz"},
			Sort:   []Sort{{Field: "foo", Order: Order{"desc"}}},
		},
		json: `{"fields": ["foo", "bar"], "size": 10, "query": {"term": {"baz": "quz"}}, "sort": [{"foo": { "order": "desc"}}]}`,
	}, {
		about: "decay function",
		query: DecayFunction{
			Function: "baz",
			Field:    "foo",
			Scale:    "quz",
		},
		json: `{"baz": {"foo":{"scale": "quz"}}}`,
	}, {
		about: "boost_factor function",
		query: BoostFactorFunction{
			BoostFactor: 1.5,
		},
		json: `{"boost_factor": 1.5}`,
	}, {
		about: "boost_factor function with filter",
		query: BoostFactorFunction{
			BoostFactor: 1.5,
			Filter: TermFilter{
				Field: "foo",
				Value: "bar",
			},
		},
		json: `{"filter": {"term": {"foo": "bar"}}, "boost_factor": 1.5}`,
	}, {
		about: "paginated query",
		query: QueryDSL{
			Fields: []string{"foo", "bar"},
			Size:   10,
			Query:  TermQuery{Field: "baz", Value: "quz"},
			Sort:   []Sort{{Field: "foo", Order: Order{"desc"}}},
			From:   10,
		},
		json: `{"fields": ["foo", "bar"], "size": 10, "query": {"term": {"baz": "quz"}}, "sort": [{"foo": { "order": "desc"}}], "from": 10}`,
	}}
	for i, test := range tests {
		c.Logf("%d: %s", i, test.about)
		// Note JSONEquals is being used a bit backwards here, this is fine
		// but any error results may be a little confusing.
		c.Assert([]byte(test.json), storetesting.JSONEquals, test.query)
	}
}
