// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4

import (
	"net/http"
	"strconv"

	"gopkg.in/errgo.v1"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/router"
	"github.com/juju/charmstore/params"
)

// GET search[?text=text][&autocomplete=1][&filter=value…][&limit=limit][&include=meta]
// http://tinyurl.com/qzobc69
func (h *Handler) serveSearch(_ http.Header, req *http.Request) (interface{}, error) {
	sp, err := parseSearchParams(req)
	if err != nil {
		return "", err
	}
	// perform query
	results, err := h.store.Search(sp)
	if err != nil {
		return nil, errgo.Notef(err, "error performing search")
	}
	response := params.SearchResponse{
		SearchTime: results.SearchTime,
		Total:      results.Total,
		Results:    make([]params.SearchResult, 0, len(results.Results)),
	}
	// TODO(mhilton) collect the metadata concurrently.
	for _, ref := range results.Results {
		meta, err := h.Router.GetMetadata(ref, sp.Include)
		if err != nil {
			// Unfortunately it is possible to get errors here due to
			// internal inconsistency, so rather than throwing away
			// all the search results, we just log the error and move on.
			logger.Errorf("cannot retrieve metadata for %v: %v", ref, err)
			continue
		}
		response.Results = append(response.Results, params.SearchResult{
			Id:   ref,
			Meta: meta,
		})
	}
	return response, nil
}

// GET search/interesting[?limit=limit][&include=meta]
// http://tinyurl.com/ntmdrg8
func (h *Handler) serveSearchInteresting(w http.ResponseWriter, req *http.Request) {
	router.WriteError(w, errNotImplemented)
}

// parseSearchParms extracts the search paramaters from the request
func parseSearchParams(req *http.Request) (charmstore.SearchParams, error) {
	sp := charmstore.SearchParams{}
	var err error
	for k, v := range req.Form {
		switch k {
		case "text":
			sp.Text = v[0]
		case "autocomplete":
			sp.AutoComplete, err = parseBool(v[0])
			if err != nil {
				return charmstore.SearchParams{}, badRequestf(err, "invalid autocomplete parameter")
			}
		case "limit":
			sp.Limit, err = strconv.Atoi(v[0])
			if err != nil {
				return charmstore.SearchParams{}, badRequestf(err, "invalid limit parameter: could not parse integer")
			}
			if sp.Limit < 1 {
				return charmstore.SearchParams{}, badRequestf(nil, "invalid limit parameter: expected integer greater than zero")
			}
		case "include":
			for _, s := range v {
				if s != "" {
					sp.Include = append(sp.Include, s)
				}
			}
		case "description", "name", "owner", "provides", "requires", "series", "summary", "tags", "type":
			if sp.Filters == nil {
				sp.Filters = make(map[string][]string)
			}
			sp.Filters[k] = v
		case "skip":
			sp.Skip, err = strconv.Atoi(v[0])
			if err != nil {
				return charmstore.SearchParams{}, badRequestf(err, "invalid skip parameter: could not parse integer")
			}
			if sp.Skip < 0 {
				return charmstore.SearchParams{}, badRequestf(nil, "invalid skip parameter: expected non-negative integer")
			}
		default:
			return charmstore.SearchParams{}, badRequestf(nil, "invalid parameter: %s", k)
		}
	}

	return sp, nil
}