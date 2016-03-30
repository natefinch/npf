// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v5 // import "gopkg.in/juju/charmstore.v5-unstable/internal/v5"

import (
	"net/http"
	"net/url"

	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable/resource"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"

	"gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"
	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
)

// GET id/meta/resources
// https://github.com/juju/charmstore/blob/v5/docs/API.md#get-idmetaresources
func (h *ReqHandler) metaResources(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	if entity.URL.Series == "bundle" {
		// Bundles do not have resources so we return an empty result.
		return []params.Resource{}, nil
	}
	if entity.CharmMeta == nil {
		return []params.Resource{}, nil
	}

	// TODO(ericsnow) Handle flags.
	// TODO(ericsnow) Use h.Store.ListResources() once that exists.
	resources, err := basicListResources(entity)
	if err != nil {
		return nil, err
	}
	var results []params.Resource
	for _, res := range resources {
		result := params.Resource2API(res)
		results = append(results, result)
	}
	return results, nil
}

func basicListResources(entity *mongodoc.Entity) ([]resource.Resource, error) {
	var resources []resource.Resource
	for _, meta := range entity.CharmMeta.Resources {
		// We use an origin of "upload" since resources cannot be uploaded yet.
		resOrigin := resource.OriginUpload
		res := resource.Resource{
			Meta:   meta,
			Origin: resOrigin,
			// Revision, Fingerprint, and Size are not set.
		}
		resources = append(resources, res)
	}
	resource.Sort(resources)
	return resources, nil
}

// POST id/resources/name
// https://github.com/juju/charmstore/blob/v5/docs/API.md#post-idresourcesname
//
// GET  id/resources/name[/revision]
// https://github.com/juju/charmstore/blob/v5/docs/API.md#get-idresourcesnamerevision
func (h *ReqHandler) serveResources(id *router.ResolvedURL, w http.ResponseWriter, req *http.Request) error {
	// Resources are "published" using "PUT id/publish" so we don't
	// support PUT here.
	// TODO(ericsnow) Support DELETE to remove a resource?
	// (like serveArchive() does)
	switch req.Method {
	case "GET":
		return h.serveDownloadResource(id, w, req)
	case "POST":
		return h.serveUploadResource(id, w, req)
	default:
		return errgo.WithCausef(nil, params.ErrMethodNotAllowed, "%s not allowed", req.Method)
	}
}

func (h *ReqHandler) serveDownloadResource(id *router.ResolvedURL, w http.ResponseWriter, req *http.Request) error {
	return errNotImplemented
}

func (h *ReqHandler) serveUploadResource(id *router.ResolvedURL, w http.ResponseWriter, req *http.Request) error {
	return errNotImplemented
}
