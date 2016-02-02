package v5_test

import (
	"net/http"
	"net/http/httptest"

	"github.com/juju/testing/httptesting"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"

	"gopkg.in/juju/charmstore.v5-unstable/internal/storetesting"
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
	"0 ~charmers/trusty/wordpress-0": storetesting.NewCharm(relationMeta(
		"requires cache memcache",
		"requires nfs mount",
	)),
	"1 ~charmers/utopic/memcached-1": storetesting.NewCharm(relationMeta(
		"provides cache memcache",
	)),
	"2 ~charmers/utopic/memcached-2": storetesting.NewCharm(relationMeta(
		"provides cache memcache",
	)),
	"90 ~charmers/utopic/redis-90": storetesting.NewCharm(relationMeta(
		"provides cache memcache",
	)),
	"47 ~charmers/trusty/nfs-47": storetesting.NewCharm(relationMeta(
		"provides nfs mount",
	)),
	"42 ~charmers/precise/nfs-42": storetesting.NewCharm(relationMeta(
		"provides nfs mount",
	)),
	"47 ~charmers/precise/nfs-47": storetesting.NewCharm(relationMeta(
		"provides nfs mount",
	)),
}

var benchmarkCharmRelatedExpectBody = params.RelatedResponse{
	Provides: map[string][]params.EntityResult{
		"memcache": {{
			Id: charm.MustParseURL("utopic/memcached-1"),
			Meta: map[string]interface{}{
				"id-name": params.IdNameResponse{"memcached"},
			},
		}, {
			Id: charm.MustParseURL("utopic/memcached-2"),
			Meta: map[string]interface{}{
				"id-name": params.IdNameResponse{"memcached"},
			},
		}, {
			Id: charm.MustParseURL("utopic/redis-90"),
			Meta: map[string]interface{}{
				"id-name": params.IdNameResponse{"redis"},
			},
		}},
		"mount": {{
			Id: charm.MustParseURL("precise/nfs-42"),
			Meta: map[string]interface{}{
				"id-name": params.IdNameResponse{"nfs"},
			},
		}, {
			Id: charm.MustParseURL("precise/nfs-47"),
			Meta: map[string]interface{}{
				"id-name": params.IdNameResponse{"nfs"},
			},
		}, {
			Id: charm.MustParseURL("trusty/nfs-47"),
			Meta: map[string]interface{}{
				"id-name": params.IdNameResponse{"nfs"},
			},
		}},
	},
}

func (s *BenchmarkSuite) BenchmarkCharmRelated(c *gc.C) {
	s.addCharms(c, benchmarkCharmRelatedAddCharms)
	expectBody := benchmarkCharmRelatedExpectBody
	srv := httptest.NewServer(s.srv)
	defer srv.Close()
	url := srv.URL + storeURL("trusty/wordpress-0/meta/charm-related?include=id-name")
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
	url := srv.URL + storeURL("trusty/wordpress-0/meta/charm-related?include=id-name")
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          url,
		ExpectStatus: http.StatusOK,
		ExpectBody:   expectBody,
	})
}
