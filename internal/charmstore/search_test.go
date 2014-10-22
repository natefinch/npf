// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore

import (
	"encoding/json"
	"sort"

	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v4"
	"gopkg.in/juju/charm.v4/testing"

	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/storetesting"
)

type StoreSearchSuite struct {
	storetesting.IsolatedMgoESSuite
	store *Store
}

var _ = gc.Suite(&StoreSearchSuite{})

func (s *StoreSearchSuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoESSuite.SetUpTest(c)
	store, err := NewStore(s.Session.DB("foo"), &StoreElasticSearch{
		s.ES.Index(s.TestIndex),
	})
	c.Assert(err, gc.IsNil)
	err = s.LoadESConfig(s.TestIndex)
	c.Assert(err, gc.IsNil)
	s.store = store
	s.addCharmsToStore(store)
}

var exportTestCharms = map[string]string{
	"wordpress": "cs:precise/wordpress-23",
	"mysql":     "cs:trusty/mysql-7",
	"varnish":   "cs:~foo/trusty/varnish-1",
}

var exportTestBundles = map[string]string{
	"wordpress": "cs:bundle/wordpress-4",
}

func (s *StoreSearchSuite) TestSuccessfulExport(c *gc.C) {
	err := s.store.ExportToElasticSearch()
	c.Assert(err, gc.IsNil)

	for _, ref := range exportTestCharms {
		var expected mongodoc.Entity
		var actual json.RawMessage
		err = s.store.DB.Entities().FindId(ref).One(&expected)
		c.Assert(err, gc.IsNil)
		err = s.store.ES.GetDocument(typeName, s.store.ES.getID(&expected), &actual)
		c.Assert(err, gc.IsNil)
		c.Assert([]byte(actual), storetesting.JSONEquals, expected)
	}
}

func (s *StoreSearchSuite) addCharmsToStore(store *Store) {
	for name, ref := range exportTestCharms {
		charmArchive := testing.Charms.CharmDir(name)
		url := charm.MustParseReference(ref)
		charmArchive.Meta().Categories = []string{name}
		store.AddCharmWithArchive(url, charmArchive)
	}
	for name, ref := range exportTestBundles {
		bundleArchive := testing.Charms.BundleDir(name)
		url := charm.MustParseReference(ref)
		bundleArchive.Data().Tags = []string{name}
		store.AddBundleWithArchive(url, bundleArchive)
	}
}

var searchTests = []struct {
	about   string
	sp      SearchParams
	results []string
}{
	{
		about: "basic text search",
		sp: SearchParams{
			Text: "wordpress",
		},
		results: []string{
			exportTestCharms["wordpress"],
		},
	}, {
		about: "blank text search",
		sp: SearchParams{
			Text: "",
		},
		results: []string{
			exportTestCharms["wordpress"],
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
			exportTestBundles["wordpress"],
		},
	}, {
		about: "autocomplete search",
		sp: SearchParams{
			Text:         "word",
			AutoComplete: true,
		},
		results: []string{
			exportTestCharms["wordpress"],
		},
	}, {
		about: "description filter search",
		sp: SearchParams{
			Text: "",
			Filters: map[string][]string{
				"description": {"blog"},
			},
		},
		results: []string{
			exportTestCharms["wordpress"],
		},
	}, {
		about: "name filter search",
		sp: SearchParams{
			Text: "",
			Filters: map[string][]string{
				"name": {"wordpress"},
			},
		},
		results: []string{
			exportTestCharms["wordpress"],
			exportTestBundles["wordpress"],
		},
	}, {
		about: "owner filter search",
		sp: SearchParams{
			Text: "",
			Filters: map[string][]string{
				"owner": {"foo"},
			},
		},
		results: []string{
			exportTestCharms["varnish"],
		},
	}, {
		about: "provides filter search",
		sp: SearchParams{
			Text: "",
			Filters: map[string][]string{
				"provides": {"mysql"},
			},
		},
		results: []string{
			exportTestCharms["mysql"],
		},
	}, {
		about: "requires filter search",
		sp: SearchParams{
			Text: "",
			Filters: map[string][]string{
				"requires": {"mysql"},
			},
		},
		results: []string{
			exportTestCharms["wordpress"],
		},
	}, {
		about: "series filter search",
		sp: SearchParams{
			Text: "",
			Filters: map[string][]string{
				"series": {"trusty"},
			},
		},
		results: []string{
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
		},
	}, {
		about: "summary filter search",
		sp: SearchParams{
			Text: "",
			Filters: map[string][]string{
				"summary": {"Database engine"},
			},
		},
		results: []string{
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
		},
	}, {
		about: "tags filter search",
		sp: SearchParams{
			Text: "",
			Filters: map[string][]string{
				"tags": {"wordpress"},
			},
		},
		results: []string{
			exportTestCharms["wordpress"],
			exportTestBundles["wordpress"],
		},
	}, {
		about: "bundle type filter search",
		sp: SearchParams{
			Text: "",
			Filters: map[string][]string{
				"type": {"bundle"},
			},
		},
		results: []string{
			exportTestBundles["wordpress"],
		},
	}, {
		about: "charm type filter search",
		sp: SearchParams{
			Text: "",
			Filters: map[string][]string{
				"type": {"charm"},
			},
		},
		results: []string{
			exportTestCharms["wordpress"],
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
		},
	}, {
		about: "charm & bundle type filter search",
		sp: SearchParams{
			Text: "",
			Filters: map[string][]string{
				"type": {"charm", "bundle"},
			},
		},
		results: []string{
			exportTestCharms["wordpress"],
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
			exportTestBundles["wordpress"],
		},
	}, {
		about: "invalid filter search",
		sp: SearchParams{
			Text: "",
			Filters: map[string][]string{
				"no such filter": {"foo"},
			},
		},
		results: []string{
			exportTestCharms["wordpress"],
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
			exportTestBundles["wordpress"],
		},
	}, {
		about: "valid & invalid filter search",
		sp: SearchParams{
			Text: "",
			Filters: map[string][]string{
				"no such filter": {"foo"},
				"type":           {"charm"},
			},
		},
		results: []string{
			exportTestCharms["wordpress"],
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
		},
	},
}

func (s *StoreSearchSuite) TestSearches(c *gc.C) {
	err := s.store.ExportToElasticSearch()
	c.Assert(err, gc.IsNil)
	s.store.ES.Database.RefreshIndex(s.TestIndex)
	for i, test := range searchTests {
		c.Logf("test %d: %s", i, test.about)
		res, err := s.store.Search(test.sp)
		c.Assert(err, gc.IsNil)
		assertSearchResults(c, res, test.results)
	}
}

func (s *StoreSearchSuite) TestLimitTestSearch(c *gc.C) {
	err := s.store.ExportToElasticSearch()
	s.store.ES.Database.RefreshIndex(s.TestIndex)
	c.Assert(err, gc.IsNil)
	sp := SearchParams{
		Text:  "wordpress",
		Limit: 1,
	}
	res, err := s.store.Search(sp)
	c.Assert(err, gc.IsNil)
	c.Assert(res.Results, gc.HasLen, 1)
}

func (s *StoreSearchSuite) TestPromulgatedRank(c *gc.C) {
	charmArchive := testing.Charms.CharmDir("varnish")
	url := charm.MustParseReference("cs:trusty/varnish-1")
	s.store.AddCharmWithArchive(url, charmArchive)
	err := s.store.ExportToElasticSearch()
	s.store.ES.Database.RefreshIndex(s.TestIndex)
	sp := SearchParams{
		Filters: map[string][]string{
			"name": {"varnish"},
		},
	}
	res, err := s.store.Search(sp)
	c.Assert(err, gc.IsNil)
	c.Assert(res.Results, gc.HasLen, 2)
	c.Assert(res.Results[0].String(), gc.Equals, "cs:trusty/varnish-1")
	c.Assert(res.Results[1].String(), gc.Equals, exportTestCharms["varnish"])
}

// assertSearchResults checks that the results obtained from a search are the same
// as those in the expected set, but in any order.
func assertSearchResults(c *gc.C, obtained SearchResult, expected []string) {
	c.Assert(len(obtained.Results), gc.Equals, len(expected))

	sort.Strings(expected)
	var ids []string
	for _, ref := range obtained.Results {
		ids = append(ids, ref.String())
	}
	sort.Strings(ids)
	for i, v := range expected {
		c.Assert(ids[i], gc.Equals, v)
	}
}
