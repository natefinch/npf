package v5_test

import (
	"net/http"
	"net/http/httptest"

	"github.com/juju/testing/httptesting"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"
)

type BenchmarkSuite struct {
	commonSuite
}

var _ = gc.Suite(&BenchmarkSuite{})

func (s *BenchmarkSuite) TestBenchmarkMeta(c *gc.C) {
	s.addPublicCharm(c, "wordpress", newResolvedURL("~charmers/precise/wordpress-23", 23))
	srv := httptest.NewServer(s.srv)
	defer srv.Close()
	url := srv.URL + storeURL("wordpress/meta/archive-size")
	c.Logf("benchmark start")
	resp, err := http.Get(url)
	if err != nil {
		c.Fatalf("get failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		c.Fatalf("response failed with code %v", resp.Status)
	}
}

func (s *BenchmarkSuite) BenchmarkMeta(c *gc.C) {
	s.addPublicCharm(c, "wordpress", newResolvedURL("~charmers/precise/wordpress-23", 23))
	srv := httptest.NewServer(s.srv)
	defer srv.Close()
	url := srv.URL + storeURL("wordpress/meta/archive-size")
	c.ResetTimer()
	for i := 0; i < c.N; i++ {
		resp, err := http.Get(url)
		if err != nil {
			c.Fatalf("get failed: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			c.Fatalf("response failed with code %v", resp.Status)
		}
	}
}

var benchmarkCharmRelatedAddCharms = map[string]charm.Charm{
	"0 ~charmers/trusty/wordpress-0": &relationTestingCharm{
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
	"1 ~charmers/utopic/memcached-1": &relationTestingCharm{
		provides: map[string]charm.Relation{
			"cache": {
				Name:      "cache",
				Role:      "provider",
				Interface: "memcache",
			},
		},
	},
	"2 ~charmers/utopic/memcached-2": &relationTestingCharm{
		provides: map[string]charm.Relation{
			"cache": {
				Name:      "cache",
				Role:      "provider",
				Interface: "memcache",
			},
		},
	},
	"90 ~charmers/utopic/redis-90": &relationTestingCharm{
		provides: map[string]charm.Relation{
			"cache": {
				Name:      "cache",
				Role:      "provider",
				Interface: "memcache",
			},
		},
	},
	"47 ~charmers/trusty/nfs-47": &relationTestingCharm{
		provides: map[string]charm.Relation{
			"nfs": {
				Name:      "nfs",
				Role:      "provider",
				Interface: "mount",
			},
		},
	},
	"42 ~charmers/precise/nfs-42": &relationTestingCharm{
		provides: map[string]charm.Relation{
			"nfs": {
				Name:      "nfs",
				Role:      "provider",
				Interface: "mount",
			},
		},
	},
	"47 ~charmers/precise/nfs-47": &relationTestingCharm{
		provides: map[string]charm.Relation{
			"nfs": {
				Name:      "nfs",
				Role:      "provider",
				Interface: "mount",
			},
		},
	},
}

var benchmarkCharmRelatedExpectBody = params.RelatedResponse{
	Provides: map[string][]params.EntityResult{
		"memcache": {{
			Id: charm.MustParseURL("utopic/memcached-1"),
			Meta: map[string]interface{}{
				"archive-size": params.ArchiveSizeResponse{Size: fakeBlobSize},
			},
		}, {
			Id: charm.MustParseURL("utopic/memcached-2"),
			Meta: map[string]interface{}{
				"archive-size": params.ArchiveSizeResponse{Size: fakeBlobSize},
			},
		}, {
			Id: charm.MustParseURL("utopic/redis-90"),
			Meta: map[string]interface{}{
				"archive-size": params.ArchiveSizeResponse{Size: fakeBlobSize},
			},
		}},
		"mount": {{
			Id: charm.MustParseURL("precise/nfs-42"),
			Meta: map[string]interface{}{
				"archive-size": params.ArchiveSizeResponse{Size: fakeBlobSize},
			},
		}, {
			Id: charm.MustParseURL("precise/nfs-47"),
			Meta: map[string]interface{}{
				"archive-size": params.ArchiveSizeResponse{Size: fakeBlobSize},
			},
		}, {
			Id: charm.MustParseURL("trusty/nfs-47"),
			Meta: map[string]interface{}{
				"archive-size": params.ArchiveSizeResponse{Size: fakeBlobSize},
			},
		}},
	},
}

func (s *BenchmarkSuite) BenchmarkCharmRelated(c *gc.C) {
	s.addCharms(c, benchmarkCharmRelatedAddCharms)
	expectBody := benchmarkCharmRelatedExpectBody
	srv := httptest.NewServer(s.srv)
	defer srv.Close()
	url := srv.URL + storeURL("trusty/wordpress-0/meta/charm-related?include=archive-size")
	c.ResetTimer()
	for i := 0; i < c.N; i++ {
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      s.srv,
			URL:          url,
			ExpectStatus: http.StatusOK,
			ExpectBody:   expectBody,
		})
	}
}

func (s *BenchmarkSuite) TestCharmRelated(c *gc.C) {
	s.addCharms(c, benchmarkCharmRelatedAddCharms)
	expectBody := benchmarkCharmRelatedExpectBody
	srv := httptest.NewServer(s.srv)
	defer srv.Close()
	url := srv.URL + storeURL("trusty/wordpress-0/meta/charm-related?include=archive-size")
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          url,
		ExpectStatus: http.StatusOK,
		ExpectBody:   expectBody,
	})
}
