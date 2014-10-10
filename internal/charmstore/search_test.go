// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore

import (
	"net/url"
	"time"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
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
	"mysql":     "cs:precise/mysql-42",
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
		url := mustParseReference(ref)
		store.AddCharmWithArchive(url, charmArchive)
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
	})
}

func (s *StoreSearchSuite) TestLimitTestSearch(c *gc.C) {
	charmArchive := testing.Charms.CharmDir("wordpress")
	ref := mustParseReference(exportTestCharms["wordpress"])
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
			"CharmMeta.Name": {"mysql"},
		},
	}
	ids, err := s.store.Search(sp)
	c.Assert(err, gc.IsNil)
	c.Assert(ids, jc.DeepEquals, []string{url.QueryEscape(exportTestCharms["mysql"])})
}
