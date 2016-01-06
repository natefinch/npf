// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v5 // import "gopkg.in/juju/charmstore.v5-unstable/internal/v5"

import (
	"net/http"

	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"

	"gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"
	"gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"
)

// GET list[?filter=value…][&include=meta][&sort=field[+dir]]
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-list
func (h *ReqHandler) serveList(_ http.Header, req *http.Request) (interface{}, error) {
	sp, err := ParseSearchParams(req)
	if err != nil {
		return "", err
	}
	// perform query
	results, err := h.Store.List(sp)
	if err != nil {
		return nil, errgo.Notef(err, "error listing charms and bundles")
	}

	// TODO 30th Nov 2015 Fabrice:
	// we should follow the same pattern as search, and put the user, admin and groups
	// into the SearchParams and leave the charmstore package to be responsible for filtering
	// For performance, we should also look at not having n request to mongo.
	filteredACLResults := make([]*mongodoc.Entity, 0, len(results.Results))
	for _, result := range results.Results {
		if err = h.AuthorizeEntity(charmstore.EntityResolvedURL(result), req); err == nil {
			filteredACLResults = append(filteredACLResults, result)
		}
	}
	return params.ListResponse{
		Results: h.addMetaData(filteredACLResults, sp.Include, req),
	}, nil
}
