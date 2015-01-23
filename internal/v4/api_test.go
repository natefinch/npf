// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"github.com/juju/testing/httptesting"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v4"
	"gopkg.in/macaroon-bakery.v0/bakery/checkers"
	"gopkg.in/macaroon-bakery.v0/bakerytest"
	"gopkg.in/macaroon-bakery.v0/httpbakery"
	"gopkg.in/macaroon.v1"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/elasticsearch"
	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/storetesting"
	"github.com/juju/charmstore/internal/v4"
	"github.com/juju/charmstore/params"
)

var serverParams = charmstore.ServerParams{
	AuthUsername: "test-user",
	AuthPassword: "test-password",
}

var es *elasticsearch.Database = &elasticsearch.Database{"localhost:9200"}
var si *charmstore.SearchIndex = &charmstore.SearchIndex{
	Database: es,
	Index:    "cs",
}

type APISuite struct {
	storetesting.IsolatedMgoSuite
	srv      http.Handler
	store    *charmstore.Store
	srv_es   http.Handler
	store_es *charmstore.Store
}

var _ = gc.Suite(&APISuite{})

// patchLegacyDownloadCountsEnabled sets LegacyDownloadCountsEnabled to the
// given value for the duration of the test.
// TODO (frankban): remove this function when removing the legacy counts logic.
func patchLegacyDownloadCountsEnabled(addCleanup func(jujutesting.CleanupFunc), value bool) {
	original := charmstore.LegacyDownloadCountsEnabled
	charmstore.LegacyDownloadCountsEnabled = value
	addCleanup(func(*gc.C) {
		charmstore.LegacyDownloadCountsEnabled = original
	})
}

func (s *APISuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	s.srv, s.store = newServer(c, s.Session, nil, serverParams)
	s.srv_es, s.store_es = newServer(c, s.Session, si, serverParams)
}

func newServer(c *gc.C, session *mgo.Session, si *charmstore.SearchIndex, config charmstore.ServerParams) (http.Handler, *charmstore.Store) {
	db := session.DB("charmstore")
	store, err := charmstore.NewStore(db, si, nil)
	c.Assert(err, gc.IsNil)
	srv, err := charmstore.NewServer(db, si, config, map[string]charmstore.NewAPIHandlerFunc{"v4": v4.NewAPIHandler})
	c.Assert(err, gc.IsNil)
	return srv, store
}

func storeURL(path string) string {
	return "/v4/" + path
}

type metaEndpointExpectedValueGetter func(*charmstore.Store, *charm.Reference) (interface{}, error)

type metaEndpoint struct {
	// name names the meta endpoint.
	name string

	// exclusive specifies whether the endpoint is
	// valid for charms only (charmOnly), bundles only (bundleOnly)
	// or to both (zero).
	exclusive int

	// get returns the expected data for the endpoint.
	get metaEndpointExpectedValueGetter

	// checkURL holds one URL to sanity check data against.
	checkURL string

	// assertCheckData holds a function that will be used to check that
	// the get function returns sane data for checkURL.
	assertCheckData func(c *gc.C, data interface{})
}

const (
	charmOnly = iota + 1
	bundleOnly
)

var metaEndpoints = []metaEndpoint{{
	name:      "charm-config",
	exclusive: charmOnly,
	get:       entityFieldGetter("CharmConfig"),
	checkURL:  "cs:precise/wordpress-23",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data.(*charm.Config).Options["blog-title"].Default, gc.Equals, "My Title")
	},
}, {
	name:      "charm-metadata",
	exclusive: charmOnly,
	get:       entityFieldGetter("CharmMeta"),
	checkURL:  "cs:precise/wordpress-23",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data.(*charm.Meta).Summary, gc.Equals, "Blog engine")
	},
}, {
	name:      "bundle-metadata",
	exclusive: bundleOnly,
	get:       entityFieldGetter("BundleData"),
	checkURL:  "cs:bundle/wordpress-simple-42",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data.(*charm.BundleData).Services["wordpress"].Charm, gc.Equals, "wordpress")
	},
}, {
	name:      "bundle-unit-count",
	exclusive: bundleOnly,
	get: entityGetter(func(entity *mongodoc.Entity) interface{} {
		if entity.BundleData == nil {
			return nil
		}
		return params.BundleCount{*entity.BundleUnitCount}
	}),
	checkURL: "cs:bundle/wordpress-simple-42",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data.(params.BundleCount).Count, gc.Equals, 2)
	},
}, {
	name:      "bundle-machine-count",
	exclusive: bundleOnly,
	get: entityGetter(func(entity *mongodoc.Entity) interface{} {
		if entity.BundleData == nil {
			return nil
		}
		return params.BundleCount{*entity.BundleMachineCount}
	}),
	checkURL: "cs:bundle/wordpress-simple-42",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data.(params.BundleCount).Count, gc.Equals, 2)
	},
}, {
	name:      "charm-actions",
	exclusive: charmOnly,
	get:       entityFieldGetter("CharmActions"),
	checkURL:  "cs:precise/dummy-10",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data.(*charm.Actions).ActionSpecs["snapshot"].Description, gc.Equals, "Take a snapshot of the database.")
	},
}, {
	name: "archive-size",
	get: entityGetter(func(entity *mongodoc.Entity) interface{} {
		return &params.ArchiveSizeResponse{
			Size: entity.Size,
		}
	}),
	checkURL:        "cs:precise/wordpress-23",
	assertCheckData: entitySizeChecker,
}, {
	name: "manifest",
	get: zipGetter(func(r *zip.Reader) interface{} {
		var manifest []params.ManifestFile
		for _, file := range r.File {
			if strings.HasSuffix(file.Name, "/") {
				continue
			}
			manifest = append(manifest, params.ManifestFile{
				Name: file.Name,
				Size: int64(file.UncompressedSize64),
			})
		}
		return manifest
	}),
	checkURL: "cs:bundle/wordpress-simple-42",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data.([]params.ManifestFile), gc.Not(gc.HasLen), 0)
	},
}, {
	name: "archive-upload-time",
	get: entityGetter(func(entity *mongodoc.Entity) interface{} {
		return &params.ArchiveUploadTimeResponse{
			UploadTime: entity.UploadTime.UTC(),
		}
	}),
	checkURL: "cs:precise/wordpress-23",
	assertCheckData: func(c *gc.C, data interface{}) {
		response := data.(*params.ArchiveUploadTimeResponse)
		c.Assert(response.UploadTime, gc.Not(jc.Satisfies), time.Time.IsZero)
		c.Assert(response.UploadTime.Location(), gc.Equals, time.UTC)
	},
}, {
	name: "revision-info",
	get: func(store *charmstore.Store, id *charm.Reference) (interface{}, error) {
		return params.RevisionInfoResponse{
			[]*charm.Reference{id},
		}, nil
	},
	checkURL: "cs:precise/wordpress-99",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.DeepEquals, params.RevisionInfoResponse{
			[]*charm.Reference{
				charm.MustParseReference("cs:precise/wordpress-99"),
			}})
	},
}, {
	name:      "charm-related",
	exclusive: charmOnly,
	get: func(store *charmstore.Store, url *charm.Reference) (interface{}, error) {
		// The charms we use for those tests are not related each other.
		// Charm relations are independently tested in relations_test.go.
		if url.Series == "bundle" {
			return nil, nil
		}
		return &params.RelatedResponse{}, nil
	},
	checkURL: "cs:precise/wordpress-23",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.FitsTypeOf, (*params.RelatedResponse)(nil))
	},
}, {
	name:      "bundles-containing",
	exclusive: charmOnly,
	get: func(store *charmstore.Store, url *charm.Reference) (interface{}, error) {
		// The charms we use for those tests are not included in any bundle.
		// Charm/bundle relations are tested in relations_test.go.
		if url.Series == "bundle" {
			return nil, nil
		}
		return []*params.MetaAnyResponse{}, nil
	},
	checkURL: "cs:precise/wordpress-23",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.FitsTypeOf, []*params.MetaAnyResponse(nil))
	},
}, {
	name: "stats",
	get: func(store *charmstore.Store, url *charm.Reference) (interface{}, error) {
		// The entities used for those tests were never downloaded.
		return &params.StatsResponse{
			ArchiveDownloadCount: 0,
		}, nil
	},
	checkURL: "cs:precise/wordpress-23",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.FitsTypeOf, (*params.StatsResponse)(nil))
	},
}, {
	name: "extra-info",
	get: func(store *charmstore.Store, url *charm.Reference) (interface{}, error) {
		return map[string]string{
			"key": "value " + url.String(),
		}, nil
	},
	checkURL: "cs:precise/wordpress-23",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.DeepEquals, map[string]string{
			"key": "value cs:precise/wordpress-23",
		})
	},
}, {
	name: "extra-info/key",
	get: func(store *charmstore.Store, url *charm.Reference) (interface{}, error) {
		return "value " + url.String(), nil
	},
	checkURL: "cs:precise/wordpress-23",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.Equals, "value cs:precise/wordpress-23")
	},
}, {
	name: "perm",
	get: func(store *charmstore.Store, url *charm.Reference) (interface{}, error) {
		e, err := store.FindBaseEntity(url)
		if err != nil {
			return nil, err
		}
		return params.PermResponse{
			Read: e.ACLs.Read,
		}, nil
	},
	checkURL: "cs:~bob/utopic/wordpress-2",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.DeepEquals, params.PermResponse{
			Read: []string{params.Everyone, "bob"},
		})
	},
}, {
	name: "perm/read",
	get: func(store *charmstore.Store, url *charm.Reference) (interface{}, error) {
		e, err := store.FindBaseEntity(url)
		if err != nil {
			return nil, err
		}
		return e.ACLs.Read, nil
	},
	checkURL: "cs:~bob/utopic/wordpress-2",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.DeepEquals, []string{params.Everyone, "bob"})
	},
}, {
	name: "tags",
	get: entityGetter(func(entity *mongodoc.Entity) interface{} {
		if entity.URL.Series == "bundle" {
			return params.TagsResponse{entity.BundleData.Tags}
		}
		if len(entity.CharmMeta.Tags) > 0 {
			return params.TagsResponse{entity.CharmMeta.Tags}
		}
		return params.TagsResponse{entity.CharmMeta.Categories}
	}),
	checkURL: "cs:utopic/category-2",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, jc.DeepEquals, params.TagsResponse{
			Tags: []string{"openstack", "storage"},
		})
	},
}, {
	name: "id-user",
	get: func(store *charmstore.Store, url *charm.Reference) (interface{}, error) {
		return params.IdUserResponse{url.User}, nil
	},
	checkURL: "cs:~bob/utopic/wordpress-2",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.Equals, params.IdUserResponse{"bob"})
	},
}, {
	name: "id-series",
	get: func(store *charmstore.Store, url *charm.Reference) (interface{}, error) {
		return params.IdSeriesResponse{url.Series}, nil
	},
	checkURL: "cs:utopic/category-2",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.Equals, params.IdSeriesResponse{"utopic"})
	},
}, {
	name: "id-name",
	get: func(store *charmstore.Store, url *charm.Reference) (interface{}, error) {
		return params.IdNameResponse{url.Name}, nil
	},
	checkURL: "cs:utopic/category-2",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.Equals, params.IdNameResponse{"category"})
	},
}, {
	name: "id-revision",
	get: func(store *charmstore.Store, url *charm.Reference) (interface{}, error) {
		return params.IdRevisionResponse{url.Revision}, nil
	},
	checkURL: "cs:utopic/category-2",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.Equals, params.IdRevisionResponse{2})
	},
}, {
	name: "id",
	get: func(store *charmstore.Store, url *charm.Reference) (interface{}, error) {
		return params.IdResponse{
			Id:       url,
			User:     url.User,
			Series:   url.Series,
			Name:     url.Name,
			Revision: url.Revision,
		}, nil
	},
	checkURL: "cs:utopic/category-2",
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, jc.DeepEquals, params.IdResponse{
			Id:       charm.MustParseReference("cs:utopic/category-2"),
			Series:   "utopic",
			Name:     "category",
			Revision: 2,
		})
	},
}}

// TestEndpointGet tries to ensure that the endpoint
// test data getters correspond with reality.
func (s *APISuite) TestEndpointGet(c *gc.C) {
	s.addTestEntities(c)
	for i, ep := range metaEndpoints {
		c.Logf("test %d: %s\n", i, ep.name)
		data, err := ep.get(s.store, charm.MustParseReference(ep.checkURL))
		c.Assert(err, gc.IsNil)
		ep.assertCheckData(c, data)
	}
}

func (s *APISuite) TestAllMetaEndpointsTested(c *gc.C) {
	// Make sure that we're testing all the metadata
	// endpoints that we need to.
	s.addCharm(c, "wordpress", "precise/wordpress-23")
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("precise/wordpress-23/meta"),
	})
	c.Logf("meta response body: %s", rec.Body)
	var list []string
	err := json.Unmarshal(rec.Body.Bytes(), &list)
	c.Assert(err, gc.IsNil)

	listNames := make(map[string]bool)
	for _, name := range list {
		c.Assert(listNames[name], gc.Equals, false, gc.Commentf("name %s", name))
		listNames[name] = true
	}

	testNames := make(map[string]bool)
	for _, test := range metaEndpoints {
		if strings.Contains(test.name, "/") {
			continue
		}
		testNames[test.name] = true
	}
	c.Assert(testNames, jc.DeepEquals, listNames)
}

var testEntities = []string{
	// A stock charm.
	"cs:precise/wordpress-23",
	// A stock bundle.
	"cs:bundle/wordpress-simple-42",
	// A charm with some actions.
	"cs:precise/dummy-10",
	// A charm with some tags.
	"cs:utopic/category-2",
	// A charm with a user.
	"cs:~bob/utopic/wordpress-2",
}

func (s *APISuite) addTestEntities(c *gc.C) []*charm.Reference {
	urls := make([]*charm.Reference, len(testEntities))
	for i, e := range testEntities {
		url := charm.MustParseReference(e)
		if url.Series == "bundle" {
			s.addBundle(c, url.Name, e)
		} else {
			s.addCharm(c, url.Name, e)
		}
		// Associate some extra-info data with the entity.
		key := url.Path() + "/meta/extra-info/key"
		s.assertPut(c, key, "value "+e)
		urls[i] = url
	}
	return urls
}

func (s *APISuite) TestMetaEndpointsSingle(c *gc.C) {
	urls := s.addTestEntities(c)
	for i, ep := range metaEndpoints {
		c.Logf("test %d. %s", i, ep.name)
		tested := false
		for _, url := range urls {
			charmId := strings.TrimPrefix(url.String(), "cs:")
			path := charmId + "/meta/" + ep.name
			expectData, err := ep.get(s.store, url)
			c.Assert(err, gc.IsNil)
			c.Logf("	expected data for %q: %#v", url, expectData)
			if isNull(expectData) {
				httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
					Handler:      s.srv,
					URL:          storeURL(path),
					ExpectStatus: http.StatusNotFound,
					ExpectBody: params.Error{
						Message: params.ErrMetadataNotFound.Error(),
						Code:    params.ErrMetadataNotFound,
					},
				})
				continue
			}
			tested = true
			s.assertGet(c, path, expectData)
		}
		if !tested {
			c.Errorf("endpoint %q is null for all endpoints, so is not properly tested", ep.name)
		}
	}
}

func (s *APISuite) TestMetaPerm(c *gc.C) {
	// Create a charm store server that will use the test third party for
	// its third party caveat.
	discharger := bakerytest.NewDischarger(nil, func(cond string, arg string) ([]checkers.Caveat, error) {
		return []checkers.Caveat{checkers.DeclaredCaveat("username", "bob")}, nil
	})
	srv, store, discharger := newServerWithDischarger(c, s.Session, "bob", "")
	defer discharger.Close()
	cookies := []*http.Cookie{dischargedAuthCookie(c, srv)}
	s.srv, s.store = srv, store

	s.addCharm(c, "wordpress", "precise/wordpress-23")
	s.addCharm(c, "wordpress", "precise/wordpress-24")
	s.addCharm(c, "wordpress", "trusty/wordpress-1")
	s.assertGet(c, "wordpress/meta/perm", params.PermResponse{
		Read: []string{params.Everyone},
	})
	e, err := s.store.FindBaseEntity(charm.MustParseReference("precise/wordpress-23"))
	c.Assert(err, gc.IsNil)
	c.Assert(e.Public, jc.IsTrue)
	c.Assert(e.ACLs.Read, gc.DeepEquals, []string{params.Everyone})

	// Check that PUT on wordpress/meta/perm returns error.
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler: s.srv,
		URL:     storeURL("precise/wordpress-23/meta/perm"),
		Method:  "PUT",
		Header: http.Header{
			"Content-Type": {"application/json"},
		},
		Body:         strings.NewReader(`"something"`),
		Username:     serverParams.AuthUsername,
		Password:     serverParams.AuthPassword,
		ExpectStatus: http.StatusInternalServerError,
		ExpectBody: params.Error{
			Message: "PUT not supported",
		},
	})

	// Change the perms to only include a specific user.
	s.assertPut(c, "precise/wordpress-23/meta/perm/read", []string{"bob"})

	// Check that the perms have changed for all revisions and series.
	for i, u := range []string{"precise/wordpress-23", "precise/wordpress-24", "trusty/wordpress-1"} {
		c.Logf("id %d: %q", i, u)
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler: s.srv,
			URL:     storeURL(u + "/meta/perm"),
			Cookies: cookies,
			ExpectBody: params.PermResponse{
				Read: []string{"bob"},
			},
		})
	}
	e, err = s.store.FindBaseEntity(charm.MustParseReference("precise/wordpress-23"))
	c.Assert(err, gc.IsNil)
	c.Assert(e.Public, jc.IsFalse)
	c.Assert(e.ACLs.Read, gc.DeepEquals, []string{"bob"})

	// Try restoring everyone's permission.
	s.assertPut(c, "wordpress/meta/perm/read", []string{"bob", params.Everyone})
	s.assertGet(c, "wordpress/meta/perm", params.PermResponse{
		Read: []string{"bob", params.Everyone},
	})
	s.assertGet(c, "wordpress/meta/perm/read", []string{"bob", params.Everyone})
	e, err = s.store.FindBaseEntity(charm.MustParseReference("precise/wordpress-23"))
	c.Assert(err, gc.IsNil)
	c.Assert(e.Public, jc.IsTrue)
	c.Assert(e.ACLs.Read, gc.DeepEquals, []string{"bob", params.Everyone})

	// Try deleting all permissions.
	s.assertPut(c, "wordpress/meta/perm/read", []string{})
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("wordpress/meta/perm"),
		Cookies:      cookies,
		ExpectStatus: http.StatusUnauthorized,
		ExpectBody: params.Error{
			Code:    params.ErrUnauthorized,
			Message: `unauthorized: access denied for user "bob"`,
		},
	})
	e, err = s.store.FindBaseEntity(charm.MustParseReference("precise/wordpress-23"))
	c.Assert(err, gc.IsNil)
	c.Assert(e.Public, jc.IsFalse)
	c.Assert(e.ACLs.Read, gc.DeepEquals, []string{})
}

func (s *APISuite) TestMetaPermPutUnauthorized(c *gc.C) {
	s.addCharm(c, "wordpress", "utopic/wordpress-42")
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler: s.srv,
		URL:     storeURL("precise/wordpress-23/meta/perm/read"),
		Method:  "PUT",
		Header: http.Header{
			"Content-Type": {"application/json"},
		},
		Body:         strings.NewReader(`["some-user"]`),
		ExpectStatus: http.StatusUnauthorized,
		ExpectBody: params.Error{
			Code:    params.ErrUnauthorized,
			Message: "authentication failed: missing HTTP auth header",
		},
	})
}

func (s *APISuite) TestExtraInfo(c *gc.C) {
	id := "precise/wordpress-23"
	s.addCharm(c, "wordpress", id)

	// Add one value and check that it's there.
	s.assertPut(c, id+"/meta/extra-info/foo", "fooval")
	s.assertGet(c, id+"/meta/extra-info/foo", "fooval")
	s.assertGet(c, id+"/meta/extra-info", map[string]string{
		"foo": "fooval",
	})

	// Add another value and check that both values are there.
	s.assertPut(c, id+"/meta/extra-info/bar", "barval")
	s.assertGet(c, id+"/meta/extra-info/bar", "barval")
	s.assertGet(c, id+"/meta/extra-info", map[string]string{
		"foo": "fooval",
		"bar": "barval",
	})

	// Overwrite a value and check that it's changed.
	s.assertPut(c, id+"/meta/extra-info/foo", "fooval2")
	s.assertGet(c, id+"/meta/extra-info/foo", "fooval2")
	s.assertGet(c, id+"/meta/extra-info", map[string]string{
		"foo": "fooval2",
		"bar": "barval",
	})

	// Write several values at once.
	s.assertPut(c, id+"/meta/any", params.MetaAnyResponse{
		Meta: map[string]interface{}{
			"extra-info": map[string]string{
				"foo": "fooval3",
				"baz": "bazval",
			},
			"extra-info/frob": []int{1, 4, 6},
		},
	})
	s.assertGet(c, id+"/meta/extra-info", map[string]interface{}{
		"foo":  "fooval3",
		"baz":  "bazval",
		"bar":  "barval",
		"frob": []int{1, 4, 6},
	})
}

var extraInfoBadPutRequestsTests = []struct {
	about        string
	path         string
	body         interface{}
	contentType  string
	expectStatus int
	expectBody   params.Error
}{{
	about:        "key with extra element",
	path:         "precise/wordpress-23/meta/extra-info/foo/bar",
	body:         "hello",
	expectStatus: http.StatusBadRequest,
	expectBody: params.Error{
		Code:    params.ErrBadRequest,
		Message: "bad key for extra-info",
	},
}, {
	about:        "key with a dot",
	path:         "precise/wordpress-23/meta/extra-info/foo.bar",
	body:         "hello",
	expectStatus: http.StatusBadRequest,
	expectBody: params.Error{
		Code:    params.ErrBadRequest,
		Message: "bad key for extra-info",
	},
}, {
	about:        "key with a dollar",
	path:         "precise/wordpress-23/meta/extra-info/foo$bar",
	body:         "hello",
	expectStatus: http.StatusBadRequest,
	expectBody: params.Error{
		Code:    params.ErrBadRequest,
		Message: "bad key for extra-info",
	},
}, {
	about: "multi key with extra element",
	path:  "precise/wordpress-23/meta/extra-info",
	body: map[string]string{
		"foo/bar": "value",
	},
	expectStatus: http.StatusBadRequest,
	expectBody: params.Error{
		Code:    params.ErrBadRequest,
		Message: "bad key for extra-info",
	},
}, {
	about: "multi key with dot",
	path:  "precise/wordpress-23/meta/extra-info",
	body: map[string]string{
		".bar": "value",
	},
	expectStatus: http.StatusBadRequest,
	expectBody: params.Error{
		Code:    params.ErrBadRequest,
		Message: "bad key for extra-info",
	},
}, {
	about: "multi key with dollar",
	path:  "precise/wordpress-23/meta/extra-info",
	body: map[string]string{
		"$bar": "value",
	},
	expectStatus: http.StatusBadRequest,
	expectBody: params.Error{
		Code:    params.ErrBadRequest,
		Message: "bad key for extra-info",
	},
}, {
	about:        "multi key with bad map",
	path:         "precise/wordpress-23/meta/extra-info",
	body:         "bad",
	expectStatus: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: `cannot unmarshal extra info body: json: cannot unmarshal string into Go value of type map[string]*json.RawMessage`,
	},
}}

func (s *APISuite) TestExtraInfoBadPutRequests(c *gc.C) {
	s.addCharm(c, "wordpress", "cs:precise/wordpress-23")
	for i, test := range extraInfoBadPutRequestsTests {
		c.Logf("test %d: %s", i, test.about)
		contentType := test.contentType
		if contentType == "" {
			contentType = "application/json"
		}
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler: s.srv,
			URL:     storeURL(test.path),
			Method:  "PUT",
			Header: http.Header{
				"Content-Type": {contentType},
			},
			Username:     serverParams.AuthUsername,
			Password:     serverParams.AuthPassword,
			Body:         strings.NewReader(mustMarshalJSON(test.body)),
			ExpectStatus: test.expectStatus,
			ExpectBody:   test.expectBody,
		})
	}
}

func (s *APISuite) TestExtraInfoPutUnauthorized(c *gc.C) {
	s.addCharm(c, "wordpress", "cs:precise/wordpress-23")
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler: s.srv,
		URL:     storeURL("precise/wordpress-23/meta/extra-info"),
		Method:  "PUT",
		Header: http.Header{
			"Content-Type": {"application/json"},
		},
		Body: strings.NewReader(mustMarshalJSON(map[string]string{
			"bar": "value",
		})),
		ExpectStatus: http.StatusUnauthorized,
		ExpectBody: params.Error{
			Code:    params.ErrUnauthorized,
			Message: "authentication failed: missing HTTP auth header",
		},
	})
}

func isNull(v interface{}) bool {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(data) == "null"
}

func (s *APISuite) TestMetaEndpointsAny(c *gc.C) {
	urls := s.addTestEntities(c)
	for _, url := range urls {
		charmId := strings.TrimPrefix(url.String(), "cs:")
		var flags []string
		expectData := params.MetaAnyResponse{
			Id:   url,
			Meta: make(map[string]interface{}),
		}
		for _, ep := range metaEndpoints {
			flags = append(flags, "include="+ep.name)
			isBundle := url.Series == "bundle"
			if ep.exclusive != 0 && isBundle != (ep.exclusive == bundleOnly) {
				// endpoint not relevant.
				continue
			}
			val, err := ep.get(s.store, url)
			c.Assert(err, gc.IsNil)
			if val != nil {
				expectData.Meta[ep.name] = val
			}
		}
		s.assertGet(c, charmId+"/meta/any?"+strings.Join(flags, "&"), expectData)
	}
}

func (s *APISuite) TestMetaAnyWithNoIncludesAndNoEntity(c *gc.C) {
	wordpressURL, _ := s.addCharm(c, "wordpress", "cs:precise/wordpress-23")
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("precise/wordpress-1/meta/any"),
		ExpectStatus: http.StatusNotFound,
		ExpectBody: params.Error{
			Code:    params.ErrNotFound,
			Message: "not found",
		},
	})
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("meta/any?id=precise/wordpress-23&id=precise/wordpress-1"),
		ExpectStatus: http.StatusOK,
		ExpectBody: map[string]interface{}{
			"precise/wordpress-23": params.MetaAnyResponse{
				Id: wordpressURL,
			},
		},
	})
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("precise/wordpress-23/meta/any"),
		ExpectStatus: http.StatusOK,
		ExpectBody: params.MetaAnyResponse{
			Id: wordpressURL,
		},
	})
}

// In this test we rely on the charm.v2 testing repo package and
// dummy charm that has actions included.
func (s *APISuite) TestMetaCharmActions(c *gc.C) {
	url, dummy := s.addCharm(c, "dummy", "cs:precise/dummy-10")
	s.assertGet(c, "precise/dummy-10/meta/charm-actions", dummy.Actions())
	s.assertGet(c, "precise/dummy-10/meta/any?include=charm-actions",
		params.MetaAnyResponse{
			Id: url,
			Meta: map[string]interface{}{
				"charm-actions": dummy.Actions(),
			},
		},
	)
}

func (s *APISuite) TestBulkMeta(c *gc.C) {
	// We choose an arbitrary set of ids and metadata here, just to smoke-test
	// whether the meta/any logic is hooked up correctly.
	// Detailed tests for this feature are in the router package.

	_, wordpress := s.addCharm(c, "wordpress", "cs:precise/wordpress-23")
	_, mysql := s.addCharm(c, "mysql", "cs:precise/mysql-10")
	s.assertGet(c,
		"meta/charm-metadata?id=precise/wordpress-23&id=precise/mysql-10",
		map[string]*charm.Meta{
			"precise/wordpress-23": wordpress.Meta(),
			"precise/mysql-10":     mysql.Meta(),
		},
	)
}

func (s *APISuite) TestBulkMetaAny(c *gc.C) {
	// We choose an arbitrary set of metadata here, just to smoke-test
	// whether the meta/any logic is hooked up correctly.
	// Detailed tests for this feature are in the router package.

	wordpressURL, wordpress := s.addCharm(c, "wordpress", "cs:precise/wordpress-23")
	mysqlURL, mysql := s.addCharm(c, "mysql", "cs:precise/mysql-10")
	s.assertGet(c,
		"meta/any?include=charm-metadata&include=charm-config&id=precise/wordpress-23&id=precise/mysql-10",
		map[string]params.MetaAnyResponse{
			"precise/wordpress-23": {
				Id: wordpressURL,
				Meta: map[string]interface{}{
					"charm-config":   wordpress.Config(),
					"charm-metadata": wordpress.Meta(),
				},
			},
			"precise/mysql-10": {
				Id: mysqlURL,
				Meta: map[string]interface{}{
					"charm-config":   mysql.Config(),
					"charm-metadata": mysql.Meta(),
				},
			},
		},
	)
}

var metaCharmTagsTests = []struct {
	about      string
	tags       []string
	categories []string
	expectTags []string
}{{
	about:      "tags only",
	tags:       []string{"foo", "bar"},
	expectTags: []string{"foo", "bar"},
}, {
	about:      "categories only",
	categories: []string{"foo", "bar"},
	expectTags: []string{"foo", "bar"},
}, {
	about:      "tags and categories",
	categories: []string{"foo", "bar"},
	tags:       []string{"tag1", "tag2"},
	expectTags: []string{"tag1", "tag2"},
}, {
	about: "no tags or categories",
}}

func (s *APISuite) TestMetaCharmTags(c *gc.C) {
	url := charm.MustParseReference("precise/wordpress-0")
	for i, test := range metaCharmTagsTests {
		c.Logf("%d: %s", i, test.about)
		wordpress := storetesting.Charms.CharmDir("wordpress")
		meta := wordpress.Meta()
		meta.Tags, meta.Categories = test.tags, test.categories
		url.Revision = i
		err := s.store.AddCharm(&testMetaCharm{
			meta:  meta,
			Charm: wordpress,
		}, charmstore.AddParams{
			URL:      url,
			BlobName: "no-such-name",
			BlobHash: fakeBlobHash,
			BlobSize: fakeBlobSize,
		})
		c.Assert(err, gc.IsNil)
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      s.srv,
			URL:          storeURL(url.Path() + "/meta/tags"),
			ExpectStatus: http.StatusOK,
			ExpectBody:   params.TagsResponse{test.expectTags},
		})
	}
}

func (s *APISuite) TestBundleTags(c *gc.C) {
	b := storetesting.Charms.BundleDir("wordpress-simple")
	data := b.Data()
	data.Tags = []string{"foo", "bar"}
	err := s.store.AddBundle(&testingBundle{data}, charmstore.AddParams{
		URL:      charm.MustParseReference("bundle/wordpress-2"),
		BlobName: "no-such-name",
		BlobHash: fakeBlobHash,
		BlobSize: fakeBlobSize,
	})
	c.Assert(err, gc.IsNil)
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("bundle/wordpress-2/meta/tags"),
		ExpectStatus: http.StatusOK,
		ExpectBody:   params.TagsResponse{[]string{"foo", "bar"}},
	})
}

type testMetaCharm struct {
	meta *charm.Meta
	charm.Charm
}

func (c *testMetaCharm) Meta() *charm.Meta {
	return c.meta
}

func (s *APISuite) TestIdsAreResolved(c *gc.C) {
	// This is just testing that ResolveURL is actually
	// passed to the router. Given how Router is
	// defined, and the ResolveURL tests, this should
	// be sufficient to "join the dots".
	_, wordpress := s.addCharm(c, "wordpress", "cs:precise/wordpress-23")
	s.assertGet(c, "wordpress/meta/charm-metadata", wordpress.Meta())
}

func (s *APISuite) TestMetaCharmNotFound(c *gc.C) {
	for i, ep := range metaEndpoints {
		c.Logf("test %d: %s", i, ep.name)
		expected := params.Error{
			Message: "no matching charm or bundle for cs:precise/wordpress-23",
			Code:    params.ErrNotFound,
		}
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      s.srv,
			URL:          storeURL("precise/wordpress-23/meta/" + ep.name),
			ExpectStatus: http.StatusNotFound,
			ExpectBody:   expected,
		})
		expected.Message = `no matching charm or bundle for "cs:wordpress"`
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      s.srv,
			URL:          storeURL("wordpress/meta/" + ep.name),
			ExpectStatus: http.StatusNotFound,
			ExpectBody:   expected,
		})
	}
}

var resolveURLTests = []struct {
	url      string
	expect   string
	notFound bool
}{{
	url:    "wordpress",
	expect: "cs:trusty/wordpress-25",
}, {
	url:    "precise/wordpress",
	expect: "cs:precise/wordpress-24",
}, {
	url:    "utopic/bigdata",
	expect: "cs:utopic/bigdata-10",
}, {
	url:    "bigdata",
	expect: "cs:utopic/bigdata-10",
}, {
	url:    "wordpress-24",
	expect: "cs:trusty/wordpress-24",
}, {
	url:    "bundlelovin",
	expect: "cs:bundle/bundlelovin-10",
}, {
	url:      "wordpress-26",
	notFound: true,
}, {
	url:      "foo",
	notFound: true,
}, {
	url:      "trusty/bigdata",
	notFound: true,
}}

func (s *APISuite) TestResolveURL(c *gc.C) {
	s.addCharm(c, "wordpress", "cs:precise/wordpress-23")
	s.addCharm(c, "wordpress", "cs:precise/wordpress-24")
	s.addCharm(c, "wordpress", "cs:trusty/wordpress-24")
	s.addCharm(c, "wordpress", "cs:trusty/wordpress-25")
	s.addCharm(c, "wordpress", "cs:utopic/wordpress-10")
	s.addCharm(c, "wordpress", "cs:saucy/bigdata-99")
	s.addCharm(c, "wordpress", "cs:utopic/bigdata-10")
	s.addBundle(c, "wordpress-simple", "cs:bundle/bundlelovin-10")
	s.addBundle(c, "wordpress-simple", "cs:bundle/wordpress-simple-10")

	for i, test := range resolveURLTests {
		c.Logf("test %d: %s", i, test.url)
		url := charm.MustParseReference(test.url)
		err := v4.ResolveURL(s.store, url)
		if test.notFound {
			c.Assert(errgo.Cause(err), gc.Equals, params.ErrNotFound)
			c.Assert(err, gc.ErrorMatches, `no matching charm or bundle for ".*"`)
			continue
		}
		c.Assert(err, gc.IsNil)
		c.Assert(url.String(), gc.Equals, test.expect)
	}
}

var serveExpandIdTests = []struct {
	about  string
	url    string
	expect []params.ExpandedId
	err    string
}{{
	about: "fully qualified URL",
	url:   "trusty/wordpress-42",
	expect: []params.ExpandedId{
		{Id: "cs:utopic/wordpress-42"},
		{Id: "cs:trusty/wordpress-47"},
	},
}, {
	about: "fully qualified URL that does not exist",
	url:   "trusty/wordpress-99",
	expect: []params.ExpandedId{
		{Id: "cs:utopic/wordpress-42"},
		{Id: "cs:trusty/wordpress-47"},
	},
}, {
	about: "partial URL",
	url:   "haproxy",
	expect: []params.ExpandedId{
		{Id: "cs:precise/haproxy-1"},
		{Id: "cs:trusty/haproxy-1"},
	},
}, {
	about: "single result",
	url:   "mongo-0",
	expect: []params.ExpandedId{
		{Id: "cs:bundle/mongo-0"},
	},
}, {
	about: "fully qualified URL with no entities found",
	url:   "precise/no-such-42",
	err:   `no matching charm or bundle for "cs:no-such"`,
}, {
	about: "partial URL with no entities found",
	url:   "no-such",
	err:   `no matching charm or bundle for "cs:no-such"`,
}}

func (s *APISuite) TestServeExpandId(c *gc.C) {
	// Add a bunch of entities in the database.
	// Note that expand-id only cares about entity identifiers,
	// so it is ok to reuse the same charm for all the entities.
	// Also here we assume Mongo returns the entities in natural order.
	s.addCharm(c, "wordpress", "cs:utopic/wordpress-42")
	s.addCharm(c, "wordpress", "cs:trusty/wordpress-47")
	s.addCharm(c, "wordpress", "cs:precise/haproxy-1")
	s.addCharm(c, "wordpress", "cs:trusty/haproxy-1")
	s.addBundle(c, "wordpress-simple", "cs:bundle/mongo-0")
	s.addBundle(c, "wordpress-simple", "cs:bundle/wordpress-simple-0")

	for i, test := range serveExpandIdTests {
		c.Logf("test %d: %s", i, test.about)
		storeURL := storeURL(test.url + "/expand-id")
		var expectStatus int
		var expectBody interface{}
		if test.err == "" {
			expectStatus = http.StatusOK
			expectBody = test.expect
		} else {
			expectStatus = http.StatusNotFound
			expectBody = params.Error{
				Code:    params.ErrNotFound,
				Message: test.err,
			}
		}
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      s.srv,
			URL:          storeURL,
			ExpectStatus: expectStatus,
			ExpectBody:   expectBody,
		})
	}
}

var serveMetaRevisionInfoTests = []struct {
	about  string
	url    string
	expect params.RevisionInfoResponse
	err    string
}{{
	about: "fully qualified url",
	url:   "trusty/wordpress-42",
	expect: params.RevisionInfoResponse{
		[]*charm.Reference{
			charm.MustParseReference("cs:trusty/wordpress-43"),
			charm.MustParseReference("cs:trusty/wordpress-42"),
			charm.MustParseReference("cs:trusty/wordpress-41"),
			charm.MustParseReference("cs:trusty/wordpress-9"),
		}},
}, {
	about: "partial url uses a default series",
	url:   "wordpress",
	expect: params.RevisionInfoResponse{
		[]*charm.Reference{
			charm.MustParseReference("cs:trusty/wordpress-43"),
			charm.MustParseReference("cs:trusty/wordpress-42"),
			charm.MustParseReference("cs:trusty/wordpress-41"),
			charm.MustParseReference("cs:trusty/wordpress-9"),
		}},
}, {
	about: "no entities found",
	url:   "precise/no-such-33",
	err:   "no matching charm or bundle for cs:precise/no-such-33",
}}

func (s *APISuite) TestServeMetaRevisionInfo(c *gc.C) {
	s.addCharm(c, "wordpress", "cs:trusty/mysql-42")
	s.addCharm(c, "wordpress", "cs:trusty/mysql-41")
	s.addCharm(c, "wordpress", "cs:precise/wordpress-42")
	s.addCharm(c, "wordpress", "cs:trusty/wordpress-43")
	s.addCharm(c, "wordpress", "cs:trusty/wordpress-41")
	s.addCharm(c, "wordpress", "cs:trusty/wordpress-9")
	s.addCharm(c, "wordpress", "cs:trusty/wordpress-42")

	for i, test := range serveMetaRevisionInfoTests {
		c.Logf("test %d: %s", i, test.about)
		storeURL := storeURL(test.url + "/meta/revision-info")
		var expectStatus int
		var expectBody interface{}
		if test.err == "" {
			expectStatus = http.StatusOK
			expectBody = test.expect
		} else {
			expectStatus = http.StatusNotFound
			expectBody = params.Error{
				Code:    params.ErrNotFound,
				Message: test.err,
			}
		}
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      s.srv,
			URL:          storeURL,
			ExpectStatus: expectStatus,
			ExpectBody:   expectBody,
		})
	}
}

var metaStatsTests = []struct {
	// about describes the test.
	about string
	// url is the entity id to use when making the meta/stats request.
	url string
	// downloads maps entity ids to a numeric key/value pair where the key is
	// the number of days in the past when the entity was downloaded and the
	// value is the number of download performed that day.
	downloads map[string]map[int]int
	// expectResponse is the expected response from the meta/stats endpoint.
	expectResponse params.StatsResponse
}{{
	about:     "no downloads",
	url:       "trusty/mysql-0",
	downloads: map[string]map[int]int{"trusty/mysql-0": {}},
}, {
	about: "single download",
	url:   "utopic/django-42",
	downloads: map[string]map[int]int{
		"utopic/django-42": {0: 1},
	},
	expectResponse: params.StatsResponse{
		ArchiveDownloadCount: 1,
		ArchiveDownload: params.StatsCount{
			Total: 1,
			Day:   1,
			Week:  1,
			Month: 1,
		},
		ArchiveDownloadAllRevisions: params.StatsCount{
			Total: 1,
			Day:   1,
			Week:  1,
			Month: 1,
		},
	},
}, {
	about: "single download a long time ago",
	url:   "utopic/django-42",
	downloads: map[string]map[int]int{
		"utopic/django-42": {100: 1},
	},
	expectResponse: params.StatsResponse{
		ArchiveDownloadCount: 1,
		ArchiveDownload: params.StatsCount{
			Total: 1,
		},
		ArchiveDownloadAllRevisions: params.StatsCount{
			Total: 1,
		},
	},
}, {
	about: "some downloads this month",
	url:   "utopic/wordpress-47",
	downloads: map[string]map[int]int{
		"utopic/wordpress-47": {20: 2, 25: 5},
	},
	expectResponse: params.StatsResponse{
		ArchiveDownloadCount: 2 + 5,
		ArchiveDownload: params.StatsCount{
			Total: 2 + 5,
			Month: 2 + 5,
		},
		ArchiveDownloadAllRevisions: params.StatsCount{
			Total: 2 + 5,
			Month: 2 + 5,
		},
	},
}, {
	about: "multiple recent downloads",
	url:   "utopic/django-42",
	downloads: map[string]map[int]int{
		"utopic/django-42": {100: 1, 12: 3, 8: 5, 4: 10, 2: 1, 0: 3},
	},
	expectResponse: params.StatsResponse{
		ArchiveDownloadCount: 1 + 3 + 5 + 10 + 1 + 3,
		ArchiveDownload: params.StatsCount{
			Total: 1 + 3 + 5 + 10 + 1 + 3,
			Day:   3,
			Week:  10 + 1 + 3,
			Month: 3 + 5 + 10 + 1 + 3,
		},
		ArchiveDownloadAllRevisions: params.StatsCount{
			Total: 1 + 3 + 5 + 10 + 1 + 3,
			Day:   3,
			Week:  10 + 1 + 3,
			Month: 3 + 5 + 10 + 1 + 3,
		},
	},
}, {
	about: "sparse downloads",
	url:   "utopic/django-42",
	downloads: map[string]map[int]int{
		"utopic/django-42": {200: 3, 28: 4, 3: 5},
	},
	expectResponse: params.StatsResponse{
		ArchiveDownloadCount: 3 + 4 + 5,
		ArchiveDownload: params.StatsCount{
			Total: 3 + 4 + 5,
			Week:  5,
			Month: 4 + 5,
		},
		ArchiveDownloadAllRevisions: params.StatsCount{
			Total: 3 + 4 + 5,
			Week:  5,
			Month: 4 + 5,
		},
	},
}, {
	about: "bundle downloads",
	url:   "bundle/django-simple-2",
	downloads: map[string]map[int]int{
		"bundle/django-simple-2": {200: 3, 28: 4, 3: 5},
	},
	expectResponse: params.StatsResponse{
		ArchiveDownloadCount: 3 + 4 + 5,
		ArchiveDownload: params.StatsCount{
			Total: 3 + 4 + 5,
			Week:  5,
			Month: 4 + 5,
		},
		ArchiveDownloadAllRevisions: params.StatsCount{
			Total: 3 + 4 + 5,
			Week:  5,
			Month: 4 + 5,
		},
	},
}, {
	about: "different charms",
	url:   "trusty/rails-47",
	downloads: map[string]map[int]int{
		"utopic/rails-47": {200: 3, 28: 4, 3: 5},
		"trusty/rails-47": {20: 2, 6: 10},
		"trusty/mysql-0":  {200: 1, 14: 2, 1: 7},
	},
	expectResponse: params.StatsResponse{
		ArchiveDownloadCount: 2 + 10,
		ArchiveDownload: params.StatsCount{
			Total: 2 + 10,
			Week:  10,
			Month: 2 + 10,
		},
		ArchiveDownloadAllRevisions: params.StatsCount{
			Total: 2 + 10,
			Week:  10,
			Month: 2 + 10,
		},
	},
}, {
	about: "different revisions of the same charm",
	url:   "precise/rails-1",
	downloads: map[string]map[int]int{
		"precise/rails-0": {300: 1, 200: 2},
		"precise/rails-1": {100: 5, 10: 3, 2: 7},
		"precise/rails-2": {6: 10, 0: 9},
	},
	expectResponse: params.StatsResponse{
		ArchiveDownloadCount: 5 + 3 + 7,
		ArchiveDownload: params.StatsCount{
			Total: 5 + 3 + 7,
			Week:  7,
			Month: 3 + 7,
		},
		ArchiveDownloadAllRevisions: params.StatsCount{
			Total: (1 + 2) + (5 + 3 + 7) + (10 + 9),
			Day:   0 + 0 + 9,
			Week:  0 + 7 + (10 + 9),
			Month: 0 + (3 + 7) + (10 + 9),
		},
	},
}, {
	about: "downloads only in an old revision",
	url:   "trusty/wordpress-2",
	downloads: map[string]map[int]int{
		"precise/wordpress-2": {2: 2, 0: 1},
		"trusty/wordpress-0":  {100: 10},
		"trusty/wordpress-2":  {},
	},
	expectResponse: params.StatsResponse{
		ArchiveDownloadAllRevisions: params.StatsCount{
			Total: 10,
		},
	},
}, {
	about: "downloads only in newer revision",
	url:   "utopic/wordpress-0",
	downloads: map[string]map[int]int{
		"utopic/wordpress-0": {},
		"utopic/wordpress-1": {31: 7, 10: 1, 3: 2, 0: 1},
		"utopic/wordpress-2": {6: 9, 0: 2},
	},
	expectResponse: params.StatsResponse{
		ArchiveDownloadAllRevisions: params.StatsCount{
			Total: (7 + 1 + 2 + 1) + (9 + 2),
			Day:   1 + 2,
			Week:  (2 + 1) + (9 + 2),
			Month: (1 + 2 + 1) + (9 + 2),
		},
	},
}, {
	about: "non promulgated charms",
	url:   "~who/utopic/django-0",
	downloads: map[string]map[int]int{
		"utopic/django-0":      {100: 1, 10: 2, 1: 3, 0: 4},
		"~who/utopic/django-0": {2: 5},
	},
	expectResponse: params.StatsResponse{
		ArchiveDownloadCount: 5,
		ArchiveDownload: params.StatsCount{
			Total: 5,
			Week:  5,
			Month: 5,
		},
		ArchiveDownloadAllRevisions: params.StatsCount{
			Total: 5,
			Week:  5,
			Month: 5,
		},
	},
}}

func (s *APISuite) TestMetaStats(c *gc.C) {
	if !storetesting.MongoJSEnabled() {
		c.Skip("MongoDB JavaScript not available")
	}
	// TODO (frankban): remove this call when removing the legacy counts logic.
	patchLegacyDownloadCountsEnabled(s.AddCleanup, false)

	today := time.Now()
	for i, test := range metaStatsTests {
		c.Logf("test %d: %s", i, test.about)

		for id, downloadsPerDay := range test.downloads {
			url := charm.MustParseReference(id)

			// Add the required entities to the database.
			if url.Series == "bundle" {
				s.addBundle(c, "wordpress-simple", id)
			} else {
				s.addCharm(c, "wordpress", id)
			}

			// Simulate the entity was downloaded at the specified dates.
			for daysAgo, downloads := range downloadsPerDay {
				date := today.AddDate(0, 0, -daysAgo)
				key := []string{params.StatsArchiveDownload, url.Series, url.Name, url.User, strconv.Itoa(url.Revision)}
				for i := 0; i < downloads; i++ {
					err := s.store.IncCounterAtTime(key, date)
					c.Assert(err, gc.IsNil)
				}
			}
		}

		// Ensure the meta/stats response reports the correct downloads count.
		s.assertGet(c, test.url+"/meta/stats", test.expectResponse)

		// Clean up the collections.
		_, err := s.store.DB.Entities().RemoveAll(nil)
		c.Assert(err, gc.IsNil)
		_, err = s.store.DB.StatCounters().RemoveAll(nil)
		c.Assert(err, gc.IsNil)
	}
}

var metaStatsWithLegacyDownloadCountsTests = []struct {
	about       string
	count       string
	expectValue int64
	expectError string
}{{
	about: "no extra-info",
}, {
	about: "zero downloads",
	count: "0",
}, {
	about:       "some downloads",
	count:       "47",
	expectValue: 47,
}, {
	about:       "invalid value",
	count:       "invalid",
	expectError: "cannot unmarshal extra-info value: invalid character 'i' looking for beginning of value",
}}

// Tests meta/stats with LegacyDownloadCountsEnabled set to true.
// TODO (frankban): remove this test case when removing the legacy counts
// logic.
func (s *APISuite) TestMetaStatsWithLegacyDownloadCounts(c *gc.C) {
	patchLegacyDownloadCountsEnabled(s.AddCleanup, true)
	id, _ := s.addCharm(c, "wordpress", "utopic/wordpress-42")
	url := storeURL("utopic/wordpress-42/meta/stats")

	for i, test := range metaStatsWithLegacyDownloadCountsTests {
		c.Logf("test %d: %s", i, test.about)

		// Update the entity extra info if required.
		if test.count != "" {
			extraInfo := map[string][]byte{
				params.LegacyDownloadStats: []byte(test.count),
			}
			err := s.store.DB.Entities().UpdateId(id, bson.D{{
				"$set", bson.D{{"extrainfo", extraInfo}},
			}})
			c.Assert(err, gc.IsNil)
		}

		var expectBody interface{}
		var expectStatus int
		if test.expectError == "" {
			// Ensure the downloads count is correctly returned.
			expectBody = params.StatsResponse{
				ArchiveDownloadCount: test.expectValue,
				ArchiveDownload: params.StatsCount{
					Total: test.expectValue,
				},
				ArchiveDownloadAllRevisions: params.StatsCount{
					Total: test.expectValue,
				},
			}
			expectStatus = http.StatusOK
		} else {
			// Ensure an error is returned.
			expectBody = params.Error{
				Message: test.expectError,
			}
			expectStatus = http.StatusInternalServerError
		}

		// Perform the request.
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      s.srv,
			URL:          url,
			ExpectStatus: expectStatus,
			ExpectBody:   expectBody,
		})
	}
}

type publishSpec struct {
	id   string
	time string
}

func (p publishSpec) published() params.Published {
	id := charm.MustParseReference(p.id)
	t, err := time.Parse("2006-01-02 15:04", p.time)
	if err != nil {
		panic(err)
	}
	return params.Published{id, t}
}

var publishedCharms = []publishSpec{{
	id:   "cs:precise-wordpress-1",
	time: "5432-10-12 00:00",
}, {
	id:   "cs:precise-mysql-1",
	time: "5432-10-12 13:00",
}, {
	id:   "cs:precise-wordpress-2",
	time: "5432-10-12 23:59",
}, {
	id:   "cs:precise-mysql-2",
	time: "5432-10-13 00:00",
}, {
	id:   "cs:precise-mysql-5",
	time: "5432-10-13 10:00",
}, {
	id:   "cs:precise-wordpress-3",
	time: "5432-10-14 01:00",
}}

var changesPublishedTests = []struct {
	args string
	// expect holds indexes into publishedCharms
	// of the expected indexes returned by charms/published
	expect []int
}{{
	args:   "",
	expect: []int{5, 4, 3, 2, 1, 0},
}, {
	args:   "?start=5432-10-13",
	expect: []int{5, 4, 3},
}, {
	args:   "?stop=5432-10-13",
	expect: []int{4, 3, 2, 1, 0},
}, {
	args:   "?start=5432-10-13&stop=5432-10-13",
	expect: []int{4, 3},
}, {
	args:   "?start=5432-10-12&stop=5432-10-13",
	expect: []int{4, 3, 2, 1, 0},
}, {
	args:   "?start=5432-10-13&stop=5432-10-12",
	expect: []int{},
}, {
	args:   "?limit=3",
	expect: []int{5, 4, 3},
}, {
	args:   "?start=5432-10-12&stop=5432-10-13&limit=2",
	expect: []int{4, 3},
}}

func (s *APISuite) TestChangesPublished(c *gc.C) {
	s.publishCharmsAtKnownTimes(c, publishedCharms)
	for i, test := range changesPublishedTests {
		c.Logf("test %d: %q", i, test.args)
		expect := make([]params.Published, len(test.expect))
		for j, index := range test.expect {
			expect[j] = publishedCharms[index].published()
		}
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:    s.srv,
			URL:        storeURL("changes/published") + test.args,
			ExpectBody: expect,
		})
	}
}

var changesPublishedErrorsTests = []struct {
	args   string
	expect params.Error
	status int
}{{
	args: "?limit=0",
	expect: params.Error{
		Code:    params.ErrBadRequest,
		Message: "invalid 'limit' value",
	},
	status: http.StatusBadRequest,
}, {
	args: "?limit=-1",
	expect: params.Error{
		Code:    params.ErrBadRequest,
		Message: "invalid 'limit' value",
	},
	status: http.StatusBadRequest,
}, {
	args: "?limit=-9999",
	expect: params.Error{
		Code:    params.ErrBadRequest,
		Message: "invalid 'limit' value",
	},
	status: http.StatusBadRequest,
}, {
	args: "?start=baddate",
	expect: params.Error{
		Code:    params.ErrBadRequest,
		Message: `invalid 'start' value "baddate": parsing time "baddate" as "2006-01-02": cannot parse "baddate" as "2006"`,
	},
	status: http.StatusBadRequest,
}, {
	args: "?stop=baddate",
	expect: params.Error{
		Code:    params.ErrBadRequest,
		Message: `invalid 'stop' value "baddate": parsing time "baddate" as "2006-01-02": cannot parse "baddate" as "2006"`,
	},
	status: http.StatusBadRequest,
}}

func (s *APISuite) TestChangesPublishedErrors(c *gc.C) {
	s.publishCharmsAtKnownTimes(c, publishedCharms)
	for i, test := range changesPublishedErrorsTests {
		c.Logf("test %d: %q", i, test.args)
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      s.srv,
			URL:          storeURL("changes/published") + test.args,
			ExpectStatus: test.status,
			ExpectBody:   test.expect,
		})
	}
}

// publishCharmsAtKnownTimes populates the store with
// a range of charms with known time stamps.
func (s *APISuite) publishCharmsAtKnownTimes(c *gc.C, charms []publishSpec) {
	for _, ch := range publishedCharms {
		s.addCharm(c, "wordpress", ch.id)
		t := ch.published().PublishTime
		err := s.store.DB.Entities().UpdateId(ch.id, bson.D{{"$set", bson.D{{"uploadtime", t}}}})
		c.Assert(err, gc.IsNil)
	}
}

var debugPprofTests = []struct {
	path  string
	match string
}{{
	path:  "debug/pprof/",
	match: `(?s).*profiles:.*heap.*`,
}, {
	path:  "debug/pprof/goroutine?debug=2",
	match: "(?s)goroutine [0-9]+.*",
}, {
	path:  "debug/pprof/cmdline",
	match: ".+charmstore.+",
}}

func (s *APISuite) TestDebugPprof(c *gc.C) {
	for i, test := range debugPprofTests {
		c.Logf("test %d: %s", i, test.path)

		rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler: s.srv,
			Header:  basicAuthHeader(serverParams.AuthUsername, serverParams.AuthPassword),
			URL:     storeURL(test.path),
		})
		c.Assert(rec.Code, gc.Equals, http.StatusOK, gc.Commentf("body: %s", rec.Body.String()))
		c.Assert(rec.Body.String(), gc.Matches, test.match)
	}
}

func (s *APISuite) TestDebugPprofFailsWithoutAuth(c *gc.C) {
	for i, test := range debugPprofTests {
		c.Logf("test %d: %s", i, test.path)
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      s.srv,
			URL:          storeURL(test.path),
			ExpectStatus: http.StatusUnauthorized,
			ExpectBody: params.Error{
				Message: "authentication failed: missing HTTP auth header",
				Code:    params.ErrUnauthorized,
			},
		})
	}
}

func basicAuthHeader(username, password string) http.Header {
	// It's a pity we have to jump through this hoop.
	req := &http.Request{
		Header: make(http.Header),
	}
	req.SetBasicAuth(username, password)
	return req.Header
}

func entityFieldGetter(fieldName string) metaEndpointExpectedValueGetter {
	return entityGetter(func(entity *mongodoc.Entity) interface{} {
		field := reflect.ValueOf(entity).Elem().FieldByName(fieldName)
		if !field.IsValid() {
			panic(errgo.Newf("entity has no field %q", fieldName))
		}
		return field.Interface()
	})
}

func entityGetter(get func(*mongodoc.Entity) interface{}) metaEndpointExpectedValueGetter {
	return func(store *charmstore.Store, url *charm.Reference) (interface{}, error) {
		var doc mongodoc.Entity
		err := store.DB.Entities().FindId(url).One(&doc)
		if err != nil {
			return nil, errgo.Mask(err)
		}
		return get(&doc), nil
	}
}

func zipGetter(get func(*zip.Reader) interface{}) metaEndpointExpectedValueGetter {
	return func(store *charmstore.Store, url *charm.Reference) (interface{}, error) {
		var doc mongodoc.Entity
		if err := store.DB.Entities().
			FindId(url).
			Select(bson.D{{"blobname", 1}}).
			One(&doc); err != nil {
			return nil, errgo.Mask(err)
		}
		blob, size, err := store.BlobStore.Open(doc.BlobName)
		if err != nil {
			return nil, errgo.Mask(err)
		}
		defer blob.Close()
		content, err := ioutil.ReadAll(blob)
		if err != nil {
			return nil, errgo.Mask(err)
		}
		r, err := zip.NewReader(bytes.NewReader(content), size)
		if err != nil {
			return nil, errgo.Mask(err)
		}
		return get(r), nil
	}
}

func entitySizeChecker(c *gc.C, data interface{}) {
	response := data.(*params.ArchiveSizeResponse)
	c.Assert(response.Size, gc.Not(gc.Equals), int64(0))
}

func (s *APISuite) addCharm(c *gc.C, charmName, curl string) (*charm.Reference, charm.Charm) {
	url := charm.MustParseReference(curl)
	wordpress := storetesting.Charms.CharmDir(charmName)
	err := s.store.AddCharmWithArchive(url, wordpress)
	c.Assert(err, gc.IsNil)
	return url, wordpress
}

func (s *APISuite) addBundle(c *gc.C, bundleName string, curl string) (*charm.Reference, charm.Bundle) {
	url := charm.MustParseReference(curl)
	bundle := storetesting.Charms.BundleDir(bundleName)
	err := s.store.AddBundleWithArchive(url, bundle)
	c.Assert(err, gc.IsNil)
	return url, bundle
}

func (s *APISuite) assertPut(c *gc.C, url string, val interface{}) {
	body, err := json.Marshal(val)
	c.Assert(err, gc.IsNil)
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL(url),
		Method:  "PUT",
		Header: http.Header{
			"Content-Type": {"application/json"},
		},
		Username: serverParams.AuthUsername,
		Password: serverParams.AuthPassword,
		Body:     bytes.NewReader(body),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK, gc.Commentf("body: %s", rec.Body.String()))
	c.Assert(rec.Body.String(), gc.HasLen, 0)
}

func (s *APISuite) assertGet(c *gc.C, url string, expectVal interface{}) {
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:    s.srv,
		URL:        storeURL(url),
		ExpectBody: expectVal,
	})
}

func (s *APISuite) addLog(c *gc.C, log *mongodoc.Log) {
	err := s.store.DB.Logs().Insert(log)
	c.Assert(err, gc.Equals, nil)
}

func mustMarshalJSON(val interface{}) string {
	data, err := json.Marshal(val)
	if err != nil {
		panic(fmt.Errorf("cannot marshal %#v: %v", val, err))
	}
	return string(data)
}

func (s *APISuite) TestMacaroon(c *gc.C) {
	var checkedCaveats []string
	var mu sync.Mutex
	var dischargeError error
	discharger := bakerytest.NewDischarger(nil, func(cond string, arg string) ([]checkers.Caveat, error) {
		mu.Lock()
		defer mu.Unlock()
		checkedCaveats = append(checkedCaveats, cond+" "+arg)
		return []checkers.Caveat{checkers.DeclaredCaveat("username", "who")}, dischargeError
	})
	defer discharger.Close()
	// Create a charmstore server that will use the test third party for
	// its third party caveat.
	srv, _ := newServer(c, s.store.DB.Session, nil, charmstore.ServerParams{
		IdentityLocation: discharger.Location(),
		PublicKeyLocator: discharger,
	})
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: srv,
		URL:     storeURL("macaroon"),
		Method:  "GET",
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK, gc.Commentf("body: %s", rec.Body.String()))
	var m macaroon.Macaroon
	err := json.Unmarshal(rec.Body.Bytes(), &m)
	c.Assert(err, gc.IsNil)
	c.Assert(m.Location(), gc.Equals, "charmstore")
	ms, err := httpbakery.DischargeAll(&m, httpbakery.NewHTTPClient(), noInteraction)
	c.Assert(err, gc.IsNil)
	sort.Strings(checkedCaveats)
	c.Assert(checkedCaveats, jc.DeepEquals, []string{
		"is-authenticated-user ",
	})
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("log"),
		ExpectStatus: http.StatusUnauthorized,
		ExpectBody: params.Error{
			Message: "authentication failed: missing HTTP auth header",
			Code:    params.ErrUnauthorized,
		},
	})
	macaroonCookie, err := httpbakery.NewCookie(ms)
	c.Assert(err, gc.IsNil)
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      srv,
		URL:          storeURL("log"),
		Cookies:      []*http.Cookie{macaroonCookie},
		ExpectStatus: http.StatusUnauthorized,
		ExpectBody: params.Error{
			Code:    params.ErrUnauthorized,
			Message: `unauthorized: access denied for user "who"`,
		},
	})
}
