// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package legacy

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/juju/errgo"
	"gopkg.in/juju/charm.v4"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/router"
	"github.com/juju/charmstore/internal/v4"
	"github.com/juju/charmstore/lppublish"
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
	url, err := h.resolveURLStr(strings.TrimPrefix(req.URL.Path, "/"))
	if err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}
	return h.v4.Id["archive"](url, w, req)
}

func (h *Handler) resolveURLStr(urlStr string) (*charm.Reference, error) {
	curl, err := charm.ParseReference(urlStr)
	if err != nil {
		return nil, errgo.WithCausef(err, params.ErrNotFound, "")
	}
	if err := v4.ResolveURL(h.store, curl); err != nil {
		// Note: preserve error cause from resolveURL.
		return nil, errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}
	return curl, nil
}

// charmStatsKey returns a stats key for the given charm reference and kind.
func charmStatsKey(url *charm.Reference, kind string) []string {
	if url.User == "" {
		return []string{kind, url.Series, url.Name}
	}
	return []string{kind, url.Series, url.Name, url.User}
}

var errNotFound = fmt.Errorf("entry not found")

func (h *Handler) serveCharmInfo(w http.ResponseWriter, req *http.Request) (interface{}, error) {
	response := make(map[string]*charm.InfoResponse)
	for _, url := range req.Form["charms"] {
		c := &charm.InfoResponse{}
		response[url] = c
		var entity mongodoc.Entity
		curl, err := h.resolveURLStr(url)
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
			if digest, found := entity.ExtraInfo[lppublish.BzrDigestKey]; found {
				if err := json.Unmarshal(digest, &c.Digest); err != nil {
					c.Errors = append(c.Errors, "cannot unmarshal digest: "+err.Error())
				}
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
	r, _, err := h.store.OpenBlob(curl)
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
