// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4

import (
	"net/http"
	"net/url"

	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v4"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/params"
)

// GET id/meta/charm-related[?include=meta[&include=meta…]]
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetacharm-related
func (h *Handler) metaCharmRelated(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values, req *http.Request) (interface{}, error) {
	if id.Series == "bundle" {
		return nil, nil
	}

	// If the charm does not define any relation we can just return without
	// hitting the db.
	if len(entity.CharmProvidedInterfaces)+len(entity.CharmRequiredInterfaces) == 0 {
		return &params.RelatedResponse{}, nil
	}

	// Build the query to retrieve the related entities.
	query := bson.M{
		"$or": []bson.M{
			{"charmrequiredinterfaces": bson.M{
				"$elemMatch": bson.M{
					"$in": entity.CharmProvidedInterfaces},
			}},
			{"charmprovidedinterfaces": bson.M{
				"$elemMatch": bson.M{
					"$in": entity.CharmRequiredInterfaces},
			}},
		},
	}
	fields := bson.D{
		{"_id", 1},
		{"charmrequiredinterfaces", 1},
		{"charmprovidedinterfaces", 1},
	}

	// Retrieve the entities from the database.
	var entities []mongodoc.Entity
	if err := h.store.DB.Entities().Find(query).Select(fields).Sort("_id").All(&entities); err != nil {
		return nil, errgo.Notef(err, "cannot retrieve the related charms")
	}

	// If no entities are found there is no need for further processing the
	// results.
	if len(entities) == 0 {
		return &params.RelatedResponse{}, nil
	}

	// Build the results, by grouping entities based on their relations' roles
	// and interfaces.
	includes := flags["include"]
	requires, err := h.getRelatedCharmsResponse(entity.CharmProvidedInterfaces, entities, func(e mongodoc.Entity) []string {
		return e.CharmRequiredInterfaces
	}, includes, req)
	if err != nil {
		return nil, errgo.Notef(err, "cannot retrieve the charm requires")
	}
	provides, err := h.getRelatedCharmsResponse(entity.CharmRequiredInterfaces, entities, func(e mongodoc.Entity) []string {
		return e.CharmProvidedInterfaces
	}, includes, req)
	if err != nil {
		return nil, errgo.Notef(err, "cannot retrieve the charm provides")
	}

	// Return the response.
	return &params.RelatedResponse{
		Requires: requires,
		Provides: provides,
	}, nil
}

type entityRelatedInterfacesGetter func(mongodoc.Entity) []string

// getRelatedCharmsResponse returns a response mapping interfaces to related
// charms. For instance:
//   map[string][]params.MetaAnyResponse{
//       "http": []params.MetaAnyResponse{
//           {Id: "cs:utopic/django-42", Meta: ...},
//           {Id: "cs:trusty/wordpress-47", Meta: ...},
//       },
//       "memcache": []params.MetaAnyResponse{
//           {Id: "cs:utopic/memcached-0", Meta: ...},
//       },
//   }
func (h *Handler) getRelatedCharmsResponse(
	ifaces []string,
	entities []mongodoc.Entity,
	getInterfaces entityRelatedInterfacesGetter,
	includes []string,
	req *http.Request,
) (map[string][]params.MetaAnyResponse, error) {
	results := make(map[string][]params.MetaAnyResponse, len(ifaces))
	for _, iface := range ifaces {
		responses, err := h.getRelatedIfaceResponses(iface, entities, getInterfaces, includes, req)
		if err != nil {
			return nil, err
		}
		if len(responses) > 0 {
			results[iface] = responses
		}
	}
	return results, nil
}

func (h *Handler) getRelatedIfaceResponses(
	iface string,
	entities []mongodoc.Entity,
	getInterfaces entityRelatedInterfacesGetter,
	includes []string,
	req *http.Request,
) ([]params.MetaAnyResponse, error) {
	// Build a list of responses including entities which are related
	// to the given interface.
	responses := make([]params.MetaAnyResponse, 0, len(entities))
	for _, entity := range entities {
		for _, entityIface := range getInterfaces(entity) {
			if entityIface == iface {
				// Retrieve the requested metadata for the entity.
				meta, err := h.GetMetadata(entity.URL, includes, req)
				if err != nil {
					return nil, err
				}
				// Build the response.
				responses = append(responses, params.MetaAnyResponse{
					Id:   entity.URL,
					Meta: meta,
				})
			}
		}
	}
	return responses, nil
}

// GET id/meta/bundles-containing[?include=meta[&include=meta…]][&any-series=1][&any-revision=1][&all-results=1]
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetabundles-containing
func (h *Handler) metaBundlesContaining(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values, req *http.Request) (interface{}, error) {
	if id.Series == "bundle" {
		return nil, nil
	}

	// Validate the URL query values.
	anySeries, err := parseBool(flags.Get("any-series"))
	if err != nil {
		return nil, badRequestf(err, "invalid value for any-series")
	}
	anyRevision, err := parseBool(flags.Get("any-revision"))
	if err != nil {
		return nil, badRequestf(err, "invalid value for any-revision")
	}
	allResults, err := parseBool(flags.Get("all-results"))
	if err != nil {
		return nil, badRequestf(err, "invalid value for all-results")
	}

	// Mutate the reference so that it represents a base URL if required.
	searchId := *id
	if anySeries || anyRevision {
		searchId.Revision = -1
		searchId.Series = ""
	}

	// Retrieve the bundles containing the resulting charm id.
	var entities []mongodoc.Entity
	if err := h.store.DB.Entities().
		Find(bson.D{{"bundlecharms", &searchId}}).
		Select(bson.D{{"_id", 1}, {"bundlecharms", 1}}).
		Sort("baseurl", "series", "-revision").
		All(&entities); err != nil {
		return nil, errgo.Notef(err, "cannot retrieve the related bundles")
	}

	// Further filter the entities if required, by only including latest
	// bundle revisions and/or excluding specific charm series or revisions.
	anySeriesOrRevisionPredicate := func(e *mongodoc.Entity) bool {
		if anySeries == anyRevision {
			return true
		}
		for _, charmId := range e.BundleCharms {
			if charmId.Name == id.Name &&
				charmId.User == id.User &&
				(anySeries || charmId.Series == id.Series) &&
				(anyRevision || charmId.Revision == id.Revision) {
				return true
			}
		}
		return false
	}
	predicate := anySeriesOrRevisionPredicate
	if !allResults {
		previous := &charm.Reference{}
		predicate = func(e *mongodoc.Entity) bool {
			if e.URL.User == previous.User && e.URL.Name == previous.Name && e.URL.Series == previous.Series {
				return false
			}
			if included := anySeriesOrRevisionPredicate(e); included {
				previous = e.URL
				return true
			}
			return false
		}
	}
	entities = filterEntities(entities, predicate)

	// Prepare and return the response.
	response := make([]*params.MetaAnyResponse, 0, len(entities))
	includes := flags["include"]
	for _, e := range entities {
		meta, err := h.GetMetadata(e.URL, includes, req)
		if err != nil {
			return nil, errgo.Notef(err, "cannot retrieve bundle metadata")
		}
		response = append(response, &params.MetaAnyResponse{
			Id:   e.URL,
			Meta: meta,
		})
	}
	return response, nil
}

// filterEntities returns a slice containing all the entities for which the
// given predicate returns true.
func filterEntities(entities []mongodoc.Entity, predicate func(*mongodoc.Entity) bool) []mongodoc.Entity {
	results := make([]mongodoc.Entity, 0, len(entities))
	for _, entity := range entities {
		if predicate(&entity) {
			results = append(results, entity)
		}
	}
	return results
}
