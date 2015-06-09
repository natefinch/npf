// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4 // import "gopkg.in/juju/charmstore.v5-unstable/internal/v4"

import (
	"archive/zip"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/juju/loggo"
	"github.com/juju/utils/jsonhttp"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v0/csclient/params"
	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/bakery/checkers"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"gopkg.in/juju/charmstore.v5-unstable/audit"
	"gopkg.in/juju/charmstore.v5-unstable/internal/agent"
	"gopkg.in/juju/charmstore.v5-unstable/internal/cache"
	"gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"
	"gopkg.in/juju/charmstore.v5-unstable/internal/identity"
	"gopkg.in/juju/charmstore.v5-unstable/internal/mempool"
	"gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"
	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
)

var logger = loggo.GetLogger("charmstore.internal.v4")

// reqHandlerPool holds a cache of ReqHandlers to save
// on allocation time. When a handler is done with,
// it is put back into the pool.
var reqHandlerPool = mempool.Pool{
	New: func() interface{} {
		return newReqHandler()
	},
}

type Handler struct {
	config         charmstore.ServerParams
	locator        *bakery.PublicKeyRing
	identityClient *identity.Client
	pool           *charmstore.Pool

	// searchCache is a cache of search results keyed on the query
	// parameters of the search. It should only be used for searches
	// from unauthenticated users.
	searchCache *cache.Cache
}

// ReqHandler holds the context for a single HTTP request.
// It uses an independent mgo session from the handler
// used by other requests.
type ReqHandler struct {
	*router.Router
	handler *Handler

	// Store holds the charmstore Store instance
	// for the request.
	Store *charmstore.Store

	// auth holds the results of any authorization that
	// has been done on this request.
	auth authorization
}

const (
	delegatableMacaroonExpiry = time.Minute
	reqHandlerCacheSize       = 50
)

func New(pool *charmstore.Pool, config charmstore.ServerParams) *Handler {
	h := &Handler{
		pool:        pool,
		config:      config,
		searchCache: cache.New(config.SearchCacheMaxAge),
		locator:     bakery.NewPublicKeyRing(),
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

// NewReqHandler returns an instance of a *ReqHandler
// suitable for handling an HTTP request. After use, the ReqHandler.Close
// method should be called to close it.
//
// If no handlers are available, it returns an error with
// a charmstore.ErrTooManySessions cause.
func (h *Handler) NewReqHandler() (*ReqHandler, error) {
	store, err := h.pool.RequestStore()
	if err != nil {
		if errgo.Cause(err) == charmstore.ErrTooManySessions {
			return nil, errgo.WithCausef(err, params.ErrServiceUnavailable, "")
		}
		return nil, errgo.Mask(err)
	}
	rh := reqHandlerPool.Get().(*ReqHandler)
	rh.handler = h
	rh.Store = store
	return rh, nil
}

// newReqHandler returns a new instance of the v4 API handler.
// The returned value has nil handler and store fields.
func newReqHandler() *ReqHandler {
	var h ReqHandler
	h.Router = router.New(&router.Handlers{
		Global: map[string]http.Handler{
			"changes/published":    router.HandleJSON(h.serveChangesPublished),
			"debug":                http.HandlerFunc(h.serveDebug),
			"debug/pprof/":         newPprofHandler(&h),
			"debug/status":         router.HandleJSON(h.serveDebugStatus),
			"log":                  router.HandleErrors(h.serveLog),
			"search":               router.HandleJSON(h.serveSearch),
			"search/interesting":   http.HandlerFunc(h.serveSearchInteresting),
			"stats/":               router.NotFoundHandler(),
			"stats/counter/":       router.HandleJSON(h.serveStatsCounter),
			"macaroon":             router.HandleJSON(h.serveMacaroon),
			"delegatable-macaroon": router.HandleJSON(h.serveDelegatableMacaroon),
		},
		Id: map[string]router.IdHandler{
			"archive":     h.serveArchive,
			"archive/":    h.resolveId(h.authId(h.serveArchiveFile)),
			"diagram.svg": h.resolveId(h.authId(h.serveDiagram)),
			"expand-id":   h.resolveId(h.authId(h.serveExpandId)),
			"icon.svg":    h.resolveId(h.authId(h.serveIcon)),
			"readme":      h.resolveId(h.authId(h.serveReadMe)),
			"resources":   h.resolveId(h.authId(h.serveResources)),
			"promulgate":  h.resolveId(h.serveAdminPromulgate),
		},
		Meta: map[string]router.BulkIncludeHandler{
			"archive-size":         h.entityHandler(h.metaArchiveSize, "size"),
			"archive-upload-time":  h.entityHandler(h.metaArchiveUploadTime, "uploadtime"),
			"bundle-machine-count": h.entityHandler(h.metaBundleMachineCount, "bundlemachinecount"),
			"bundle-metadata":      h.entityHandler(h.metaBundleMetadata, "bundledata"),
			"bundles-containing":   h.entityHandler(h.metaBundlesContaining),
			"bundle-unit-count":    h.entityHandler(h.metaBundleUnitCount, "bundleunitcount"),
			"charm-actions":        h.entityHandler(h.metaCharmActions, "charmactions"),
			"charm-config":         h.entityHandler(h.metaCharmConfig, "charmconfig"),
			"charm-metadata":       h.entityHandler(h.metaCharmMetadata, "charmmeta"),
			"charm-related":        h.entityHandler(h.metaCharmRelated, "charmprovidedinterfaces", "charmrequiredinterfaces"),
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
			"hash":          h.entityHandler(h.metaHash, "blobhash"),
			"hash256":       h.entityHandler(h.metaHash256, "blobhash256"),
			"id":            h.entityHandler(h.metaId, "_id"),
			"id-name":       h.entityHandler(h.metaIdName, "_id"),
			"id-user":       h.entityHandler(h.metaIdUser, "_id"),
			"id-revision":   h.entityHandler(h.metaIdRevision, "_id"),
			"id-series":     h.entityHandler(h.metaIdSeries, "_id"),
			"manifest":      h.entityHandler(h.metaManifest, "blobname"),
			"perm":          h.puttableBaseEntityHandler(h.metaPerm, h.putMetaPerm, "acls"),
			"perm/":         h.puttableBaseEntityHandler(h.metaPermWithKey, h.putMetaPermWithKey, "acls"),
			"promulgated":   h.baseEntityHandler(h.metaPromulgated, "promulgated"),
			"revision-info": router.SingleIncludeHandler(h.metaRevisionInfo),
			"stats":         h.entityHandler(h.metaStats),
			"tags":          h.entityHandler(h.metaTags, "charmmeta", "bundledata"),

			// endpoints not yet implemented:
			// "color": router.SingleIncludeHandler(h.metaColor),
		},
	}, h.resolveURL, h.AuthorizeEntity, h.entityExists)
	return &h
}

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
	rh.Router.ServeHTTP(w, req)
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
	h.Store = nil
	h.handler = nil
	h.auth = authorization{}
	reqHandlerPool.Put(h)
}

// ResolveURL resolves the series and revision of the given URL if either is
// unspecified by filling them out with information retrieved from the store.
func ResolveURL(store *charmstore.Store, url *charm.Reference) (*router.ResolvedURL, error) {
	if url.Series != "" && url.Revision != -1 && url.User != "" {
		// URL is fully specified; no need for a database lookup.
		return &router.ResolvedURL{
			URL:                 *url,
			PromulgatedRevision: -1,
		}, nil
	}
	entity, err := store.FindBestEntity(url, "_id", "promulgated-revision")
	if err != nil && errgo.Cause(err) != params.ErrNotFound {
		return nil, errgo.Mask(err)
	}
	if errgo.Cause(err) == params.ErrNotFound {
		return nil, noMatchingURLError(url)
	}
	if url.User == "" {
		return &router.ResolvedURL{
			URL:                 *entity.URL,
			PromulgatedRevision: entity.PromulgatedRevision,
		}, nil
	}
	return &router.ResolvedURL{
		URL:                 *entity.URL,
		PromulgatedRevision: -1,
	}, nil
}

func noMatchingURLError(url *charm.Reference) error {
	return errgo.WithCausef(nil, params.ErrNotFound, "no matching charm or bundle for %q", url)
}

func (h *ReqHandler) resolveURL(url *charm.Reference) (*router.ResolvedURL, error) {
	return ResolveURL(h.Store, url)
}

type entityHandlerFunc func(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error)

type baseEntityHandlerFunc func(entity *mongodoc.BaseEntity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error)

// entityHandler returns a Handler that calls f with a *mongodoc.Entity that
// contains at least the given fields. It allows only GET requests.
func (h *ReqHandler) entityHandler(f entityHandlerFunc, fields ...string) router.BulkIncludeHandler {
	return h.puttableEntityHandler(f, nil, fields...)
}

func (h *ReqHandler) puttableEntityHandler(get entityHandlerFunc, handlePut router.FieldPutFunc, fields ...string) router.BulkIncludeHandler {
	handleGet := func(doc interface{}, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
		edoc := doc.(*mongodoc.Entity)
		val, err := get(edoc, id, path, flags, req)
		return val, errgo.Mask(err, errgo.Any)
	}
	type entityHandlerKey struct{}
	return router.FieldIncludeHandler(router.FieldIncludeHandlerParams{
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
	return router.FieldIncludeHandler(router.FieldIncludeHandlerParams{
		Key:          baseEntityHandlerKey{},
		Query:        h.baseEntityQuery,
		Fields:       fields,
		HandleGet:    handleGet,
		HandlePut:    handlePut,
		Update:       h.updateBaseEntity,
		UpdateSearch: h.updateSearchBase,
	})
}

func (h *ReqHandler) updateBaseEntity(id *router.ResolvedURL, fields map[string]interface{}) error {
	if err := h.Store.UpdateBaseEntity(id, bson.D{{"$set", fields}}); err != nil {
		return errgo.Notef(err, "cannot update base entity %q", id)
	}
	return nil
}

func (h *ReqHandler) updateEntity(id *router.ResolvedURL, fields map[string]interface{}) error {
	err := h.Store.UpdateEntity(id, bson.D{{"$set", fields}})
	if err != nil {
		return errgo.Notef(err, "cannot update %q", &id.URL)
	}
	err = h.Store.UpdateSearchFields(id, fields)
	if err != nil {
		return errgo.Notef(err, "cannot update %q", &id.URL)
	}
	return nil
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

func (h *ReqHandler) entityExists(id *router.ResolvedURL, req *http.Request) (bool, error) {
	// TODO add http.Request to entityExists params
	_, err := h.entityQuery(id, nil, req)
	if errgo.Cause(err) == params.ErrNotFound {
		return false, nil
	}
	if err != nil {
		return false, errgo.Mask(err)
	}
	return true, nil
}

func (h *ReqHandler) baseEntityQuery(id *router.ResolvedURL, selector map[string]int, req *http.Request) (interface{}, error) {
	fields := make([]string, 0, len(selector))
	for k, v := range selector {
		if v == 0 {
			continue
		}
		fields = append(fields, k)
	}
	val, err := h.Store.FindBaseEntity(&id.URL, fields...)
	if errgo.Cause(err) == params.ErrNotFound {
		return nil, errgo.WithCausef(nil, params.ErrNotFound, "no matching charm or bundle for %s", id)
	}
	if err != nil {
		return nil, errgo.Mask(err)
	}
	return val, nil
}

func (h *ReqHandler) entityQuery(id *router.ResolvedURL, selector map[string]int, req *http.Request) (interface{}, error) {
	val, err := h.Store.FindEntity(id, fieldsFromSelector(selector)...)
	if errgo.Cause(err) == params.ErrNotFound {
		return nil, errgo.WithCausef(nil, params.ErrNotFound, "no matching charm or bundle for %s", id)
	}
	if err != nil {
		return nil, errgo.Mask(err)
	}
	return val, nil
}

var ltsReleases = map[string]bool{
	"lucid":   true,
	"precise": true,
	"trusty":  true,
}

func fieldsFromSelector(selector map[string]int) []string {
	fields := make([]string, 0, len(selector))
	for k, v := range selector {
		if v == 0 {
			continue
		}
		fields = append(fields, k)
	}
	return fields
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
func (h *ReqHandler) serveResources(id *router.ResolvedURL, _ bool, w http.ResponseWriter, req *http.Request) error {
	return errNotImplemented
}

// GET id/expand-id
// https://docs.google.com/a/canonical.com/document/d/1TgRA7jW_mmXoKH3JiwBbtPvQu7WiM6XMrz1wSrhTMXw/edit#bookmark=id.4xdnvxphb2si
func (h *ReqHandler) serveExpandId(id *router.ResolvedURL, _ bool, w http.ResponseWriter, req *http.Request) error {
	baseURL := id.PreferredURL()
	baseURL.Revision = -1
	baseURL.Series = ""

	// baseURL now represents the base URL of the given id;
	// it will be a promulgated URL iff the original URL was
	// specified without a user, which will cause EntitiesQuery
	// to return entities that match appropriately.

	// Retrieve all the entities with the same base URL.
	q := h.Store.EntitiesQuery(baseURL).Select(bson.D{{"_id", 1}, {"promulgated-url", 1}})
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

	// A not found error should have been already returned by the router in the
	// case a partial id is provided. Here we do the same for the case when
	// a fully qualified URL is provided, but no matching entities are found.
	if len(docs) == 0 {
		return noMatchingURLError(id.PreferredURL())
	}

	// Collect all the expanded identifiers for each entity.
	response := make([]params.ExpandedId, 0, len(docs))
	for _, doc := range docs {
		url := doc.PreferredURL(id.PromulgatedRevision != -1)
		response = append(response, params.ExpandedId{Id: url.String()})
	}

	// Write the response in JSON format.
	return jsonhttp.WriteJSON(w, http.StatusOK, response)
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
	counts, countsAllRevisions, err := h.Store.ArchiveDownloadCounts(id.PreferredURL())
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
		return errgo.Notef(err, "cannot unmarshal extra info body")
	}
	// Check all the fields are OK before adding any fields to be updated.
	for key := range fields {
		if err := checkExtraInfoKey(key); err != nil {
			return err
		}
	}
	for key, val := range fields {
		updater.UpdateField("extrainfo."+key, *val)
	}
	return nil
}

// PUT id/meta/extra-info/key
// https://github.com/juju/charmstore/blob/v4/docs/API.md#put-idmetaextra-infokey
func (h *ReqHandler) putMetaExtraInfoWithKey(id *router.ResolvedURL, path string, val *json.RawMessage, updater *router.FieldUpdater, req *http.Request) error {
	key := strings.TrimPrefix(path, "/")
	if err := checkExtraInfoKey(key); err != nil {
		return err
	}
	updater.UpdateField("extrainfo."+key, *val)
	return nil
}

func checkExtraInfoKey(key string) error {
	if strings.ContainsAny(key, "./$") {
		return errgo.WithCausef(nil, params.ErrBadRequest, "bad key for extra-info")
	}
	return nil
}

// GET id/meta/perm
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetaperm
func (h *ReqHandler) metaPerm(entity *mongodoc.BaseEntity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return params.PermResponse{
		Read:  entity.ACLs.Read,
		Write: entity.ACLs.Write,
	}, nil
}

// PUT id/meta/perm
// https://github.com/juju/charmstore/blob/v4/docs/API.md#put-idmeta
func (h *ReqHandler) putMetaPerm(id *router.ResolvedURL, path string, val *json.RawMessage, updater *router.FieldUpdater, req *http.Request) error {
	var perms params.PermRequest
	if err := json.Unmarshal(*val, &perms); err != nil {
		return errgo.Mask(err)
	}
	isPublic := false
	for _, p := range perms.Read {
		if p == params.Everyone {
			isPublic = true
			break
		}
	}

	updater.UpdateField("acls.read", perms.Read)
	updater.UpdateField("public", isPublic)
	updater.UpdateField("acls.write", perms.Write)

	// This is currently wrong as the updater will fire later and might fail.
	// TODO: Associate the audit entry with the FieldUpdater
	h.addAuditSetPerms(id, perms.Read, perms.Write)

	updater.UpdateSearch()
	return nil
}

var testAddAuditCallback func(e audit.Entry)

// addAuditSetPerms adds an audit entry recording that the permissions of the given
// entity have been set to the given ACLs.
func (h *ReqHandler) addAuditSetPerms(id *router.ResolvedURL, read, write []string) {
	if h.auth.Username == "" && !h.auth.Admin {
		panic("No auth set in ReqHandler")
	}
	e := audit.Entry{
		Op:     audit.OpSetPerm,
		Entity: &id.URL,
		ACL: &audit.ACL{
			Read:  read,
			Write: write,
		},
		User: h.auth.Username,
	}
	if h.auth.Admin && e.User == "" {
		e.User = "admin"
	}
	h.Store.AddAudit(e)

	if testAddAuditCallback != nil {
		testAddAuditCallback(e)
	}
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
	switch path {
	case "/read":
		return entity.ACLs.Read, nil
	case "/write":
		return entity.ACLs.Write, nil
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
	isPublic := false
	for _, p := range perms {
		if p == params.Everyone {
			isPublic = true
			break
		}
	}
	switch path {
	case "/read":
		updater.UpdateField("acls.read", perms)
		updater.UpdateField("public", isPublic)
		h.addAuditSetPerms(id, perms, nil)
		updater.UpdateSearch()
		return nil
	case "/write":
		updater.UpdateField("acls.write", perms)
		h.addAuditSetPerms(id, nil, perms)
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

type PublishedResponse struct {
	Id        *charm.Reference
	Published time.Time
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
	if limit != -1 {
		query = query.Limit(limit)
	}

	results := []params.Published{}
	var entity mongodoc.Entity
	for iter := query.Iter(); iter.Next(&entity); {
		results = append(results, params.Published{
			Id:          entity.URL,
			PublishTime: entity.UploadTime.UTC(),
		})
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
	// Note that we require authorization even though we allow
	// anyone to obtain a delegatable macaroon. This means
	// that we will be able to add the declared caveats to
	// the returned macaroon.
	auth, err := h.authorize(req, []string{params.Everyone}, true, nil)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	if auth.Username == "" {
		return nil, errgo.WithCausef(nil, params.ErrForbidden, "delegatable macaroon is not obtainable using admin credentials")
	}
	// TODO propagate expiry time from macaroons in request.
	m, err := h.Store.Bakery.NewMacaroon("", nil, []checkers.Caveat{
		checkers.DeclaredCaveat(usernameAttr, auth.Username),
		checkers.TimeBeforeCaveat(time.Now().Add(delegatableMacaroonExpiry)),
	})
	if err != nil {
		return nil, errgo.Mask(err)
	}
	return m, nil
}

// GET id/promulgate
// See https://github.com/juju/charmstore/blob/v4/docs/API.md#put-idpromulgate
func (h *ReqHandler) serveAdminPromulgate(id *router.ResolvedURL, _ bool, w http.ResponseWriter, req *http.Request) error {
	if _, err := h.authorize(req, []string{promulgatorsGroup}, false, id); err != nil {
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
	return nil
}

type resolvedIdHandler func(id *router.ResolvedURL, fullySpecified bool, w http.ResponseWriter, req *http.Request) error

// authId returns a resolvedIdHandler that checks that the client
// is authorized to perform the HTTP request method before
// invoking f.
func (h *ReqHandler) authId(f resolvedIdHandler) resolvedIdHandler {
	return func(id *router.ResolvedURL, fullySpecified bool, w http.ResponseWriter, req *http.Request) error {
		if err := h.AuthorizeEntity(id, req); err != nil {
			return errgo.Mask(err, errgo.Any)
		}
		if err := f(id, fullySpecified, w, req); err != nil {
			return errgo.Mask(err, errgo.Any)
		}
		return nil
	}
}

func isFullySpecified(id *charm.Reference) bool {
	return id.Series != "" && id.Revision != -1
}

// resolveId returns an id handler that resolves any non-fully-specified
// entity ids using h.resolveURL before calling f with the resolved id.
func (h *ReqHandler) resolveId(f resolvedIdHandler) router.IdHandler {
	return func(id *charm.Reference, w http.ResponseWriter, req *http.Request) error {
		rid, err := h.resolveURL(id)
		if err != nil {
			return errgo.Mask(err, errgo.Is(params.ErrNotFound))
		}
		return f(rid, isFullySpecified(id), w, req)
	}
}
