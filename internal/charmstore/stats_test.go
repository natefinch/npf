// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore_test

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
	"github.com/juju/charmstore/internal/storetesting"
	"gopkg.in/mgo.v2/bson"
	jujutesting "github.com/juju/testing"
	gc "launchpad.net/gocheck"
	"github.com/juju/charmstore/internal/charmstore"
)

type StatsSuite struct {
	storetesting.IsolatedMgoSuite
	store *charmstore.Store
}

var noTestMongoJs *bool = flag.Bool("notest-mongojs", false, "Disable MongoDB tests that require javascript")

var _ = gc.Suite(&StatsSuite{})

func (s *StatsSuite) SetUpSuite(c *gc.C) {
	s.IsolatedMgoSuite.SetUpSuite(c)
	if os.Getenv("JUJU_NOTEST_MONGOJS") == "1" || jujutesting.MgoServer.WithoutV8 {
		c.Log("Tests requiring MongoDB Javascript will be skipped")
		*noTestMongoJs = true
	}
}

func (s *StatsSuite) TearDownSuite(c *gc.C) {
	s.IsolatedMgoSuite.TearDownSuite(c)
}

func (s *StatsSuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	store, err := charmstore.NewStore(s.Session.DB("foo"))
	c.Assert(err, gc.IsNil)
	s.store = store
}

func (s *StatsSuite) TearDownTest(c *gc.C) {
	s.IsolatedMgoSuite.TearDownTest(c)
}

func (s *StatsSuite) TestSumCounters(c *gc.C) {
	if *noTestMongoJs {
		c.Skip("MongoDB javascript not available")
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
	if *noTestMongoJs {
		c.Skip("MongoDB javascript not available")
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
	if *noTestMongoJs {
		c.Skip("MongoDB javascript not available")
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
	if *noTestMongoJs {
		c.Skip("MongoDB javascript not available")
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
	if *noTestMongoJs {
		c.Skip("MongoDB javascript not available")
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
	st, err := charmstore.NewStore(s.store.DB.Database)
	c.Assert(err, gc.IsNil)

	for i := range tests {
		req := &charmstore.CounterRequest{Key: tests[i].prefix, Prefix: true, List: true}
		result, err := st.Counters(req)
		c.Assert(err, gc.IsNil)
		c.Assert(result, gc.DeepEquals, tests[i].result)
	}
}

func (s *StatsSuite) TestListCountersBy(c *gc.C) {
	if *noTestMongoJs {
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

	counters := s.store.DB.StatCounters()
	for i, inc := range incs {
		err := s.store.IncCounter(inc.key)
		c.Assert(err, gc.IsNil)

		// Hack time so counters are assigned to 2012-05-<day>
		filter := bson.M{"t": bson.M{"$gt": charmstore.TimeToStamp(time.Date(2013, time.January, 1, 0, 0, 0, 0, time.UTC))}}
		stamp := charmstore.TimeToStamp(day(inc.day))
		stamp += int32(i) * 60 // Make every entry unique.
		err = counters.Update(filter, bson.D{{"$set", bson.D{{"t", stamp}}}})
		c.Check(err, gc.IsNil)
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
