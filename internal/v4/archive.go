// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/juju/utils/jsonhttp"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v0/csclient/params"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"
	"gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"
	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
)

// GET id/archive
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idarchive
//
// POST id/archive?hash=sha384hash
// https://github.com/juju/charmstore/blob/v4/docs/API.md#post-idarchive
//
// DELETE id/archive
// https://github.com/juju/charmstore/blob/v4/docs/API.md#delete-idarchive
//
// PUT id/archive?hash=sha384hash
// This is like POST except that it puts the archive to a known revision
// rather than choosing a new one. As this feature is to support legacy
// ingestion methods, and will be removed in the future, it has no entry
// in the specification.
func (h *Handler) serveArchive(id *charm.Reference, w http.ResponseWriter, req *http.Request) error {
	switch req.Method {
	case "DELETE":
		return h.resolveId(h.authId(h.serveDeleteArchive))(id, w, req)
	case "GET":
		return h.resolveId(h.authId(h.serveGetArchive))(id, w, req)
	case "POST", "PUT":
		if err := h.authorizeUpload(id, req); err != nil {
			return errgo.Mask(err, errgo.Any)
		}
		if req.Method == "POST" {
			return h.servePostArchive(id, w, req)
		}
		return h.servePutArchive(id, w, req)
	}
	// TODO(rog) params.ErrMethodNotAllowed
	return errgo.Newf("method not allowed")
}

func (h *Handler) authorizeUpload(id *charm.Reference, req *http.Request) error {
	if id.User == "" {
		return badRequestf(nil, "user not specified in entity upload URL %q", id)
	}
	store := h.pool.Store()
	defer store.Close()
	// Note that we pass a nil entity URL to authorizeWithPerms, because
	// we haven't got a resolved URL at this point. At some
	// point in the future, we may want to be able to allow
	// is-entity first-party caveats to be allowed when uploading
	// at which point we will need to rethink this a little.
	baseURL := *id
	baseURL.Revision = -1
	baseURL.Series = ""
	baseEntity, err := store.FindBaseEntity(id, "acls")
	if err == nil {
		return h.authorizeWithPerms(req, baseEntity.ACLs.Read, baseEntity.ACLs.Write, nil)
	}
	if errgo.Cause(err) != params.ErrNotFound {
		return errgo.Notef(err, "cannot retrieve entity %q for authorization", id)
	}
	// The base entity does not currently exist, so we default to
	// assuming write permissions for the entity user.
	return h.authorizeWithPerms(req, nil, []string{id.User}, nil)
}

func (h *Handler) serveGetArchive(id *router.ResolvedURL, fullySpecified bool, w http.ResponseWriter, req *http.Request) error {
	store := h.pool.Store()
	defer store.Close()
	r, size, hash, err := store.OpenBlob(id)
	if err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}
	defer r.Close()
	header := w.Header()
	setArchiveCacheControl(w.Header(), fullySpecified)
	header.Set(params.ContentHashHeader, hash)
	header.Set(params.EntityIdHeader, id.String())

	if StatsEnabled(req) {
		store.IncrementDownloadCountsAsync(id)
	}
	// TODO(rog) should we set connection=close here?
	// See https://codereview.appspot.com/5958045
	serveContent(w, req, size, r)
	return nil
}

func (h *Handler) serveDeleteArchive(id *router.ResolvedURL, fullySpecified bool, w http.ResponseWriter, req *http.Request) error {
	store := h.pool.Store()
	defer store.Close()
	// Retrieve the entity blob name from the database.
	blobName, _, err := store.BlobNameAndHash(id)
	if err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}
	// Remove the entity.
	if err := store.DB.Entities().RemoveId(&id.URL); err != nil {
		return errgo.Notef(err, "cannot remove %s", id)
	}
	// Remove the reference to the archive from the blob store.
	if err := store.BlobStore.Remove(blobName); err != nil {
		return errgo.Notef(err, "cannot remove blob %s", blobName)
	}
	store.IncCounterAsync(charmstore.EntityStatsKey(&id.URL, params.StatsArchiveDelete))
	return nil
}

func (h *Handler) updateStatsArchiveUpload(id *charm.Reference, err *error) {
	store := h.pool.Store()
	defer store.Close()
	// Upload stats don't include revision: it is assumed that each
	// entity revision is only uploaded once.
	id.Revision = -1
	kind := params.StatsArchiveUpload
	if *err != nil {
		kind = params.StatsArchiveFailedUpload
	}
	store.IncCounterAsync(charmstore.EntityStatsKey(id, kind))
}

func (h *Handler) servePostArchive(id *charm.Reference, w http.ResponseWriter, req *http.Request) (err error) {
	defer h.updateStatsArchiveUpload(id, &err)

	if id.Series == "" {
		return badRequestf(nil, "series not specified")
	}
	if id.Revision != -1 {
		return badRequestf(nil, "revision specified, but should not be specified")
	}
	if id.User == "" {
		return badRequestf(nil, "user not specified")
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
	rid := &router.ResolvedURL{
		URL: *id,
	}
	// Choose the next revision number for the upload.
	if oldId == nil {
		rid.URL.Revision = 0
	} else {
		rid.URL.Revision = oldId.Revision + 1
	}
	rid.PromulgatedRevision, err = h.getNewPromulgatedRevision(id)
	if err != nil {
		return errgo.Mask(err)
	}

	if err := h.addBlobAndEntity(rid, req.Body, hash, req.ContentLength); err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrDuplicateUpload))
	}
	return jsonhttp.WriteJSON(w, http.StatusOK, &params.ArchiveUploadResponse{
		Id:            &rid.URL,
		PromulgatedId: rid.PromulgatedURL(),
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
	if id.User == "" {
		return badRequestf(nil, "user not specified")
	}
	hash := req.Form.Get("hash")
	if hash == "" {
		return badRequestf(nil, "hash parameter not specified")
	}
	if req.ContentLength == -1 {
		return badRequestf(nil, "Content-Length not specified")
	}
	rid := &router.ResolvedURL{
		URL:                 *id,
		PromulgatedRevision: -1,
	}
	// Get the PromulgatedURL from the request parameters. When ingesting
	// entities might not be added in order and the promulgated revision might
	// not match the non-promulgated revision, so the full promulgated URL
	// needs to be specified.
	promulgatedURL := req.Form.Get("promulgated")
	var pid *charm.Reference
	if promulgatedURL != "" {
		pid, err = charm.ParseReference(promulgatedURL)
		if err != nil {
			return badRequestf(err, "cannot parse promulgated url")
		}
		if pid.User != "" {
			return badRequestf(nil, "promulgated URL cannot have a user")
		}
		if pid.Name != id.Name {
			return badRequestf(nil, "promulgated URL has incorrect charm name")
		}
		if pid.Series != id.Series {
			return badRequestf(nil, "promulgated URL has incorrect series")
		}
		if pid.Revision == -1 {
			return badRequestf(nil, "promulgated URL has no revision")
		}
		rid.PromulgatedRevision = pid.Revision
	}
	if err := h.addBlobAndEntity(rid, req.Body, hash, req.ContentLength); err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrDuplicateUpload))
	}
	return jsonhttp.WriteJSON(w, http.StatusOK, &params.ArchiveUploadResponse{
		Id:            id,
		PromulgatedId: rid.PromulgatedURL(),
	})
	return nil
}

// addBlobAndEntity streams the contents of the given body
// to the blob store and adds an entity record for it.
// The hash and contentLength parameters hold
// the content hash and the content length respectively.
func (h *Handler) addBlobAndEntity(id *router.ResolvedURL, body io.Reader, hash string, contentLength int64) (err error) {
	name := bson.NewObjectId().Hex()

	// Calculate the SHA256 hash while uploading the blob in the blob store.
	hash256 := sha256.New()
	body = io.TeeReader(body, hash256)

	store := h.pool.Store()
	defer store.Close()
	// Upload the actual blob, and make sure that it is removed
	// if we fail later.
	err = store.BlobStore.PutUnchallenged(body, name, contentLength, hash)
	if err != nil {
		return errgo.Notef(err, "cannot put archive blob")
	}
	r, _, err := store.BlobStore.Open(name)
	if err != nil {
		return errgo.Notef(err, "cannot open newly created blob")
	}
	defer r.Close()
	defer func() {
		if err != nil {
			store.BlobStore.Remove(name)
			// TODO(rog) log if remove fails.
		}
	}()

	// Add the entity entry to the charm store.
	sum256 := fmt.Sprintf("%x", hash256.Sum(nil))
	if err := h.addEntity(id, r, name, hash, sum256, contentLength); err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrDuplicateUpload))
	}
	return nil
}

// addEntity adds the entity represented by the contents
// of the given reader, associating it with the given id.
func (h *Handler) addEntity(id *router.ResolvedURL, r io.ReadSeeker, blobName, hash, hash256 string, contentLength int64) error {
	store := h.pool.Store()
	defer store.Close()
	readerAt := charmstore.ReaderAtSeeker(r)
	p := charmstore.AddParams{
		URL:         id,
		BlobName:    blobName,
		BlobHash:    hash,
		BlobHash256: hash256,
		BlobSize:    contentLength,
	}
	if id.URL.Series == "bundle" {
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
		if err := store.AddBundle(b, p); err != nil {
			return errgo.Mask(err, errgo.Is(params.ErrDuplicateUpload))
		}
		return nil
	}
	ch, err := charm.ReadCharmArchiveFromReader(readerAt, contentLength)
	if err != nil {
		return errgo.Notef(err, "cannot read charm archive")
	}
	if err := checkCharmIsValid(ch); err != nil {
		return errgo.Mask(err)
	}
	if err := store.AddCharm(ch, p); err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrDuplicateUpload))
	}
	return nil
}

func checkCharmIsValid(ch charm.Charm) error {
	m := ch.Meta()
	for _, rels := range []map[string]charm.Relation{m.Provides, m.Requires, m.Peers} {
		if err := checkRelationsAreValid(rels); err != nil {
			return errgo.Mask(err)
		}
	}
	return nil
}

func checkRelationsAreValid(rels map[string]charm.Relation) error {
	for _, rel := range rels {
		if rel.Name == "relation-name" {
			return errgo.Newf("relation %s has almost certainly not been changed from the template", rel.Name)
		}
		if rel.Interface == "interface-name" {
			return errgo.Newf("interface %s in relation %s has almost certainly not been changed from the template", rel.Interface, rel.Name)
		}
	}
	return nil
}

func (h *Handler) latestRevisionInfo(id *charm.Reference) (*charm.Reference, string, error) {
	store := h.pool.Store()
	defer store.Close()
	entities, err := store.FindEntities(id, "_id", "blobhash")
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

// GET id/archive/path
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idarchivepath
func (h *Handler) serveArchiveFile(id *router.ResolvedURL, fullySpecified bool, w http.ResponseWriter, req *http.Request) error {
	store := h.pool.Store()
	defer store.Close()
	r, size, _, err := store.OpenBlob(id)
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
		setArchiveCacheControl(w.Header(), fullySpecified)
		w.WriteHeader(http.StatusOK)
		io.Copy(w, content)
		return nil
	}
	return errgo.WithCausef(nil, params.ErrNotFound, "file %q not found in the archive", filePath)
}

func (h *Handler) bundleCharms(ids []string) (map[string]charm.Charm, error) {
	store := h.pool.Store()
	defer store.Close()
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
		e, err := store.FindBestEntity(url)
		if err != nil {
			if errgo.Cause(err) == params.ErrNotFound {
				// Ignore this error too, for the same reasons
				// described above.
				continue
			}
			return nil, err
		}
		urls = append(urls, e.URL)
		idKeys = append(idKeys, id)
	}
	var entities []mongodoc.Entity
	if err := store.DB.Entities().
		Find(bson.D{{"_id", bson.D{{"$in", urls}}}}).
		All(&entities); err != nil {
		return nil, err
	}

	entityCharms := make(map[charm.Reference]charm.Charm, len(entities))
	for i, entity := range entities {
		entityCharms[*entity.URL] = &entityCharm{entities[i]}
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
type entityCharm struct {
	mongodoc.Entity
}

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

var (
	// archiveCacheVersionedMaxAge specifies the cache expiry duration for items
	// returned from the archive where the id is fully specified.
	archiveCacheVersionedMaxAge = 365 * 24 * time.Hour

	// archiveCacheNonVersionedMaxAge specifies the cache expiry duration for items
	// returned from the archive where the id is not fully specified.
	archiveCacheNonVersionedMaxAge = 5 * time.Minute
)

// setArchiveCacheControl sets any cache control headers
// in a response to an archive-derived endpoint.
// The idFullySpecified header specifies whether
// the entity id in the request was fully specified by the client.
func setArchiveCacheControl(h http.Header, idFullySpecified bool) {
	age := archiveCacheVersionedMaxAge
	if !idFullySpecified {
		age = archiveCacheNonVersionedMaxAge
	}
	seconds := int(age / time.Second)
	h.Set("Cache-Control", "public, max-age="+strconv.Itoa(seconds))
}

// getNewPromulgatedRevision returns the promulgated revision
// to give to a newly uploaded charm with the given id.
// It returns -1 if the charm is not promulgated.
func (h *Handler) getNewPromulgatedRevision(id *charm.Reference) (int, error) {
	store := h.pool.Store()
	defer store.Close()
	baseEntity, err := store.FindBaseEntity(id, "promulgated")
	if err != nil && errgo.Cause(err) != params.ErrNotFound {
		return 0, errgo.Mask(err)
	}
	if baseEntity == nil || !baseEntity.Promulgated {
		return -1, nil
	}
	query := store.EntitiesQuery(&charm.Reference{
		Series:   id.Series,
		Name:     id.Name,
		Revision: -1,
	})
	var entity mongodoc.Entity
	err = query.Sort("-promulgated-revision").Select(bson.D{{"promulgated-revision", 1}}).One(&entity)
	if err == mgo.ErrNotFound {
		return 0, nil
	}
	if err != nil {
		return 0, errgo.Mask(err)
	}
	return entity.PromulgatedRevision + 1, nil
}
