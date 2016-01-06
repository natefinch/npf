// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v5 // import "gopkg.in/juju/charmstore.v5-unstable/internal/v5"

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/juju/httprequest"
	"github.com/juju/loggo"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"
	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/bakery/checkers"
	"gopkg.in/macaroon-bakery.v1/httpbakery"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"gopkg.in/juju/charmstore.v5-unstable/audit"
	"gopkg.in/juju/charmstore.v5-unstable/internal/agent"
	"gopkg.in/juju/charmstore.v5-unstable/internal/cache"
	"gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"
	"gopkg.in/juju/charmstore.v5-unstable/internal/entitycache"
	"gopkg.in/juju/charmstore.v5-unstable/internal/identity"
	"gopkg.in/juju/charmstore.v5-unstable/internal/mempool"
	"gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"
	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
)

var logger = loggo.GetLogger("charmstore.internal.v5")

// reqHandlerPool holds a cache of ReqHandlers to save
// on allocation time. When a handler is done with,
// it is put back into the pool.
var reqHandlerPool = mempool.Pool{
	New: func() interface{} {
		return newReqHandler()
	},
}

type Handler struct {
	// Pool holds the store pool that the handler was created
	// with.
	Pool *charmstore.Pool

	config         charmstore.ServerParams
	locator        bakery.PublicKeyLocator
	identityClient *identity.Client

	// searchCache is a cache of search results keyed on the query
	// parameters of the search. It should only be used for searches
	// from unauthenticated users.
	searchCache *cache.Cache
}

// ReqHandler holds the context for a single HTTP request.
// It uses an independent mgo session from the handler
// used by other requests.
type ReqHandler struct {
	// Router holds the router that the ReqHandler will use
	// to route HTTP requests. This is usually set by
	// Handler.NewReqHandler to the result of RouterHandlers.
	Router *router.Router

	// Handler holds the Handler that the ReqHandler
	// is derived from.
	Handler *Handler

	// Store holds the charmstore Store instance
	// for the request.
	Store *charmstore.Store

	// auth holds the results of any authorization that
	// has been done on this request.
	auth authorization

	// cache holds the per-request entity cache.
	Cache *entitycache.Cache
}

const (
	DelegatableMacaroonExpiry = time.Minute
	reqHandlerCacheSize       = 50
)

func New(pool *charmstore.Pool, config charmstore.ServerParams) *Handler {
	h := &Handler{
		Pool:        pool,
		config:      config,
		searchCache: cache.New(config.SearchCacheMaxAge),
		locator:     config.PublicKeyLocator,
		identityClient: identity.NewClient(&identity.Params{
			URL:    config.IdentityAPIURL,
			Client: agent.NewClient(config.AgentUsername, config.AgentKey),
		}),
	}
	return h
}

// Close closes the Handler.
func (h *Handler) Close() {
}

var (
	RequiredEntityFields = []string{
		"baseurl",
		"user",
		"name",
		"revision",
		"series",
		"promulgated-revision",
		"development",
		"promulgated-url",
	}
	RequiredBaseEntityFields = []string{
		"acls",
		"public",
		"developmentacls",
		"promulgated",
	}
)

// NewReqHandler returns an instance of a *ReqHandler
// suitable for handling an HTTP request. After use, the ReqHandler.Close
// method should be called to close it.
//
// If no handlers are available, it returns an error with
// a charmstore.ErrTooManySessions cause.
func (h *Handler) NewReqHandler() (*ReqHandler, error) {
	store, err := h.Pool.RequestStore()
	if err != nil {
		if errgo.Cause(err) == charmstore.ErrTooManySessions {
			return nil, errgo.WithCausef(err, params.ErrServiceUnavailable, "")
		}
		return nil, errgo.Mask(err)
	}
	rh := reqHandlerPool.Get().(*ReqHandler)
	rh.Handler = h
	rh.Store = store
	rh.Cache = entitycache.New(store)
	rh.Cache.AddEntityFields(RequiredEntityFields...)
	rh.Cache.AddBaseEntityFields(RequiredBaseEntityFields...)
	return rh, nil
}

// RouterHandlers returns router handlers that will route requests to
// the given ReqHandler. This is provided so that different API versions
// can override selected parts of the handlers to serve their own API
// while still using ReqHandler to serve the majority of the API.
func RouterHandlers(h *ReqHandler) *router.Handlers {
	resolveId := h.ResolvedIdHandler
	authId := h.AuthIdHandler
	return &router.Handlers{
		Global: map[string]http.Handler{
			"changes/published":    router.HandleJSON(h.serveChangesPublished),
			"debug":                http.HandlerFunc(h.serveDebug),
			"debug/pprof/":         newPprofHandler(h),
			"debug/status":         router.HandleJSON(h.serveDebugStatus),
			"list":                 router.HandleJSON(h.serveList),
			"log":                  router.HandleErrors(h.serveLog),
			"search":               router.HandleJSON(h.serveSearch),
			"search/interesting":   http.HandlerFunc(h.serveSearchInteresting),
			"set-auth-cookie":      router.HandleErrors(h.serveSetAuthCookie),
			"stats/":               router.NotFoundHandler(),
			"stats/counter/":       router.HandleJSON(h.serveStatsCounter),
			"stats/update":         router.HandleErrors(h.serveStatsUpdate),
			"macaroon":             router.HandleJSON(h.serveMacaroon),
			"delegatable-macaroon": router.HandleJSON(h.serveDelegatableMacaroon),
			"whoami":               router.HandleJSON(h.serveWhoAmI),
		},
		Id: map[string]router.IdHandler{
			"archive":     h.serveArchive,
			"archive/":    resolveId(authId(h.serveArchiveFile)),
			"diagram.svg": resolveId(authId(h.serveDiagram)),
			"expand-id":   resolveId(authId(h.serveExpandId)),
			"icon.svg":    resolveId(authId(h.serveIcon)),
			"promulgate":  resolveId(h.serveAdminPromulgate),
			"publish":     h.servePublish,
			"readme":      resolveId(authId(h.serveReadMe)),
			"resources":   resolveId(authId(h.serveResources)),
		},
		Meta: map[string]router.BulkIncludeHandler{
			"archive-size":         h.EntityHandler(h.metaArchiveSize, "size"),
			"archive-upload-time":  h.EntityHandler(h.metaArchiveUploadTime, "uploadtime"),
			"bundle-machine-count": h.EntityHandler(h.metaBundleMachineCount, "bundlemachinecount"),
			"bundle-metadata":      h.EntityHandler(h.metaBundleMetadata, "bundledata"),
			"bundles-containing":   h.EntityHandler(h.metaBundlesContaining),
			"bundle-unit-count":    h.EntityHandler(h.metaBundleUnitCount, "bundleunitcount"),
			"charm-actions":        h.EntityHandler(h.metaCharmActions, "charmactions"),
			"charm-config":         h.EntityHandler(h.metaCharmConfig, "charmconfig"),
			"charm-metadata":       h.EntityHandler(h.metaCharmMetadata, "charmmeta"),
			"charm-related":        h.EntityHandler(h.metaCharmRelated, "charmprovidedinterfaces", "charmrequiredinterfaces"),
			"extra-info": h.puttableEntityHandler(
				h.metaExtraInfo,
				h.putMetaExtraInfo,
				"extrainfo",
			),
			"extra-info/": h.puttableEntityHandler(
				h.metaExtraInfoWithKey,
				h.putMetaExtraInfoWithKey,
				"extrainfo",
			),
			"common-info": h.puttableBaseEntityHandler(
				h.metaCommonInfo,
				h.putMetaCommonInfo,
				"commoninfo",
			),
			"common-info/": h.puttableBaseEntityHandler(
				h.metaCommonInfoWithKey,
				h.putMetaCommonInfoWithKey,
				"commoninfo",
			),
			"hash":             h.EntityHandler(h.metaHash, "blobhash"),
			"hash256":          h.EntityHandler(h.metaHash256, "blobhash256"),
			"id":               h.EntityHandler(h.metaId, "_id"),
			"id-name":          h.EntityHandler(h.metaIdName, "_id"),
			"id-user":          h.EntityHandler(h.metaIdUser, "_id"),
			"id-revision":      h.EntityHandler(h.metaIdRevision, "_id"),
			"id-series":        h.EntityHandler(h.metaIdSeries, "_id"),
			"manifest":         h.EntityHandler(h.metaManifest, "blobname"),
			"perm":             h.puttableBaseEntityHandler(h.metaPerm, h.putMetaPerm, "acls", "developmentacls"),
			"perm/":            h.puttableBaseEntityHandler(h.metaPermWithKey, h.putMetaPermWithKey, "acls", "developmentacls"),
			"promulgated":      h.baseEntityHandler(h.metaPromulgated, "promulgated"),
			"revision-info":    router.SingleIncludeHandler(h.metaRevisionInfo),
			"stats":            h.EntityHandler(h.metaStats),
			"supported-series": h.EntityHandler(h.metaSupportedSeries, "supportedseries"),
			"tags":             h.EntityHandler(h.metaTags, "charmmeta", "bundledata"),

			// endpoints not yet implemented:
			// "color": router.SingleIncludeHandler(h.metaColor),
		},
	}
}

// newReqHandler returns a new instance of the v4 API handler.
// The returned value has nil handler and store fields.
func newReqHandler() *ReqHandler {
	var h ReqHandler
	h.Router = router.New(RouterHandlers(&h), &h)
	return &h
}

// ServeHTTP implements http.Handler by first retrieving a
// request-specific instance of ReqHandler and
// calling ServeHTTP on that.
func (h *Handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
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

// ServeHTTP implements http.Handler by calling h.Router.ServeHTTP.
func (h *ReqHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	h.Router.ServeHTTP(w, req)
}

// NewAPIHandler returns a new Handler as an http Handler.
// It is defined for the convenience of callers that require a
// charmstore.NewAPIHandlerFunc.
func NewAPIHandler(pool *charmstore.Pool, config charmstore.ServerParams) charmstore.HTTPCloseHandler {
	return New(pool, config)
}

// Close closes the ReqHandler. This should always be called when the
// ReqHandler is done with.
func (h *ReqHandler) Close() {
	h.Store.Close()
	h.Cache.Close()
	h.Reset()
	reqHandlerPool.Put(h)
}

// Reset resets the request-specific fields of the ReqHandler
// so that it's suitable for putting back into a pool for reuse.
func (h *ReqHandler) Reset() {
	h.Store = nil
	h.Handler = nil
	h.Cache = nil
	h.auth = authorization{}
}

// ResolveURL implements router.Context.ResolveURL.
func (h *ReqHandler) ResolveURL(url *charm.URL) (*router.ResolvedURL, error) {
	return resolveURL(h.Cache, url)
}

// ResolveURL implements router.Context.ResolveURLs.
func (h *ReqHandler) ResolveURLs(urls []*charm.URL) ([]*router.ResolvedURL, error) {
	h.Cache.StartFetch(urls)
	rurls := make([]*router.ResolvedURL, len(urls))
	for i, url := range urls {
		var err error
		rurls[i], err = resolveURL(h.Cache, url)
		if err != nil && errgo.Cause(err) != params.ErrNotFound {
			return nil, err
		}
	}
	return rurls, nil
}

// WillIncludeMetadata implements router.Context.WillIncludeMetadata.
func (h *ReqHandler) WillIncludeMetadata(includes []string) {
}

// resolveURL implements URL resolving for the ReqHandler.
// It's defined as a separate function so it can be more
// easily unit-tested.
func resolveURL(cache *entitycache.Cache, url *charm.URL) (*router.ResolvedURL, error) {
	// We've added promulgated-url as a required field, so
	// we'll always get it from the Entity result.
	entity, err := cache.Entity(url)
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
	return rurl, nil
}

func noMatchingURLError(url *charm.URL) error {
	return errgo.WithCausef(nil, params.ErrNotFound, "no matching charm or bundle for %q", url)
}

type EntityHandlerFunc func(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error)

type baseEntityHandlerFunc func(entity *mongodoc.BaseEntity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error)

// EntityHandler returns a Handler that calls f with a *mongodoc.Entity that
// contains at least the given fields. It allows only GET requests.
func (h *ReqHandler) EntityHandler(f EntityHandlerFunc, fields ...string) router.BulkIncludeHandler {
	return h.puttableEntityHandler(f, nil, fields...)
}

func (h *ReqHandler) puttableEntityHandler(get EntityHandlerFunc, handlePut router.FieldPutFunc, fields ...string) router.BulkIncludeHandler {
	handleGet := func(doc interface{}, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
		edoc := doc.(*mongodoc.Entity)
		val, err := get(edoc, id, path, flags, req)
		return val, errgo.Mask(err, errgo.Any)
	}
	type entityHandlerKey struct{}
	return router.NewFieldIncludeHandler(router.FieldIncludeHandlerParams{
		Key:          entityHandlerKey{},
		Query:        h.entityQuery,
		Fields:       fields,
		HandleGet:    handleGet,
		HandlePut:    handlePut,
		Update:       h.updateEntity,
		UpdateSearch: h.updateSearch,
	})
}

// baseEntityHandler returns a Handler that calls f with a *mongodoc.Entity that
// contains at least the given fields. It allows only GET requests.
func (h *ReqHandler) baseEntityHandler(f baseEntityHandlerFunc, fields ...string) router.BulkIncludeHandler {
	return h.puttableBaseEntityHandler(f, nil, fields...)
}

func (h *ReqHandler) puttableBaseEntityHandler(get baseEntityHandlerFunc, handlePut router.FieldPutFunc, fields ...string) router.BulkIncludeHandler {
	handleGet := func(doc interface{}, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
		edoc := doc.(*mongodoc.BaseEntity)
		val, err := get(edoc, id, path, flags, req)
		return val, errgo.Mask(err, errgo.Any)
	}
	type baseEntityHandlerKey struct{}
	return router.NewFieldIncludeHandler(router.FieldIncludeHandlerParams{
		Key:          baseEntityHandlerKey{},
		Query:        h.baseEntityQuery,
		Fields:       fields,
		HandleGet:    handleGet,
		HandlePut:    handlePut,
		Update:       h.updateBaseEntity,
		UpdateSearch: h.updateSearchBase,
	})
}

func (h *ReqHandler) processEntries(entries []audit.Entry) {
	for _, e := range entries {
		h.addAudit(e)
	}
}

func (h *ReqHandler) updateBaseEntity(id *router.ResolvedURL, fields map[string]interface{}, entries []audit.Entry) error {
	if err := h.Store.UpdateBaseEntity(id, entityUpdateOp(fields)); err != nil {
		return errgo.Notef(err, "cannot update base entity %q", id)
	}
	h.processEntries(entries)
	return nil
}

func (h *ReqHandler) updateEntity(id *router.ResolvedURL, fields map[string]interface{}, entries []audit.Entry) error {
	err := h.Store.UpdateEntity(id, entityUpdateOp(fields))
	if err != nil {
		return errgo.Notef(err, "cannot update %q", &id.URL)
	}
	err = h.Store.UpdateSearchFields(id, fields)
	if err != nil {
		return errgo.Notef(err, "cannot update %q", &id.URL)
	}
	h.processEntries(entries)
	return nil
}

// entityUpdateOp returns a mongo update operation that
// sets the given fields. Any nil fields will be unset.
func entityUpdateOp(fields map[string]interface{}) bson.D {
	setFields := make(bson.D, 0, len(fields))
	var unsetFields bson.D
	for name, val := range fields {
		if val != nil {
			setFields = append(setFields, bson.DocElem{name, val})
		} else {
			unsetFields = append(unsetFields, bson.DocElem{name, val})
		}
	}
	op := make(bson.D, 0, 2)
	if len(setFields) > 0 {
		op = append(op, bson.DocElem{"$set", setFields})
	}
	if len(unsetFields) > 0 {
		op = append(op, bson.DocElem{"$unset", unsetFields})
	}
	return op
}

func (h *ReqHandler) updateSearch(id *router.ResolvedURL, fields map[string]interface{}) error {
	return h.Store.UpdateSearch(id)
}

// updateSearchBase updates the search records for all entities with
// the same base URL as the given id.
func (h *ReqHandler) updateSearchBase(id *router.ResolvedURL, fields map[string]interface{}) error {
	baseURL := id.URL
	baseURL.Series = ""
	baseURL.Revision = -1
	if err := h.Store.UpdateSearchBaseURL(&baseURL); err != nil {
		return errgo.Mask(err)
	}
	return nil
}

func (h *ReqHandler) baseEntityQuery(id *router.ResolvedURL, selector map[string]int, req *http.Request) (interface{}, error) {
	fields := make([]string, 0, len(selector))
	for k, v := range selector {
		if v == 0 {
			continue
		}
		fields = append(fields, k)
	}
	val, err := h.Cache.BaseEntity(&id.URL, fields...)
	if errgo.Cause(err) == params.ErrNotFound {
		return nil, errgo.WithCausef(nil, params.ErrNotFound, "no matching charm or bundle for %s", id)
	}
	if err != nil {
		return nil, errgo.Mask(err)
	}
	return val, nil
}

func (h *ReqHandler) entityQuery(id *router.ResolvedURL, selector map[string]int, req *http.Request) (interface{}, error) {
	val, err := h.Store.FindEntity(id, selector)
	if errgo.Cause(err) == params.ErrNotFound {
		return nil, errgo.WithCausef(nil, params.ErrNotFound, "no matching charm or bundle for %s", id)
	}
	if err != nil {
		return nil, errgo.Mask(err)
	}
	return val, nil
}

var errNotImplemented = errgo.Newf("method not implemented")

// GET /debug
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-debug
func (h *ReqHandler) serveDebug(w http.ResponseWriter, req *http.Request) {
	router.WriteError(w, errNotImplemented)
}

// POST id/resources/name.stream
// https://github.com/juju/charmstore/blob/v4/docs/API.md#post-idresourcesnamestream
//
// GET  id/resources/name.stream[-revision]/arch/filename
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idresourcesnamestream-revisionarchfilename
//
// PUT id/resources/[~user/]series/name.stream-revision/arch?sha256=hash
// https://github.com/juju/charmstore/blob/v4/docs/API.md#put-idresourcesuserseriesnamestream-revisionarchsha256hash
func (h *ReqHandler) serveResources(id *router.ResolvedURL, w http.ResponseWriter, req *http.Request) error {
	return errNotImplemented
}

// GET id/expand-id
// https://docs.google.com/a/canonical.com/document/d/1TgRA7jW_mmXoKH3JiwBbtPvQu7WiM6XMrz1wSrhTMXw/edit#bookmark=id.4xdnvxphb2si
func (h *ReqHandler) serveExpandId(id *router.ResolvedURL, w http.ResponseWriter, req *http.Request) error {
	baseURL := id.PreferredURL()
	baseURL.Revision = -1
	baseURL.Series = ""

	// baseURL now represents the base URL of the given id;
	// it will be a promulgated URL iff the original URL was
	// specified without a user, which will cause EntitiesQuery
	// to return entities that match appropriately.

	// Retrieve all the entities with the same base URL.
	// Note that we don't do any permission checking of the returned URLs.
	// This is because we know that the user is allowed to read at
	// least the resolved URL passed into serveExpandId.
	// If this does not specify "development", then no development
	// revisions will be chosen, so the single ACL already checked
	// is sufficient. If it *does* specify "development", then we assume
	// that the development ACLs are more restrictive than the
	// non-development ACLs, and given that, we can allow all
	// the URLs.
	q := h.Store.EntitiesQuery(baseURL).Select(bson.D{{"_id", 1}, {"promulgated-url", 1}, {"development", 1}})
	if id.PromulgatedRevision != -1 {
		q = q.Sort("-series", "-promulgated-revision")
	} else {
		q = q.Sort("-series", "-revision")
	}
	var docs []*mongodoc.Entity
	err := q.All(&docs)
	if err != nil && errgo.Cause(err) != mgo.ErrNotFound {
		return errgo.Mask(err)
	}

	// Collect all the expanded identifiers for each entity.
	response := make([]params.ExpandedId, 0, len(docs))
	for _, doc := range docs {
		url := doc.PreferredURL(id.PromulgatedRevision != -1)
		response = append(response, params.ExpandedId{Id: url.String()})
	}

	// Write the response in JSON format.
	return httprequest.WriteJSON(w, http.StatusOK, response)
}

func badRequestf(underlying error, f string, a ...interface{}) error {
	err := errgo.WithCausef(underlying, params.ErrBadRequest, f, a...)
	err.(*errgo.Err).SetLocation(1)
	return err
}

// GET id/meta/charm-metadata
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetacharm-metadata
func (h *ReqHandler) metaCharmMetadata(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return entity.CharmMeta, nil
}

// GET id/meta/bundle-metadata
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetabundle-metadata
func (h *ReqHandler) metaBundleMetadata(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return entity.BundleData, nil
}

// GET id/meta/bundle-unit-count
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetabundle-unit-count
func (h *ReqHandler) metaBundleUnitCount(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return bundleCount(entity.BundleUnitCount), nil
}

// GET id/meta/bundle-machine-count
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetabundle-machine-count
func (h *ReqHandler) metaBundleMachineCount(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return bundleCount(entity.BundleMachineCount), nil
}

func bundleCount(x *int) interface{} {
	if x == nil {
		return nil
	}
	return params.BundleCount{
		Count: *x,
	}
}

// GET id/meta/manifest
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetamanifest
func (h *ReqHandler) metaManifest(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	r, size, err := h.Store.BlobStore.Open(entity.BlobName)
	if err != nil {
		return nil, errgo.Notef(err, "cannot open archive data for %s", id)
	}
	defer r.Close()
	zipReader, err := zip.NewReader(charmstore.ReaderAtSeeker(r), size)
	if err != nil {
		return nil, errgo.Notef(err, "cannot read archive data for %s", id)
	}
	// Collect the files.
	manifest := make([]params.ManifestFile, 0, len(zipReader.File))
	for _, file := range zipReader.File {
		fileInfo := file.FileInfo()
		if fileInfo.IsDir() {
			continue
		}
		manifest = append(manifest, params.ManifestFile{
			Name: file.Name,
			Size: fileInfo.Size(),
		})
	}
	return manifest, nil
}

// GET id/meta/charm-actions
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetacharm-actions
func (h *ReqHandler) metaCharmActions(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return entity.CharmActions, nil
}

// GET id/meta/charm-config
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetacharm-config
func (h *ReqHandler) metaCharmConfig(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return entity.CharmConfig, nil
}

// GET id/meta/color
func (h *ReqHandler) metaColor(id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/archive-size
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetaarchive-size
func (h *ReqHandler) metaArchiveSize(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return &params.ArchiveSizeResponse{
		Size: entity.Size,
	}, nil
}

// GET id/meta/hash
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetahash
func (h *ReqHandler) metaHash(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return &params.HashResponse{
		Sum: entity.BlobHash,
	}, nil
}

// GET id/meta/hash256
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetahash256
func (h *ReqHandler) metaHash256(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	// TODO frankban: remove this lazy calculation after the cshash256
	// command is run in the production db. At that point, entities
	// always have their blobhash256 field populated, and there is no
	// need for this lazy evaluation anymore.
	if entity.BlobHash256 == "" {
		var err error
		if entity.BlobHash256, err = h.Store.UpdateEntitySHA256(id); err != nil {
			return nil, errgo.Notef(err, "cannot retrieve the SHA256 hash for entity %s", entity.URL)
		}
	}
	return &params.HashResponse{
		Sum: entity.BlobHash256,
	}, nil
}

// GET id/meta/tags
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetatags
func (h *ReqHandler) metaTags(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	var tags []string
	switch {
	case id.URL.Series == "bundle":
		tags = entity.BundleData.Tags
	case len(entity.CharmMeta.Tags) > 0:
		// TODO only return whitelisted tags.
		tags = entity.CharmMeta.Tags
	default:
		tags = entity.CharmMeta.Categories
	}
	return params.TagsResponse{
		Tags: tags,
	}, nil
}

// GET id/meta/stats/
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetastats
func (h *ReqHandler) metaStats(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {

	// Retrieve the aggregated downloads count for the specific revision.
	refresh, err := router.ParseBool(flags.Get("refresh"))
	if err != nil {
		return charmstore.SearchParams{}, badRequestf(err, "invalid refresh parameter")
	}
	counts, countsAllRevisions, err := h.Store.ArchiveDownloadCounts(id.PreferredURL(), refresh)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	// Return the response.
	return &params.StatsResponse{
		ArchiveDownloadCount: counts.Total,
		ArchiveDownload: params.StatsCount{
			Total: counts.Total,
			Day:   counts.LastDay,
			Week:  counts.LastWeek,
			Month: counts.LastMonth,
		},
		ArchiveDownloadAllRevisions: params.StatsCount{
			Total: countsAllRevisions.Total,
			Day:   countsAllRevisions.LastDay,
			Week:  countsAllRevisions.LastWeek,
			Month: countsAllRevisions.LastMonth,
		},
	}, nil
}

// GET id/meta/revision-info
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetarevision-info
func (h *ReqHandler) metaRevisionInfo(id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	searchURL := id.PreferredURL()
	searchURL.Revision = -1

	q := h.Store.EntitiesQuery(searchURL)
	if id.PromulgatedRevision != -1 {
		q = q.Sort("-promulgated-revision")
	} else {
		q = q.Sort("-revision")
	}
	var docs []*mongodoc.Entity
	if err := q.Select(bson.D{{"_id", 1}, {"promulgated-url", 1}}).All(&docs); err != nil {
		return "", errgo.Notef(err, "cannot get ids")
	}

	if len(docs) == 0 {
		return "", errgo.WithCausef(nil, params.ErrNotFound, "no matching charm or bundle for %s", id)
	}
	var response params.RevisionInfoResponse
	for _, doc := range docs {
		if id.PromulgatedRevision != -1 {
			response.Revisions = append(response.Revisions, doc.PromulgatedURL)
		} else {
			response.Revisions = append(response.Revisions, doc.URL)
		}
	}

	// Write the response in JSON format.
	return &response, nil
}

// GET id/meta/id-user
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetaid-user
func (h *ReqHandler) metaIdUser(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return params.IdUserResponse{
		User: id.PreferredURL().User,
	}, nil
}

// GET id/meta/id-series
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetaid-series
func (h *ReqHandler) metaIdSeries(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return params.IdSeriesResponse{
		Series: id.PreferredURL().Series,
	}, nil
}

// GET id/meta/id
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetaid
func (h *ReqHandler) metaId(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	u := id.PreferredURL()
	return params.IdResponse{
		Id:       u,
		User:     u.User,
		Series:   u.Series,
		Name:     u.Name,
		Revision: u.Revision,
	}, nil
}

// GET id/meta/id-name
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetaid-name
func (h *ReqHandler) metaIdName(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return params.IdNameResponse{
		Name: id.URL.Name,
	}, nil
}

// GET id/meta/id-revision
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetaid-revision
func (h *ReqHandler) metaIdRevision(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return params.IdRevisionResponse{
		Revision: id.PreferredURL().Revision,
	}, nil
}

// GET id/meta/supported-series
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetasupported-series
func (h *ReqHandler) metaSupportedSeries(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	if entity.URL.Series == "bundle" {
		return nil, nil
	}
	return &params.SupportedSeriesResponse{
		SupportedSeries: entity.SupportedSeries,
	}, nil
}

// GET id/meta/extra-info
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetaextra-info
func (h *ReqHandler) metaExtraInfo(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	// The extra-info is stored in mongo as simple byte
	// slices, so convert the values to json.RawMessages
	// so that the client will see the original JSON.
	m := make(map[string]*json.RawMessage)
	for key, val := range entity.ExtraInfo {
		jmsg := json.RawMessage(val)
		m[key] = &jmsg
	}
	return m, nil
}

// GET id/meta/extra-info/key
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetaextra-infokey
func (h *ReqHandler) metaExtraInfoWithKey(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	path = strings.TrimPrefix(path, "/")
	var data json.RawMessage = entity.ExtraInfo[path]
	if len(data) == 0 {
		return nil, nil
	}
	return &data, nil
}

// PUT id/meta/extra-info
// https://github.com/juju/charmstore/blob/v4/docs/API.md#put-idmetaextra-info
func (h *ReqHandler) putMetaExtraInfo(id *router.ResolvedURL, path string, val *json.RawMessage, updater *router.FieldUpdater, req *http.Request) error {
	var fields map[string]*json.RawMessage
	if err := json.Unmarshal(*val, &fields); err != nil {
		return errgo.Notef(err, "cannot unmarshal extra-info body")
	}
	// Check all the fields are OK before adding any fields to be updated.
	for key := range fields {
		if err := checkExtraInfoKey(key, "extra-info"); err != nil {
			return err
		}
	}
	for key, val := range fields {
		if val == nil {
			updater.UpdateField("extrainfo."+key, nil, nil)
		} else {
			updater.UpdateField("extrainfo."+key, *val, nil)
		}
	}
	return nil
}

var nullBytes = []byte("null")

// PUT id/meta/extra-info/key
// https://github.com/juju/charmstore/blob/v4/docs/API.md#put-idmetaextra-infokey
func (h *ReqHandler) putMetaExtraInfoWithKey(id *router.ResolvedURL, path string, val *json.RawMessage, updater *router.FieldUpdater, req *http.Request) error {
	key := strings.TrimPrefix(path, "/")
	if err := checkExtraInfoKey(key, "extra-info"); err != nil {
		return err
	}
	// If the user puts null, we treat that as if they want to
	// delete the field.
	if val == nil || bytes.Equal(*val, nullBytes) {
		updater.UpdateField("extrainfo."+key, nil, nil)
	} else {
		updater.UpdateField("extrainfo."+key, *val, nil)
	}
	return nil
}

// GET id/meta/common-info
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetacommon-info
func (h *ReqHandler) metaCommonInfo(entity *mongodoc.BaseEntity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	// The common-info is stored in mongo as simple byte
	// slices, so convert the values to json.RawMessages
	// so that the client will see the original JSON.
	m := make(map[string]*json.RawMessage)
	for key, val := range entity.CommonInfo {
		jmsg := json.RawMessage(val)
		m[key] = &jmsg
	}
	return m, nil
}

// GET id/meta/common-info/key
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetacommon-infokey
func (h *ReqHandler) metaCommonInfoWithKey(entity *mongodoc.BaseEntity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	path = strings.TrimPrefix(path, "/")
	var data json.RawMessage = entity.CommonInfo[path]
	if len(data) == 0 {
		return nil, nil
	}
	return &data, nil
}

// PUT id/meta/common-info
// https://github.com/juju/charmstore/blob/v4/docs/API.md#put-idmetacommon-info
func (h *ReqHandler) putMetaCommonInfo(id *router.ResolvedURL, path string, val *json.RawMessage, updater *router.FieldUpdater, req *http.Request) error {
	var fields map[string]*json.RawMessage
	if err := json.Unmarshal(*val, &fields); err != nil {
		return errgo.Notef(err, "cannot unmarshal common-info body")
	}
	// Check all the fields are OK before adding any fields to be updated.
	for key := range fields {
		if err := checkExtraInfoKey(key, "common-info"); err != nil {
			return err
		}
	}
	for key, val := range fields {
		if val == nil {
			updater.UpdateField("commoninfo."+key, nil, nil)
		} else {
			updater.UpdateField("commoninfo."+key, *val, nil)
		}
	}
	return nil
}

// PUT id/meta/common-info/key
// https://github.com/juju/charmstore/blob/v4/docs/API.md#put-idmetacommon-infokey
func (h *ReqHandler) putMetaCommonInfoWithKey(id *router.ResolvedURL, path string, val *json.RawMessage, updater *router.FieldUpdater, req *http.Request) error {
	key := strings.TrimPrefix(path, "/")
	if err := checkExtraInfoKey(key, "common-info"); err != nil {
		return err
	}
	// If the user puts null, we treat that as if they want to
	// delete the field.
	if val == nil || bytes.Equal(*val, nullBytes) {
		updater.UpdateField("commoninfo."+key, nil, nil)
	} else {
		updater.UpdateField("commoninfo."+key, *val, nil)
	}
	return nil
}

func checkExtraInfoKey(key string, field string) error {
	if strings.ContainsAny(key, "./$") {
		return errgo.WithCausef(nil, params.ErrBadRequest, "bad key for "+field)
	}
	return nil
}

// GET id/meta/perm
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetaperm
func (h *ReqHandler) metaPerm(entity *mongodoc.BaseEntity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	acls := entity.ACLs
	if id.Development {
		acls = entity.DevelopmentACLs
	}
	return params.PermResponse{
		Read:  acls.Read,
		Write: acls.Write,
	}, nil
}

// PUT id/meta/perm
// https://github.com/juju/charmstore/blob/v4/docs/API.md#put-idmeta
func (h *ReqHandler) putMetaPerm(id *router.ResolvedURL, path string, val *json.RawMessage, updater *router.FieldUpdater, req *http.Request) error {
	var perms params.PermRequest
	if err := json.Unmarshal(*val, &perms); err != nil {
		return errgo.Mask(err)
	}
	field := "acls"
	if id.Development {
		field = "developmentacls"
	} else {
		isPublic := false
		for _, p := range perms.Read {
			if p == params.Everyone {
				isPublic = true
				break
			}
		}
		updater.UpdateField("public", isPublic, nil)
	}
	updater.UpdateField(field+".read", perms.Read, &audit.Entry{
		Op:     audit.OpSetPerm,
		Entity: &id.URL,
		ACL: &audit.ACL{
			Read: perms.Read,
		},
	})
	updater.UpdateField(field+".write", perms.Write, &audit.Entry{
		Op:     audit.OpSetPerm,
		Entity: &id.URL,
		ACL: &audit.ACL{
			Write: perms.Write,
		},
	})
	updater.UpdateSearch()
	return nil
}

// GET id/meta/promulgated
// See https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetapromulgated
func (h *ReqHandler) metaPromulgated(entity *mongodoc.BaseEntity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return params.PromulgatedResponse{
		Promulgated: bool(entity.Promulgated),
	}, nil
}

// GET id/meta/perm/key
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetapermkey
func (h *ReqHandler) metaPermWithKey(entity *mongodoc.BaseEntity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	acls := entity.ACLs
	if id.Development {
		acls = entity.DevelopmentACLs
	}
	switch path {
	case "/read":
		return acls.Read, nil
	case "/write":
		return acls.Write, nil
	}
	return nil, errgo.WithCausef(nil, params.ErrNotFound, "unknown permission")
}

// PUT id/meta/perm/key
// https://github.com/juju/charmstore/blob/v4/docs/API.md#put-idmetapermkey
func (h *ReqHandler) putMetaPermWithKey(id *router.ResolvedURL, path string, val *json.RawMessage, updater *router.FieldUpdater, req *http.Request) error {
	var perms []string
	if err := json.Unmarshal(*val, &perms); err != nil {
		return errgo.Mask(err)
	}
	field := "acls"
	if id.Development {
		field = "developmentacls"
	}
	switch path {
	case "/read":
		updater.UpdateField(field+".read", perms, &audit.Entry{
			Op:     audit.OpSetPerm,
			Entity: &id.URL,
			ACL: &audit.ACL{
				Read: perms,
			},
		})
		if !id.Development {
			isPublic := false
			for _, p := range perms {
				if p == params.Everyone {
					isPublic = true
					break
				}
			}
			updater.UpdateField("public", isPublic, nil)
		}
		updater.UpdateSearch()
		return nil
	case "/write":
		updater.UpdateField(field+".write", perms, &audit.Entry{
			Op:     audit.OpSetPerm,
			Entity: &id.URL,
			ACL: &audit.ACL{
				Write: perms,
			},
		})
		return nil
	}
	return errgo.WithCausef(nil, params.ErrNotFound, "unknown permission")
}

// GET id/meta/archive-upload-time
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetaarchive-upload-time
func (h *ReqHandler) metaArchiveUploadTime(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return &params.ArchiveUploadTimeResponse{
		UploadTime: entity.UploadTime.UTC(),
	}, nil
}

// GET changes/published[?limit=$count][&from=$fromdate][&to=$todate]
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-changespublished
func (h *ReqHandler) serveChangesPublished(_ http.Header, r *http.Request) (interface{}, error) {
	start, stop, err := parseDateRange(r.Form)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Is(params.ErrBadRequest))
	}
	limit := -1
	if limitStr := r.Form.Get("limit"); limitStr != "" {
		limit, err = strconv.Atoi(limitStr)
		if err != nil || limit <= 0 {
			return nil, badRequestf(nil, "invalid 'limit' value")
		}
	}
	var tquery bson.D
	if !start.IsZero() {
		tquery = make(bson.D, 0, 2)
		tquery = append(tquery, bson.DocElem{
			Name:  "$gte",
			Value: start,
		})
	}
	if !stop.IsZero() {
		tquery = append(tquery, bson.DocElem{
			Name:  "$lte",
			Value: stop,
		})
	}
	var findQuery bson.D
	if len(tquery) > 0 {
		findQuery = bson.D{{"uploadtime", tquery}}
	}
	query := h.Store.DB.Entities().
		Find(findQuery).
		Sort("-uploadtime").
		Select(bson.D{{"_id", 1}, {"uploadtime", 1}})

	results := []params.Published{}
	var count int
	var entity mongodoc.Entity
	iter := query.Iter()
	for iter.Next(&entity) {
		// Ignore entities that aren't readable by the current user.
		if err := h.AuthorizeEntity(charmstore.EntityResolvedURL(&entity), r); err != nil {
			continue
		}
		results = append(results, params.Published{
			Id:          entity.URL,
			PublishTime: entity.UploadTime.UTC(),
		})
		count++
		if limit > 0 && limit <= count {
			break
		}
	}
	if err := iter.Close(); err != nil {
		return nil, errgo.Mask(err)
	}
	return results, nil
}

// GET /macaroon
// See https://github.com/juju/charmstore/blob/v4/docs/API.md#get-macaroon
func (h *ReqHandler) serveMacaroon(_ http.Header, _ *http.Request) (interface{}, error) {
	return h.newMacaroon()
}

// GET /delegatable-macaroon
// See https://github.com/juju/charmstore/blob/v4/docs/API.md#get-delegatable-macaroon
func (h *ReqHandler) serveDelegatableMacaroon(_ http.Header, req *http.Request) (interface{}, error) {
	values, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	entityIds := values["id"]
	// No entity ids, so we provide a macaroon that's good for any entity that the
	// user can access, as long as that entity doesn't have terms and conditions.
	if len(entityIds) == 0 {
		auth, err := h.authorize(req, []string{params.Everyone}, true, nil)
		if err != nil {
			return nil, errgo.Mask(err, errgo.Any)
		}
		if auth.Username == "" {
			return nil, errgo.WithCausef(nil, params.ErrForbidden, "delegatable macaroon is not obtainable using admin credentials")
		}
		// TODO propagate expiry time from macaroons in request.
		m, err := h.Store.Bakery.NewMacaroon("", nil, []checkers.Caveat{
			checkers.DeclaredCaveat(UsernameAttr, auth.Username),
			checkers.TimeBeforeCaveat(time.Now().Add(DelegatableMacaroonExpiry)),
			checkers.DenyCaveat(OpAccessCharmWithTerms),
		})
		if err != nil {
			return nil, errgo.Mask(err)
		}
		return m, nil
	}
	resolvedURLs := make([]*router.ResolvedURL, len(entityIds))
	for i, id := range entityIds {
		charmRef, err := charm.ParseURL(id)
		if err != nil {
			return nil, errgo.WithCausef(err, params.ErrBadRequest, `bad "id" parameter`)
		}
		resolvedURL, err := h.ResolveURL(charmRef)
		if err != nil {
			return nil, errgo.Mask(err)
		}
		resolvedURLs[i] = resolvedURL
	}

	// Note that we require authorization even though we allow
	// anyone to obtain a delegatable macaroon. This means
	// that we will be able to add the declared caveats to
	// the returned macaroon.
	auth, err := h.authorizeEntityAndTerms(req, resolvedURLs)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	if auth.Username == "" {
		return nil, errgo.WithCausef(nil, params.ErrForbidden, "delegatable macaroon is not obtainable using admin credentials")
	}

	resolvedURLstrings := make([]string, len(resolvedURLs))
	for i, resolvedURL := range resolvedURLs {
		resolvedURLstrings[i] = resolvedURL.URL.String()
	}

	// TODO propagate expiry time from macaroons in request.
	m, err := h.Store.Bakery.NewMacaroon("", nil, []checkers.Caveat{
		checkers.DeclaredCaveat(UsernameAttr, auth.Username),
		checkers.TimeBeforeCaveat(time.Now().Add(DelegatableMacaroonExpiry)),
		checkers.Caveat{Condition: "is-entity " + strings.Join(resolvedURLstrings, " ")},
	})
	if err != nil {
		return nil, errgo.Mask(err)
	}
	return m, nil
}

// GET /whoami
// See https://github.com/juju/charmstore/blob/v4/docs/API.md#whoami
func (h *ReqHandler) serveWhoAmI(_ http.Header, req *http.Request) (interface{}, error) {
	auth, err := h.authorize(req, []string{params.Everyone}, true, nil)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	if auth.Admin {
		return nil, errgo.WithCausef(nil, params.ErrForbidden, "admin credentials used")
	}
	groups, err := h.GroupsForUser(auth.Username)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	return params.WhoAmIResponse{
		User:   auth.Username,
		Groups: groups,
	}, nil
}

// PUT id/promulgate
// See https://github.com/juju/charmstore/blob/v4/docs/API.md#put-idpromulgate
func (h *ReqHandler) serveAdminPromulgate(id *router.ResolvedURL, w http.ResponseWriter, req *http.Request) error {
	if _, err := h.authorize(req, []string{PromulgatorsGroup}, false, id); err != nil {
		return errgo.Mask(err, errgo.Any)
	}
	if req.Method != "PUT" {
		return errgo.WithCausef(nil, params.ErrMethodNotAllowed, "%s not allowed", req.Method)
	}
	data, err := ioutil.ReadAll(req.Body)
	if err != nil {
		return errgo.Mask(err)
	}
	var promulgate params.PromulgateRequest
	if err := json.Unmarshal(data, &promulgate); err != nil {
		return errgo.WithCausef(err, params.ErrBadRequest, "")
	}
	if err := h.Store.SetPromulgated(id, promulgate.Promulgated); err != nil {
		return errgo.Mask(err, errgo.Any)
	}

	if promulgate.Promulgated {
		// Set write permissions for the non-development entity to promulgators
		// only, so that the user cannot just publish newer promulgated
		// versions of the charm or bundle. Promulgators are responsible of
		// reviewing and publishing subsequent revisions of this entity.
		if err := h.updateBaseEntity(id, map[string]interface{}{
			"acls.write": []string{PromulgatorsGroup},
		}, nil); err != nil {
			return errgo.Notef(err, "cannot set permissions for %q", id)
		}
	}

	// Build an audit entry for this promulgation.
	e := audit.Entry{
		Entity: &id.URL,
	}
	if promulgate.Promulgated {
		e.Op = audit.OpPromulgate
	} else {
		e.Op = audit.OpUnpromulgate
	}
	h.addAudit(e)

	return nil
}

// PUT id/publish
// See https://github.com/juju/charmstore/blob/v4/docs/API.md#put-idpublish
func (h *ReqHandler) servePublish(id *charm.URL, w http.ResponseWriter, req *http.Request) error {
	// Perform basic validation of the request.
	if req.Method != "PUT" {
		return errgo.WithCausef(nil, params.ErrMethodNotAllowed, "%s not allowed", req.Method)
	}
	if id.Channel != "" {
		return errgo.WithCausef(nil, params.ErrForbidden, "can only set publish on published URL, %q provided", id)
	}

	// Retrieve the requested action from the request body.
	var publish struct {
		params.PublishRequest `httprequest:",body"`
	}
	if err := httprequest.Unmarshal(httprequest.Params{Request: req}, &publish); err != nil {
		return errgo.WithCausef(err, params.ErrBadRequest, "cannot unmarshal publish request body")
	}

	// Retrieve the resolved URL for the entity to update. It will be referring
	// to the entity under development is the action is to publish a charm or
	// bundle, or the published one otherwise.
	url := *id
	if publish.Published {
		url = *id.WithChannel(charm.DevelopmentChannel)
	}
	rurl, err := h.Router.Context.ResolveURL(&url)
	if err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}

	// Authorize the operation: users must have write permissions on the
	// published charm or bundle.
	prurl := *rurl
	prurl.Development = false
	if err := h.AuthorizeEntity(&prurl, req); err != nil {
		return errgo.Mask(err, errgo.Any)
	}

	// Update the entity.
	if err := h.Store.SetDevelopment(rurl, !publish.Published); err != nil {
		return errgo.NoteMask(err, "cannot publish or unpublish charm or bundle", errgo.Is(params.ErrNotFound))
	}

	// Return information on the updated charm or bundle.
	rurl.Development = !publish.Published
	return httprequest.WriteJSON(w, http.StatusOK, &params.PublishResponse{
		Id:            rurl.UserOwnedURL(),
		PromulgatedId: rurl.PromulgatedURL(),
	})
}

// serveSetAuthCookie sets the provided macaroon slice as a cookie on the
// client.
func (h *ReqHandler) serveSetAuthCookie(w http.ResponseWriter, req *http.Request) error {
	// Allow cross-domain requests for the origin of this specific request so
	// that cookies can be set even if the request is xhr.
	w.Header().Set("Access-Control-Allow-Origin", req.Header.Get("Origin"))
	if req.Method != "PUT" {
		return errgo.WithCausef(nil, params.ErrMethodNotAllowed, "%s not allowed", req.Method)
	}
	var p params.SetAuthCookie
	decoder := json.NewDecoder(req.Body)
	if err := decoder.Decode(&p); err != nil {
		return errgo.Notef(err, "cannot unmarshal macaroons")
	}
	cookie, err := httpbakery.NewCookie(p.Macaroons)
	if err != nil {
		return errgo.Notef(err, "cannot create macaroons cookie")
	}
	cookie.Path = "/"
	cookie.Name = "macaroon-ui"
	http.SetCookie(w, cookie)
	return nil
}

// ResolvedIdHandler represents a HTTP handler that is invoked
// on a resolved entity id.
type ResolvedIdHandler func(id *router.ResolvedURL, w http.ResponseWriter, req *http.Request) error

// AuthIdHandler returns a ResolvedIdHandler that uses h.Router.Context.AuthorizeEntity to
// check that the client is authorized to perform the HTTP request method before
// invoking f.
//
// Note that it only accesses h.Router.Context when the returned
// handler is called.
func (h *ReqHandler) AuthIdHandler(f ResolvedIdHandler) ResolvedIdHandler {
	return func(id *router.ResolvedURL, w http.ResponseWriter, req *http.Request) error {
		if err := h.Router.Context.AuthorizeEntity(id, req); err != nil {
			return errgo.Mask(err, errgo.Any)
		}
		if err := f(id, w, req); err != nil {
			return errgo.Mask(err, errgo.Any)
		}
		return nil
	}
}

// ResolvedIdHandler returns an id handler that uses h.Router.Context.ResolveURL
// to resolves any entity ids before calling f with the resolved id.
//
// Note that it only accesses h.Router.Context when the returned
// handler is called.
func (h *ReqHandler) ResolvedIdHandler(f ResolvedIdHandler) router.IdHandler {
	return func(id *charm.URL, w http.ResponseWriter, req *http.Request) error {
		rid, err := h.Router.Context.ResolveURL(id)
		if err != nil {
			return errgo.Mask(err, errgo.Is(params.ErrNotFound))
		}
		return f(rid, w, req)
	}
}

var testAddAuditCallback func(e audit.Entry)

// addAudit delegates an audit entry to the store to record an audit log after
// it has set correctly the user doing the action.
func (h *ReqHandler) addAudit(e audit.Entry) {
	if h.auth.Username == "" && !h.auth.Admin {
		panic("No auth set in ReqHandler")
	}
	e.User = h.auth.Username
	if h.auth.Admin && e.User == "" {
		e.User = "admin"
	}
	h.Store.AddAudit(e)
	if testAddAuditCallback != nil {
		testAddAuditCallback(e)
	}
}
