// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4 // import "gopkg.in/juju/charmstore.v5-unstable/internal/v4"

import (
	"net/http"

	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"

	"gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"
	"gopkg.in/juju/charmstore.v5-unstable/internal/mempool"
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

// reqHandlerPool holds a cache of ReqHandlers to save
// on allocation time. When a handler is done with,
// it is put back into the pool.
var reqHandlerPool = mempool.Pool{
	New: func() interface{} {
		return newReqHandler()
	},
}

type Handler struct {
	*v5.Handler
}

type ReqHandler struct {
	*v5.ReqHandler
}

func New(pool *charmstore.Pool, config charmstore.ServerParams) Handler {
	return Handler{
		Handler: v5.New(pool, config),
	}
}

func (h Handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// When requests in this handler use router.RelativeURL, we want
	// the "absolute path" there to be interpreted relative to the
	// root of this handler, not the absolute root of the web server,
	// which may be abitrarily many levels up.
	req.RequestURI = req.URL.Path

	rh, err := h.NewReqHandler()
	if err != nil {
		router.WriteError(w, err)
		return
	}
	defer rh.Close()
	rh.ServeHTTP(w, req)
}

func NewAPIHandler(pool *charmstore.Pool, config charmstore.ServerParams) charmstore.HTTPCloseHandler {
	return New(pool, config)
}

// NewReqHandler fetchs a new instance of ReqHandler
// from h.Pool and returns it. The ReqHandler must
// be closed when finished with.
func (h *Handler) NewReqHandler() (ReqHandler, error) {
	store, err := h.Pool.RequestStore()
	if err != nil {
		if errgo.Cause(err) == charmstore.ErrTooManySessions {
			return ReqHandler{}, errgo.WithCausef(err, params.ErrServiceUnavailable, "")
		}
		return ReqHandler{}, errgo.Mask(err)
	}
	rh := reqHandlerPool.Get().(ReqHandler)
	rh.Handler = h.Handler
	rh.Store = store
	return rh, nil
}

func newReqHandler() ReqHandler {
	h := ReqHandler{
		ReqHandler: new(v5.ReqHandler),
	}
	handlers := v5.RouterHandlers(h.ReqHandler)
	// TODO mutate handlers appropriately.

	h.Router = router.New(handlers, h)
	return h
}

// ResolveURL implements router.Context.ResolveURL,
// ensuring that any resulting ResolvedURL always
// has a non-empty PreferredSeries field.
func (h ReqHandler) ResolveURL(url *charm.URL) (*router.ResolvedURL, error) {
	return resolveURL(h.Store, url)
}

// resolveURL implements URL resolving for the ReqHandler.
// It's defined as a separate function so it can be more
// easily unit-tested.
func resolveURL(store *charmstore.Store, url *charm.URL) (*router.ResolvedURL, error) {
	entity, err := store.FindBestEntity(url, "_id", "promulgated-revision", "supportedseries")
	if err != nil && errgo.Cause(err) != params.ErrNotFound {
		return nil, errgo.Mask(err)
	}
	if errgo.Cause(err) == params.ErrNotFound {
		return nil, noMatchingURLError(url)
	}
	rurl := &router.ResolvedURL{
		URL:                 *entity.URL,
		PromulgatedRevision: -1,
		Development:         url.Channel == charm.DevelopmentChannel,
	}
	if url.User == "" {
		rurl.PromulgatedRevision = entity.PromulgatedRevision
	}
	if rurl.URL.Series != "" {
		return rurl, nil
	}
	if url.Series != "" {
		rurl.PreferredSeries = url.Series
		return rurl, nil
	}
	if len(entity.SupportedSeries) == 0 {
		return nil, errgo.Newf("entity %q has no supported series", &rurl.URL)
	}
	rurl.PreferredSeries = entity.SupportedSeries[0]
	return rurl, nil
}

// Close closes the ReqHandler. This should always be called when the
// ReqHandler is done with.
func (h ReqHandler) Close() {
	h.Store.Close()
	h.Reset()
	reqHandlerPool.Put(h)
}

// StatsEnabled reports whether statistics should be gathered for
// the given HTTP request.
func StatsEnabled(req *http.Request) bool {
	return v5.StatsEnabled(req)
}

func noMatchingURLError(url *charm.URL) error {
	return errgo.WithCausef(nil, params.ErrNotFound, "no matching charm or bundle for %q", url)
}
