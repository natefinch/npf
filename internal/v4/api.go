// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4

import (
	"archive/zip"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/juju/errgo"
	"gopkg.in/juju/charm.v3"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/router"
	"github.com/juju/charmstore/params"
)

type handler struct {
	*router.Router
	store  *charmstore.Store
	config charmstore.ServerParams
}

// New returns a new instance of the v4 API handler.
func New(store *charmstore.Store, config charmstore.ServerParams) http.Handler {
	h := &handler{
		store:  store,
		config: config,
	}
	h.Router = router.New(&router.Handlers{
		Global: map[string]http.Handler{
			"debug":              http.HandlerFunc(h.serveDebug),
			"search":             http.HandlerFunc(h.serveSearch),
			"search/interesting": http.HandlerFunc(h.serveSearchInteresting),
			"stats/":             router.NotFoundHandler(),
			"stats/counter/":     router.HandleJSON(h.serveStatsCounter),
		},
		Id: map[string]router.IdHandler{
			"archive":   h.serveArchive,
			"archive/":  h.serveArchiveFile,
			"expand-id": h.serveExpandId,
			"resources": h.serveResources,
		},
		Meta: map[string]router.BulkIncludeHandler{
			"archive-size":        h.entityHandler(h.metaArchiveSize, "size"),
			"archive-upload-time": h.entityHandler(h.metaArchiveUploadTime, "uploadtime"),
			"bundle-metadata":     h.entityHandler(h.metaBundleMetadata, "bundledata"),
			"bundles-containing":  h.entityHandler(h.metaBundlesContaining),
			"charm-actions":       h.entityHandler(h.metaCharmActions, "charmactions"),
			"charm-config":        h.entityHandler(h.metaCharmConfig, "charmconfig"),
			"charm-metadata":      h.entityHandler(h.metaCharmMetadata, "charmmeta"),
			"charm-related":       h.entityHandler(h.metaCharmRelated, "charmprovidedinterfaces", "charmrequiredinterfaces"),
			"manifest":            h.entityHandler(h.metaManifest, "blobname"),
			"revision-info":       router.SingleIncludeHandler(h.metaRevisionInfo),
			"stats":               h.entityHandler(h.metaStats),
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

			// endpoints not yet implemented - use SingleIncludeHandler for the time being.
			"color": router.SingleIncludeHandler(h.metaColor),
		},
	}, h.resolveURL)
	return h
}

func (h *handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
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

func (h *handler) resolveURL(url *charm.Reference) error {
	return ResolveURL(h.store, url)
}

type entityHandlerFunc func(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error)

// entityHandler returns a handler that calls f with a *mongodoc.Entity that
// contains at least the given fields. It allows only GET requests.
func (h *handler) entityHandler(f entityHandlerFunc, fields ...string) router.BulkIncludeHandler {
	return h.puttableEntityHandler(f, nil, fields...)
}

func (h *handler) puttableEntityHandler(get entityHandlerFunc, put router.FieldPutFunc, fields ...string) router.BulkIncludeHandler {
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
		HandlePut: put,
		Update:    h.updateEntity,
	})
}

func (h *handler) updateEntity(id *charm.Reference, fields map[string]interface{}) error {
	err := h.store.DB.Entities().UpdateId(id, bson.D{{"$set", fields}})
	if err != nil {
		return errgo.Notef(err, "cannot update %q", id)
	}
	return nil
}

func (h *handler) entityQuery(id *charm.Reference, selector map[string]int) (interface{}, error) {
	var val mongodoc.Entity
	err := h.store.DB.Entities().
		Find(bson.D{{"_id", id}}).
		Select(selector).
		One(&val)
	if err == mgo.ErrNotFound {
		return nil, errgo.WithCausef(nil, params.ErrNotFound, "")
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

// GET search[?text=text][&autocomplete=1][&filter=value…][&limit=limit][&include=meta]
// http://tinyurl.com/qzobc69
func (h *handler) serveSearch(w http.ResponseWriter, req *http.Request) {
	router.WriteError(w, errNotImplemented)
}

// GET search/interesting[?limit=limit][&include=meta]
// http://tinyurl.com/ntmdrg8
func (h *handler) serveSearchInteresting(w http.ResponseWriter, req *http.Request) {
	router.WriteError(w, errNotImplemented)
}

// GET /debug
// http://tinyurl.com/m63xhz8
func (h *handler) serveDebug(w http.ResponseWriter, req *http.Request) {
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
func (h *handler) serveResources(charmId *charm.Reference, w http.ResponseWriter, req *http.Request) error {
	return errNotImplemented
}

// GET id/expand-id
// https://docs.google.com/a/canonical.com/document/d/1TgRA7jW_mmXoKH3JiwBbtPvQu7WiM6XMrz1wSrhTMXw/edit#bookmark=id.4xdnvxphb2si
func (h *handler) serveExpandId(id *charm.Reference, w http.ResponseWriter, req *http.Request) error {
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
	return router.WriteJSON(w, http.StatusOK, response)
}

func badRequestf(underlying error, f string, a ...interface{}) error {
	err := errgo.WithCausef(underlying, params.ErrBadRequest, f, a...)
	err.(*errgo.Err).SetLocation(1)
	return err
}

// GET id/meta/charm-metadata
// http://tinyurl.com/poeoulw
func (h *handler) metaCharmMetadata(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	return entity.CharmMeta, nil
}

// GET id/meta/bundle-metadata
// http://tinyurl.com/ozshbtb
func (h *handler) metaBundleMetadata(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	return entity.BundleData, nil
}

// GET id/meta/manifest
// http://tinyurl.com/p3xdcto
func (h *handler) metaManifest(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	r, size, err := h.store.BlobStore.Open(entity.BlobName)
	if err != nil {
		return nil, errgo.Notef(err, "cannot open archive data for %s", id)
	}
	defer r.Close()
	zipReader, err := zip.NewReader(&readerAtSeeker{r}, size)
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
func (h *handler) metaCharmActions(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	return entity.CharmActions, nil
}

// GET id/meta/charm-config
// http://tinyurl.com/oxxyujx
func (h *handler) metaCharmConfig(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	return entity.CharmConfig, nil
}

// GET id/meta/color
// http://tinyurl.com/o2t3j4p
func (h *handler) metaColor(id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/archive-size
// http://tinyurl.com/m8b9geq
func (h *handler) metaArchiveSize(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	return &params.ArchiveSizeResponse{
		Size: entity.Size,
	}, nil
}

// GET id/meta/stats/
// http://tinyurl.com/lvyp2l5
func (h *handler) metaStats(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	req := charmstore.CounterRequest{
		Key: entityStatsKey(id, params.StatsArchiveDownload),
	}
	results, err := h.store.Counters(&req)
	if err != nil {
		return nil, errgo.Notef(err, "cannot retrieve stats")
	}
	return &params.StatsResponse{
		// If a list is not requested as part of the charmstore.CounterRequest,
		// one result is always returned: if the key is not found the count is
		// set to 0.
		ArchiveDownloadCount: results[0].Count,
	}, nil
}

// GET id/meta/revision-info
// http://tinyurl.com/q6xos7f
func (h *handler) metaRevisionInfo(id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	baseURL := *id
	baseURL.Revision = -1
	baseURL.Series = ""

	var docs []mongodoc.Entity
	if err := h.store.DB.Entities().Find(
		bson.D{{"baseurl", &baseURL}}).Select(
		bson.D{{"_id", 1}}).All(&docs); err != nil {
		return "", errgo.Notef(err, "cannot get ids")
	}

	if len(docs) == 0 {
		return "", errgo.WithCausef(nil, params.ErrNotFound, "")
	}

	// Sort in descending order by revision.
	sort.Sort(entitiesByRevision(docs))
	response := &params.RevisionInfoResponse{}
	for _, doc := range docs {
		if doc.URL.Series == id.Series {
			response.Revisions = append(response.Revisions, doc.URL)
		}
	}

	if len(response.Revisions) == 0 {
		return "", noMatchingURLError(&baseURL)
	}

	// Write the response in JSON format.
	return response, nil
}

type entitiesByRevision []mongodoc.Entity

func (s entitiesByRevision) Len() int      { return len(s) }
func (s entitiesByRevision) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

// Implement the Less method of the sort interface backward, with > so that
// the sort order is descending.
func (s entitiesByRevision) Less(i, j int) bool { return s[i].URL.Revision > s[j].URL.Revision }

// GET id/meta/extra-info
// http://tinyurl.com/keos7wd
func (h *handler) metaExtraInfo(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
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
func (h *handler) metaExtraInfoWithKey(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	path = strings.TrimPrefix(path, "/")
	var data json.RawMessage = entity.ExtraInfo[path]
	if len(data) == 0 {
		return nil, nil
	}
	return &data, nil
}

func (h *handler) putMetaExtraInfo(id *charm.Reference, path string, val *json.RawMessage, updater *router.FieldUpdater) error {
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

func (h *handler) putMetaExtraInfoWithKey(id *charm.Reference, path string, val *json.RawMessage, updater *router.FieldUpdater) error {
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
func (h *handler) metaArchiveUploadTime(entity *mongodoc.Entity, id *charm.Reference, path string, flags url.Values) (interface{}, error) {
	return &params.ArchiveUploadTimeResponse{
		UploadTime: entity.UploadTime.UTC(),
	}, nil
}
