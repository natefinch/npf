// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4

import (
	"archive/zip"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/juju/errgo"
	"gopkg.in/juju/charm.v3"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/internal/blobstore"
	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/router"
	"github.com/juju/charmstore/params"
)

// GET id/archive
// http://tinyurl.com/qjrwq53
//
// POST id/archive?sha256=hash
// http://tinyurl.com/lzrzrgb

// DELETE id/archive
// http://tinyurl.com/ojmlwos
func (h *handler) serveArchive(id *charm.Reference, w http.ResponseWriter, req *http.Request) error {
	switch req.Method {
	default:
		// TODO(rog) params.ErrMethodNotAllowed
		return errgo.Newf("method not allowed")
	case "DELETE":
		if err := h.authenticate(w, req); err != nil {
			return err
		}
		return h.serveDeleteArchive(id, w, req)
	case "POST":
		if err := h.authenticate(w, req); err != nil {
			return err
		}
		return h.servePostArchive(id, w, req)
	case "GET":
	}
	r, size, err := h.openBlob(id)
	if err != nil {
		return errgo.Mask(err)
	}
	defer r.Close()
	// TODO frankban 2014-08-22: log possible IncCounter errors.
	go h.store.IncCounter(entityStatsKey(id, params.StatsArchiveDownload))
	serveContent(w, req, size, r)
	return nil
}

func (h *handler) serveDeleteArchive(id *charm.Reference, w http.ResponseWriter, req *http.Request) error {
	// Retrieve the entity blob name from the database.
	blobName, err := h.findBlobName(id)
	if err != nil {
		return err
	}
	// Remove the entity.
	if err := h.store.DB.Entities().RemoveId(id); err != nil {
		return errgo.Notef(err, "cannot remove %s", id)
	}
	// Remove the reference to the archive from the blob store.
	if err := h.store.BlobStore.Remove(blobName); err != nil {
		return errgo.Notef(err, "cannot remove blob %s", blobName)
	}
	// TODO frankban 2014-08-25: log possible IncCounter errors.
	go h.store.IncCounter(entityStatsKey(id, params.StatsArchiveDelete))
	return nil
}

func (h *handler) servePostArchive(id *charm.Reference, w http.ResponseWriter, req *http.Request) error {
	// Validate the request parameters.
	if id.Series == "" {
		return badRequestf(nil, "series not specified")
	}
	if id.Revision != -1 {
		return badRequestf(nil, "revision specified, but should not be specified")
	}
	hash := req.Form.Get("hash")
	if hash == "" {
		return badRequestf(nil, "hash parameter not specified")
	}
	if req.ContentLength == -1 {
		return badRequestf(nil, "Content-Length not specified")
	}

	// Upload the actual blob, and make sure that it is removed
	// if we fail later.
	name := bson.NewObjectId().Hex()
	err := h.store.BlobStore.PutUnchallenged(req.Body, name, req.ContentLength, hash)
	if err != nil {
		return errgo.Notef(err, "cannot put archive blob")
	}
	r, _, err := h.store.BlobStore.Open(name)
	if err != nil {
		return errgo.Notef(err, "cannot open newly created blob")
	}
	defer r.Close()
	defer func() {
		if err != nil {
			h.store.BlobStore.Remove(name)
			// TODO(rog) log if remove fails.
		}
	}()

	// Create the entry for the entity in charm store.
	rev, err := h.nextRevisionForId(id)
	if err != nil {
		return errgo.Notef(err, "cannot get next revision for id")
	}
	id.Revision = rev
	readerAt := &readerAtSeeker{r}
	if id.Series == "bundle" {
		b, err := charm.ReadBundleArchiveFromReader(readerAt, req.ContentLength)
		if err != nil {
			return errgo.Notef(err, "cannot read bundle archive")
		}
		bundleData := b.Data()
		charms, err := h.bundleCharms(bundleData.RequiredCharms())
		if err != nil {
			return errgo.Notef(err, "cannot retrieve bundle charms")
		}
		if err := bundleData.VerifyWithCharms(verifyConstraints, charms); err != nil {
			// TODO frankban: use multiError (defined in internal/router).
			return errgo.Notef(verificationError(err), "bundle verification failed")
		}
		if err := h.store.AddBundle(id, b, name, hash, req.ContentLength); err != nil {
			return errgo.Mask(err, errgo.Is(params.ErrDuplicateUpload))
		}
	} else {
		ch, err := charm.ReadCharmArchiveFromReader(readerAt, req.ContentLength)
		if err != nil {
			return errgo.Notef(err, "cannot read charm archive")
		}
		if err := h.store.AddCharm(id, ch, name, hash, req.ContentLength); err != nil {
			return errgo.Mask(err, errgo.Is(params.ErrDuplicateUpload))
		}
	}
	return router.WriteJSON(w, http.StatusOK, &params.ArchivePostResponse{
		Id: id,
	})
}

func verifyConstraints(s string) error {
	// TODO(rog) provide some actual constraints checking here.
	return nil
}

// GET id/archive/…
// http://tinyurl.com/lampm24
func (h *handler) serveArchiveFile(id *charm.Reference, w http.ResponseWriter, req *http.Request) error {
	r, size, err := h.openBlob(id)
	if err != nil {
		return err
	}
	defer r.Close()
	zipReader, err := zip.NewReader(&readerAtSeeker{r}, size)
	if err != nil {
		return errgo.Notef(err, "cannot read archive data for %s", id)
	}

	// Retrieve the requested file from the zip archive.
	filePath := strings.TrimPrefix(path.Clean(req.URL.Path), "/")
	for _, file := range zipReader.File {
		if path.Clean(file.Name) != filePath {
			continue
		}
		// The file is found.
		fileInfo := file.FileInfo()
		if fileInfo.IsDir() {
			return errgo.WithCausef(nil, params.ErrForbidden, "directory listing not allowed")
		}
		content, err := file.Open()
		if err != nil {
			return errgo.Notef(err, "unable to read file %q", filePath)
		}
		defer content.Close()
		// Send the response to the client.
		ctype := mime.TypeByExtension(filepath.Ext(filePath))
		if ctype != "" {
			w.Header().Set("Content-Type", ctype)
		}
		w.Header().Set("Content-Length", strconv.FormatInt(fileInfo.Size(), 10))
		w.WriteHeader(http.StatusOK)
		io.Copy(w, content)
		return nil
	}
	return errgo.WithCausef(nil, params.ErrNotFound, "file %q not found in the archive", filePath)
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

func (h *handler) findBlobName(id *charm.Reference) (string, error) {
	var entity mongodoc.Entity
	if err := h.store.DB.Entities().
		FindId(id).
		Select(bson.D{{"blobname", 1}}).
		One(&entity); err != nil {
		if err == mgo.ErrNotFound {
			return "", errgo.WithCausef(nil, params.ErrNotFound, "")
		}
		return "", errgo.Notef(err, "cannot get %s", id)
	}
	return entity.BlobName, nil
}

func (h *handler) openBlob(id *charm.Reference) (blobstore.ReadSeekCloser, int64, error) {
	blobName, err := h.findBlobName(id)
	if err != nil {
		return nil, 0, err
	}
	r, size, err := h.store.BlobStore.Open(blobName)
	if err != nil {
		return nil, 0, errgo.Notef(err, "cannot open archive data for %s", id)
	}
	return r, size, nil
}

func (h *handler) bundleCharms(ids []string) (map[string]charm.Charm, error) {
	numIds := len(ids)
	urls := make([]*charm.Reference, 0, numIds)
	idKeys := make([]string, 0, numIds)
	// TODO resolve ids concurrently.
	for _, id := range ids {
		url, err := charm.ParseReference(id)
		if err != nil {
			// Ignore this error. This will be caught in the bundle
			// verification process (see bundleData.VerifyWithCharms) and will
			// be returned to the user along with other bundle errors.
			continue
		}
		if err = h.resolveURL(url); err != nil {
			if errgo.Cause(err) == params.ErrNotFound {
				// Ignore this error too, for the same reasons
				// described above.
				continue
			}
			return nil, err
		}
		urls = append(urls, url)
		idKeys = append(idKeys, id)
	}
	var entities []mongodoc.Entity
	if err := h.store.DB.Entities().
		Find(bson.D{{"_id", bson.D{{"$in", urls}}}}).
		All(&entities); err != nil {
		return nil, err
	}

	entityCharms := make(map[charm.Reference]charm.Charm, len(entities))
	for i, entity := range entities {
		entityCharms[*entity.URL] = (*entityCharm)(&entities[i])
	}
	charms := make(map[string]charm.Charm, len(urls))
	for i, url := range urls {
		if ch, ok := entityCharms[*url]; ok {
			charms[idKeys[i]] = ch
		}
	}
	return charms, nil
}

// entityCharm implements charm.Charm.
type entityCharm mongodoc.Entity

func (e *entityCharm) Meta() *charm.Meta {
	return e.CharmMeta
}

func (e *entityCharm) Config() *charm.Config {
	return e.CharmConfig
}

func (e *entityCharm) Actions() *charm.Actions {
	return e.CharmActions
}

func (e *entityCharm) Revision() int {
	return e.URL.Revision
}

// verificationError returns an error whose string representation is a list of
// all the verification error messages stored in err, in JSON format.
// Note that err must be a *charm.VerificationError.
func verificationError(err error) error {
	verr, ok := err.(*charm.VerificationError)
	if !ok {
		return err
	}
	messages := make([]string, len(verr.Errors))
	for i, err := range verr.Errors {
		messages[i] = err.Error()
	}
	sort.Strings(messages)
	encodedMessages, err := json.Marshal(messages)
	if err != nil {
		// This should never happen.
		return err
	}
	return errgo.New(string(encodedMessages))
}
