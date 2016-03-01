// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v5_test // import "gopkg.in/juju/charmstore.v5-unstable/internal/v5"

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"
	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/bakery/checkers"
	"gopkg.in/macaroon-bakery.v1/httpbakery"
	"gopkg.in/macaroon.v1"
	"gopkg.in/mgo.v2/bson"

	"gopkg.in/juju/charmstore.v5-unstable/audit"
	"gopkg.in/juju/charmstore.v5-unstable/elasticsearch"
	"gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"
	"gopkg.in/juju/charmstore.v5-unstable/internal/entitycache"
	"gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"
	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
	"gopkg.in/juju/charmstore.v5-unstable/internal/series"
	"gopkg.in/juju/charmstore.v5-unstable/internal/storetesting"
	"gopkg.in/juju/charmstore.v5-unstable/internal/v5"
)

var testPublicKey = bakery.PublicKey{
	bakery.Key{
		0xf6, 0xfb, 0xcf, 0x67, 0x8c, 0x5a, 0xb6, 0x52,
		0xa9, 0x23, 0x4d, 0x7e, 0x01, 0xf5, 0x0a, 0x25,
		0xc4, 0x63, 0x69, 0x54, 0x42, 0x62, 0xaf, 0x62,
		0xbe, 0x40, 0x6a, 0x0b, 0xe2, 0x9a, 0xb0, 0x5f,
	},
}

const (
	testUsername = "test-user"
	testPassword = "test-password"
)

var es *elasticsearch.Database = &elasticsearch.Database{"localhost:9200"}
var si *charmstore.SearchIndex = &charmstore.SearchIndex{
	Database: es,
	Index:    "cs",
}

type APISuite struct {
	commonSuite
}

func (s *APISuite) SetUpSuite(c *gc.C) {
	s.enableIdentity = true
	s.commonSuite.SetUpSuite(c)
}

var newResolvedURL = router.MustNewResolvedURL

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

type metaEndpointExpectedValueGetter func(*charmstore.Store, *router.ResolvedURL) (interface{}, error)

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
	checkURL *router.ResolvedURL

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
	checkURL:  newResolvedURL("cs:~charmers/precise/wordpress-23", 23),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data.(*charm.Config).Options["blog-title"].Default, gc.Equals, "My Title")
	},
}, {
	name:      "charm-metadata",
	exclusive: charmOnly,
	get:       entityFieldGetter("CharmMeta"),
	checkURL:  newResolvedURL("~charmers/precise/wordpress-23", 23),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data.(*charm.Meta).Summary, gc.Equals, "Blog engine")
	},
}, {
	name:      "bundle-metadata",
	exclusive: bundleOnly,
	get:       entityFieldGetter("BundleData"),
	checkURL:  newResolvedURL("cs:~charmers/bundle/wordpress-simple-42", 42),
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
	checkURL: newResolvedURL("~charmers/bundle/wordpress-simple-42", 42),
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
	checkURL: newResolvedURL("~charmers/bundle/wordpress-simple-42", 42),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data.(params.BundleCount).Count, gc.Equals, 2)
	},
}, {
	name:      "charm-actions",
	exclusive: charmOnly,
	get:       entityFieldGetter("CharmActions"),
	checkURL:  newResolvedURL("~charmers/precise/dummy-10", 10),
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
	checkURL:        newResolvedURL("~charmers/precise/wordpress-23", 23),
	assertCheckData: entitySizeChecker,
}, {
	name: "hash",
	get: entityGetter(func(entity *mongodoc.Entity) interface{} {
		return &params.HashResponse{
			Sum: entity.BlobHash,
		}
	}),
	checkURL: newResolvedURL("~charmers/precise/wordpress-23", 23),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data.(*params.HashResponse).Sum, gc.Not(gc.Equals), "")
	},
}, {
	name: "hash256",
	get: entityGetter(func(entity *mongodoc.Entity) interface{} {
		return &params.HashResponse{
			Sum: entity.BlobHash256,
		}
	}),
	checkURL: newResolvedURL("~charmers/precise/wordpress-23", 23),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data.(*params.HashResponse).Sum, gc.Not(gc.Equals), "")
	},
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
	checkURL: newResolvedURL("~charmers/bundle/wordpress-simple-42", 42),
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
	checkURL: newResolvedURL("~charmers/precise/wordpress-23", 23),
	assertCheckData: func(c *gc.C, data interface{}) {
		response := data.(*params.ArchiveUploadTimeResponse)
		c.Assert(response.UploadTime, gc.Not(jc.Satisfies), time.Time.IsZero)
		c.Assert(response.UploadTime.Location(), gc.Equals, time.UTC)
	},
}, {
	name: "revision-info",
	get: func(store *charmstore.Store, id *router.ResolvedURL) (interface{}, error) {
		ref := &id.URL
		if id.PromulgatedRevision != -1 {
			ref = id.PreferredURL()
		}
		return params.RevisionInfoResponse{
			[]*charm.URL{ref},
		}, nil
	},
	checkURL: newResolvedURL("~charmers/precise/wordpress-99", 99),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.DeepEquals, params.RevisionInfoResponse{
			[]*charm.URL{
				charm.MustParseURL("cs:precise/wordpress-99"),
			}})
	},
}, {
	name:      "charm-related",
	exclusive: charmOnly,
	get: func(store *charmstore.Store, url *router.ResolvedURL) (interface{}, error) {
		// The charms we use for those tests are not related each other.
		// Charm relations are independently tested in relations_test.go.
		if url.URL.Series == "bundle" {
			return nil, nil
		}
		switch url.URL.String() {
		case "cs:~charmers/precise/wordpress-23", "cs:~bob/utopic/wordpress-2":
			return &params.RelatedResponse{
				Provides: map[string][]params.EntityResult{
					"mysql": {{
						Id: charm.MustParseURL("cs:precise/mysql-5"),
					}},
				},
			}, nil
		case "cs:~charmers/precise/mysql-5":
			return &params.RelatedResponse{
				Requires: map[string][]params.EntityResult{
					"mysql": {{
						Id: charm.MustParseURL("cs:~bob/utopic/wordpress-2"),
					}, {
						Id: charm.MustParseURL("cs:precise/wordpress-23"),
					}},
				},
			}, nil
		}
		return &params.RelatedResponse{}, nil
	},
	checkURL: newResolvedURL("~charmers/precise/wordpress-23", 23),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.FitsTypeOf, (*params.RelatedResponse)(nil))
	},
}, {
	name:      "bundles-containing",
	exclusive: charmOnly,
	get: func(store *charmstore.Store, url *router.ResolvedURL) (interface{}, error) {
		// The charms we use for those tests are not included in any bundle.
		// Charm/bundle relations are tested in relations_test.go.
		if url.URL.Series == "bundle" {
			return nil, nil
		}
		return []*params.MetaAnyResponse{}, nil
	},
	checkURL: newResolvedURL("~charmers/precise/wordpress-23", 23),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.FitsTypeOf, []*params.MetaAnyResponse(nil))
	},
}, {
	name: "stats",
	get: func(store *charmstore.Store, url *router.ResolvedURL) (interface{}, error) {
		// The entities used for those tests were never downloaded.
		return &params.StatsResponse{
			ArchiveDownloadCount: 0,
		}, nil
	},
	checkURL: newResolvedURL("~charmers/precise/wordpress-23", 23),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.FitsTypeOf, (*params.StatsResponse)(nil))
	},
}, {
	name: "extra-info",
	get: func(store *charmstore.Store, url *router.ResolvedURL) (interface{}, error) {
		return map[string]string{
			"key": "value " + url.URL.String(),
		}, nil
	},
	checkURL: newResolvedURL("~charmers/precise/wordpress-23", 23),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.DeepEquals, map[string]string{
			"key": "value cs:~charmers/precise/wordpress-23",
		})
	},
}, {
	name: "extra-info/key",
	get: func(store *charmstore.Store, url *router.ResolvedURL) (interface{}, error) {
		return "value " + url.URL.String(), nil
	},
	checkURL: newResolvedURL("~charmers/precise/wordpress-23", 23),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.Equals, "value cs:~charmers/precise/wordpress-23")
	},
}, {
	name: "common-info",
	get: func(store *charmstore.Store, url *router.ResolvedURL) (interface{}, error) {
		return map[string]string{
			"key": "value " + url.URL.String(),
		}, nil
	},
	checkURL: newResolvedURL("~charmers/precise/wordpress-23", 23),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.DeepEquals, map[string]string{
			"key": "value cs:~charmers/precise/wordpress-23",
		})
	},
}, {
	name: "common-info/key",
	get: func(store *charmstore.Store, url *router.ResolvedURL) (interface{}, error) {
		return "value " + url.URL.String(), nil
	},
	checkURL: newResolvedURL("~charmers/precise/wordpress-23", 23),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.Equals, "value cs:~charmers/precise/wordpress-23")
	},
}, {
	name: "perm",
	get: func(store *charmstore.Store, url *router.ResolvedURL) (interface{}, error) {
		acls, err := entityACLs(store, url)
		if err != nil {
			return nil, err
		}
		return params.PermResponse{
			Read:  acls.Read,
			Write: acls.Write,
		}, nil
	},
	checkURL: newResolvedURL("~bob/utopic/wordpress-2", -1),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.DeepEquals, params.PermResponse{
			Read:  []string{params.Everyone},
			Write: []string{"bob"},
		})
	},
}, {
	name: "perm/read",
	get: func(store *charmstore.Store, url *router.ResolvedURL) (interface{}, error) {
		acls, err := entityACLs(store, url)
		if err != nil {
			return nil, err
		}
		return acls.Read, nil
	},
	checkURL: newResolvedURL("cs:~bob/utopic/wordpress-2", -1),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.DeepEquals, []string{params.Everyone})
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
	checkURL: newResolvedURL("~charmers/utopic/category-2", 2),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, jc.DeepEquals, params.TagsResponse{
			Tags: []string{"openstack", "storage"},
		})
	},
}, {
	name: "id-user",
	get: func(store *charmstore.Store, url *router.ResolvedURL) (interface{}, error) {
		return params.IdUserResponse{url.PreferredURL().User}, nil
	},
	checkURL: newResolvedURL("cs:~bob/utopic/wordpress-2", -1),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.Equals, params.IdUserResponse{"bob"})
	},
}, {
	name: "id-series",
	get: func(store *charmstore.Store, url *router.ResolvedURL) (interface{}, error) {
		return params.IdSeriesResponse{url.URL.Series}, nil
	},
	checkURL: newResolvedURL("~charmers/utopic/category-2", 2),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.Equals, params.IdSeriesResponse{"utopic"})
	},
}, {
	name: "id-name",
	get: func(store *charmstore.Store, url *router.ResolvedURL) (interface{}, error) {
		return params.IdNameResponse{url.URL.Name}, nil
	},
	checkURL: newResolvedURL("~charmers/utopic/category-2", 2),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.Equals, params.IdNameResponse{"category"})
	},
}, {
	name: "id-revision",
	get: func(store *charmstore.Store, url *router.ResolvedURL) (interface{}, error) {
		return params.IdRevisionResponse{url.PreferredURL().Revision}, nil
	},
	checkURL: newResolvedURL("~charmers/utopic/category-2", 2),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.Equals, params.IdRevisionResponse{2})
	},
}, {
	name: "id",
	get: func(store *charmstore.Store, url *router.ResolvedURL) (interface{}, error) {
		id := url.PreferredURL()
		return params.IdResponse{
			Id:       id,
			User:     id.User,
			Series:   id.Series,
			Name:     id.Name,
			Revision: id.Revision,
		}, nil
	},
	checkURL: newResolvedURL("~charmers/utopic/category-2", 2),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, jc.DeepEquals, params.IdResponse{
			Id:       charm.MustParseURL("cs:utopic/category-2"),
			User:     "",
			Series:   "utopic",
			Name:     "category",
			Revision: 2,
		})
	},
}, {
	name: "promulgated",
	get: func(store *charmstore.Store, url *router.ResolvedURL) (interface{}, error) {
		e, err := store.FindBaseEntity(&url.URL, nil)
		if err != nil {
			return nil, err
		}
		return params.PromulgatedResponse{
			Promulgated: bool(e.Promulgated),
		}, nil
	},
	checkURL: newResolvedURL("cs:~bob/utopic/wordpress-2", -1),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.Equals, params.PromulgatedResponse{Promulgated: false})
	},
}, {
	name: "supported-series",
	get: entityGetter(func(entity *mongodoc.Entity) interface{} {
		if entity.URL.Series == "bundle" {
			return nil
		}
		return params.SupportedSeriesResponse{
			SupportedSeries: entity.SupportedSeries,
		}
	}),
	checkURL: newResolvedURL("~charmers/utopic/category-2", 2),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, jc.DeepEquals, params.SupportedSeriesResponse{
			SupportedSeries: []string{"utopic"},
		})
	},
}, {
	name: "terms",
	get: func(store *charmstore.Store, url *router.ResolvedURL) (interface{}, error) {
		doc, err := store.FindEntity(url, nil)
		if err != nil {
			return nil, errgo.Mask(err)
		}
		if doc.URL.Series == "bundle" {
			return nil, nil
		}
		if doc.CharmMeta == nil || len(doc.CharmMeta.Terms) == 0 {
			return []string{}, nil
		}
		return doc.CharmMeta.Terms, nil
	},
	checkURL: newResolvedURL("cs:~charmers/precise/terms-42", 42),
	assertCheckData: func(c *gc.C, data interface{}) {
		c.Assert(data, gc.DeepEquals, []string{"terms-1/1", "terms-2/5"})
	},
}}

// TestEndpointGet tries to ensure that the endpoint
// test data getters correspond with reality.
func (s *APISuite) TestEndpointGet(c *gc.C) {
	s.addTestEntities(c)
	for i, ep := range metaEndpoints {
		c.Logf("test %d: %s\n", i, ep.name)
		data, err := ep.get(s.store, ep.checkURL)
		c.Assert(err, gc.IsNil)
		ep.assertCheckData(c, data)
	}
}

func (s *APISuite) TestAllMetaEndpointsTested(c *gc.C) {
	// Make sure that we're testing all the metadata
	// endpoints that we need to.
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("~charmers/precise/wordpress-23", 23))
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

var testEntities = []*router.ResolvedURL{
	// A stock charm.
	newResolvedURL("cs:~charmers/precise/wordpress-23", 23),
	// Another stock charm, to satisfy the bundle's requirements.
	newResolvedURL("cs:~charmers/precise/mysql-5", 5),
	// A stock bundle.
	newResolvedURL("cs:~charmers/bundle/wordpress-simple-42", 42),
	// A charm with some actions.
	newResolvedURL("cs:~charmers/precise/dummy-10", 10),
	// A charm with some tags.
	newResolvedURL("cs:~charmers/utopic/category-2", 2),
	// A charm with a different user.
	newResolvedURL("cs:~bob/utopic/wordpress-2", -1),
	// A charms, which requires agreement to terms
	newResolvedURL("cs:~charmers/precise/terms-42", 42),
}

func (s *APISuite) addTestEntities(c *gc.C) []*router.ResolvedURL {
	for _, e := range testEntities {
		if e.URL.Series == "bundle" {
			s.addPublicBundleFromRepo(c, e.URL.Name, e, true)
		} else {
			s.addPublicCharmFromRepo(c, e.URL.Name, e)
		}
		// Associate some extra-info data with the entity.
		key := e.URL.Path() + "/meta/extra-info/key"
		commonkey := e.URL.Path() + "/meta/common-info/key"
		s.assertPut(c, key, "value "+e.URL.String())
		s.assertPut(c, commonkey, "value "+e.URL.String())
	}
	return testEntities
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
			c.Logf("	path %q: %#v", url, path)
			s.assertGet(c, path, expectData)
		}
		if !tested {
			c.Errorf("endpoint %q is null for all endpoints, so is not properly tested", ep.name)
		}
	}
}

func (s *APISuite) TestMetaPermAudit(c *gc.C) {
	var calledEntities []audit.Entry
	s.PatchValue(v5.TestAddAuditCallback, func(e audit.Entry) {
		calledEntities = append(calledEntities, e)
	})
	s.discharge = dischargeForUser("bob")

	url := newResolvedURL("~bob/precise/wordpress-23", 23)
	s.addPublicCharmFromRepo(c, "wordpress", url)
	s.assertPutNonAdmin(c, "precise/wordpress-23/meta/perm/read", []string{"charlie"})
	c.Assert(calledEntities, jc.DeepEquals, []audit.Entry{{
		User: "bob",
		Op:   audit.OpSetPerm,
		ACL: &audit.ACL{
			Read: []string{"charlie"},
		},
		Entity: charm.MustParseURL("~bob/precise/wordpress-23"),
	}})
	calledEntities = []audit.Entry{}

	s.assertPut(c, "precise/wordpress-23/meta/perm/write", []string{"bob", "foo"})
	c.Assert(calledEntities, jc.DeepEquals, []audit.Entry{{
		User: "admin",
		Op:   audit.OpSetPerm,
		ACL: &audit.ACL{
			Write: []string{"bob", "foo"},
		},
		Entity: charm.MustParseURL("~bob/precise/wordpress-23"),
	}})
	calledEntities = []audit.Entry{}

	s.assertPutNonAdmin(c, "precise/wordpress-23/meta/perm", params.PermRequest{
		Read:  []string{"a"},
		Write: []string{"b", "c"},
	})
	c.Assert(calledEntities, jc.DeepEquals, []audit.Entry{{
		User: "bob",
		Op:   audit.OpSetPerm,
		ACL: &audit.ACL{
			Read: []string{"a"},
		},
		Entity: charm.MustParseURL("~bob/precise/wordpress-23"),
	}, {
		User: "bob",
		Op:   audit.OpSetPerm,
		ACL: &audit.ACL{
			Write: []string{"b", "c"},
		},
		Entity: charm.MustParseURL("~bob/precise/wordpress-23"),
	}})
}

func (s *APISuite) TestMetaPermPublicWrite(c *gc.C) {
	url := newResolvedURL("~bob/precise/wordpress-23", 23)
	s.addPublicCharmFromRepo(c, "wordpress", url)
	s.assertPut(c, "precise/wordpress-23/meta/perm/write", []string{"everyone"})

	// Even though the endpoint has write permissions open to anyone,
	// we still require authentication so that we can make an entry in
	// the audit log.
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler: s.srv,
		URL:     storeURL("precise/wordpress-23/meta/perm/read"),
		Method:  "PUT",
		Header: http.Header{
			"Content-Type": {"application/json"},
		},
		Body:         strings.NewReader(`["alice"]`),
		ExpectStatus: http.StatusProxyAuthRequired,
		ExpectBody:   dischargeRequiredBody,
	})
	s.discharge = dischargeForUser("bob")
	s.assertPutNonAdmin(c, "precise/wordpress-23/meta/perm/read", []string{"alice"})
}

func (s *APISuite) TestMetaPerm(c *gc.C) {
	s.discharge = dischargeForUser("charmers")

	for _, u := range []*router.ResolvedURL{
		newResolvedURL("~charmers/precise/wordpress-23", 23),
		newResolvedURL("~charmers/precise/wordpress-24", 24),
		newResolvedURL("~charmers/trusty/wordpress-1", 1),
	} {
		err := s.store.AddCharmWithArchive(u, storetesting.NewCharm(nil))
		c.Assert(err, gc.IsNil)
	}
	s.assertGet(c, "wordpress/meta/perm?channel=unpublished", params.PermResponse{
		Read:  []string{"charmers"},
		Write: []string{"charmers"},
	})
	e, err := s.store.FindBaseEntity(charm.MustParseURL("precise/wordpress-23"), nil)
	c.Assert(err, gc.IsNil)
	c.Assert(e.ChannelACLs, gc.DeepEquals, map[mongodoc.Channel]mongodoc.ACL{
		mongodoc.UnpublishedChannel: {
			Read:  []string{"charmers"},
			Write: []string{"charmers"},
		},
		mongodoc.DevelopmentChannel: {
			Read:  []string{"charmers"},
			Write: []string{"charmers"},
		},
		mongodoc.StableChannel: {
			Read:  []string{"charmers"},
			Write: []string{"charmers"},
		},
	})

	// Change the read perms to only include a specific user and the
	// published write perms to include an "admin" user.
	// Because the entity isn't published yet, the unpublished channel ACLs
	// will be changed.
	s.assertPut(c, "precise/wordpress-23/meta/perm/read", []string{"bob"})
	s.assertPut(c, "precise/wordpress-23/meta/perm/write", []string{"admin"})

	// The permissions are only for bob now, so act as bob.
	s.discharge = dischargeForUser("bob")

	// Check that the perms have changed for all revisions and series.
	for i, u := range []string{"precise/wordpress-23", "precise/wordpress-24", "trusty/wordpress-1"} {
		c.Logf("id %d: %q", i, u)
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler: s.srv,
			Do:      bakeryDo(nil),
			URL:     storeURL(u + "/meta/perm"),
			ExpectBody: params.PermResponse{
				Read:  []string{"bob"},
				Write: []string{"admin"},
			},
		})
	}
	e, err = s.store.FindBaseEntity(charm.MustParseURL("precise/wordpress-23"), nil)
	c.Assert(err, gc.IsNil)
	c.Assert(e.ChannelACLs, gc.DeepEquals, map[mongodoc.Channel]mongodoc.ACL{
		mongodoc.UnpublishedChannel: {
			Read:  []string{"bob"},
			Write: []string{"admin"},
		},
		mongodoc.DevelopmentChannel: {
			Read:  []string{"charmers"},
			Write: []string{"charmers"},
		},
		mongodoc.StableChannel: {
			Read:  []string{"charmers"},
			Write: []string{"charmers"},
		},
	})

	// Publish one of the revisions to development and check that the development ACLs
	// have changed.
	err = s.store.Publish(newResolvedURL("~charmers/precise/wordpress-23", 23), mongodoc.DevelopmentChannel)
	c.Assert(err, gc.IsNil)
	s.assertPut(c, "precise/wordpress-23/meta/perm/read", []string{"bob", "charlie"})

	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler: s.srv,
		Do:      bakeryDo(nil),
		URL:     storeURL("precise/wordpress-23/meta/perm"),
		ExpectBody: params.PermResponse{
			Read:  []string{"bob", "charlie"},
			Write: []string{"charmers"},
		},
	})

	// The other revisions should still see the old ACLs.
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler: s.srv,
		Do:      bakeryDo(nil),
		URL:     storeURL("precise/wordpress-24/meta/perm"),
		ExpectBody: params.PermResponse{
			Read:  []string{"bob"},
			Write: []string{"admin"},
		},
	})

	e, err = s.store.FindBaseEntity(charm.MustParseURL("precise/wordpress-23"), nil)
	c.Assert(err, gc.IsNil)
	c.Assert(e.ChannelACLs, gc.DeepEquals, map[mongodoc.Channel]mongodoc.ACL{
		mongodoc.UnpublishedChannel: {
			Read:  []string{"bob"},
			Write: []string{"admin"},
		},
		mongodoc.DevelopmentChannel: {
			Read:  []string{"bob", "charlie"},
			Write: []string{"charmers"},
		},
		mongodoc.StableChannel: {
			Read:  []string{"charmers"},
			Write: []string{"charmers"},
		},
	})

	// The stable permissions only allow charmers currently, so act as
	// charmers again.
	s.discharge = dischargeForUser("charmers")

	// Publish one of the revisions to stable and check that the stable ACLs
	// have changed.
	err = s.store.Publish(newResolvedURL("~charmers/trusty/wordpress-1", 1), mongodoc.StableChannel)
	c.Assert(err, gc.IsNil)
	s.assertPut(c, "trusty/wordpress-1/meta/perm/write", []string{"doris"})

	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler: s.srv,
		Do:      bakeryDo(nil),
		URL:     storeURL("~charmers/trusty/wordpress-1/meta/perm"),
		ExpectBody: params.PermResponse{
			Read:  []string{"charmers"},
			Write: []string{"doris"},
		},
	})

	// The other revisions should still see the old ACLs.
	s.discharge = dischargeForUser("bob")
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler: s.srv,
		Do:      bakeryDo(nil),
		URL:     storeURL("precise/wordpress-24/meta/perm"),
		ExpectBody: params.PermResponse{
			Read:  []string{"bob"},
			Write: []string{"admin"},
		},
	})

	// The development-channel entity should still see the development ACLS.
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler: s.srv,
		Do:      bakeryDo(nil),
		URL:     storeURL("precise/wordpress-23/meta/perm"),
		ExpectBody: params.PermResponse{
			Read:  []string{"bob", "charlie"},
			Write: []string{"charmers"},
		},
	})

	e, err = s.store.FindBaseEntity(charm.MustParseURL("trusty/wordpress-1"), nil)
	c.Assert(err, gc.IsNil)
	c.Assert(e.ChannelACLs, jc.DeepEquals, map[mongodoc.Channel]mongodoc.ACL{
		mongodoc.UnpublishedChannel: {
			Read:  []string{"bob"},
			Write: []string{"admin"},
		},
		mongodoc.DevelopmentChannel: {
			Read:  []string{"bob", "charlie"},
			Write: []string{"charmers"},
		},
		mongodoc.StableChannel: {
			Read:  []string{"charmers"},
			Write: []string{"doris"},
		},
	})

	// Try restoring everyone's read permission on the charm.
	// Note: wordpress resolves to trusty/wordpress-1 here because
	// trusty is a later LTS series than precise.
	s.assertPut(c, "wordpress/meta/perm/read", []string{"bob", params.Everyone})
	s.assertGet(c, "wordpress/meta/perm", params.PermResponse{
		Read:  []string{"bob", params.Everyone},
		Write: []string{"doris"},
	})
	s.assertGet(c, "wordpress/meta/perm/read", []string{"bob", params.Everyone})
	e, err = s.store.FindBaseEntity(charm.MustParseURL("trusty/wordpress-1"), nil)
	c.Assert(err, gc.IsNil)
	c.Assert(e.ChannelACLs, jc.DeepEquals, map[mongodoc.Channel]mongodoc.ACL{
		mongodoc.UnpublishedChannel: {
			Read:  []string{"bob"},
			Write: []string{"admin"},
		},
		mongodoc.DevelopmentChannel: {
			Read:  []string{"bob", "charlie"},
			Write: []string{"charmers"},
		},
		mongodoc.StableChannel: {
			Read:  []string{"bob", params.Everyone},
			Write: []string{"doris"},
		},
	})

	// Try deleting all permissions.
	s.assertPut(c, "wordpress/meta/perm/read", []string{})
	s.assertPut(c, "wordpress/meta/perm/write", []string{})

	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		Do:           bakeryDo(nil),
		URL:          storeURL("wordpress/meta/perm"),
		ExpectStatus: http.StatusUnauthorized,
		ExpectBody: params.Error{
			Code:    params.ErrUnauthorized,
			Message: `unauthorized: access denied for user "bob"`,
		},
	})
	e, err = s.store.FindBaseEntity(charm.MustParseURL("trusty/wordpress-1"), nil)
	c.Assert(err, gc.IsNil)
	c.Assert(e.ChannelACLs, gc.DeepEquals, map[mongodoc.Channel]mongodoc.ACL{
		mongodoc.UnpublishedChannel: {
			Read:  []string{"bob"},
			Write: []string{"admin"},
		},
		mongodoc.DevelopmentChannel: {
			Read:  []string{"bob", "charlie"},
			Write: []string{"charmers"},
		},
		mongodoc.StableChannel: {
			Read:  []string{},
			Write: []string{},
		},
	})

	// Try setting all permissions in one request.
	s.assertPut(c, "wordpress/meta/perm", params.PermRequest{
		Read:  []string{"bob"},
		Write: []string{"admin"},
	})
	e, err = s.store.FindBaseEntity(charm.MustParseURL("trusty/wordpress-1"), nil)
	c.Assert(err, gc.IsNil)
	c.Assert(e.ChannelACLs, jc.DeepEquals, map[mongodoc.Channel]mongodoc.ACL{
		mongodoc.UnpublishedChannel: {
			Read:  []string{"bob"},
			Write: []string{"admin"},
		},
		mongodoc.DevelopmentChannel: {
			Read:  []string{"bob", "charlie"},
			Write: []string{"charmers"},
		},
		mongodoc.StableChannel: {
			Read:  []string{"bob"},
			Write: []string{"admin"},
		},
	})

	// Try putting only read permissions.
	readRequest := struct {
		Read []string
	}{Read: []string{"joe"}}
	s.assertPut(c, "wordpress/meta/perm", readRequest)
	e, err = s.store.FindBaseEntity(charm.MustParseURL("trusty/wordpress-1"), nil)
	c.Assert(err, gc.IsNil)
	c.Assert(e.ChannelACLs, jc.DeepEquals, map[mongodoc.Channel]mongodoc.ACL{
		mongodoc.UnpublishedChannel: {
			Read:  []string{"bob"},
			Write: []string{"admin"},
		},
		mongodoc.DevelopmentChannel: {
			Read:  []string{"bob", "charlie"},
			Write: []string{"charmers"},
		},
		mongodoc.StableChannel: {
			Read:  []string{"joe"},
			Write: []string{},
		},
	})
}

func (s *APISuite) TestMetaPermPutUnauthorized(c *gc.C) {
	id := "precise/wordpress-23"
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("~charmers/"+id, 23))
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler: s.noMacaroonSrv,
		URL:     storeURL("~charmers/" + id + "/meta/perm/read"),
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

func (s *APISuite) TestMetaTerms(c *gc.C) {
	id1 := "precise/terms-17"
	s.addPublicCharmFromRepo(c, "terms", newResolvedURL("~charmers/"+id1, 17))
	s.assertGet(c, id1+"/meta/terms", []string{"terms-1/1", "terms-2/5"})

	id2 := "precise/mysql-1"
	s.addPublicCharmFromRepo(c, "mysql", newResolvedURL("~charmers/"+id2, 1))
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL(id2 + "meta/terms"),
		Method:       "GET",
		ExpectStatus: http.StatusNotFound,
		ExpectBody: params.Error{
			Code:    params.ErrNotFound,
			Message: "not found",
		},
	})
}

func (s *APISuite) TestMetaTermsBundle(c *gc.C) {
	id := newResolvedURL("~charmers/bundle/wordpress-simple-10", 10)
	s.addPublicBundleFromRepo(c, "wordpress-simple", id, true)

	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL(id.URL.Path() + "/meta/terms"),
		Method:       "GET",
		ExpectStatus: http.StatusNotFound,
		ExpectBody: params.Error{
			Code:    params.ErrMetadataNotFound,
			Message: "metadata not found",
		},
	})
}

func (s *APISuite) TestSeries(c *gc.C) {
	for k := range series.Series {
		if k == "bundle" {
			continue
		}
		id := k + "/wordpress-23"
		s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("~charmers/"+id, 23))
		s.assertGet(c, id+"/meta/id", map[string]interface{}{
			"Id":       "cs:" + k + "/wordpress-23",
			"Series":   k,
			"Name":     "wordpress",
			"Revision": 23,
		})
	}
}

func (s *APISuite) TestExtraInfo(c *gc.C) {
	id := "precise/wordpress-23"
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("~charmers/"+id, 23))
	s.checkInfo(c, "extra-info", id)
	s.checkInfo(c, "common-info", id)
}

func (s *APISuite) checkInfo(c *gc.C, path string, id string) {
	// Add one value and check that it's there.
	s.assertPut(c, id+"/meta/"+path+"/foo", "fooval")
	s.assertGet(c, id+"/meta/"+path+"/foo", "fooval")
	s.assertGet(c, id+"/meta/"+path, map[string]string{
		"foo": "fooval",
	})

	// Add another value and check that both values are there.
	s.assertPut(c, id+"/meta/"+path+"/bar", "barval")
	s.assertGet(c, id+"/meta/"+path+"/bar", "barval")
	s.assertGet(c, id+"/meta/"+path, map[string]string{
		"foo": "fooval",
		"bar": "barval",
	})

	// Overwrite a value and check that it's changed.
	s.assertPut(c, id+"/meta/"+path+"/foo", "fooval2")
	s.assertGet(c, id+"/meta/"+path+"/foo", "fooval2")
	s.assertGet(c, id+"/meta/"+path+"", map[string]string{
		"foo": "fooval2",
		"bar": "barval",
	})

	// Write several values at once.
	s.assertPut(c, id+"/meta/any", params.MetaAnyResponse{
		Meta: map[string]interface{}{
			path: map[string]string{
				"foo": "fooval3",
				"baz": "bazval",
			},
			path + "/frob": []int{1, 4, 6},
		},
	})
	s.assertGet(c, id+"/meta/"+path, map[string]interface{}{
		"foo":  "fooval3",
		"baz":  "bazval",
		"bar":  "barval",
		"frob": []int{1, 4, 6},
	})

	// Delete a single value.
	s.assertPut(c, id+"/meta/"+path+"/foo", nil)
	s.assertGet(c, id+"/meta/"+path, map[string]interface{}{
		"baz":  "bazval",
		"bar":  "barval",
		"frob": []int{1, 4, 6},
	})

	// Delete a value and add some values at the same time.
	s.assertPut(c, id+"/meta/any", params.MetaAnyResponse{
		Meta: map[string]interface{}{
			path: map[string]interface{}{
				"baz":    nil,
				"bar":    nil,
				"dazzle": "x",
				"fizzle": "y",
			},
		},
	})
	s.assertGet(c, id+"/meta/"+path, map[string]interface{}{
		"frob":   []int{1, 4, 6},
		"dazzle": "x",
		"fizzle": "y",
	})
}

var extraInfoBadPutRequestsTests = []struct {
	about        string
	key          string
	body         interface{}
	contentType  string
	expectStatus int
	expectBody   params.Error
}{{
	about:        "key with extra element",
	key:          "foo/bar",
	body:         "hello",
	expectStatus: http.StatusBadRequest,
	expectBody: params.Error{
		Code:    params.ErrBadRequest,
		Message: "bad key for $1",
	},
}, {
	about:        "key with a dot",
	key:          "foo.bar",
	body:         "hello",
	expectStatus: http.StatusBadRequest,
	expectBody: params.Error{
		Code:    params.ErrBadRequest,
		Message: "bad key for $1",
	},
}, {
	about:        "key with a dollar",
	key:          "foo$bar",
	body:         "hello",
	expectStatus: http.StatusBadRequest,
	expectBody: params.Error{
		Code:    params.ErrBadRequest,
		Message: "bad key for $1",
	},
}, {
	about: "multi key with extra element",
	key:   "",
	body: map[string]string{
		"foo/bar": "value",
	},
	expectStatus: http.StatusBadRequest,
	expectBody: params.Error{
		Code:    params.ErrBadRequest,
		Message: "bad key for $1",
	},
}, {
	about: "multi key with dot",
	key:   "",
	body: map[string]string{
		".bar": "value",
	},
	expectStatus: http.StatusBadRequest,
	expectBody: params.Error{
		Code:    params.ErrBadRequest,
		Message: "bad key for $1",
	},
}, {
	about: "multi key with dollar",
	key:   "",
	body: map[string]string{
		"$bar": "value",
	},
	expectStatus: http.StatusBadRequest,
	expectBody: params.Error{
		Code:    params.ErrBadRequest,
		Message: "bad key for $1",
	},
}, {
	about:        "multi key with bad map",
	key:          "",
	body:         "bad",
	expectStatus: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: `cannot unmarshal $1 body: json: cannot unmarshal string into Go value of type map[string]*json.RawMessage`,
	},
}}

func (s *APISuite) TestExtraInfoBadPutRequests(c *gc.C) {
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/precise/wordpress-23", 23))
	path := "precise/wordpress-23/meta/"
	for i, test := range extraInfoBadPutRequestsTests {
		c.Logf("test %d: %s", i, test.about)
		contentType := test.contentType
		if contentType == "" {
			contentType = "application/json"
		}
		extraBodyMessage := strings.Replace(test.expectBody.Message, "$1", "extra-info", -1)
		commonBodyMessage := strings.Replace(test.expectBody.Message, "$1", "common-info", -1)
		test.expectBody.Message = extraBodyMessage
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler: s.srv,
			URL:     storeURL(path + "extra-info/" + test.key),
			Method:  "PUT",
			Header: http.Header{
				"Content-Type": {contentType},
			},
			Username:     testUsername,
			Password:     testPassword,
			Body:         strings.NewReader(mustMarshalJSON(test.body)),
			ExpectStatus: test.expectStatus,
			ExpectBody:   test.expectBody,
		})
		test.expectBody.Message = commonBodyMessage
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler: s.srv,
			URL:     storeURL(path + "common-info/" + test.key),
			Method:  "PUT",
			Header: http.Header{
				"Content-Type": {contentType},
			},
			Username:     testUsername,
			Password:     testPassword,
			Body:         strings.NewReader(mustMarshalJSON(test.body)),
			ExpectStatus: test.expectStatus,
			ExpectBody:   test.expectBody,
		})
	}
}

func (s *APISuite) TestExtraInfoPutUnauthorized(c *gc.C) {
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/precise/wordpress-23", 23))
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
		ExpectStatus: http.StatusProxyAuthRequired,
		ExpectBody:   dischargeRequiredBody,
	})
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler: s.srv,
		URL:     storeURL("precise/wordpress-23/meta/extra-info"),
		Method:  "PUT",
		Header: http.Header{
			"Content-Type":            {"application/json"},
			"Bakery-Protocol-Version": {"1"},
		},
		Body: strings.NewReader(mustMarshalJSON(map[string]string{
			"bar": "value",
		})),
		ExpectStatus: http.StatusUnauthorized,
		ExpectHeader: http.Header{
			"WWW-Authenticate": {"Macaroon"},
		},
		ExpectBody: dischargeRequiredBody,
	})
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler: s.srv,
		URL:     storeURL("precise/wordpress-23/meta/common-info"),
		Method:  "PUT",
		Header: http.Header{
			"Content-Type": {"application/json"},
		},
		Body: strings.NewReader(mustMarshalJSON(map[string]string{
			"bar": "value",
		})),
		ExpectStatus: http.StatusProxyAuthRequired,
		ExpectBody:   dischargeRequiredBody,
	})
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler: s.srv,
		URL:     storeURL("precise/wordpress-23/meta/common-info"),
		Method:  "PUT",
		Header: http.Header{
			"Content-Type":            {"application/json"},
			"Bakery-Protocol-Version": {"1"},
		},
		Body: strings.NewReader(mustMarshalJSON(map[string]string{
			"bar": "value",
		})),
		ExpectStatus: http.StatusUnauthorized,
		ExpectHeader: http.Header{
			"WWW-Authenticate": {"Macaroon"},
		},
		ExpectBody: dischargeRequiredBody,
	})
}

func (s *APISuite) TestCommonInfo(c *gc.C) {
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("~charmers/precise/wordpress-23", 23))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("~charmers/precise/wordpress-24", 24))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("~charmers/trusty/wordpress-1", 1))

	s.assertPut(c, "wordpress/meta/common-info/key", "something")

	s.assertGet(c, "wordpress/meta/common-info", map[string]string{
		"key": "something",
	})
	for i, u := range []string{"precise/wordpress-23", "precise/wordpress-24", "trusty/wordpress-1"} {
		c.Logf("id %d: %q", i, u)
		s.assertGet(c, u+"/meta/common-info", map[string]string{
			"key": "something",
		})
		e, err := s.store.FindBaseEntity(charm.MustParseURL(u), nil)
		c.Assert(err, gc.IsNil)
		c.Assert(e.CommonInfo, gc.DeepEquals, map[string][]byte{
			"key": []byte("\"something\""),
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
	rurls := s.addTestEntities(c)
	// We check the meta endpoint for both promulgated and non-promulgated
	// versions of each URL.
	urls := make([]*router.ResolvedURL, 0, len(rurls)*2)
	for _, rurl := range rurls {
		urls = append(urls, rurl)
		if rurl.PromulgatedRevision != -1 {
			rurl1 := *rurl
			rurl1.PromulgatedRevision = -1
			urls = append(urls, &rurl1)
		}
	}
	for _, url := range urls {
		charmId := strings.TrimPrefix(url.String(), "cs:")
		var flags []string
		expectData := params.MetaAnyResponse{
			Id:   url.PreferredURL(),
			Meta: make(map[string]interface{}),
		}
		for _, ep := range metaEndpoints {
			flags = append(flags, "include="+ep.name)
			isBundle := url.URL.Series == "bundle"
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
	wordpressURL, _ := s.addPublicCharmFromRepo(
		c,
		"wordpress",
		newResolvedURL("cs:~charmers/precise/wordpress-23", 23),
	)
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("precise/wordpress-1/meta/any"),
		ExpectStatus: http.StatusNotFound,
		ExpectBody: params.Error{
			Code:    params.ErrNotFound,
			Message: `no matching charm or bundle for "cs:precise/wordpress-1"`,
		},
	})
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("meta/any?id=precise/wordpress-23&id=precise/wordpress-1"),
		ExpectStatus: http.StatusOK,
		ExpectBody: map[string]interface{}{
			"precise/wordpress-23": params.MetaAnyResponse{
				Id: wordpressURL.PreferredURL(),
			},
		},
	})
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("precise/wordpress-23/meta/any"),
		ExpectStatus: http.StatusOK,
		ExpectBody: params.MetaAnyResponse{
			Id: wordpressURL.PreferredURL(),
		},
	})
}

// In this test we rely on the charm.v2 testing repo package and
// dummy charm that has actions included.
func (s *APISuite) TestMetaCharmActions(c *gc.C) {
	url, dummy := s.addPublicCharmFromRepo(c, "dummy", newResolvedURL("cs:~charmers/precise/dummy-10", 10))
	s.assertGet(c, "precise/dummy-10/meta/charm-actions", dummy.Actions())
	s.assertGet(c, "precise/dummy-10/meta/any?include=charm-actions",
		params.MetaAnyResponse{
			Id: url.PreferredURL(),
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

	_, wordpress := s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/precise/wordpress-23", 23))
	_, mysql := s.addPublicCharmFromRepo(c, "mysql", newResolvedURL("cs:~charmers/precise/mysql-10", 10))
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

	wordpressURL, wordpress := s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/precise/wordpress-23", 23))
	mysqlURL, mysql := s.addPublicCharmFromRepo(c, "mysql", newResolvedURL("cs:~charmers/precise/mysql-10", 10))
	s.assertGet(c,
		"meta/any?include=charm-metadata&include=charm-config&id=precise/wordpress-23&id=precise/mysql-10",
		map[string]params.MetaAnyResponse{
			"precise/wordpress-23": {
				Id: wordpressURL.PreferredURL(),
				Meta: map[string]interface{}{
					"charm-config":   wordpress.Config(),
					"charm-metadata": wordpress.Meta(),
				},
			},
			"precise/mysql-10": {
				Id: mysqlURL.PreferredURL(),
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
	url := newResolvedURL("~charmers/precise/wordpress-0", -1)
	for i, test := range metaCharmTagsTests {
		c.Logf("%d: %s", i, test.about)
		url.URL.Revision = i
		s.addPublicCharm(c, storetesting.NewCharm(&charm.Meta{
			Tags:       test.tags,
			Categories: test.categories,
		}), url)
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      s.srv,
			URL:          storeURL(url.URL.Path() + "/meta/tags"),
			ExpectStatus: http.StatusOK,
			ExpectBody:   params.TagsResponse{test.expectTags},
		})
	}
}

func (s *APISuite) TestPromulgatedMetaCharmTags(c *gc.C) {
	url := newResolvedURL("~charmers/precise/wordpress-0", 0)
	for i, test := range metaCharmTagsTests {
		c.Logf("%d: %s", i, test.about)
		url.URL.Revision = i
		url.PromulgatedRevision = i
		s.addPublicCharm(c, storetesting.NewCharm(&charm.Meta{
			Tags:       test.tags,
			Categories: test.categories,
		}), url)
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      s.srv,
			URL:          storeURL(url.URL.Path() + "/meta/tags"),
			ExpectStatus: http.StatusOK,
			ExpectBody:   params.TagsResponse{test.expectTags},
		})
	}
}

func (s *APISuite) TestBundleTags(c *gc.C) {
	url := newResolvedURL("~charmers/bundle/wordpress-simple-2", -1)
	s.addPublicBundle(c, storetesting.NewBundle(&charm.BundleData{
		Tags: []string{"foo", "bar"},
		Services: map[string]*charm.ServiceSpec{
			"wordpress": {
				Charm: "wordpress",
			},
		},
	}), url, true)
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL(url.URL.Path() + "/meta/tags"),
		ExpectStatus: http.StatusOK,
		ExpectBody:   params.TagsResponse{[]string{"foo", "bar"}},
	})
}

func (s *APISuite) TestPromulgatedBundleTags(c *gc.C) {
	url := newResolvedURL("~charmers/bundle/wordpress-simple-2", 2)
	s.addPublicBundle(c, storetesting.NewBundle(&charm.BundleData{
		Tags: []string{"foo", "bar"},
		Services: map[string]*charm.ServiceSpec{
			"wordpress": {
				Charm: "wordpress",
			},
		},
	}), url, true)
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL(url.URL.Path() + "/meta/tags"),
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
	_, wordpress := s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/precise/wordpress-23", 23))
	s.assertGet(c, "wordpress/meta/charm-metadata", wordpress.Meta())
}

func (s *APISuite) TestMetaCharmNotFound(c *gc.C) {
	for i, ep := range metaEndpoints {
		c.Logf("test %d: %s", i, ep.name)
		expected := params.Error{
			Message: `no matching charm or bundle for "cs:precise/wordpress-23"`,
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
	expect   *router.ResolvedURL
	notFound bool
}{{
	url:    "wordpress",
	expect: newResolvedURL("cs:~charmers/trusty/wordpress-25", 25),
}, {
	url:    "precise/wordpress",
	expect: newResolvedURL("cs:~charmers/precise/wordpress-24", 24),
}, {
	url:    "utopic/bigdata",
	expect: newResolvedURL("cs:~charmers/utopic/bigdata-10", 10),
}, {
	url:    "~charmers/precise/wordpress",
	expect: newResolvedURL("cs:~charmers/precise/wordpress-24", -1),
}, {
	url:      "~charmers/precise/wordpress-99",
	notFound: true,
}, {
	url:    "~charmers/wordpress",
	expect: newResolvedURL("cs:~charmers/trusty/wordpress-25", -1),
}, {
	url:      "~charmers/wordpress-24",
	notFound: true,
}, {
	url:    "~bob/wordpress",
	expect: newResolvedURL("cs:~bob/trusty/wordpress-1", -1),
}, {
	url:    "~bob/precise/wordpress",
	expect: newResolvedURL("cs:~bob/precise/wordpress-2", -1),
}, {
	url:    "bigdata",
	expect: newResolvedURL("cs:~charmers/utopic/bigdata-10", 10),
}, {
	url:      "wordpress-24",
	notFound: true,
}, {
	url:    "bundlelovin",
	expect: newResolvedURL("cs:~charmers/bundle/bundlelovin-10", 10),
}, {
	url:      "wordpress-26",
	notFound: true,
}, {
	url:      "foo",
	notFound: true,
}, {
	url:      "trusty/bigdata",
	notFound: true,
}, {
	url:      "~bob/wily/django-47",
	notFound: true,
}, {
	url:      "~bob/django",
	notFound: true,
}, {
	url:      "wily/django",
	notFound: true,
}, {
	url:      "django",
	notFound: true,
}, {
	url:    "~bob/multi-series",
	expect: newResolvedURL("cs:~bob/multi-series-0", -1),
}, {
	url:    "~bob/utopic/multi-series",
	expect: newResolvedURL("cs:~bob/multi-series-0", -1),
}}

func (s *APISuite) TestResolveURL(c *gc.C) {
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/precise/wordpress-23", 23))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/precise/wordpress-24", 24))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/trusty/wordpress-24", 24))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/trusty/wordpress-25", 25))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/utopic/wordpress-10", 10))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/saucy/bigdata-99", 99))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/utopic/bigdata-10", 10))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~bob/trusty/wordpress-1", -1))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~bob/precise/wordpress-2", -1))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~bob/precise/other-2", -1))
	s.addPublicBundleFromRepo(c, "wordpress-simple", newResolvedURL("cs:~charmers/bundle/bundlelovin-10", 10), true)
	s.addPublicBundleFromRepo(c, "wordpress-simple", newResolvedURL("cs:~charmers/bundle/wordpress-simple-10", 10), true)
	s.addPublicCharmFromRepo(c, "multi-series", newResolvedURL("cs:~bob/multi-series-0", -1))

	for i, test := range resolveURLTests {
		c.Logf("test %d: %s", i, test.url)
		url := charm.MustParseURL(test.url)
		rurl, err := v5.ResolveURL(entitycache.New(&v5.StoreWithChannel{
			Store:   s.store,
			Channel: mongodoc.UnpublishedChannel,
		}), url)
		if test.notFound {
			c.Assert(errgo.Cause(err), gc.Equals, params.ErrNotFound)
			c.Assert(err, gc.ErrorMatches, `no matching charm or bundle for ".*"`)
			c.Assert(rurl, gc.IsNil)
			continue
		}
		c.Assert(err, gc.IsNil)
		c.Assert(rurl, jc.DeepEquals, test.expect)
	}
}

var serveExpandIdTests = []struct {
	about  string
	url    string
	expect []params.ExpandedId
	err    string
}{{
	about: "fully qualified URL",
	url:   "~charmers/trusty/wordpress-47",
	expect: []params.ExpandedId{
		{Id: "cs:~charmers/utopic/wordpress-42"},
		{Id: "cs:~charmers/trusty/wordpress-47"},
		{Id: "cs:~charmers/wordpress-5"},
	},
}, {
	about: "promulgated URL",
	url:   "trusty/wordpress-47",
	expect: []params.ExpandedId{
		{Id: "cs:utopic/wordpress-42"},
		{Id: "cs:trusty/wordpress-47"},
		{Id: "cs:wordpress-49"},
	},
}, {
	about: "non-promulgated charm",
	url:   "~bob/precise/builder",
	expect: []params.ExpandedId{
		{Id: "cs:~bob/precise/builder-5"},
	},
}, {
	about: "partial URL",
	url:   "haproxy",
	expect: []params.ExpandedId{
		{Id: "cs:trusty/haproxy-1"},
		{Id: "cs:precise/haproxy-1"},
	},
}, {
	about: "revision with series matches bundles (and multi-series charms) only",
	url:   "mongo-0",
	expect: []params.ExpandedId{
		{Id: "cs:bundle/mongo-0"},
	},
}, {
	about: "single result",
	url:   "bundle/mongo-0",
	expect: []params.ExpandedId{
		{Id: "cs:bundle/mongo-0"},
	},
}, {
	about: "fully qualified URL with no entities found",
	url:   "~charmers/precise/no-such-42",
	err:   `no matching charm or bundle for "cs:~charmers/precise/no-such-42"`,
}, {
	about: "partial URL with no entities found",
	url:   "no-such",
	err:   `no matching charm or bundle for "cs:no-such"`,
}}

func (s *APISuite) TestServeExpandId(c *gc.C) {
	// Add a bunch of entities in the database.
	// Note that expand-id only cares about entity identifiers,
	// so it is ok to reuse the same charm for all the entities.
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/utopic/wordpress-42", 42))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/trusty/wordpress-47", 47))
	err := s.store.AddCharmWithArchive(newResolvedURL("cs:~charmers/trusty/wordpress-48", 48), storetesting.NewCharm(nil))
	c.Assert(err, gc.IsNil)
	err = s.store.Publish(newResolvedURL("cs:~charmers/trusty/wordpress-48", 48), mongodoc.DevelopmentChannel)
	c.Assert(err, gc.IsNil)
	s.addPublicCharmFromRepo(c, "multi-series", newResolvedURL("cs:~charmers/wordpress-5", 49))

	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/precise/haproxy-1", 1))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/trusty/haproxy-1", 1))

	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~bob/precise/builder-5", -1))

	s.addPublicBundleFromRepo(c, "wordpress-simple", newResolvedURL("cs:~charmers/bundle/mongo-0", 0), true)
	s.addPublicBundleFromRepo(c, "wordpress-simple", newResolvedURL("cs:~charmers/bundle/wordpress-simple-0", 0), true)

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
	asUser string
	expect params.RevisionInfoResponse
	err    string
}{{
	about: "fully qualified url",
	url:   "trusty/wordpress-9",
	expect: params.RevisionInfoResponse{
		[]*charm.URL{
			charm.MustParseURL("cs:trusty/wordpress-43"),
			charm.MustParseURL("cs:trusty/wordpress-42"),
			charm.MustParseURL("cs:trusty/wordpress-41"),
			charm.MustParseURL("cs:trusty/wordpress-9"),
		},
	},
}, {
	about: "partial url uses a default series",
	url:   "wordpress",
	expect: params.RevisionInfoResponse{
		[]*charm.URL{
			charm.MustParseURL("cs:trusty/wordpress-43"),
			charm.MustParseURL("cs:trusty/wordpress-42"),
			charm.MustParseURL("cs:trusty/wordpress-41"),
			charm.MustParseURL("cs:trusty/wordpress-9"),
		},
	},
}, {
	about: "non-promulgated URL gives non-promulgated revisions (~charmers)",
	url:   "~charmers/trusty/cinder",
	expect: params.RevisionInfoResponse{
		[]*charm.URL{
			charm.MustParseURL("cs:~charmers/trusty/cinder-6"),
			charm.MustParseURL("cs:~charmers/trusty/cinder-5"),
			charm.MustParseURL("cs:~charmers/trusty/cinder-4"),
			charm.MustParseURL("cs:~charmers/trusty/cinder-3"),
			charm.MustParseURL("cs:~charmers/trusty/cinder-2"),
			charm.MustParseURL("cs:~charmers/trusty/cinder-1"),
			charm.MustParseURL("cs:~charmers/trusty/cinder-0"),
		},
	},
}, {
	about: "non-promulgated URL gives non-promulgated revisions (~openstack-charmers)",
	url:   "~openstack-charmers/trusty/cinder",
	expect: params.RevisionInfoResponse{
		[]*charm.URL{
			charm.MustParseURL("cs:~openstack-charmers/trusty/cinder-1"),
			charm.MustParseURL("cs:~openstack-charmers/trusty/cinder-0"),
		},
	},
}, {
	about: "promulgated URL gives promulgated revisions",
	url:   "trusty/cinder",
	expect: params.RevisionInfoResponse{
		[]*charm.URL{
			charm.MustParseURL("cs:trusty/cinder-5"),
			charm.MustParseURL("cs:trusty/cinder-4"),
			charm.MustParseURL("cs:trusty/cinder-3"),
			charm.MustParseURL("cs:trusty/cinder-2"),
			charm.MustParseURL("cs:trusty/cinder-1"),
			charm.MustParseURL("cs:trusty/cinder-0"),
		},
	},
}, {
	about: "multi-series charm expands to all revisions of that charm",
	url:   "multi-series",
	expect: params.RevisionInfoResponse{
		[]*charm.URL{
			charm.MustParseURL("cs:multi-series-41"),
			charm.MustParseURL("cs:multi-series-40"),
		},
	},
}, {
	about: "multi-series charm with series specified",
	url:   "trusty/multi-series",
	expect: params.RevisionInfoResponse{
		[]*charm.URL{
			charm.MustParseURL("cs:multi-series-41"),
			charm.MustParseURL("cs:multi-series-40"),
		},
	},
}, {
	about: "multi-series charm with non-promulgated URL",
	url:   "~charmers/multi-series",
	expect: params.RevisionInfoResponse{
		[]*charm.URL{
			charm.MustParseURL("cs:~charmers/multi-series-2"),
			charm.MustParseURL("cs:~charmers/multi-series-1"),
		},
	},
}, {
	about: "multi-series charm with non-promulgated URL and series specified",
	url:   "~charmers/utopic/multi-series",
	expect: params.RevisionInfoResponse{
		[]*charm.URL{
			charm.MustParseURL("cs:~charmers/multi-series-2"),
			charm.MustParseURL("cs:~charmers/multi-series-1"),
		},
	},
}, {
	about: "mixed multi/single series charm, latest rev",
	url:   "mixed",
	expect: params.RevisionInfoResponse{
		[]*charm.URL{
			charm.MustParseURL("cs:mixed-43"),
			charm.MustParseURL("cs:mixed-42"),
			charm.MustParseURL("cs:trusty/mixed-41"),
			charm.MustParseURL("cs:trusty/mixed-40"),
		},
	},
}, {
	about: "mixed multi/single series charm with series",
	url:   "trusty/mixed-40",
	expect: params.RevisionInfoResponse{
		[]*charm.URL{
			charm.MustParseURL("cs:mixed-43"),
			charm.MustParseURL("cs:mixed-42"),
			charm.MustParseURL("cs:trusty/mixed-41"),
			charm.MustParseURL("cs:trusty/mixed-40"),
		},
	},
}, {
	about: "no entities found",
	url:   "precise/no-such-33",
	err:   `no matching charm or bundle for "cs:precise/no-such-33"`,
}}

func (s *APISuite) TestServeMetaRevisionInfo(c *gc.C) {
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/trusty/mysql-41", 41))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/trusty/mysql-42", 42))

	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/trusty/wordpress-9", 9))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/trusty/wordpress-41", 41))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/trusty/wordpress-42", 42))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/trusty/wordpress-43", 43))

	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/precise/wordpress-42", 42))

	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/trusty/cinder-0", -1))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/trusty/cinder-1", -1))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/trusty/cinder-2", 0))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/trusty/cinder-3", 1))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~openstack-charmers/trusty/cinder-0", 2))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~openstack-charmers/trusty/cinder-1", 3))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/trusty/cinder-4", -1))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/trusty/cinder-5", 4))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/trusty/cinder-6", 5))

	s.addPublicCharmFromRepo(c, "multi-series", newResolvedURL("cs:~charmers/multi-series-1", 40))
	s.addPublicCharmFromRepo(c, "multi-series", newResolvedURL("cs:~charmers/multi-series-2", 41))

	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/trusty/mixed-1", 40))
	s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~charmers/trusty/mixed-2", 41))
	s.addPublicCharmFromRepo(c, "multi-series", newResolvedURL("cs:~charmers/mixed-3", 42))
	s.addPublicCharmFromRepo(c, "multi-series", newResolvedURL("cs:~charmers/mixed-4", 43))

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
		do := bakeryDo(nil)
		if test.asUser != "" {
			do = s.bakeryDoAsUser(c, test.asUser)
		}
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      s.srv,
			URL:          storeURL,
			Do:           do,
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
	// value is the number of downloads performed that day.
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
		"utopic/django-42": {200: 3, 27: 4, 3: 5},
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
		"bundle/django-simple-2": {200: 3, 27: 4, 3: 5},
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
		"utopic/rails-47": {200: 3, 27: 4, 3: 5},
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
			url := &router.ResolvedURL{
				URL:                 *charm.MustParseURL(id),
				PromulgatedRevision: -1,
			}
			if url.URL.User == "" {
				url.URL.User = "charmers"
				url.PromulgatedRevision = url.URL.Revision
			}

			// Add the required entities to the database.
			if url.URL.Series == "bundle" {
				s.addPublicBundleFromRepo(c, "wordpress-simple", url, true)
			} else {
				s.addPublicCharmFromRepo(c, "wordpress", url)
			}

			// Simulate the entity was downloaded at the specified dates.
			for daysAgo, downloads := range downloadsPerDay {
				date := today.AddDate(0, 0, -daysAgo)
				key := []string{params.StatsArchiveDownload, url.URL.Series, url.URL.Name, url.URL.User, strconv.Itoa(url.URL.Revision)}
				for i := 0; i < downloads; i++ {
					err := s.store.IncCounterAtTime(key, date)
					c.Assert(err, gc.IsNil)
				}
				if url.PromulgatedRevision > -1 {
					key := []string{params.StatsArchiveDownloadPromulgated, url.URL.Series, url.URL.Name, "", strconv.Itoa(url.PromulgatedRevision)}
					for i := 0; i < downloads; i++ {
						err := s.store.IncCounterAtTime(key, date)
						c.Assert(err, gc.IsNil)
					}
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
	id, _ := s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("~charmers/utopic/wordpress-42", 42))
	url := storeURL("utopic/wordpress-42/meta/stats")

	for i, test := range metaStatsWithLegacyDownloadCountsTests {
		c.Logf("test %d: %s", i, test.about)

		// Update the entity extra info if required.
		if test.count != "" {
			extraInfo := map[string][]byte{
				params.LegacyDownloadStats: []byte(test.count),
			}
			err := s.store.UpdateEntity(id, bson.D{{
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
	id   *router.ResolvedURL
	time string
	acl  []string
}

func (p publishSpec) published() params.Published {
	t, err := time.Parse("2006-01-02 15:04", p.time)
	if err != nil {
		panic(err)
	}
	return params.Published{&p.id.URL, t}
}

var publishedCharms = []publishSpec{{
	id:   newResolvedURL("cs:~charmers/precise/wordpress-1", 1),
	time: "5432-10-12 00:00",
}, {
	id:   newResolvedURL("cs:~charmers/precise/mysql-1", 1),
	time: "5432-10-12 13:00",
}, {
	id:   newResolvedURL("cs:~charmers/precise/wordpress-2", 2),
	time: "5432-10-12 23:59",
}, {
	id:   newResolvedURL("cs:~charmers/precise/mysql-2", 2),
	time: "5432-10-13 00:00",
}, {
	id:   newResolvedURL("cs:~charmers/precise/mysql-5", 5),
	time: "5432-10-13 10:00",
}, {
	id:   newResolvedURL("cs:~charmers/precise/wordpress-3", 3),
	time: "5432-10-14 01:00",
}, {
	id:   newResolvedURL("cs:~charmers/precise/django-0", -1),
	time: "5432-10-14 02:00",
	acl:  []string{"charmers"},
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

func (s *APISuite) TestChangesPublishedAdmin(c *gc.C) {
	s.publishCharmsAtKnownTimes(c, publishedCharms)
	expect := make([]params.Published, len(publishedCharms))
	for i := range expect {
		expect[i] = publishedCharms[len(publishedCharms)-(i+1)].published()
	}
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:    s.srv,
		Username:   testUsername,
		Password:   testPassword,
		URL:        storeURL("changes/published"),
		ExpectBody: expect,
	})
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
		id, _ := s.addPublicCharmFromRepo(c, "wordpress", ch.id)
		t := ch.published().PublishTime
		err := s.store.UpdateEntity(id, bson.D{{"$set", bson.D{{"uploadtime", t}}}})
		c.Assert(err, gc.IsNil)
		if len(ch.acl) > 0 {
			err := s.store.SetPerms(&id.URL, "unpublished.read", ch.acl...)
			c.Assert(err, gc.IsNil)
			err = s.store.SetPerms(&id.URL, "stable.read", ch.acl...)
			c.Assert(err, gc.IsNil)
		}
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
			Header:  basicAuthHeader(testUsername, testPassword),
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
			ExpectStatus: http.StatusProxyAuthRequired,
			ExpectBody:   dischargeRequiredBody,
		})
	}
}

func (s *APISuite) TestHash256Laziness(c *gc.C) {
	// TODO frankban: remove this test after updating entities in the
	// production db with their SHA256 hash value. Entities are updated by
	// running the cshash256 command.
	id, _ := s.addPublicCharmFromRepo(c, "wordpress", newResolvedURL("cs:~who/precise/wordpress-0", -1))

	// Retrieve the SHA256 hash.
	entity, err := s.store.FindEntity(id, charmstore.FieldSelector("blobhash256"))
	c.Assert(err, gc.IsNil)
	c.Assert(entity.BlobHash256, gc.Not(gc.Equals), "")
}

var urlChannelResolvingEntities = []struct {
	id      *router.ResolvedURL
	channel mongodoc.Channel
}{{
	id:      newResolvedURL("~charmers/precise/wordpress-0", 0),
	channel: mongodoc.StableChannel,
}, {
	id:      newResolvedURL("~charmers/precise/wordpress-1", 1),
	channel: mongodoc.DevelopmentChannel,
}, {
	id:      newResolvedURL("~charmers/precise/wordpress-2", 2),
	channel: mongodoc.UnpublishedChannel,
}, {
	id:      newResolvedURL("~charmers/trusty/mysql-0", 0),
	channel: mongodoc.UnpublishedChannel,
}}

var urlChannelResolvingTests = []struct {
	url          string
	channel      mongodoc.Channel
	expectURL    string
	expectStatus int
	expectError  params.Error
}{{
	url:       "wordpress",
	expectURL: "cs:precise/wordpress-0",
}, {
	url:       "wordpress",
	channel:   mongodoc.StableChannel,
	expectURL: "cs:precise/wordpress-0",
}, {
	url:       "wordpress",
	channel:   mongodoc.DevelopmentChannel,
	expectURL: "cs:precise/wordpress-1",
}, {
	url:       "wordpress",
	channel:   mongodoc.UnpublishedChannel,
	expectURL: "cs:precise/wordpress-2",
}, {
	url:       "~charmers/precise/wordpress",
	channel:   mongodoc.StableChannel,
	expectURL: "cs:~charmers/precise/wordpress-0",
}, {
	url:       "~charmers/precise/wordpress-2",
	channel:   mongodoc.StableChannel,
	expectURL: "cs:~charmers/precise/wordpress-2",
}, {
	url:          "mysql",
	expectStatus: http.StatusNotFound,
	expectError: params.Error{
		Message: `no matching charm or bundle for "cs:mysql"`,
		Code:    params.ErrNotFound,
	},
}, {
	url:          "mysql",
	channel:      "unknown",
	expectStatus: http.StatusBadRequest,
	expectError: params.Error{
		Message: `invalid channel "unknown" specified in request`,
		Code:    params.ErrBadRequest,
	},
}}

func (s *APISuite) TestURLChannelResolving(c *gc.C) {
	s.discharge = dischargeForUser("charmers")
	for _, add := range urlChannelResolvingEntities {
		err := s.store.AddCharmWithArchive(add.id, storetesting.NewCharm(nil))
		c.Assert(err, gc.IsNil)
		if add.channel != mongodoc.UnpublishedChannel {
			err = s.store.Publish(add.id, add.channel)
			c.Assert(err, gc.IsNil)
		}
	}
	for i, test := range urlChannelResolvingTests {
		path := test.url + "/meta/any"
		if test.channel != "" {
			path += "?channel=" + string(test.channel)
		}
		c.Logf("test %d: %v", i, test.url)
		if test.expectError.Message != "" {
			httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
				Handler:      s.srv,
				Do:           bakeryDo(nil),
				URL:          storeURL(path),
				ExpectStatus: test.expectStatus,
				ExpectBody:   test.expectError,
			})
		} else {
			httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
				Handler: s.srv,
				Do:      bakeryDo(nil),
				URL:     storeURL(path),
				ExpectBody: params.MetaAnyResponse{
					Id: charm.MustParseURL(test.expectURL),
				},
			})
		}
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
	return func(store *charmstore.Store, url *router.ResolvedURL) (interface{}, error) {
		doc, err := store.FindEntity(url, nil)
		if err != nil {
			return nil, errgo.Mask(err)
		}
		return get(doc), nil
	}
}

func zipGetter(get func(*zip.Reader) interface{}) metaEndpointExpectedValueGetter {
	return func(store *charmstore.Store, url *router.ResolvedURL) (interface{}, error) {
		doc, err := store.FindEntity(url, charmstore.FieldSelector("blobname"))
		if err != nil {
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

func (s *APISuite) assertPutNonAdmin(c *gc.C, url string, val interface{}) {
	s.assertPut0(c, url, val, false)
}

func (s *APISuite) assertPut(c *gc.C, url string, val interface{}) {
	s.assertPut0(c, url, val, true)
}

func (s *APISuite) assertPut0(c *gc.C, url string, val interface{}, asAdmin bool) {
	body, err := json.Marshal(val)
	c.Assert(err, gc.IsNil)
	p := httptesting.JSONCallParams{
		Handler: s.srv,
		URL:     storeURL(url),
		Method:  "PUT",
		Do:      bakeryDo(nil),
		Header: http.Header{
			"Content-Type": {"application/json"},
		},
		Body: bytes.NewReader(body),
	}
	if asAdmin {
		p.Username = testUsername
		p.Password = testPassword
	}
	httptesting.AssertJSONCall(c, p)
}

func (s *APISuite) assertGet(c *gc.C, url string, expectVal interface{}) {
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:    s.srv,
		Do:         bakeryDo(nil),
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
	s.discharge = func(cond string, arg string) ([]checkers.Caveat, error) {
		mu.Lock()
		defer mu.Unlock()
		checkedCaveats = append(checkedCaveats, cond+" "+arg)
		return []checkers.Caveat{checkers.DeclaredCaveat("username", "who")}, dischargeError
	}
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("macaroon"),
		Method:  "GET",
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK, gc.Commentf("body: %s", rec.Body.String()))
	var m macaroon.Macaroon
	err := json.Unmarshal(rec.Body.Bytes(), &m)
	c.Assert(err, gc.IsNil)
	c.Assert(m.Location(), gc.Equals, "charmstore")
	client := httpbakery.NewClient()
	ms, err := client.DischargeAll(&m)
	c.Assert(err, gc.IsNil)
	sort.Strings(checkedCaveats)
	c.Assert(checkedCaveats, jc.DeepEquals, []string{
		"is-authenticated-user ",
	})
	macaroonCookie, err := httpbakery.NewCookie(ms)
	c.Assert(err, gc.IsNil)
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("log"),
		Do:           bakeryDo(nil),
		Cookies:      []*http.Cookie{macaroonCookie},
		ExpectStatus: http.StatusUnauthorized,
		ExpectBody: params.Error{
			Code:    params.ErrUnauthorized,
			Message: `unauthorized: access denied for user "who"`,
		},
	})
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.noMacaroonSrv,
		URL:          storeURL("log"),
		ExpectStatus: http.StatusUnauthorized,
		ExpectBody: params.Error{
			Message: "authentication failed: missing HTTP auth header",
			Code:    params.ErrUnauthorized,
		},
	})
}

func (s *APISuite) TestWhoAmIFailWithNoMacaroon(c *gc.C) {
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.noMacaroonSrv,
		URL:          storeURL("whoami"),
		Do:           bakeryDo(nil),
		ExpectStatus: http.StatusUnauthorized,
		ExpectBody: params.Error{
			Code:    params.ErrUnauthorized,
			Message: "authentication failed: missing HTTP auth header",
		},
	})
}

func (s *APISuite) TestWhoAmIReturnsNameAndGroups(c *gc.C) {
	s.discharge = dischargeForUser("who")
	s.idM.groups = map[string][]string{
		"who": {"foo", "bar"},
	}
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("whoami"),
		Do:           bakeryDo(nil),
		ExpectStatus: http.StatusOK,
		ExpectBody: params.WhoAmIResponse{
			User:   "who",
			Groups: []string{"foo", "bar"},
		},
	})
}

var promulgateTests = []struct {
	about              string
	entities           []*mongodoc.Entity
	baseEntities       []*mongodoc.BaseEntity
	id                 string
	useHTTPDo          bool
	method             string
	caveats            []checkers.Caveat
	groups             map[string][]string
	body               io.Reader
	username           string
	password           string
	expectStatus       int
	expectBody         interface{}
	expectEntities     []*mongodoc.Entity
	expectBaseEntities []*mongodoc.BaseEntity
	expectPromulgate   bool
	expectUser         string
}{{
	about: "unpromulgate base entity",
	entities: []*mongodoc.Entity{
		storetesting.NewEntity("~charmers/trusty/wordpress-0").WithPromulgatedURL("trusty/wordpress-0").Build(),
	},
	baseEntities: []*mongodoc.BaseEntity{
		storetesting.NewBaseEntity("~charmers/wordpress").WithPromulgated(true).Build(),
	},
	id:           "~charmers/wordpress",
	body:         storetesting.JSONReader(params.PromulgateRequest{Promulgated: false}),
	username:     testUsername,
	password:     testPassword,
	expectStatus: http.StatusOK,
	expectEntities: []*mongodoc.Entity{
		storetesting.NewEntity("~charmers/trusty/wordpress-0").WithPromulgatedURL("trusty/wordpress-0").Build(),
	},
	expectBaseEntities: []*mongodoc.BaseEntity{
		storetesting.NewBaseEntity("~charmers/wordpress").Build(),
	},
	expectUser: "admin",
}, {
	about: "promulgate base entity",
	entities: []*mongodoc.Entity{
		storetesting.NewEntity("~charmers/trusty/wordpress-0").Build(),
	},
	baseEntities: []*mongodoc.BaseEntity{
		storetesting.NewBaseEntity("~charmers/wordpress").Build(),
	},
	id:           "~charmers/wordpress",
	body:         storetesting.JSONReader(params.PromulgateRequest{Promulgated: true}),
	username:     testUsername,
	password:     testPassword,
	expectStatus: http.StatusOK,
	expectEntities: []*mongodoc.Entity{
		storetesting.NewEntity("~charmers/trusty/wordpress-0").WithPromulgatedURL("trusty/wordpress-0").Build(),
	},
	expectBaseEntities: []*mongodoc.BaseEntity{
		storetesting.NewBaseEntity("~charmers/wordpress").WithACLs(mongodoc.StableChannel, mongodoc.ACL{
			Write: []string{v5.PromulgatorsGroup},
		}).WithPromulgated(true).Build(),
	},
	expectPromulgate: true,
	expectUser:       "admin",
}, {
	about: "unpromulgate base entity not found",
	entities: []*mongodoc.Entity{
		storetesting.NewEntity("~charmers/trusty/wordpress-0").WithPromulgatedURL("trusty/wordpress-0").Build(),
	},
	baseEntities: []*mongodoc.BaseEntity{
		storetesting.NewBaseEntity("~charmers/wordpress").WithPromulgated(true).Build(),
	},
	id:           "~charmers/mysql",
	body:         storetesting.JSONReader(params.PromulgateRequest{Promulgated: false}),
	username:     testUsername,
	password:     testPassword,
	expectStatus: http.StatusNotFound,
	expectBody: params.Error{
		Code:    params.ErrNotFound,
		Message: `no matching charm or bundle for "cs:~charmers/mysql"`,
	},
	expectEntities: []*mongodoc.Entity{
		storetesting.NewEntity("~charmers/trusty/wordpress-0").WithPromulgatedURL("trusty/wordpress-0").Build(),
	},
	expectBaseEntities: []*mongodoc.BaseEntity{
		storetesting.NewBaseEntity("~charmers/wordpress").WithPromulgated(true).Build(),
	},
}, {
	about: "promulgate base entity not found",
	entities: []*mongodoc.Entity{
		storetesting.NewEntity("~charmers/trusty/wordpress-0").Build(),
	},
	baseEntities: []*mongodoc.BaseEntity{
		storetesting.NewBaseEntity("~charmers/wordpress").Build(),
	},
	id:           "~charmers/mysql",
	body:         storetesting.JSONReader(params.PromulgateRequest{Promulgated: true}),
	username:     testUsername,
	password:     testPassword,
	expectStatus: http.StatusNotFound,
	expectBody: params.Error{
		Code:    params.ErrNotFound,
		Message: `no matching charm or bundle for "cs:~charmers/mysql"`,
	},
	expectEntities: []*mongodoc.Entity{
		storetesting.NewEntity("~charmers/trusty/wordpress-0").Build(),
	},
	expectBaseEntities: []*mongodoc.BaseEntity{
		storetesting.NewBaseEntity("~charmers/wordpress").Build(),
	},
}, {
	about: "bad method",
	entities: []*mongodoc.Entity{
		storetesting.NewEntity("~charmers/trusty/wordpress-0").WithPromulgatedURL("trusty/wordpress-0").Build(),
	},
	baseEntities: []*mongodoc.BaseEntity{
		storetesting.NewBaseEntity("~charmers/wordpress").WithPromulgated(true).Build(),
	},
	id:           "~charmers/wordpress",
	body:         storetesting.JSONReader(params.PromulgateRequest{Promulgated: false}),
	username:     testUsername,
	password:     testPassword,
	method:       "POST",
	expectStatus: http.StatusMethodNotAllowed,
	expectBody: params.Error{
		Code:    params.ErrMethodNotAllowed,
		Message: "POST not allowed",
	},
	expectEntities: []*mongodoc.Entity{
		storetesting.NewEntity("~charmers/trusty/wordpress-0").WithPromulgatedURL("trusty/wordpress-0").Build(),
	},
	expectBaseEntities: []*mongodoc.BaseEntity{
		storetesting.NewBaseEntity("~charmers/wordpress").WithPromulgated(true).Build(),
	},
}, {
	about: "bad JSON",
	entities: []*mongodoc.Entity{
		storetesting.NewEntity("~charmers/trusty/wordpress-0").WithPromulgatedURL("trusty/wordpress-0").Build(),
	},
	baseEntities: []*mongodoc.BaseEntity{
		storetesting.NewBaseEntity("~charmers/wordpress").WithPromulgated(true).Build(),
	},
	id:           "~charmers/wordpress",
	body:         bytes.NewReader([]byte("tru")),
	username:     testUsername,
	password:     testPassword,
	expectStatus: http.StatusBadRequest,
	expectBody: params.Error{
		Code:    params.ErrBadRequest,
		Message: "bad request: invalid character ' ' in literal true (expecting 'e')",
	},
	expectEntities: []*mongodoc.Entity{
		storetesting.NewEntity("~charmers/trusty/wordpress-0").WithPromulgatedURL("trusty/wordpress-0").Build(),
	},
	expectBaseEntities: []*mongodoc.BaseEntity{
		storetesting.NewBaseEntity("~charmers/wordpress").WithPromulgated(true).Build(),
	},
}, {
	about: "unpromulgate base entity with macaroon",
	entities: []*mongodoc.Entity{
		storetesting.NewEntity("~charmers/trusty/wordpress-0").WithPromulgatedURL("trusty/wordpress-0").Build(),
	},
	baseEntities: []*mongodoc.BaseEntity{
		storetesting.NewBaseEntity("~charmers/wordpress").WithPromulgated(true).Build(),
	},
	id:   "~charmers/wordpress",
	body: storetesting.JSONReader(params.PromulgateRequest{Promulgated: false}),
	caveats: []checkers.Caveat{
		checkers.DeclaredCaveat(v5.UsernameAttr, v5.PromulgatorsGroup),
	},
	expectStatus: http.StatusOK,
	expectEntities: []*mongodoc.Entity{
		storetesting.NewEntity("~charmers/trusty/wordpress-0").WithPromulgatedURL("trusty/wordpress-0").Build(),
	},
	expectBaseEntities: []*mongodoc.BaseEntity{
		storetesting.NewBaseEntity("~charmers/wordpress").Build(),
	},
	expectUser: v5.PromulgatorsGroup,
}, {
	about: "promulgate base entity with macaroon",
	entities: []*mongodoc.Entity{
		storetesting.NewEntity("~charmers/trusty/wordpress-0").Build(),
	},
	baseEntities: []*mongodoc.BaseEntity{
		storetesting.NewBaseEntity("~charmers/wordpress").Build(),
	},
	id:   "~charmers/wordpress",
	body: storetesting.JSONReader(params.PromulgateRequest{Promulgated: true}),
	caveats: []checkers.Caveat{
		checkers.DeclaredCaveat(v5.UsernameAttr, v5.PromulgatorsGroup),
	},
	expectStatus: http.StatusOK,
	expectEntities: []*mongodoc.Entity{
		storetesting.NewEntity("~charmers/trusty/wordpress-0").WithPromulgatedURL("trusty/wordpress-0").Build(),
	},
	expectBaseEntities: []*mongodoc.BaseEntity{
		storetesting.NewBaseEntity("~charmers/wordpress").WithACLs(mongodoc.StableChannel, mongodoc.ACL{
			Write: []string{v5.PromulgatorsGroup},
		}).WithPromulgated(true).Build(),
	},
	expectPromulgate: true,
	expectUser:       v5.PromulgatorsGroup,
}, {
	about: "promulgate base entity with group macaroon",
	entities: []*mongodoc.Entity{
		storetesting.NewEntity("~charmers/trusty/wordpress-0").Build(),
	},
	baseEntities: []*mongodoc.BaseEntity{
		storetesting.NewBaseEntity("~charmers/wordpress").Build(),
	},
	id:   "~charmers/wordpress",
	body: storetesting.JSONReader(params.PromulgateRequest{Promulgated: true}),
	caveats: []checkers.Caveat{
		checkers.DeclaredCaveat(v5.UsernameAttr, "bob"),
	},
	groups: map[string][]string{
		"bob": {v5.PromulgatorsGroup, "yellow"},
	},
	expectStatus: http.StatusOK,
	expectEntities: []*mongodoc.Entity{
		storetesting.NewEntity("~charmers/trusty/wordpress-0").WithPromulgatedURL("trusty/wordpress-0").Build(),
	},
	expectBaseEntities: []*mongodoc.BaseEntity{
		storetesting.NewBaseEntity("~charmers/wordpress").WithACLs(mongodoc.StableChannel, mongodoc.ACL{
			Write: []string{v5.PromulgatorsGroup},
		}).WithPromulgated(true).Build(),
	},
	expectPromulgate: true,
	expectUser:       "bob",
}, {
	about: "no authorisation",
	entities: []*mongodoc.Entity{
		storetesting.NewEntity("~charmers/trusty/wordpress-0").WithPromulgatedURL("trusty/wordpress-0").Build(),
	},
	baseEntities: []*mongodoc.BaseEntity{
		storetesting.NewBaseEntity("~charmers/wordpress").WithPromulgated(true).Build(),
	},
	useHTTPDo:    true,
	id:           "~charmers/wordpress",
	body:         storetesting.JSONReader(params.PromulgateRequest{Promulgated: false}),
	expectStatus: http.StatusProxyAuthRequired,
	expectBody:   dischargeRequiredBody,
	expectEntities: []*mongodoc.Entity{
		storetesting.NewEntity("~charmers/trusty/wordpress-0").WithPromulgatedURL("trusty/wordpress-0").Build(),
	},
	expectBaseEntities: []*mongodoc.BaseEntity{
		storetesting.NewBaseEntity("~charmers/wordpress").WithPromulgated(true).Build(),
	},
}, {
	about: "promulgate base entity with unauthorized user macaroon",
	entities: []*mongodoc.Entity{
		storetesting.NewEntity("~charmers/trusty/wordpress-0").Build(),
	},
	baseEntities: []*mongodoc.BaseEntity{
		storetesting.NewBaseEntity("~charmers/wordpress").Build(),
	},
	id:   "~charmers/wordpress",
	body: storetesting.JSONReader(params.PromulgateRequest{Promulgated: true}),
	caveats: []checkers.Caveat{
		checkers.DeclaredCaveat(v5.UsernameAttr, "bob"),
	},
	groups: map[string][]string{
		"bob": {"yellow"},
	},
	expectStatus: http.StatusUnauthorized,
	expectBody: params.Error{
		Message: `unauthorized: access denied for user "bob"`,
		Code:    params.ErrUnauthorized,
	},
	expectEntities: []*mongodoc.Entity{
		storetesting.NewEntity("~charmers/trusty/wordpress-0").Build(),
	},
	expectBaseEntities: []*mongodoc.BaseEntity{
		storetesting.NewBaseEntity("~charmers/wordpress").Build(),
	},
}}

func (s *APISuite) TestPromulgate(c *gc.C) {
	for i, test := range promulgateTests {
		c.Logf("%d. %s\n", i, test.about)
		_, err := s.store.DB.Entities().RemoveAll(nil)
		c.Assert(err, gc.IsNil)
		_, err = s.store.DB.BaseEntities().RemoveAll(nil)
		c.Assert(err, gc.IsNil)
		for _, e := range test.entities {
			err := s.store.DB.Entities().Insert(e)
			c.Assert(err, gc.IsNil)
		}
		for _, e := range test.baseEntities {
			err := s.store.DB.BaseEntities().Insert(e)
			c.Assert(err, gc.IsNil)
		}
		if test.method == "" {
			test.method = "PUT"
		}

		var calledEntities []audit.Entry
		s.PatchValue(v5.TestAddAuditCallback, func(e audit.Entry) {
			calledEntities = append(calledEntities, e)
		})

		client := httpbakery.NewHTTPClient()
		s.discharge = func(_, _ string) ([]checkers.Caveat, error) {
			return test.caveats, nil
		}
		s.idM.groups = test.groups
		p := httptesting.JSONCallParams{
			Handler: s.srv,
			// TODO avoid using channel=unpublished here
			URL:          storeURL(test.id + "/promulgate?channel=unpublished"),
			Method:       test.method,
			Body:         test.body,
			Header:       http.Header{"Content-Type": {"application/json"}},
			Username:     test.username,
			Password:     test.password,
			ExpectStatus: test.expectStatus,
			ExpectBody:   test.expectBody,
		}
		if !test.useHTTPDo {
			p.Do = bakeryDo(client)
		}
		httptesting.AssertJSONCall(c, p)
		n, err := s.store.DB.Entities().Count()
		c.Assert(err, gc.IsNil)
		c.Assert(n, gc.Equals, len(test.expectEntities))
		for _, e := range test.expectEntities {
			storetesting.AssertEntity(c, s.store.DB.Entities(), e)
		}
		n, err = s.store.DB.BaseEntities().Count()
		c.Assert(err, gc.IsNil)
		c.Assert(n, gc.Equals, len(test.expectBaseEntities))
		for _, e := range test.expectBaseEntities {
			storetesting.AssertBaseEntity(c, s.store.DB.BaseEntities(), e)
		}

		if test.expectStatus == http.StatusOK {
			ref := charm.MustParseURL(test.id)
			ref.Series = "trusty"
			ref.Revision = 0

			e := audit.Entry{
				User:   test.expectUser,
				Op:     audit.OpUnpromulgate,
				Entity: ref,
			}
			if test.expectPromulgate {
				e.Op = audit.OpPromulgate
			}
			c.Assert(calledEntities, jc.DeepEquals, []audit.Entry{e})
		} else {
			c.Assert(len(calledEntities), gc.Equals, 0)
		}
		calledEntities = nil
	}
}

func (s *APISuite) TestEndpointRequiringBaseEntityWithPromulgatedId(c *gc.C) {
	// Add a promulgated charm.
	url := newResolvedURL("~charmers/precise/wordpress-23", 23)
	s.addPublicCharmFromRepo(c, "wordpress", url)

	// Unpromulgate the base entity
	err := s.store.SetPromulgated(url, false)
	c.Assert(err, gc.IsNil)

	// Check that we can still enquire about the promulgation status
	// of the entity when using its promulgated URL.
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler: s.srv,
		URL:     storeURL("precise/wordpress-23/meta/promulgated"),
		ExpectBody: params.PromulgatedResponse{
			Promulgated: false,
		},
	})
}

func (s *APISuite) TestTooManyConcurrentRequests(c *gc.C) {
	// We don't have any control over the number of concurrent
	// connections allowed by s.srv, so we make our own
	// server here with custom config.
	config := charmstore.ServerParams{
		MaxMgoSessions: 1,
	}
	db := s.Session.DB("charmstore")
	srv, err := charmstore.NewServer(db, nil, config, map[string]charmstore.NewAPIHandlerFunc{"v5": v5.NewAPIHandler})
	c.Assert(err, gc.IsNil)
	defer srv.Close()

	// Get a store from the pool so that we'll be
	// at the concurrent request limit.
	store := srv.Pool().Store()
	defer store.Close()

	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      srv,
		Do:           bakeryDo(nil),
		URL:          storeURL("debug/status"),
		ExpectStatus: http.StatusServiceUnavailable,
		ExpectBody: params.Error{
			Message: "service unavailable: too many mongo sessions in use",
			Code:    params.ErrServiceUnavailable,
		},
	})
}

// dischargeRequiredBody returns a httptesting.BodyAsserter that checks
// that the response body contains a discharge required error holding a macaroon
// with a third-party caveat addressed to expectedEntityLocation.
var dischargeRequiredBody httptesting.BodyAsserter = func(c *gc.C, body json.RawMessage) {
	var response httpbakery.Error
	err := json.Unmarshal(body, &response)
	c.Assert(err, gc.IsNil)
	c.Assert(response.Code, gc.Equals, httpbakery.ErrDischargeRequired)
	c.Assert(response.Message, gc.Equals, "verification failed: no macaroon cookies in request")
	c.Assert(response.Info.Macaroon, gc.NotNil)
	for _, cav := range response.Info.Macaroon.Caveats() {
		if cav.Location != "" {
			return
		}
	}
	c.Fatalf("no third party caveat found in response macaroon; caveats %#v", response.Info.Macaroon.Caveats())
}

func (s *APISuite) TestSetAuthCookie(c *gc.C) {
	m, err := macaroon.New([]byte("key"), "id", "location")
	c.Assert(err, jc.ErrorIsNil)
	ms := macaroon.Slice{m}
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("set-auth-cookie"),
		Method:  "PUT",
		Header:  http.Header{"Origin": []string{"https://1.2.3.4"}},
		JSONBody: params.SetAuthCookie{
			Macaroons: ms,
		},
	})
	// The request is successful.
	c.Assert(rec.Code, gc.Equals, http.StatusOK)

	// The response includes the CORS header for the specific request.
	c.Assert(rec.Header().Get("Access-Control-Allow-Origin"), gc.Equals, "https://1.2.3.4")

	// The response includes the macaroons cookie.
	resp := http.Response{Header: rec.Header()}
	cookies := resp.Cookies()
	c.Assert(len(cookies), gc.Equals, 1)
	expected, err := httpbakery.NewCookie(ms)
	expected.Path = "/"
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(cookies[0].Value, gc.Equals, expected.Value)
}

func (s *APISuite) TestSetAuthCookieBodyError(c *gc.C) {
	m, err := macaroon.New([]byte("key"), "id", "location")
	c.Assert(err, jc.ErrorIsNil)
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("set-auth-cookie"),
		Method:       "PUT",
		JSONBody:     macaroon.Slice{m},
		ExpectStatus: http.StatusInternalServerError,
		ExpectBody: params.Error{
			Message: "cannot unmarshal macaroons: json: cannot unmarshal array into Go value of type params.SetAuthCookie",
		},
	})
}

func (s *APISuite) TestSetAuthCookieMethodError(c *gc.C) {
	m, err := macaroon.New([]byte("key"), "id", "location")
	c.Assert(err, jc.ErrorIsNil)
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("set-auth-cookie"),
		Method:       "POST",
		JSONBody:     macaroon.Slice{m},
		ExpectStatus: http.StatusMethodNotAllowed,
		ExpectBody: params.Error{
			Code:    params.ErrMethodNotAllowed,
			Message: "POST not allowed",
		},
	})
}

// entityACLs returns the ACLs that apply to the entity with the given URL.
func entityACLs(store *charmstore.Store, url *router.ResolvedURL) (mongodoc.ACL, error) {
	e, err := store.FindEntity(url, nil)
	if err != nil {
		return mongodoc.ACL{}, err
	}
	be, err := store.FindBaseEntity(&url.URL, nil)
	if err != nil {
		return mongodoc.ACL{}, err
	}
	ch := mongodoc.UnpublishedChannel
	if e.Stable {
		ch = mongodoc.StableChannel
	} else if e.Development {
		ch = mongodoc.DevelopmentChannel
	}
	return be.ChannelACLs[ch], nil
}
