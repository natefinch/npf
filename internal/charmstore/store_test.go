// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore

import (
	"archive/zip"
	"bytes"
	"crypto/sha512"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"sort"
	"time"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v4"

	"github.com/juju/charmstore/internal/blobstore"
	"github.com/juju/charmstore/internal/elasticsearch"
	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/storetesting"
	"github.com/juju/charmstore/params"
)

type StoreSuite struct {
	storetesting.IsolatedMgoESSuite
}

var _ = gc.Suite(&StoreSuite{})

func (s *StoreSuite) checkAddCharm(c *gc.C, ch charm.Charm, addToES bool) {
	var ses *StoreElasticSearch
	if addToES {
		ses = &StoreElasticSearch{
			s.ES.Index(s.TestIndex),
		}
	}
	store, err := NewStore(s.Session.DB("juju_test"), ses)
	c.Assert(err, gc.IsNil)
	url := charm.MustParseReference("cs:precise/wordpress-23")

	// Add the charm to the store.
	beforeAdding := time.Now()
	err = store.AddCharmWithArchive(url, ch)
	c.Assert(err, gc.IsNil)
	afterAdding := time.Now()

	var doc mongodoc.Entity
	err = store.DB.Entities().FindId("cs:precise/wordpress-23").One(&doc)
	c.Assert(err, gc.IsNil)

	// Ensure the document was indexed in ElasticSearch, if an ES database was provided.
	if ses != nil {
		var result mongodoc.Entity
		err = ses.GetDocument(typeName, ses.getID(&doc), &result)
		c.Assert(err, gc.IsNil)
		exists, err := ses.Database.EnsureID(ses.Index.Index, typeName, ses.getID(&doc))
		c.Assert(err, gc.IsNil)
		c.Assert(exists, gc.Equals, true)
	}
	// The entity doc has been correctly added to the mongo collection.
	size, hash := mustGetSizeAndHash(ch)
	sort.Strings(doc.CharmProvidedInterfaces)
	sort.Strings(doc.CharmRequiredInterfaces)

	// Check the upload time and then reset it to its zero value
	// so that we can test the deterministic parts later.
	c.Assert(doc.UploadTime, jc.TimeBetween(beforeAdding, afterAdding))

	doc.UploadTime = time.Time{}

	blobName := doc.BlobName
	c.Assert(blobName, gc.Matches, "[0-9a-z]+")
	doc.BlobName = ""
	c.Assert(doc, jc.DeepEquals, mongodoc.Entity{
		URL:                     url,
		BaseURL:                 charm.MustParseReference("cs:wordpress"),
		BlobHash:                hash,
		Size:                    size,
		CharmMeta:               ch.Meta(),
		CharmActions:            ch.Actions(),
		CharmConfig:             ch.Config(),
		CharmProvidedInterfaces: []string{"http", "logging", "monitoring"},
		CharmRequiredInterfaces: []string{"mysql", "varnish"},
	})

	// The charm archive has been properly added to the blob store.
	r, obtainedSize, err := store.BlobStore.Open(blobName)
	c.Assert(err, gc.IsNil)
	c.Assert(obtainedSize, gc.Equals, size)
	data, err := ioutil.ReadAll(r)
	c.Assert(err, gc.IsNil)
	charmArchive, err := charm.ReadCharmArchiveBytes(data)
	c.Assert(err, gc.IsNil)
	c.Assert(charmArchive.Meta(), jc.DeepEquals, ch.Meta())
	c.Assert(charmArchive.Config(), jc.DeepEquals, ch.Config())
	c.Assert(charmArchive.Actions(), jc.DeepEquals, ch.Actions())
	c.Assert(charmArchive.Revision(), jc.DeepEquals, ch.Revision())

	// Try inserting the charm again - it should fail because the charm is
	// already there.
	err = store.AddCharmWithArchive(url, ch)
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrDuplicateUpload)
}

func (s *StoreSuite) checkAddBundle(c *gc.C, bundle charm.Bundle, addToES bool) {
	var ses *StoreElasticSearch

	if addToES {
		ses = &StoreElasticSearch{
			s.ES.Index(s.TestIndex),
		}
	}
	store, err := NewStore(s.Session.DB("juju_test"), ses)
	c.Assert(err, gc.IsNil)
	url := charm.MustParseReference("cs:bundle/wordpress-simple-42")

	// Add the bundle to the store.
	beforeAdding := time.Now()
	err = store.AddBundleWithArchive(url, bundle)
	c.Assert(err, gc.IsNil)
	afterAdding := time.Now()

	var doc mongodoc.Entity
	err = store.DB.Entities().FindId("cs:bundle/wordpress-simple-42").One(&doc)
	c.Assert(err, gc.IsNil)
	sort.Sort(orderedURLs(doc.BundleCharms))

	// Ensure the document was indexed in ElasticSearch, if an ES database was provided.
	if ses != nil {
		var result mongodoc.Entity
		err = ses.GetDocument(typeName, ses.getID(&doc), &result)
		c.Assert(err, gc.IsNil)
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
	size, hash := mustGetSizeAndHash(bundle)
	c.Assert(doc, jc.DeepEquals, mongodoc.Entity{
		URL:          url,
		BaseURL:      charm.MustParseReference("cs:wordpress-simple"),
		BlobHash:     hash,
		Size:         size,
		BundleData:   bundle.Data(),
		BundleReadMe: bundle.ReadMe(),
		BundleCharms: []*charm.Reference{
			charm.MustParseReference("mysql"),
			charm.MustParseReference("wordpress"),
		},
		BundleMachineCount: newInt(2),
		BundleUnitCount:    newInt(2),
	})

	// The bundle archive has been properly added to the blob store.
	r, obtainedSize, err := store.BlobStore.Open(blobName)
	c.Assert(err, gc.IsNil)
	c.Assert(obtainedSize, gc.Equals, size)
	data, err := ioutil.ReadAll(r)
	c.Assert(err, gc.IsNil)
	bundleArchive, err := charm.ReadBundleArchiveBytes(data)
	c.Assert(err, gc.IsNil)
	c.Assert(bundleArchive.Data(), jc.DeepEquals, bundle.Data())
	c.Assert(bundleArchive.ReadMe(), jc.DeepEquals, bundle.ReadMe())

	// Try inserting the bundle again - it should fail because the bundle is
	// already there.
	err = store.AddBundleWithArchive(url, bundle)
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrDuplicateUpload)
}

type orderedURLs []*charm.Reference

func (o orderedURLs) Less(i, j int) bool {
	return o[i].String() < o[j].String()
}

func (o orderedURLs) Swap(i, j int) {
	o[i], o[j] = o[j], o[i]
}

func (o orderedURLs) Len() int {
	return len(o)
}

var urlFindingTests = []struct {
	inStore []string
	expand  string
	expect  []string
}{{
	inStore: []string{"cs:precise/wordpress-23"},
	expand:  "wordpress",
	expect:  []string{"cs:precise/wordpress-23"},
}, {
	inStore: []string{"cs:precise/wordpress-23", "cs:precise/wordpress-24"},
	expand:  "wordpress",
	expect:  []string{"cs:precise/wordpress-23", "cs:precise/wordpress-24"},
}, {
	inStore: []string{"cs:precise/wordpress-23", "cs:trusty/wordpress-24"},
	expand:  "precise/wordpress",
	expect:  []string{"cs:precise/wordpress-23"},
}, {
	inStore: []string{"cs:precise/wordpress-23", "cs:trusty/wordpress-24", "cs:foo/bar-434"},
	expand:  "wordpress",
	expect:  []string{"cs:precise/wordpress-23", "cs:trusty/wordpress-24"},
}, {
	inStore: []string{"cs:precise/wordpress-23", "cs:trusty/wordpress-23", "cs:trusty/wordpress-24"},
	expand:  "wordpress-23",
	expect:  []string{"cs:precise/wordpress-23", "cs:trusty/wordpress-23"},
}, {
	inStore: []string{"cs:~user/precise/wordpress-23", "cs:~user/trusty/wordpress-23"},
	expand:  "~user/precise/wordpress",
	expect:  []string{"cs:~user/precise/wordpress-23"},
}, {
	inStore: []string{"cs:~user/precise/wordpress-23", "cs:~user/trusty/wordpress-23"},
	expand:  "~user/wordpress",
	expect:  []string{"cs:~user/precise/wordpress-23", "cs:~user/trusty/wordpress-23"},
}, {
	inStore: []string{"cs:precise/wordpress-23", "cs:trusty/wordpress-24", "cs:foo/bar-434"},
	expand:  "precise/wordpress-23",
	expect:  []string{"cs:precise/wordpress-23"},
}, {
	inStore: []string{"cs:precise/wordpress-23", "cs:trusty/wordpress-24", "cs:foo/bar-434"},
	expand:  "arble",
	expect:  []string{},
}, {
	inStore: []string{},
	expand:  "precise/wordpress-23",
	expect:  []string{},
}}

func (s *StoreSuite) TestExpandURL(c *gc.C) {
	s.testURLFinding(c, func(store *Store, expand *charm.Reference, expect []*charm.Reference) {
		gotURLs, err := store.ExpandURL(expand)
		c.Assert(err, gc.IsNil)

		sort.Sort(orderedURLs(gotURLs))
		c.Assert(gotURLs, jc.DeepEquals, expect)
	})
}

func (s *StoreSuite) testURLFinding(c *gc.C, check func(store *Store, expand *charm.Reference, expect []*charm.Reference)) {
	wordpress := storetesting.Charms.CharmDir("wordpress")
	for i, test := range urlFindingTests {
		c.Logf("test %d: %q from %q", i, test.expand, test.inStore)
		store, err := NewStore(s.Session.DB("foo"), nil)
		c.Assert(err, gc.IsNil)
		_, err = store.DB.Entities().RemoveAll(nil)
		c.Assert(err, gc.IsNil)
		urls := mustParseReferences(test.inStore)
		for _, url := range urls {
			err := store.AddCharmWithArchive(url, wordpress)
			c.Assert(err, gc.IsNil)
		}
		expectURLs := make([]*charm.Reference, len(test.expect))
		for i, expect := range test.expect {
			expectURLs[i] = charm.MustParseReference(expect)
		}
		check(store, charm.MustParseReference(test.expand), expectURLs)
	}
}

func (s *StoreSuite) TestFindEntities(c *gc.C) {
	s.testURLFinding(c, func(store *Store, expand *charm.Reference, expect []*charm.Reference) {
		// check FindEntities works when just retrieving the id.
		gotEntities, err := store.FindEntities(expand, "_id")
		c.Assert(err, gc.IsNil)
		sort.Sort(entitiesByURL(gotEntities))
		c.Assert(gotEntities, gc.HasLen, len(expect))
		for i, url := range expect {
			c.Assert(gotEntities[i], jc.DeepEquals, &mongodoc.Entity{
				URL: url,
			})
		}

		// check FindEntities works when retrieving all fields.
		gotEntities, err = store.FindEntities(expand)
		c.Assert(err, gc.IsNil)
		sort.Sort(entitiesByURL(gotEntities))
		c.Assert(gotEntities, gc.HasLen, len(expect))
		for i, url := range expect {
			var entity mongodoc.Entity
			err := store.DB.Entities().FindId(url).One(&entity)
			c.Assert(err, gc.IsNil)
			c.Assert(gotEntities[i], jc.DeepEquals, &entity)
		}
	})
}

func (s *StoreSuite) TestFindEntity(c *gc.C) {
	s.testURLFinding(c, func(store *Store, expand *charm.Reference, expect []*charm.Reference) {
		entity, err := store.FindEntity(expand, "_id")
		if expand.Series == "" || expand.Revision == -1 {
			c.Assert(err, gc.ErrorMatches, `entity id ".*" is not fully qualified`)
			return
		}
		if len(expect) == 0 {
			c.Assert(err, gc.ErrorMatches, "entity not found")
			c.Assert(errgo.Cause(err), gc.Equals, params.ErrNotFound)
			return
		}
		c.Assert(err, gc.IsNil)
		c.Assert(len(expect), gc.Equals, 1)
		c.Assert(entity.BlobName, gc.Equals, "")
		c.Assert(entity.URL, gc.DeepEquals, expect[0])

		// Check that it works when returning other fields too.
		entity, err = store.FindEntity(expand, "blobname")
		c.Assert(err, gc.IsNil)
		c.Assert(entity.BlobName, gc.Not(gc.Equals), "")
	})
}

func (s *StoreSuite) TestAddCharmWithFailedESInsert(c *gc.C) {
	// Make an elastic search with a non-existent address,
	// so that will try to add the charm there, but fail.
	esdb := &elasticsearch.Database{
		Addr: "0.1.2.3:0123",
	}
	es := &StoreElasticSearch{
		Index: esdb.Index("an-index"),
	}

	store, err := NewStore(s.Session.DB("juju_test"), es)
	c.Assert(err, gc.IsNil)

	url := charm.MustParseReference("precise/wordpress-12")
	err = store.AddCharmWithArchive(url, storetesting.Charms.CharmDir("wordpress"))
	c.Assert(err, gc.ErrorMatches, "cannot index cs:precise/wordpress-12 to ElasticSearch: .*")

	// Check that the entity has been correctly removed.
	_, err = store.FindEntity(url)
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrNotFound)
}

type entitiesByURL []*mongodoc.Entity

func (s entitiesByURL) Len() int      { return len(s) }
func (s entitiesByURL) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s entitiesByURL) Less(i, j int) bool {
	return s[i].URL.String() < s[j].URL.String()
}

var bundleUnitCountTests = []struct {
	about       string
	data        *charm.BundleData
	expectUnits int
}{{
	about: "empty bundle",
	data:  &charm.BundleData{},
}, {
	about: "no units",
	data: &charm.BundleData{
		Services: map[string]*charm.ServiceSpec{
			"django": {
				Charm:    "cs:utopic/django-0",
				NumUnits: 0,
			},
			"haproxy": {
				Charm:    "cs:trusty/haproxy-0",
				NumUnits: 0,
			},
		},
	},
}, {
	about: "a single unit",
	data: &charm.BundleData{
		Services: map[string]*charm.ServiceSpec{
			"django": {
				Charm:    "cs:trusty/django-42",
				NumUnits: 1,
			},
			"haproxy": {
				Charm:    "cs:trusty/haproxy-47",
				NumUnits: 0,
			},
		},
	},
	expectUnits: 1,
}, {
	about: "multiple units",
	data: &charm.BundleData{
		Services: map[string]*charm.ServiceSpec{
			"django": {
				Charm:    "cs:utopic/django-1",
				NumUnits: 1,
			},
			"haproxy": {
				Charm:    "cs:utopic/haproxy-2",
				NumUnits: 2,
			},
			"postgres": {
				Charm:    "cs:utopic/postgres-3",
				NumUnits: 5,
			},
		},
	},
	expectUnits: 8,
}}

func (s *StoreSuite) TestBundleUnitCount(c *gc.C) {
	store, err := NewStore(s.Session.DB("foo"), nil)
	c.Assert(err, gc.IsNil)
	entities := store.DB.Entities()
	for i, test := range bundleUnitCountTests {
		c.Logf("test %d: %s", i, test.about)
		url := &charm.Reference{
			Schema:   "cs",
			Series:   "bundle",
			Name:     "django",
			Revision: i,
		}

		// Add the bundle used for this test.
		err := store.AddBundle(&testingBundle{
			data: test.data,
		}, AddParams{
			URL:      url,
			BlobName: "blobName",
			BlobHash: fakeBlobHash,
			BlobSize: fakeBlobSize,
		})
		c.Assert(err, gc.IsNil)

		// Retrieve the bundle from the database.
		var doc mongodoc.Entity
		err = entities.FindId(url).One(&doc)
		c.Assert(err, gc.IsNil)

		c.Assert(*doc.BundleUnitCount, gc.Equals, test.expectUnits)
	}
}

var bundleMachineCountTests = []struct {
	about          string
	data           *charm.BundleData
	expectMachines int
}{{
	about: "no machines",
	data: &charm.BundleData{
		Services: map[string]*charm.ServiceSpec{
			"django": {
				Charm:    "cs:utopic/django-0",
				NumUnits: 0,
			},
			"haproxy": {
				Charm:    "cs:trusty/haproxy-0",
				NumUnits: 0,
			},
		},
	},
}, {
	about: "a single machine (no placement)",
	data: &charm.BundleData{
		Services: map[string]*charm.ServiceSpec{
			"django": {
				Charm:    "cs:trusty/django-42",
				NumUnits: 1,
			},
			"haproxy": {
				Charm:    "cs:trusty/haproxy-47",
				NumUnits: 0,
			},
		},
	},
	expectMachines: 1,
}, {
	about: "a single machine (machine placement)",
	data: &charm.BundleData{
		Services: map[string]*charm.ServiceSpec{
			"django": {
				Charm:    "cs:trusty/django-42",
				NumUnits: 1,
				To:       []string{"1"},
			},
		},
		Machines: map[string]*charm.MachineSpec{
			"1": nil,
		},
	},
	expectMachines: 1,
}, {
	about: "a single machine (hulk smash)",
	data: &charm.BundleData{
		Services: map[string]*charm.ServiceSpec{
			"django": {
				Charm:    "cs:trusty/django-42",
				NumUnits: 1,
				To:       []string{"1"},
			},
			"haproxy": {
				Charm:    "cs:trusty/haproxy-47",
				NumUnits: 1,
				To:       []string{"1"},
			},
		},
		Machines: map[string]*charm.MachineSpec{
			"1": nil,
		},
	},
	expectMachines: 1,
}, {
	about: "a single machine (co-location)",
	data: &charm.BundleData{
		Services: map[string]*charm.ServiceSpec{
			"django": {
				Charm:    "cs:trusty/django-42",
				NumUnits: 1,
			},
			"haproxy": {
				Charm:    "cs:trusty/haproxy-47",
				NumUnits: 1,
				To:       []string{"django/0"},
			},
		},
	},
	expectMachines: 1,
}, {
	about: "a single machine (containerization)",
	data: &charm.BundleData{
		Services: map[string]*charm.ServiceSpec{
			"django": {
				Charm:    "cs:trusty/django-42",
				NumUnits: 1,
				To:       []string{"1"},
			},
			"haproxy": {
				Charm:    "cs:trusty/haproxy-47",
				NumUnits: 1,
				To:       []string{"lxc:1"},
			},
			"postgres": {
				Charm:    "cs:utopic/postgres-3",
				NumUnits: 2,
				To:       []string{"kvm:1"},
			},
		},
		Machines: map[string]*charm.MachineSpec{
			"1": nil,
		},
	},
	expectMachines: 1,
}, {
	about: "multiple machines (no placement)",
	data: &charm.BundleData{
		Services: map[string]*charm.ServiceSpec{
			"django": {
				Charm:    "cs:utopic/django-1",
				NumUnits: 1,
			},
			"haproxy": {
				Charm:    "cs:utopic/haproxy-2",
				NumUnits: 2,
			},
			"postgres": {
				Charm:    "cs:utopic/postgres-3",
				NumUnits: 5,
			},
		},
	},
	expectMachines: 1 + 2 + 5,
}, {
	about: "multiple machines (machine placement)",
	data: &charm.BundleData{
		Services: map[string]*charm.ServiceSpec{
			"django": {
				Charm:    "cs:utopic/django-1",
				NumUnits: 2,
				To:       []string{"1", "3"},
			},
			"haproxy": {
				Charm:    "cs:utopic/haproxy-2",
				NumUnits: 1,
				To:       []string{"2"},
			},
		},
		Machines: map[string]*charm.MachineSpec{
			"1": nil, "2": nil, "3": nil,
		},
	},
	expectMachines: 2 + 1,
}, {
	about: "multiple machines (hulk smash)",
	data: &charm.BundleData{
		Services: map[string]*charm.ServiceSpec{
			"django": {
				Charm:    "cs:trusty/django-42",
				NumUnits: 1,
				To:       []string{"1"},
			},
			"haproxy": {
				Charm:    "cs:trusty/haproxy-47",
				NumUnits: 1,
				To:       []string{"2"},
			},
			"postgres": {
				Charm:    "cs:utopic/postgres-3",
				NumUnits: 2,
				To:       []string{"1", "2"},
			},
		},
		Machines: map[string]*charm.MachineSpec{
			"1": nil, "2": nil,
		},
	},
	expectMachines: 1 + 1 + 0,
}, {
	about: "multiple machines (co-location)",
	data: &charm.BundleData{
		Services: map[string]*charm.ServiceSpec{
			"django": {
				Charm:    "cs:trusty/django-42",
				NumUnits: 2,
			},
			"haproxy": {
				Charm:    "cs:trusty/haproxy-47",
				NumUnits: 3,
				To:       []string{"django/0", "django/1", "new"},
			},
		},
	},
	expectMachines: 2 + 1,
}, {
	about: "multiple machines (containerization)",
	data: &charm.BundleData{
		Services: map[string]*charm.ServiceSpec{
			"django": {
				Charm:    "cs:trusty/django-42",
				NumUnits: 2,
				To:       []string{"1", "2"},
			},
			"haproxy": {
				Charm:    "cs:trusty/haproxy-47",
				NumUnits: 4,
				To:       []string{"lxc:1", "lxc:2", "lxc:3", "lxc:3"},
			},
			"postgres": {
				Charm:    "cs:utopic/postgres-3",
				NumUnits: 1,
				To:       []string{"kvm:2"},
			},
		},
		Machines: map[string]*charm.MachineSpec{
			"1": nil, "2": nil, "3": nil,
		},
	},
	expectMachines: 2 + 1 + 0,
}, {
	about: "multiple machines (partial placement in a container)",
	data: &charm.BundleData{
		Services: map[string]*charm.ServiceSpec{
			"django": {
				Charm:    "cs:trusty/django-42",
				NumUnits: 1,
				To:       []string{"1"},
			},
			"haproxy": {
				Charm:    "cs:trusty/haproxy-47",
				NumUnits: 10,
				To:       []string{"lxc:1", "lxc:2"},
			},
		},
		Machines: map[string]*charm.MachineSpec{
			"1": nil, "2": nil,
		},
	},
	expectMachines: 1 + 1,
}, {
	about: "multiple machines (partial placement in a new machine)",
	data: &charm.BundleData{
		Services: map[string]*charm.ServiceSpec{
			"django": {
				Charm:    "cs:trusty/django-42",
				NumUnits: 1,
				To:       []string{"1"},
			},
			"haproxy": {
				Charm:    "cs:trusty/haproxy-47",
				NumUnits: 10,
				To:       []string{"lxc:1", "1", "new"},
			},
		},
		Machines: map[string]*charm.MachineSpec{
			"1": nil,
		},
	},
	expectMachines: 1 + 8,
}, {
	about: "multiple machines (partial placement with new machines)",
	data: &charm.BundleData{
		Services: map[string]*charm.ServiceSpec{
			"django": {
				Charm:    "cs:trusty/django-42",
				NumUnits: 3,
			},
			"haproxy": {
				Charm:    "cs:trusty/haproxy-47",
				NumUnits: 6,
				To:       []string{"new", "1", "lxc:1", "new"},
			},
			"postgres": {
				Charm:    "cs:utopic/postgres-3",
				NumUnits: 10,
				To:       []string{"kvm:2", "lxc:django/1", "new", "new", "kvm:2"},
			},
		},
		Machines: map[string]*charm.MachineSpec{
			"1": nil, "2": nil,
		},
	},
	expectMachines: 3 + 5 + 3,
}, {
	about: "placement into container on new machine",
	data: &charm.BundleData{
		Services: map[string]*charm.ServiceSpec{
			"haproxy": {
				Charm:    "cs:trusty/haproxy-47",
				NumUnits: 6,
				To:       []string{"lxc:new", "1", "lxc:1", "kvm:new"},
			},
		},
		Machines: map[string]*charm.MachineSpec{
			"1": nil,
		},
	},
	expectMachines: 5,
}}

func (s *StoreSuite) TestBundleMachineCount(c *gc.C) {
	store, err := NewStore(s.Session.DB("foo"), nil)
	c.Assert(err, gc.IsNil)
	entities := store.DB.Entities()
	for i, test := range bundleMachineCountTests {
		c.Logf("test %d: %s", i, test.about)
		url := &charm.Reference{
			Schema:   "cs",
			Series:   "bundle",
			Name:     "django",
			Revision: i,
		}
		err := test.data.Verify(func(string) error { return nil })
		c.Assert(err, gc.IsNil)
		// Add the bundle used for this test.
		err = store.AddBundle(&testingBundle{
			data: test.data,
		}, AddParams{
			URL:      url,
			BlobName: "blobName",
			BlobHash: fakeBlobHash,
			BlobSize: fakeBlobSize,
		})
		c.Assert(err, gc.IsNil)

		// Retrieve the bundle from the database.
		var doc mongodoc.Entity
		err = entities.FindId(url).One(&doc)
		c.Assert(err, gc.IsNil)

		c.Assert(*doc.BundleMachineCount, gc.Equals, test.expectMachines)
	}
}

func urlStrings(urls []*charm.Reference) []string {
	urlStrs := make([]string, len(urls))
	for i, url := range urls {
		urlStrs[i] = url.String()
	}
	return urlStrs
}

func mustParseReferences(urlStrs []string) []*charm.Reference {
	urls := make([]*charm.Reference, len(urlStrs))
	for i, u := range urlStrs {
		urls[i] = charm.MustParseReference(u)
	}
	return urls
}

func (s *StoreSuite) TestAddCharmDir(c *gc.C) {
	charmDir := storetesting.Charms.CharmDir("wordpress")
	s.checkAddCharm(c, charmDir, false)
}

func (s *StoreSuite) TestAddCharmArchive(c *gc.C) {
	charmArchive := storetesting.Charms.CharmArchive(c.MkDir(), "wordpress")
	s.checkAddCharm(c, charmArchive, false)
}

func (s *StoreSuite) TestAddBundleDir(c *gc.C) {
	bundleDir := storetesting.Charms.BundleDir("wordpress-simple")
	s.checkAddBundle(c, bundleDir, false)
}

func (s *StoreSuite) TestAddBundleArchive(c *gc.C) {
	bundleArchive, err := charm.ReadBundleArchive(
		storetesting.Charms.BundleArchivePath(c.MkDir(), "wordpress-simple"),
	)
	c.Assert(err, gc.IsNil)
	s.checkAddBundle(c, bundleArchive, false)
}

func (s *StoreSuite) TestAddCharmWithBundleSeries(c *gc.C) {
	store, err := NewStore(s.Session.DB("foo"), nil)
	c.Assert(err, gc.IsNil)
	ch := storetesting.Charms.CharmArchive(c.MkDir(), "wordpress")
	err = store.AddCharm(ch, AddParams{
		URL: charm.MustParseReference("bundle/wordpress-2"),
	})
	c.Assert(err, gc.ErrorMatches, `charm added with invalid id cs:bundle/wordpress-2`)
}

func (s *StoreSuite) TestAddBundleWithCharmSeries(c *gc.C) {
	store, err := NewStore(s.Session.DB("foo"), nil)
	c.Assert(err, gc.IsNil)
	b := storetesting.Charms.BundleDir("wordpress-simple")
	err = store.AddBundle(b, AddParams{
		URL: charm.MustParseReference("precise/wordpress-simple-2"),
	})
	c.Assert(err, gc.ErrorMatches, `bundle added with invalid id cs:precise/wordpress-simple-2`)
}

func (s *StoreSuite) TestAddBundleDuplicatingCharm(c *gc.C) {
	store, err := NewStore(s.Session.DB("foo"), nil)
	c.Assert(err, gc.IsNil)
	ch := storetesting.Charms.CharmDir("wordpress")
	err = store.AddCharmWithArchive(charm.MustParseReference("precise/wordpress-2"), ch)
	c.Assert(err, gc.IsNil)

	b := storetesting.Charms.BundleDir("wordpress-simple")
	err = store.AddBundleWithArchive(charm.MustParseReference("bundle/wordpress-5"), b)
	c.Assert(err, gc.ErrorMatches, "bundle name duplicates charm name cs:precise/wordpress-2")
}

func (s *StoreSuite) TestAddCharmDuplicatingBundle(c *gc.C) {
	store, err := NewStore(s.Session.DB("foo"), nil)
	c.Assert(err, gc.IsNil)

	b := storetesting.Charms.BundleDir("wordpress-simple")
	err = store.AddBundleWithArchive(charm.MustParseReference("bundle/wordpress-2"), b)
	c.Assert(err, gc.IsNil)

	ch := storetesting.Charms.CharmDir("wordpress")
	err = store.AddCharmWithArchive(charm.MustParseReference("precise/wordpress-5"), ch)
	c.Assert(err, gc.ErrorMatches, "charm name duplicates bundle name cs:bundle/wordpress-2")
}

func (s *StoreSuite) TestOpenBlob(c *gc.C) {
	charmArchive := storetesting.Charms.CharmArchive(c.MkDir(), "wordpress")

	store, err := NewStore(s.Session.DB("foo"), nil)
	c.Assert(err, gc.IsNil)
	url := charm.MustParseReference("cs:precise/wordpress-23")

	err = store.AddCharmWithArchive(url, charmArchive)
	c.Assert(err, gc.IsNil)

	f, err := os.Open(charmArchive.Path)
	c.Assert(err, gc.IsNil)
	defer f.Close()
	expectHash := hashOfReader(c, f)

	r, size, hash, err := store.OpenBlob(url)
	c.Assert(err, gc.IsNil)
	defer r.Close()

	c.Assert(hashOfReader(c, r), gc.Equals, expectHash)
	c.Assert(hash, gc.Equals, expectHash)

	info, err := f.Stat()
	c.Assert(err, gc.IsNil)
	c.Assert(size, gc.Equals, info.Size())
}

func (s *StoreSuite) TestBlobNameAndHash(c *gc.C) {
	charmArchive := storetesting.Charms.CharmArchive(c.MkDir(), "wordpress")

	store, err := NewStore(s.Session.DB("foo"), nil)
	c.Assert(err, gc.IsNil)
	url := charm.MustParseReference("cs:precise/wordpress-23")

	err = store.AddCharmWithArchive(url, charmArchive)
	c.Assert(err, gc.IsNil)

	f, err := os.Open(charmArchive.Path)
	c.Assert(err, gc.IsNil)
	defer f.Close()
	expectHash := hashOfReader(c, f)

	name, hash, err := store.BlobNameAndHash(url)
	c.Assert(err, gc.IsNil)

	r, _, err := store.BlobStore.Open(name)
	c.Assert(err, gc.IsNil)
	defer r.Close()

	c.Assert(hash, gc.Equals, expectHash)
	c.Assert(hashOfReader(c, r), gc.Equals, expectHash)
}

func (s *StoreSuite) TestCollections(c *gc.C) {
	store, err := NewStore(s.Session.DB("foo"), nil)
	c.Assert(err, gc.IsNil)
	colls := store.DB.Collections()
	names, err := store.DB.CollectionNames()
	c.Assert(err, gc.IsNil)
	// Check that all collections mentioned by Collections are actually created.
	for _, coll := range colls {
		found := false
		for _, name := range names {
			if name == coll.Name {
				found = true
			}
		}
		if !found {
			c.Errorf("collection %q not created", coll.Name)
		}

	}
	// Check that all created collections are mentioned in Collections.
	for _, name := range names {
		if name == "system.indexes" || name == "managedStoredResources" {
			continue
		}
		found := false
		for _, coll := range colls {
			if coll.Name == name {
				found = true
			}
		}
		if !found {
			c.Errorf("extra collection %q found", name)
		}
	}
}

func (s *StoreSuite) TestOpenCachedBlobFileWithInvalidEntity(c *gc.C) {
	store, err := NewStore(s.Session.DB("foo"), nil)
	c.Assert(err, gc.IsNil)

	wordpress := storetesting.Charms.CharmDir("wordpress")
	url := charm.MustParseReference("cs:precise/wordpress-23")
	err = store.AddCharmWithArchive(url, wordpress)
	c.Assert(err, gc.IsNil)

	entity, err := store.FindEntity(url, "charmmeta")
	c.Assert(err, gc.IsNil)
	r, err := store.OpenCachedBlobFile(entity, "", nil)
	c.Assert(err, gc.ErrorMatches, "provided entity does not have required fields")
	c.Assert(r, gc.Equals, nil)
}

func (s *StoreSuite) TestOpenCachedBlobFileWithFoundContent(c *gc.C) {
	store, err := NewStore(s.Session.DB("foo"), nil)
	c.Assert(err, gc.IsNil)

	wordpress := storetesting.Charms.CharmDir("wordpress")
	url := charm.MustParseReference("cs:precise/wordpress-23")
	err = store.AddCharmWithArchive(url, wordpress)
	c.Assert(err, gc.IsNil)

	// Get our expected content.
	data, err := ioutil.ReadFile(filepath.Join(wordpress.Path, "metadata.yaml"))
	c.Assert(err, gc.IsNil)
	expectContent := string(data)

	entity, err := store.FindEntity(url, "blobname", "contents")
	c.Assert(err, gc.IsNil)

	// Check that, when we open the file for the first time,
	// we see the expected content.
	r, err := store.OpenCachedBlobFile(entity, mongodoc.FileIcon, func(f *zip.File) bool {
		return path.Clean(f.Name) == "metadata.yaml"
	})
	c.Assert(err, gc.IsNil)
	data, err = ioutil.ReadAll(r)
	c.Assert(err, gc.IsNil)
	c.Assert(string(data), gc.Equals, expectContent)

	// When retrieving the entity again, check that the Contents
	// map has been set appropriately...
	entity, err = store.FindEntity(url, "blobname", "contents")
	c.Assert(err, gc.IsNil)
	c.Assert(entity.Contents, gc.HasLen, 1)
	c.Assert(entity.Contents[mongodoc.FileIcon].IsValid(), gc.Equals, true)

	// ... and that OpenCachedBlobFile still returns a reader with the
	// same data, without making use of the isFile callback.
	r, err = store.OpenCachedBlobFile(entity, mongodoc.FileIcon, func(f *zip.File) bool {
		c.Errorf("isFile called unexpectedly")
		return false
	})
	data, err = ioutil.ReadAll(r)
	c.Assert(err, gc.IsNil)
	c.Assert(string(data), gc.Equals, expectContent)
}

func (s *StoreSuite) TestOpenCachedBlobFileWithNotFoundContent(c *gc.C) {
	store, err := NewStore(s.Session.DB("foo"), nil)
	c.Assert(err, gc.IsNil)

	wordpress := storetesting.Charms.CharmDir("wordpress")
	url := charm.MustParseReference("cs:precise/wordpress-23")
	err = store.AddCharmWithArchive(url, wordpress)
	c.Assert(err, gc.IsNil)

	entity, err := store.FindEntity(url, "blobname", "contents")
	c.Assert(err, gc.IsNil)

	// Check that, when we open the file for the first time,
	// we get a NotFound error.
	r, err := store.OpenCachedBlobFile(entity, mongodoc.FileIcon, func(f *zip.File) bool {
		return false
	})
	c.Assert(err, gc.ErrorMatches, "not found")
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrNotFound)
	c.Assert(r, gc.Equals, nil)

	// When retrieving the entity again, check that the Contents
	// map has been set appropriately...
	entity, err = store.FindEntity(url, "blobname", "contents")
	c.Assert(err, gc.IsNil)
	c.Assert(entity.Contents, gc.DeepEquals, map[mongodoc.FileId]mongodoc.ZipFile{
		mongodoc.FileIcon: {},
	})

	// ... and that OpenCachedBlobFile still returns a NotFound
	// error, without making use of the isFile callback.
	r, err = store.OpenCachedBlobFile(entity, mongodoc.FileIcon, func(f *zip.File) bool {
		c.Errorf("isFile called unexpectedly")
		return false
	})
	c.Assert(err, gc.ErrorMatches, "not found")
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrNotFound)
	c.Assert(r, gc.Equals, nil)
}

func hashOfReader(c *gc.C, r io.Reader) string {
	hash := sha512.New384()
	_, err := io.Copy(hash, r)
	c.Assert(err, gc.IsNil)
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func mustGetSizeAndHash(c interface{}) (int64, string) {
	var r io.ReadWriter
	var err error
	switch c := c.(type) {
	case archiverTo:
		r = new(bytes.Buffer)
		err = c.ArchiveTo(r)
	case *charm.BundleArchive:
		r, err = os.Open(c.Path)
	case *charm.CharmArchive:
		r, err = os.Open(c.Path)
	default:
		panic(fmt.Sprintf("unable to get size and hash for type %T", c))
	}
	if err != nil {
		panic(err)
	}
	hash := blobstore.NewHash()
	size, err := io.Copy(hash, r)
	if err != nil {
		panic(err)
	}
	return size, fmt.Sprintf("%x", hash.Sum(nil))
}

// testingBundle implements charm.Bundle, allowing tests
// to create a bundle with custom data.
type testingBundle struct {
	data *charm.BundleData
}

func (b *testingBundle) Data() *charm.BundleData {
	return b.data
}

func (b *testingBundle) ReadMe() string {
	// For the purposes of this implementation, the charm readme is not
	// relevant.
	return ""
}

// Define fake blob attributes to be used in tests.
var fakeBlobSize, fakeBlobHash = func() (int64, string) {
	b := []byte("fake content")
	h := blobstore.NewHash()
	h.Write(b)
	return int64(len(b)), fmt.Sprintf("%x", h.Sum(nil))
}()

func (s *StoreSuite) TestSESPutDoesNotErrorWithNoESConfigured(c *gc.C) {
	store, err := NewStore(s.Session.DB("mongodoctoelasticsearch"), nil)
	c.Assert(err, gc.IsNil)
	var entity mongodoc.Entity
	err = store.ES.put(&entity)
	c.Assert(err, gc.IsNil)
}

func (s *StoreSuite) TestAddCharmDirIndexed(c *gc.C) {
	charmDir := storetesting.Charms.CharmDir("wordpress")
	s.checkAddCharm(c, charmDir, true)
}

func (s *StoreSuite) TestAddCharmArchiveIndexed(c *gc.C) {
	charmArchive := storetesting.Charms.CharmArchive(c.MkDir(), "wordpress")
	s.checkAddCharm(c, charmArchive, true)
}

func (s *StoreSuite) TestAddBundleDirIndexed(c *gc.C) {
	bundleDir := storetesting.Charms.BundleDir("wordpress-simple")
	s.checkAddBundle(c, bundleDir, true)
}

func (s *StoreSuite) TestAddBundleArchiveIndexed(c *gc.C) {
	bundleArchive, err := charm.ReadBundleArchive(
		storetesting.Charms.BundleArchivePath(c.MkDir(), "wordpress-simple"),
	)
	c.Assert(err, gc.IsNil)
	s.checkAddBundle(c, bundleArchive, true)
}
