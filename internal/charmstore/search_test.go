// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore

import (
	"net/url"
	"time"

	jc "github.com/juju/testing/checkers"
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
		Database: s.ES,
		Index:    s.TestIndex,
	})
	c.Assert(err, gc.IsNil)
	err = s.loadESConfig()
	c.Assert(err, gc.IsNil)
	s.store = store
	s.addCharmsToStore(store)
}

func (s *StoreSearchSuite) loadESConfig() error {
	if err := s.ES.PutIndex(s.TestIndex, esIndex); err != nil {
		return err
	}
	if err := s.ES.PutMapping(s.TestIndex, "entity", esMapping); err != nil {
		return err
	}
	return nil
}

var exportTestCharms = map[string]string{
	"wordpress": "cs:precise/wordpress-23",
	"mysql":     "cs:trusty/mysql-7",
	"varnish":   "cs:~foo/trusty/varnish-1",
}

var exportTestBundles = map[string]string{
	"wordpress": "cs:bundle/wordpress",
}

func (s *StoreSearchSuite) TestSuccessfulExport(c *gc.C) {
	err := s.store.ExportToElasticSearch()
	c.Assert(err, gc.IsNil)

	for _, ref := range exportTestCharms {
		var expected mongodoc.Entity
		var actual mongodoc.Entity
		err = s.store.DB.Entities().FindId(ref).One(&expected)
		c.Assert(err, gc.IsNil)
		err = s.store.ES.GetDocument(s.TestIndex, typeName, url.QueryEscape(ref), &actual)
		c.Assert(err, gc.IsNil)
		// make sure everything agrees on the time zone
		// TODO(mhilton) separate the functionality for comparing mongodoc.Entitys
		// if that needs to be performed in other places
		expected.UploadTime = expected.UploadTime.In(time.UTC)
		actual.UploadTime = actual.UploadTime.In(time.UTC)
		c.Assert(actual, jc.DeepEquals, expected)
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

func (s *StoreSearchSuite) TestBasicTextSearch(c *gc.C) {
	err := s.store.ExportToElasticSearch()
	c.Assert(err, gc.IsNil)
	s.store.ES.Database.RefreshIndex(s.TestIndex)
	sp := SearchParams{
		Text: "wordpress",
	}
	ids, err := s.store.Search(sp)
	c.Assert(err, gc.IsNil)
	c.Assert(ids, jc.DeepEquals, []string{url.QueryEscape(exportTestCharms["wordpress"])})
}

func (s *StoreSearchSuite) TestBlankTextSearch(c *gc.C) {
	err := s.store.ExportToElasticSearch()
	s.store.ES.Database.RefreshIndex(s.TestIndex)
	c.Assert(err, gc.IsNil)
	sp := SearchParams{
		Text: "",
	}
	ids, err := s.store.Search(sp)
	c.Assert(err, gc.IsNil)
	c.Assert(ids, jc.SameContents, []string{
		url.QueryEscape(exportTestCharms["wordpress"]),
		url.QueryEscape(exportTestCharms["mysql"]),
		url.QueryEscape(exportTestCharms["varnish"]),
		url.QueryEscape(exportTestBundles["wordpress"]),
	})
}

func (s *StoreSearchSuite) TestLimitTestSearch(c *gc.C) {
	charmArchive := testing.Charms.CharmDir("wordpress")
	ref := charm.MustParseReference(exportTestCharms["wordpress"])
	ref.Revision += 1
	s.store.AddCharmWithArchive(ref, charmArchive)

	err := s.store.ExportToElasticSearch()
	s.store.ES.Database.RefreshIndex(s.TestIndex)
	c.Assert(err, gc.IsNil)
	sp := SearchParams{
		Text:  "wordpress",
		Limit: 1,
	}
	ids, err := s.store.Search(sp)
	c.Assert(err, gc.IsNil)
	c.Assert(ids, gc.HasLen, 1)
}

func (s *StoreSearchSuite) TestAutoCompleteSearch(c *gc.C) {
	err := s.store.ExportToElasticSearch()
	c.Assert(err, gc.IsNil)
	s.store.ES.Database.RefreshIndex(s.TestIndex)
	sp := SearchParams{
		Text:         "word",
		AutoComplete: true,
	}
	ids, err := s.store.Search(sp)
	c.Assert(err, gc.IsNil)
	c.Assert(ids, jc.DeepEquals, []string{url.QueryEscape(exportTestCharms["wordpress"])})
}

func (s *StoreSearchSuite) TestFilteredSearchWithNameAlias(c *gc.C) {
	err := s.store.ExportToElasticSearch()
	c.Assert(err, gc.IsNil)
	s.store.ES.Database.RefreshIndex(s.TestIndex)
	sp := SearchParams{
		Text: "",
		Filters: map[string][]string{
			"name": {"mysql"},
		},
	}
	ids, err := s.store.Search(sp)
	c.Assert(err, gc.IsNil)
	c.Assert(ids, jc.DeepEquals, []string{url.QueryEscape(exportTestCharms["mysql"])})
}

func (s *StoreSearchSuite) TestFilteredSearchWithUnAliasedField(c *gc.C) {
	err := s.store.ExportToElasticSearch()
	c.Assert(err, gc.IsNil)
	s.store.ES.Database.RefreshIndex(s.TestIndex)
	sp := SearchParams{
		Text: "",
		Filters: map[string][]string{
			"name": {"mysql"},
		},
	}
	ids, err := s.store.Search(sp)
	c.Assert(err, gc.IsNil)
	c.Assert(ids, jc.DeepEquals, []string{url.QueryEscape(exportTestCharms["mysql"])})
}

func (s *StoreSearchSuite) TestSearchWithDescriptionFilter(c *gc.C) {
	err := s.store.ExportToElasticSearch()
	c.Assert(err, gc.IsNil)
	s.store.ES.Database.RefreshIndex(s.TestIndex)
	sp := SearchParams{
		Text: "",
		Filters: map[string][]string{
			"description": {"blog"},
		},
	}
	ids, err := s.store.Search(sp)
	c.Assert(err, gc.IsNil)
	c.Assert(ids, jc.DeepEquals, []string{url.QueryEscape(exportTestCharms["wordpress"])})
}

func (s *StoreSearchSuite) TestSearchWithNameFilter(c *gc.C) {
	err := s.store.ExportToElasticSearch()
	c.Assert(err, gc.IsNil)
	s.store.ES.Database.RefreshIndex(s.TestIndex)
	sp := SearchParams{
		Text: "",
		Filters: map[string][]string{
			"name": {"mysql"},
		},
	}
	ids, err := s.store.Search(sp)
	c.Assert(err, gc.IsNil)
	c.Assert(ids, jc.DeepEquals, []string{url.QueryEscape(exportTestCharms["mysql"])})
}

func (s *StoreSearchSuite) TestSearchWithOwnerFilter(c *gc.C) {
	err := s.store.ExportToElasticSearch()
	c.Assert(err, gc.IsNil)
	s.store.ES.Database.RefreshIndex(s.TestIndex)
	sp := SearchParams{
		Text: "",
		Filters: map[string][]string{
			"owner": {"foo"},
		},
	}
	ids, err := s.store.Search(sp)
	c.Assert(err, gc.IsNil)
	c.Assert(ids, jc.DeepEquals, []string{url.QueryEscape(exportTestCharms["varnish"])})
}

func (s *StoreSearchSuite) TestSearchWithProvidesFilter(c *gc.C) {
	err := s.store.ExportToElasticSearch()
	c.Assert(err, gc.IsNil)
	s.store.ES.Database.RefreshIndex(s.TestIndex)
	sp := SearchParams{
		Text: "",
		Filters: map[string][]string{
			"provides": {"mysql"},
		},
	}
	ids, err := s.store.Search(sp)
	c.Assert(err, gc.IsNil)
	c.Assert(ids, jc.DeepEquals, []string{url.QueryEscape(exportTestCharms["mysql"])})
}

func (s *StoreSearchSuite) TestSearchWithRequiresFilter(c *gc.C) {
	err := s.store.ExportToElasticSearch()
	c.Assert(err, gc.IsNil)
	s.store.ES.Database.RefreshIndex(s.TestIndex)
	sp := SearchParams{
		Text: "",
		Filters: map[string][]string{
			"requires": {"mysql"},
		},
	}
	ids, err := s.store.Search(sp)
	c.Assert(err, gc.IsNil)
	c.Assert(ids, jc.DeepEquals, []string{url.QueryEscape(exportTestCharms["wordpress"])})
}

func (s *StoreSearchSuite) TestSearchWithSeriesFilter(c *gc.C) {
	err := s.store.ExportToElasticSearch()
	c.Assert(err, gc.IsNil)
	s.store.ES.Database.RefreshIndex(s.TestIndex)
	sp := SearchParams{
		Text: "",
		Filters: map[string][]string{
			"series": {"trusty"},
		},
	}
	ids, err := s.store.Search(sp)
	c.Assert(err, gc.IsNil)
	c.Assert(ids, jc.SameContents, []string{
		url.QueryEscape(exportTestCharms["mysql"]),
		url.QueryEscape(exportTestCharms["varnish"]),
	})
}

func (s *StoreSearchSuite) TestSearchWithSummaryFilter(c *gc.C) {
	err := s.store.ExportToElasticSearch()
	c.Assert(err, gc.IsNil)
	s.store.ES.Database.RefreshIndex(s.TestIndex)
	sp := SearchParams{
		Text: "",
		Filters: map[string][]string{
			"summary": {"Database engine"},
		},
	}
	ids, err := s.store.Search(sp)
	c.Assert(err, gc.IsNil)
	c.Assert(ids, jc.SameContents, []string{
		url.QueryEscape(exportTestCharms["mysql"]),
		url.QueryEscape(exportTestCharms["varnish"]),
	})
}

func (s *StoreSearchSuite) TestSearchWithTagsFilter(c *gc.C) {
	err := s.store.ExportToElasticSearch()
	c.Assert(err, gc.IsNil)
	s.store.ES.Database.RefreshIndex(s.TestIndex)
	sp := SearchParams{
		Text: "",
		Filters: map[string][]string{
			"tags": {"wordpress"},
		},
	}
	ids, err := s.store.Search(sp)
	c.Assert(err, gc.IsNil)
	c.Assert(ids, jc.SameContents, []string{
		url.QueryEscape(exportTestCharms["wordpress"]),
		url.QueryEscape(exportTestBundles["wordpress"]),
	})
}

func (s *StoreSearchSuite) TestSearchWithBundleType(c *gc.C) {
	err := s.store.ExportToElasticSearch()
	c.Assert(err, gc.IsNil)
	s.store.ES.Database.RefreshIndex(s.TestIndex)
	sp := SearchParams{
		Text: "",
		Filters: map[string][]string{
			"type": {"bundle"},
		},
	}
	ids, err := s.store.Search(sp)
	c.Assert(err, gc.IsNil)
	c.Assert(ids, jc.DeepEquals, []string{url.QueryEscape(exportTestBundles["wordpress"])})
}

func (s *StoreSearchSuite) TestSearchWithCharmType(c *gc.C) {
	err := s.store.ExportToElasticSearch()
	c.Assert(err, gc.IsNil)
	s.store.ES.Database.RefreshIndex(s.TestIndex)
	sp := SearchParams{
		Text: "",
		Filters: map[string][]string{
			"type": {"charm"},
		},
	}
	ids, err := s.store.Search(sp)
	c.Assert(err, gc.IsNil)
	c.Assert(ids, jc.SameContents, []string{
		url.QueryEscape(exportTestCharms["wordpress"]),
		url.QueryEscape(exportTestCharms["mysql"]),
		url.QueryEscape(exportTestCharms["varnish"]),
	})
}

func (s *StoreSearchSuite) TestSearchWithCharmAndBundleTypes(c *gc.C) {
	err := s.store.ExportToElasticSearch()
	c.Assert(err, gc.IsNil)
	s.store.ES.Database.RefreshIndex(s.TestIndex)
	sp := SearchParams{
		Text: "",
		Filters: map[string][]string{
			"type": {"charm", "bundle"},
		},
	}
	ids, err := s.store.Search(sp)
	c.Assert(err, gc.IsNil)
	c.Assert(ids, jc.SameContents, []string{
		url.QueryEscape(exportTestCharms["wordpress"]),
		url.QueryEscape(exportTestCharms["mysql"]),
		url.QueryEscape(exportTestCharms["varnish"]),
		url.QueryEscape(exportTestBundles["wordpress"]),
	})
}

func (s *StoreSearchSuite) TestSearchWithInvalidFilter(c *gc.C) {
	err := s.store.ExportToElasticSearch()
	c.Assert(err, gc.IsNil)
	s.store.ES.Database.RefreshIndex(s.TestIndex)
	sp := SearchParams{
		Text: "",
		Filters: map[string][]string{
			"no such filter": {"foo"},
		},
	}
	ids, err := s.store.Search(sp)
	c.Assert(err, gc.IsNil)
	c.Assert(ids, jc.SameContents, []string{
		url.QueryEscape(exportTestCharms["wordpress"]),
		url.QueryEscape(exportTestCharms["mysql"]),
		url.QueryEscape(exportTestCharms["varnish"]),
		url.QueryEscape(exportTestBundles["wordpress"]),
	})
}

func (s *StoreSearchSuite) TestSearchWithValidAndInvalidFilters(c *gc.C) {
	err := s.store.ExportToElasticSearch()
	c.Assert(err, gc.IsNil)
	s.store.ES.Database.RefreshIndex(s.TestIndex)
	sp := SearchParams{
		Text: "",
		Filters: map[string][]string{
			"type":           {"charm"},
			"no such filter": {"foo"},
		},
	}
	ids, err := s.store.Search(sp)
	c.Assert(err, gc.IsNil)
	c.Assert(ids, jc.SameContents, []string{
		url.QueryEscape(exportTestCharms["wordpress"]),
		url.QueryEscape(exportTestCharms["mysql"]),
		url.QueryEscape(exportTestCharms["varnish"]),
	})
}
