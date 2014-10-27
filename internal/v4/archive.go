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

	"github.com/juju/utils/jsonhttp"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v4"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/params"
)

// GET id/archive
// http://tinyurl.com/qjrwq53
//
// POST id/archive?hash=sha384hash
// http://tinyurl.com/lzrzrgb
//
// DELETE id/archive
// http://tinyurl.com/ojmlwos
//
// PUT id/archive?hash=sha384hash
// This is like POST except that it puts the archive to a known revision
// rather than choosing a new one. As this feature is to support legacy
// ingestion methods, and will be removed in the future, it has no entry
// in the specification.
func (h *Handler) serveArchive(id *charm.Reference, w http.ResponseWriter, req *http.Request) error {
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
	case "PUT":
		if err := h.authenticate(w, req); err != nil {
			return err
		}
		return h.servePutArchive(id, w, req)
	case "GET":
	}
	r, size, hash, err := h.store.OpenBlob(id)
	if err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}
	defer r.Close()
	header := w.Header()
	header.Set(params.ContentHashHeader, hash)
	header.Set(params.EntityIdHeader, id.String())
	// Storefront is linking to icon.svg directly, so we need CORS support.
	// http://www.w3.org/TR/cors/
	header.Set("Access-Control-Allow-Origin", "*")
	header.Set("Access-Control-Allow-Headers", "X-Requested-With")
	h.store.IncCounterAsync(entityStatsKey(id, params.StatsArchiveDownload))
	// TODO(rog) should we set connection=close here?
	// See https://codereview.appspot.com/5958045
	serveContent(w, req, size, r)
	return nil
}

func (h *Handler) serveDeleteArchive(id *charm.Reference, w http.ResponseWriter, req *http.Request) error {
	// Retrieve the entity blob name from the database.
	blobName, _, err := h.store.BlobNameAndHash(id)
	if err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}
	// Remove the entity.
	if err := h.store.DB.Entities().RemoveId(id); err != nil {
		return errgo.Notef(err, "cannot remove %s", id)
	}
	// Remove the reference to the archive from the blob store.
	if err := h.store.BlobStore.Remove(blobName); err != nil {
		return errgo.Notef(err, "cannot remove blob %s", blobName)
	}
	h.store.IncCounterAsync(entityStatsKey(id, params.StatsArchiveDelete))
	return nil
}

func (h *Handler) updateStatsArchiveUpload(id *charm.Reference, err *error) {
	// Upload stats don't include revision: it is assumed that each
	// entity revision is only uploaded once.
	id.Revision = -1
	kind := params.StatsArchiveUpload
	if *err != nil {
		kind = params.StatsArchiveFailedUpload
	}
	h.store.IncCounterAsync(entityStatsKey(id, kind))
}

func (h *Handler) servePostArchive(id *charm.Reference, w http.ResponseWriter, req *http.Request) (err error) {
	defer h.updateStatsArchiveUpload(id, &err)

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

	oldId, oldHash, err := h.latestRevisionInfo(id)
	if err != nil && errgo.Cause(err) != params.ErrNotFound {
		return errgo.Notef(err, "cannot get hash of latest revision")
	}
	if oldHash == hash {
		// The hash matches the hash of the latest revision, so
		// no need to upload anything.
		return jsonhttp.WriteJSON(w, http.StatusOK, &params.ArchiveUploadResponse{
			Id: oldId,
		})
	}

	// Choose the next revision number for the upload.
	if oldId != nil {
		id.Revision = oldId.Revision + 1
	} else {
		id.Revision = 0
	}
	if err := h.addBlobAndEntity(id, req.Body, hash, req.ContentLength); err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrDuplicateUpload))
	}
	return jsonhttp.WriteJSON(w, http.StatusOK, &params.ArchiveUploadResponse{
		Id: id,
	})
}

func (h *Handler) servePutArchive(id *charm.Reference, w http.ResponseWriter, req *http.Request) (err error) {
	defer h.updateStatsArchiveUpload(id, &err)
	if id.Series == "" {
		return badRequestf(nil, "series not specified")
	}
	if id.Revision == -1 {
		return badRequestf(nil, "revision not specified")
	}
	hash := req.Form.Get("hash")
	if hash == "" {
		return badRequestf(nil, "hash parameter not specified")
	}
	if req.ContentLength == -1 {
		return badRequestf(nil, "Content-Length not specified")
	}
	if err := h.addBlobAndEntity(id, req.Body, hash, req.ContentLength); err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrDuplicateUpload))
	}
	return jsonhttp.WriteJSON(w, http.StatusOK, &params.ArchiveUploadResponse{
		Id: id,
	})
	return nil
}

// addBlobAndEntity streams the contents of the given body
// to the blob store and adds an entity record for it.
// The hash and contentLength parameters hold
// the content hash and the content length respectively.
func (h *Handler) addBlobAndEntity(id *charm.Reference, body io.Reader, hash string, contentLength int64) (err error) {
	// Upload the actual blob, and make sure that it is removed
	// if we fail later.
	name := bson.NewObjectId().Hex()
	err = h.store.BlobStore.PutUnchallenged(body, name, contentLength, hash)
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

	// Add the entity entry to the charm store.
	if err := h.addEntity(id, r, name, hash, contentLength); err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrDuplicateUpload))
	}
	return nil
}

// addEntity adds the entity represented by the contents
// of the given reader, associating it with the given id.
func (h *Handler) addEntity(id *charm.Reference, r io.ReadSeeker, blobName string, hash string, contentLength int64) error {
	readerAt := charmstore.ReaderAtSeeker(r)
	if id.Series == "bundle" {
		b, err := charm.ReadBundleArchiveFromReader(readerAt, contentLength)
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
		if err := h.store.AddBundle(b, charmstore.AddParams{
			URL:      id,
			BlobName: blobName,
			BlobHash: hash,
			BlobSize: contentLength,
		}); err != nil {
			return errgo.Mask(err, errgo.Is(params.ErrDuplicateUpload))
		}
		return nil
	}
	ch, err := charm.ReadCharmArchiveFromReader(readerAt, contentLength)
	if err != nil {
		return errgo.Notef(err, "cannot read charm archive")
	}
	if err := h.store.AddCharm(ch, charmstore.AddParams{
		URL:      id,
		BlobName: blobName,
		BlobHash: hash,
		BlobSize: contentLength,
	}); err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrDuplicateUpload))
	}
	return nil
}

func (h *Handler) latestRevisionInfo(id *charm.Reference) (*charm.Reference, string, error) {
	entities, err := h.store.FindEntities(id, "_id", "blobhash")
	if err != nil {
		return nil, "", errgo.Mask(err)
	}
	if len(entities) == 0 {
		return nil, "", params.ErrNotFound
	}
	latest := entities[0]
	for _, entity := range entities {
		if entity.URL.Revision > latest.URL.Revision {
			latest = entity
		}
	}
	return latest.URL, latest.BlobHash, nil
}

func verifyConstraints(s string) error {
	// TODO(rog) provide some actual constraints checking here.
	return nil
}

// GET id/archive/…
// http://tinyurl.com/lampm24
func (h *Handler) serveArchiveFile(id *charm.Reference, w http.ResponseWriter, req *http.Request) error {
	r, size, _, err := h.store.OpenBlob(id)
	if err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}
	defer r.Close()
	zipReader, err := zip.NewReader(charmstore.ReaderAtSeeker(r), size)
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
		// Storefront is linking to icon.svg directly, so we need CORS support.
		// http://www.w3.org/TR/cors/
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "X-Requested-With")
		w.WriteHeader(http.StatusOK)
		io.Copy(w, content)
		return nil
	}
	return errgo.WithCausef(nil, params.ErrNotFound, "file %q not found in the archive", filePath)
}

func (h *Handler) bundleCharms(ids []string) (map[string]charm.Charm, error) {
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

func (e *entityCharm) Metrics() *charm.Metrics {
	return nil
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
