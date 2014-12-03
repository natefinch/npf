// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/juju/loggo"
	jc "github.com/juju/testing/checkers"
	"github.com/juju/testing/httptesting"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v4"

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
	"wordpress-simple": "cs:bundle/wordpress-simple-4",
}

func (s *SearchSuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoESSuite.SetUpTest(c)
	si := &charmstore.SearchIndex{s.ES, s.TestIndex}
	s.srv, s.store = newServer(c, s.Session, si, serverParams)
	s.addCharmsToStore(c, s.store)
	err := s.ES.RefreshIndex(s.TestIndex)
	c.Assert(err, gc.IsNil)
}

func (s *SearchSuite) addCharmsToStore(c *gc.C, store *charmstore.Store) {
	for name, ref := range exportTestCharms {
		err := store.AddCharmWithArchive(charm.MustParseReference(ref), getCharm(name))
		c.Assert(err, gc.IsNil)
	}
	for name, ref := range exportTestBundles {
		err := store.AddBundleWithArchive(charm.MustParseReference(ref), getBundle(name))
		c.Assert(err, gc.IsNil)
	}
}

func getCharm(name string) *charm.CharmDir {
	ca := storetesting.Charms.CharmDir(name)
	ca.Meta().Categories = append(strings.Split(name, "-"), "bar")
	return ca
}

func getBundle(name string) *charm.BundleDir {
	ba := storetesting.Charms.BundleDir(name)
	ba.Data().Tags = append(strings.Split(name, "-"), "baz")
	return ba
}

func (s *SearchSuite) TestParseSearchParams(c *gc.C) {
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
		about: "autocomplete",
		query: "autocomplete=1",
		expectParams: charmstore.SearchParams{
			AutoComplete: true,
		},
	}, {
		about:       "invalid autocomplete",
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
			c.Assert(err, gc.Not(gc.IsNil))
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
			exportTestBundles["wordpress-simple"],
		},
	}, {
		about: "text search",
		query: "text=wordpress",
		results: []string{
			exportTestCharms["wordpress"],
			exportTestBundles["wordpress-simple"],
		},
	}, {
		about: "autocomplete search",
		query: "text=word&autocomplete=1",
		results: []string{
			exportTestCharms["wordpress"],
			exportTestBundles["wordpress-simple"],
		},
	}, {
		about: "blank text search",
		query: "text=",
		results: []string{
			exportTestCharms["wordpress"],
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
			exportTestBundles["wordpress-simple"],
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
			exportTestBundles["wordpress-simple"],
		},
	}, {
		about: "type filter search",
		query: "type=bundle",
		results: []string{
			exportTestBundles["wordpress-simple"],
		},
	}, {
		about: "multiple type filter search",
		query: "type=bundle&type=charm",
		results: []string{
			exportTestCharms["wordpress"],
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
			exportTestBundles["wordpress-simple"],
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
			exportTestBundles["wordpress-simple"],
		},
	}, {
		about:   "paginated search",
		query:   "name=mysql&skip=1",
		results: []string{},
	}}
	for i, test := range tests {
		c.Logf("test %d. %s", i, test.about)
		rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler: s.srv,
			URL:     storeURL("search?" + test.query),
		})
		var sr params.SearchResponse
		err := json.Unmarshal(rec.Body.Bytes(), &sr)
		c.Assert(err, gc.IsNil)
		c.Assert(sr.Results, gc.HasLen, len(test.results))
		c.Logf("results: %s", rec.Body.Bytes())
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
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
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
		query: "name=wordpress-simple&type=bundle&include=bundle-metadata",
		meta: map[string]interface{}{
			"bundle-metadata": getBundle("wordpress-simple").Data(),
		},
	}, {
		about: "bundle-machine-count",
		query: "name=wordpress-simple&type=bundle&include=bundle-machine-count",
		meta: map[string]interface{}{
			"bundle-machine-count": params.BundleCount{2},
		},
	}, {
		about: "bundle-unit-count",
		query: "name=wordpress-simple&type=bundle&include=bundle-unit-count",
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
		rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler: s.srv,
			URL:     storeURL("search?" + test.query),
		})
		c.Assert(rec.Code, gc.Equals, http.StatusOK)
		var sr struct {
			Results []struct {
				Meta json.RawMessage
			}
		}
		err := json.Unmarshal(rec.Body.Bytes(), &sr)
		c.Assert(err, gc.IsNil)
		c.Assert(sr.Results, gc.HasLen, 1)
		c.Assert(string(sr.Results[0].Meta), jc.JSONEquals, test.meta)
	}
}

func (s *SearchSuite) TestSearchError(c *gc.C) {
	err := s.ES.DeleteIndex(s.TestIndex)
	c.Assert(err, gc.Equals, nil)
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("search?name=wordpress"),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusInternalServerError)
	var resp params.Error
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	c.Assert(err, gc.IsNil)
	c.Assert(resp.Code, gc.Equals, params.ErrorCode(""))
	c.Assert(resp.Message, gc.Matches, "error performing search: search failed: .*")
}

func (s *SearchSuite) TestSearchIncludeError(c *gc.C) {
	// Perform a search for all charms, including the
	// manifest, which will try to retrieve all charm
	// blobs.
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("search?type=charm&include=manifest"),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	var resp params.SearchResponse
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	c.Assert(resp.Results, gc.HasLen, len(exportTestCharms))

	// Now remove one of the blobs. The search should still
	// work, but only return a single result.
	blobName, _, err := s.store.BlobNameAndHash(charm.MustParseReference(exportTestCharms["wordpress"]))
	c.Assert(err, gc.IsNil)
	err = s.store.BlobStore.Remove(blobName)
	c.Assert(err, gc.IsNil)

	// Now search again - we should get one result less
	// (and the error will be logged).

	// Register a logger that so that we can check the logging output.
	// It will be automatically removed later because IsolatedMgoESSuite
	// uses LoggingSuite.
	var tw loggo.TestWriter
	err = loggo.RegisterWriter("test-log", &tw, loggo.DEBUG)
	c.Assert(err, gc.IsNil)

	rec = httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("search?type=charm&include=manifest"),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	resp = params.SearchResponse{}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	c.Assert(resp.Results, gc.HasLen, len(exportTestCharms)-1)

	c.Assert(tw.Log(), jc.LogMatches, []string{"cannot retrieve metadata for cs:precise/wordpress-23: cannot open archive data for cs:precise/wordpress-23: .*"})
}

func (s *SearchSuite) TestSorting(c *gc.C) {
	tests := []struct {
		about   string
		query   string
		results []string
	}{{
		about: "name ascending",
		query: "sort=name",
		results: []string{
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
			exportTestCharms["wordpress"],
			exportTestBundles["wordpress-simple"],
		},
	}, {
		about: "name descending",
		query: "sort=-name",
		results: []string{
			exportTestBundles["wordpress-simple"],
			exportTestCharms["wordpress"],
			exportTestCharms["varnish"],
			exportTestCharms["mysql"],
		},
	}, {
		about: "series ascending",
		query: "sort=series,name",
		results: []string{
			exportTestBundles["wordpress-simple"],
			exportTestCharms["wordpress"],
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
		},
	}, {
		about: "series descending",
		query: "sort=-series&sort=name",
		results: []string{
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
			exportTestCharms["wordpress"],
			exportTestBundles["wordpress-simple"],
		},
	}, {
		about: "owner ascending",
		query: "sort=owner,name",
		results: []string{
			exportTestCharms["mysql"],
			exportTestCharms["wordpress"],
			exportTestBundles["wordpress-simple"],
			exportTestCharms["varnish"],
		},
	}, {
		about: "owner descending",
		query: "sort=-owner&sort=name",
		results: []string{
			exportTestCharms["varnish"],
			exportTestCharms["mysql"],
			exportTestCharms["wordpress"],
			exportTestBundles["wordpress-simple"],
		},
	}}
	for i, test := range tests {
		c.Logf("test %d. %s", i, test.about)
		rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler: s.srv,
			URL:     storeURL("search?" + test.query),
		})
		var sr params.SearchResponse
		err := json.Unmarshal(rec.Body.Bytes(), &sr)
		c.Assert(err, gc.IsNil)
		c.Assert(sr.Results, gc.HasLen, len(test.results))
		for i, res := range sr.Results {
			c.Assert(res.Id.String(), gc.Equals, test.results[i])
		}
	}
}

func (s *SearchSuite) TestSortUnsupportedField(c *gc.C) {
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("search?sort=foo"),
	})
	var e params.Error
	err := json.Unmarshal(rec.Body.Bytes(), &e)
	c.Assert(err, gc.IsNil)
	c.Assert(e.Code, gc.Equals, params.ErrBadRequest)
	c.Assert(e.Message, gc.Equals, "invalid sort field: foo")
}
