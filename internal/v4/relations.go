// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4 // import "gopkg.in/juju/charmstore.v5-unstable/internal/v4"

import (
	"net/http"
	"net/url"

	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"
	"gopkg.in/mgo.v2/bson"

	"gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"
	"gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"
	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
)

// GET id/meta/charm-related[?include=meta[&include=meta…]]
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetacharm-related
func (h *ReqHandler) metaCharmRelated(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	if id.URL.Series == "bundle" {
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
					"$in": entity.CharmProvidedInterfaces,
				},
			}},
			{"charmprovidedinterfaces": bson.M{
				"$elemMatch": bson.M{
					"$in": entity.CharmRequiredInterfaces,
				},
			}},
		},
	}
	fields := bson.D{
		{"_id", 1},
		{"charmrequiredinterfaces", 1},
		{"charmprovidedinterfaces", 1},
		{"promulgated-url", 1},
		{"promulgated-revision", 1},
	}

	// Retrieve the entities from the database.
	var entities []mongodoc.Entity
	if err := h.Store.DB.Entities().Find(query).Select(fields).Sort("_id").All(&entities); err != nil {
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
func (h *ReqHandler) getRelatedCharmsResponse(
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

func (h *ReqHandler) getRelatedIfaceResponses(
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
				meta, err := h.getMetadataForEntity(&entity, includes, req)
				if err != nil {
					return nil, err
				}
				// Build the response.
				responses = append(responses, params.MetaAnyResponse{
					Id:   entity.PreferredURL(true),
					Meta: meta,
				})
			}
		}
	}
	return responses, nil
}

// GET id/meta/bundles-containing[?include=meta[&include=meta…]][&any-series=1][&any-revision=1][&all-results=1]
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetabundles-containing
func (h *ReqHandler) metaBundlesContaining(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	if id.URL.Series == "bundle" {
		return nil, nil
	}

	// Validate the URL query values.
	anySeries, err := router.ParseBool(flags.Get("any-series"))
	if err != nil {
		return nil, badRequestf(err, "invalid value for any-series")
	}
	anyRevision, err := router.ParseBool(flags.Get("any-revision"))
	if err != nil {
		return nil, badRequestf(err, "invalid value for any-revision")
	}
	allResults, err := router.ParseBool(flags.Get("all-results"))
	if err != nil {
		return nil, badRequestf(err, "invalid value for all-results")
	}

	// Mutate the reference so that it represents a base URL if required.
	prefURL := id.PreferredURL()
	searchId := *prefURL
	if anySeries || anyRevision {
		searchId.Revision = -1
		searchId.Series = ""
	}

	// Retrieve the bundles containing the resulting charm id.
	var entities []*mongodoc.Entity
	if err := h.Store.DB.Entities().
		Find(bson.D{{"bundlecharms", &searchId}}).
		Select(bson.D{{"_id", 1}, {"bundlecharms", 1}, {"promulgated-url", 1}}).
		All(&entities); err != nil {
		return nil, errgo.Notef(err, "cannot retrieve the related bundles")
	}

	// Further filter the entities if required, by only including latest
	// bundle revisions and/or excluding specific charm series or revisions.

	// Filter entities so it contains only entities that actually
	// match the desired search criteria.
	filterEntities(&entities, func(e *mongodoc.Entity) bool {
		if anySeries == anyRevision {
			// If neither anySeries or anyRevision are true, then
			// the search will be exact and therefore e must be
			// matched.
			// If both anySeries and anyRevision are true, then
			// the base entity that we are searching for is exactly
			// what we want to search for, therefore e must be matched.
			return true
		}
		for _, charmId := range e.BundleCharms {
			if charmId.Name == prefURL.Name &&
				charmId.User == prefURL.User &&
				(anySeries || charmId.Series == prefURL.Series) &&
				(anyRevision || charmId.Revision == prefURL.Revision) {
				return true
			}
		}
		return false
	})

	var latest map[charm.URL]int
	if !allResults {
		// Include only the latest revision of any bundle.
		// This is made somewhat tricky by the fact that
		// each bundle can have two URLs, its canonical
		// URL (with user) and its promulgated URL.
		//
		// We want to maximise the URL revision regardless of
		// whether the URL is promulgated or not, so we
		// we build a map holding the latest revision for both
		// promulgated and non-promulgated revisions
		// and then include entities that have the latest
		// revision for either.
		latest = make(map[charm.URL]int)

		// updateLatest updates the latest revision for u
		// without its revision if it's greater than the existing
		// entry.
		updateLatest := func(u *charm.URL) {
			u1 := *u
			u1.Revision = -1
			if rev, ok := latest[u1]; !ok || rev < u.Revision {
				latest[u1] = u.Revision
			}
		}
		for _, e := range entities {
			updateLatest(e.URL)
			if e.PromulgatedURL != nil {
				updateLatest(e.PromulgatedURL)
			}
		}
		filterEntities(&entities, func(e *mongodoc.Entity) bool {
			if e.PromulgatedURL != nil {
				u := *e.PromulgatedURL
				u.Revision = -1
				if latest[u] == e.PromulgatedURL.Revision {
					return true
				}
			}
			u := *e.URL
			u.Revision = -1
			return latest[u] == e.URL.Revision
		})
	}

	// Prepare and return the response.
	response := make([]*params.MetaAnyResponse, 0, len(entities))
	includes := flags["include"]
	// TODO(rog) make this concurrent.
	for _, e := range entities {
		// Ignore entities that aren't readable by the current user.
		if err := h.AuthorizeEntity(charmstore.EntityResolvedURL(e), req); err != nil {
			continue
		}
		meta, err := h.getMetadataForEntity(e, includes, req)
		if err != nil {
			return nil, errgo.Notef(err, "cannot retrieve bundle metadata")
		}
		response = append(response, &params.MetaAnyResponse{
			Id:   e.PreferredURL(true),
			Meta: meta,
		})
	}
	return response, nil
}

func (h *ReqHandler) getMetadataForEntity(e *mongodoc.Entity, includes []string, req *http.Request) (map[string]interface{}, error) {
	return h.GetMetadata(charmstore.EntityResolvedURL(e), includes, req)
}

// filterEntities deletes all entities from *entities for which
// the given predicate returns false.
func filterEntities(entities *[]*mongodoc.Entity, predicate func(*mongodoc.Entity) bool) {
	entities1 := *entities
	j := 0
	for _, e := range entities1 {
		if predicate(e) {
			entities1[j] = e
			j++
		}
	}
	*entities = entities1[0:j]
}
