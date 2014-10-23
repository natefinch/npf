// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4_test

import (
	"encoding/json"
	"net/http"

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

func (s *SearchSuite) TestParseSearchParamsText(c *gc.C) {
	var req http.Request
	req.Form = map[string][]string{"text": {"test text"}}
	sp, err := ParseSearchParams(&req)
	c.Assert(err, gc.IsNil)
	c.Assert(sp.Text, gc.Equals, "test text")
}

func (s *SearchSuite) TestParseSearchParamsAutocompete(c *gc.C) {
	var req http.Request
	req.Form = map[string][]string{"autocomplete": {"1"}}
	sp, err := ParseSearchParams(&req)
	c.Assert(err, gc.IsNil)
	c.Assert(sp.AutoComplete, gc.Equals, true)
}

func (s *SearchSuite) TestParseSearchParamsFilters(c *gc.C) {
	var req http.Request
	req.Form = map[string][]string{
		"tags": {"f11", "f12"},
		"name": {"f21"},
	}
	sp, err := ParseSearchParams(&req)
	c.Assert(err, gc.IsNil)
	c.Assert(sp.Filters["tags"], jc.DeepEquals, []string{"f11", "f12"})
	c.Assert(sp.Filters["name"], jc.DeepEquals, []string{"f21"})
}

func (s *SearchSuite) TestParseSearchParamsLimit(c *gc.C) {
	var req http.Request
	req.Form = map[string][]string{"limit": {"20"}}
	sp, err := ParseSearchParams(&req)
	c.Assert(err, gc.IsNil)
	c.Assert(sp.Limit, gc.Equals, 20)
}

func (s *SearchSuite) TestParseSearchParamsInclude(c *gc.C) {
	var req http.Request
	req.Form = map[string][]string{"include": {"meta1", "meta2"}}
	sp, err := ParseSearchParams(&req)
	c.Assert(err, gc.IsNil)
	c.Assert(sp.Include, jc.DeepEquals, []string{"meta1", "meta2"})
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
