// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4_test

import (
	"net/http"

	"gopkg.in/juju/charm.v3"
	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/storetesting"
	"github.com/juju/charmstore/params"
)

type RelationsSuite struct {
	storetesting.IsolatedMgoSuite
	srv   http.Handler
	store *charmstore.Store
}

var _ = gc.Suite(&RelationsSuite{})

func (s *RelationsSuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	s.srv, s.store = newServer(c, s.Session)
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
				Id: mustParseReference("trusty/haproxy-47"),
			}, {
				Id: mustParseReference("precise/haproxy-48"),
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
	about:       "includes",
	charms:      metaCharmRelatedCharms,
	id:          "precise/nfs-1",
	querystring: "?include=archive-size&include=charm-metadata",
	expectBody: params.RelatedResponse{
		Requires: map[string][]params.MetaAnyResponse{
			"mount": []params.MetaAnyResponse{{
				Id: mustParseReference("utopic/wordpress-0"),
				Meta: map[string]interface{}{
					"archive-size": params.ArchiveSizeResponse{Size: 42},
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
	blobSize := int64(42)
	for id, ch := range charms {
		url := mustParseReference(id)
		// The blob related info are not used in these tests.
		// The related charms are retrieved from the entities collection,
		// without accessing the blob store.
		err := s.store.AddCharm(url, ch, "blobName", "blobHash", blobSize)
		c.Assert(err, gc.IsNil)
	}
}

func (s *RelationsSuite) TestMetaCharmRelated(c *gc.C) {
	for i, test := range metaCharmRelatedTests {
		c.Logf("test %d: %s", i, test.about)
		s.addCharms(c, test.charms)
		storeURL := storeURL(test.id + "/meta/charm-related" + test.querystring)
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
			Handler:    s.srv,
			URL:        storeURL,
			ExpectCode: http.StatusOK,
			ExpectBody: test.expectBody,
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
		Handler:    s.srv,
		URL:        storeURL,
		ExpectCode: http.StatusInternalServerError,
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
