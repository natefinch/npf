// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore_test

import (
	"encoding/json"
	"fmt"

	"gopkg.in/juju/charm.v3"
	"gopkg.in/mgo.v2/bson"
	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore/internal/blobstore"
	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/storetesting"
)

// Define fake blob attributes to be used in tests.
var fakeBlobSize, fakeBlobHash = func() (int64, string) {
	b := []byte("fake content")
	h := blobstore.NewHash()
	h.Write(b)
	return int64(len(b)), fmt.Sprintf("%x", h.Sum(nil))
}()

type extraInfoSuite struct {
	storetesting.IsolatedMgoSuite
	store *charmstore.Store
}

var _ = gc.Suite(&extraInfoSuite{})

func (s *extraInfoSuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	store, err := charmstore.NewStore(s.Session.DB("foo"))
	c.Assert(err, gc.IsNil)
	s.store = store
}

var unitsCountTests = []struct {
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
				Charm:    "cs:utopic/haproxy-3",
				NumUnits: 5,
			},
		},
	},
	expectUnits: 8,
}}

func (s *extraInfoSuite) TestUnitsCount(c *gc.C) {
	entities := s.store.DB.Entities()
	for i, test := range unitsCountTests {
		c.Logf("test %d: %s", i, test.about)
		url := &charm.Reference{
			Schema:   "cs",
			Series:   "utopic",
			Name:     "django",
			Revision: i,
		}

		// Add the bundle used for this test.
		err := s.store.AddBundle(url, &extraInfoTestingBundle{
			data: test.data,
		}, "blobName", fakeBlobHash, fakeBlobSize)
		c.Assert(err, gc.IsNil)

		// Retrieve the bundle from the database.
		var doc mongodoc.Entity
		err = entities.FindId(url).Select(bson.D{{"extrainfo", 1}}).One(&doc)
		c.Assert(err, gc.IsNil)

		// Ensure the units count is correctly included in the extra info.
		c.Assert(doc.ExtraInfo, gc.HasLen, 1)
		var unitsCount int
		err = json.Unmarshal(doc.ExtraInfo["units-count"], &unitsCount)
		c.Assert(err, gc.IsNil)
		c.Assert(unitsCount, gc.Equals, test.expectUnits)
	}
}

// extraInfoTestingBundle implements charm.Bundle, and it is used for testing
// bundle initial extra info.
type extraInfoTestingBundle struct {
	data *charm.BundleData
}

func (b *extraInfoTestingBundle) Data() *charm.BundleData {
	return b.data
}

func (b *extraInfoTestingBundle) ReadMe() string {
	// For the purposes of this implementation, the charm readme is not
	// relevant.
	return ""
}
