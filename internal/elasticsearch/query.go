// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package elasticsearch

import "encoding/json"

// Query represents a query in the elasticsearch DSL.
// Only one of the query types should be used at once.
type Query struct {
	Filtered *FilteredQuery `json:"filtered,omitempty"`
	Match    *MatchQuery    `json:"match,omitempty"`
	MatchAll *MatchAllQuery `json:"match_all,omitempty"`
	Prefix   *PrefixQuery   `json:"prefix,omitempty"`
}

// MatchAllQuery provides a query that matches all
// documents in the index.
type MatchAllQuery struct {
}

// MatchQuery provides a query that matches against
// a complete field.
type MatchQuery map[string]interface{}

// PrefixQuery provides a query that matches against
// the start of a field.
type PrefixQuery map[string]interface{}

// Filtered Query provides a query that can be
// subsequently filtered.
type FilteredQuery struct {
	Query  *Query  `json:"query,omitempty"`
	Filter *Filter `json:"filter,omitempty"`
}

// Filter represents a filter in the elasticsearch DSL.
type Filter struct {
	And  AndFilter   `json:"and,omitempty"`
	Or   OrFilter    `json:"or,omitempty"`
	Term *TermFilter `json:"term,omitempty"`
}

// AndFilter provides a filter that requires all of the internal
// filters to match.
type AndFilter []Filter

// OrFilter provides a filter that requires any of the internal
// filters to match.
type OrFilter []Filter

// TermFilter provides a filter that requires a field to match.
type TermFilter map[string]string

// QueryDSL provides a structure to put together a query using the
// elasticsearch DSL.
type QueryDSL struct {
	Size  int    `json:"size,omitempty"`
	Query Query  `json:"query,omitempty"`
	Sort  []Sort `json:"sort,omitempty"`
}

type Sort struct {
	Field string
	Order Order
}

type Order struct {
	Order string `json:"order"`
}

func (s Sort) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]Order{
		s.Field: {s.Order.Order},
	})
}
