// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"sort"

	jc "github.com/juju/testing/checkers"
	"gopkg.in/juju/charm.v2"
	"gopkg.in/juju/charm.v2/testing"
	"gopkg.in/mgo.v2"
	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore/internal/blobstore"
	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/storetesting"
	"github.com/juju/charmstore/params"
)

type StoreSuite struct {
	storetesting.IsolatedMgoSuite
}

var _ = gc.Suite(&StoreSuite{})

func (s *StoreSuite) TestAddCharm(c *gc.C) {
	store := NewStore(s.Session.DB("foo"))
	url := charm.MustParseURL("cs:precise/wordpress-23")
	wordpress := testing.Charms.CharmDir("wordpress")
	err := store.AddCharm(url, wordpress)
	c.Assert(err, gc.IsNil)

	var doc mongodoc.Entity
	err = store.DB.Entities().FindId("cs:precise/wordpress-23").One(&doc)
	c.Assert(err, gc.IsNil)

	// The entity doc has been correctly added to the mongo collection.
	size, hash := mustGetSizeAndHash(wordpress)
	sort.Strings(doc.CharmProvidedInterfaces)
	sort.Strings(doc.CharmRequiredInterfaces)
	c.Assert(doc, jc.DeepEquals, mongodoc.Entity{
		URL:                     (*params.CharmURL)(url),
		BaseURL:                 mustParseURL("cs:wordpress"),
		BlobHash:                hash,
		Size:                    size,
		CharmMeta:               wordpress.Meta(),
		CharmActions:            wordpress.Actions(),
		CharmConfig:             wordpress.Config(),
		CharmProvidedInterfaces: []string{"http", "logging", "monitoring"},
		CharmRequiredInterfaces: []string{"mysql", "varnish"},
	})

	// The charm archive has been properly added to the blob store.
	r, obtainedSize, err := store.BlobDB.Open(hash)
	c.Assert(err, gc.IsNil)
	c.Assert(obtainedSize, gc.Equals, size)
	data, err := ioutil.ReadAll(r)
	c.Assert(err, gc.IsNil)
	charmArchive, err := charm.ReadCharmArchiveBytes(data)
	c.Assert(err, gc.IsNil)
	c.Assert(charmArchive.Meta(), jc.DeepEquals, wordpress.Meta())
	c.Assert(charmArchive.Config(), jc.DeepEquals, wordpress.Config())
	c.Assert(charmArchive.Actions(), jc.DeepEquals, wordpress.Actions())
	c.Assert(charmArchive.Revision(), jc.DeepEquals, wordpress.Revision())

	// Try inserting the charm again - it should fail because the charm is
	// already there.
	err = store.AddCharm(url, wordpress)
	c.Assert(err, jc.Satisfies, mgo.IsDup)
}

func (s *StoreSuite) TestAddBundle(c *gc.C) {
	store := NewStore(s.Session.DB("foo"))
	url := charm.MustParseURL("cs:bundle/wordpress-simple-42")
	bundle := testing.Charms.BundleDir("wordpress")
	err := store.AddBundle(url, bundle)
	c.Assert(err, gc.IsNil)

	var doc mongodoc.Entity
	err = store.DB.Entities().FindId("cs:bundle/wordpress-simple-42").One(&doc)
	c.Assert(err, gc.IsNil)

	// The entity doc has been correctly added to the mongo collection.
	size, hash := mustGetSizeAndHash(bundle)
	c.Assert(doc, jc.DeepEquals, mongodoc.Entity{
		URL:          (*params.CharmURL)(url),
		BaseURL:      mustParseURL("cs:wordpress-simple"),
		BlobHash:     hash,
		Size:         size,
		BundleData:   bundle.Data(),
		BundleReadMe: bundle.ReadMe(),
		BundleCharms: []*params.CharmURL{
			mustParseURL("wordpress"),
			mustParseURL("mysql"),
		},
	})

	// The bundle archive has been properly added to the blob store.
	r, obtainedSize, err := store.BlobDB.Open(hash)
	c.Assert(err, gc.IsNil)
	c.Assert(obtainedSize, gc.Equals, size)
	data, err := ioutil.ReadAll(r)
	c.Assert(err, gc.IsNil)
	verifyConstraints := func(c string) error { return nil }
	bundleArchive, err := charm.ReadBundleArchiveBytes(data, verifyConstraints)
	c.Assert(err, gc.IsNil)
	c.Assert(bundleArchive.Data(), jc.DeepEquals, bundle.Data())
	c.Assert(bundleArchive.ReadMe(), jc.DeepEquals, bundle.ReadMe())

	// Try inserting the bundle again - it should fail because the bundle is
	// already there.
	err = store.AddBundle(url, bundle)
	c.Assert(err, jc.Satisfies, mgo.IsDup)
}

func mustParseURL(urlStr string) *params.CharmURL {
	ref, _, err := charm.ParseReference(urlStr)
	if err != nil {
		panic(err)
	}
	return &params.CharmURL{
		Reference: ref,
	}
}

func mustGetSizeAndHash(a archiverTo) (int64, string) {
	var buffer bytes.Buffer
	if err := a.ArchiveTo(&buffer); err != nil {
		panic(err)
	}
	hash := blobstore.NewHash()
	size, err := io.Copy(hash, &buffer)
	if err != nil {
		panic(err)
	}
	return size, fmt.Sprintf("%x", hash.Sum(nil))
}
