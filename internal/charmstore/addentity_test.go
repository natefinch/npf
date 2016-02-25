// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore // import "gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"regexp"
	"sort"
	"time"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"

	"gopkg.in/juju/charmstore.v5-unstable/internal/blobstore"
	"gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"
	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
	"gopkg.in/juju/charmstore.v5-unstable/internal/storetesting"
)

type AddEntitySuite struct {
	commonSuite
}

var _ = gc.Suite(&AddEntitySuite{})

func (s *AddEntitySuite) TestAddCharmWithUser(c *gc.C) {
	store := s.newStore(c, false)
	defer store.Close()

	wordpress := storetesting.Charms.CharmDir("wordpress")
	url := router.MustNewResolvedURL("cs:~who/precise/wordpress-23", -1)
	err := store.AddCharmWithArchive(url, wordpress)
	c.Assert(err, gc.IsNil)
	assertBaseEntity(c, store, mongodoc.BaseURL(&url.URL), false)
}

func (s *AddEntitySuite) TestAddPromulgatedCharmDir(c *gc.C) {
	charmDir := storetesting.Charms.CharmDir("wordpress")
	s.checkAddCharm(c, charmDir, router.MustNewResolvedURL("~charmers/precise/wordpress-1", 1))
}

func (s *AddEntitySuite) TestAddPromulgatedCharmArchive(c *gc.C) {
	charmArchive := storetesting.Charms.CharmArchive(c.MkDir(), "wordpress")
	s.checkAddCharm(c, charmArchive, router.MustNewResolvedURL("~charmers/precise/wordpress-1", 1))
}

func (s *AddEntitySuite) TestAddUserOwnedCharmDir(c *gc.C) {
	charmDir := storetesting.Charms.CharmDir("wordpress")
	s.checkAddCharm(c, charmDir, router.MustNewResolvedURL("~charmers/precise/wordpress-1", -1))
}

func (s *AddEntitySuite) TestAddUserOwnedCharmArchive(c *gc.C) {
	charmArchive := storetesting.Charms.CharmArchive(c.MkDir(), "wordpress")
	s.checkAddCharm(c, charmArchive, router.MustNewResolvedURL("~charmers/precise/wordpress-1", -1))
}

func (s *AddEntitySuite) TestAddBundleDir(c *gc.C) {
	bundleDir := storetesting.Charms.BundleDir("wordpress-simple")
	s.checkAddBundle(c, bundleDir, router.MustNewResolvedURL("~charmers/bundle/wordpress-simple-2", 3))
}

func (s *AddEntitySuite) TestAddBundleArchive(c *gc.C) {
	bundleArchive, err := charm.ReadBundleArchive(
		storetesting.Charms.BundleArchivePath(c.MkDir(), "wordpress-simple"),
	)
	s.addRequiredCharms(c, bundleArchive)
	c.Assert(err, gc.IsNil)
	s.checkAddBundle(c, bundleArchive, router.MustNewResolvedURL("~charmers/bundle/wordpress-simple-2", 3))
}

func (s *AddEntitySuite) TestAddUserOwnedBundleDir(c *gc.C) {
	bundleDir := storetesting.Charms.BundleDir("wordpress-simple")
	s.checkAddBundle(c, bundleDir, router.MustNewResolvedURL("~charmers/bundle/wordpress-simple-1", -1))
}

func (s *AddEntitySuite) TestAddUserOwnedBundleArchive(c *gc.C) {
	bundleArchive, err := charm.ReadBundleArchive(
		storetesting.Charms.BundleArchivePath(c.MkDir(), "wordpress-simple"),
	)
	c.Assert(err, gc.IsNil)
	s.checkAddBundle(c, bundleArchive, router.MustNewResolvedURL("~charmers/bundle/wordpress-simple-1", -1))
}

func (s *AddEntitySuite) TestAddCharmWithBundleSeries(c *gc.C) {
	store := s.newStore(c, false)
	defer store.Close()
	ch := storetesting.Charms.CharmArchive(c.MkDir(), "wordpress")
	err := store.AddCharmWithArchive(router.MustNewResolvedURL("~charmers/bundle/wordpress-2", -1), ch)
	c.Assert(err, gc.ErrorMatches, `cannot read bundle archive: archive file "bundle.yaml" not found`)
}

func (s *AddEntitySuite) TestAddCharmWithMultiSeries(c *gc.C) {
	store := s.newStore(c, false)
	defer store.Close()
	ch := storetesting.Charms.CharmArchive(c.MkDir(), "multi-series")
	s.checkAddCharm(c, ch, router.MustNewResolvedURL("~charmers/multi-series-1", 1))
	// Make sure it can be accessed with a number of names
	e, err := store.FindBestEntity(charm.MustParseURL("~charmers/multi-series-1"), mongodoc.UnpublishedChannel, nil)
	c.Assert(err, gc.IsNil)
	c.Assert(e.URL.String(), gc.Equals, "cs:~charmers/multi-series-1")
	e, err = store.FindBestEntity(charm.MustParseURL("~charmers/trusty/multi-series-1"), mongodoc.UnpublishedChannel, nil)
	c.Assert(err, gc.IsNil)
	c.Assert(e.URL.String(), gc.Equals, "cs:~charmers/multi-series-1")
	e, err = store.FindBestEntity(charm.MustParseURL("~charmers/wily/multi-series-1"), mongodoc.UnpublishedChannel, nil)
	c.Assert(err, gc.IsNil)
	c.Assert(e.URL.String(), gc.Equals, "cs:~charmers/multi-series-1")
	_, err = store.FindBestEntity(charm.MustParseURL("~charmers/precise/multi-series-1"), mongodoc.UnpublishedChannel, nil)
	c.Assert(err, gc.ErrorMatches, "no matching charm or bundle for cs:~charmers/precise/multi-series-1")
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrNotFound)
}

func (s *AddEntitySuite) TestAddCharmWithSeriesWhenThereIsAnExistingMultiSeriesVersion(c *gc.C) {
	store := s.newStore(c, false)
	defer store.Close()
	ch := storetesting.Charms.CharmArchive(c.MkDir(), "multi-series")
	err := store.AddCharmWithArchive(router.MustNewResolvedURL("~charmers/multi-series-1", -1), ch)
	c.Assert(err, gc.IsNil)
	ch = storetesting.Charms.CharmArchive(c.MkDir(), "wordpress")
	err = store.AddCharmWithArchive(router.MustNewResolvedURL("~charmers/trusty/multi-series-2", -1), ch)
	c.Assert(err, gc.ErrorMatches, `charm name duplicates multi-series charm name cs:~charmers/multi-series-1`)
}

func (s *AddEntitySuite) TestAddCharmWithMultiSeriesToES(c *gc.C) {
	store := s.newStore(c, true)
	defer store.Close()
	ch := storetesting.Charms.CharmArchive(c.MkDir(), "multi-series")
	s.checkAddCharm(c, ch, router.MustNewResolvedURL("~charmers/juju-gui-1", 1))
}

func (s *AddEntitySuite) TestAddBundleDuplicatingCharm(c *gc.C) {
	store := s.newStore(c, false)
	defer store.Close()
	ch := storetesting.Charms.CharmDir("wordpress")
	err := store.AddCharmWithArchive(router.MustNewResolvedURL("~tester/precise/wordpress-2", -1), ch)
	c.Assert(err, gc.IsNil)

	b := storetesting.Charms.BundleDir("wordpress-simple")
	s.addRequiredCharms(c, b)
	err = store.AddBundleWithArchive(router.MustNewResolvedURL("~tester/bundle/wordpress-5", -1), b)
	c.Assert(err, gc.ErrorMatches, "bundle name duplicates charm name cs:~tester/precise/wordpress-2")
}

func (s *AddEntitySuite) TestAddCharmDuplicatingBundle(c *gc.C) {
	store := s.newStore(c, false)
	defer store.Close()

	b := storetesting.Charms.BundleDir("wordpress-simple")
	s.addRequiredCharms(c, b)
	err := store.AddBundleWithArchive(router.MustNewResolvedURL("~charmers/bundle/wordpress-simple-2", -1), b)
	c.Assert(err, gc.IsNil)

	ch := storetesting.Charms.CharmDir("wordpress")
	err = store.AddCharmWithArchive(router.MustNewResolvedURL("~charmers/precise/wordpress-simple-5", -1), ch)
	c.Assert(err, gc.ErrorMatches, "charm name duplicates bundle name cs:~charmers/bundle/wordpress-simple-2")
}

var uploadEntityErrorsTests = []struct {
	about       string
	url         string
	upload      ArchiverTo
	blobHash    string
	blobSize    int64
	expectError string
	expectCause error
}{{
	about:       "revision not specified",
	url:         "~charmers/precise/wordpress",
	upload:      storetesting.NewCharm(nil),
	expectError: "entity id does not specify revision",
	expectCause: params.ErrEntityIdNotAllowed,
}, {
	about:       "user not specified",
	url:         "precise/wordpress-23",
	upload:      storetesting.NewCharm(nil),
	expectError: "entity id does not specify user",
	expectCause: params.ErrEntityIdNotAllowed,
}, {
	about:       "hash mismatch",
	url:         "~charmers/precise/wordpress-0",
	upload:      storetesting.NewCharm(nil),
	blobHash:    "blahblah",
	expectError: "cannot put archive blob: hash mismatch",
	// It would be nice if this was:
	// expectCause: params.ErrInvalidEntity,
}, {
	about:       "size mismatch",
	url:         "~charmers/precise/wordpress-0",
	upload:      storetesting.NewCharm(nil),
	blobSize:    99999,
	expectError: "cannot put archive blob: cannot calculate data checksums: EOF",
	// It would be nice if the above error was better and
	// the cause was:
	// expectCause: params.ErrInvalidEntity,
}, {
	about:       "charm uploaded to bundle URL",
	url:         "~charmers/bundle/foo-0",
	upload:      storetesting.NewCharm(nil),
	expectError: `cannot read bundle archive: archive file "bundle.yaml" not found`,
	// It would be nice if this was:
	// expectCause: params.ErrInvalidEntity,
}, {
	about: "bundle uploaded to charm URL",
	url:   "~charmers/precise/foo-0",
	upload: storetesting.NewBundle(&charm.BundleData{
		Services: map[string]*charm.ServiceSpec{
			"foo": {
				Charm: "foo",
			},
		},
	}),
	expectError: `cannot read charm archive: archive file "metadata.yaml" not found`,
	// It would be nice if this was:
	// expectCause: params.ErrInvalidEntity,
}, {
	about:       "banned relation name",
	url:         "~charmers/precise/foo-0",
	upload:      storetesting.NewCharm(storetesting.RelationMeta("requires relation-name foo")),
	expectError: `relation relation-name has almost certainly not been changed from the template`,
	expectCause: params.ErrInvalidEntity,
}, {
	about:       "banned interface name",
	url:         "~charmers/precise/foo-0",
	upload:      storetesting.NewCharm(storetesting.RelationMeta("requires foo interface-name")),
	expectError: `interface interface-name in relation foo has almost certainly not been changed from the template`,
	expectCause: params.ErrInvalidEntity,
}, {
	about:       "unrecognized series",
	url:         "~charmers/precise/foo-0",
	upload:      storetesting.NewCharm(storetesting.MetaWithSupportedSeries(nil, "badseries")),
	expectError: `unrecognized series "badseries" in metadata`,
	expectCause: params.ErrInvalidEntity,
}, {
	about:       "inconsistent series",
	url:         "~charmers/trusty/foo-0",
	upload:      storetesting.NewCharm(storetesting.MetaWithSupportedSeries(nil, "trusty", "win10")),
	expectError: `cannot mix series from ubuntu and windows in single charm`,
	expectCause: params.ErrInvalidEntity,
}, {
	about:       "series not specified",
	url:         "~charmers/foo-0",
	upload:      storetesting.NewCharm(nil),
	expectError: `series not specified in url or charm metadata`,
	expectCause: params.ErrEntityIdNotAllowed,
}, {
	about:       "series not allowed by metadata",
	url:         "~charmers/precise/foo-0",
	upload:      storetesting.NewCharm(storetesting.MetaWithSupportedSeries(nil, "trusty")),
	expectError: `"precise" series not listed in charm metadata`,
	expectCause: params.ErrEntityIdNotAllowed,
}, {
	about: "bundle refers to non-existent charm",
	url:   "~charmers/bundle/foo-0",
	upload: storetesting.NewBundle(&charm.BundleData{
		Services: map[string]*charm.ServiceSpec{
			"foo": {
				Charm: "bad-charm",
			},
		},
	}),
	expectError: regexp.QuoteMeta(`bundle verification failed: ["service \"foo\" refers to non-existent charm \"bad-charm\""]`),
	expectCause: params.ErrInvalidEntity,
}, {
	about:       "bundle verification fails",
	url:         "~charmers/bundle/foo-0",
	upload:      storetesting.NewBundle(&charm.BundleData{}),
	expectError: regexp.QuoteMeta(`bundle verification failed: ["at least one service must be specified"]`),
	expectCause: params.ErrInvalidEntity,
}, {
	about:       "invalid zip format",
	url:         "~charmers/foo-0",
	upload:      zipWithInvalidFormat(),
	expectError: `cannot read charm archive: zip: not a valid zip file`,
	expectCause: params.ErrInvalidEntity,
}, {
	about:       "invalid zip algorithm",
	url:         "~charmers/foo-0",
	upload:      zipWithInvalidAlgorithm(),
	expectError: `cannot read charm archive: zip: unsupported compression algorithm`,
	expectCause: params.ErrInvalidEntity,
}, {
	about:       "invalid zip checksum",
	url:         "~charmers/foo-0",
	upload:      zipWithInvalidChecksum(),
	expectError: `cannot read charm archive: zip: checksum error`,
	expectCause: params.ErrInvalidEntity,
}}

func (s *AddEntitySuite) TestUploadEntityErrors(c *gc.C) {
	store := s.newStore(c, true)
	defer store.Close()
	for i, test := range uploadEntityErrorsTests {
		c.Logf("test %d: %s", i, test.about)
		var buf bytes.Buffer
		err := test.upload.ArchiveTo(&buf)
		c.Assert(err, gc.IsNil)
		if test.blobHash == "" {
			h := blobstore.NewHash()
			h.Write(buf.Bytes())
			test.blobHash = fmt.Sprintf("%x", h.Sum(nil))
		}
		if test.blobSize == 0 {
			test.blobSize = int64(len(buf.Bytes()))
		}
		url := &router.ResolvedURL{
			URL: *charm.MustParseURL(test.url),
		}
		err = store.UploadEntity(url, &buf, test.blobHash, test.blobSize)
		c.Assert(err, gc.ErrorMatches, test.expectError)
		if test.expectCause != nil {
			c.Assert(errgo.Cause(err), gc.Equals, test.expectCause)
		} else {
			c.Assert(errgo.Cause(err), gc.Equals, err)
		}
	}
}

func (s *AddEntitySuite) checkAddCharm(c *gc.C, ch charm.Charm, url *router.ResolvedURL) {
	store := s.newStore(c, true)
	defer store.Close()

	// Add the charm to the store.
	beforeAdding := time.Now()
	err := store.AddCharmWithArchive(url, ch)
	c.Assert(err, gc.IsNil)
	afterAdding := time.Now()

	var doc *mongodoc.Entity
	err = store.DB.Entities().FindId(&url.URL).One(&doc)
	c.Assert(err, gc.IsNil)

	// The entity doc has been correctly added to the mongo collection.
	size, hash, hash256 := getSizeAndHashes(ch)
	sort.Strings(doc.CharmProvidedInterfaces)
	sort.Strings(doc.CharmRequiredInterfaces)

	// Check the upload time and then reset it to its zero value
	// so that we can test the deterministic parts later.
	c.Assert(doc.UploadTime, jc.TimeBetween(beforeAdding, afterAdding))

	doc.UploadTime = time.Time{}

	assertDoc := assertBlobFields(c, doc, url, hash, hash256, size)
	c.Assert(assertDoc, jc.DeepEquals, denormalizedEntity(&mongodoc.Entity{
		URL:                     &url.URL,
		BlobHash:                hash,
		BlobHash256:             hash256,
		Size:                    size,
		CharmMeta:               ch.Meta(),
		CharmActions:            ch.Actions(),
		CharmConfig:             ch.Config(),
		CharmProvidedInterfaces: []string{"http", "logging", "monitoring"},
		CharmRequiredInterfaces: []string{"mysql", "varnish"},
		PromulgatedURL:          url.PromulgatedURL(),
		SupportedSeries:         ch.Meta().Series,
	}))

	// The charm archive has been properly added to the blob store.
	r, obtainedSize, err := store.BlobStore.Open(doc.BlobName)
	c.Assert(err, gc.IsNil)
	defer r.Close()
	c.Assert(obtainedSize, gc.Equals, size)
	data, err := ioutil.ReadAll(r)
	c.Assert(err, gc.IsNil)
	charmArchive, err := charm.ReadCharmArchiveBytes(data)
	c.Assert(err, gc.IsNil)
	c.Assert(charmArchive.Meta(), jc.DeepEquals, ch.Meta())
	c.Assert(charmArchive.Config(), jc.DeepEquals, ch.Config())
	c.Assert(charmArchive.Actions(), jc.DeepEquals, ch.Actions())
	c.Assert(charmArchive.Revision(), jc.DeepEquals, ch.Revision())

	// Check that the base entity has been properly created.
	assertBaseEntity(c, store, mongodoc.BaseURL(&url.URL), url.PromulgatedRevision != -1)

	// Try inserting the charm again - it should fail because the charm is
	// already there.
	err = store.AddCharmWithArchive(url, ch)
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrDuplicateUpload)
}

func (s *AddEntitySuite) checkAddBundle(c *gc.C, bundle charm.Bundle, url *router.ResolvedURL) {
	store := s.newStore(c, true)
	defer store.Close()
	// Add the bundle to the store.
	beforeAdding := time.Now()
	s.addRequiredCharms(c, bundle)
	err := store.AddBundleWithArchive(url, bundle)
	c.Assert(err, gc.IsNil)
	afterAdding := time.Now()

	var doc *mongodoc.Entity
	err = store.DB.Entities().FindId(&url.URL).One(&doc)
	c.Assert(err, gc.IsNil)
	sort.Sort(orderedURLs(doc.BundleCharms))

	// Check the upload time and then reset it to its zero value
	// so that we can test the deterministic parts later.
	c.Assert(doc.UploadTime, jc.TimeBetween(beforeAdding, afterAdding))
	doc.UploadTime = time.Time{}

	// The entity doc has been correctly added to the mongo collection.
	size, hash, hash256 := getSizeAndHashes(bundle)

	assertDoc := assertBlobFields(c, doc, url, hash, hash256, size)
	c.Assert(assertDoc, jc.DeepEquals, denormalizedEntity(&mongodoc.Entity{
		URL:          &url.URL,
		BlobHash:     hash,
		BlobHash256:  hash256,
		Size:         size,
		BundleData:   bundle.Data(),
		BundleReadMe: bundle.ReadMe(),
		BundleCharms: []*charm.URL{
			charm.MustParseURL("mysql"),
			charm.MustParseURL("wordpress"),
		},
		BundleMachineCount: newInt(2),
		BundleUnitCount:    newInt(2),
		PromulgatedURL:     url.PromulgatedURL(),
	}))

	// The bundle archive has been properly added to the blob store.
	r, obtainedSize, err := store.BlobStore.Open(doc.BlobName)
	c.Assert(err, gc.IsNil)
	defer r.Close()
	c.Assert(obtainedSize, gc.Equals, size)
	data, err := ioutil.ReadAll(r)
	c.Assert(err, gc.IsNil)
	bundleArchive, err := charm.ReadBundleArchiveBytes(data)
	c.Assert(err, gc.IsNil)
	c.Assert(bundleArchive.Data(), jc.DeepEquals, bundle.Data())
	c.Assert(bundleArchive.ReadMe(), jc.DeepEquals, bundle.ReadMe())

	// Check that the base entity has been properly created.
	assertBaseEntity(c, store, mongodoc.BaseURL(&url.URL), url.PromulgatedRevision != -1)

	// Try inserting the bundle again - it should fail because the bundle is
	// already there.
	err = store.AddBundleWithArchive(url, bundle)
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrDuplicateUpload, gc.Commentf("error: %v", err))
}

// assertBlobFields asserts that the blob-related fields in doc are as expected.
// It returns a copy of doc with unpredictable fields zeroed out.
func assertBlobFields(c *gc.C, doc *mongodoc.Entity, url *router.ResolvedURL, hash, hash256 string, size int64) *mongodoc.Entity {
	doc1 := *doc
	doc = &doc1

	// The blob name is random, but we check that it's
	// in the correct format, and non-empty.
	blobName := doc.BlobName
	c.Assert(blobName, gc.Matches, "[0-9a-z]+")
	doc.BlobName = ""
	// The PreV5* fields are unpredictable, so zero them out
	// for the purposes of comparison.
	if doc.CharmMeta != nil && len(doc.CharmMeta.Series) > 0 {
		// It's a multi-series charm, so the PreV5* fields should be active.
		if doc.PreV5BlobSize <= doc.Size {
			c.Fatalf("pre-v5 blobsize %d is unexpectedly less than original blob size %d", doc.PreV5BlobSize, doc.Size)
		}
		c.Assert(doc.PreV5BlobHash, gc.Not(gc.Equals), "")
		c.Assert(doc.PreV5BlobHash, gc.Not(gc.Equals), hash)
		c.Assert(doc.PreV5BlobHash256, gc.Not(gc.Equals), "")
		c.Assert(doc.PreV5BlobHash256, gc.Not(gc.Equals), hash256)
	} else {
		c.Assert(doc.PreV5BlobSize, gc.Equals, doc.Size)
		c.Assert(doc.PreV5BlobHash, gc.Equals, doc.BlobHash)
		c.Assert(doc.PreV5BlobHash256, gc.Equals, doc.BlobHash256)
	}
	doc.PreV5BlobSize = 0
	doc.PreV5BlobHash = ""
	doc.PreV5BlobHash256 = ""
	return doc
}

func assertBaseEntity(c *gc.C, store *Store, url *charm.URL, promulgated bool) {
	baseEntity, err := store.FindBaseEntity(url, nil)
	c.Assert(err, gc.IsNil)
	acls := mongodoc.ACL{
		Read:  []string{url.User},
		Write: []string{url.User},
	}
	expectACLs := map[mongodoc.Channel]mongodoc.ACL{
		mongodoc.StableChannel:      acls,
		mongodoc.DevelopmentChannel: acls,
		mongodoc.UnpublishedChannel: acls,
	}
	c.Assert(storetesting.NormalizeBaseEntity(baseEntity), jc.DeepEquals, storetesting.NormalizeBaseEntity(&mongodoc.BaseEntity{
		URL:         url,
		User:        url.User,
		Name:        url.Name,
		Promulgated: mongodoc.IntBool(promulgated),
		ChannelACLs: expectACLs,
	}))
}

type orderedURLs []*charm.URL

func (o orderedURLs) Less(i, j int) bool {
	return o[i].String() < o[j].String()
}

func (o orderedURLs) Swap(i, j int) {
	o[i], o[j] = o[j], o[i]
}

func (o orderedURLs) Len() int {
	return len(o)
}

type byteArchiver []byte

func (a byteArchiver) ArchiveTo(w io.Writer) error {
	_, err := w.Write(a)
	return err
}

func zipWithInvalidFormat() ArchiverTo {
	return byteArchiver(nil)
}

func zipWithInvalidChecksum() ArchiverTo {
	return byteArchiver(
		"PK\x03\x04\x14\x00\b\x00\x00\x00\x00\x00\x00" +
			"\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00" +
			"\x00\x00\r\x00\x00\x00metadata.yamlielloPK\a\b" +
			"\x86\xa6\x106\x05\x00\x00\x00\x05\x00\x00\x00PK" +
			"\x01\x02\x14\x00\x14\x00\b\x00\x00\x00\x00\x00" +
			"\x00\x00\x86\xa6\x106\x05\x00\x00\x00\x05\x00" +
			"\x00\x00\r\x00\x00\x00\x00\x00\x00\x00\x00\x00" +
			"\x00\x00\x00\x00\x00\x00\x00\x00metadata.yamlPK" +
			"\x05\x06\x00\x00\x00\x00\x01\x00\x01\x00;\x00" +
			"\x00\x00@\x00\x00\x00\x00\x00",
	)

	data := storetesting.NewCharm(nil).Bytes()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		panic(err)
	}
	// Change the contents of a file so
	// that it won't (probably) fit the checksum
	// any more.
	off, err := zr.File[0].DataOffset()
	if err != nil {
		panic(err)
	}
	data[off] += 2
	return byteArchiver(data)
}

func zipWithInvalidAlgorithm() ArchiverTo {
	return byteArchiver(
		"PK\x03\x04\x14\x00\b\x00\t\x00\x00\x00" +
			"\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00" +
			"\x00\x00\x00\x00\r\x00\x00\x00metadata.yamlhello" +
			"PK\a\b\x86\xa6\x106\x05\x00\x00\x00\x05\x00" +
			"\x00\x00PK\x01\x02\x14\x00\x14\x00\b\x00" +
			"\t\x00\x00\x00\x00\x00\x86\xa6\x106\x05\x00" +
			"\x00\x00\x05\x00\x00\x00\r\x00\x00\x00\x00" +
			"\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00" +
			"\x00\x00metadata.yamlPK\x05\x06\x00\x00\x00" +
			"\x00\x01\x00\x01\x00;\x00\x00\x00@\x00\x00" +
			"\x00\x00\x00",
	)
}
