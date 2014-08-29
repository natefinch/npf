// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/juju/errgo"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/params"
)

// Note that changing the StatsGranularity constant
// will not change the stats time granularity - it
// is defined for external code clarity.

// StatsGranularity holds the time granularity of statistics
// gathering. IncCounter calls within this duration
// may be aggregated.
const StatsGranularity = time.Minute

// The stats mechanism uses the following MongoDB collections:
//
//     juju.stat.counters - Counters for statistics
//     juju.stat.tokens   - Tokens used in statistics counter keys

func (s StoreDatabase) StatCounters() *mgo.Collection {
	return s.C("juju.stat.counters")
}

func (s StoreDatabase) StatTokens() *mgo.Collection {
	return s.C("juju.stat.tokens")
}

// statsKey returns the compound statistics identifier that represents key.
// If write is true, the identifier will be created if necessary.
// Identifiers have a form similar to "ab:c:def:", where each section is a
// base-32 number that represents the respective word in key. This form
// allows efficiently indexing and searching for prefixes, while detaching
// the key content and size from the actual words used in key.
func (s *Store) statsKey(db StoreDatabase, key []string, write bool) (string, error) {
	if len(key) == 0 {
		return "", errgo.New("store: empty statistics key")
	}
	tokens := db.StatTokens()
	skey := make([]byte, 0, len(key)*4)
	// Retry limit is mainly to prevent infinite recursion in edge cases,
	// such as if the database is ever run in read-only mode.
	// The logic below should deteministically stop in normal scenarios.
	var err error
	for i, retry := 0, 30; i < len(key) && retry > 0; retry-- {
		err = nil
		id, found := s.statsTokenId(key[i])
		if !found {
			var t tokenId
			err = tokens.Find(bson.D{{"t", key[i]}}).One(&t)
			if err == mgo.ErrNotFound {
				if !write {
					return "", errgo.WithCausef(nil, params.ErrNotFound, "")
				}
				t.Id, err = tokens.Count()
				if err != nil {
					continue
				}
				t.Id++
				t.Token = key[i]
				err = tokens.Insert(&t)
			}
			if err != nil {
				continue
			}
			s.cacheStatsTokenId(t.Token, t.Id)
			id = t.Id
		}
		skey = strconv.AppendInt(skey, int64(id), 32)
		skey = append(skey, ':')
		i++
	}
	if err != nil {
		return "", err
	}
	return string(skey), nil
}

const statsTokenCacheSize = 1024

type tokenId struct {
	Id    int    `bson:"_id"`
	Token string `bson:"t"`
}

// cacheStatsTokenId adds the id for token into the cache.
// The cache has two generations so that the least frequently used
// tokens are evicted regularly.
func (s *Store) cacheStatsTokenId(token string, id int) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	// Can't possibly be >, but reviews want it for defensiveness.
	if len(s.statsIdNew) >= statsTokenCacheSize {
		s.statsIdOld = s.statsIdNew
		s.statsIdNew = nil
		s.statsTokenOld = s.statsTokenNew
		s.statsTokenNew = nil
	}
	if s.statsIdNew == nil {
		s.statsIdNew = make(map[string]int, statsTokenCacheSize)
		s.statsTokenNew = make(map[int]string, statsTokenCacheSize)
	}
	s.statsIdNew[token] = id
	s.statsTokenNew[id] = token
}

// statsTokenId returns the id for token from the cache, if found.
func (s *Store) statsTokenId(token string) (id int, found bool) {
	s.cacheMu.RLock()
	id, found = s.statsIdNew[token]
	if found {
		s.cacheMu.RUnlock()
		return
	}
	id, found = s.statsIdOld[token]
	s.cacheMu.RUnlock()
	if found {
		s.cacheStatsTokenId(token, id)
	}
	return
}

// statsIdToken returns the token for id from the cache, if found.
func (s *Store) statsIdToken(id int) (token string, found bool) {
	s.cacheMu.RLock()
	token, found = s.statsTokenNew[id]
	if found {
		s.cacheMu.RUnlock()
		return
	}
	token, found = s.statsTokenOld[id]
	s.cacheMu.RUnlock()
	if found {
		s.cacheStatsTokenId(token, id)
	}
	return
}

var counterEpoch = time.Date(2012, 1, 1, 0, 0, 0, 0, time.UTC).Unix()

func timeToStamp(t time.Time) int32 {
	return int32(t.Unix() - counterEpoch)
}

// IncCounter increases by one the counter associated with the composed key.
func (s *Store) IncCounter(key []string) error {
	return s.IncCounterAtTime(key, time.Now())
}

// IncCounter increases by one the counter associated with the composed key,
// associating it with the given time, which should be time.Now.
// This method is exposed for testing purposes only - production
// code should always call IncCounter.
func (s *Store) IncCounterAtTime(key []string, t time.Time) error {
	db := s.DB.Copy()
	defer db.Close()

	skey, err := s.statsKey(db, key, true)
	if err != nil {
		return err
	}

	// Round to the start of the minute so we get one document per minute at most.
	t = t.UTC().Add(-time.Duration(t.Second()) * time.Second)
	counters := db.StatCounters()
	_, err = counters.Upsert(bson.D{{"k", skey}, {"t", timeToStamp(t)}}, bson.D{{"$inc", bson.D{{"c", 1}}}})
	return err
}

// CounterRequest represents a request to aggregate counter values.
type CounterRequest struct {
	// Key and Prefix determine the counter keys to match.
	// If Prefix is false, Key must match exactly. Otherwise, counters
	// must begin with Key and have at least one more key token.
	Key    []string
	Prefix bool

	// If List is true, matching counters are aggregated under their
	// prefixes instead of being returned as a single overall sum.
	//
	// For example, given the following counts:
	//
	//   {"a", "b"}: 1,
	//   {"a", "c"}: 3
	//   {"a", "c", "d"}: 5
	//   {"a", "c", "e"}: 7
	//
	// and assuming that Prefix is true, the following keys will
	// present the respective results if List is true:
	//
	//        {"a"} => {{"a", "b"}, 1, false},
	//                 {{"a", "c"}, 3, false},
	//                 {{"a", "c"}, 12, true}
	//   {"a", "c"} => {{"a", "c", "d"}, 3, false},
	//                 {{"a", "c", "e"}, 5, false}
	//
	// If List is false, the same key prefixes will present:
	//
	//        {"a"} => {{"a"}, 16, true}
	//   {"a", "c"} => {{"a", "c"}, 12, false}
	//
	List bool

	// By defines the period covered by each aggregated data point.
	// If unspecified, it defaults to ByAll, which aggregates all
	// matching data points in a single entry.
	By CounterRequestBy

	// Start, if provided, changes the query so that only data points
	// ocurring at the given time or afterwards are considered.
	Start time.Time

	// Stop, if provided, changes the query so that only data points
	// ocurring at the given time or before are considered.
	Stop time.Time
}

type CounterRequestBy int

const (
	ByAll CounterRequestBy = iota
	ByDay
	ByWeek
)

type Counter struct {
	Key    []string
	Prefix bool
	Count  int64
	Time   time.Time
}

// Counters aggregates and returns counter values according to the provided request.
func (s *Store) Counters(req *CounterRequest) ([]Counter, error) {
	db := s.DB.Copy()
	defer db.Close()

	tokensColl := db.StatTokens()
	countersColl := db.StatCounters()

	searchKey, err := s.statsKey(db, req.Key, false)
	if errgo.Cause(err) == params.ErrNotFound {
		if !req.List {
			return []Counter{{
				Key:    req.Key,
				Prefix: req.Prefix,
				Count:  0,
			}}, nil
		}
		return nil, nil
	}
	if err != nil {
		return nil, errgo.Mask(err)
	}
	var regex string
	if req.Prefix {
		regex = "^" + searchKey + ".+"
	} else {
		regex = "^" + searchKey + "$"
	}

	// This reduce function simply sums, for each emitted key, all the values found under it.
	job := mgo.MapReduce{Reduce: "function(key, values) { return Array.sum(values); }"}
	var emit string
	switch req.By {
	case ByDay:
		emit = "emit(k+'@'+NumberInt(this.t/86400), this.c);"
	case ByWeek:
		emit = "emit(k+'@'+NumberInt(this.t/604800), this.c);"
	default:
		emit = "emit(k, this.c);"
	}
	if req.List && req.Prefix {
		// For a search key "a:b:" matching a key "a:b:c:d:e:", this map function emits "a:b:c:*".
		// For a search key "a:b:" matching a key "a:b:c:", it emits "a:b:c:".
		// For a search key "a:b:" matching a key "a:b:", it emits "a:b:".
		job.Scope = bson.D{{"searchKeyLen", len(searchKey)}}
		job.Map = fmt.Sprintf(`
			function() {
				var k = this.k;
				var i = k.indexOf(':', searchKeyLen)+1;
				if (k.length > i)  { k = k.substr(0, i)+'*'; }
				%s
			}`, emit)
	} else {
		// For a search key "a:b:" matching a key "a:b:c:d:e:", this map function emits "a:b:*".
		// For a search key "a:b:" matching a key "a:b:c:", it also emits "a:b:*".
		// For a search key "a:b:" matching a key "a:b:", it emits "a:b:".
		emitKey := searchKey
		if req.Prefix {
			emitKey += "*"
		}
		job.Scope = bson.D{{"emitKey", emitKey}}
		job.Map = fmt.Sprintf(`
			function() {
				var k = emitKey;
				%s
			}`, emit)
	}

	var result []struct {
		Key   string `bson:"_id"`
		Value int64
	}
	var query, tquery bson.D
	if !req.Start.IsZero() {
		tquery = append(tquery, bson.DocElem{
			Name:  "$gte",
			Value: timeToStamp(req.Start),
		})
	}
	if !req.Stop.IsZero() {
		tquery = append(tquery, bson.DocElem{
			Name:  "$lte",
			Value: timeToStamp(req.Stop),
		})
	}
	if len(tquery) == 0 {
		query = bson.D{{"k", bson.D{{"$regex", regex}}}}
	} else {
		query = bson.D{{"k", bson.D{{"$regex", regex}}}, {"t", tquery}}
	}
	_, err = countersColl.Find(query).MapReduce(&job, &result)
	if err != nil {
		return nil, err
	}
	var counters []Counter
	for i := range result {
		key := result[i].Key
		when := time.Time{}
		if req.By != ByAll {
			var stamp int64
			if at := strings.Index(key, "@"); at != -1 && len(key) > at+1 {
				stamp, _ = strconv.ParseInt(key[at+1:], 10, 32)
				key = key[:at]
			}
			if stamp == 0 {
				return nil, errgo.Newf("internal error: bad aggregated key: %q", result[i].Key)
			}
			switch req.By {
			case ByDay:
				stamp = stamp * 86400
			case ByWeek:
				// The +1 puts it at the end of the period.
				stamp = (stamp + 1) * 604800
			}
			when = time.Unix(counterEpoch+stamp, 0).In(time.UTC)
		}
		ids := strings.Split(key, ":")
		tokens := make([]string, 0, len(ids))
		for i := 0; i < len(ids)-1; i++ {
			if ids[i] == "*" {
				continue
			}
			id, err := strconv.ParseInt(ids[i], 32, 32)
			if err != nil {
				return nil, errgo.Newf("store: invalid id: %q", ids[i])
			}
			token, found := s.statsIdToken(int(id))
			if !found {
				var t tokenId
				err = tokensColl.FindId(id).One(&t)
				if err == mgo.ErrNotFound {
					return nil, errgo.Newf("store: internal error; token id not found: %d", id)
				}
				s.cacheStatsTokenId(t.Token, t.Id)
				token = t.Token
			}
			tokens = append(tokens, token)
		}
		counter := Counter{
			Key:    tokens,
			Prefix: len(ids) > 0 && ids[len(ids)-1] == "*",
			Count:  result[i].Value,
			Time:   when,
		}
		counters = append(counters, counter)
	}
	if !req.List && len(counters) == 0 {
		counters = []Counter{{Key: req.Key, Prefix: req.Prefix, Count: 0}}
	} else if len(counters) > 1 {
		sort.Sort(sortableCounters(counters))
	}
	return counters, nil
}

type sortableCounters []Counter

func (s sortableCounters) Len() int      { return len(s) }
func (s sortableCounters) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s sortableCounters) Less(i, j int) bool {
	// Earlier times first.
	if !s[i].Time.Equal(s[j].Time) {
		return s[i].Time.Before(s[j].Time)
	}
	// Then larger counts first.
	if s[i].Count != s[j].Count {
		return s[j].Count < s[i].Count
	}
	// Then smaller/shorter keys first.
	ki := s[i].Key
	kj := s[j].Key
	for n := range ki {
		if n >= len(kj) {
			return false
		}
		if ki[n] != kj[n] {
			return ki[n] < kj[n]
		}
	}
	if len(ki) < len(kj) {
		return true
	}
	// Then full keys first.
	return !s[i].Prefix && s[j].Prefix
}
