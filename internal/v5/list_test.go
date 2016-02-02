// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v5_test // import "gopkg.in/juju/charmstore.v5-unstable/internal/v5"

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/juju/loggo"
	jc "github.com/juju/testing/checkers"
	"github.com/juju/testing/httptesting"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"
	"gopkg.in/macaroon-bakery.v1/bakery/checkers"
	"gopkg.in/macaroon-bakery.v1/httpbakery"
	"gopkg.in/macaroon.v1"
	"gopkg.in/mgo.v2/bson"

	"gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"
	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
	"gopkg.in/juju/charmstore.v5-unstable/internal/storetesting"
)

type ListSuite struct {
	commonSuite
}

var _ = gc.Suite(&ListSuite{})

var exportListTestCharms = map[string]*router.ResolvedURL{
	"wordpress": newResolvedURL("cs:~charmers/precise/wordpress-23", 23),
	"mysql":     newResolvedURL("cs:~openstack-charmers/trusty/mysql-7", 7),
	"varnish":   newResolvedURL("cs:~foo/trusty/varnish-1", -1),
	"riak":      newResolvedURL("cs:~charmers/trusty/riak-67", 67),
}

var exportListTestBundles = map[string]*router.ResolvedURL{
	"wordpress-simple": newResolvedURL("cs:~charmers/bundle/wordpress-simple-4", 4),
}

func (s *ListSuite) SetUpSuite(c *gc.C) {
	s.enableIdentity = true
	s.commonSuite.SetUpSuite(c)
}

func (s *ListSuite) SetUpTest(c *gc.C) {
	s.commonSuite.SetUpTest(c)
	s.addCharmsToStore(c)
	// hide the riak charm
	err := s.store.DB.BaseEntities().UpdateId(
		charm.MustParseURL("cs:~charmers/riak"),
		bson.D{{"$set", map[string]mongodoc.ACL{
			"acls": {
				Read: []string{"charmers", "test-user"},
			},
		}}},
	)
	c.Assert(err, gc.IsNil)
}

func (s *ListSuite) addCharmsToStore(c *gc.C) {
	for name, id := range exportListTestCharms {
		err := s.store.AddCharmWithArchive(id, getListCharm(name))
		c.Assert(err, gc.IsNil)
		err = s.store.SetPerms(&id.URL, "read", params.Everyone, id.URL.User)
		c.Assert(err, gc.IsNil)
		err = s.store.UpdateSearch(id)
		c.Assert(err, gc.IsNil)
	}
	for name, id := range exportListTestBundles {
		err := s.store.AddBundleWithArchive(id, getListBundle(name))
		c.Assert(err, gc.IsNil)
		err = s.store.SetPerms(&id.URL, "read", params.Everyone, id.URL.User)
		c.Assert(err, gc.IsNil)
		err = s.store.UpdateSearch(id)
		c.Assert(err, gc.IsNil)
	}
}

func getListCharm(name string) *storetesting.Charm {
	ca := storetesting.Charms.CharmDir(name)
	meta := ca.Meta()
	meta.Categories = append(strings.Split(name, "-"), "bar")
	return storetesting.NewCharm(meta)
}

func getListBundle(name string) *storetesting.Bundle {
	ba := storetesting.Charms.BundleDir(name)
	data := ba.Data()
	data.Tags = append(strings.Split(name, "-"), "baz")
	return storetesting.NewBundle(data)
}

func (s *ListSuite) TestSuccessfulList(c *gc.C) {
	tests := []struct {
		about   string
		query   string
		results []*router.ResolvedURL
	}{{
		about: "bare list",
		query: "",
		results: []*router.ResolvedURL{
			exportTestBundles["wordpress-simple"],
			exportTestCharms["wordpress"],
			exportTestCharms["varnish"],
			exportTestCharms["mysql"],
		},
	}, {
		about: "name filter list",
		query: "name=mysql",
		results: []*router.ResolvedURL{
			exportTestCharms["mysql"],
		},
	}, {
		about: "owner filter list",
		query: "owner=foo",
		results: []*router.ResolvedURL{
			exportTestCharms["varnish"],
		},
	}, {
		about: "series filter list",
		query: "series=trusty",
		results: []*router.ResolvedURL{
			exportTestCharms["varnish"],
			exportTestCharms["mysql"],
		},
	}, {
		about: "type filter list",
		query: "type=bundle",
		results: []*router.ResolvedURL{
			exportTestBundles["wordpress-simple"],
		},
	}, {
		about: "promulgated",
		query: "promulgated=1",
		results: []*router.ResolvedURL{
			exportTestBundles["wordpress-simple"],
			exportTestCharms["wordpress"],
			exportTestCharms["mysql"],
		},
	}, {
		about: "not promulgated",
		query: "promulgated=0",
		results: []*router.ResolvedURL{
			exportTestCharms["varnish"],
		},
	}, {
		about: "promulgated with owner",
		query: "promulgated=1&owner=openstack-charmers",
		results: []*router.ResolvedURL{
			exportTestCharms["mysql"],
		},
	}}
	for i, test := range tests {
		c.Logf("test %d. %s", i, test.about)
		rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler: s.srv,
			URL:     storeURL("list?" + test.query),
		})
		var sr params.ListResponse
		err := json.Unmarshal(rec.Body.Bytes(), &sr)
		c.Assert(err, gc.IsNil)
		c.Assert(sr.Results, gc.HasLen, len(test.results))
		c.Logf("results: %s", rec.Body.Bytes())
		for i := range test.results {
			c.Assert(sr.Results[i].Id.String(), gc.Equals, test.results[i].PreferredURL().String(), gc.Commentf("element %d"))
		}
	}
}

func (s *ListSuite) TestMetadataFields(c *gc.C) {
	tests := []struct {
		about string
		query string
		meta  map[string]interface{}
	}{{
		about: "archive-size",
		query: "name=mysql&include=archive-size",
		meta: map[string]interface{}{
			"archive-size": params.ArchiveSizeResponse{getListCharm("mysql").Size()},
		},
	}, {
		about: "bundle-metadata",
		query: "name=wordpress-simple&type=bundle&include=bundle-metadata",
		meta: map[string]interface{}{
			"bundle-metadata": getListBundle("wordpress-simple").Data(),
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
			"charm-actions": getListCharm("wordpress").Actions(),
		},
	}, {
		about: "charm-config",
		query: "name=wordpress&type=charm&include=charm-config",
		meta: map[string]interface{}{
			"charm-config": getListCharm("wordpress").Config(),
		},
	}, {
		about: "charm-related",
		query: "name=wordpress&type=charm&include=charm-related",
		meta: map[string]interface{}{
			"charm-related": params.RelatedResponse{
				Provides: map[string][]params.EntityResult{
					"mysql": {
						{
							Id: exportTestCharms["mysql"].PreferredURL(),
						},
					},
					"varnish": {
						{
							Id: exportTestCharms["varnish"].PreferredURL(),
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
				Provides: map[string][]params.EntityResult{
					"mysql": {
						{
							Id: exportTestCharms["mysql"].PreferredURL(),
						},
					},
					"varnish": {
						{
							Id: exportTestCharms["varnish"].PreferredURL(),
						},
					},
				},
			},
			"charm-config": getListCharm("wordpress").Config(),
		},
	}}
	for i, test := range tests {
		c.Logf("test %d. %s", i, test.about)
		rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler: s.srv,
			URL:     storeURL("list?" + test.query),
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

func (s *ListSuite) TestListIncludeError(c *gc.C) {
	// Perform a list for all charms, including the
	// manifest, which will try to retrieve all charm
	// blobs.
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("list?type=charm&include=manifest"),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	var resp params.ListResponse
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	// cs:riak will not be found because it is not visible to "everyone".
	c.Assert(resp.Results, gc.HasLen, len(exportTestCharms)-1)

	// Now remove one of the blobs. The list should still
	// work, but only return a single result.
	blobName, _, err := s.store.BlobNameAndHash(newResolvedURL("~charmers/precise/wordpress-23", 23))
	c.Assert(err, gc.IsNil)
	err = s.store.BlobStore.Remove(blobName)
	c.Assert(err, gc.IsNil)

	// Now list again - we should get one result less
	// (and the error will be logged).

	// Register a logger that so that we can check the logging output.
	// It will be automatically removed later because IsolatedMgoESSuite
	// uses LoggingSuite.
	var tw loggo.TestWriter
	err = loggo.RegisterWriter("test-log", &tw, loggo.DEBUG)
	c.Assert(err, gc.IsNil)

	rec = httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("list?type=charm&include=manifest"),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	resp = params.ListResponse{}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	// cs:riak will not be found because it is not visible to "everyone".
	// cs:wordpress will not be found because it has no manifest.
	c.Assert(resp.Results, gc.HasLen, len(exportTestCharms)-2)

	c.Assert(tw.Log(), jc.LogMatches, []string{"cannot retrieve metadata for cs:precise/wordpress-23: cannot open archive data for cs:precise/wordpress-23: .*"})
}

func (s *ListSuite) TestSortingList(c *gc.C) {
	tests := []struct {
		about   string
		query   string
		results []*router.ResolvedURL
	}{{
		about: "name ascending",
		query: "sort=name",
		results: []*router.ResolvedURL{
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
			exportTestCharms["wordpress"],
			exportTestBundles["wordpress-simple"],
		},
	}, {
		about: "name descending",
		query: "sort=-name",
		results: []*router.ResolvedURL{
			exportTestBundles["wordpress-simple"],
			exportTestCharms["wordpress"],
			exportTestCharms["varnish"],
			exportTestCharms["mysql"],
		},
	}, {
		about: "series ascending",
		query: "sort=series,name",
		results: []*router.ResolvedURL{
			exportTestBundles["wordpress-simple"],
			exportTestCharms["wordpress"],
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
		},
	}, {
		about: "series descending",
		query: "sort=-series&sort=name",
		results: []*router.ResolvedURL{
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
			exportTestCharms["wordpress"],
			exportTestBundles["wordpress-simple"],
		},
	}, {
		about: "owner ascending",
		query: "sort=owner,name",
		results: []*router.ResolvedURL{
			exportTestCharms["wordpress"],
			exportTestBundles["wordpress-simple"],
			exportTestCharms["varnish"],
			exportTestCharms["mysql"],
		},
	}, {
		about: "owner descending",
		query: "sort=-owner&sort=name",
		results: []*router.ResolvedURL{
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
			exportTestCharms["wordpress"],
			exportTestBundles["wordpress-simple"],
		},
	}}
	for i, test := range tests {
		c.Logf("test %d. %s", i, test.about)
		rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler: s.srv,
			URL:     storeURL("list?" + test.query),
		})
		var sr params.ListResponse
		err := json.Unmarshal(rec.Body.Bytes(), &sr)
		c.Assert(err, gc.IsNil)
		c.Assert(sr.Results, gc.HasLen, len(test.results), gc.Commentf("expected %#v", test.results))
		c.Logf("results: %s", rec.Body.Bytes())
		for i := range test.results {
			c.Assert(sr.Results[i].Id.String(), gc.Equals, test.results[i].PreferredURL().String(), gc.Commentf("element %d"))
		}
	}
}

func (s *ListSuite) TestSortUnsupportedListField(c *gc.C) {
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("list?sort=text"),
	})
	var e params.Error
	err := json.Unmarshal(rec.Body.Bytes(), &e)
	c.Assert(err, gc.IsNil)
	c.Assert(e.Code, gc.Equals, params.ErrBadRequest)
	c.Assert(e.Message, gc.Equals, "invalid sort field: unrecognized sort parameter \"text\"")
}

func (s *ListSuite) TestGetLatestRevisionOnly(c *gc.C) {
	id := newResolvedURL("cs:~charmers/precise/wordpress-24", 24)
	err := s.store.AddCharmWithArchive(id, getListCharm("wordpress"))
	c.Assert(err, gc.IsNil)
	err = s.store.SetPerms(&id.URL, "read", params.Everyone, id.URL.User)

	testresults := []*router.ResolvedURL{
		exportTestBundles["wordpress-simple"],
		id,
		exportTestCharms["varnish"],
		exportTestCharms["mysql"],
	}

	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("list"),
	})
	var sr params.ListResponse
	err = json.Unmarshal(rec.Body.Bytes(), &sr)
	c.Assert(err, gc.IsNil)
	c.Assert(sr.Results, gc.HasLen, 4, gc.Commentf("expected %#v", testresults))
	c.Logf("results: %s", rec.Body.Bytes())
	for i := range testresults {
		c.Assert(sr.Results[i].Id.String(), gc.Equals, testresults[i].PreferredURL().String(), gc.Commentf("element %d"))
	}

	testresults = []*router.ResolvedURL{
		exportTestCharms["mysql"],
		exportTestCharms["varnish"],
		id,
		exportTestBundles["wordpress-simple"],
	}
	rec = httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("list?sort=name"),
	})
	err = json.Unmarshal(rec.Body.Bytes(), &sr)
	c.Assert(err, gc.IsNil)
	c.Assert(sr.Results, gc.HasLen, 4, gc.Commentf("expected %#v", testresults))
	c.Logf("results: %s", rec.Body.Bytes())
	for i := range testresults {
		c.Assert(sr.Results[i].Id.String(), gc.Equals, testresults[i].PreferredURL().String(), gc.Commentf("element %d"))
	}
}

func (s *ListSuite) assertPut(c *gc.C, url string, val interface{}) {
	body, err := json.Marshal(val)
	c.Assert(err, gc.IsNil)
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL(url),
		Method:  "PUT",
		Header: http.Header{
			"Content-Type": {"application/json"},
		},
		Username: testUsername,
		Password: testPassword,
		Body:     bytes.NewReader(body),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK, gc.Commentf("headers: %v, body: %s", rec.HeaderMap, rec.Body.String()))
	c.Assert(rec.Body.String(), gc.HasLen, 0)
}

func (s *ListSuite) TestListWithAdminCredentials(c *gc.C) {
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler:  s.srv,
		URL:      storeURL("list"),
		Username: testUsername,
		Password: testPassword,
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	expected := []*router.ResolvedURL{
		exportTestCharms["mysql"],
		exportTestCharms["wordpress"],
		exportTestCharms["riak"],
		exportTestCharms["varnish"],
		exportTestBundles["wordpress-simple"],
	}
	var sr params.ListResponse
	err := json.Unmarshal(rec.Body.Bytes(), &sr)
	c.Assert(err, gc.IsNil)
	assertListResultSet(c, sr, expected)
}

func (s *ListSuite) TestListWithUserMacaroon(c *gc.C) {
	m, err := s.store.Bakery.NewMacaroon("", nil, []checkers.Caveat{
		checkers.DeclaredCaveat("username", "test-user"),
	})
	c.Assert(err, gc.IsNil)
	macaroonCookie, err := httpbakery.NewCookie(macaroon.Slice{m})
	c.Assert(err, gc.IsNil)
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("list"),
		Cookies: []*http.Cookie{macaroonCookie},
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	expected := []*router.ResolvedURL{
		exportTestCharms["mysql"],
		exportTestCharms["wordpress"],
		exportTestCharms["riak"],
		exportTestCharms["varnish"],
		exportTestBundles["wordpress-simple"],
	}
	var sr params.ListResponse
	err = json.Unmarshal(rec.Body.Bytes(), &sr)
	c.Assert(err, gc.IsNil)
	assertListResultSet(c, sr, expected)
}

func (s *ListSuite) TestSearchWithBadAdminCredentialsAndACookie(c *gc.C) {
	m, err := s.store.Bakery.NewMacaroon("", nil, []checkers.Caveat{
		checkers.DeclaredCaveat("username", "test-user"),
	})
	c.Assert(err, gc.IsNil)
	macaroonCookie, err := httpbakery.NewCookie(macaroon.Slice{m})
	c.Assert(err, gc.IsNil)
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler:  s.srv,
		URL:      storeURL("list"),
		Cookies:  []*http.Cookie{macaroonCookie},
		Username: testUsername,
		Password: "bad-password",
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	expected := []*router.ResolvedURL{
		exportTestCharms["mysql"],
		exportTestCharms["wordpress"],
		exportTestCharms["varnish"],
		exportTestBundles["wordpress-simple"],
	}
	var sr params.ListResponse
	err = json.Unmarshal(rec.Body.Bytes(), &sr)
	c.Assert(err, gc.IsNil)
	assertListResultSet(c, sr, expected)
}

func assertListResultSet(c *gc.C, sr params.ListResponse, expected []*router.ResolvedURL) {
	sort.Sort(listResultById(sr.Results))
	sort.Sort(resolvedURLByPreferredURL(expected))
	c.Assert(sr.Results, gc.HasLen, len(expected), gc.Commentf("expected %#v", expected))
	for i := range expected {
		c.Assert(sr.Results[i].Id.String(), gc.Equals, expected[i].PreferredURL().String(), gc.Commentf("element %d"))
	}
}

type listResultById []params.EntityResult

func (s listResultById) Len() int      { return len(s) }
func (s listResultById) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s listResultById) Less(i, j int) bool {
	return s[i].Id.String() < s[j].Id.String()
}
