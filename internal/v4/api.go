// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4 // import "gopkg.in/juju/charmstore.v5-unstable/internal/v4"

import (
	"net/http"

	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"

	"gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"
	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
	"gopkg.in/juju/charmstore.v5-unstable/internal/v5"
)

const (
	PromulgatorsGroup         = v5.PromulgatorsGroup
	UsernameAttr              = v5.UsernameAttr
	DelegatableMacaroonExpiry = v5.DelegatableMacaroonExpiry
	DefaultIcon               = v5.DefaultIcon
	ArchiveCachePublicMaxAge  = v5.ArchiveCachePublicMaxAge
)

type Handler struct {
	*v5.Handler
}

type ReqHandler struct {
	*v5.ReqHandler
}

func New(pool *charmstore.Pool, config charmstore.ServerParams) Handler {
	h := Handler{
		Handler: v5.New(pool, config),
	}
	// TODO Set h.ResolveURL here.
	return h
}

func NewAPIHandler(pool *charmstore.Pool, config charmstore.ServerParams) charmstore.HTTPCloseHandler {
	return New(pool, config)
}

func (h *Handler) NewReqHandler() (ReqHandler, error) {
	v5h, err := h.Handler.NewReqHandler()
	if err != nil {
		return ReqHandler{}, errgo.Mask(err, errgo.Is(charmstore.ErrTooManySessions))
	}
	return ReqHandler{
		ReqHandler: v5h,
	}, nil
}

// StatsEnabled reports whether statistics should be gathered for
// the given HTTP request.
func StatsEnabled(req *http.Request) bool {
	return v5.StatsEnabled(req)
}

func ResolveURL(store *charmstore.Store, url *charm.URL) (*router.ResolvedURL, error) {
	// TODO modify this so that it always resolves the series.
	return v5.ResolveURL(store, url)
}
