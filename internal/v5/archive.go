// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v5 // import "gopkg.in/juju/charmstore.v5-unstable/internal/v5"

import (
	"archive/zip"
	"io"
	"io/ioutil"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/juju/httprequest"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"
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
func (h *ReqHandler) serveArchive(id *charm.URL, w http.ResponseWriter, req *http.Request) error {
	resolveId := h.ResolvedIdHandler
	authId := h.AuthIdHandler
	switch req.Method {
	case "DELETE":
		return resolveId(authId(h.serveDeleteArchive))(id, w, req)
	case "GET":
		return resolveId(h.serveGetArchive)(id, w, req)
	case "POST", "PUT":
		// Make sure we consume the full request body, before responding.
		//
		// It seems a shame to require the whole, possibly large, archive
		// is uploaded if we already know that the request is going to
		// fail, but it is necessary to prevent some failures.
		//
		// TODO: investigate using 100-Continue statuses to prevent
		// unnecessary uploads.
		defer io.Copy(ioutil.Discard, req.Body)
		if err := h.authorizeUpload(id, req); err != nil {
			return errgo.Mask(err, errgo.Any)
		}
		if req.Method == "POST" {
			return h.servePostArchive(id, w, req)
		}
		return h.servePutArchive(id, w, req)
	}
	return errgo.WithCausef(nil, params.ErrMethodNotAllowed, "%s not allowed", req.Method)
}

func (h *ReqHandler) authorizeUpload(id *charm.URL, req *http.Request) error {
	if id.User == "" {
		return badRequestf(nil, "user not specified in entity upload URL %q", id)
	}
	baseEntity, err := h.Store.FindBaseEntity(id, charmstore.FieldSelector("acls", "developmentacls"))
	// Note that we pass a nil entity URL to authorizeWithPerms, because
	// we haven't got a resolved URL at this point. At some
	// point in the future, we may want to be able to allow
	// is-entity first-party caveats to be allowed when uploading
	// at which point we will need to rethink this a little.
	if err == nil {
		if err := h.authorizeWithPerms(req, baseEntity.DevelopmentACLs.Read, baseEntity.DevelopmentACLs.Write, nil); err != nil {
			return errgo.Mask(err, errgo.Any)
		}
		// If uploading a published entity, also check that the user has
		// publishing permissions.
		if id.Channel == "" {
			if err := h.authorizeWithPerms(req, baseEntity.ACLs.Read, baseEntity.ACLs.Write, nil); err != nil {
				return errgo.Mask(err, errgo.Any)
			}
		}
		return nil
	}
	if errgo.Cause(err) != params.ErrNotFound {
		return errgo.Notef(err, "cannot retrieve entity %q for authorization", id)
	}
	// The base entity does not currently exist, so we default to
	// assuming write permissions for the entity user.
	if err := h.authorizeWithPerms(req, nil, []string{id.User}, nil); err != nil {
		return errgo.Mask(err, errgo.Any)
	}
	return nil
}

func (h *ReqHandler) serveGetArchive(id *router.ResolvedURL, w http.ResponseWriter, req *http.Request) error {
	_, err := h.authorizeEntityAndTerms(req, []*router.ResolvedURL{id})
	if err != nil {
		return errgo.Mask(err, errgo.Any)
	}
	r, size, hash, err := h.Store.OpenBlob(id)
	if err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}
	defer r.Close()
	header := w.Header()
	setArchiveCacheControl(w.Header(), h.isPublic(id.URL))
	header.Set(params.ContentHashHeader, hash)
	header.Set(params.EntityIdHeader, id.String())

	if StatsEnabled(req) {
		h.Store.IncrementDownloadCountsAsync(id)
	}
	// TODO(rog) should we set connection=close here?
	// See https://codereview.appspot.com/5958045
	serveContent(w, req, size, r)
	return nil
}

func (h *ReqHandler) serveDeleteArchive(id *router.ResolvedURL, w http.ResponseWriter, req *http.Request) error {
	// Retrieve the entity blob name from the database.
	blobName, _, err := h.Store.BlobNameAndHash(id)
	if err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}
	// Remove the entity.
	if err := h.Store.DB.Entities().RemoveId(&id.URL); err != nil {
		return errgo.Notef(err, "cannot remove %s", id)
	}
	// Remove the reference to the archive from the blob store.
	if err := h.Store.BlobStore.Remove(blobName); err != nil {
		return errgo.Notef(err, "cannot remove blob %s", blobName)
	}
	h.Store.IncCounterAsync(charmstore.EntityStatsKey(&id.URL, params.StatsArchiveDelete))
	return nil
}

func (h *ReqHandler) updateStatsArchiveUpload(id *charm.URL, err *error) {
	// Upload stats don't include revision: it is assumed that each
	// entity revision is only uploaded once.
	id.Revision = -1
	kind := params.StatsArchiveUpload
	if *err != nil {
		kind = params.StatsArchiveFailedUpload
	}
	h.Store.IncCounterAsync(charmstore.EntityStatsKey(id, kind))
}

func (h *ReqHandler) servePostArchive(id *charm.URL, w http.ResponseWriter, req *http.Request) (err error) {
	defer h.updateStatsArchiveUpload(id, &err)

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

	oldURL, oldHash, err := h.latestRevisionInfo(id)
	if err != nil && errgo.Cause(err) != params.ErrNotFound {
		return errgo.Notef(err, "cannot get hash of latest revision")
	}
	if oldHash == hash {
		// The hash matches the hash of the latest revision, so
		// no need to upload anything. When uploading a published URL and
		// the latest revision is a development entity, then we need to
		// actually publish the existing entity. Note that at this point the
		// user is already known to have the required permissions.
		underDevelopment := id.Channel == charm.DevelopmentChannel
		if oldURL.Development && !underDevelopment {
			if err := h.Store.SetDevelopment(oldURL, false); err != nil {
				return errgo.NoteMask(err, "cannot publish charm or bundle", errgo.Is(params.ErrNotFound))
			}
		}
		oldURL.Development = underDevelopment
		return httprequest.WriteJSON(w, http.StatusOK, &params.ArchiveUploadResponse{
			Id:            oldURL.UserOwnedURL(),
			PromulgatedId: oldURL.PromulgatedURL(),
		})
	}
	rid := &router.ResolvedURL{
		URL:         *id.WithChannel(""),
		Development: id.Channel == charm.DevelopmentChannel,
	}
	// Choose the next revision number for the upload.
	if oldURL == nil {
		rid.URL.Revision = 0
	} else {
		rid.URL.Revision = oldURL.URL.Revision + 1
	}
	rid.PromulgatedRevision, err = h.getNewPromulgatedRevision(id)
	if err != nil {
		return errgo.Mask(err)
	}
	if err := h.Store.UploadEntity(rid, req.Body, hash, req.ContentLength); err != nil {
		return errgo.Mask(err,
			errgo.Is(params.ErrDuplicateUpload),
			errgo.Is(params.ErrEntityIdNotAllowed),
			errgo.Is(params.ErrInvalidEntity),
		)
	}
	return httprequest.WriteJSON(w, http.StatusOK, &params.ArchiveUploadResponse{
		Id:            rid.UserOwnedURL(),
		PromulgatedId: rid.PromulgatedURL(),
	})
}

func (h *ReqHandler) servePutArchive(id *charm.URL, w http.ResponseWriter, req *http.Request) (err error) {
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
		URL:                 *id.WithChannel(""),
		PromulgatedRevision: -1,
		Development:         id.Channel == charm.DevelopmentChannel,
	}
	// Get the PromulgatedURL from the request parameters. When ingesting
	// entities might not be added in order and the promulgated revision might
	// not match the non-promulgated revision, so the full promulgated URL
	// needs to be specified.
	promulgatedURL := req.Form.Get("promulgated")
	var pid *charm.URL
	if promulgatedURL != "" {
		pid, err = charm.ParseURL(promulgatedURL)
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
	if err := h.Store.UploadEntity(rid, req.Body, hash, req.ContentLength); err != nil {
		return errgo.Mask(err,
			errgo.Is(params.ErrDuplicateUpload),
			errgo.Is(params.ErrEntityIdNotAllowed),
			errgo.Is(params.ErrInvalidEntity),
		)
	}
	return httprequest.WriteJSON(w, http.StatusOK, &params.ArchiveUploadResponse{
		Id:            rid.UserOwnedURL(),
		PromulgatedId: rid.PromulgatedURL(),
	})
}

func (h *ReqHandler) latestRevisionInfo(id *charm.URL) (*router.ResolvedURL, string, error) {
	entities, err := h.Store.FindEntities(id.WithChannel(charm.DevelopmentChannel), charmstore.FieldSelector("_id", "blobhash", "promulgated-url", "development"))
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
	return charmstore.EntityResolvedURL(latest), latest.BlobHash, nil
}

// GET id/archive/path
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idarchivepath
func (h *ReqHandler) serveArchiveFile(id *router.ResolvedURL, w http.ResponseWriter, req *http.Request) error {
	entity, err := h.Cache.Entity(id.UserOwnedURL(), charmstore.FieldSelector("blobname", "blobhash"))
	if err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}
	r, size, err := h.Store.BlobStore.Open(entity.BlobName)
	if err != nil {
		return errgo.Notef(err, "cannot open archive data for %v", id)
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
		setArchiveCacheControl(w.Header(), h.isPublic(id.URL))
		w.WriteHeader(http.StatusOK)
		io.Copy(w, content)
		return nil
	}
	return errgo.WithCausef(nil, params.ErrNotFound, "file %q not found in the archive", filePath)
}

func (h *ReqHandler) isPublic(id charm.URL) bool {
	baseEntity, err := h.Store.FindBaseEntity(&id, charmstore.FieldSelector("acls"))
	if err == nil {
		for _, p := range baseEntity.ACLs.Read {
			if p == params.Everyone {
				return true
			}
		}
	}
	return false
}

// ArchiveCachePublicMaxAge specifies the cache expiry duration for items
// returned from the archive where the id represents the id of a public entity.
const ArchiveCachePublicMaxAge = 1 * time.Hour

// setArchiveCacheControl sets cache control headers
// in a response to an archive-derived endpoint.
// The isPublic parameter specifies whether
// the entity id can or not be cached .
func setArchiveCacheControl(h http.Header, isPublic bool) {
	if isPublic {
		seconds := int(ArchiveCachePublicMaxAge / time.Second)
		h.Set("Cache-Control", "public, max-age="+strconv.Itoa(seconds))
	} else {
		h.Set("Cache-Control", "no-cache, must-revalidate")
	}
}

// getNewPromulgatedRevision returns the promulgated revision
// to give to a newly uploaded charm with the given id.
// It returns -1 if the charm is not promulgated.
func (h *ReqHandler) getNewPromulgatedRevision(id *charm.URL) (int, error) {
	baseEntity, err := h.Store.FindBaseEntity(id, charmstore.FieldSelector("promulgated"))
	if err != nil && errgo.Cause(err) != params.ErrNotFound {
		return 0, errgo.Mask(err)
	}
	if baseEntity == nil || !baseEntity.Promulgated {
		return -1, nil
	}
	query := h.Store.EntitiesQuery(&charm.URL{
		Series:   id.Series,
		Name:     id.Name,
		Channel:  id.Channel,
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
