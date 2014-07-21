// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore

import (
	"sort"

	jc "github.com/juju/testing/checkers"
	"gopkg.in/juju/charm.v2"
	"gopkg.in/juju/charm.v2/testing"
	"labix.org/v2/mgo"
	"labix.org/v2/mgo/bson"
	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/storetesting"
)

type StoreSuite struct {
	storetesting.IsolatedMgoSuite
}

var _ = gc.Suite(&StoreSuite{})

func (s *StoreSuite) TestAddCharm(c *gc.C) {
	store := newStore(s.Session.DB("foo"))
	url := charm.MustParseURL("cs:precise/wordpress-23")
	wordpress := testing.Charms.CharmDir("wordpress")
	err := store.AddCharm(url, wordpress)
	c.Assert(err, gc.IsNil)

	var doc mongodoc.Entity
	err = store.DB.Entities().Find(bson.D{{"_id", "cs:precise/wordpress-23"}}).One(&doc)
	c.Assert(err, gc.IsNil)
	sort.Strings(doc.CharmProvidedInterfaces)
	sort.Strings(doc.CharmRequiredInterfaces)
	c.Assert(doc, jc.DeepEquals, mongodoc.Entity{
		URL:                     url,
		BaseURL:                 mustParseReference("cs:wordpress"),
		CharmMeta:               wordpress.Meta(),
		CharmActions:            wordpress.Actions(),
		CharmConfig:             wordpress.Config(),
		CharmProvidedInterfaces: []string{"http", "logging", "monitoring"},
		CharmRequiredInterfaces: []string{"mysql", "varnish"},
	})

	// Try inserting the charm again - it should fail because the charm is already
	// there
	err = store.AddCharm(url, wordpress)
	c.Assert(err, jc.Satisfies, mgo.IsDup)
}

func mustParseReference(urlStr string) *charm.Reference {
	ref, _, err := charm.ParseReference(urlStr)
	if err != nil {
		panic(err)
	}
	return &ref
}
