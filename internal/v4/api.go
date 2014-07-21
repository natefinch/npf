// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4

import (
	"fmt"
	"log"
	"net/http"
	"net/url"

	"gopkg.in/juju/charm.v2"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/router"
)

type handler struct {
	*router.Router
	store *charmstore.Store
}

// New returns a new instance of the v4 API handler.
func New(store *charmstore.Store) http.Handler {
	h := &handler{
		store: store,
	}
	h.Router = router.New(store.DB.Database, &router.Handlers{
		Global: map[string]http.Handler{
			"stats/counter":      http.HandlerFunc(h.serveStatsCounter),
			"search":             http.HandlerFunc(h.serveSearch),
			"search/interesting": http.HandlerFunc(h.serveSearchInteresting),
			"debug":              http.HandlerFunc(h.serveDebug),
		},
		Id: map[string]router.IdHandler{
			"resources": h.serveResources,
			"archive":   h.serveArchive,
			"archive/":  h.serveArchiveFile,
			"expand-id": h.serveExpandId,
		},
		Meta: map[string]router.MetaHandler{
			"charm-metadata":      h.metaCharmMetadata,
			"bundle-metadata":     h.metaBundleMetadata,
			"manifest":            h.metaManifest,
			"charm-actions":       h.metaCharmActions,
			"charm-config":        h.metaCharmConfig,
			"color":               h.metaColor,
			"archive-size":        h.metaArchiveSize,
			"bundles-containing":  h.metaBundlesContaining,
			"extra-info":          h.metaExtraInfo,
			"extra-info/":         h.metaExtraInfoWithKey,
			"charm-related":       h.metaCharmRelated,
			"archive-upload-time": h.metaArchiveUploadTime,
		},
	})
	return h
}

var errNotImplemented = fmt.Errorf("method not implemented")

// GET stats/counter/key[:key]...?[by=unit]&start=date][&stop=date][&list=1]
// http://tinyurl.com/nkdovcf
func (h *handler) serveStatsCounter(w http.ResponseWriter, req *http.Request) {
	router.WriteError(w, errNotImplemented)
}

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
func (h *handler) serveResources(charmId *charm.URL, w http.ResponseWriter, req *http.Request) error {
	return errNotImplemented
}

// GET id/expand-id
// https://docs.google.com/a/canonical.com/document/d/1TgRA7jW_mmXoKH3JiwBbtPvQu7WiM6XMrz1wSrhTMXw/edit#bookmark=id.4xdnvxphb2si
func (h *handler) serveExpandId(charmId *charm.URL, w http.ResponseWriter, req *http.Request) error {
	return errNotImplemented
}

// GET id/archive
// http://tinyurl.com/qjrwq53
//
// POST id/archive?sha256=hash
// http://tinyurl.com/lzrzrgb
func (h *handler) serveArchive(charmId *charm.URL, w http.ResponseWriter, req *http.Request) error {
	log.Printf("in serveArchive")
	return errNotImplemented
}

// GET id/archive/…
// http://tinyurl.com/lampm24
func (h *handler) serveArchiveFile(charmId *charm.URL, w http.ResponseWriter, req *http.Request) error {
	return errNotImplemented
}

// GET id/meta/charm-metadata
// http://tinyurl.com/poeoulw
func (h *handler) metaCharmMetadata(getter router.ItemGetter, id *charm.URL, path string, flags url.Values) (interface{}, error) {
	var doc *mongodoc.Entity
	err := getter.GetItem(id, &doc, "charmmeta")
	if err != nil {
		return nil, err
	}
	// TODO(rog) When we have bundles, check whether the id is a bundle
	// and return ErrMetadataNotRelevant if so.
	return doc.CharmMeta, nil
}

// GET id/meta/bundle-metadata
// http://tinyurl.com/ozshbtb
func (h *handler) metaBundleMetadata(getter router.ItemGetter, id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/manifest
// http://tinyurl.com/p3xdcto
func (h *handler) metaManifest(getter router.ItemGetter, id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/charm-actions
// http://tinyurl.com/kfd2h34
func (h *handler) metaCharmActions(getter router.ItemGetter, id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/charm-config
// http://tinyurl.com/oxxyujx
func (h *handler) metaCharmConfig(getter router.ItemGetter, id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/color
// http://tinyurl.com/o2t3j4p
func (h *handler) metaColor(getter router.ItemGetter, id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/revision-info
// http://tinyurl.com/q6xos7f
func (h *handler) revisionInfo(getter router.ItemGetter, id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/archive-size
// http://tinyurl.com/m8b9geq
func (h *handler) metaArchiveSize(getter router.ItemGetter, id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/stats/
// http://tinyurl.com/lvyp2l5
func (h *handler) metaStats(getter router.ItemGetter, id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/bundles-containing[?include=meta[&include=meta…]]
// http://tinyurl.com/oqc386r
func (h *handler) metaBundlesContaining(getter router.ItemGetter, id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/extra-info
// http://tinyurl.com/keos7wd
func (h *handler) metaExtraInfo(getter router.ItemGetter, id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/extra-info/key
// http://tinyurl.com/polrbn7
func (h *handler) metaExtraInfoWithKey(getter router.ItemGetter, id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/charm-related[?include=meta[&include=meta…]]
// http://tinyurl.com/q7vdmzl
func (h *handler) metaCharmRelated(getter router.ItemGetter, id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/archive-upload-time
// http://tinyurl.com/nmujuqk
func (h *handler) metaArchiveUploadTime(getter router.ItemGetter, id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}
