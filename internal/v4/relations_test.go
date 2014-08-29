// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4_test

import (
	"fmt"
	"net/http"

	"gopkg.in/juju/charm.v3"
	"gopkg.in/mgo.v2/bson"
	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore/internal/blobstore"
	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/storetesting"
	"github.com/juju/charmstore/params"
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
	s.srv, s.store = newServer(c, s.Session, serverParams)
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
			"memcache": []params.MetaAnyResponse{{
				Id: mustParseReference("utopic/memcached-42"),
			}},
			"mount": []params.MetaAnyResponse{{
				Id: mustParseReference("precise/nfs-1"),
			}},
		},
		Requires: map[string][]params.MetaAnyResponse{
			"http": []params.MetaAnyResponse{{
				Id: mustParseReference("precise/haproxy-48"),
			}, {
				Id: mustParseReference("trusty/haproxy-47"),
			}},
		},
	},
}, {
	about:  "only provides",
	charms: metaCharmRelatedCharms,
	id:     "trusty/haproxy-47",
	expectBody: params.RelatedResponse{
		Provides: map[string][]params.MetaAnyResponse{
			"http": []params.MetaAnyResponse{{
				Id: mustParseReference("utopic/wordpress-0"),
			}},
		},
	},
}, {
	about:  "only requires",
	charms: metaCharmRelatedCharms,
	id:     "utopic/memcached-42",
	expectBody: params.RelatedResponse{
		Requires: map[string][]params.MetaAnyResponse{
			"memcache": []params.MetaAnyResponse{{
				Id: mustParseReference("utopic/wordpress-0"),
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
			"memcache": []params.MetaAnyResponse{{
				Id: mustParseReference("utopic/memcached-1"),
			}, {
				Id: mustParseReference("utopic/memcached-2"),
			}, {
				Id: mustParseReference("utopic/memcached-3"),
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
			"memcache": []params.MetaAnyResponse{{
				Id: mustParseReference("utopic/memcached-1"),
			}, {
				Id: mustParseReference("utopic/memcached-2"),
			}, {
				Id: mustParseReference("utopic/redis-90"),
			}},
			"mount": []params.MetaAnyResponse{{
				Id: mustParseReference("precise/nfs-42"),
			}, {
				Id: mustParseReference("precise/nfs-47"),
			}, {
				Id: mustParseReference("trusty/nfs-47"),
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
			"mount": []params.MetaAnyResponse{{
				Id: mustParseReference("utopic/wordpress-0"),
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
		url := mustParseReference(id)
		// The blob related info are not used in these tests.
		// The related charms are retrieved from the entities collection,
		// without accessing the blob store.
		err := s.store.AddCharm(url, ch, "blobName", fakeBlobHash, fakeBlobSize)
		c.Assert(err, gc.IsNil)
	}
}

func (s *RelationsSuite) TestMetaCharmRelated(c *gc.C) {
	for i, test := range metaCharmRelatedTests {
		c.Logf("test %d: %s", i, test.about)
		s.addCharms(c, test.charms)
		storeURL := storeURL(test.id + "/meta/charm-related" + test.querystring)
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
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
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
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
	"bundle/wordpress-simple-0": &relationTestingBundle{
		urls: []string{
			"cs:utopic/wordpress-42",
			"cs:utopic/mysql-0",
		},
	},
	"bundle/wordpress-simple-1": &relationTestingBundle{
		urls: []string{
			"cs:utopic/wordpress-47",
			"cs:utopic/mysql-1",
		},
	},
	"bundle/wordpress-complex-1": &relationTestingBundle{
		urls: []string{
			"cs:utopic/wordpress-42",
			"cs:utopic/wordpress-47",
			"cs:trusty/mysql-0",
			"cs:trusty/mysql-1",
			"cs:trusty/memcached-2",
		},
	},
	"bundle/django-generic-42": &relationTestingBundle{
		urls: []string{
			"django",
			"django",
			"mysql-1",
			"trusty/memcached",
		},
	},
	"bundle/useless-0": &relationTestingBundle{
		urls: []string{"cs:utopic/wordpress-42"},
	},
	"bundle/mediawiki-47": &relationTestingBundle{
		urls: []string{
			"precise/mediawiki-0",
			"mysql",
		},
	},
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
		Id: mustParseReference("bundle/useless-0"),
	}, {
		Id: mustParseReference("bundle/wordpress-complex-1"),
	}, {
		Id: mustParseReference("bundle/wordpress-simple-0"),
	}},
}, {
	about:        "specific charm present in one bundle",
	id:           "trusty/memcached-2",
	expectStatus: http.StatusOK,
	expectBody: []*params.MetaAnyResponse{{
		Id: mustParseReference("bundle/wordpress-complex-1"),
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
		Id: mustParseReference("bundle/wordpress-complex-1"),
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
		Id: mustParseReference("bundle/wordpress-simple-0"),
	}},
}, {
	about:        "any series set to true",
	id:           "trusty/mysql-0",
	querystring:  "?any-series=1",
	expectStatus: http.StatusOK,
	expectBody: []*params.MetaAnyResponse{{
		Id: mustParseReference("bundle/wordpress-complex-1"),
	}, {
		Id: mustParseReference("bundle/wordpress-simple-0"),
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
		Id: mustParseReference("bundle/django-generic-42"),
	}, {
		Id: mustParseReference("bundle/wordpress-complex-1"),
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
	about:        "any series and revision",
	id:           "saucy/mysql-99",
	querystring:  "?any-series=1&any-revision=1",
	expectStatus: http.StatusOK,
	expectBody: []*params.MetaAnyResponse{{
		Id: mustParseReference("bundle/django-generic-42"),
	}, {
		Id: mustParseReference("bundle/mediawiki-47"),
	}, {
		Id: mustParseReference("bundle/wordpress-complex-1"),
	}, {
		Id: mustParseReference("bundle/wordpress-simple-0"),
	}, {
		Id: mustParseReference("bundle/wordpress-simple-1"),
	}},
}, {
	about:        "any series and revision with includes",
	id:           "saucy/wordpress-99",
	querystring:  "?any-series=1&any-revision=1&include=archive-size&include=bundle-metadata",
	expectStatus: http.StatusOK,
	expectBody: []*params.MetaAnyResponse{{
		Id: mustParseReference("bundle/useless-0"),
		Meta: map[string]interface{}{
			"archive-size":    params.ArchiveSizeResponse{Size: fakeBlobSize},
			"bundle-metadata": metaBundlesContainingBundles["bundle/useless-0"].Data(),
		},
	}, {
		Id: mustParseReference("bundle/wordpress-complex-1"),
		Meta: map[string]interface{}{
			"archive-size":    params.ArchiveSizeResponse{Size: fakeBlobSize},
			"bundle-metadata": metaBundlesContainingBundles["bundle/wordpress-complex-1"].Data(),
		},
	}, {
		Id: mustParseReference("bundle/wordpress-simple-0"),
		Meta: map[string]interface{}{
			"archive-size":    params.ArchiveSizeResponse{Size: fakeBlobSize},
			"bundle-metadata": metaBundlesContainingBundles["bundle/wordpress-simple-0"].Data(),
		},
	}, {
		Id: mustParseReference("bundle/wordpress-simple-1"),
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
		url := mustParseReference(id)
		// The blob related info are not used in these tests.
		// The charm-bundle relations are retrieved from the entities
		// collection, without accessing the blob store.
		err := s.store.AddBundle(url, b, "blobName", fakeBlobHash, fakeBlobSize)
		c.Assert(err, gc.IsNil)
	}

	for i, test := range metaBundlesContainingTests {
		c.Logf("test %d: %s", i, test.about)

		// Expand the URL if required before adding the charm to the database,
		// so that at least one matching charm can be resolved.
		url := mustParseReference(test.id)
		if url.Series == "" {
			url.Series = "utopic"
		}
		if url.Revision == -1 {
			url.Revision = 0
		}

		// Add the charm we need bundle info on to the database.
		err := s.store.AddCharm(url, &relationTestingCharm{}, "blobName", fakeBlobHash, fakeBlobSize)
		c.Assert(err, gc.IsNil)

		// Perform the request and ensure the response is what we expect.
		storeURL := storeURL(test.id + "/meta/bundles-containing" + test.querystring)
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
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

// relationTestingBundle implements charm.Bundle, and it is used for testing
// charm to bundle relations (for instance for the bundles-containing call).
type relationTestingBundle struct {
	// urls is a list of charm references to be included in the bundle.
	// For each URL, a corresponding service is automatically created.
	urls []string
}

func (b *relationTestingBundle) Data() *charm.BundleData {
	services := make(map[string]*charm.ServiceSpec, len(b.urls))
	for i, url := range b.urls {
		service := &charm.ServiceSpec{
			Charm:    url,
			NumUnits: 1,
		}
		services[fmt.Sprintf("service-%d", i)] = service
	}
	return &charm.BundleData{
		Services: services,
	}
}

func (b *relationTestingBundle) ReadMe() string {
	// For the purposes of this implementation, the charm readme is not
	// relevant.
	return ""
}
