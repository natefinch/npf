// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4_test

import (
	"fmt"
	"net/http"

	"github.com/juju/testing/httptesting"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v5-unstable"
	"gopkg.in/mgo.v2/bson"

	"gopkg.in/juju/charmstore.v4/internal/blobstore"
	"gopkg.in/juju/charmstore.v4/internal/charmstore"
	"gopkg.in/juju/charmstore.v4/internal/storetesting"
	"gopkg.in/juju/charmstore.v4/params"
)

// Define fake blob attributes to be used in tests.
var fakeBlobSize, fakeBlobHash = func() (int64, string) {
	b := []byte("fake content")
	h := blobstore.NewHash()
	h.Write(b)
	return int64(len(b)), fmt.Sprintf("%x", h.Sum(nil))
}()

type RelationsSuite struct {
	storetesting.IsolatedMgoSuite
	srv   http.Handler
	store *charmstore.Store
}

var _ = gc.Suite(&RelationsSuite{})

func (s *RelationsSuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	s.srv, s.store = newServer(c, s.Session, nil, serverParams)
}

// metaCharmRelatedCharms defines a bunch of charms to be used in
// the relation tests.
var metaCharmRelatedCharms = map[string]charm.Charm{
	"utopic/wordpress-0": &relationTestingCharm{
		provides: map[string]charm.Relation{
			"website": {
				Name:      "website",
				Role:      "provider",
				Interface: "http",
			},
		},
		requires: map[string]charm.Relation{
			"cache": {
				Name:      "cache",
				Role:      "requirer",
				Interface: "memcache",
			},
			"nfs": {
				Name:      "nfs",
				Role:      "requirer",
				Interface: "mount",
			},
		},
	},
	"utopic/memcached-42": &relationTestingCharm{
		provides: map[string]charm.Relation{
			"cache": {
				Name:      "cache",
				Role:      "provider",
				Interface: "memcache",
			},
		},
	},
	"precise/nfs-1": &relationTestingCharm{
		provides: map[string]charm.Relation{
			"nfs": {
				Name:      "nfs",
				Role:      "provider",
				Interface: "mount",
			},
		},
	},
	"trusty/haproxy-47": &relationTestingCharm{
		requires: map[string]charm.Relation{
			"reverseproxy": {
				Name:      "reverseproxy",
				Role:      "requirer",
				Interface: "http",
			},
		},
	},
	"precise/haproxy-48": &relationTestingCharm{
		requires: map[string]charm.Relation{
			"reverseproxy": {
				Name:      "reverseproxy",
				Role:      "requirer",
				Interface: "http",
			},
		},
	},
}

var metaCharmRelatedTests = []struct {
	// Description of the test.
	about string
	// Charms to be stored in the store before the test is run.
	charms map[string]charm.Charm
	// The id of the charm for which related charms are returned.
	id string
	// The querystring to append to the resulting charmstore URL.
	querystring string
	// The expected response body.
	expectBody params.RelatedResponse
}{{
	about:  "provides and requires",
	charms: metaCharmRelatedCharms,
	id:     "utopic/wordpress-0",
	expectBody: params.RelatedResponse{
		Provides: map[string][]params.MetaAnyResponse{
			"memcache": {{
				Id: charm.MustParseReference("utopic/memcached-42"),
			}},
			"mount": {{
				Id: charm.MustParseReference("precise/nfs-1"),
			}},
		},
		Requires: map[string][]params.MetaAnyResponse{
			"http": {{
				Id: charm.MustParseReference("precise/haproxy-48"),
			}, {
				Id: charm.MustParseReference("trusty/haproxy-47"),
			}},
		},
	},
}, {
	about:  "only provides",
	charms: metaCharmRelatedCharms,
	id:     "trusty/haproxy-47",
	expectBody: params.RelatedResponse{
		Provides: map[string][]params.MetaAnyResponse{
			"http": {{
				Id: charm.MustParseReference("utopic/wordpress-0"),
			}},
		},
	},
}, {
	about:  "only requires",
	charms: metaCharmRelatedCharms,
	id:     "utopic/memcached-42",
	expectBody: params.RelatedResponse{
		Requires: map[string][]params.MetaAnyResponse{
			"memcache": {{
				Id: charm.MustParseReference("utopic/wordpress-0"),
			}},
		},
	},
}, {
	about: "no relations found",
	charms: map[string]charm.Charm{
		"utopic/wordpress-0": &relationTestingCharm{
			provides: map[string]charm.Relation{
				"website": {
					Name:      "website",
					Role:      "provider",
					Interface: "http",
				},
			},
			requires: map[string]charm.Relation{
				"cache": {
					Name:      "cache",
					Role:      "requirer",
					Interface: "memcache",
				},
				"nfs": {
					Name:      "nfs",
					Role:      "requirer",
					Interface: "mount",
				},
			},
		},
	},
	id: "utopic/wordpress-0",
}, {
	about: "no relations defined",
	charms: map[string]charm.Charm{
		"utopic/django-42": &relationTestingCharm{},
	},
	id: "utopic/django-42",
}, {
	about: "multiple revisions of the same related charm",
	charms: map[string]charm.Charm{
		"trusty/wordpress-0": &relationTestingCharm{
			requires: map[string]charm.Relation{
				"cache": {
					Name:      "cache",
					Role:      "requirer",
					Interface: "memcache",
				},
			},
		},
		"utopic/memcached-1": &relationTestingCharm{
			provides: map[string]charm.Relation{
				"cache": {
					Name:      "cache",
					Role:      "provider",
					Interface: "memcache",
				},
			},
		},
		"utopic/memcached-2": &relationTestingCharm{
			provides: map[string]charm.Relation{
				"cache": {
					Name:      "cache",
					Role:      "provider",
					Interface: "memcache",
				},
			},
		},
		"utopic/memcached-3": &relationTestingCharm{
			provides: map[string]charm.Relation{
				"cache": {
					Name:      "cache",
					Role:      "provider",
					Interface: "memcache",
				},
			},
		},
	},
	id: "trusty/wordpress-0",
	expectBody: params.RelatedResponse{
		Provides: map[string][]params.MetaAnyResponse{
			"memcache": {{
				Id: charm.MustParseReference("utopic/memcached-1"),
			}, {
				Id: charm.MustParseReference("utopic/memcached-2"),
			}, {
				Id: charm.MustParseReference("utopic/memcached-3"),
			}},
		},
	},
}, {
	about: "reference ordering",
	charms: map[string]charm.Charm{
		"trusty/wordpress-0": &relationTestingCharm{
			requires: map[string]charm.Relation{
				"cache": {
					Name:      "cache",
					Role:      "requirer",
					Interface: "memcache",
				},
				"nfs": {
					Name:      "nfs",
					Role:      "requirer",
					Interface: "mount",
				},
			},
		},
		"utopic/memcached-1": &relationTestingCharm{
			provides: map[string]charm.Relation{
				"cache": {
					Name:      "cache",
					Role:      "provider",
					Interface: "memcache",
				},
			},
		},
		"utopic/memcached-2": &relationTestingCharm{
			provides: map[string]charm.Relation{
				"cache": {
					Name:      "cache",
					Role:      "provider",
					Interface: "memcache",
				},
			},
		},
		"utopic/redis-90": &relationTestingCharm{
			provides: map[string]charm.Relation{
				"cache": {
					Name:      "cache",
					Role:      "provider",
					Interface: "memcache",
				},
			},
		},
		"trusty/nfs-47": &relationTestingCharm{
			provides: map[string]charm.Relation{
				"nfs": {
					Name:      "nfs",
					Role:      "provider",
					Interface: "mount",
				},
			},
		},
		"precise/nfs-42": &relationTestingCharm{
			provides: map[string]charm.Relation{
				"nfs": {
					Name:      "nfs",
					Role:      "provider",
					Interface: "mount",
				},
			},
		},
		"precise/nfs-47": &relationTestingCharm{
			provides: map[string]charm.Relation{
				"nfs": {
					Name:      "nfs",
					Role:      "provider",
					Interface: "mount",
				},
			},
		},
	},
	id: "trusty/wordpress-0",
	expectBody: params.RelatedResponse{
		Provides: map[string][]params.MetaAnyResponse{
			"memcache": {{
				Id: charm.MustParseReference("utopic/memcached-1"),
			}, {
				Id: charm.MustParseReference("utopic/memcached-2"),
			}, {
				Id: charm.MustParseReference("utopic/redis-90"),
			}},
			"mount": {{
				Id: charm.MustParseReference("precise/nfs-42"),
			}, {
				Id: charm.MustParseReference("precise/nfs-47"),
			}, {
				Id: charm.MustParseReference("trusty/nfs-47"),
			}},
		},
	},
}, {
	about:       "includes",
	charms:      metaCharmRelatedCharms,
	id:          "precise/nfs-1",
	querystring: "?include=archive-size&include=charm-metadata",
	expectBody: params.RelatedResponse{
		Requires: map[string][]params.MetaAnyResponse{
			"mount": {{
				Id: charm.MustParseReference("utopic/wordpress-0"),
				Meta: map[string]interface{}{
					"archive-size": params.ArchiveSizeResponse{Size: fakeBlobSize},
					"charm-metadata": &charm.Meta{
						Provides: map[string]charm.Relation{
							"website": {
								Name:      "website",
								Role:      "provider",
								Interface: "http",
							},
						},
						Requires: map[string]charm.Relation{
							"cache": {
								Name:      "cache",
								Role:      "requirer",
								Interface: "memcache",
							},
							"nfs": {
								Name:      "nfs",
								Role:      "requirer",
								Interface: "mount",
							},
						},
					},
				},
			}},
		},
	},
}}

func (s *RelationsSuite) addCharms(c *gc.C, charms map[string]charm.Charm) {
	for id, ch := range charms {
		url := charm.MustParseReference(id)
		var purl *charm.Reference
		pRev := -1
		if url.User == "" {
			purl = url
			url = new(charm.Reference)
			*url = *purl
			url.User = "charmers"
			pRev = purl.Revision
		}
		// The blob related info are not used in these tests.
		// The related charms are retrieved from the entities collection,
		// without accessing the blob store.
		err := s.store.AddCharm(ch, charmstore.AddParams{
			URL:                 url,
			BlobName:            "blobName",
			BlobHash:            fakeBlobHash,
			BlobSize:            fakeBlobSize,
			PromulgatedURL:      purl,
			PromulgatedRevision: pRev,
		})
		c.Assert(err, gc.IsNil)
	}
}

func (s *RelationsSuite) TestMetaCharmRelated(c *gc.C) {
	for i, test := range metaCharmRelatedTests {
		c.Logf("test %d: %s", i, test.about)
		s.addCharms(c, test.charms)
		storeURL := storeURL(test.id + "/meta/charm-related" + test.querystring)
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      s.srv,
			URL:          storeURL,
			ExpectStatus: http.StatusOK,
			ExpectBody:   test.expectBody,
		})
		// Clean up the entities in the store.
		_, err := s.store.DB.Entities().RemoveAll(nil)
		c.Assert(err, gc.IsNil)
	}
}

func (s *RelationsSuite) TestMetaCharmRelatedIncludeError(c *gc.C) {
	s.addCharms(c, metaCharmRelatedCharms)
	storeURL := storeURL("utopic/wordpress-0/meta/charm-related?include=no-such")
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL,
		ExpectStatus: http.StatusInternalServerError,
		ExpectBody: params.Error{
			Message: `cannot retrieve the charm requires: unrecognized metadata name "no-such"`,
		},
	})
}

// relationTestingCharm implements charm.Charm, and it is used for testing
// charm relations.
type relationTestingCharm struct {
	provides map[string]charm.Relation
	requires map[string]charm.Relation
}

func (ch *relationTestingCharm) Meta() *charm.Meta {
	// The only metadata we are interested in is the relation data.
	return &charm.Meta{
		Provides: ch.provides,
		Requires: ch.requires,
	}
}

func (ch *relationTestingCharm) Config() *charm.Config {
	// For the purposes of this implementation, the charm configuration is not
	// relevant.
	return nil
}

func (e *relationTestingCharm) Metrics() *charm.Metrics {
	return nil
}

func (ch *relationTestingCharm) Actions() *charm.Actions {
	// For the purposes of this implementation, the charm actions are not
	// relevant.
	return nil
}

func (ch *relationTestingCharm) Revision() int {
	// For the purposes of this implementation, the charm revision is not
	// relevant.
	return 0
}

// metaBundlesContainingBundles defines a bunch of bundles to be used in
// the bundles-containing tests.
var metaBundlesContainingBundles = map[string]charm.Bundle{
	"bundle/wordpress-simple-0": relationTestingBundle([]string{
		"cs:utopic/wordpress-42",
		"cs:utopic/mysql-0",
	}),
	"bundle/wordpress-simple-1": relationTestingBundle([]string{
		"cs:utopic/wordpress-47",
		"cs:utopic/mysql-1",
	}),
	"bundle/wordpress-complex-1": relationTestingBundle([]string{
		"cs:utopic/wordpress-42",
		"cs:utopic/wordpress-47",
		"cs:trusty/mysql-0",
		"cs:trusty/mysql-1",
		"cs:trusty/memcached-2",
	}),
	"bundle/django-generic-42": relationTestingBundle([]string{
		"django",
		"django",
		"mysql-1",
		"trusty/memcached",
	}),
	"bundle/useless-0": relationTestingBundle([]string{
		"cs:utopic/wordpress-42",
		"precise/mediawiki-10",
	}),
	"bundle/mediawiki-simple-46": relationTestingBundle([]string{
		"precise/mediawiki-0",
	}),
	"bundle/mediawiki-simple-47": relationTestingBundle([]string{
		"precise/mediawiki-0",
		"mysql",
	}),
	"bundle/mediawiki-simple-48": relationTestingBundle([]string{
		"precise/mediawiki-0",
	}),
}

var metaBundlesContainingTests = []struct {
	// Description of the test.
	about string
	// The id of the charm for which related bundles are returned.
	id string
	// The querystring to append to the resulting charmstore URL.
	querystring string
	// The expected status code of the response.
	expectStatus int
	// The expected response body.
	expectBody interface{}
}{{
	about:        "specific charm present in several bundles",
	id:           "utopic/wordpress-42",
	expectStatus: http.StatusOK,
	expectBody: []*params.MetaAnyResponse{{
		Id: charm.MustParseReference("bundle/useless-0"),
	}, {
		Id: charm.MustParseReference("bundle/wordpress-complex-1"),
	}, {
		Id: charm.MustParseReference("bundle/wordpress-simple-0"),
	}},
}, {
	about:        "specific charm present in one bundle",
	id:           "trusty/memcached-2",
	expectStatus: http.StatusOK,
	expectBody: []*params.MetaAnyResponse{{
		Id: charm.MustParseReference("bundle/wordpress-complex-1"),
	}},
}, {
	about:        "specific charm not present in any bundle",
	id:           "trusty/django-42",
	expectStatus: http.StatusOK,
	expectBody:   []*params.MetaAnyResponse{},
}, {
	about:        "specific charm with includes",
	id:           "trusty/mysql-1",
	querystring:  "?include=archive-size&include=bundle-metadata",
	expectStatus: http.StatusOK,
	expectBody: []*params.MetaAnyResponse{{
		Id: charm.MustParseReference("bundle/wordpress-complex-1"),
		Meta: map[string]interface{}{
			"archive-size":    params.ArchiveSizeResponse{Size: fakeBlobSize},
			"bundle-metadata": metaBundlesContainingBundles["bundle/wordpress-complex-1"].Data(),
		},
	}},
}, {
	about:        "partial charm id",
	id:           "mysql", // The test will add cs:utopic/mysql-0.
	expectStatus: http.StatusOK,
	expectBody: []*params.MetaAnyResponse{{
		Id: charm.MustParseReference("bundle/wordpress-simple-0"),
	}},
}, {
	about:        "any series set to true",
	id:           "trusty/mysql-0",
	querystring:  "?any-series=1",
	expectStatus: http.StatusOK,
	expectBody: []*params.MetaAnyResponse{{
		Id: charm.MustParseReference("bundle/wordpress-complex-1"),
	}, {
		Id: charm.MustParseReference("bundle/wordpress-simple-0"),
	}},
}, {
	about:        "any series and all-results set to true",
	id:           "trusty/mysql-0",
	querystring:  "?any-series=1&all-results=1",
	expectStatus: http.StatusOK,
	expectBody: []*params.MetaAnyResponse{{
		Id: charm.MustParseReference("bundle/wordpress-complex-1"),
	}, {
		// This result is included even if the latest wordpress-simple does not
		// contain the mysql-0 charm.
		Id: charm.MustParseReference("bundle/wordpress-simple-0"),
	}},
}, {
	about:        "invalid any series",
	id:           "utopic/mysql-0",
	querystring:  "?any-series=true",
	expectStatus: http.StatusBadRequest,
	expectBody: params.Error{
		Code:    params.ErrBadRequest,
		Message: `invalid value for any-series: unexpected bool value "true" (must be "0" or "1")`,
	},
}, {
	about:        "any revision set to true",
	id:           "trusty/memcached-99",
	querystring:  "?any-revision=1",
	expectStatus: http.StatusOK,
	expectBody: []*params.MetaAnyResponse{{
		Id: charm.MustParseReference("bundle/django-generic-42"),
	}, {
		Id: charm.MustParseReference("bundle/wordpress-complex-1"),
	}},
}, {
	about:        "invalid any revision",
	id:           "trusty/memcached-99",
	querystring:  "?any-revision=why-not",
	expectStatus: http.StatusBadRequest,
	expectBody: params.Error{
		Code:    params.ErrBadRequest,
		Message: `invalid value for any-revision: unexpected bool value "why-not" (must be "0" or "1")`,
	},
}, {
	about:        "all-results set to true",
	id:           "precise/mediawiki-0",
	expectStatus: http.StatusOK,
	querystring:  "?all-results=1",
	expectBody: []*params.MetaAnyResponse{{
		Id: charm.MustParseReference("bundle/mediawiki-simple-48"),
	}, {
		Id: charm.MustParseReference("bundle/mediawiki-simple-47"),
	}, {
		Id: charm.MustParseReference("bundle/mediawiki-simple-46"),
	}},
}, {
	about:        "all-results set to false",
	id:           "precise/mediawiki-0",
	expectStatus: http.StatusOK,
	expectBody: []*params.MetaAnyResponse{{
		Id: charm.MustParseReference("bundle/mediawiki-simple-48"),
	}},
}, {
	about:        "invalid all-results",
	id:           "trusty/memcached-99",
	querystring:  "?all-results=yes!",
	expectStatus: http.StatusBadRequest,
	expectBody: params.Error{
		Code:    params.ErrBadRequest,
		Message: `invalid value for all-results: unexpected bool value "yes!" (must be "0" or "1")`,
	},
}, {
	about:        "any series and revision, all results",
	id:           "saucy/mysql-99",
	querystring:  "?any-series=1&any-revision=1&all-results=1",
	expectStatus: http.StatusOK,
	expectBody: []*params.MetaAnyResponse{{
		Id: charm.MustParseReference("bundle/django-generic-42"),
	}, {
		Id: charm.MustParseReference("bundle/mediawiki-simple-47"),
	}, {
		Id: charm.MustParseReference("bundle/wordpress-complex-1"),
	}, {
		Id: charm.MustParseReference("bundle/wordpress-simple-1"),
	}, {
		Id: charm.MustParseReference("bundle/wordpress-simple-0"),
	}},
}, {
	about:        "any series, any revision",
	id:           "saucy/mysql-99",
	querystring:  "?any-series=1&any-revision=1",
	expectStatus: http.StatusOK,
	expectBody: []*params.MetaAnyResponse{{
		Id: charm.MustParseReference("bundle/django-generic-42"),
	}, {
		Id: charm.MustParseReference("bundle/mediawiki-simple-47"),
	}, {
		Id: charm.MustParseReference("bundle/wordpress-complex-1"),
	}, {
		Id: charm.MustParseReference("bundle/wordpress-simple-1"),
	}},
}, {
	about:        "any series and revision, last results",
	id:           "saucy/mediawiki",
	querystring:  "?any-series=1&any-revision=1",
	expectStatus: http.StatusOK,
	expectBody: []*params.MetaAnyResponse{{
		Id: charm.MustParseReference("bundle/mediawiki-simple-48"),
	}, {
		Id: charm.MustParseReference("bundle/useless-0"),
	}},
}, {
	about:        "any series and revision with includes",
	id:           "saucy/wordpress-99",
	querystring:  "?any-series=1&any-revision=1&include=archive-size&include=bundle-metadata",
	expectStatus: http.StatusOK,
	expectBody: []*params.MetaAnyResponse{{
		Id: charm.MustParseReference("bundle/useless-0"),
		Meta: map[string]interface{}{
			"archive-size":    params.ArchiveSizeResponse{Size: fakeBlobSize},
			"bundle-metadata": metaBundlesContainingBundles["bundle/useless-0"].Data(),
		},
	}, {
		Id: charm.MustParseReference("bundle/wordpress-complex-1"),
		Meta: map[string]interface{}{
			"archive-size":    params.ArchiveSizeResponse{Size: fakeBlobSize},
			"bundle-metadata": metaBundlesContainingBundles["bundle/wordpress-complex-1"].Data(),
		},
	}, {
		Id: charm.MustParseReference("bundle/wordpress-simple-1"),
		Meta: map[string]interface{}{
			"archive-size":    params.ArchiveSizeResponse{Size: fakeBlobSize},
			"bundle-metadata": metaBundlesContainingBundles["bundle/wordpress-simple-1"].Data(),
		},
	}},
}, {
	about:        "include-error",
	id:           "utopic/wordpress-42",
	querystring:  "?include=no-such",
	expectStatus: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: `cannot retrieve bundle metadata: unrecognized metadata name "no-such"`,
	},
}}

func (s *RelationsSuite) TestMetaBundlesContaining(c *gc.C) {
	// Add the bundles used for testing to the database.
	for id, b := range metaBundlesContainingBundles {
		var purl *charm.Reference
		pRev := -1
		url := charm.MustParseReference(id)
		if url.User == "" {
			purl = new(charm.Reference)
			*purl = *url
			url.User = "charmers"
			pRev = purl.Revision
		}
		// The blob related info are not used in these tests.
		// The charm-bundle relations are retrieved from the entities
		// collection, without accessing the blob store.
		err := s.store.AddBundle(b, charmstore.AddParams{
			URL:                 url,
			BlobName:            "blobName",
			BlobHash:            fakeBlobHash,
			BlobSize:            fakeBlobSize,
			PromulgatedURL:      purl,
			PromulgatedRevision: pRev,
		})
		c.Assert(err, gc.IsNil)
	}

	for i, test := range metaBundlesContainingTests {
		c.Logf("test %d: %s", i, test.about)

		// Expand the URL if required before adding the charm to the database,
		// so that at least one matching charm can be resolved.
		var purl *charm.Reference
		pRev := -1
		url := charm.MustParseReference(test.id)
		if url.Series == "" {
			url.Series = "utopic"
		}
		if url.Revision == -1 {
			url.Revision = 0
		}
		if url.User == "" {
			purl = new(charm.Reference)
			*purl = *url
			url.User = "charmers"
			pRev = purl.Revision
		}

		// Add the charm we need bundle info on to the database.
		err := s.store.AddCharm(&relationTestingCharm{}, charmstore.AddParams{
			URL:                 url,
			BlobName:            "blobName",
			BlobHash:            fakeBlobHash,
			BlobSize:            fakeBlobSize,
			PromulgatedURL:      purl,
			PromulgatedRevision: pRev,
		})
		c.Assert(err, gc.IsNil)

		// Perform the request and ensure the response is what we expect.
		storeURL := storeURL(test.id + "/meta/bundles-containing" + test.querystring)
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      s.srv,
			URL:          storeURL,
			ExpectStatus: test.expectStatus,
			ExpectBody:   test.expectBody,
		})

		// Clean up the charm entity in the store.
		err = s.store.DB.Entities().Remove(bson.D{{"_id", url}})
		c.Assert(err, gc.IsNil)
	}
}

// relationTestingBundle returns a bundle for use in relation
// testing. The urls parameter holds a list of charm references
// to be included in the bundle.
// For each URL, a corresponding service is automatically created.
func relationTestingBundle(urls []string) charm.Bundle {
	services := make(map[string]*charm.ServiceSpec, len(urls))
	for i, url := range urls {
		service := &charm.ServiceSpec{
			Charm:    url,
			NumUnits: 1,
		}
		services[fmt.Sprintf("service-%d", i)] = service
	}
	return &testingBundle{
		data: &charm.BundleData{
			Services: services,
		},
	}
}

// testingBundle is a bundle implementation that
// returns bundle metadata held in the data field.
type testingBundle struct {
	data *charm.BundleData
}

func (b *testingBundle) Data() *charm.BundleData {
	return b.data
}

func (b *testingBundle) ReadMe() string {
	// For the purposes of this implementation, the charm readme is not
	// relevant.
	return ""
}
