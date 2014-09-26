// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/juju/errgo"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v4"
	charmtesting "gopkg.in/juju/charm.v4/testing"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/storetesting"
	"github.com/juju/charmstore/internal/storetesting/stats"
	"github.com/juju/charmstore/internal/v4"
	"github.com/juju/charmstore/params"
)

var serverParams = charmstore.ServerParams{
	AuthUsername: "test-user",
	AuthPassword: "test-password",
}

type APISuite struct {
	storetesting.IsolatedMgoSuite
	srv   http.Handler
	store *charmstore.Store
}

var _ = gc.Suite(&APISuite{})

func (s *APISuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	s.srv, s.store = newServer(c, s.Session, serverParams)
}

func newServer(c *gc.C, session *mgo.Session, config charmstore.ServerParams) (http.Handler, *charmstore.Store) {
	db := session.DB("charmstore")
	store, err := charmstore.NewStore(db)
	c.Assert(err, gc.IsNil)
	srv, err := charmstore.NewServer(db, config, map[string]charmstore.NewAPIHandlerFunc{"v4": v4.NewAPIHandler})
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
	checkURL:  "cs:bundle/wordpress-42",
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
	checkURL: "cs:bundle/wordpress-42",
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
	checkURL: "cs:bundle/wordpress-42",
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
	checkURL: "cs:bundle/wordpress-42",
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
				mustParseReference("cs:precise/wordpress-99"),
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
}}

// TestEndpointGet tries to ensure that the endpoint
// test data getters correspond with reality.
func (s *APISuite) TestEndpointGet(c *gc.C) {
	s.addTestEntities(c)
	for i, ep := range metaEndpoints {
		c.Logf("test %d: %s\n", i, ep.name)
		data, err := ep.get(s.store, mustParseReference(ep.checkURL))
		c.Assert(err, gc.IsNil)
		ep.assertCheckData(c, data)
	}
}

var testEntities = []string{
	// A stock charm.
	"cs:precise/wordpress-23",
	// A stock bundle.
	"cs:bundle/wordpress-42",
	// A charm with some actions.
	"cs:precise/dummy-10",
}

func (s *APISuite) addTestEntities(c *gc.C) []*charm.Reference {
	urls := make([]*charm.Reference, len(testEntities))
	for i, e := range testEntities {
		url := mustParseReference(e)
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
				storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
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
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
			Handler: s.srv,
			URL:     storeURL(test.path),
			Method:  "PUT",
			Header: http.Header{
				"Content-Type": {contentType},
			},
			Body:         strings.NewReader(mustMarshalJSON(test.body)),
			ExpectStatus: test.expectStatus,
			ExpectBody:   test.expectBody,
		})
	}
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
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
			Handler:      s.srv,
			URL:          storeURL("precise/wordpress-23/meta/" + ep.name),
			ExpectStatus: http.StatusNotFound,
			ExpectBody:   expected,
		})
		expected.Message = `no matching charm or bundle for "cs:wordpress"`
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
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
	s.addCharm(c, "wordpress", "cs:bundle/bundlelovin-10")
	s.addCharm(c, "wordpress", "cs:bundle/wordpress-10")

	for i, test := range resolveURLTests {
		c.Logf("test %d: %s", i, test.url)
		url := mustParseReference(test.url)
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
		{Id: "cs:bundle/wordpress-0"},
	},
}, {
	about: "fully qualified URL that does not exist",
	url:   "trusty/wordpress-99",
	expect: []params.ExpandedId{
		{Id: "cs:utopic/wordpress-42"},
		{Id: "cs:trusty/wordpress-47"},
		{Id: "cs:bundle/wordpress-0"},
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
	s.addCharm(c, "wordpress", "cs:bundle/mongo-0")
	s.addCharm(c, "wordpress", "cs:bundle/wordpress-0")

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
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
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
			mustParseReference("cs:trusty/wordpress-43"),
			mustParseReference("cs:trusty/wordpress-42"),
			mustParseReference("cs:trusty/wordpress-41"),
			mustParseReference("cs:trusty/wordpress-9"),
		}},
}, {
	about: "partial url uses a default series",
	url:   "wordpress",
	expect: params.RevisionInfoResponse{
		[]*charm.Reference{
			mustParseReference("cs:trusty/wordpress-43"),
			mustParseReference("cs:trusty/wordpress-42"),
			mustParseReference("cs:trusty/wordpress-41"),
			mustParseReference("cs:trusty/wordpress-9"),
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
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
			Handler:      s.srv,
			URL:          storeURL,
			ExpectStatus: expectStatus,
			ExpectBody:   expectBody,
		})
	}
}

var metaStatsTests = []struct {
	about     string
	url       string
	downloads int64
}{{
	about: "no downloads",
	url:   "trusty/mysql-0",
}, {
	about:     "single download",
	url:       "utopic/django-42",
	downloads: 1,
}, {
	about:     "multiple downloads",
	url:       "utopic/django-47",
	downloads: 5,
}, {
	about:     "bundle downloads",
	url:       "bundle/wordpress-simple-42",
	downloads: 2,
}, {
	about:     "single user download",
	url:       "~who/utopic/django-42",
	downloads: 1,
}}

func (s *APISuite) TestMetaStats(c *gc.C) {
	if !storetesting.MongoJSEnabled() {
		c.Skip("MongoDB JavaScript not available")
	}

	// Add a bunch of entities in the database.
	s.addCharm(c, "wordpress", "cs:trusty/mysql-0")
	s.addCharm(c, "wordpress", "cs:utopic/django-42")
	s.addCharm(c, "wordpress", "cs:utopic/django-47")
	s.addCharm(c, "wordpress", "cs:~who/utopic/django-42")
	s.addBundle(c, "wordpress", "cs:bundle/wordpress-simple-42")

	for i, test := range metaStatsTests {
		c.Logf("test %d: %s", i, test.about)

		// Download the entity archive for the requested number of times.
		archiveUrl := storeURL(test.url + "/archive")
		for i := 0; i < int(test.downloads); i++ {
			rec := storetesting.DoRequest(c, storetesting.DoRequestParams{
				Handler: s.srv,
				URL:     archiveUrl,
			})
			c.Assert(rec.Code, gc.Equals, http.StatusOK)
		}

		// Wait until the counters are updated.
		url := mustParseReference(test.url)
		key := []string{params.StatsArchiveDownload, url.Series, url.Name, url.User, strconv.Itoa(url.Revision)}
		stats.CheckCounterSum(c, s.store, key, false, test.downloads)

		// Ensure the meta/stats response reports the correct downloads count.
		s.assertGet(c, test.url+"/meta/stats", params.StatsResponse{
			ArchiveDownloadCount: test.downloads,
		})
	}
}

type publishSpec struct {
	id   string
	time string
}

func (p publishSpec) published() params.Published {
	id := mustParseReference(p.id)
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
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
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
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
			Handler:      s.srv,
			URL:          storeURL("changes/published") + test.args,
			ExpectStatus: test.status,
			ExpectBody:   test.expect,
		})
	}
}

func (s *APISuite) TestStatus(c *gc.C) {
	for _, id := range []string{
		"cs:precise/wordpress-2",
		"cs:precise/wordpress-3",
		"cs:~foo/precise/arble-9",
		"cs:~bar/quantal/arble-10",
		"cs:bundle/oflaughs-3",
		"cs:~bar/bundle/oflaughs-4",
	} {
		if strings.Contains(id, "bundle") {
			s.addBundle(c, "wordpress", id)
		} else {
			s.addCharm(c, "wordpress", id)
		}
	}
	now := time.Now()
	s.PatchValue(v4.StartTime, now)
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("debug/status"),
		ExpectStatus: http.StatusOK,
		ExpectBody: map[string]params.DebugStatus{
			"mongo_connected": {
				Name:   "MongoDB is connected",
				Value:  "Connected",
				Passed: true,
			},
			"mongo_collections": {
				Name:   "MongoDB collections",
				Value:  "All required collections exist",
				Passed: true,
			},
			"entities": {
				Name:   "Entities in charm store",
				Value:  "4 charms; 2 bundles; 3 promulgated",
				Passed: true,
			},
			"server_started": {
				Name:   "Server started",
				Value:  now.String(),
				Passed: true,
			},
		},
	})
}

func (s *APISuite) TestStatusWithoutCorrectCollections(c *gc.C) {
	s.store.DB.Entities().DropCollection()
	now := time.Now()
	s.PatchValue(v4.StartTime, now)
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("debug/status"),
		ExpectStatus: http.StatusOK,
		ExpectBody: map[string]params.DebugStatus{
			"mongo_connected": {
				Name:   "MongoDB is connected",
				Value:  "Connected",
				Passed: true,
			},
			"mongo_collections": {
				Name:   "MongoDB collections",
				Value:  "Missing collections: [" + s.store.DB.Entities().Name + "]",
				Passed: false,
			},
			"entities": {
				Name:   "Entities in charm store",
				Value:  "0 charms; 0 bundles; 0 promulgated",
				Passed: true,
			},
			"server_started": {
				Name:   "Server started",
				Value:  now.String(),
				Passed: true,
			},
		},
	})
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

func assertNotImplemented(c *gc.C, h http.Handler, path string) {
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:      h,
		URL:          storeURL(path),
		ExpectStatus: http.StatusInternalServerError,
		ExpectBody: params.Error{
			Message: "method not implemented",
		},
	})
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
	url := mustParseReference(curl)
	wordpress := charmtesting.Charms.CharmDir(charmName)
	err := s.store.AddCharmWithArchive(url, wordpress)
	c.Assert(err, gc.IsNil)
	return url, wordpress
}

func (s *APISuite) addBundle(c *gc.C, bundleName string, curl string) (*charm.Reference, charm.Bundle) {
	url := mustParseReference(curl)
	bundle := charmtesting.Charms.BundleDir(bundleName)
	err := s.store.AddBundleWithArchive(url, bundle)
	c.Assert(err, gc.IsNil)
	return url, bundle
}

func (s *APISuite) assertPut(c *gc.C, url string, val interface{}) {
	body, err := json.Marshal(val)
	c.Assert(err, gc.IsNil)
	rec := storetesting.DoRequest(c, storetesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL(url),
		Method:  "PUT",
		Header: http.Header{
			"Content-Type": {"application/json"},
		},
		Body: bytes.NewReader(body),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK, gc.Commentf("body: %s", rec.Body.String()))
	c.Assert(rec.Body.String(), gc.HasLen, 0)
}

func (s *APISuite) assertGet(c *gc.C, url string, expectVal interface{}) {
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:    s.srv,
		URL:        storeURL(url),
		ExpectBody: expectVal,
	})
}

func mustMarshalJSON(val interface{}) string {
	data, err := json.Marshal(val)
	if err != nil {
		panic(fmt.Errorf("cannot marshal %#v: %v", val, err))
	}
	return string(data)
}

func mustParseReference(url string) *charm.Reference {
	ref, err := charm.ParseReference(url)
	if err != nil {
		panic(err)
	}
	return ref
}
