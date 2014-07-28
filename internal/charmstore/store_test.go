// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore

import (
	"sort"

	jc "github.com/juju/testing/checkers"
	"gopkg.in/juju/charm.v2"
	"gopkg.in/juju/charm.v2/testing"
	"labix.org/v2/mgo"
	gc "launchpad.net/gocheck"

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
	sort.Strings(doc.CharmProvidedInterfaces)
	sort.Strings(doc.CharmRequiredInterfaces)
	c.Assert(doc, jc.DeepEquals, mongodoc.Entity{
		URL:                     (*params.CharmURL)(url),
		BaseURL:                 mustParseURL("cs:wordpress"),
		CharmMeta:               wordpress.Meta(),
		CharmActions:            wordpress.Actions(),
		CharmConfig:             wordpress.Config(),
		CharmProvidedInterfaces: []string{"http", "logging", "monitoring"},
		CharmRequiredInterfaces: []string{"mysql", "varnish"},
	})

	// Try inserting the charm again - it should fail because the charm is already
	// there.
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
	c.Assert(doc, jc.DeepEquals, mongodoc.Entity{
		URL:          (*params.CharmURL)(url),
		BaseURL:      mustParseURL("cs:wordpress-simple"),
		BundleData:   bundle.Data(),
		BundleReadMe: bundle.ReadMe(),
		BundleCharms: []*params.CharmURL{
			mustParseURL("wordpress"),
			mustParseURL("mysql"),
		},
	})

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
