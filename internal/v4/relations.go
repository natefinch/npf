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
