// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/juju/errgo"
	jc "github.com/juju/testing/checkers"
	"gopkg.in/juju/charm.v3"
	charmtesting "gopkg.in/juju/charm.v3/testing"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/storetesting"
	"github.com/juju/charmstore/internal/v4"
	"github.com/juju/charmstore/params"
)

type APISuite struct {
	storetesting.IsolatedMgoSuite
	srv   http.Handler
	store *charmstore.Store
}

var _ = gc.Suite(&APISuite{})

func (s *APISuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	s.srv, s.store = newServer(c, s.Session)
}

func newServer(c *gc.C, session *mgo.Session) (http.Handler, *charmstore.Store) {
	db := session.DB("charmstore")
	store, err := charmstore.NewStore(db)
	c.Assert(err, gc.IsNil)
	srv, err := charmstore.NewServer(db, map[string]charmstore.NewAPIHandler{"v4": v4.New})
	c.Assert(err, gc.IsNil)
	return srv, store
}

func storeURL(path string) string {
	return "/v4/" + path
}

type metaEndpointExpectedValueGetter func(*charmstore.Store, *charm.Reference) (interface{}, error)

type metaEndpoint struct {
	// The name of the meta endpoint.
	name string
	// Whether the endpoint applies only to charms (charmOnly),
	// to bundles (bundleOnly) or to both (empty field).
	exclusive int
	// A function returning the expected data for the endpoint.
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
		_ = data.(*params.RelatedResponse)
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
		_ = data.([]*params.MetaAnyResponse)
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
			storeURL := storeURL(charmId + "/meta/" + ep.name)
			expectData, err := ep.get(s.store, url)
			c.Assert(err, gc.IsNil)
			c.Logf("	expected data for %q: %#v", url, expectData)
			if isNull(expectData) {
				storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
					Handler:    s.srv,
					URL:        storeURL,
					ExpectCode: http.StatusNotFound,
					ExpectBody: params.Error{
						Message: params.ErrMetadataNotFound.Error(),
						Code:    params.ErrMetadataNotFound,
					},
				})
				continue
			}
			tested = true
			storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
				Handler:    s.srv,
				URL:        storeURL,
				ExpectBody: expectData,
			})

		}
		if !tested {
			c.Errorf("endpoint %q is null for all endpoints, so is not properly tested", ep.name)
		}
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
		storeURL := storeURL(charmId + "/meta/any?" + strings.Join(flags, "&"))
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
			Handler:    s.srv,
			URL:        storeURL,
			ExpectBody: expectData,
		})
	}
}

// In this test we rely on the charm.v2 testing repo package and
// dummy charm that has actions included.
func (s *APISuite) TestMetaCharmActions(c *gc.C) {
	url, dummy := s.addCharm(c, "dummy", "cs:precise/dummy-10")
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:    s.srv,
		URL:        storeURL("precise/dummy-10/meta/charm-actions"),
		ExpectBody: dummy.Actions(),
	})
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler: s.srv,
		URL:     storeURL("precise/dummy-10/meta/any?include=charm-actions"),
		ExpectBody: params.MetaAnyResponse{
			Id: url,
			Meta: map[string]interface{}{
				"charm-actions": dummy.Actions(),
			},
		},
	})
}

func (s *APISuite) TestBulkMeta(c *gc.C) {
	// We choose an arbitrary set of ids and metadata here, just to smoke-test
	// whether the meta/any logic is hooked up correctly.
	// Detailed tests for this feature are in the router package.

	_, wordpress := s.addCharm(c, "wordpress", "cs:precise/wordpress-23")
	_, mysql := s.addCharm(c, "mysql", "cs:precise/mysql-10")
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler: s.srv,
		URL:     storeURL("meta/charm-metadata?id=precise/wordpress-23&id=precise/mysql-10"),
		ExpectBody: map[string]*charm.Meta{
			"precise/wordpress-23": wordpress.Meta(),
			"precise/mysql-10":     mysql.Meta(),
		},
	})
}

func (s *APISuite) TestBulkMetaAny(c *gc.C) {
	// We choose an arbitrary set of metadata here, just to smoke-test
	// whether the meta/any logic is hooked up correctly.
	// Detailed tests for this feature are in the router package.

	wordpressURL, wordpress := s.addCharm(c, "wordpress", "cs:precise/wordpress-23")
	mysqlURL, mysql := s.addCharm(c, "mysql", "cs:precise/mysql-10")
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler: s.srv,
		URL:     storeURL("meta/any?include=charm-metadata&include=charm-config&id=precise/wordpress-23&id=precise/mysql-10"),
		ExpectBody: map[string]params.MetaAnyResponse{
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
	})
}

func (s *APISuite) TestIdsAreResolved(c *gc.C) {
	// This is just testing that ResolveURL is actually
	// passed to the router. Given how Router is
	// defined, and the ResolveURL tests, this should
	// be sufficient to "join the dots".
	_, wordpress := s.addCharm(c, "wordpress", "cs:precise/wordpress-23")
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:    s.srv,
		URL:        storeURL("wordpress/meta/charm-metadata"),
		ExpectBody: wordpress.Meta(),
	})
}

func (s *APISuite) TestMetaCharmNotFound(c *gc.C) {
	for i, ep := range metaEndpoints {
		c.Logf("test %d: %s", i, ep.name)
		expected := params.Error{
			Message: params.ErrNotFound.Error(),
			Code:    params.ErrNotFound,
		}
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
			Handler:    s.srv,
			URL:        storeURL("precise/wordpress-23/meta/" + ep.name),
			ExpectCode: http.StatusNotFound,
			ExpectBody: expected,
		})
		expected.Message = `no matching charm or bundle for "cs:wordpress"`
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
			Handler:    s.srv,
			URL:        storeURL("wordpress/meta/" + ep.name),
			ExpectCode: http.StatusNotFound,
			ExpectBody: expected,
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
		var expectCode int
		var expectBody interface{}
		if test.err == "" {
			expectCode = http.StatusOK
			expectBody = test.expect
		} else {
			expectCode = http.StatusNotFound
			expectBody = params.Error{
				Code:    params.ErrNotFound,
				Message: test.err,
			}
		}
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
			Handler:    s.srv,
			URL:        storeURL,
			ExpectCode: expectCode,
			ExpectBody: expectBody,
		})
	}
}

func assertNotImplemented(c *gc.C, h http.Handler, path string) {
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:    h,
		URL:        storeURL(path),
		ExpectCode: http.StatusInternalServerError,
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

func mustParseReference(url string) *charm.Reference {
	ref, err := charm.ParseReference(url)
	if err != nil {
		panic(err)
	}
	return ref
}
