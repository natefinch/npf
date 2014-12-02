// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore_test

import (
	"archive/zip"
	"bytes"
	"io"
	"io/ioutil"
	"strings"

	jujutesting "github.com/juju/testing"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/mongodoc"
)

type zipSuite struct {
	jujutesting.IsolationSuite
	contents map[string]string
}

var _ = gc.Suite(&zipSuite{})

func (s *zipSuite) SetUpSuite(c *gc.C) {
	s.IsolationSuite.SetUpSuite(c)
	s.contents = map[string]string{
		"readme.md":              "readme contents",
		"uncompressed_readme.md": "readme contents",
		"icon.svg":               "icon contents",
		"metadata.yaml":          "metadata contents",
		"empty":                  "",
		"uncompressed_empty":     "",
	}
}

func (s *zipSuite) makeZipReader(c *gc.C, contents map[string]string) (io.ReadSeeker, []*zip.File) {
	// Create a customized zip archive in memory.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range contents {
		header := &zip.FileHeader{
			Name:   name,
			Method: zip.Deflate,
		}
		if strings.HasPrefix(name, "uncompressed_") {
			header.Method = zip.Store
		}
		f, err := w.CreateHeader(header)
		c.Assert(err, gc.IsNil)
		_, err = f.Write([]byte(content))
		c.Assert(err, gc.IsNil)
	}
	c.Assert(w.Close(), gc.IsNil)

	// Retrieve the zip files in the archive.
	zipReader := bytes.NewReader(buf.Bytes())
	r, err := zip.NewReader(zipReader, int64(buf.Len()))
	c.Assert(err, gc.IsNil)
	c.Assert(r.File, gc.HasLen, len(contents))
	return zipReader, r.File
}

func (s *zipSuite) TestZipFileReader(c *gc.C) {
	zipReader, files := s.makeZipReader(c, s.contents)

	// Check that a ZipFile created from each file in the archive
	// can be read correctly.
	for i, f := range files {
		c.Logf("test %d: %s", i, f.Name)
		zf, err := charmstore.NewZipFile(f)
		c.Assert(err, gc.IsNil)
		zfr, err := charmstore.ZipFileReader(zipReader, zf)
		c.Assert(err, gc.IsNil)
		content, err := ioutil.ReadAll(zfr)
		c.Assert(err, gc.IsNil)
		c.Assert(string(content), gc.Equals, s.contents[f.Name])
	}
}

func (s *zipSuite) TestZipFileReaderWithErrorOnSeek(c *gc.C) {
	er := &seekErrorReader{}
	r, err := charmstore.ZipFileReader(er, mongodoc.ZipFile{})
	c.Assert(err, gc.ErrorMatches, "cannot seek to 0 in zip content: foiled!")
	c.Assert(r, gc.Equals, nil)
}

type seekErrorReader struct {
	io.Reader
}

func (r *seekErrorReader) Seek(offset int64, whence int) (int64, error) {
	return 0, errgo.New("foiled!")
}

func (s *zipSuite) TestNewZipFile(c *gc.C) {
	_, files := s.makeZipReader(c, s.contents)

	// Check that we can create a new ZipFile from
	// each zip file in the archive.
	for i, f := range files {
		c.Logf("test %d: %s", i, f.Name)
		zf, err := charmstore.NewZipFile(f)
		c.Assert(err, gc.IsNil)
		offset, err := f.DataOffset()
		c.Assert(err, gc.IsNil)

		c.Assert(zf.Offset, gc.Equals, offset)
		c.Assert(zf.Size, gc.Equals, int64(f.CompressedSize64))
		c.Assert(zf.Compressed, gc.Equals, !strings.HasPrefix(f.Name, "uncompressed_"))
	}
}

func (s *zipSuite) TestNewZipFileWithCompressionMethodError(c *gc.C) {
	_, files := s.makeZipReader(c, map[string]string{"foo": "contents"})
	f := files[0]
	f.Method = 99
	_, err := charmstore.NewZipFile(f)
	c.Assert(err, gc.ErrorMatches, `unknown zip compression method for "foo"`)
}
