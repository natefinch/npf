// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package legacy

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/router"
	"github.com/juju/charmstore/internal/v4"
)

type Handler struct {
	v4    *v4.Handler
	store *charmstore.Store
	mux   *http.ServeMux
}

func NewAPIHandler(store *charmstore.Store, config charmstore.ServerParams) http.Handler {
	h := &Handler{
		v4:    v4.New(store, config),
		store: store,
		mux:   http.NewServeMux(),
	}
	h.handle("/charm-info", h.serveCharmInfo)
	h.handle("/charm/", h.serveCharm)
	return h
}

func (h *Handler) handle(path string, handler http.HandlerFunc) {
	prefix := path
	if strings.HasSuffix(prefix, "/") {
		prefix = prefix[0 : len(prefix)-1]
	}
	h.mux.Handle(path, http.StripPrefix(prefix, handler))
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	h.mux.ServeHTTP(w, req)
}

func (h *Handler) serveCharmInfo(w http.ResponseWriter, req *http.Request) {
	router.WriteError(w, fmt.Errorf("charm-info not implemented"))
}

func (h *Handler) serveCharm(w http.ResponseWriter, req *http.Request) {
	router.WriteError(w, fmt.Errorf("charm not implemented"))
}
