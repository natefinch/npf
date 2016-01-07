package v5_test

import (
	"net/http"
	"net/http/httptest"

	gc "gopkg.in/check.v1"
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
