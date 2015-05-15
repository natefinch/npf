// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4

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

	"gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"
	"gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"
	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
)

var logger = loggo.GetLogger("charmstore.internal.v4")

type Handler struct {
	*router.Router
	pool    *charmstore.Pool
	config  charmstore.ServerParams
	locator *bakery.PublicKeyRing
}

const delegatableMacaroonExpiry = time.Minute

// New returns a new instance of the v4 API handler.
func New(pool *charmstore.Pool, config charmstore.ServerParams) *Handler {
	h := &Handler{
		pool:    pool,
		config:  config,
		locator: bakery.NewPublicKeyRing(),
	}

	h.Router = router.New(&router.Handlers{
		Global: map[string]http.Handler{
			"changes/published":    router.HandleJSON(h.serveChangesPublished),
			"debug":                http.HandlerFunc(h.serveDebug),
			"debug/pprof/":         newPprofHandler(h),
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
	return h
}

// NewAPIHandler returns a new Handler as an http Handler.
// It is defined for the convenience of callers that require a
// charmstore.NewAPIHandlerFunc.
func NewAPIHandler(pool *charmstore.Pool, config charmstore.ServerParams) http.Handler {
	return New(pool, config)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// When requests in this handler use router.RelativeURL, we want
	// the "absolute path" there to be interpreted relative to the
	// root of this handler, not the absolute root of the web server,
	// which may be abitrarily many levels up.
	req.RequestURI = req.URL.Path
	h.Router.ServeHTTP(w, req)
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

func (h *Handler) resolveURL(url *charm.Reference) (*router.ResolvedURL, error) {
	store := h.pool.Store()
	defer store.Close()
	return ResolveURL(store, url)
}

type entityHandlerFunc func(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error)

type baseEntityHandlerFunc func(entity *mongodoc.BaseEntity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error)

// entityHandler returns a Handler that calls f with a *mongodoc.Entity that
// contains at least the given fields. It allows only GET requests.
func (h *Handler) entityHandler(f entityHandlerFunc, fields ...string) router.BulkIncludeHandler {
	return h.puttableEntityHandler(f, nil, fields...)
}

func (h *Handler) puttableEntityHandler(get entityHandlerFunc, handlePut router.FieldPutFunc, fields ...string) router.BulkIncludeHandler {
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
func (h *Handler) baseEntityHandler(f baseEntityHandlerFunc, fields ...string) router.BulkIncludeHandler {
	return h.puttableBaseEntityHandler(f, nil, fields...)
}

func (h *Handler) puttableBaseEntityHandler(get baseEntityHandlerFunc, handlePut router.FieldPutFunc, fields ...string) router.BulkIncludeHandler {
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

func (h *Handler) updateBaseEntity(id *router.ResolvedURL, fields map[string]interface{}) error {
	store := h.pool.Store()
	defer store.Close()
	if err := store.UpdateBaseEntity(id, bson.D{{"$set", fields}}); err != nil {
		return errgo.Notef(err, "cannot update base entity %q", id)
	}
	return nil
}

func (h *Handler) updateEntity(id *router.ResolvedURL, fields map[string]interface{}) error {
	store := h.pool.Store()
	defer store.Close()
	err := store.UpdateEntity(id, bson.D{{"$set", fields}})
	if err != nil {
		return errgo.Notef(err, "cannot update %q", &id.URL)
	}
	err = store.UpdateSearchFields(id, fields)
	if err != nil {
		return errgo.Notef(err, "cannot update %q", &id.URL)
	}
	return nil
}

func (h *Handler) updateSearch(id *router.ResolvedURL, fields map[string]interface{}) error {
	store := h.pool.Store()
	defer store.Close()
	return store.UpdateSearch(id)
}

// updateSearchBase updates the search records for all entities with
// the same base URL as the given id.
func (h *Handler) updateSearchBase(id *router.ResolvedURL, fields map[string]interface{}) error {
	store := h.pool.Store()
	defer store.Close()
	baseURL := id.URL
	baseURL.Series = ""
	baseURL.Revision = -1
	if err := store.UpdateSearchBaseURL(&baseURL); err != nil {
		return errgo.Mask(err)
	}
	return nil
}

func (h *Handler) entityExists(id *router.ResolvedURL, req *http.Request) (bool, error) {
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

func (h *Handler) baseEntityQuery(id *router.ResolvedURL, selector map[string]int, req *http.Request) (interface{}, error) {
	fields := make([]string, 0, len(selector))
	for k, v := range selector {
		if v == 0 {
			continue
		}
		fields = append(fields, k)
	}
	store := h.pool.Store()
	defer store.Close()
	val, err := store.FindBaseEntity(&id.URL, fields...)
	if errgo.Cause(err) == params.ErrNotFound {
		return nil, errgo.WithCausef(nil, params.ErrNotFound, "no matching charm or bundle for %s", id)
	}
	if err != nil {
		return nil, errgo.Mask(err)
	}
	return val, nil
}

func (h *Handler) entityQuery(id *router.ResolvedURL, selector map[string]int, req *http.Request) (interface{}, error) {
	store := h.pool.Store()
	defer store.Close()
	val, err := store.FindEntity(id, fieldsFromSelector(selector)...)
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
func (h *Handler) serveDebug(w http.ResponseWriter, req *http.Request) {
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
func (h *Handler) serveResources(id *router.ResolvedURL, _ bool, w http.ResponseWriter, req *http.Request) error {
	return errNotImplemented
}

// GET id/expand-id
// https://docs.google.com/a/canonical.com/document/d/1TgRA7jW_mmXoKH3JiwBbtPvQu7WiM6XMrz1wSrhTMXw/edit#bookmark=id.4xdnvxphb2si
func (h *Handler) serveExpandId(id *router.ResolvedURL, _ bool, w http.ResponseWriter, req *http.Request) error {
	baseURL := id.PreferredURL()
	baseURL.Revision = -1
	baseURL.Series = ""
	store := h.pool.Store()
	defer store.Close()

	// baseURL now represents the base URL of the given id;
	// it will be a promulgated URL iff the original URL was
	// specified without a user, which will cause EntitiesQuery
	// to return entities that match appropriately.

	// Retrieve all the entities with the same base URL.
	q := store.EntitiesQuery(baseURL).Select(bson.D{{"_id", 1}, {"promulgated-url", 1}})
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
func (h *Handler) metaCharmMetadata(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return entity.CharmMeta, nil
}

// GET id/meta/bundle-metadata
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetabundle-metadata
func (h *Handler) metaBundleMetadata(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return entity.BundleData, nil
}

// GET id/meta/bundle-unit-count
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetabundle-unit-count
func (h *Handler) metaBundleUnitCount(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return bundleCount(entity.BundleUnitCount), nil
}

// GET id/meta/bundle-machine-count
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetabundle-machine-count
func (h *Handler) metaBundleMachineCount(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
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
func (h *Handler) metaManifest(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	store := h.pool.Store()
	defer store.Close()
	r, size, err := store.BlobStore.Open(entity.BlobName)
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
func (h *Handler) metaCharmActions(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return entity.CharmActions, nil
}

// GET id/meta/charm-config
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetacharm-config
func (h *Handler) metaCharmConfig(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return entity.CharmConfig, nil
}

// GET id/meta/color
func (h *Handler) metaColor(id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/archive-size
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetaarchive-size
func (h *Handler) metaArchiveSize(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return &params.ArchiveSizeResponse{
		Size: entity.Size,
	}, nil
}

// GET id/meta/hash
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetahash
func (h *Handler) metaHash(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return &params.HashResponse{
		Sum: entity.BlobHash,
	}, nil
}

// GET id/meta/hash256
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetahash256
func (h *Handler) metaHash256(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	// TODO frankban: remove this lazy calculation after the cshash256
	// command is run in the production db. At that point, entities
	// always have their blobhash256 field populated, and there is no
	// need for this lazy evaluation anymore.
	if entity.BlobHash256 == "" {
		store := h.pool.Store()
		defer store.Close()
		var err error
		if entity.BlobHash256, err = store.UpdateEntitySHA256(id); err != nil {
			return nil, errgo.Notef(err, "cannot retrieve the SHA256 hash for entity %s", entity.URL)
		}
	}
	return &params.HashResponse{
		Sum: entity.BlobHash256,
	}, nil
}

// GET id/meta/tags
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetatags
func (h *Handler) metaTags(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
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
func (h *Handler) metaStats(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	store := h.pool.Store()
	defer store.Close()
	// Retrieve the aggregated downloads count for the specific revision.
	counts, countsAllRevisions, err := store.ArchiveDownloadCounts(id.PreferredURL())
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
func (h *Handler) metaRevisionInfo(id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	searchURL := id.PreferredURL()
	searchURL.Revision = -1

	store := h.pool.Store()
	defer store.Close()
	q := store.EntitiesQuery(searchURL)
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
func (h *Handler) metaIdUser(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return params.IdUserResponse{
		User: id.PreferredURL().User,
	}, nil
}

// GET id/meta/id-series
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetaid-series
func (h *Handler) metaIdSeries(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return params.IdSeriesResponse{
		Series: id.PreferredURL().Series,
	}, nil
}

// GET id/meta/id
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetaid
func (h *Handler) metaId(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
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
func (h *Handler) metaIdName(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return params.IdNameResponse{
		Name: id.URL.Name,
	}, nil
}

// GET id/meta/id-revision
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetaid-revision
func (h *Handler) metaIdRevision(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return params.IdRevisionResponse{
		Revision: id.PreferredURL().Revision,
	}, nil
}

// GET id/meta/extra-info
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetaextra-info
func (h *Handler) metaExtraInfo(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
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
func (h *Handler) metaExtraInfoWithKey(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	path = strings.TrimPrefix(path, "/")
	var data json.RawMessage = entity.ExtraInfo[path]
	if len(data) == 0 {
		return nil, nil
	}
	return &data, nil
}

// PUT id/meta/extra-info
// https://github.com/juju/charmstore/blob/v4/docs/API.md#put-idmetaextra-info
func (h *Handler) putMetaExtraInfo(id *router.ResolvedURL, path string, val *json.RawMessage, updater *router.FieldUpdater, req *http.Request) error {
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
func (h *Handler) putMetaExtraInfoWithKey(id *router.ResolvedURL, path string, val *json.RawMessage, updater *router.FieldUpdater, req *http.Request) error {
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
func (h *Handler) metaPerm(entity *mongodoc.BaseEntity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return params.PermResponse{
		Read:  entity.ACLs.Read,
		Write: entity.ACLs.Write,
	}, nil
}

// PUT id/meta/perm
// https://github.com/juju/charmstore/blob/v4/docs/API.md#put-idmeta
func (h *Handler) putMetaPerm(id *router.ResolvedURL, path string, val *json.RawMessage, updater *router.FieldUpdater, req *http.Request) error {
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
	updater.UpdateSearch()
	return nil
}

// GET id/meta/promulgated
// See https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetapromulgated
func (h *Handler) metaPromulgated(entity *mongodoc.BaseEntity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
	return params.PromulgatedResponse{
		Promulgated: bool(entity.Promulgated),
	}, nil
}

// GET id/meta/perm/key
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetapermkey
func (h *Handler) metaPermWithKey(entity *mongodoc.BaseEntity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
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
func (h *Handler) putMetaPermWithKey(id *router.ResolvedURL, path string, val *json.RawMessage, updater *router.FieldUpdater, req *http.Request) error {
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
		updater.UpdateSearch()
		return nil
	case "/write":
		updater.UpdateField("acls.write", perms)
		return nil
	}
	return errgo.WithCausef(nil, params.ErrNotFound, "unknown permission")
}

// GET id/meta/archive-upload-time
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetaarchive-upload-time
func (h *Handler) metaArchiveUploadTime(entity *mongodoc.Entity, id *router.ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
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
func (h *Handler) serveChangesPublished(_ http.Header, r *http.Request) (interface{}, error) {
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
	store := h.pool.Store()
	defer store.Close()
	query := store.DB.Entities().
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
func (h *Handler) serveMacaroon(_ http.Header, _ *http.Request) (interface{}, error) {
	return h.newMacaroon()
}

// GET /delegatable-macaroon
// See https://github.com/juju/charmstore/blob/v4/docs/API.md#get-delegatable-macaroon
func (h *Handler) serveDelegatableMacaroon(_ http.Header, req *http.Request) (interface{}, error) {
	store := h.pool.Store()
	defer store.Close()
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
	m, err := store.Bakery.NewMacaroon("", nil, []checkers.Caveat{
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
func (h *Handler) serveAdminPromulgate(id *router.ResolvedURL, _ bool, w http.ResponseWriter, req *http.Request) error {
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
	store := h.pool.Store()
	defer store.Close()
	if err := store.SetPromulgated(id, promulgate.Promulgated); err != nil {
		return errgo.Mask(err, errgo.Any)
	}
	return nil
}

type resolvedIdHandler func(id *router.ResolvedURL, fullySpecified bool, w http.ResponseWriter, req *http.Request) error

// authId returns a resolvedIdHandler that checks that the client
// is authorized to perform the HTTP request method before
// invoking f.
func (h *Handler) authId(f resolvedIdHandler) resolvedIdHandler {
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
func (h *Handler) resolveId(f resolvedIdHandler) router.IdHandler {
	return func(id *charm.Reference, w http.ResponseWriter, req *http.Request) error {
		rid, err := h.resolveURL(id)
		if err != nil {
			return errgo.Mask(err, errgo.Is(params.ErrNotFound))
		}
		return f(rid, isFullySpecified(id), w, req)
	}
}
