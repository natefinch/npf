// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4

import (
	"io"
	"net/http"

	"github.com/juju/errgo"
	"gopkg.in/juju/charm.v3"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/router"
	"github.com/juju/charmstore/params"
)

// GET id/archive
// http://tinyurl.com/qjrwq53
//
// POST id/archive?sha256=hash
// http://tinyurl.com/lzrzrgb
func (h *handler) serveArchive(charmId *charm.Reference, w http.ResponseWriter, req *http.Request) error {
	switch req.Method {
	default:
		// TODO(rog) params.ErrMethodNotAllowed
		return errgo.Newf("method not allowed")
	case "POST":
		resp, err := h.servePostArchive(charmId, w, req)
		if err != nil {
			return err
		}
		return router.WriteJSON(w, http.StatusOK, resp)
	case "GET":
	}
	var entity mongodoc.Entity
	if err := h.store.DB.Entities().
		FindId(charmId).
		Select(bson.D{{"blobhash", 1}}).
		One(&entity); err != nil {
		if err == mgo.ErrNotFound {
			return params.ErrNotFound
		}
		return errgo.Notef(err, "cannot get %s", charmId)
	}
	r, size, err := h.store.BlobStore.Open(entity.BlobHash)
	if err != nil {
		return errgo.Notef(err, "cannot open archive data for %s", charmId)
	}
	defer r.Close()
	serveContent(w, req, size, r)
	return nil
}

func (h *handler) servePostArchive(id *charm.Reference, w http.ResponseWriter, req *http.Request) (resp *params.ArchivePostResponse, err error) {
	// Validate the request parameters.

	if id.Series == "" {
		return nil, badRequestf(nil, "series not specified")
	}
	if id.Revision != -1 {
		return nil, badRequestf(nil, "revision specified, but should not be specified")
	}
	hash := req.Form.Get("hash")
	if hash == "" {
		return nil, badRequestf(nil, "hash parameter not specified")
	}
	if req.ContentLength == -1 {
		return nil, badRequestf(nil, "Content-Length not specified")
	}

	// Upload the actual blob, and make sure that it is removed
	// if we fail later.

	err = h.store.BlobStore.PutUnchallenged(req.Body, req.ContentLength, hash)
	if err != nil {
		return nil, errgo.Notef(err, "cannot put archive blob")
	}
	r, _, err := h.store.BlobStore.Open(hash)
	if err != nil {
		return nil, errgo.Notef(err, "cannot open newly created blob")
	}
	defer r.Close()
	defer func() {
		if err != nil {
			h.store.BlobStore.Remove(hash)
			// TODO(rog) log if remove fails.
		}
	}()

	// Create the entry for the entity in charm store.

	rev, err := h.nextRevisionForId(id)
	if err != nil {
		return nil, errgo.Notef(err, "cannot get next revision for id")
	}
	id.Revision = rev
	readerAt := &readerAtSeeker{r}
	if id.Series == "bundle" {
		b, err := charm.ReadBundleArchiveFromReader(readerAt, req.ContentLength)
		if err != nil {
			return nil, errgo.Notef(err, "cannot read bundle archive")
		}
		if err := b.Data().Verify(verifyConstraints); err != nil {
			return nil, errgo.Notef(err, "bundle verification failed")
		}
		if err := h.store.AddBundle(id, b, hash, req.ContentLength); err != nil {
			return nil, errgo.Mask(err, errgo.Is(params.ErrDuplicateUpload))
		}
	} else {
		ch, err := charm.ReadCharmArchiveFromReader(readerAt, req.ContentLength)
		if err != nil {
			return nil, errgo.Notef(err, "cannot read charm archive")
		}
		if err := h.store.AddCharm(id, ch, hash, req.ContentLength); err != nil {
			return nil, errgo.Mask(err, errgo.Is(params.ErrDuplicateUpload))
		}
	}
	return &params.ArchivePostResponse{
		Id: id,
	}, nil
}

func verifyConstraints(s string) error {
	// TODO(rog) provide some actual constraints checking here.
	return nil
}

type readerAtSeeker struct {
	r io.ReadSeeker
}

func (r *readerAtSeeker) ReadAt(buf []byte, p int64) (int, error) {
	if _, err := r.r.Seek(p, 0); err != nil {
		return 0, errgo.Notef(err, "cannot seek")
	}
	return r.r.Read(buf)
}

func (h *handler) nextRevisionForId(id *charm.Reference) (int, error) {
	id1 := *id
	id1.Revision = -1
	err := ResolveURL(h.store, &id1)
	if err == nil {
		return id1.Revision + 1, nil
	}
	if errgo.Cause(err) != params.ErrNotFound {
		return 0, errgo.Notef(err, "cannot resolve id")
	}
	return 0, nil
}

// GET id/archive/…
// http://tinyurl.com/lampm24
func (h *handler) serveArchiveFile(charmId *charm.Reference, w http.ResponseWriter, req *http.Request) error {
	return errNotImplemented
}
