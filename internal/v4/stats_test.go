// Copyright 2012 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/storetesting"
	"github.com/juju/charmstore/params"
)

type StatsSuite struct {
	storetesting.IsolatedMgoSuite
	srv   http.Handler
	store *charmstore.Store
}

var _ = gc.Suite(&StatsSuite{})

func (s *StatsSuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	s.srv, s.store = newServer(c, s.Session)
}

// checkCounterSum checks that statistics are properly collected.
// It retries a few times as they are generally collected in background.
func checkCounterSum(c *gc.C, store *charmstore.Store, key []string, prefix bool, expected int64) {
	var sum int64
	for retry := 0; retry < 10; retry++ {
		time.Sleep(100 * time.Millisecond)
		req := charmstore.CounterRequest{
			Key:    key,
			Prefix: prefix,
		}
		cs, err := store.Counters(&req)
		c.Assert(err, gc.IsNil)
		if sum = cs[0].Count; sum == expected {
			if expected == 0 && retry < 2 {
				continue // Wait a bit to make sure.
			}
			return
		}
	}
	c.Errorf("counter sum for %#v is %d, want %d", key, sum, expected)
}

func (s *StatsSuite) TestServerStatsStatus(c *gc.C) {
	tests := []struct {
		path    string
		status  int
		message string
		code    params.ErrorCode
	}{{
		path:    "stats/counter/",
		status:  http.StatusForbidden,
		message: "forbidden",
		code:    params.ErrForbidden,
	}, {
		path:    "stats/counter/*",
		status:  http.StatusForbidden,
		message: "unknown key",
		code:    params.ErrForbidden,
	}, {
		path:    "stats/counter/any/",
		status:  http.StatusNotFound,
		message: "not found",
		code:    params.ErrNotFound,
	}, {
		path:    "stats/",
		status:  http.StatusNotFound,
		message: "not found",
		code:    params.ErrNotFound,
	}, {
		path:    "stats/any",
		status:  http.StatusNotFound,
		message: "not found",
		code:    params.ErrNotFound,
	}, {
		path:    "stats/counter/any?by=fortnight",
		status:  http.StatusBadRequest,
		message: `invalid 'by' value "fortnight"`,
		code:    params.ErrBadRequest,
	}, {
		path:    "stats/counter/any?start=tomorrow",
		status:  http.StatusBadRequest,
		message: `invalid 'start' value "tomorrow": parsing time "tomorrow" as "2006-01-02": cannot parse "tomorrow" as "2006"`,
		code:    params.ErrBadRequest,
	}, {
		path:    "stats/counter/any?stop=3",
		status:  http.StatusBadRequest,
		message: `invalid 'stop' value "3": parsing time "3" as "2006-01-02": cannot parse "3" as "2006"`,
		code:    params.ErrBadRequest,
	}}
	for i, test := range tests {
		c.Logf("test %d. %s", i, test.path)
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
			Handler:    s.srv,
			URL:        storeURL(test.path),
			ExpectCode: test.status,
			ExpectBody: params.Error{
				Message: test.message,
				Code:    test.code,
			},
		})
	}
}

func (s *StatsSuite) TestStatsCounter(c *gc.C) {
	if !storetesting.MongoJSEnabled() {
		c.Skip("MongoDB javascript not available")
	}

	for _, key := range [][]string{{"a", "b"}, {"a", "b"}, {"a", "c"}, {"a"}} {
		err := s.store.IncCounter(key)
		c.Assert(err, gc.IsNil)
	}

	var all []interface{}
	err := s.store.DB.StatCounters().Find(nil).All(&all)
	c.Assert(err, gc.IsNil)
	data, err := json.Marshal(all)
	c.Assert(err, gc.IsNil)
	c.Logf("%s", data)

	expected := map[string]int64{
		"a:b":   2,
		"a:b:*": 0,
		"a:*":   3,
		"a":     1,
		"a:b:c": 0,
	}

	for counter, n := range expected {
		c.Logf("test %q", counter)
		url := storeURL("stats/counter/" + counter)
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
			Handler: s.srv,
			URL:     url,
			ExpectBody: []params.Statistic{{
				Count: n,
			}},
		})
	}
}

func (s *StatsSuite) TestStatsCounterList(c *gc.C) {
	if !storetesting.MongoJSEnabled() {
		c.Skip("MongoDB javascript not available")
	}

	incs := [][]string{
		{"a"},
		{"a", "b"},
		{"a", "b", "c"},
		{"a", "b", "c"},
		{"a", "b", "d"},
		{"a", "b", "e"},
		{"a", "f", "g"},
		{"a", "f", "h"},
		{"a", "i"},
		{"j", "k"},
	}
	for _, key := range incs {
		err := s.store.IncCounter(key)
		c.Assert(err, gc.IsNil)
	}

	tests := []struct {
		key    string
		result []params.Statistic
	}{{
		key: "a",
		result: []params.Statistic{{
			Key:   "a",
			Count: 1,
		}},
	}, {
		key: "a:*",
		result: []params.Statistic{{
			Key:   "a:b:*",
			Count: 4,
		}, {
			Key:   "a:f:*",
			Count: 2,
		}, {
			Key:   "a:b",
			Count: 1,
		}, {
			Key:   "a:i",
			Count: 1,
		}},
	}, {
		key: "a:b:*",
		result: []params.Statistic{{
			Key:   "a:b:c",
			Count: 2,
		}, {
			Key:   "a:b:d",
			Count: 1,
		}, {
			Key:   "a:b:e",
			Count: 1,
		}},
	}, {
		key: "a:*",
		result: []params.Statistic{{
			Key:   "a:b:*",
			Count: 4,
		}, {
			Key:   "a:f:*",
			Count: 2,
		}, {
			Key:   "a:b",
			Count: 1,
		}, {
			Key:   "a:i",
			Count: 1,
		}},
	}}

	for i, test := range tests {
		c.Logf("test %d: %s", i, test.key)
		url := storeURL("stats/counter/" + test.key + "?list=1")
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
			Handler:    s.srv,
			URL:        url,
			ExpectBody: test.result,
		})
	}
}

func (s *StatsSuite) TestStatsCounterBy(c *gc.C) {
	if !storetesting.MongoJSEnabled() {
		c.Skip("MongoDB javascript not available")
	}

	incs := []struct {
		key []string
		day int
	}{
		{[]string{"a"}, 1},
		{[]string{"a"}, 1},
		{[]string{"b"}, 1},
		{[]string{"a", "b"}, 1},
		{[]string{"a", "c"}, 1},
		{[]string{"a"}, 3},
		{[]string{"a", "b"}, 3},
		{[]string{"b"}, 9},
		{[]string{"b"}, 9},
		{[]string{"a", "c", "d"}, 9},
		{[]string{"a", "c", "e"}, 9},
		{[]string{"a", "c", "f"}, 9},
	}

	day := func(i int) time.Time {
		return time.Date(2012, time.May, i, 0, 0, 0, 0, time.UTC)
	}

	for i, inc := range incs {
		t := day(inc.day)
		// Ensure each entry is unique by adding
		// a sufficient increment for each test.
		t = t.Add(time.Duration(i) * charmstore.StatsGranularity)

		err := s.store.IncCounterAtTime(inc.key, t)
		c.Assert(err, gc.IsNil)
	}

	tests := []struct {
		request charmstore.CounterRequest
		result  []params.Statistic
	}{{
		request: charmstore.CounterRequest{
			Key:    []string{"a"},
			Prefix: false,
			List:   false,
			By:     charmstore.ByDay,
		},
		result: []params.Statistic{{
			Date:  "2012-05-01",
			Count: 2,
		}, {
			Date:  "2012-05-03",
			Count: 1,
		}},
	}, {
		request: charmstore.CounterRequest{
			Key:    []string{"a"},
			Prefix: true,
			List:   false,
			By:     charmstore.ByDay,
		},
		result: []params.Statistic{{
			Date:  "2012-05-01",
			Count: 2,
		}, {
			Date:  "2012-05-03",
			Count: 1,
		}, {
			Date:  "2012-05-09",
			Count: 3,
		}},
	}, {
		request: charmstore.CounterRequest{
			Key:    []string{"a"},
			Prefix: true,
			List:   false,
			By:     charmstore.ByDay,
			Start:  time.Date(2012, 5, 2, 0, 0, 0, 0, time.UTC),
		},
		result: []params.Statistic{{
			Date:  "2012-05-03",
			Count: 1,
		}, {
			Date:  "2012-05-09",
			Count: 3,
		}},
	}, {
		request: charmstore.CounterRequest{
			Key:    []string{"a"},
			Prefix: true,
			List:   false,
			By:     charmstore.ByDay,
			Stop:   time.Date(2012, 5, 4, 0, 0, 0, 0, time.UTC),
		},
		result: []params.Statistic{{
			Date:  "2012-05-01",
			Count: 2,
		}, {
			Date:  "2012-05-03",
			Count: 1,
		}},
	}, {
		request: charmstore.CounterRequest{
			Key:    []string{"a"},
			Prefix: true,
			List:   false,
			By:     charmstore.ByDay,
			Start:  time.Date(2012, 5, 3, 0, 0, 0, 0, time.UTC),
			Stop:   time.Date(2012, 5, 3, 0, 0, 0, 0, time.UTC),
		},
		result: []params.Statistic{{
			Date:  "2012-05-03",
			Count: 1,
		}},
	}, {
		request: charmstore.CounterRequest{
			Key:    []string{"a"},
			Prefix: true,
			List:   true,
			By:     charmstore.ByDay,
		},
		result: []params.Statistic{{
			Key:   "a:b",
			Date:  "2012-05-01",
			Count: 1,
		}, {
			Key:   "a:c",
			Date:  "2012-05-01",
			Count: 1,
		}, {
			Key:   "a:b",
			Date:  "2012-05-03",
			Count: 1,
		}, {
			Key:   "a:c:*",
			Date:  "2012-05-09",
			Count: 3,
		}},
	}, {
		request: charmstore.CounterRequest{
			Key:    []string{"a"},
			Prefix: true,
			List:   false,
			By:     charmstore.ByWeek,
		},
		result: []params.Statistic{{
			Date:  "2012-05-06",
			Count: 3,
		}, {
			Date:  "2012-05-13",
			Count: 3,
		}},
	}, {
		request: charmstore.CounterRequest{
			Key:    []string{"a"},
			Prefix: true,
			List:   true,
			By:     charmstore.ByWeek,
		},
		result: []params.Statistic{{
			Key:   "a:b",
			Date:  "2012-05-06",
			Count: 2,
		}, {
			Key:   "a:c",
			Date:  "2012-05-06",
			Count: 1,
		}, {
			Key:   "a:c:*",
			Date:  "2012-05-13",
			Count: 3,
		}},
	}}

	for i, test := range tests {
		flags := make(url.Values)
		url := storeURL("stats/counter/" + strings.Join(test.request.Key, ":"))
		if test.request.Prefix {
			url += ":*"
		}
		if test.request.List {
			flags.Set("list", "1")
		}
		if !test.request.Start.IsZero() {
			flags.Set("start", test.request.Start.Format("2006-01-02"))
		}
		if !test.request.Stop.IsZero() {
			flags.Set("stop", test.request.Stop.Format("2006-01-02"))
		}
		switch test.request.By {
		case charmstore.ByDay:
			flags.Set("by", "day")
		case charmstore.ByWeek:
			flags.Set("by", "week")
		}
		if len(flags) > 0 {
			url += "?" + flags.Encode()
		}
		c.Logf("test %d: %s", i, url)
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
			Handler:    s.srv,
			URL:        url,
			ExpectBody: test.result,
		})
	}
}
