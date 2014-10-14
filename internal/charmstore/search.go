// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore

import (
	"encoding/json"
	"net/url"

	"github.com/juju/errgo"

	"github.com/juju/charmstore/internal/elasticsearch"
	"github.com/juju/charmstore/internal/mongodoc"
)

// StoreElasticSearch provides strongly typed methods for accessing the
// elasticsearch database. These methods will not return errors if
// elasticsearch is not configured, allowing them to be safely called even if
// it is not enabled in this service.
type StoreElasticSearch struct {
	*elasticsearch.Database
	Index string
}

const typeName = "entity"

// Put inserts the mongodoc.Entity into elasticsearch if elasticsearch
// is configured.
func (ses *StoreElasticSearch) put(entity *mongodoc.Entity) error {
	if ses == nil || ses.Database == nil {
		return nil
	}
	return ses.PutDocument(ses.Index, typeName, ses.getID(entity), entity)
}

// getID returns the id to be used for indexing the entity into ElasticSearch.
func (ses *StoreElasticSearch) getID(entity *mongodoc.Entity) string {
	return url.QueryEscape(entity.URL.String())
}

// Search searches for entities. The query is a json string which conforms
// to the elasticsearch querydsl.
// http://www.elasticsearch.org/guide/en/elasticsearch/reference/current/query-dsl.html
// It returns a slice containing each Id of the matching documents returned by
// elasticsearch.
func (ses *StoreElasticSearch) search(query string) ([]string, error) {
	if ses == nil || ses.Database == nil {
		return nil, nil
	}
	results, err := ses.Database.Search(ses.Index, typeName, query)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(results.Hits.Hits))
	for _, hit := range results.Hits.Hits {
		ids = append(ids, hit.ID)
	}
	return ids, nil
	// TODO: return more than just ids e.g. total, hits, score, information, serach time
}

// ExportToElasticSearch reads all of the mongodoc Entities and writes
// them to elasticsearch
func (store *Store) ExportToElasticSearch() error {
	var result mongodoc.Entity
	iter := store.DB.Entities().Find(nil).Iter()
	defer iter.Close()
	for iter.Next(&result) {
		if err := store.ES.put(&result); err != nil {
			return errgo.Notef(err, "cannot index %s", result.URL)
		}
	}
	if err := iter.Close(); err != nil {
		return err
	}
	return nil
}

// SearchParams represents the search parameters used by the store.
type SearchParams struct {
	// The text to use in the full text search query.
	Text string
	// If autocomplete is specified, the search will return only charms and
	// bundles with a name that has text as a prefix.
	AutoComplete bool
	// Limit the search to items with attributes that match the specified filter value.
	Filters map[string][]string
	// Limit the number of returned items to the specified count.
	Limit int
}

// Search the store for the given SearchParams.
// Returns a slice containing each Id of the matching documents returned by
// elasticsearch.
func (store *Store) Search(sp SearchParams) ([]string, error) {
	query := createSearchDSL(sp)
	return store.ES.search(query)
}

// queryFields provides a map of fields to weighting to use with the
// elasticsearch query.
func queryFields(sp SearchParams) map[string]float64 {
	fields := map[string]float64{
		"CharmMeta.Description": 3,
	}
	if sp.AutoComplete {
		fields["CharmMeta.Name.ngrams"] = 10
	} else {
		fields["CharmMeta.Name"] = 10
	}
	return fields
}

// encodeFields takes a map of field name to weight and builds a slice of strings
// representing those weighted fields for a MultiMatchQuery.
func encodeFields(fields map[string]float64) []string {
	fs := make([]string, 0, len(fields))
	for k, v := range fields {
		fs = append(fs, elasticsearch.BoostField(k, v))
	}
	return fs
}

// createSearchDSL builds an elasticsearch query from the query parameters.
// http://www.elasticsearch.org/guide/en/elasticsearch/reference/current/query-dsl.html
func createSearchDSL(sp SearchParams) string {
	qdsl := elasticsearch.QueryDSL{
		Size: sp.Limit,
	}

	// Full text search
	var q elasticsearch.Query
	if sp.Text == "" {
		q = elasticsearch.MatchAllQuery{}
	} else {
		q = elasticsearch.MultiMatchQuery{
			Query:  sp.Text,
			Fields: encodeFields(queryFields(sp)),
		}
	}

	// Attenuation function
	q = elasticsearch.FunctionScoreQuery{
		Query: q,
		Functions: []elasticsearch.Function{
			{
				Function: "linear",
				Field:    "UploadTime",
				Scale:    "365d",
			},
		},
	}

	// Filters
	qdsl.Query = elasticsearch.FilteredQuery{
		Query:  q,
		Filter: createFilters(sp.Filters),
	}

	bytes, err := json.Marshal(qdsl)
	if err != nil {
		panic(err)
	}
	return string(bytes)
}

// createFilters converts the filters requested with the serch API into
// filters in the elasticsearch query DSL. Please see http://tinyurl.com/qzobc69
// for details of how filters are specified in the API. For each key in f a filter is
// created that matches any one of the set of values specified for that key.
// The created filter will only match when at least one of the requested values
// matches for all of the requested keys. createFilters will silently skip over any
// filter names that are not mapped.
func createFilters(f map[string][]string) elasticsearch.Filter {
	af := make(elasticsearch.AndFilter, 0, len(f))
	for k, vals := range f {
		filter, ok := filters[k]
		if !ok {
			continue
		}
		of := make(elasticsearch.OrFilter, 0, len(vals))
		for _, v := range vals {
			of = append(of, filter(v))
		}
		af = append(af, of)
	}
	return af
}

// filters contains a mapping from a filter parameter in the API to a
// function that will generate an elasticsearch query DSL filter for the
// given value.
var filters = map[string]func(string) elasticsearch.Filter{
	"description": descriptionFilter,
	"name":        termFilter("CharmMeta.Name"),
	"owner":       ownerFilter,
	"provides":    termFilter("CharmProvidedInterfaces"),
	"requires":    termFilter("CharmRequiredInterfaces"),
	"series":      seriesFilter,
	"summary":     summaryFilter,
	"tags":        tagsFilter,
	"type":        typeFilter,
}

// descriptionFilter generates a filter that will match against the
// description field of the charm data.
func descriptionFilter(value string) elasticsearch.Filter {
	return elasticsearch.QueryFilter{
		Query: elasticsearch.MatchQuery{
			Field: "CharmMeta.Description",
			Query: value,
			Type:  "phrase",
		},
	}
}

// ownerFilter generates a filter that will match against the
// owner taken from the URL.
func ownerFilter(value string) elasticsearch.Filter {
	return elasticsearch.RegexpFilter{
		Field:  "URL",
		Regexp: "cs:~" + value + "/.*",
	}
}

// seriesFilter generates a filter that will match against the
// series taken from the URL.
func seriesFilter(value string) elasticsearch.Filter {
	return elasticsearch.RegexpFilter{
		Field:  "URL",
		Regexp: "cs:(~[^/]*/)?" + value + "/.*",
	}
}

// summaryFilter generates a filter that will match against the
// summary field from the charm data.
func summaryFilter(value string) elasticsearch.Filter {
	return elasticsearch.QueryFilter{
		Query: elasticsearch.MatchQuery{
			Field: "CharmMeta.Summary",
			Query: value,
			Type:  "phrase",
		},
	}
}

// tagsFilter generates a filter that will match against the "tags" field
// in the data. For charms this is the Categories field and for bundles this
// is the Tags field.
func tagsFilter(value string) elasticsearch.Filter {
	return elasticsearch.OrFilter{
		elasticsearch.TermFilter{
			Field: "CharmMeta.Categories",
			Value: value,
		},
		elasticsearch.TermFilter{
			Field: "BundleData.Tags",
			Value: value,
		},
	}
}

// termFilter creates a function that generates a filter on the specified
// document field.
func termFilter(field string) func(string) elasticsearch.Filter {
	return func(value string) elasticsearch.Filter {
		return elasticsearch.TermFilter{
			Field: field,
			Value: value,
		}
	}
}

// bundleFilter is a filter that matches against bundles, based on
// the URL.
var bundleFilter = seriesFilter("bundle")

// typeFilter generates a filter that is used to match either only charms,
// or only bundles.
func typeFilter(value string) elasticsearch.Filter {
	if value == "bundle" {
		return bundleFilter
	}
	return elasticsearch.NotFilter{bundleFilter}
}
