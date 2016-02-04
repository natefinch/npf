// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore // import "gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"

import (
	"io/ioutil"
	"sort"
	"time"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"

	"gopkg.in/juju/charmstore.v5-unstable/elasticsearch"
	"gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"
	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
	"gopkg.in/juju/charmstore.v5-unstable/internal/storetesting"
)

type AddEntitySuite struct {
	commonSuite
}

var _ = gc.Suite(&AddEntitySuite{})

func (s *AddEntitySuite) TestAddCharmDirIndexed(c *gc.C) {
	charmDir := storetesting.Charms.CharmDir("wordpress")
	s.checkAddCharm(c, charmDir, true, router.MustNewResolvedURL("cs:~charmers/precise/wordpress-2", -1))
}

func (s *AddEntitySuite) TestAddCharmArchiveIndexed(c *gc.C) {
	charmArchive := storetesting.Charms.CharmArchive(c.MkDir(), "wordpress")
	s.checkAddCharm(c, charmArchive, true, router.MustNewResolvedURL("cs:~charmers/precise/wordpress-2", -1))
}

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
	s.checkAddCharm(c, charmDir, false, router.MustNewResolvedURL("~charmers/precise/wordpress-1", 1))
}

func (s *AddEntitySuite) TestAddPromulgatedCharmArchive(c *gc.C) {
	charmArchive := storetesting.Charms.CharmArchive(c.MkDir(), "wordpress")
	s.checkAddCharm(c, charmArchive, false, router.MustNewResolvedURL("~charmers/precise/wordpress-1", 1))
}

func (s *AddEntitySuite) TestAddUserOwnedCharmDir(c *gc.C) {
	charmDir := storetesting.Charms.CharmDir("wordpress")
	s.checkAddCharm(c, charmDir, false, router.MustNewResolvedURL("~charmers/precise/wordpress-1", -1))
}

func (s *AddEntitySuite) TestAddUserOwnedCharmArchive(c *gc.C) {
	charmArchive := storetesting.Charms.CharmArchive(c.MkDir(), "wordpress")
	s.checkAddCharm(c, charmArchive, false, router.MustNewResolvedURL("~charmers/precise/wordpress-1", -1))
}

func (s *AddEntitySuite) TestAddDevelopmentCharmArchive(c *gc.C) {
	charmArchive := storetesting.Charms.CharmArchive(c.MkDir(), "wordpress")
	url := router.MustNewResolvedURL("~charmers/development/precise/wordpress-1", 1)
	s.checkAddCharm(c, charmArchive, false, url)
}

func (s *AddEntitySuite) TestAddBundleDir(c *gc.C) {
	bundleDir := storetesting.Charms.BundleDir("wordpress-simple")
	s.checkAddBundle(c, bundleDir, false, router.MustNewResolvedURL("~charmers/bundle/wordpress-simple-2", 3))
}

func (s *AddEntitySuite) TestAddBundleArchive(c *gc.C) {
	bundleArchive, err := charm.ReadBundleArchive(
		storetesting.Charms.BundleArchivePath(c.MkDir(), "wordpress-simple"),
	)
	s.addRequiredCharms(c, bundleArchive)
	c.Assert(err, gc.IsNil)
	s.checkAddBundle(c, bundleArchive, false, router.MustNewResolvedURL("~charmers/bundle/wordpress-simple-2", 3))
}

func (s *AddEntitySuite) TestAddUserOwnedBundleDir(c *gc.C) {
	bundleDir := storetesting.Charms.BundleDir("wordpress-simple")
	s.checkAddBundle(c, bundleDir, false, router.MustNewResolvedURL("~charmers/bundle/wordpress-simple-1", -1))
}

func (s *AddEntitySuite) TestAddUserOwnedBundleArchive(c *gc.C) {
	bundleArchive, err := charm.ReadBundleArchive(
		storetesting.Charms.BundleArchivePath(c.MkDir(), "wordpress-simple"),
	)
	c.Assert(err, gc.IsNil)
	s.checkAddBundle(c, bundleArchive, false, router.MustNewResolvedURL("~charmers/bundle/wordpress-simple-1", -1))
}

func (s *AddEntitySuite) TestAddDevelopmentBundleArchive(c *gc.C) {
	bundleArchive, err := charm.ReadBundleArchive(
		storetesting.Charms.BundleArchivePath(c.MkDir(), "wordpress-simple"),
	)
	c.Assert(err, gc.IsNil)
	url := router.MustNewResolvedURL("~charmers/development/bundle/wordpress-simple-2", 3)
	s.checkAddBundle(c, bundleArchive, false, url)
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
	s.checkAddCharm(c, ch, false, router.MustNewResolvedURL("~charmers/multi-series-1", 1))
	// Make sure it can be accessed with a number of names
	e, err := store.FindEntity(router.MustNewResolvedURL("~charmers/multi-series-1", 1), nil)
	c.Assert(err, gc.IsNil)
	c.Assert(e.URL.String(), gc.Equals, "cs:~charmers/multi-series-1")
	e, err = store.FindEntity(router.MustNewResolvedURL("~charmers/trusty/multi-series-1", 1), nil)
	c.Assert(err, gc.IsNil)
	c.Assert(e.URL.String(), gc.Equals, "cs:~charmers/multi-series-1")
	e, err = store.FindEntity(router.MustNewResolvedURL("~charmers/wily/multi-series-1", 1), nil)
	c.Assert(err, gc.IsNil)
	c.Assert(e.URL.String(), gc.Equals, "cs:~charmers/multi-series-1")
	_, err = store.FindEntity(router.MustNewResolvedURL("~charmers/precise/multi-series-1", 1), nil)
	c.Assert(err, gc.ErrorMatches, "entity not found")
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
	s.checkAddCharm(c, ch, true, router.MustNewResolvedURL("~charmers/juju-gui-1", 1))
}

var addInvalidCharmURLTests = []string{
	"cs:precise/wordpress-2",         // no user
	"cs:~charmers/precise/wordpress", // no revision
}

func (s *AddEntitySuite) TestAddInvalidCharmURL(c *gc.C) {
	store := s.newStore(c, false)
	defer store.Close()
	ch := storetesting.Charms.CharmArchive(c.MkDir(), "wordpress")
	for i, urlStr := range addInvalidCharmURLTests {
		c.Logf("test %d: %s", i, urlStr)
		err := store.AddCharmWithArchive(&router.ResolvedURL{
			URL:                 *charm.MustParseURL(urlStr),
			PromulgatedRevision: -1,
		}, ch,
		)
		c.Assert(err, gc.ErrorMatches, `charm added with invalid id .*`)
	}
}

var addInvalidBundleURLTests = []string{
	"cs:bundle/wordpress-2",         // no user
	"cs:~charmers/bundle/wordpress", // no revision
}

func (s *AddEntitySuite) TestAddInvalidBundleURL(c *gc.C) {
	store := s.newStore(c, false)
	defer store.Close()
	b := storetesting.Charms.BundleDir("wordpress-simple")
	s.addRequiredCharms(c, b)
	for i, urlStr := range addInvalidBundleURLTests {
		c.Logf("test %d: %s", i, urlStr)
		err := store.AddBundleWithArchive(&router.ResolvedURL{
			URL:                 *charm.MustParseURL(urlStr),
			PromulgatedRevision: -1,
		}, b,
		)
		c.Assert(err, gc.ErrorMatches, `bundle added with invalid id .*`)
	}
}

func (s *AddEntitySuite) TestAddBundleDuplicatingCharm(c *gc.C) {
	store := s.newStore(c, false)
	defer store.Close()
	ch := storetesting.Charms.CharmDir("wordpress")
	err := store.AddCharmWithArchive(router.MustNewResolvedURL("~charmers/precise/wordpress-2", -1), ch)
	c.Assert(err, gc.IsNil)

	b := storetesting.Charms.BundleDir("wordpress-simple")
	s.addRequiredCharms(c, b)
	err = store.AddBundleWithArchive(router.MustNewResolvedURL("~charmers/bundle/wordpress-5", -1), b)
	c.Assert(err, gc.ErrorMatches, "bundle name duplicates charm name cs:~charmers/precise/wordpress-2")
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

func (s *AddEntitySuite) TestAddBundleDirIndexed(c *gc.C) {
	bundleDir := storetesting.Charms.BundleDir("wordpress-simple")
	s.checkAddBundle(c, bundleDir, true, router.MustNewResolvedURL("cs:~charmers/bundle/baboom-2", -1))
}

func (s *AddEntitySuite) TestAddBundleArchiveIndexed(c *gc.C) {
	bundleArchive, err := charm.ReadBundleArchive(
		storetesting.Charms.BundleArchivePath(c.MkDir(), "wordpress-simple"),
	)
	c.Assert(err, gc.IsNil)
	s.addRequiredCharms(c, bundleArchive)
	s.checkAddBundle(c, bundleArchive, true, router.MustNewResolvedURL("cs:~charmers/bundle/baboom-2", -1))
}

func (s *AddEntitySuite) TestAddCharmDirIndexedAndPromulgated(c *gc.C) {
	charmDir := storetesting.Charms.CharmDir("wordpress")
	s.checkAddCharm(c, charmDir, true, router.MustNewResolvedURL("cs:~charmers/precise/wordpress-2", -1))
}

func (s *AddEntitySuite) TestAddCharmArchiveIndexedAndPromulgated(c *gc.C) {
	charmArchive := storetesting.Charms.CharmArchive(c.MkDir(), "wordpress")
	s.checkAddCharm(c, charmArchive, true, router.MustNewResolvedURL("cs:~charmers/precise/wordpress-2", 2))
}

func (s *AddEntitySuite) TestAddBundleDirIndexedAndPromulgated(c *gc.C) {
	bundleDir := storetesting.Charms.BundleDir("wordpress-simple")
	s.checkAddBundle(c, bundleDir, true, router.MustNewResolvedURL("cs:~charmers/bundle/baboom-2", 2))
}

func (s *AddEntitySuite) TestAddBundleArchiveIndexedAndPromulgated(c *gc.C) {
	bundleArchive, err := charm.ReadBundleArchive(
		storetesting.Charms.BundleArchivePath(c.MkDir(), "wordpress-simple"),
	)
	c.Assert(err, gc.IsNil)
	s.checkAddBundle(c, bundleArchive, true, router.MustNewResolvedURL("cs:~charmers/bundle/baboom-2", 2))
}

func (s *AddEntitySuite) checkAddCharm(c *gc.C, ch charm.Charm, addToES bool, url *router.ResolvedURL) {
	var es *elasticsearch.Database
	if addToES {
		es = s.ES
	}
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

	// Ensure the document was indexed in ElasticSearch, if an ES database was provided.
	if es != nil {
		var result SearchDoc
		id := store.ES.getID(doc.URL)
		err = store.ES.GetDocument(s.TestIndex, typeName, id, &result)
		c.Assert(err, gc.IsNil)
		exists, err := store.ES.HasDocument(s.TestIndex, typeName, id)
		c.Assert(err, gc.IsNil)
		c.Assert(exists, gc.Equals, true)
		if purl := url.DocPromulgatedURL(); purl != nil {
			c.Assert(result.PromulgatedURL, jc.DeepEquals, purl)
		}
	}
	// The entity doc has been correctly added to the mongo collection.
	size, hash, hash256 := getSizeAndHashes(ch)
	sort.Strings(doc.CharmProvidedInterfaces)
	sort.Strings(doc.CharmRequiredInterfaces)

	// Check the upload time and then reset it to its zero value
	// so that we can test the deterministic parts later.
	c.Assert(doc.UploadTime, jc.TimeBetween(beforeAdding, afterAdding))

	doc.UploadTime = time.Time{}

	blobName := doc.BlobName
	c.Assert(blobName, gc.Matches, "[0-9a-z]+")
	doc.BlobName = ""

	c.Assert(doc, jc.DeepEquals, denormalizedEntity(&mongodoc.Entity{
		URL:                     &url.URL,
		BlobHash:                hash,
		BlobHash256:             hash256,
		Size:                    size,
		CharmMeta:               ch.Meta(),
		CharmActions:            ch.Actions(),
		CharmConfig:             ch.Config(),
		CharmProvidedInterfaces: []string{"http", "logging", "monitoring"},
		CharmRequiredInterfaces: []string{"mysql", "varnish"},
		PromulgatedURL:          url.DocPromulgatedURL(),
		SupportedSeries:         ch.Meta().Series,
		Development:             url.Development,
	}))

	// The charm archive has been properly added to the blob store.
	r, obtainedSize, err := store.BlobStore.Open(blobName)
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

func (s *AddEntitySuite) checkAddBundle(c *gc.C, bundle charm.Bundle, addToES bool, url *router.ResolvedURL) {
	var es *elasticsearch.Database

	if addToES {
		es = s.ES
	}
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

	// Ensure the document was indexed in ElasticSearch, if an ES database was provided.
	if es != nil {
		var result SearchDoc
		id := store.ES.getID(doc.URL)
		err = store.ES.GetDocument(s.TestIndex, typeName, id, &result)
		c.Assert(err, gc.IsNil)
		exists, err := store.ES.HasDocument(s.TestIndex, typeName, id)
		c.Assert(err, gc.IsNil)
		c.Assert(exists, gc.Equals, true)
		if purl := url.PromulgatedURL(); purl != nil {
			c.Assert(result.PromulgatedURL, jc.DeepEquals, purl)
		}
	}

	// Check the upload time and then reset it to its zero value
	// so that we can test the deterministic parts later.
	c.Assert(doc.UploadTime, jc.TimeBetween(beforeAdding, afterAdding))
	doc.UploadTime = time.Time{}

	// The blob name is random, but we check that it's
	// in the correct format, and non-empty.
	blobName := doc.BlobName
	c.Assert(blobName, gc.Matches, "[0-9a-z]+")
	doc.BlobName = ""

	// The entity doc has been correctly added to the mongo collection.
	size, hash, hash256 := getSizeAndHashes(bundle)
	c.Assert(doc, jc.DeepEquals, denormalizedEntity(&mongodoc.Entity{
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
		PromulgatedURL:     url.DocPromulgatedURL(),
		Development:        url.Development,
	}))

	// The bundle archive has been properly added to the blob store.
	r, obtainedSize, err := store.BlobStore.Open(blobName)
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
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrDuplicateUpload)
}

func assertBaseEntity(c *gc.C, store *Store, url *charm.URL, promulgated bool) {
	baseEntity, err := store.FindBaseEntity(url, nil)
	c.Assert(err, gc.IsNil)
	expectACLs := mongodoc.ACL{
		Read:  []string{url.User},
		Write: []string{url.User},
	}
	c.Assert(baseEntity, jc.DeepEquals, &mongodoc.BaseEntity{
		URL:             url,
		User:            url.User,
		Name:            url.Name,
		Public:          false,
		ACLs:            expectACLs,
		DevelopmentACLs: expectACLs,
		Promulgated:     mongodoc.IntBool(promulgated),
	})
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
