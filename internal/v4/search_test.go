// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4_test

import (
	"encoding/json"
	"net/http"
	"net/url"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v4"
	"gopkg.in/juju/charm.v4/testing"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/storetesting"
	. "github.com/juju/charmstore/internal/v4"
	"github.com/juju/charmstore/params"
)

type SearchSuite struct {
	storetesting.IsolatedMgoESSuite
	srv   http.Handler
	store *charmstore.Store
}

var _ = gc.Suite(&SearchSuite{})

var exportTestCharms = map[string]string{
	"wordpress": "cs:precise/wordpress-23",
	"mysql":     "cs:trusty/mysql-7",
	"varnish":   "cs:~foo/trusty/varnish-1",
}

var exportTestBundles = map[string]string{
	"wordpress": "cs:bundle/wordpress-4",
}

func (s *SearchSuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoESSuite.SetUpTest(c)
	s.srv, s.store = newServer(c, s.Session, s.ES.Index(s.TestIndex), serverParams)
	err := s.LoadESConfig(s.TestIndex)
	c.Assert(err, gc.IsNil)
	s.addCharmsToStore(s.store)
	err = s.ES.RefreshIndex(s.TestIndex)
	c.Assert(err, gc.IsNil)
}

func (s *SearchSuite) addCharmsToStore(store *charmstore.Store) {
	for name, ref := range exportTestCharms {
		store.AddCharmWithArchive(charm.MustParseReference(ref), getCharm(name))
	}
	for name, ref := range exportTestBundles {
		store.AddBundleWithArchive(charm.MustParseReference(ref), getBundle(name))
	}
}

func getCharm(name string) *charm.CharmDir {
	ca := testing.Charms.CharmDir(name)
	ca.Meta().Categories = []string{name, "bar"}
	return ca
}

func getBundle(name string) *charm.BundleDir {
	ba := testing.Charms.BundleDir(name)
	ba.Data().Tags = []string{name, "baz"}
	return ba
}

func (s *SearchSuite) TestParseSerchParams(c *gc.C) {
	tests := []struct {
		about        string
		query        string
		expectParams charmstore.SearchParams
		expectError  string
	}{{
		about: "bare search",
		query: "",
	}, {
		about: "text search",
		query: "text=test",
		expectParams: charmstore.SearchParams{
			Text: "test",
		},
	}, {
		about: "autocomple",
		query: "autocomplete=1",
		expectParams: charmstore.SearchParams{
			AutoComplete: true,
		},
	}, {
		about:       "invalid autocomple",
		query:       "autocomplete=true",
		expectError: `invalid autocomplete parameter: unexpected bool value "true" (must be "0" or "1")`,
	}, {
		about: "limit",
		query: "limit=20",
		expectParams: charmstore.SearchParams{
			Limit: 20,
		},
	}, {
		about:       "invalid limit",
		query:       "limit=twenty",
		expectError: `invalid limit parameter: could not parse integer: strconv.ParseInt: parsing "twenty": invalid syntax`,
	}, {
		about:       "limit too low",
		query:       "limit=-1",
		expectError: "invalid limit parameter: expected integer greater than zero",
	}, {
		about: "include",
		query: "include=archive-size",
		expectParams: charmstore.SearchParams{
			Include: []string{"archive-size"},
		},
	}, {
		about: "include many",
		query: "include=archive-size&include=bundle-data",
		expectParams: charmstore.SearchParams{
			Include: []string{"archive-size", "bundle-data"},
		},
	}, {
		about: "include many with blanks",
		query: "include=archive-size&include=&include=bundle-data",
		expectParams: charmstore.SearchParams{
			Include: []string{"archive-size", "bundle-data"},
		},
	}, {
		about: "description filter",
		query: "description=text",
		expectParams: charmstore.SearchParams{
			Filters: map[string][]string{
				"description": {"text"},
			},
		},
	}, {
		about: "name filter",
		query: "name=text",
		expectParams: charmstore.SearchParams{
			Filters: map[string][]string{
				"name": {"text"},
			},
		},
	}, {
		about: "owner filter",
		query: "owner=text",
		expectParams: charmstore.SearchParams{
			Filters: map[string][]string{
				"owner": {"text"},
			},
		},
	}, {
		about: "provides filter",
		query: "provides=text",
		expectParams: charmstore.SearchParams{
			Filters: map[string][]string{
				"provides": {"text"},
			},
		},
	}, {
		about: "requires filter",
		query: "requires=text",
		expectParams: charmstore.SearchParams{
			Filters: map[string][]string{
				"requires": {"text"},
			},
		},
	}, {
		about: "series filter",
		query: "series=text",
		expectParams: charmstore.SearchParams{
			Filters: map[string][]string{
				"series": {"text"},
			},
		},
	}, {
		about: "tags filter",
		query: "tags=text",
		expectParams: charmstore.SearchParams{
			Filters: map[string][]string{
				"tags": {"text"},
			},
		},
	}, {
		about: "type filter",
		query: "type=text",
		expectParams: charmstore.SearchParams{
			Filters: map[string][]string{
				"type": {"text"},
			},
		},
	}, {
		about: "many filters",
		query: "name=name&owner=owner&series=series1&series=series2",
		expectParams: charmstore.SearchParams{
			Filters: map[string][]string{
				"name":   {"name"},
				"owner":  {"owner"},
				"series": {"series1", "series2"},
			},
		},
	}, {
		about:       "bad parameter",
		query:       "a=b",
		expectError: "invalid parameter: a",
	}, {
		about: "skip",
		query: "skip=20",
		expectParams: charmstore.SearchParams{
			Skip: 20,
		},
	}, {
		about:       "invalid skip",
		query:       "skip=twenty",
		expectError: `invalid skip parameter: could not parse integer: strconv.ParseInt: parsing "twenty": invalid syntax`,
	}, {
		about:       "skip too low",
		query:       "skip=-1",
		expectError: "invalid skip parameter: expected non-negative integer",
	}}
	for i, test := range tests {
		c.Logf("test %d. %s", i, test.about)
		var req http.Request
		var err error
		req.Form, err = url.ParseQuery(test.query)
		c.Assert(err, gc.IsNil)
		sp, err := ParseSearchParams(&req)
		if test.expectError != "" {
			c.Assert(err.Error(), gc.Equals, test.expectError)
		} else {
			c.Assert(err, gc.IsNil)
		}
		c.Assert(sp, jc.DeepEquals, test.expectParams)
	}
}

func (s *SearchSuite) TestSuccessfulSearches(c *gc.C) {
	tests := []struct {
		about   string
		query   string
		results []string
	}{{
		about: "bare search",
		query: "",
		results: []string{
			exportTestCharms["wordpress"],
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
			exportTestBundles["wordpress"],
		},
	}, {
		about: "text search",
		query: "text=wordpress",
		results: []string{
			exportTestCharms["wordpress"],
			exportTestBundles["wordpress"],
		},
	}, {
		about: "autocomplete search",
		query: "text=word&autocomplete=1",
		results: []string{
			exportTestCharms["wordpress"],
			exportTestBundles["wordpress"],
		},
	}, {
		about: "blank text search",
		query: "text=",
		results: []string{
			exportTestCharms["wordpress"],
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
			exportTestBundles["wordpress"],
		},
	}, {
		about: "description filter search",
		query: "description=database",
		results: []string{
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
		},
	}, {
		about: "name filter search",
		query: "name=mysql",
		results: []string{
			exportTestCharms["mysql"],
		},
	}, {
		about: "owner filter search",
		query: "owner=foo",
		results: []string{
			exportTestCharms["varnish"],
		},
	}, {
		about: "provides filter search",
		query: "provides=mysql",
		results: []string{
			exportTestCharms["mysql"],
		},
	}, {
		about: "requires filter search",
		query: "requires=mysql",
		results: []string{
			exportTestCharms["wordpress"],
		},
	}, {
		about: "series filter search",
		query: "series=trusty",
		results: []string{
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
		},
	}, {
		about: "summary filter search",
		query: "summary=database",
		results: []string{
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
		},
	}, {
		about: "tags filter search",
		query: "tags=wordpress",
		results: []string{
			exportTestCharms["wordpress"],
			exportTestBundles["wordpress"],
		},
	}, {
		about: "type filter search",
		query: "type=bundle",
		results: []string{
			exportTestBundles["wordpress"],
		},
	}, {
		about: "multiple type filter search",
		query: "type=bundle&type=charm",
		results: []string{
			exportTestCharms["wordpress"],
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
			exportTestBundles["wordpress"],
		},
	}, {
		about: "provides multiple interfaces filter search",
		query: "provides=monitoring+http",
		results: []string{
			exportTestCharms["wordpress"],
		},
	}, {
		about: "requires multiple interfaces filter search",
		query: "requires=mysql+varnish",
		results: []string{
			exportTestCharms["wordpress"],
		},
	}, {
		about: "multiple tags filter search",
		query: "tags=mysql+bar",
		results: []string{
			exportTestCharms["mysql"],
		},
	}, {
		about: "blank owner",
		query: "owner=",
		results: []string{
			exportTestCharms["wordpress"],
			exportTestCharms["mysql"],
			exportTestBundles["wordpress"],
		},
	}, {
		about:   "paginated search",
		query:   "name=mysql&skip=1",
		results: []string{},
	}}
	for i, test := range tests {
		c.Logf("test %d. %s", i, test.about)
		rec := storetesting.DoRequest(c, storetesting.DoRequestParams{
			Handler: s.srv,
			URL:     storeURL("search?" + test.query),
		})
		var sr params.SearchResponse
		err := json.Unmarshal(rec.Body.Bytes(), &sr)
		c.Assert(err, gc.IsNil)
		c.Assert(sr.Results, gc.HasLen, len(test.results))
	OUTER:
		for _, res := range sr.Results {
			for i, r := range test.results {
				if res.Id.String() == r {
					test.results[i] = ""
					continue OUTER
				}
			}
			c.Errorf("%s:Unexpected result received %q", test.about, res.Id.String())
		}
	}
}

func (s *SearchSuite) TestPaginatedSearch(c *gc.C) {
	rec := storetesting.DoRequest(c, storetesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("search?text=wordpress&skip=1"),
	})
	var sr params.SearchResponse
	err := json.Unmarshal(rec.Body.Bytes(), &sr)
	c.Assert(err, gc.IsNil)
	c.Assert(sr.Results, gc.HasLen, 1)
	c.Assert(sr.Total, gc.Equals, 2)
}

func (s *SearchSuite) TestMetadataFields(c *gc.C) {
	tests := []struct {
		about string
		query string
		meta  map[string]interface{}
	}{{
		about: "archive-size",
		query: "name=mysql&include=archive-size",
		meta: map[string]interface{}{
			"archive-size": params.ArchiveSizeResponse{438},
		},
	}, {
		about: "bundle-metadata",
		query: "name=wordpress&type=bundle&include=bundle-metadata",
		meta: map[string]interface{}{
			"bundle-metadata": getBundle("wordpress").Data(),
		},
	}, {
		about: "bundle-machine-count",
		query: "name=wordpress&type=bundle&include=bundle-machine-count",
		meta: map[string]interface{}{
			"bundle-machine-count": params.BundleCount{2},
		},
	}, {
		about: "bundle-unit-count",
		query: "name=wordpress&type=bundle&include=bundle-unit-count",
		meta: map[string]interface{}{
			"bundle-unit-count": params.BundleCount{2},
		},
	}, {
		about: "charm-actions",
		query: "name=wordpress&type=charm&include=charm-actions",
		meta: map[string]interface{}{
			"charm-actions": getCharm("wordpress").Actions(),
		},
	}, {
		about: "charm-config",
		query: "name=wordpress&type=charm&include=charm-config",
		meta: map[string]interface{}{
			"charm-config": getCharm("wordpress").Config(),
		},
	}, {
		about: "charm-related",
		query: "name=wordpress&type=charm&include=charm-related",
		meta: map[string]interface{}{
			"charm-related": params.RelatedResponse{
				Provides: map[string][]params.MetaAnyResponse{
					"mysql": {
						{
							Id: charm.MustParseReference(exportTestCharms["mysql"]),
						},
					},
					"varnish": {
						{
							Id: charm.MustParseReference(exportTestCharms["varnish"]),
						},
					},
				},
			},
		},
	}, {
		about: "multiple values",
		query: "name=wordpress&type=charm&include=charm-related&include=charm-config",
		meta: map[string]interface{}{
			"charm-related": params.RelatedResponse{
				Provides: map[string][]params.MetaAnyResponse{
					"mysql": {
						{
							Id: charm.MustParseReference(exportTestCharms["mysql"]),
						},
					},
					"varnish": {
						{
							Id: charm.MustParseReference(exportTestCharms["varnish"]),
						},
					},
				},
			},
			"charm-config": getCharm("wordpress").Config(),
		},
	}}
	for i, test := range tests {
		c.Logf("test %d. %s", i, test.about)
		rec := storetesting.DoRequest(c, storetesting.DoRequestParams{
			Handler: s.srv,
			URL:     storeURL("search?" + test.query),
		})
		var sr struct {
			Results []struct {
				Meta json.RawMessage
			}
		}
		err := json.Unmarshal(rec.Body.Bytes(), &sr)
		c.Assert(err, gc.IsNil)
		c.Assert(sr.Results, gc.HasLen, 1)
		c.Assert([]byte(sr.Results[0].Meta), storetesting.JSONEquals, test.meta)
	}
}
