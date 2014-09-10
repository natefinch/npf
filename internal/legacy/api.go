// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package legacy

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/juju/errgo"
	"gopkg.in/juju/charm.v3"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/router"
	"github.com/juju/charmstore/internal/v4"
	"github.com/juju/charmstore/params"
)

type Handler struct {
	v4    *router.Handlers
	store *charmstore.Store
	mux   *http.ServeMux
}

func NewAPIHandler(store *charmstore.Store, config charmstore.ServerParams) http.Handler {
	h := &Handler{
		v4:    v4.New(store, config).Handlers(),
		store: store,
		mux:   http.NewServeMux(),
	}
	h.handle("/charm-info", http.HandlerFunc(h.serveCharmInfo))
	h.handle("/charm/", router.HandleErrors(h.serveCharm))
	return h
}

func (h *Handler) handle(path string, handler http.Handler) {
	prefix := strings.TrimSuffix(path, "/")
	h.mux.Handle(path, http.StripPrefix(prefix, handler))
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	h.mux.ServeHTTP(w, req)
}

func (h *Handler) serveCharmInfo(w http.ResponseWriter, req *http.Request) {
	router.WriteError(w, fmt.Errorf("charm-info not implemented"))
}

func (h *Handler) serveCharm(w http.ResponseWriter, req *http.Request) error {
	if req.Method != "GET" && req.Method != "HEAD" {
		return params.ErrMethodNotAllowed
	}
	url, err := charm.ParseReference(strings.TrimPrefix(req.URL.Path, "/"))
	if err != nil {
		return errgo.WithCausef(err, params.ErrNotFound, "")
	}
	if err := v4.ResolveURL(h.store, url); err != nil {
		// Note: preserve error cause from resolveURL.
		return errgo.Mask(err, errgo.Any)
	}
	return h.v4.Id["archive"](url, w, req)
}
