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
		BaseURL:                 (*params.CharmURL)(mustParseURL("cs:wordpress")),
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
		BaseURL:      (*params.CharmURL)(mustParseURL("cs:wordpress-simple")),
		BundleData:   bundle.Data(),
		BundleReadMe: bundle.ReadMe(),
		BundleCharms: []*params.CharmURL{
			(*params.CharmURL)(mustParseURL("wordpress")),
			(*params.CharmURL)(mustParseURL("mysql")),
		},
	})

	// Try inserting the bundle again - it should fail because the bundle is
	// already there.
	err = store.AddBundle(url, bundle)
	c.Assert(err, jc.Satisfies, mgo.IsDup)
}

var expandURLTests = []struct {
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
}}

func (s *StoreSuite) TestExpandURL(c *gc.C) {
	wordpress := testing.Charms.CharmDir("wordpress")
	for i, test := range expandURLTests {
		c.Logf("test %d: %q from %q", i, test.expand, test.inStore)
		store := NewStore(s.Session.DB("foo"))
		_, err := store.DB.Entities().RemoveAll(nil)
		c.Assert(err, gc.IsNil)
		urls := mustParseURLs(test.inStore)
		for _, url := range urls {
			err := store.AddCharm(url, wordpress)
			c.Assert(err, gc.IsNil)
		}
		gotURLs, err := store.ExpandURL(mustParseURL(test.expand))
		c.Assert(err, gc.IsNil)

		gotURLStrs := urlStrings(gotURLs)
		sort.Strings(gotURLStrs)
		c.Assert(gotURLStrs, jc.DeepEquals, test.expect)
	}
}

func urlStrings(urls []*charm.URL) []string {
	urlStrs := make([]string, len(urls))
	for i, url := range urls {
		urlStrs[i] = url.String()
	}
	return urlStrs
}

func mustParseURLs(urlStrs []string) []*charm.URL {
	urls := make([]*charm.URL, len(urlStrs))
	for i, u := range urlStrs {
		var err error
		urls[i], err = charm.ParseURL(u)
		if err != nil {
			panic(err)
		}
	}
	return urls
}

// mustParseURL is like charm.MustParseURL except
// that it allows an unspecified series.
func mustParseURL(s string) *charm.URL {
	ref, series, err := charm.ParseReference(s)
	if err != nil {
		panic(err)
	}
	return &charm.URL{
		Reference: ref,
		Series:    series,
	}
}
