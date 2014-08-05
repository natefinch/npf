// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// serveContent serves the given content as a single HTTP endpoint.
// We use http.FileServer under the covers because that
// provides us with all the http Content-Range goodness
// that we'd like.
func serveContent(w http.ResponseWriter, req *http.Request, length int64, content io.ReadSeeker) {
	fs := &archiveFS{
		length:     length,
		ReadSeeker: content,
	}
	// Copy the request and mutate the path to pretend
	// we're looking for the given file.
	nreq := *req
	nreq.URL.Path = "/archive"
	h := http.FileServer(fs)
	h.ServeHTTP(w, &nreq)
}

type archiveFS struct {
	length int64
	io.ReadSeeker
}

func (fs *archiveFS) Open(name string) (http.File, error) {
	if name != "/archive" {
		return nil, fmt.Errorf("unexpected name %q", name)
	}
	return fs, nil
}

func (fs *archiveFS) Close() error {
	return nil
}

func (fs *archiveFS) Stat() (os.FileInfo, error) {
	return fs, nil
}

func (fs *archiveFS) Readdir(count int) ([]os.FileInfo, error) {
	return nil, fmt.Errorf("not a directory")
}

// Name implements os.FileInfo.Name.
func (fs *archiveFS) Name() string {
	return "archive"
}

// Size implements os.FileInfo.Size.
func (fs *archiveFS) Size() int64 {
	return fs.length
}

// Mode implements os.FileInfo.Mode.
func (fs *archiveFS) Mode() os.FileMode {
	return 0444
}

// ModTime implements os.FileInfo.ModTime.
func (fs *archiveFS) ModTime() time.Time {
	return time.Time{}
}

// IsDir implements os.FileInfo.IsDir.
func (fs *archiveFS) IsDir() bool {
	return false
}

// Sys implements os.FileInfo.Sys.
func (fs *archiveFS) Sys() interface{} {
	return nil
}
