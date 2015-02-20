// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/juju/utils"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v4"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/internal/elasticsearch"
	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/params"
)

type SearchIndex struct {
	*elasticsearch.Database
	Index string
}

const typeName = "entity"

// seriesBoost defines how much the results for each
// series will be boosted. Series are currently ranked in
// reverse order of LTS releases, followed by the latest
// non-LTS release, followed by everything else.
var seriesBoost = map[string]float64{
	"bundle":  1.1255,
	"trusty":  1.125,
	"precise": 1.1125,
	"utopic":  1.1,
}

// deprecatedSeries are series that should not show up in search
// results. This list is used to filter out the charms before they are
// indexed.
var deprecatedSeries = map[string]bool{
	"oneiric": true,
	"quantal": true,
	"raring":  true,
	"saucy":   true,
}

// SearchDoc is a mongodoc.Entity with additional fields useful for searching.
// This is the document that is stored in the search index.
type SearchDoc struct {
	*mongodoc.Entity
	TotalDownloads int64
	ReadACLs       []string
}

// UpdateSearchAsync will update the search record for the entity
// reference r in the backgroud.
func (s *Store) UpdateSearchAsync(r *charm.Reference) {
	go func() {
		if err := s.UpdateSearch(r); err != nil {
			logger.Errorf("cannot update search record for %v: %s", r, err)
		}
	}()
}

// UpdateSearch updates the search record for the entity reference r.
// The search index only includes the latest revision of each entity so
// the latest revision of the charm specified by r will be indexed.
func (s *Store) UpdateSearch(r *charm.Reference) error {
	if s.ES == nil || s.ES.Database == nil {
		return nil
	}
	if deprecatedSeries[r.Series] {
		return nil
	}
	var entity mongodoc.Entity
	if err := s.DB.Entities().Find(bson.M{"user": r.User, "name": r.Name, "series": r.Series}).Sort("-revision").One(&entity); err != nil {
		if err == mgo.ErrNotFound {
			return errgo.WithCausef(nil, params.ErrNotFound, "entity not found %s", r)
		}
		return errgo.Notef(err, "cannot get %s", r)
	}
	baseEntity, err := s.FindBaseEntity(entity.BaseURL, "acls")
	if err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}
	doc, err := s.searchDocFromEntity(&entity, baseEntity)
	if err != nil {
		return errgo.Mask(err)
	}
	if err := s.ES.update(doc); err != nil {
		return errgo.Notef(err, "cannot update search index")
	}
	return nil
}

// UpdateSearchFields updates the search record for the entity reference r
// with the updated values in fields.
func (s *Store) UpdateSearchFields(r *charm.Reference, fields map[string]interface{}) error {
	if s.ES == nil || s.ES.Database == nil {
		return nil
	}
	var needUpdate bool
	for k := range fields {
		// Add any additional fields here that should update the search index.
		if k == "extrainfo.legacy-download-stats" {
			needUpdate = true
		}
	}
	if !needUpdate {
		return nil
	}
	if err := s.UpdateSearch(r); err != nil {
		return errgo.Mask(err)
	}
	return nil
}

// searchDocFromEntity performs the processing required to convert a mongodoc.Entity
// to an esDoc for indexing.
func (s *Store) searchDocFromEntity(e *mongodoc.Entity, be *mongodoc.BaseEntity) (*SearchDoc, error) {
	doc := SearchDoc{Entity: e}
	doc.ReadACLs = be.ACLs.Read
	_, allRevisions, err := s.ArchiveDownloadCounts(e.URL)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	doc.TotalDownloads = allRevisions.Total
	return &doc, nil
}

// update inserts an entity into elasticsearch if elasticsearch
// is configured. The entity with id r is extracted from mongodb
// and written into elasticsearch.
func (si *SearchIndex) update(doc *SearchDoc) error {
	if si == nil || si.Database == nil {
		return nil
	}
	err := si.PutDocumentVersionWithType(
		si.Index,
		typeName,
		si.getID(doc.URL),
		int64(doc.URL.Revision),
		elasticsearch.ExternalGTE,
		doc)
	if err != nil && err != elasticsearch.ErrConflict {
		return errgo.Mask(err)
	}
	return nil
}

// getID returns an ID for the elasticsearch document based on the contents of the
// mongoDB document. This is to allow elasticsearch documents to be replaced with
// updated versions when charm data is changed.
func (si *SearchIndex) getID(r *charm.Reference) string {
	ref := *r
	ref.Revision = -1
	b := sha1.Sum([]byte(ref.String()))
	s := base64.URLEncoding.EncodeToString(b[:])
	// Cut off any trailing = as there is no need for them and they will get URL escaped.
	return strings.TrimRight(s, "=")
}

// Search searches for matching entities in the configured elasticsearch index.
// If there is no elasticsearch index configured then it will return an empty
// SearchResult, as if no results were found.
func (si *SearchIndex) search(sp SearchParams) (SearchResult, error) {
	if si == nil || si.Database == nil {
		return SearchResult{}, nil
	}
	q := createSearchDSL(sp)
	q.Fields = append(q.Fields, "URL")
	esr, err := si.Search(si.Index, typeName, q)
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

// GetSearchDocument retrieves the current search record for the charm
// reference id.
func (si *SearchIndex) GetSearchDocument(id *charm.Reference) (*SearchDoc, error) {
	if si == nil || si.Database == nil {
		return &SearchDoc{}, nil
	}
	var s SearchDoc
	err := si.GetDocument(si.Index, "entity", si.getID(id), &s)
	if err != nil {
		return nil, errgo.Notef(err, "cannot retrieve search document for %v", id)
	}
	return &s, nil
}

// version is a document that stores the structure information
// in the elasticsearch database.
type version struct {
	Version int64
	Index   string
}

const versionIndex = ".versions"
const versionType = "version"

// ensureIndexes makes sure that the required indexes exist and have the right
// settings. If force is true then ensureIndexes will create new indexes irrespective
// of the status of the current index.
func (si *SearchIndex) ensureIndexes(force bool) error {
	if si == nil || si.Database == nil {
		return nil
	}
	old, dv, err := si.getCurrentVersion()
	if err != nil {
		return errgo.Notef(err, "cannot get current version")
	}
	if !force && old.Version >= esSettingsVersion {
		return nil
	}
	index, err := si.newIndex()
	if err != nil {
		return errgo.Notef(err, "cannot create index")
	}
	new := version{
		Version: esSettingsVersion,
		Index:   index,
	}
	updated, err := si.updateVersion(new, dv)
	if err != nil {
		return errgo.Notef(err, "cannot update version")
	}
	if !updated {
		// Update failed so delete the new index
		if err := si.DeleteIndex(index); err != nil {
			return errgo.Notef(err, "cannot delete index")
		}
		return nil
	}
	// Update succeeded - update the aliases
	if err := si.Alias(index, si.Index); err != nil {
		return errgo.Notef(err, "cannot create alias")
	}
	// Delete the old unused index
	if old.Index != "" {
		if err := si.DeleteIndex(old.Index); err != nil {
			return errgo.Notef(err, "cannot delete index")
		}
	}
	return nil
}

// getCurrentVersion gets the version of elasticsearch settings, if any
// that are deployed to elasticsearch.
func (si *SearchIndex) getCurrentVersion() (version, int64, error) {
	var v version
	d, err := si.GetESDocument(versionIndex, versionType, si.Index)
	if err != nil && err != elasticsearch.ErrNotFound {
		return version{}, 0, errgo.Notef(err, "cannot get settings version")
	}
	if d.Found {
		if err := json.Unmarshal(d.Source, &v); err != nil {
			return version{}, 0, errgo.Notef(err, "invalid version")
		}
	}
	return v, d.Version, nil
}

// newIndex creates a new index with current elasticsearch settings.
// The new Index will have a randomized name based on si.Index.
func (si *SearchIndex) newIndex() (string, error) {
	uuid, err := utils.NewUUID()
	if err != nil {
		return "", errgo.Notef(err, "cannot create index name")
	}
	index := si.Index + "-" + uuid.String()
	if err := si.PutIndex(index, esIndex); err != nil {
		return "", errgo.Notef(err, "cannot set index settings")
	}
	if err := si.PutMapping(index, "entity", esMapping); err != nil {
		return "", errgo.Notef(err, "cannot set index mapping")
	}
	return index, nil
}

// updateVersion attempts to atomically update the document specifying the version of
// the elasticsearch settings. If it succeeds then err will be nil, if the update could not be
// made atomically then err will be elasticsearch.ErrConflict, otherwise err is a non-nil
// error.
func (si *SearchIndex) updateVersion(v version, dv int64) (bool, error) {
	var err error
	if dv == 0 {
		err = si.CreateDocument(versionIndex, versionType, si.Index, v)
	} else {
		err = si.PutDocumentVersion(versionIndex, versionType, si.Index, dv, v)
	}
	if err != nil {
		if errgo.Cause(err) == elasticsearch.ErrConflict {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// syncSearch populates the SearchIndex with all the data currently stored in
// mongodb. If the SearchIndex is not configured then this method returns a nil error.
func (s *Store) syncSearch() error {
	if s.ES == nil || s.ES.Database == nil {
		return nil
	}
	var result mongodoc.Entity
	// Only get the IDs here, UpdateSearch will get the full document
	// if it is in a series that is indexed.
	iter := s.DB.Entities().Find(nil).Select(bson.M{"_id": 1}).Iter()
	defer iter.Close() // Make sure we always close on error.
	for iter.Next(&result) {
		if err := s.UpdateSearch(result.URL); err != nil {
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
	// ACL values to search in addition to everyone. ACL values may represent user names
	// or group names.
	Groups []string
	// Admin searches will not filter on the ACL and will show results for all matching
	// charms.
	Admin bool
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

// sortFields contains a mapping from api fieldnames to the entity fields to search.
var sortFields = map[string]string{
	"name":      "Name",
	"owner":     "User",
	"series":    "Series",
	"downloads": "TotalDownloads",
}

// SearchResult represents the result of performing a search.
type SearchResult struct {
	SearchTime time.Duration
	Total      int
	Results    []*charm.Reference
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

	// Boosting
	f := []elasticsearch.Function{
		// TODO(mhilton) review this function in future if downloads get sufficiently
		// large that the order becomes undesirable.
		elasticsearch.FieldValueFactorFunction{
			Field:    "TotalDownloads",
			Factor:   0.000001,
			Modifier: "ln2p",
		},
		elasticsearch.BoostFactorFunction{
			Filter:      ownerFilter(""),
			BoostFactor: 1.25,
		},
	}
	for k, v := range seriesBoost {
		f = append(f, elasticsearch.BoostFactorFunction{
			Filter:      seriesFilter(k),
			BoostFactor: v,
		})
	}
	q = elasticsearch.FunctionScoreQuery{
		Query:     q,
		Functions: f,
	}

	// Filters
	qdsl.Query = elasticsearch.FilteredQuery{
		Query:  q,
		Filter: createFilters(sp.Filters, sp.Admin, sp.Groups),
	}

	// Sorting
	for _, s := range sp.sort {
		qdsl.Sort = append(qdsl.Sort, createSort(s))
	}

	return qdsl
}

// createFilters converts the filters requested with the search API into
// filters in the elasticsearch query DSL.
// See https://github.com/juju/charmstore/blob/v4/docs/API.md#get-search
// for details of how filters are specified in the API. For each key in f a
// filter is created that matches any one of the set of values specified for
// that key. The created filter will only match when at least one of the
// requested values matches for all of the requested keys. Any filter names
// that are not defined in the filters map will be silently skipped.
func createFilters(f map[string][]string, admin bool, groups []string) elasticsearch.Filter {
	af := make(elasticsearch.AndFilter, 0, len(f)+1)
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
	if admin {
		return af
	}
	gf := make(elasticsearch.OrFilter, 0, len(groups)+1)
	gf = append(gf, elasticsearch.TermFilter{
		Field: "ReadACLs",
		Value: params.Everyone,
	})
	for _, g := range groups {
		gf = append(gf, elasticsearch.TermFilter{
			Field: "ReadACLs",
			Value: g,
		})
	}
	af = append(af, gf)
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
