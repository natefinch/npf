// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4

import (
	"archive/zip"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/juju/jujusvg"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v4"

	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/router"
	"github.com/juju/charmstore/params"
)

// GET id/diagram.svg
// http://tinyurl.com/nqjvxov
func (h *Handler) serveDiagram(id *charm.Reference, w http.ResponseWriter, req *http.Request) error {
	if id.Series != "bundle" {
		return errgo.WithCausef(nil, params.ErrNotFound, "diagrams not supported for charms")
	}
	entity, err := h.store.FindEntity(id, "bundledata")
	if err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}

	var urlErr error
	// TODO consider what happens when a charm's SVG does not exist.
	canvas, err := jujusvg.NewFromBundle(entity.BundleData, func(id *charm.Reference) string {
		// TODO change jujusvg so that the iconURL function can
		// return an error.
		absPath := "/" + id.Path() + "/archive/icon.svg"
		p, err := router.RelativeURLPath(req.RequestURI, absPath)
		if err != nil {
			urlErr = errgo.Notef(err, "cannot make relative URL from %q and %q", req.RequestURI, absPath)
		}
		return p
	})
	if err != nil {
		return errgo.Notef(err, "cannot create canvas")
	}
	if urlErr != nil {
		return urlErr
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	canvas.Marshal(w)
	return nil
}

// These are all forms of README files
// actually observed in charms in the wild.
var allowedReadMe = map[string]bool{
	"readme":          true,
	"readme.md":       true,
	"readme.rst":      true,
	"readme.ex":       true,
	"readme.markdown": true,
	"readme.txt":      true,
}

func (h *Handler) serveReadMe(id *charm.Reference, w http.ResponseWriter, req *http.Request) error {
	entity, err := h.store.FindEntity(id, "_id", "contents", "blobname")
	if err != nil {
		return errgo.NoteMask(err, "cannot get README", errgo.Is(params.ErrNotFound))
	}
	isReadMeFile := func(f *zip.File) bool {
		name := strings.ToLower(path.Clean(f.Name))
		// This is the same condition currently used by the GUI.
		// TODO propagate likely content type from file extension.
		return allowedReadMe[name]
	}
	r, err := h.store.OpenCachedBlobFile(entity, mongodoc.FileReadMe, isReadMeFile)
	if err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}
	defer r.Close()
	io.Copy(w, r)
	return nil
}

func (h *Handler) serveIcon(id *charm.Reference, w http.ResponseWriter, req *http.Request) error {
	if id.Series == "bundle" {
		return errgo.WithCausef(nil, params.ErrNotFound, "icons not supported for bundles")
	}

	entity, err := h.store.FindEntity(id, "_id", "contents", "blobname")
	if err != nil {
		return errgo.NoteMask(err, "cannot get icon", errgo.Is(params.ErrNotFound))
	}
	isIconFile := func(f *zip.File) bool {
		return path.Clean(f.Name) == "icon.svg"
	}
	r, err := h.store.OpenCachedBlobFile(entity, mongodoc.FileIcon, isIconFile)
	if err != nil {
		if errgo.Cause(err) != params.ErrNotFound {
			return errgo.Mask(err)
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		io.Copy(w, strings.NewReader(defaultIcon))
		return nil
	}
	defer r.Close()
	w.Header().Set("Content-Type", "image/svg+xml")
	io.Copy(w, r)
	return nil
}
