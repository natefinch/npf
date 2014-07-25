// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"gopkg.in/juju/charm.v2"

	"github.com/juju/charmstore/internal/blobstore"
)

type archiverTo interface {
	ArchiveTo(io.Writer) error
}

type nopCloser struct {
	io.ReadSeeker
}

func (nopCloser) Close() error {
	return nil
}

// NopCloser returns a blobstore.ReadSeekCloser with a no-op Close method
// wrapping the provided ReadSeeker r.
func NopCloser(r io.ReadSeeker) blobstore.ReadSeekCloser {
	return nopCloser{r}
}

// getArchive is used to turn the current charm and bundle implementations
// into ReadSeekClosers for the corresponding archive.
func getArchive(c interface{}) (blobstore.ReadSeekCloser, error) {
	var path string
	switch c := c.(type) {
	case archiverTo:
		// E.g. charm.CharmDir or charm.BundleDir.
		var buffer bytes.Buffer
		if err := c.ArchiveTo(&buffer); err != nil {
			return nil, err
		}
		return NopCloser(bytes.NewReader(buffer.Bytes())), nil
	case *charm.BundleArchive:
		path = c.Path
	case *charm.CharmArchive:
		path = c.Path
	default:
		return nil, fmt.Errorf("cannot get the archive for charm type %T", c)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return file, nil
}
