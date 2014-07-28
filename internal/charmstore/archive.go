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

// getArchive is used to turn the current charm and bundle implementations
// into ReadSeekClosers for their corresponding archive.
func getArchive(c interface{}) (blobstore.ReadSeekCloser, error) {
	var path string
	switch c := c.(type) {
	case archiverTo:
		// For example: charm.CharmDir or charm.BundleDir.
		var buffer bytes.Buffer
		if err := c.ArchiveTo(&buffer); err != nil {
			return nil, err
		}
		return nopCloser(bytes.NewReader(buffer.Bytes())), nil
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

type nopCloserReadSeeker struct {
	io.ReadSeeker
}

func (nopCloserReadSeeker) Close() error {
	return nil
}

// nopCloser returns a blobstore.ReadSeekCloser with a no-op Close method
// wrapping the provided ReadSeeker r.
func nopCloser(r io.ReadSeeker) blobstore.ReadSeekCloser {
	return nopCloserReadSeeker{r}
}
