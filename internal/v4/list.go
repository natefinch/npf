// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4 // import "gopkg.in/juju/charmstore.v5-unstable/internal/v4"

import (
	"net/http"

	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"

	"gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"
	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
)

// GET list[?filter=value…][&include=meta][&sort=field[+dir]]
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-list
func (h *ReqHandler) serveList(_ http.Header, req *http.Request) (interface{}, error) {
	lp, err := parseListParams(req)
	if err != nil {
		return "", err
	}
	return h.doList(lp, req)
}

// doList performs a listing specified by ListParams. If lp
// specifies that additional metadata needs to be added to the results,
// then it is added. It also filters the result based on ACLs.
func (h *ReqHandler) doList(lp charmstore.ListParams, req *http.Request) (interface{}, error) {
	// perform query
	results, err := h.Store.List(lp)
	if err != nil {
		return nil, errgo.Notef(err, "error listing charms and bundles")
	}
	filteredACLResults := make([]*router.ResolvedURL, 0)
	for _, result := range results.Results {
		if err = h.AuthorizeEntity(result, req); err == nil {
			filteredACLResults = append(filteredACLResults, result)
		}
	}
	return params.ListResponse{
		Results: h.addMetaData(filteredACLResults, lp.Include, req),
	}, nil
}

// parseListParms extracts the list parameters from the request
func parseListParams(req *http.Request) (charmstore.ListParams, error) {
	lp := charmstore.ListParams{}
	var err error
	for k, v := range req.Form {
		switch k {
		case "include":
			for _, s := range v {
				if s != "" {
					lp.Include = append(lp.Include, s)
				}
			}
		case "name":
			if lp.Filters == nil {
				lp.Filters = make(map[string]interface{})
			}
			lp.Filters[k] = v[0]
		case "owner":
			if lp.Filters == nil {
				lp.Filters = make(map[string]interface{})
			}
			lp.Filters["user"] = v[0]
		case "series":
			if lp.Filters == nil {
				lp.Filters = make(map[string]interface{})
			}
			lp.Filters["series"] = v[0]
		case "type":
			if lp.Filters == nil {
				lp.Filters = make(map[string]interface{})
			}
			if v[0] == "bundle" {
				lp.Filters["series"] = "bundle"
			} else {
				lp.Filters["series"] = map[string]interface{}{"$ne": "bundle"}
			}
		case "promulgated":
			promulgated, err := router.ParseBool(v[0])
			if err != nil {
				return charmstore.ListParams{}, badRequestf(err, "invalid promulgated filter parameter")
			}
			if lp.Filters == nil {
				lp.Filters = make(map[string]interface{})
			}
			if promulgated {
				lp.Filters["promulgated-revision"] = map[string]interface{}{"$gt": 0}
			} else {
				lp.Filters["promulgated-revision"] = map[string]interface{}{"$lt": 0}
			}
		case "sort":
			err = lp.ParseSortFieldsList(v...)
			if err != nil {
				return charmstore.ListParams{}, badRequestf(err, "invalid sort field")
			}
		default:
			return charmstore.ListParams{}, badRequestf(nil, "invalid parameter: %s", k)
		}
	}
	return lp, nil
}
