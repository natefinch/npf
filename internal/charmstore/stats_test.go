// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore_test // import "gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v0/csclient/params"
	"gopkg.in/mgo.v2/bson"

	"gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"
	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
	"gopkg.in/juju/charmstore.v5-unstable/internal/storetesting"
)

type StatsSuite struct {
	jujutesting.IsolatedMgoSuite
	store *charmstore.Store
}

var _ = gc.Suite(&StatsSuite{})

func (s *StatsSuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	pool, err := charmstore.NewPool(s.Session.DB("foo"), nil, nil, charmstore.ServerParams{})
	c.Assert(err, gc.IsNil)
	s.store = pool.Store()
	pool.Close()
}

func (s *StatsSuite) TearDownTest(c *gc.C) {
	s.store.Close()
	s.IsolatedMgoSuite.TearDownTest(c)
}

func (s *StatsSuite) TestSumCounters(c *gc.C) {
	if !storetesting.MongoJSEnabled() {
		c.Skip("MongoDB JavaScript not available")
	}

	req := charmstore.CounterRequest{Key: []string{"a"}}
	cs, err := s.store.Counters(&req)
	c.Assert(err, gc.IsNil)
	c.Assert(cs, gc.DeepEquals, []charmstore.Counter{{Key: req.Key, Count: 0}})

	for i := 0; i < 10; i++ {
		err := s.store.IncCounter([]string{"a", "b", "c"})
		c.Assert(err, gc.IsNil)
	}
	for i := 0; i < 7; i++ {
		s.store.IncCounter([]string{"a", "b"})
		c.Assert(err, gc.IsNil)
	}
	for i := 0; i < 3; i++ {
		s.store.IncCounter([]string{"a", "z", "b"})
		c.Assert(err, gc.IsNil)
	}

	tests := []struct {
		key    []string
		prefix bool
		result int64
	}{
		{[]string{"a", "b", "c"}, false, 10},
		{[]string{"a", "b"}, false, 7},
		{[]string{"a", "z", "b"}, false, 3},
		{[]string{"a", "b", "c"}, true, 0},
		{[]string{"a", "b", "c", "d"}, false, 0},
		{[]string{"a", "b"}, true, 10},
		{[]string{"a"}, true, 20},
		{[]string{"b"}, true, 0},
	}

	for _, t := range tests {
		c.Logf("Test: %#v\n", t)
		req = charmstore.CounterRequest{Key: t.key, Prefix: t.prefix}
		cs, err := s.store.Counters(&req)
		c.Assert(err, gc.IsNil)
		c.Assert(cs, gc.DeepEquals, []charmstore.Counter{{Key: t.key, Prefix: t.prefix, Count: t.result}})
	}

	// High-level interface works. Now check that the data is
	// stored correctly.
	counters := s.store.DB.StatCounters()
	docs1, err := counters.Count()
	c.Assert(err, gc.IsNil)
	if docs1 != 3 && docs1 != 4 {
		fmt.Errorf("Expected 3 or 4 docs in counters collection, got %d", docs1)
	}

	// Hack times so that the next operation adds another document.
	err = counters.Update(nil, bson.D{{"$set", bson.D{{"t", 1}}}})
	c.Check(err, gc.IsNil)

	err = s.store.IncCounter([]string{"a", "b", "c"})
	c.Assert(err, gc.IsNil)

	docs2, err := counters.Count()
	c.Assert(err, gc.IsNil)
	c.Assert(docs2, gc.Equals, docs1+1)

	req = charmstore.CounterRequest{Key: []string{"a", "b", "c"}}
	cs, err = s.store.Counters(&req)
	c.Assert(err, gc.IsNil)
	c.Assert(cs, gc.DeepEquals, []charmstore.Counter{{Key: req.Key, Count: 11}})

	req = charmstore.CounterRequest{Key: []string{"a"}, Prefix: true}
	cs, err = s.store.Counters(&req)
	c.Assert(err, gc.IsNil)
	c.Assert(cs, gc.DeepEquals, []charmstore.Counter{{Key: req.Key, Prefix: true, Count: 21}})
}

func (s *StatsSuite) TestCountersReadOnlySum(c *gc.C) {
	if !storetesting.MongoJSEnabled() {
		c.Skip("MongoDB JavaScript not available")
	}

	// Summing up an unknown key shouldn't add the key to the database.
	req := charmstore.CounterRequest{Key: []string{"a", "b", "c"}}
	_, err := s.store.Counters(&req)
	c.Assert(err, gc.IsNil)

	tokens := s.Session.DB("juju").C("stat.tokens")
	n, err := tokens.Count()
	c.Assert(err, gc.IsNil)
	c.Assert(n, gc.Equals, 0)
}

func (s *StatsSuite) TestCountersTokenCaching(c *gc.C) {
	if !storetesting.MongoJSEnabled() {
		c.Skip("MongoDB JavaScript not available")
	}

	assertSum := func(i int, want int64) {
		req := charmstore.CounterRequest{Key: []string{strconv.Itoa(i)}}
		cs, err := s.store.Counters(&req)
		c.Assert(err, gc.IsNil)
		c.Assert(cs[0].Count, gc.Equals, want)
	}
	assertSum(100000, 0)

	const genSize = 1024

	// All of these will be cached, as we have two generations
	// of genSize entries each.
	for i := 0; i < genSize*2; i++ {
		err := s.store.IncCounter([]string{strconv.Itoa(i)})
		c.Assert(err, gc.IsNil)
	}

	// Now go behind the scenes and corrupt all the tokens.
	tokens := s.store.DB.StatTokens()
	iter := tokens.Find(nil).Iter()
	var t struct {
		Id    int    "_id"
		Token string "t"
	}
	for iter.Next(&t) {
		err := tokens.UpdateId(t.Id, bson.M{"$set": bson.M{"t": "corrupted" + t.Token}})
		c.Assert(err, gc.IsNil)
	}
	c.Assert(iter.Err(), gc.IsNil)

	// We can consult the counters for the cached entries still.
	// First, check that the newest generation is good.
	for i := genSize; i < genSize*2; i++ {
		assertSum(i, 1)
	}

	// Now, we can still access a single entry of the older generation,
	// but this will cause the generations to flip and thus the rest
	// of the old generation will go away as the top half of the
	// entries is turned into the old generation.
	assertSum(0, 1)

	// Now we've lost access to the rest of the old generation.
	for i := 1; i < genSize; i++ {
		assertSum(i, 0)
	}

	// But we still have all of the top half available since it was
	// moved into the old generation.
	for i := genSize; i < genSize*2; i++ {
		assertSum(i, 1)
	}
}

func (s *StatsSuite) TestCounterTokenUniqueness(c *gc.C) {
	if !storetesting.MongoJSEnabled() {
		c.Skip("MongoDB JavaScript not available")
	}

	var wg0, wg1 sync.WaitGroup
	wg0.Add(10)
	wg1.Add(10)
	for i := 0; i < 10; i++ {
		go func() {
			wg0.Done()
			wg0.Wait()
			defer wg1.Done()
			err := s.store.IncCounter([]string{"a"})
			c.Check(err, gc.IsNil)
		}()
	}
	wg1.Wait()

	req := charmstore.CounterRequest{Key: []string{"a"}}
	cs, err := s.store.Counters(&req)
	c.Assert(err, gc.IsNil)
	c.Assert(cs[0].Count, gc.Equals, int64(10))
}

func (s *StatsSuite) TestListCounters(c *gc.C) {
	if !storetesting.MongoJSEnabled() {
		c.Skip("MongoDB JavaScript not available")
	}

	incs := [][]string{
		{"c", "b", "a"}, // Assign internal id c < id b < id a, to make sorting slightly trickier.
		{"a"},
		{"a", "c"},
		{"a", "b"},
		{"a", "b", "c"},
		{"a", "b", "c"},
		{"a", "b", "e"},
		{"a", "b", "d"},
		{"a", "f", "g"},
		{"a", "f", "h"},
		{"a", "i"},
		{"a", "i", "j"},
		{"k", "l"},
	}
	for _, key := range incs {
		err := s.store.IncCounter(key)
		c.Assert(err, gc.IsNil)
	}

	tests := []struct {
		prefix []string
		result []charmstore.Counter
	}{
		{
			[]string{"a"},
			[]charmstore.Counter{
				{Key: []string{"a", "b"}, Prefix: true, Count: 4},
				{Key: []string{"a", "f"}, Prefix: true, Count: 2},
				{Key: []string{"a", "b"}, Prefix: false, Count: 1},
				{Key: []string{"a", "c"}, Prefix: false, Count: 1},
				{Key: []string{"a", "i"}, Prefix: false, Count: 1},
				{Key: []string{"a", "i"}, Prefix: true, Count: 1},
			},
		}, {
			[]string{"a", "b"},
			[]charmstore.Counter{
				{Key: []string{"a", "b", "c"}, Prefix: false, Count: 2},
				{Key: []string{"a", "b", "d"}, Prefix: false, Count: 1},
				{Key: []string{"a", "b", "e"}, Prefix: false, Count: 1},
			},
		}, {
			[]string{"z"},
			[]charmstore.Counter(nil),
		},
	}

	// Use a different store to exercise cache filling.
	pool, err := charmstore.NewPool(s.store.DB.Database, nil, nil, charmstore.ServerParams{})
	c.Assert(err, gc.IsNil)
	st := pool.Store()
	defer st.Close()
	pool.Close()

	for i := range tests {
		req := &charmstore.CounterRequest{Key: tests[i].prefix, Prefix: true, List: true}
		result, err := st.Counters(req)
		c.Assert(err, gc.IsNil)
		c.Assert(result, gc.DeepEquals, tests[i].result)
	}
}

func (s *StatsSuite) TestListCountersBy(c *gc.C) {
	if !storetesting.MongoJSEnabled() {
		c.Skip("MongoDB JavaScript not available")
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
		result  []charmstore.Counter
	}{
		{
			charmstore.CounterRequest{
				Key:    []string{"a"},
				Prefix: false,
				List:   false,
				By:     charmstore.ByDay,
			},
			[]charmstore.Counter{
				{Key: []string{"a"}, Prefix: false, Count: 2, Time: day(1)},
				{Key: []string{"a"}, Prefix: false, Count: 1, Time: day(3)},
			},
		}, {
			charmstore.CounterRequest{
				Key:    []string{"a"},
				Prefix: true,
				List:   false,
				By:     charmstore.ByDay,
			},
			[]charmstore.Counter{
				{Key: []string{"a"}, Prefix: true, Count: 2, Time: day(1)},
				{Key: []string{"a"}, Prefix: true, Count: 1, Time: day(3)},
				{Key: []string{"a"}, Prefix: true, Count: 3, Time: day(9)},
			},
		}, {
			charmstore.CounterRequest{
				Key:    []string{"a"},
				Prefix: true,
				List:   false,
				By:     charmstore.ByDay,
				Start:  day(2),
			},
			[]charmstore.Counter{
				{Key: []string{"a"}, Prefix: true, Count: 1, Time: day(3)},
				{Key: []string{"a"}, Prefix: true, Count: 3, Time: day(9)},
			},
		}, {
			charmstore.CounterRequest{
				Key:    []string{"a"},
				Prefix: true,
				List:   false,
				By:     charmstore.ByDay,
				Stop:   day(4),
			},
			[]charmstore.Counter{
				{Key: []string{"a"}, Prefix: true, Count: 2, Time: day(1)},
				{Key: []string{"a"}, Prefix: true, Count: 1, Time: day(3)},
			},
		}, {
			charmstore.CounterRequest{
				Key:    []string{"a"},
				Prefix: true,
				List:   false,
				By:     charmstore.ByDay,
				Start:  day(3),
				Stop:   day(8),
			},
			[]charmstore.Counter{
				{Key: []string{"a"}, Prefix: true, Count: 1, Time: day(3)},
			},
		}, {
			charmstore.CounterRequest{
				Key:    []string{"a"},
				Prefix: true,
				List:   true,
				By:     charmstore.ByDay,
			},
			[]charmstore.Counter{
				{Key: []string{"a", "b"}, Prefix: false, Count: 1, Time: day(1)},
				{Key: []string{"a", "c"}, Prefix: false, Count: 1, Time: day(1)},
				{Key: []string{"a", "b"}, Prefix: false, Count: 1, Time: day(3)},
				{Key: []string{"a", "c"}, Prefix: true, Count: 3, Time: day(9)},
			},
		}, {
			charmstore.CounterRequest{
				Key:    []string{"a"},
				Prefix: true,
				List:   false,
				By:     charmstore.ByWeek,
			},
			[]charmstore.Counter{
				{Key: []string{"a"}, Prefix: true, Count: 3, Time: day(6)},
				{Key: []string{"a"}, Prefix: true, Count: 3, Time: day(13)},
			},
		}, {
			charmstore.CounterRequest{
				Key:    []string{"a"},
				Prefix: true,
				List:   true,
				By:     charmstore.ByWeek,
			},
			[]charmstore.Counter{
				{Key: []string{"a", "b"}, Prefix: false, Count: 2, Time: day(6)},
				{Key: []string{"a", "c"}, Prefix: false, Count: 1, Time: day(6)},
				{Key: []string{"a", "c"}, Prefix: true, Count: 3, Time: day(13)},
			},
		},
	}

	for _, test := range tests {
		result, err := s.store.Counters(&test.request)
		c.Assert(err, gc.IsNil)
		c.Assert(result, gc.DeepEquals, test.result)
	}
}

type testStatsEntity struct {
	id          *router.ResolvedURL
	lastDay     int
	lastWeek    int
	lastMonth   int
	total       int
	legacyTotal int
}

var archiveDownloadCountsTests = []struct {
	about              string
	charms             []testStatsEntity
	id                 *charm.Reference
	expectThisRevision charmstore.AggregatedCounts
	expectAllRevisions charmstore.AggregatedCounts
}{{
	about: "single revision",
	charms: []testStatsEntity{{
		id:          charmstore.MustParseResolvedURL("~charmers/trusty/wordpress-0"),
		lastDay:     1,
		lastWeek:    2,
		lastMonth:   3,
		total:       4,
		legacyTotal: 0,
	}},
	id: charm.MustParseReference("~charmers/trusty/wordpress-0"),
	expectThisRevision: charmstore.AggregatedCounts{
		LastDay:   1,
		LastWeek:  3,
		LastMonth: 6,
		Total:     10,
	},
	expectAllRevisions: charmstore.AggregatedCounts{
		LastDay:   1,
		LastWeek:  3,
		LastMonth: 6,
		Total:     10,
	},
}, {
	about: "single revision with legacy count",
	charms: []testStatsEntity{{
		id:          charmstore.MustParseResolvedURL("~charmers/trusty/wordpress-0"),
		lastDay:     1,
		lastWeek:    2,
		lastMonth:   3,
		total:       4,
		legacyTotal: 10,
	}},
	id: charm.MustParseReference("~charmers/trusty/wordpress-0"),
	expectThisRevision: charmstore.AggregatedCounts{
		LastDay:   1,
		LastWeek:  3,
		LastMonth: 6,
		Total:     20,
	},
	expectAllRevisions: charmstore.AggregatedCounts{
		LastDay:   1,
		LastWeek:  3,
		LastMonth: 6,
		Total:     20,
	},
}, {
	about: "multiple revisions",
	charms: []testStatsEntity{{
		id:          charmstore.MustParseResolvedURL("~charmers/trusty/wordpress-0"),
		lastDay:     1,
		lastWeek:    2,
		lastMonth:   3,
		total:       4,
		legacyTotal: 0,
	}, {
		id:          charmstore.MustParseResolvedURL("~charmers/trusty/wordpress-1"),
		lastDay:     2,
		lastWeek:    3,
		lastMonth:   4,
		total:       5,
		legacyTotal: 0,
	}},
	id: charm.MustParseReference("~charmers/trusty/wordpress-1"),
	expectThisRevision: charmstore.AggregatedCounts{
		LastDay:   2,
		LastWeek:  5,
		LastMonth: 9,
		Total:     14,
	},
	expectAllRevisions: charmstore.AggregatedCounts{
		LastDay:   3,
		LastWeek:  8,
		LastMonth: 15,
		Total:     24,
	},
}, {
	about: "multiple revisions with legacy count",
	charms: []testStatsEntity{{
		id:          charmstore.MustParseResolvedURL("~charmers/trusty/wordpress-0"),
		lastDay:     1,
		lastWeek:    2,
		lastMonth:   3,
		total:       4,
		legacyTotal: 100,
	}, {
		id:          charmstore.MustParseResolvedURL("~charmers/trusty/wordpress-1"),
		lastDay:     2,
		lastWeek:    3,
		lastMonth:   4,
		total:       5,
		legacyTotal: 100,
	}},
	id: charm.MustParseReference("~charmers/trusty/wordpress-1"),
	expectThisRevision: charmstore.AggregatedCounts{
		LastDay:   2,
		LastWeek:  5,
		LastMonth: 9,
		Total:     114,
	},
	expectAllRevisions: charmstore.AggregatedCounts{
		LastDay:   3,
		LastWeek:  8,
		LastMonth: 15,
		Total:     124,
	},
}, {
	about: "promulgated revision",
	charms: []testStatsEntity{{
		id:          charmstore.MustParseResolvedURL("0 ~charmers/trusty/wordpress-0"),
		lastDay:     1,
		lastWeek:    2,
		lastMonth:   3,
		total:       4,
		legacyTotal: 0,
	}},
	id: charm.MustParseReference("trusty/wordpress-0"),
	expectThisRevision: charmstore.AggregatedCounts{
		LastDay:   1,
		LastWeek:  3,
		LastMonth: 6,
		Total:     10,
	},
	expectAllRevisions: charmstore.AggregatedCounts{
		LastDay:   1,
		LastWeek:  3,
		LastMonth: 6,
		Total:     10,
	},
}, {
	about: "promulgated revision with legacy count",
	charms: []testStatsEntity{{
		id:          charmstore.MustParseResolvedURL("0 ~charmers/trusty/wordpress-0"),
		lastDay:     1,
		lastWeek:    2,
		lastMonth:   3,
		total:       4,
		legacyTotal: 10,
	}},
	id: charm.MustParseReference("trusty/wordpress-0"),
	expectThisRevision: charmstore.AggregatedCounts{
		LastDay:   1,
		LastWeek:  3,
		LastMonth: 6,
		Total:     20,
	},
	expectAllRevisions: charmstore.AggregatedCounts{
		LastDay:   1,
		LastWeek:  3,
		LastMonth: 6,
		Total:     20,
	},
}, {
	about: "promulgated revision with changed owner",
	charms: []testStatsEntity{{
		id:          charmstore.MustParseResolvedURL("0 ~charmers/trusty/wordpress-0"),
		lastDay:     1,
		lastWeek:    10,
		lastMonth:   100,
		total:       1000,
		legacyTotal: 0,
	}, {
		id:          charmstore.MustParseResolvedURL("~charmers/trusty/wordpress-1"),
		lastDay:     2,
		lastWeek:    20,
		lastMonth:   200,
		total:       2000,
		legacyTotal: 0,
	}, {
		id:          charmstore.MustParseResolvedURL("~wordpress-charmers/trusty/wordpress-0"),
		lastDay:     3,
		lastWeek:    30,
		lastMonth:   300,
		total:       3000,
		legacyTotal: 0,
	}, {
		id:          charmstore.MustParseResolvedURL("1 ~wordpress-charmers/trusty/wordpress-1"),
		lastDay:     4,
		lastWeek:    40,
		lastMonth:   400,
		total:       4000,
		legacyTotal: 0,
	}},
	id: charm.MustParseReference("trusty/wordpress-1"),
	expectThisRevision: charmstore.AggregatedCounts{
		LastDay:   4,
		LastWeek:  44,
		LastMonth: 444,
		Total:     4444,
	},
	expectAllRevisions: charmstore.AggregatedCounts{
		LastDay:   5,
		LastWeek:  55,
		LastMonth: 555,
		Total:     5555,
	},
}}

func (s *StatsSuite) TestArchiveDownloadCounts(c *gc.C) {
	s.PatchValue(&charmstore.LegacyDownloadCountsEnabled, true)
	for i, test := range archiveDownloadCountsTests {
		c.Logf("%d: %s", i, test.about)
		// Clear everything
		charmstore.StatsCacheEvictAll(s.store)
		s.store.DB.Entities().RemoveAll(nil)
		s.store.DB.StatCounters().RemoveAll(nil)
		for _, charm := range test.charms {
			ch := storetesting.Charms.CharmDir(charm.id.URL.Name)
			err := s.store.AddCharmWithArchive(charm.id, ch)
			c.Assert(err, gc.IsNil)
			url := charm.id.URL
			now := time.Now()
			setDownloadCounts(c, s.store, &url, now, charm.lastDay)
			setDownloadCounts(c, s.store, &url, now.Add(-2*24*time.Hour), charm.lastWeek)
			setDownloadCounts(c, s.store, &url, now.Add(-10*24*time.Hour), charm.lastMonth)
			setDownloadCounts(c, s.store, &url, now.Add(-100*24*time.Hour), charm.total)
			if charm.id.PromulgatedRevision > -1 {
				url.Revision = charm.id.PromulgatedRevision
				url.User = ""
				setDownloadCounts(c, s.store, &url, now, charm.lastDay)
				setDownloadCounts(c, s.store, &url, now.Add(-2*24*time.Hour), charm.lastWeek)
				setDownloadCounts(c, s.store, &url, now.Add(-10*24*time.Hour), charm.lastMonth)
				setDownloadCounts(c, s.store, &url, now.Add(-100*24*time.Hour), charm.total)
			}
			extraInfo := map[string][]byte{
				params.LegacyDownloadStats: []byte(fmt.Sprintf("%d", charm.legacyTotal)),
			}
			err = s.store.UpdateEntity(charm.id, bson.D{{
				"$set", bson.D{{"extrainfo", extraInfo}},
			}})
			c.Assert(err, gc.IsNil)
		}
		thisRevision, allRevisions, err := s.store.ArchiveDownloadCounts(test.id, false)
		c.Assert(err, gc.IsNil)
		c.Assert(thisRevision, jc.DeepEquals, test.expectThisRevision)
		c.Assert(allRevisions, jc.DeepEquals, test.expectAllRevisions)
	}
}

func setDownloadCounts(c *gc.C, s *charmstore.Store, id *charm.Reference, t time.Time, n int) {
	key := charmstore.EntityStatsKey(id, params.StatsArchiveDownload)
	for i := 0; i < n; i++ {
		err := s.IncCounterAtTime(key, t)
		c.Assert(err, gc.IsNil)
	}
}

func (s *StatsSuite) TestIncrementDownloadCounts(c *gc.C) {
	ch := storetesting.Charms.CharmDir("wordpress")
	id := charmstore.MustParseResolvedURL("0 ~charmers/trusty/wordpress-1")
	err := s.store.AddCharmWithArchive(id, ch)
	c.Assert(err, gc.IsNil)
	err = s.store.IncrementDownloadCounts(id)
	c.Assert(err, gc.IsNil)
	expect := charmstore.AggregatedCounts{
		LastDay:   1,
		LastWeek:  1,
		LastMonth: 1,
		Total:     1,
	}
	thisRevision, allRevisions, err := s.store.ArchiveDownloadCounts(charm.MustParseReference("~charmers/trusty/wordpress-1"), false)
	c.Assert(err, gc.IsNil)
	c.Assert(thisRevision, jc.DeepEquals, expect)
	c.Assert(allRevisions, jc.DeepEquals, expect)
	thisRevision, allRevisions, err = s.store.ArchiveDownloadCounts(charm.MustParseReference("trusty/wordpress-0"), false)
	c.Assert(err, gc.IsNil)
	c.Assert(thisRevision, jc.DeepEquals, expect)
	c.Assert(allRevisions, jc.DeepEquals, expect)
}

func (s *StatsSuite) TestIncrementDownloadCountsWithNoCache(c *gc.C) {
	ch := storetesting.Charms.CharmDir("wordpress")
	id := charmstore.MustParseResolvedURL("0 ~charmers/trusty/wordpress-1")
	err := s.store.AddCharmWithArchive(id, ch)
	c.Assert(err, gc.IsNil)
	err = s.store.IncrementDownloadCounts(id)
	c.Assert(err, gc.IsNil)
	expect := charmstore.AggregatedCounts{
		LastDay:   1,
		LastWeek:  1,
		LastMonth: 1,
		Total:     1,
	}
	expectAfter := charmstore.AggregatedCounts{
		LastDay:   2,
		LastWeek:  2,
		LastMonth: 2,
		Total:     2,
	}
	thisRevision, allRevisions, err := s.store.ArchiveDownloadCounts(charm.MustParseReference("~charmers/trusty/wordpress-1"), false)
	c.Assert(err, gc.IsNil)
	c.Assert(thisRevision, jc.DeepEquals, expect)
	c.Assert(allRevisions, jc.DeepEquals, expect)
	err = s.store.IncrementDownloadCounts(id)
	thisRevision, allRevisions, err = s.store.ArchiveDownloadCounts(charm.MustParseReference("~charmers/trusty/wordpress-1"), false)
	c.Assert(err, gc.IsNil)
	c.Assert(thisRevision, jc.DeepEquals, expect)
	c.Assert(allRevisions, jc.DeepEquals, expect)
	thisRevision, allRevisions, err = s.store.ArchiveDownloadCounts(charm.MustParseReference("~charmers/trusty/wordpress-1"), true)
	c.Assert(err, gc.IsNil)
	c.Assert(thisRevision, jc.DeepEquals, expectAfter)
	c.Assert(allRevisions, jc.DeepEquals, expectAfter)
}
