// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4 // import "gopkg.in/juju/charmstore.v5-unstable/internal/v4"

import (
	"net/http"
	"sync/atomic"

	"github.com/juju/utils/parallel"
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
	results.Results = filteredACLResults
	return h.addMetaData(results, lp.Include, req)
}

//addMetada adds the requested meta data with the include list.
func (h *ReqHandler) addMetaData(results charmstore.ListResult, include []string, req *http.Request) (interface{}, error){
	response := params.ListResponse{
		Results:    make([]params.EntityResult, len(results.Results)),
	}
	run := parallel.NewRun(maxConcurrency)
	var missing int32
	for i, ref := range results.Results {
		i, ref := i, ref
		run.Do(func() error {
			meta, err := h.Router.GetMetadata(ref, include, req)
			if err != nil {
				// Unfortunately it is possible to get errors here due to
				// internal inconsistency, so rather than throwing away
				// all the search results, we just log the error and move on.
				logger.Errorf("cannot retrieve metadata for %v: %v", ref, err)
				atomic.AddInt32(&missing, 1)
				return nil
			}
			response.Results[i] = params.EntityResult{
				Id:   ref.PreferredURL(),
				Meta: meta,
			}
			return nil
		})
	}
	// We never return an error from the Do function above, so no need to
	// check the error here.
	run.Wait()
	if missing == 0 {
		return response, nil
	}
	// We're missing some results - shuffle all the results down to
	// fill the gaps.
	j := 0
	for _, result := range response.Results {
		if result.Id != nil {
			response.Results[j] = result
			j++
		}
	}
	response.Results = response.Results[0:j]
	return response, nil
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
