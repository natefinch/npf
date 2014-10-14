// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4

import ( //	"encoding/json"
	"net/http"
	"strconv"

	"gopkg.in/errgo.v1"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/router"
	"github.com/juju/charmstore/params"
)

// GET search[?text=text][&autocomplete=1][&filter=value…][&limit=limit][&include=meta]
// http://tinyurl.com/qzobc69
func (h *Handler) serveSearch(w http.ResponseWriter, req *http.Request) (interface{}, error) {
	sp, err := parseSearchParams(req)
	if err != nil {
		return "", err
	}
	// perform query
	ids, err := h.store.Search(sp.SearchParams)
	if err != nil {
		router.WriteError(w, errgo.Notef(err, "error performing search"))
	}
	results := []params.SearchResult{}
	for _, id := range ids {
		sr := params.SearchResult{Id: id}
		// TODO (mhilton) get metadata
		results = append(results, sr)
	}
	return results, nil
}

// GET search/interesting[?limit=limit][&include=meta]
// http://tinyurl.com/ntmdrg8
func (h *Handler) serveSearchInteresting(w http.ResponseWriter, req *http.Request) {
	router.WriteError(w, errNotImplemented)
}

type searchParams struct {
	charmstore.SearchParams
	Include []string
}

// parseSearchParms extracts the search paramaters from the request
func parseSearchParams(req *http.Request) (searchParams, error) {
	var sp searchParams
	var err error
	for k, v := range req.Form {
		switch k {
		case "text":
			sp.Text = v[0]
		case "autocomplete":
			sp.AutoComplete, err = parseBool(v[0])
			if err != nil {
				return searchParams{}, badRequestf(err, "invalid autocomplete parameter")
			}
		case "limit":
			sp.Limit, err = strconv.Atoi(v[0])
			if err != nil {
				return searchParams{}, badRequestf(err, "invalid limit parameter: could not parse integer")
			}
			if sp.Limit < 1 {
				return searchParams{}, badRequestf(err, "invalid limit parameter: expected integer greater than zero")
			}
		case "include":
			sp.Include = v
		default:
			if sp.Filters == nil {
				sp.Filters = map[string][]string{}
			}
			sp.Filters[k] = v
		}
	}

	return sp, nil
}
