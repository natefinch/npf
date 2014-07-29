// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4

import (
	"fmt"
	"net/http"
	"net/url"

	"gopkg.in/juju/charm.v2"
	"labix.org/v2/mgo/bson"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/router"
	"github.com/juju/charmstore/params"
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
	h.Router = router.New(&router.Handlers{
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
		Meta: map[string]router.BulkIncludeHandler{
			"charm-metadata":  h.entityHandler(h.metaCharmMetadata, "charmmeta"),
			"bundle-metadata": h.entityHandler(h.metaBundleMetadata, "bundledata"),
			"charm-config":    h.entityHandler(h.metaCharmConfig, "charmconfig"),

			// endpoints not yet implemented - use SingleIncludeHandler for the time being.
			"manifest":            router.SingleIncludeHandler(h.metaManifest),
			"charm-actions":       router.SingleIncludeHandler(h.metaCharmActions),
			"color":               router.SingleIncludeHandler(h.metaColor),
			"archive-size":        router.SingleIncludeHandler(h.metaArchiveSize),
			"bundles-containing":  router.SingleIncludeHandler(h.metaBundlesContaining),
			"extra-info":          router.SingleIncludeHandler(h.metaExtraInfo),
			"extra-info/":         router.SingleIncludeHandler(h.metaExtraInfoWithKey),
			"charm-related":       router.SingleIncludeHandler(h.metaCharmRelated),
			"archive-upload-time": router.SingleIncludeHandler(h.metaArchiveUploadTime),
		},
	}, h.resolveURL)
	return h
}

// ResolveURL resolves the series and revision of the given URL
// if either is unspecified by filling them out with information retrieved
// from the store.
func ResolveURL(store *charmstore.Store, url *charm.URL) error {
	if url.Series != "" && url.Revision != -1 {
		return nil
	}
	urls, err := store.ExpandURL(url)
	if err != nil {
		return err
	}
	if len(urls) == 0 {
		return fmt.Errorf("no matching charm or bundle for %q", url)
	}
	*url = *selectPreferredURL(urls)
	return nil
}

func (h *handler) resolveURL(url *charm.URL) error {
	return ResolveURL(h.store, url)
}

type entityHandlerFunc func(entity *mongodoc.Entity, id *charm.URL, path string, flags url.Values) (interface{}, error)

// entityHandler returns a handler that calls f with a *mongodoc.Entity that
// contains at least the given fields.
func (h *handler) entityHandler(f entityHandlerFunc, fields ...string) router.BulkIncludeHandler {
	handle := func(doc interface{}, id *charm.URL, path string, flags url.Values) (interface{}, error) {
		edoc := doc.(*mongodoc.Entity)
		return f(edoc, id, path, flags)
	}
	type entityHandlerKey struct{}
	return router.FieldIncludeHandler(
		entityHandlerKey{},
		h.entityQuery,
		fields,
		handle,
	)
}

func (h *handler) entityQuery(id *charm.URL, selector map[string]int) (interface{}, error) {
	var val mongodoc.Entity
	err := h.store.DB.Entities().
		Find(bson.D{{"_id", id}}).
		Select(selector).
		One(&val)
	if err != nil {
		return nil, err
	}
	return &val, nil
}

var ltsReleases = map[string]bool{
	"lucid":   true,
	"precise": true,
	"trusty":  true,
}

func selectPreferredURL(urls []*charm.URL) *charm.URL {
	best := urls[0]
	for _, url := range urls {
		if preferredURL(url, best) {
			best = url
		}
	}
	return best
}

// preferredURL reports whether url0 is preferred over url1.
func preferredURL(url0, url1 *charm.URL) bool {
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

var ErrMetadataNotRelevant = fmt.Errorf("metadata not relevant for the given entity")

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
	return errNotImplemented
}

// GET id/archive/…
// http://tinyurl.com/lampm24
func (h *handler) serveArchiveFile(charmId *charm.URL, w http.ResponseWriter, req *http.Request) error {
	return errNotImplemented
}

// GET id/meta/charm-metadata
// http://tinyurl.com/poeoulw
func (h *handler) metaCharmMetadata(entity *mongodoc.Entity, id *charm.URL, path string, flags url.Values) (interface{}, error) {
	if params.IsBundle(id) {
		return nil, ErrMetadataNotRelevant
	}
	return entity.CharmMeta, nil
}

// GET id/meta/bundle-metadata
// http://tinyurl.com/ozshbtb
func (h *handler) metaBundleMetadata(entity *mongodoc.Entity, id *charm.URL, path string, flags url.Values) (interface{}, error) {
	if !params.IsBundle(id) {
		return nil, ErrMetadataNotRelevant
	}
	return entity.BundleData, nil
}

// GET id/meta/manifest
// http://tinyurl.com/p3xdcto
func (h *handler) metaManifest(id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/charm-actions
// http://tinyurl.com/kfd2h34
func (h *handler) metaCharmActions(id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/charm-config
// http://tinyurl.com/oxxyujx
func (h *handler) metaCharmConfig(entity *mongodoc.Entity, id *charm.URL, path string, flags url.Values) (interface{}, error) {
	if params.IsBundle(id) {
		return nil, ErrMetadataNotRelevant
	}
	return entity.CharmConfig, nil
}

// GET id/meta/color
// http://tinyurl.com/o2t3j4p
func (h *handler) metaColor(id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/archive-size
// http://tinyurl.com/m8b9geq
func (h *handler) metaArchiveSize(id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/stats/
// http://tinyurl.com/lvyp2l5
func (h *handler) metaStats(id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/bundles-containing[?include=meta[&include=meta…]]
// http://tinyurl.com/oqc386r
func (h *handler) metaBundlesContaining(id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/extra-info
// http://tinyurl.com/keos7wd
func (h *handler) metaExtraInfo(id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/extra-info/key
// http://tinyurl.com/polrbn7
func (h *handler) metaExtraInfoWithKey(id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/charm-related[?include=meta[&include=meta…]]
// http://tinyurl.com/q7vdmzl
func (h *handler) metaCharmRelated(id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}

// GET id/meta/archive-upload-time
// http://tinyurl.com/nmujuqk
func (h *handler) metaArchiveUploadTime(id *charm.URL, path string, flags url.Values) (interface{}, error) {
	return nil, errNotImplemented
}
