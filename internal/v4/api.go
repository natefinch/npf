// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4

import (
	"archive/zip"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/juju/loggo"
	"github.com/juju/utils/jsonhttp"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v4"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/router"
	"github.com/juju/charmstore/params"
)

var logger = loggo.GetLogger("charmstore.internal.v4")

type Handler struct {
	*router.Router
	store  *charmstore.Store
	config charmstore.ServerParams
}

// New returns a new instance of the v4 API handler.
func New(store *charmstore.Store, config charmstore.ServerParams) *Handler {
	h := &Handler{
		store:  store,
		config: config,
	}
	h.Router = router.New(&router.Handlers{
		Global: map[string]http.Handler{
			"changes/published":  router.HandleJSON(h.serveChangesPublished),
			"debug":              http.HandlerFunc(h.serveDebug),
			"debug/pprof/":       newPprofHandler(h),
			"debug/status":       router.HandleJSON(h.serveDebugStatus),
			"debug/info":         router.HandleJSON(h.serveDebugInfo),
			"log":                router.HandleErrors(h.serveLog),
			"search":             router.HandleJSON(h.serveSearch),
			"search/interesting": http.HandlerFunc(h.serveSearchInteresting),
			"stats/":             router.NotFoundHandler(),
			"stats/counter/":     router.HandleJSON(h.serveStatsCounter),
		},
		Id: map[string]router.IdHandler{
			"archive":     h.serveArchive,
			"archive/":    h.serveArchiveFile,
			"diagram.svg": h.serveDiagram,
			"expand-id":   h.serveExpandId,
			"icon.svg":    h.serveIcon,
			"readme":      h.serveReadMe,
			"resources":   h.serveResources,
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
			"id":            h.entityHandler(h.metaId, "_id"),
			"id-name":       h.entityHandler(h.metaIdName, "_id"),
			"id-user":       h.entityHandler(h.metaIdUser, "_id"),
			"id-revision":   h.entityHandler(h.metaIdRevision, "_id"),
			"id-series":     h.entityHandler(h.metaIdSeries, "_id"),
			"manifest":      h.entityHandler(h.metaManifest, "blobname"),
			"revision-info": router.SingleIncludeHandler(h.metaRevisionInfo),
			"stats":         h.entityHandler(h.metaStats),
			"tags":          h.entityHandler(h.metaTags, "charmmeta", "bundledata"),

			// endpoints not yet implemented:
			// "color": router.SingleIncludeHandler(h.metaColor),
		},
	}, h.resolveURL, h.entityExists)
	return h
}

// NewAPIHandler returns a new Handler as an http Handler.
// It is defined for the convenience of callers that require a
// charmstore.NewAPIHandlerFunc.
func NewAPIHandler(store *charmstore.Store, config charmstore.ServerParams) http.Handler {
	return New(store, config)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// When requests in this handler use router.RelativeURL, we want
	// the "absolute path" there to be interpreted relative to the
	// root of this handler, not the absolute root of the web server,
	// which may be abitrarily many levels up.
	req.RequestURI = req.URL.Path
	h.Router.ServeHTTP(w, req)
}

// ResolveURL resolves the series and revision of the given URL
// if either is unspecified by filling them out with information retrieved
// from the store.
func ResolveURL(store *charmstore.Store, url *charm.Reference) error {
	if url.Series != "" && url.Revision != -1 {
		return nil
	}
	urls, err := store.ExpandURL(url)
	if err != nil {
		return errgo.Notef(err, "cannot expand URL")
	}
	if len(urls) == 0 {
		return noMatchingURLError(url)
	}
	*url = *selectPreferredURL(urls)
	return nil
}

func noMatchingURLError(url *charm.Reference) error {
	return errgo.WithCausef(nil, params.ErrNotFound, "no matching charm or bundle for %q", url)
}

func (h *Handler) resolveURL(url *charm.Reference) error {
	return ResolveURL(h.store, url)
}

type entityHandlerFunc func(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error)

// entityHandler returns a Handler that calls f with a *mongodoc.Entity that
// contains at least the given fields. It allows only GET requests.
func (h *Handler) entityHandler(f entityHandlerFunc, fields ...string) router.BulkIncludeHandler {
	return h.puttableEntityHandler(f, nil, fields...)
}

func (h *Handler) puttableEntityHandler(get entityHandlerFunc, handlePut router.FieldPutFunc, fields ...string) router.BulkIncludeHandler {
	handleGet := func(doc interface{}, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
		edoc := doc.(*mongodoc.Entity)
		val, err := get(edoc, id, path, flags)
		return val, errgo.Mask(err, errgo.Any)
	}
	type entityHandlerKey struct{}
	return router.FieldIncludeHandler(router.FieldIncludeHandlerParams{
		Key:       entityHandlerKey{},
		Query:     h.entityQuery,
		Fields:    fields,
		HandleGet: handleGet,
		HandlePut: handlePut,
		Update:    h.updateEntity,
	})
}

func (h *Handler) updateEntity(id *charm.Reference, fields map[string]interface{}) error {
	err := h.store.DB.Entities().UpdateId(id, bson.D{{"$set", fields}})
	if err != nil {
		return errgo.Notef(err, "cannot update %q", id)
	}
	return nil
}

func (h *Handler) entityExists(id *charm.Reference) (bool, error) {
	_, err := h.entityQuery(id, nil)
	if errgo.Cause(err) == params.ErrNotFound {
		return false, nil
	}
	if err != nil {
		return false, errgo.Mask(err)
	}
	return true, nil
}

func (h *Handler) entityQuery(id *charm.Reference, selector map[string]int) (interface{}, error) {
	var val mongodoc.Entity
	err := h.store.DB.Entities().
		Find(bson.D{{"_id", id}}).
		Select(selector).
		One(&val)
	if err == mgo.ErrNotFound {
		return nil, errgo.WithCausef(nil, params.ErrNotFound, "no matching charm or bundle for %s", id)
	}
	if err != nil {
		return nil, errgo.Mask(err)
	}
	return &val, nil
}

var ltsReleases = map[string]bool{
	"lucid":   true,
	"precise": true,
	"trusty":  true,
}

func selectPreferredURL(urls []*charm.Reference) *charm.Reference {
	best := urls[0]
	for _, url := range urls {
		if preferredURL(url, best) {
			best = url
		}
	}
	return best
}

// preferredURL reports whether url0 is preferred over url1.
func preferredURL(url0, url1 *charm.Reference) bool {
	if url0.Series == url1.Series {
		return url0.Revision > url1.Revision
	}
	if url0.Series == "bundle" || url1.Series == "bundle" {
		// One of the URLs refers to a bundle. Choose
		// a charm by preference.
		return url0.Series != "bundle"
	}
	if ltsReleases[url0.Series] == ltsReleases[url1.Series] {
		return url0.Series > url1.Series
	}
	return ltsReleases[url0.Series]
}

// parseBool returns the boolean value represented by the string.
// It accepts "1" or "0". Any other value returns an error.
func parseBool(value string) (bool, error) {
	switch value {
	case "0", "":
		return false, nil
	case "1":
		return true, nil
	}
	return false, errgo.Newf(`unexpected bool value %q (must be "0" or "1")`, value)
}

var errNotImplemented = errgo.Newf("method not implemented")

// GET /debug
// http://tinyurl.com/m63xhz8
func (h *Handler) serveDebug(w http.ResponseWriter, req *http.Request) {
	router.WriteError(w, errNotImplemented)
}

// POST id/resources/name.stream
// http://tinyurl.com/pnmwvy4
//
// GET  id/resources/name.stream[-revision]/arch/filename
// http://tinyurl.com/pydbn3u
//
// PUT id/resources/[~user/]series/name.stream-revision/arch?sha256=hash
// http://tinyurl.com/k8l8kdg
func (h *Handler) serveResources(charmId *charm.Reference, _ bool, w http.ResponseWriter, req *http.Request) error {
	return errNotImplemented
}

// GET id/expand-id
// https://docs.google.com/a/canonical.com/document/d/1TgRA7jW_mmXoKH3JiwBbtPvQu7WiM6XMrz1wSrhTMXw/edit#bookmark=id.4xdnvxphb2si
func (h *Handler) serveExpandId(id *charm.Reference, _ bool, w http.ResponseWriter, req *http.Request) error {
	// Mutate the given id so that it represents a base URL.
	id.Revision = -1
	id.Series = ""

	// Retrieve all the entities with the same base URL.
	var docs []mongodoc.Entity
	if err := h.store.DB.Entities().Find(bson.D{{"baseurl", id}}).Select(bson.D{{"_id", 1}}).All(&docs); err != nil {
		return errgo.Notef(err, "cannot get ids")
	}

	// A not found error should have been already returned by the router in the
	// case a partial id is provided. Here we do the same for the case when
	// a fully qualified URL is provided, but no matching entities are found.
	if len(docs) == 0 {
		return noMatchingURLError(id)
	}

	// Collect all the expanded identifiers for each entity.
	response := make([]params.ExpandedId, 0, len(docs))
	for _, doc := range docs {
		response = append(response, params.ExpandedId{Id: doc.URL.String()})
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
// http://tinyurl.com/poeoulw
func (h *Handler) metaCharmMetadata(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	return entity.CharmMeta, nil
}

// GET id/meta/bundle-metadata
// http://tinyurl.com/ozshbtb
func (h *Handler) metaBundleMetadata(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	return entity.BundleData, nil
}

// GET id/meta/bundle-unit-count
// http://tinyurl.com/mkvowub
func (h *Handler) metaBundleUnitCount(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	return bundleCount(entity.BundleUnitCount), nil
}

// GET id/meta/bundle-machine-count
// http://tinyurl.com/qfuubrv
func (h *Handler) metaBundleMachineCount(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
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
// http://tinyurl.com/p3xdcto
func (h *Handler) metaManifest(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	r, size, err := h.store.BlobStore.Open(entity.BlobName)
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
// http://tinyurl.com/kfd2h34
func (h *Handler) metaCharmActions(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	return entity.CharmActions, nil
}

// GET id/meta/charm-config
// http://tinyurl.com/oxxyujx
func (h *Handler) metaCharmConfig(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	return entity.CharmConfig, nil
}

// GET id/meta/color
// http://tinyurl.com/o2t3j4p
func (h *Handler) metaColor(id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/archive-size
// http://tinyurl.com/m8b9geq
func (h *Handler) metaArchiveSize(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	return &params.ArchiveSizeResponse{
		Size: entity.Size,
	}, nil
}

// GET id/meta/tags
// http://tinyurl.com/njyqwj2
func (h *Handler) metaTags(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	var tags []string
	switch {
	case id.Series == "bundle":
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
// http://tinyurl.com/lvyp2l5
func (h *Handler) metaStats(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	// Retrieve the aggregated downloads count for the specific revision.
	counts, countsAllRevisions, err := h.store.ArchiveDownloadCounts(id)
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
// http://tinyurl.com/q6xos7f
func (h *Handler) metaRevisionInfo(id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	baseURL := *id
	baseURL.Revision = -1
	baseURL.Series = ""

	var docs []mongodoc.Entity
	if err := h.store.DB.Entities().Find(
		bson.D{{"baseurl", &baseURL}, {"series", id.Series}}).Select(
		bson.D{{"_id", 1}}).Sort("-revision").All(&docs); err != nil {
		return "", errgo.Notef(err, "cannot get ids")
	}

	if len(docs) == 0 {
		return "", errgo.WithCausef(nil, params.ErrNotFound, "no matching charm or bundle for %s", id)
	}
	response := &params.RevisionInfoResponse{}
	for _, doc := range docs {
		response.Revisions = append(response.Revisions, doc.URL)
	}
	if len(response.Revisions) == 0 {
		return "", noMatchingURLError(&baseURL)
	}

	// Write the response in JSON format.
	return response, nil
}

// GET id/meta/id-user
// http://tinyurl.com/o7xmhz2
func (h *Handler) metaIdUser(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	return params.IdUserResponse{
		User: id.User,
	}, nil
}

// GET id/meta/id-series
// http://tinyurl.com/pnwmr6j
func (h *Handler) metaIdSeries(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	return params.IdSeriesResponse{
		Series: id.Series,
	}, nil
}

// GET id/meta/id-name
// http://tinyurl.com/lnqwbsp
func (h *Handler) metaId(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	return params.IdResponse{
		Id:       id,
		User:     id.User,
		Series:   id.Series,
		Name:     id.Name,
		Revision: id.Revision,
	}, nil
}

// GET id/meta/id-name
// http://tinyurl.com/m5q8gcy
func (h *Handler) metaIdName(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	return params.IdNameResponse{
		Name: id.Name,
	}, nil
}

// GET id/meta/id-revision
// http://tinyurl.com/ntd3coz
func (h *Handler) metaIdRevision(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	return params.IdRevisionResponse{
		Revision: id.Revision,
	}, nil
}

// GET id/meta/extra-info
// http://tinyurl.com/keos7wd
func (h *Handler) metaExtraInfo(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
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
// http://tinyurl.com/polrbn7
func (h *Handler) metaExtraInfoWithKey(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	path = strings.TrimPrefix(path, "/")
	var data json.RawMessage = entity.ExtraInfo[path]
	if len(data) == 0 {
		return nil, nil
	}
	return &data, nil
}

func (h *Handler) putMetaExtraInfo(id *charm.Reference, path string, val *json.RawMessage, updater *router.FieldUpdater) error {
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

func (h *Handler) putMetaExtraInfoWithKey(id *charm.Reference, path string, val *json.RawMessage, updater *router.FieldUpdater) error {
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

// GET id/meta/archive-upload-time
// http://tinyurl.com/nmujuqk
func (h *Handler) metaArchiveUploadTime(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	return &params.ArchiveUploadTimeResponse{
		UploadTime: entity.UploadTime.UTC(),
	}, nil
}

type PublishedResponse struct {
	Id        *charm.Reference
	Published time.Time
}

// GET changes/published[?limit=$count][&from=$fromdate][&to=$todate]
// http://tinyurl.com/qx5zdee
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
	query := h.store.DB.Entities().
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
