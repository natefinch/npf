// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// The legacy package implements the legacy API, as follows:
//
// /charm-info
//
// A GET call to `/charm-info` returns info about one or more charms, including
// its canonical URL, revision, SHA256 checksum and VCS revision digest.
// The returned info is in JSON format.
// For instance a request to `/charm-info?charms=cs:trusty/juju-gui` returns the
// following response:
//
//     {"cs:trusty/juju-gui": {
//         "canonical-url": "cs:trusty/juju-gui",
//         "revision": 3,
//         "sha256": "a15c77f3f92a0fb7b61e9...",
//         "digest": jeff.pihach@canonical.com-20140612210347-6cc9su1jqjkhbi84"
//     }}
//
// /charm-event:
//
// A GET call to `/charm-event` returns info about an event occurred in the life
// of the specified charm(s). Currently two types of events are logged:
// "published" (a charm has been published and it's available in the store) and
// "publish-error" (an error occurred while importing the charm).
// E.g. a call to `/charm-event?charms=cs:trusty/juju-gui` generates the following
// JSON response:
//
//     {"cs:trusty/juju-gui": {
//         "kind": "published",
//         "revision": 3,
//         "digest": "jeff.pihach@canonicalcom-20140612210347-6cc9su1jqjkhbi84",
//         "time": "2014-06-16T14:41:19Z"
//     }}
//
// /charm/
//
// The `charm` API provides the ability to download a charm as a Zip archive,
// given the charm identifier. For instance, it is possible to download the Juju
// GUI charm by performing a GET call to `/charm/trusty/juju-gui-42`. Both the
// revision and OS series can be omitted, e.g. `/charm/juju-gui` will download the
// last revision of the Juju GUI charm with support to the more recent Ubuntu LTS
// series.
//
// /stats/counter/
//
// Stats can be retrieved by calling `/stats/counter/{key}` where key is a query
// that specifies the counter stats to calculate and return.
//
// For instance, a call to `/stats/counter/charm-bundle:*` returns the number of
// times a charm has been downloaded from the store. To get the same value for
// a specific charm, it is possible to filter the results by passing the charm
// series and name, e.g. `/stats/counter/charm-bundle:trusty:juju-gui`.
//
// The results can be grouped by specifying the `by` query (possible values are
// `day` and `week`), and time delimited using the `start` and `stop` queries.
//
// It is also possible to list the results by passing `list=1`. For example, a GET
// call to `/stats/counter/charm-bundle:trusty:*?by=day&list=1` returns an
// aggregated count of trusty charms downloads, grouped by charm and day, similar
// to the following:
//
//     charm-bundle:trusty:juju-gui  2014-06-17  5
//     charm-bundle:trusty:mysql     2014-06-17  1
package legacy

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v4"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/router"
	"github.com/juju/charmstore/internal/v4"
	"github.com/juju/charmstore/params"
)

type Handler struct {
	v4    *router.Handlers
	store *charmstore.Store
	mux   *http.ServeMux
}

func NewAPIHandler(store *charmstore.Store, config charmstore.ServerParams) http.Handler {
	h := &Handler{
		v4:    v4.New(store, config).Handlers(),
		store: store,
		mux:   http.NewServeMux(),
	}
	h.handle("/charm-info", router.HandleJSON(h.serveCharmInfo))
	h.handle("/charm/", router.HandleErrors(h.serveCharm))
	h.handle("/charm-event", router.HandleJSON(h.serveCharmEvent))
	return h
}

func (h *Handler) handle(path string, handler http.Handler) {
	prefix := strings.TrimSuffix(path, "/")
	h.mux.Handle(path, http.StripPrefix(prefix, handler))
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	req.ParseForm()
	h.mux.ServeHTTP(w, req)
}

func (h *Handler) serveCharm(w http.ResponseWriter, req *http.Request) error {
	if req.Method != "GET" && req.Method != "HEAD" {
		return params.ErrMethodNotAllowed
	}
	url, fullySpecified, err := h.resolveURLStr(strings.TrimPrefix(req.URL.Path, "/"))
	if err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}
	return h.v4.Id["archive"](url, fullySpecified, w, req)
}

func (h *Handler) resolveURLStr(urlStr string) (*charm.Reference, bool, error) {
	curl, err := charm.ParseReference(urlStr)
	if err != nil {
		return nil, false, errgo.WithCausef(err, params.ErrNotFound, "")
	}
	fullySpecified := curl.Series != "" && curl.Revision != -1
	if err := v4.ResolveURL(h.store, curl); err != nil {
		// Note: preserve error cause from resolveURL.
		return nil, false, errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}
	return curl, fullySpecified, nil
}

// charmStatsKey returns a stats key for the given charm reference and kind.
func charmStatsKey(url *charm.Reference, kind string) []string {
	if url.User == "" {
		return []string{kind, url.Series, url.Name}
	}
	return []string{kind, url.Series, url.Name, url.User}
}

var errNotFound = fmt.Errorf("entry not found")

func (h *Handler) serveCharmInfo(_ http.Header, req *http.Request) (interface{}, error) {
	response := make(map[string]*charm.InfoResponse)
	for _, url := range req.Form["charms"] {
		c := &charm.InfoResponse{}
		response[url] = c
		var entity mongodoc.Entity
		curl, _, err := h.resolveURLStr(url)
		if err != nil {
			if errgo.Cause(err) == params.ErrNotFound {
				err = errNotFound
			}
		} else {
			err = h.store.DB.Entities().FindId(curl).One(&entity)
			if err == mgo.ErrNotFound {
				// The old API actually returned "entry not found"
				// on *any* error, but it seems reasonable to be
				// a little more descriptive for other errors.
				err = errNotFound
			}
		}
		if err == nil && entity.BlobHash256 == "" {
			// Lazily calculate SHA256 so that we don't burden
			// non-legacy code with that task.
			entity.BlobHash256, err = h.updateEntitySHA256(curl)
		}

		// Prepare the response part for this charm.
		if err == nil {
			c.CanonicalURL = curl.String()
			c.Sha256 = entity.BlobHash256
			c.Revision = curl.Revision
			c.Digest, err = entityBzrDigest(&entity)
			if err != nil {
				c.Errors = append(c.Errors, err.Error())
			}
			h.store.IncCounterAsync(charmStatsKey(curl, params.StatsCharmInfo))
		} else {
			c.Errors = append(c.Errors, err.Error())
			if curl != nil {
				h.store.IncCounterAsync(charmStatsKey(curl, params.StatsCharmMissing))
			}
		}
	}
	return response, nil
}

// updateEntitySHA256 updates the BlobHash256 entry for the entity.
// It is defined as a variable so that it can be mocked in tests.
var updateEntitySHA256 = func(store *charmstore.Store, url *charm.Reference, sum256 string) {
	err := store.DB.Entities().UpdateId(url, bson.D{{"$set", bson.D{{"blobhash256", sum256}}}})
	if err != nil && err != mgo.ErrNotFound {
		log.Printf("cannot update sha256 of archive: %v", err)
	}
}

func (h *Handler) updateEntitySHA256(curl *charm.Reference) (string, error) {
	r, _, _, err := h.store.OpenBlob(curl)
	defer r.Close()
	hash := sha256.New()
	_, err = io.Copy(hash, r)
	if err != nil {
		return "", errgo.Notef(err, "cannot calculate sha256 of archive")
	}
	sum256 := fmt.Sprintf("%x", hash.Sum(nil))

	// Update the entry asynchronously because it doesn't matter
	// if it succeeds or fails, or if several instances of the
	// charm store do it concurrently, and it doesn't
	// need to be on the critical path for charm-info.
	go updateEntitySHA256(h.store, curl, sum256)

	return sum256, nil
}

// serveCharmEvent returns events related to the charms specified in the
// "charms" query. In this implementation, the only supported event is
// "published", required by the "juju publish" command.
func (h *Handler) serveCharmEvent(_ http.Header, req *http.Request) (interface{}, error) {
	response := make(map[string]*charm.EventResponse)
	for _, url := range req.Form["charms"] {
		c := &charm.EventResponse{}

		// Ignore the digest part of the request.
		if i := strings.Index(url, "@"); i != -1 {
			url = url[:i]
		}
		// We intentionally do not implement the long_keys query parameter that
		// the legacy charm store supported, as "juju publish" does not use it.
		response[url] = c

		// Validate the charm URL.
		id, err := charm.ParseReference(url)
		if err != nil {
			c.Errors = []string{"invalid charm URL: " + err.Error()}
			continue
		}
		if id.Revision != -1 {
			c.Errors = []string{"got charm URL with revision: " + id.String()}
			continue
		}
		if err := v4.ResolveURL(h.store, id); err != nil {
			if errgo.Cause(err) == params.ErrNotFound {
				err = errNotFound
			}
			c.Errors = []string{err.Error()}
			continue
		}

		// Retrieve the charm.
		entity, err := h.store.FindEntity(id, "_id", "uploadtime", "extrainfo")
		if err != nil {
			if errgo.Cause(err) == params.ErrNotFound {
				// The old API actually returned "entry not found"
				// on *any* error, but it seems reasonable to be
				// a little more descriptive for other errors.
				err = errNotFound
			}
			c.Errors = []string{err.Error()}
			continue
		}

		// Prepare the response part for this charm.
		c.Kind = "published"
		c.Revision = id.Revision
		c.Time = entity.UploadTime.UTC().Format(time.RFC3339)
		c.Digest, err = entityBzrDigest(entity)
		if err != nil {
			c.Errors = []string{err.Error()}
		}
		h.store.IncCounterAsync(charmStatsKey(id, params.StatsCharmEvent))
	}
	return response, nil
}

func entityBzrDigest(entity *mongodoc.Entity) (string, error) {
	value, found := entity.ExtraInfo[params.BzrDigestKey]
	if !found {
		return "", nil
	}
	var digest string
	if err := json.Unmarshal(value, &digest); err != nil {
		return "", errgo.Notef(err, "cannot unmarshal digest")
	}
	return digest, nil
}
