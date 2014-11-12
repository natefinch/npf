// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore

import (
	"crypto/sha1"
	"encoding/base64"
	"strings"
	"time"

	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v4"

	"github.com/juju/charmstore/internal/elasticsearch"
	"github.com/juju/charmstore/internal/mongodoc"
)

// StoreElasticSearch provides strongly typed methods for accessing the
// elasticsearch database. These methods will not return errors if
// elasticsearch is not configured, allowing them to be safely called even if
// it is not enabled in this service.
type StoreElasticSearch struct {
	*elasticsearch.Index
}

const typeName = "entity"

type esDoc struct {
	*mongodoc.Entity
	Name   string
	User   string
	Series string
}

// Put inserts the mongodoc.Entity into elasticsearch if elasticsearch
// is configured.
func (ses *StoreElasticSearch) put(entity *mongodoc.Entity) error {
	if ses == nil || ses.Index == nil {
		return nil
	}
	doc := esDoc{
		Entity: entity,
		Name:   entity.URL.Name,
		User:   entity.URL.User,
		Series: entity.URL.Series,
	}
	_, err := ses.PutDocumentVersion(typeName, ses.getID(entity), int64(entity.URL.Revision), doc)
	return errgo.Mask(err)
}

// getID returns an ID for the elasticsearch document based on the contents of the
// mongoDB document. This is to allow elasticsearch documents to be replaced with
// updated versions when charm data is changed.
func (ses *StoreElasticSearch) getID(entity *mongodoc.Entity) string {
	ref := *entity.URL
	ref.Revision = -1
	b := sha1.Sum([]byte(ref.String()))
	s := base64.URLEncoding.EncodeToString(b[:])
	// Cut off any trailing = as there is no need for them and they will get URL escaped.
	return strings.TrimRight(s, "=")
}

// Search searches for matching entities in the configured elasticsearch index.
// If there is no elasticsearch index configured then it will return an empty
// SearchResult, as if no results were found.
func (ses *StoreElasticSearch) search(sp SearchParams) (SearchResult, error) {
	if ses == nil || ses.Index == nil {
		return SearchResult{}, nil
	}
	q := createSearchDSL(sp)
	q.Fields = append(q.Fields, "URL")
	esr, err := ses.Search(typeName, q)
	if err != nil {
		return SearchResult{}, errgo.Mask(err)
	}
	r := SearchResult{
		SearchTime: time.Duration(esr.Took) * time.Millisecond,
		Total:      esr.Hits.Total,
		Results:    make([]*charm.Reference, 0, len(esr.Hits.Hits)),
	}
	for _, h := range esr.Hits.Hits {
		ref, err := charm.ParseReference(h.Fields.GetString("URL"))
		if err != nil {
			return SearchResult{}, errgo.Notef(err, "invalid result %q", h.Fields.GetString("URL"))
		}
		r.Results = append(r.Results, ref)
	}
	return r, nil
}

// ExportToElasticSearch reads all of the mongodoc Entities and writes
// them to elasticsearch
func (store *Store) ExportToElasticSearch() error {
	var result mongodoc.Entity
	iter := store.DB.Entities().Find(nil).Iter()
	defer iter.Close() // Make sure we always close on error.
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

// SearchParams represents the search parameters used to search the store.
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
	// Include the following metadata items in the search results.
	Include []string
	// Start the the returned items at a specific offset.
	Skip int
	// Sort the returned items.
	sort []sortParam
}

func (sp *SearchParams) ParseSortFields(f ...string) error {
	for _, s := range f {
		for _, s := range strings.Split(s, ",") {
			var sort sortParam
			if strings.HasPrefix(s, "-") {
				sort.Order = sortDescending
				s = s[1:]
			}
			sort.Field = sortFields[s]
			if sort.Field == "" {
				return errgo.Newf("%s", s)
			}
			sp.sort = append(sp.sort, sort)
		}
	}

	return nil
}

// sortOrder defines the order in which a field should be sorted.
type sortOrder int

const (
	sortAscending sortOrder = iota
	sortDescending
)

// sortParam represents a field and direction on which results should be sorted.
type sortParam struct {
	Field string
	Order sortOrder
}

// sortFields contains a mapping from api fieldnames to the esdoc fields to search.
var sortFields = map[string]string{
	"name":   "Name",
	"owner":  "User",
	"series": "Series",
}

// SearchResult represents the result of performing a search.
type SearchResult struct {
	SearchTime time.Duration
	Total      int
	Results    []*charm.Reference
}

// Search searches the store for the given SearchParams.
// It returns a slice a SearchResult containing the results of the search.
func (store *Store) Search(sp SearchParams) (SearchResult, error) {
	results, err := store.ES.search(sp)
	if err != nil {
		return SearchResult{}, errgo.Mask(err)
	}
	return results, nil
}

// queryFields provides a map of fields to weighting to use with the
// elasticsearch query.
func queryFields(sp SearchParams) map[string]float64 {
	fields := map[string]float64{
		"URL.ngrams":              8,
		"CharmMeta.Categories":    5,
		"BundleData.Tags":         5,
		"CharmProvidedInterfaces": 3,
		"CharmRequiredInterfaces": 3,
		"CharmMeta.Description":   1,
		"BundleReadMe":            1,
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
func createSearchDSL(sp SearchParams) elasticsearch.QueryDSL {
	qdsl := elasticsearch.QueryDSL{
		From: sp.Skip,
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

	q = elasticsearch.FunctionScoreQuery{
		Query: q,
		Functions: []elasticsearch.Function{
			elasticsearch.DecayFunction{
				Function: "linear",
				Field:    "UploadTime",
				Scale:    "365d",
			},
			elasticsearch.BoostFactorFunction{
				Filter:      ownerFilter(""),
				BoostFactor: 1.25,
			},
		},
	}

	// Filters
	qdsl.Query = elasticsearch.FilteredQuery{
		Query:  q,
		Filter: createFilters(sp.Filters),
	}

	// Sorting
	for _, s := range sp.sort {
		qdsl.Sort = append(qdsl.Sort, createSort(s))
	}

	return qdsl
}

// createFilters converts the filters requested with the serch API into
// filters in the elasticsearch query DSL. Please see http://tinyurl.com/qzobc69
// for details of how filters are specified in the API. For each key in f a filter is
// created that matches any one of the set of values specified for that key.
// The created filter will only match when at least one of the requested values
// matches for all of the requested keys. Any filter names that are not defined
// in the filters map will be silently skipped.
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
	"name":        nameFilter,
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

// nameFilter generates a filter that will match against the
// name of the charm or bundle.
func nameFilter(value string) elasticsearch.Filter {
	return elasticsearch.QueryFilter{
		Query: elasticsearch.MatchQuery{
			Field: "Name",
			Query: value,
			Type:  "phrase",
		},
	}
}

// ownerFilter generates a filter that will match against the
// owner taken from the URL.
func ownerFilter(value string) elasticsearch.Filter {
	return elasticsearch.QueryFilter{
		Query: elasticsearch.MatchQuery{
			Field: "User",
			Query: value,
			Type:  "phrase",
		},
	}
}

// seriesFilter generates a filter that will match against the
// series taken from the URL.
func seriesFilter(value string) elasticsearch.Filter {
	return elasticsearch.QueryFilter{
		Query: elasticsearch.MatchQuery{
			Field: "Series",
			Query: value,
			Type:  "phrase",
		},
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
	tags := strings.Split(value, " ")
	af := make(elasticsearch.AndFilter, 0, len(tags))
	for _, t := range tags {
		if t == "" {
			continue
		}
		af = append(af, elasticsearch.OrFilter{
			elasticsearch.TermFilter{
				Field: "CharmMeta.Categories",
				Value: t,
			},
			elasticsearch.TermFilter{
				Field: "BundleData.Tags",
				Value: t,
			},
		})
	}
	return af
}

// termFilter creates a function that generates a filter on the specified
// document field.
func termFilter(field string) func(string) elasticsearch.Filter {
	return func(value string) elasticsearch.Filter {
		terms := strings.Split(value, " ")
		af := make(elasticsearch.AndFilter, 0, len(terms))
		for _, t := range terms {
			if t == "" {
				continue
			}
			af = append(af, elasticsearch.TermFilter{
				Field: field,
				Value: t,
			})
		}
		return af
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

// createSort creates an elasticsearch.Sort query parameter out of a Sort parameter.
func createSort(s sortParam) elasticsearch.Sort {
	sort := elasticsearch.Sort{
		Field: s.Field,
		Order: elasticsearch.Ascending,
	}
	if s.Order == sortDescending {
		sort.Order = elasticsearch.Descending
	}
	return sort
}
