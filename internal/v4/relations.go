// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4

import (
	"net/url"

	"github.com/juju/errgo"
	"gopkg.in/juju/charm.v3"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/params"
)

// GET id/meta/charm-related[?include=meta[&include=meta…]]
// http://tinyurl.com/q7vdmzl
func (h *handler) metaCharmRelated(entity *mongodoc.Entity, id *charm.Reference, path, method string, flags url.Values) (interface{}, error) {
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
			bson.M{"charmrequiredinterfaces": bson.M{
				"$elemMatch": bson.M{"$in": entity.CharmProvidedInterfaces},
			}},
			bson.M{"charmprovidedinterfaces": bson.M{
				"$elemMatch": bson.M{"$in": entity.CharmRequiredInterfaces},
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
	if err := h.store.DB.Entities().Find(query).Select(fields).All(&entities); err != nil {
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
	}, includes)
	if err != nil {
		return nil, errgo.Notef(err, "cannot retrieve the charm requires")
	}
	provides, err := h.getRelatedCharmsResponse(entity.CharmRequiredInterfaces, entities, func(e mongodoc.Entity) []string {
		return e.CharmProvidedInterfaces
	}, includes)
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
func (h *handler) getRelatedCharmsResponse(
	ifaces []string,
	entities []mongodoc.Entity,
	getInterfaces entityRelatedInterfacesGetter,
	includes []string) (map[string][]params.MetaAnyResponse, error) {
	results := make(map[string][]params.MetaAnyResponse, len(ifaces))
	for _, iface := range ifaces {
		responses, err := h.getRelatedIfaceResponses(iface, entities, getInterfaces, includes)
		if err != nil {
			return nil, err
		}
		if len(responses) > 0 {
			results[iface] = responses
		}
	}
	return results, nil
}

func (h *handler) getRelatedIfaceResponses(
	iface string,
	entities []mongodoc.Entity,
	getInterfaces entityRelatedInterfacesGetter,
	includes []string) ([]params.MetaAnyResponse, error) {
	// Build a list of responses including entities which are related
	// to the given interface.
	responses := make([]params.MetaAnyResponse, 0, len(entities))
	for _, entity := range entities {
		for _, entityIface := range getInterfaces(entity) {
			if entityIface == iface {
				// Retrieve the requested metadata for the entity.
				meta, err := h.GetMetadata(entity.URL, includes)
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

// GET id/meta/bundles-containing[?include=meta[&include=meta…]][&any-series=1][&any-revision=1]
// http://tinyurl.com/oqc386r
func (h *handler) metaBundlesContaining(entity *mongodoc.Entity, id *charm.Reference, path, method string, flags url.Values) (interface{}, error) {
	if id.Series == "bundle" {
		return nil, nil
	}

	// Validate the URL query values.
	anySeries, err := stringToBool(flags.Get("any-series"))
	if err != nil {
		return nil, badRequestf(err, "invalid value for any-series")
	}
	anyRevision, err := stringToBool(flags.Get("any-revision"))
	if err != nil {
		return nil, badRequestf(err, "invalid value for any-revision")
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
		All(&entities); err != nil {
		return nil, errgo.Notef(err, "cannot retrieve the related bundles")
	}

	// Further filter the entities if required.
	if anySeries != anyRevision {
		predicate := func(e *mongodoc.Entity) bool {
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
		entities = filterEntities(entities, predicate)
	}

	// Prepare and return the response.
	response := make([]*params.MetaAnyResponse, 0, len(entities))
	includes := flags["include"]
	for _, e := range entities {
		meta, err := h.GetMetadata(e.URL, includes)
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

// filterEntities filters the given entities based on the given predicate.
func filterEntities(entities []mongodoc.Entity, predicate func(*mongodoc.Entity) bool) []mongodoc.Entity {
	results := make([]mongodoc.Entity, 0, len(entities))
	for _, entity := range entities {
		if predicate(&entity) {
			results = append(results, entity)
		}
	}
	return results
}
