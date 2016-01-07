package entitycache_test

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"

	"gopkg.in/juju/charmstore.v5-unstable/internal/entitycache"
	"gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"
)

var _ = gc.Suite(&suite{})

type suite struct{}

type entityQuery struct {
	url    *charm.URL
	fields map[string]int
	reply  chan entityReply
}

type entityReply struct {
	entity *mongodoc.Entity
	err    error
}

type baseEntityQuery struct {
	url    *charm.URL
	fields map[string]int
	reply  chan baseEntityReply
}

type baseEntityReply struct {
	entity *mongodoc.BaseEntity
	err    error
}

func (*suite) TestEntityIssuesBaseEntityQueryConcurrently(c *gc.C) {
	store := newChanStore()
	cache := entitycache.New(store)
	defer cache.Close()
	cache.AddBaseEntityFields(map[string]int{"url": 1, "name": 1})

	entity := &mongodoc.Entity{
		URL:      charm.MustParseURL("~bob/wordpress-1"),
		BaseURL:  charm.MustParseURL("~bob/wordpress"),
		BlobName: "w1",
		Size:     99,
	}
	baseEntity := &mongodoc.BaseEntity{
		URL:  charm.MustParseURL("~bob/wordpress"),
		Name: "wordpress",
	}
	queryDone := make(chan struct{})
	go func() {
		defer close(queryDone)
		e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-1"), map[string]int{"url": 1, "baseurl": 1, "blobname": 1})
		c.Check(err, gc.IsNil)
		c.Check(e, gc.Equals, entity)
	}()

	// Acquire both the queries before replying so that we know they've been
	// issued concurrently.
	query1 := <-store.entityqc
	c.Assert(query1.url, jc.DeepEquals, charm.MustParseURL("~bob/wordpress-1"))
	c.Assert(query1.fields, jc.DeepEquals, map[string]int{
		"url":      1,
		"baseurl":  1,
		"blobname": 1,
	})
	query2 := <-store.baseEntityqc
	c.Assert(query2.url, jc.DeepEquals, charm.MustParseURL("~bob/wordpress"))
	c.Assert(query2.fields, jc.DeepEquals, map[string]int{
		"url":  1,
		"name": 1,
	})
	query1.reply <- entityReply{
		entity: entity,
	}
	query2.reply <- baseEntityReply{
		entity: baseEntity,
	}
	<-queryDone

	// Accessing the same entity again and the base entity should
	// not call any method on the store, so close the query channels
	// to ensure it doesn't.
	close(store.entityqc)
	close(store.baseEntityqc)
	e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-1"), map[string]int{"url": 1, "baseurl": 1, "blobname": 1})
	c.Check(err, gc.IsNil)
	c.Check(e, gc.Equals, entity)

	be, err := cache.BaseEntity(charm.MustParseURL("~bob/wordpress"), map[string]int{"url": 1, "name": 1})
	c.Check(err, gc.IsNil)
	c.Check(be, gc.Equals, baseEntity)
}

func (*suite) TestFetchWhenFieldsChangeBeforeQueryResult(c *gc.C) {
	store := newChanStore()
	cache := entitycache.New(store)
	defer cache.Close()
	cache.AddBaseEntityFields(map[string]int{"url": 1, "name": 1})

	entity := &mongodoc.Entity{
		URL:      charm.MustParseURL("~bob/wordpress-1"),
		BaseURL:  charm.MustParseURL("~bob/wordpress"),
		BlobName: "w1",
	}
	baseEntity := &mongodoc.BaseEntity{
		URL:  charm.MustParseURL("~bob/wordpress"),
		Name: "wordpress",
	}
	store.findBaseEntity = func(url *charm.URL, fields map[string]int) (*mongodoc.BaseEntity, error) {
		c.Check(url, jc.DeepEquals, baseEntity.URL)
		c.Check(fields, jc.DeepEquals, map[string]int{
			"url":  1,
			"name": 1,
		})
		return baseEntity, nil
	}

	queryDone := make(chan struct{})
	go func() {
		defer close(queryDone)
		e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-1"), map[string]int{"url": 1, "baseurl": 1})
		c.Check(err, gc.IsNil)
		c.Check(e, gc.Equals, entity)
	}()

	query1 := <-store.entityqc
	c.Assert(query1.url, jc.DeepEquals, charm.MustParseURL("~bob/wordpress-1"))
	c.Assert(query1.fields, jc.DeepEquals, map[string]int{
		"url":     1,
		"baseurl": 1,
	})
	// Before we send the reply, make another query with different fields,
	// so the version changes.
	entity2 := &mongodoc.Entity{
		URL:      charm.MustParseURL("~bob/wordpress-1"),
		BaseURL:  charm.MustParseURL("~bob/wordpress"),
		BlobName: "w1",
		Size:     99,
	}
	query2Done := make(chan struct{})
	go func() {
		defer close(query2Done)
		// Note the extra "size" field.
		e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-1"), map[string]int{"url": 1, "baseurl": 1, "size": 1})
		c.Check(err, gc.IsNil)
		c.Check(e, gc.Equals, entity2)
	}()
	// The second query should be sent immediately without waiting
	// for the first because it invalidates the cache..
	query2 := <-store.entityqc
	c.Assert(query2.url, jc.DeepEquals, charm.MustParseURL("~bob/wordpress-1"))
	c.Assert(query2.fields, jc.DeepEquals, map[string]int{
		"url":     1,
		"baseurl": 1,
		"size":    1,
	})
	query2.reply <- entityReply{
		entity: entity2,
	}
	<-query2Done

	// Reply to the first query and make sure that it completed.
	query1.reply <- entityReply{
		entity: entity,
	}
	<-queryDone

	// Accessing the same entity again not call any method on the store, so close the query channels
	// to ensure it doesn't.
	close(store.entityqc)
	close(store.baseEntityqc)
	e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-1"), map[string]int{"url": 1, "baseurl": 1})
	c.Check(err, gc.IsNil)
	c.Check(e, gc.Equals, entity2)
}

func (*suite) TestSecondFetchesWaitForFirst(c *gc.C) {
	store := newChanStore()
	cache := entitycache.New(store)
	defer cache.Close()
	cache.AddBaseEntityFields(map[string]int{"url": 1, "name": 1})

	entity := &mongodoc.Entity{
		URL:      charm.MustParseURL("~bob/wordpress-1"),
		BaseURL:  charm.MustParseURL("~bob/wordpress"),
		BlobName: "w1",
	}
	baseEntity := &mongodoc.BaseEntity{
		URL:  charm.MustParseURL("~bob/wordpress"),
		Name: "wordpress",
	}
	store.findBaseEntity = func(url *charm.URL, fields map[string]int) (*mongodoc.BaseEntity, error) {
		c.Check(url, jc.DeepEquals, baseEntity.URL)
		c.Check(fields, jc.DeepEquals, map[string]int{
			"url":  1,
			"name": 1,
		})
		return baseEntity, nil
	}

	var initialRequestGroup sync.WaitGroup
	initialRequestGroup.Add(1)
	go func() {
		defer initialRequestGroup.Done()
		e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-1"), map[string]int{"url": 1, "baseurl": 1})
		c.Check(err, gc.IsNil)
		c.Check(e, gc.Equals, entity)
	}()

	query1 := <-store.entityqc
	c.Assert(query1.url, jc.DeepEquals, charm.MustParseURL("~bob/wordpress-1"))
	c.Assert(query1.fields, jc.DeepEquals, map[string]int{
		"url":     1,
		"baseurl": 1,
	})

	// Send some more queries for the same charm. These should not send a
	// store request but instead wait for the first one.
	for i := 0; i < 5; i++ {
		initialRequestGroup.Add(1)
		go func() {
			defer initialRequestGroup.Done()
			e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-1"), map[string]int{"url": 1, "baseurl": 1})
			c.Check(err, gc.IsNil)
			c.Check(e, gc.Equals, entity)
		}()
	}
	select {
	case q := <-store.entityqc:
		c.Fatalf("unexpected store query %#v", q)
	case <-time.After(10 * time.Millisecond):
	}

	entity2 := &mongodoc.Entity{
		URL:      charm.MustParseURL("~bob/wordpress-2"),
		BaseURL:  charm.MustParseURL("~bob/wordpress"),
		BlobName: "w2",
	}
	// Send another query for a different charm. This will cause the
	// waiting goroutines to be woken up but go back to sleep again
	// because their entry isn't yet available.
	otherRequestDone := make(chan struct{})
	go func() {
		defer close(otherRequestDone)
		e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-2"), map[string]int{"url": 1, "baseurl": 1})
		c.Check(err, gc.IsNil)
		c.Check(e, gc.Equals, entity2)
	}()
	query2 := <-store.entityqc
	c.Assert(query2.url, jc.DeepEquals, charm.MustParseURL("~bob/wordpress-2"))
	c.Assert(query2.fields, jc.DeepEquals, map[string]int{
		"url":     1,
		"baseurl": 1,
	})
	query2.reply <- entityReply{
		entity: entity2,
	}

	// Now reply to the initial store request, which should make
	// everything complete.
	query1.reply <- entityReply{
		entity: entity,
	}
	initialRequestGroup.Wait()
}

func (*suite) TestGetEntityNotFound(c *gc.C) {
	entityFetchCount := 0
	baseEntityFetchCount := 0
	store := &callbackStore{
		findBestEntity: func(url *charm.URL, fields map[string]int) (*mongodoc.Entity, error) {
			entityFetchCount++
			return nil, errgo.NoteMask(params.ErrNotFound, "entity", errgo.Any)
		},
		findBaseEntity: func(url *charm.URL, fields map[string]int) (*mongodoc.BaseEntity, error) {
			baseEntityFetchCount++
			return nil, errgo.NoteMask(params.ErrNotFound, "base entity", errgo.Any)
		},
	}
	cache := entitycache.New(store)
	defer cache.Close()
	e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-1"), nil)
	c.Assert(e, gc.IsNil)
	c.Assert(err, gc.ErrorMatches, "not found")
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrNotFound)

	// Make sure that the not-found result has been cached.
	e, err = cache.Entity(charm.MustParseURL("~bob/wordpress-1"), nil)
	c.Assert(e, gc.IsNil)
	c.Assert(err, gc.ErrorMatches, "not found")
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrNotFound)

	c.Assert(entityFetchCount, gc.Equals, 1)

	// Make sure fetching the base entity works the same way.
	be, err := cache.BaseEntity(charm.MustParseURL("~bob/wordpress"), nil)
	c.Assert(be, gc.IsNil)
	c.Assert(err, gc.ErrorMatches, "not found")
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrNotFound)

	be, err = cache.BaseEntity(charm.MustParseURL("~bob/wordpress"), nil)
	c.Assert(be, gc.IsNil)
	c.Assert(err, gc.ErrorMatches, "not found")
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrNotFound)

	c.Assert(baseEntityFetchCount, gc.Equals, 1)
}

func (*suite) TestFetchError(c *gc.C) {
	entityFetchCount := 0
	baseEntityFetchCount := 0
	store := &callbackStore{
		findBestEntity: func(url *charm.URL, fields map[string]int) (*mongodoc.Entity, error) {
			entityFetchCount++
			return nil, errgo.New("entity error")
		},
		findBaseEntity: func(url *charm.URL, fields map[string]int) (*mongodoc.BaseEntity, error) {
			baseEntityFetchCount++
			return nil, errgo.New("base entity error")
		},
	}
	cache := entitycache.New(store)
	defer cache.Close()

	// Check that we get the entity fetch error from cache.Entity.
	e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-1"), nil)
	c.Assert(e, gc.IsNil)
	c.Assert(err, gc.ErrorMatches, `cannot fetch "cs:~bob/wordpress-1": entity error`)

	// Check that the error is cached.
	e, err = cache.Entity(charm.MustParseURL("~bob/wordpress-1"), nil)
	c.Assert(e, gc.IsNil)
	c.Assert(err, gc.ErrorMatches, `cannot fetch "cs:~bob/wordpress-1": entity error`)

	c.Assert(entityFetchCount, gc.Equals, 1)

	// Check that we get the base-entity fetch error from cache.BaseEntity.
	be, err := cache.BaseEntity(charm.MustParseURL("~bob/wordpress"), nil)
	c.Assert(be, gc.IsNil)
	c.Assert(err, gc.ErrorMatches, `cannot fetch "cs:~bob/wordpress": base entity error`)

	// Check that the error is cached.
	be, err = cache.BaseEntity(charm.MustParseURL("~bob/wordpress"), nil)
	c.Assert(be, gc.IsNil)
	c.Assert(err, gc.ErrorMatches, `cannot fetch "cs:~bob/wordpress": base entity error`)
	c.Assert(baseEntityFetchCount, gc.Equals, 1)
}

func (*suite) TestStartFetch(c *gc.C) {
	store := newChanStore()
	cache := entitycache.New(store)

	url := charm.MustParseURL("~bob/wordpress-1")
	baseURL := charm.MustParseURL("~bob/wordpress")
	cache.StartFetch([]*charm.URL{url})

	entity := &mongodoc.Entity{
		URL:      url,
		BaseURL:  baseURL,
		BlobName: "foo",
	}
	baseEntity := &mongodoc.BaseEntity{
		URL: baseURL,
	}

	// Both queries should be issued concurrently.
	query1 := <-store.entityqc
	c.Assert(query1.url, jc.DeepEquals, charm.MustParseURL("~bob/wordpress-1"))
	query2 := <-store.baseEntityqc
	c.Assert(query2.url, jc.DeepEquals, charm.MustParseURL("~bob/wordpress"))

	entityQueryDone := make(chan struct{})
	go func() {
		defer close(entityQueryDone)
		e, err := cache.Entity(url, nil)
		c.Check(err, gc.IsNil)
		c.Check(e, jc.DeepEquals, entity)
	}()
	baseEntityQueryDone := make(chan struct{})
	go func() {
		defer close(baseEntityQueryDone)
		e, err := cache.BaseEntity(baseURL, nil)
		c.Check(err, gc.IsNil)
		c.Check(e, jc.DeepEquals, baseEntity)
	}()

	// Reply to the entity query.
	// This should cause the extra entity query to complete.
	query1.reply <- entityReply{
		entity: entity,
	}
	<-entityQueryDone

	// Reply to the base entity query.
	// This should cause the extra base entity query to complete.
	query2.reply <- baseEntityReply{
		entity: &mongodoc.BaseEntity{
			URL: baseURL,
		},
	}
	<-baseEntityQueryDone
}

func (*suite) TestAddEntityFields(c *gc.C) {
	store := newChanStore()
	baseEntity := &mongodoc.BaseEntity{
		URL: charm.MustParseURL("cs:~bob/wordpress"),
	}
	entity := &mongodoc.Entity{
		URL:      charm.MustParseURL("cs:~bob/wordpress-1"),
		BlobName: "foo",
		Size:     999,
		BlobHash: "ffff",
	}
	baseEntityCount := 0
	store.findBaseEntity = func(url *charm.URL, fields map[string]int) (*mongodoc.BaseEntity, error) {
		baseEntityCount++
		if url.String() != "cs:~bob/wordpress" {
			return nil, params.ErrNotFound
		}
		return baseEntity, nil
	}
	cache := entitycache.New(store)
	cache.AddEntityFields(map[string]int{"blobname": 1, "size": 1})
	queryDone := make(chan struct{})
	go func() {
		defer close(queryDone)
		e, err := cache.Entity(charm.MustParseURL("cs:~bob/wordpress-1"), map[string]int{"blobname": 1})
		c.Check(err, gc.IsNil)
		expect := *entity
		selectFields(&expect, "url", "blobname", "size")
		c.Check(e, jc.DeepEquals, &expect)

		// Adding existing entity fields should have no effect.
		cache.AddEntityFields(map[string]int{"blobname": 1, "size": 1})

		e, err = cache.Entity(charm.MustParseURL("cs:~bob/wordpress-1"), map[string]int{"size": 1})
		c.Check(err, gc.IsNil)
		expect = *entity
		selectFields(&expect, "url", "blobname", "size")
		c.Check(e, jc.DeepEquals, &expect)

		// Adding a new field should will cause the cache to be invalidated
		// and a new fetch to take place.

		cache.AddEntityFields(map[string]int{"blobhash": 1})
		e, err = cache.Entity(charm.MustParseURL("cs:~bob/wordpress-1"), nil)
		c.Check(err, gc.IsNil)
		expect = *entity
		selectFields(&expect, "url", "blobname", "size", "blobhash")
		c.Check(e, jc.DeepEquals, &expect)
	}()

	query1 := <-store.entityqc
	c.Assert(query1.url, jc.DeepEquals, charm.MustParseURL("~bob/wordpress-1"))
	c.Assert(query1.fields, jc.DeepEquals, map[string]int{
		"blobname": 1,
		"size":     1,
	})
	e := *entity
	selectFields(&e, "url", "blobname", "size")
	query1.reply <- entityReply{
		entity: &e,
	}

	// When the entity fields are added, we expect another query
	// because that invalidates the cache.
	query2 := <-store.entityqc
	c.Assert(query2.url, jc.DeepEquals, charm.MustParseURL("~bob/wordpress-1"))
	c.Assert(query2.fields, jc.DeepEquals, map[string]int{
		"blobhash": 1,
		"blobname": 1,
		"size":     1,
	})
	e = *entity
	selectFields(&e, "url", "blobhash", "blobname", "size")
	query2.reply <- entityReply{
		entity: &e,
	}
	<-queryDone
}

func (*suite) TestLookupByDifferentKey(c *gc.C) {
	entityFetchCount := 0
	entity := &mongodoc.Entity{
		URL:      charm.MustParseURL("~bob/wordpress-1"),
		BaseURL:  charm.MustParseURL("~bob/wordpress"),
		BlobName: "w1",
	}
	baseEntity := &mongodoc.BaseEntity{
		URL:  charm.MustParseURL("~bob/wordpress"),
		Name: "wordpress",
	}
	store := &callbackStore{
		findBestEntity: func(url *charm.URL, fields map[string]int) (*mongodoc.Entity, error) {
			entityFetchCount++
			return entity, nil
		},
		findBaseEntity: func(url *charm.URL, fields map[string]int) (*mongodoc.BaseEntity, error) {
			if url.String() != "cs:~bob/wordpress" {
				return nil, params.ErrNotFound
			}
			return baseEntity, nil
		},
	}
	cache := entitycache.New(store)
	defer cache.Close()
	e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-1"), nil)
	c.Assert(err, gc.IsNil)
	c.Assert(e, gc.Equals, entity)

	oldEntity := entity

	// The second fetch will trigger another query because
	// we can't tell whether it's the same entity or not,
	// but it should return the cached entity anyway.
	entity = &mongodoc.Entity{
		URL:      charm.MustParseURL("~bob/wordpress-1"),
		BaseURL:  charm.MustParseURL("~bob/wordpress"),
		BlobName: "w2",
	}
	e, err = cache.Entity(charm.MustParseURL("~bob/wordpress"), nil)
	c.Assert(err, gc.IsNil)
	c.Logf("got %p; old entity %p; new entity %p", e, oldEntity, entity)
	c.Assert(e, gc.Equals, oldEntity)
	c.Assert(entityFetchCount, gc.Equals, 2)
}

func (s *suite) TestIterSingle(c *gc.C) {
	store := newChanStore()
	store.findBestEntity = func(url *charm.URL, fields map[string]int) (*mongodoc.Entity, error) {
		c.Errorf("store query made unexpectedly")
		return nil, errgo.New("no queries expected during iteration")
	}
	cache := entitycache.New(store)
	defer cache.Close()
	fakeIter := newFakeIter()
	iter := entitycache.CacheIter(cache, fakeIter, map[string]int{"size": 1, "blobsize": 1})
	nextDone := make(chan struct{})
	go func() {
		defer close(nextDone)
		ok := iter.Next()
		c.Assert(ok, gc.Equals, true)
	}()
	iterq := <-fakeIter.req
	entity := &mongodoc.Entity{
		URL:      charm.MustParseURL("~bob/wordpress-1"),
		BaseURL:  charm.MustParseURL("~bob/wordpress"),
		BlobName: "w1",
	}
	*iterq.entity = *entity
	expectCached := iterq.entity
	iterq.reply <- nil

	// The iterator should batch up entities so make sure that
	// it does not return the entry immediately.
	select {
	case <-nextDone:
		c.Fatalf("Next returned early - no batching?")
	case <-time.After(10 * time.Millisecond):
	}

	// Get the next iterator query and reply to signal that
	// the iterator has completed.
	iterq = <-fakeIter.req
	iterq.reply <- errIterFinished

	// The base entity should be requested asynchronously now.
	baseQuery := <-store.baseEntityqc

	// ... but the initial reply shouldn't be held up by that.
	<-nextDone

	// Check that the entity is the one we expect and that it hasn't
	// changed from the original.
	c.Assert(iter.Entity(), gc.Equals, expectCached)
	c.Assert(iter.Entity(), jc.DeepEquals, entity)

	// Check that the entity can now be fetched from the cache.
	e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-1"), nil)
	c.Assert(err, gc.IsNil)
	c.Assert(e, gc.Equals, expectCached)

	// A request for the base entity should now block
	// until the initial base entity request has been satisfied.
	baseEntity := &mongodoc.BaseEntity{
		URL:  charm.MustParseURL("~bob/wordpress"),
		Name: "wordpress",
	}
	queryDone := make(chan struct{})
	go func() {
		defer close(queryDone)
		e, err := cache.BaseEntity(charm.MustParseURL("~bob/wordpress"), nil)
		c.Check(err, gc.IsNil)
		c.Check(e, gc.Equals, baseEntity)
	}()

	// Check that no additional base entity query is made.
	select {
	case <-queryDone:
		c.Fatalf("Next returned early - no batching?")
	case <-time.After(10 * time.Millisecond):
	}

	// Reply to the base entity query ...
	baseQuery.reply <- baseEntityReply{
		entity: baseEntity,
	}
	// ... which should result in the one we just made
	// being satisfied too.
	<-queryDone
}

func (*suite) TestIterWithEntryAlreadyInCache(c *gc.C) {
	store := &staticStore{
		entities: []*mongodoc.Entity{{
			URL:      charm.MustParseURL("~bob/wordpress-1"),
			BaseURL:  charm.MustParseURL("~bob/wordpress"),
			BlobName: "w1",
		}, {
			URL:      charm.MustParseURL("~bob/wordpress-2"),
			BaseURL:  charm.MustParseURL("~bob/wordpress"),
			BlobName: "w2",
		}, {
			URL:      charm.MustParseURL("~alice/mysql-1"),
			BaseURL:  charm.MustParseURL("~alice/mysql"),
			BlobName: "a1",
		}},
		baseEntities: []*mongodoc.BaseEntity{{
			URL: charm.MustParseURL("~bob/wordpress"),
		}, {
			URL: charm.MustParseURL("~alice/mysql"),
		}},
	}
	cache := entitycache.New(store)
	defer cache.Close()
	e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-1"), map[string]int{"size": 1, "blobsize": 1})
	c.Assert(err, gc.IsNil)
	c.Assert(e, gc.Equals, store.entities[0])

	be, err := cache.BaseEntity(charm.MustParseURL("~bob/wordpress"), map[string]int{"size": 1, "blobsize": 1})
	c.Assert(err, gc.IsNil)
	c.Assert(be, gc.Equals, store.baseEntities[0])

	iterEntity := &mongodoc.Entity{
		URL:      charm.MustParseURL("~bob/wordpress-1"),
		BaseURL:  charm.MustParseURL("~bob/wordpress"),
		BlobName: "w2",
	}

	fakeIter := newFakeIter()
	iter := entitycache.CacheIter(cache, fakeIter, map[string]int{"size": 1, "blobsize": 1})
	iterDone := make(chan struct{})
	go func() {
		defer close(iterDone)
		ok := iter.Next()
		c.Check(ok, gc.Equals, true)
		// Even though the entity is in the cache, we still
		// receive the entity returned from the iterator.
		// Strictly speaking this is implementation-dependent,
		// so we just check with DeepEquals rather than equality.
		c.Check(iter.Entity(), jc.DeepEquals, iterEntity)

		ok = iter.Next()
		c.Check(ok, gc.Equals, false)
	}()

	// Provide the iterator request with an entity that's already
	// in the cache.
	iterq := <-fakeIter.req
	*iterq.entity = *iterEntity
	iterq.reply <- nil

	iterq = <-fakeIter.req
	iterq.reply <- errIterFinished

	<-iterDone

	// The original cached entities should still be there.
	cache = entitycache.New(store)
	defer cache.Close()
	e, err = cache.Entity(charm.MustParseURL("~bob/wordpress-1"), nil)
	c.Assert(err, gc.IsNil)
	c.Assert(e, gc.Equals, store.entities[0])

	be, err = cache.BaseEntity(charm.MustParseURL("~bob/wordpress"), nil)
	c.Assert(err, gc.IsNil)
	c.Assert(be, gc.Equals, store.baseEntities[0])
}

func (*suite) TestIterCloseEarlyWhenBatchLimitExceeded(c *gc.C) {
	// The iterator gets closed when the batch limit has been
	// exceeded.

	entities := make([]*mongodoc.Entity, entitycache.BaseEntityThreshold)
	baseEntities := make([]*mongodoc.BaseEntity, entitycache.BaseEntityThreshold)
	for i := range entities {
		entities[i] = &mongodoc.Entity{
			URL: &charm.URL{
				Schema:   "cs",
				Name:     fmt.Sprintf("wordpress%d", i),
				User:     "bob",
				Revision: i,
			},
			BaseURL: &charm.URL{
				Name: fmt.Sprintf("wordpress%d", i),
				User: "bob",
			},
			BlobName: fmt.Sprintf("w%d", i),
		}
		baseEntities[i] = &mongodoc.BaseEntity{
			URL: entities[i].BaseURL,
		}
	}
	store := &staticStore{
		baseEntities: baseEntities,
	}
	cache := entitycache.New(store)
	fakeIter := &sliceIter{
		entities: entities,
	}
	iter := entitycache.CacheIter(cache, fakeIter, map[string]int{"blobname": 1})
	iter.Close()
	c.Assert(iter.Next(), gc.Equals, false)
}

func (*suite) TestIterCloseEarlyBeforeBatchLimitExceeded(c *gc.C) {
	// The iterator gets closed before the batch limit has
	// been exceeded (this can only be found out
	// when there's an entry already cached)

	// prime the store with base entities, but no entities.
	// The entities should be satisfied from the iterator.
	store := &staticStore{
		entities: []*mongodoc.Entity{{
			URL:      charm.MustParseURL("~bob/wordpress-2"),
			BaseURL:  charm.MustParseURL("~bob/wordpress"),
			BlobName: "w2",
		}},
		baseEntities: []*mongodoc.BaseEntity{{
			URL: charm.MustParseURL("~bob/wordpress"),
		}, {
			URL:    charm.MustParseURL("~alice/mysql"),
			Public: true,
		}},
	}
	cache := entitycache.New(store)
	cache.AddBaseEntityFields(map[string]int{"public": 1})
	// Ask for one of the entities that will be iterated over,
	// so that the Iter.run loop will send an entity without
	// starting the base URL fetch.
	e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-2"), map[string]int{"blobname": 1})
	c.Assert(err, gc.IsNil)
	c.Assert(e, jc.DeepEquals, store.entities[0])

	entities := []*mongodoc.Entity{{
		URL:      charm.MustParseURL("~alice/mysql-1"),
		BaseURL:  charm.MustParseURL("~alice/mysql"),
		BlobName: "a1",
	}, {
		URL:      charm.MustParseURL("~bob/wordpress-2"),
		BaseURL:  charm.MustParseURL("~bob/wordpress"),
		BlobName: "w1",
	}}

	fakeIter := newFakeIter()
	iter := entitycache.CacheIter(cache, fakeIter, map[string]int{"blobname": 1})
	iterDone := make(chan struct{})
	go func() {
		defer close(iterDone)
		// Close the iterator early without reading any entries.
		iter.Close()
	}()

	// Wait for the first Next request and reply to it,
	// so that we have added a pending base entity
	iterq := <-fakeIter.req
	*iterq.entity = *entities[0]
	iterq.reply <- nil

	// Wait for another Next request and reply with the entry that
	// we caused to be cached earlier. This will cause the entity to
	// be sent directly, which will result in iter.send returning
	// false because nothing is reading on iter.entityc and we have
	// called iter.Close. This in turn should cause the pending
	// base entity fetches to be cleared out and Iter.run to terminate
	// and thus the Close to finish.
	iterq = <-fakeIter.req
	*iterq.entity = *entities[1]
	iterq.reply <- nil

	// Wait for the Close to complete.
	select {
	case <-iterDone:
	case <-fakeIter.req:
		c.Fatalf("unexpected extra request made")
	}

	// Ensure that we can still retrieve the base entity that
	// was added (and then removed) by the iterator.
	be, err := cache.BaseEntity(charm.MustParseURL("~alice/mysql-1"), map[string]int{"public": 1})
	c.Assert(err, gc.IsNil)
	c.Assert(be, jc.DeepEquals, store.baseEntities[1])
}

func (*suite) TestIterEntityBatchLimitExceeded(c *gc.C) {
	entities := make([]*mongodoc.Entity, entitycache.EntityThreshold)
	for i := range entities {
		entities[i] = &mongodoc.Entity{
			URL: &charm.URL{
				Schema:   "cs",
				Name:     "wordpress",
				User:     "bob",
				Revision: i,
			},
			BaseURL:  charm.MustParseURL("~bob/wordpress"),
			BlobName: fmt.Sprintf("w%d", i),
		}
	}
	entities = append(entities, &mongodoc.Entity{
		URL:     charm.MustParseURL("~alice/mysql1-1"),
		BaseURL: charm.MustParseURL("~alice/mysql1"),
	})
	store := newChanStore()
	cache := entitycache.New(store)
	fakeIter := &sliceIter{
		entities: entities,
	}
	iter := entitycache.CacheIter(cache, fakeIter, map[string]int{"blobname": 1})

	// The iterator should fetch up to entityThreshold entities
	// from the underlying iterator before sending
	// the batched base-entity request, then it
	// will make all those entries available.
	query := <-store.baseEntityqc
	c.Assert(query.url, jc.DeepEquals, charm.MustParseURL("~bob/wordpress"))
	query.reply <- baseEntityReply{
		entity: &mongodoc.BaseEntity{
			URL: charm.MustParseURL("~bob/wordpress"),
		},
	}
	for i := 0; i < entitycache.EntityThreshold; i++ {
		ok := iter.Next()
		c.Assert(ok, gc.Equals, true)
		c.Assert(iter.Entity(), jc.DeepEquals, entities[i])
	}
	// When the iterator reaches its end, the
	// remaining entity and base entity are fetched.
	query = <-store.baseEntityqc
	c.Assert(query.url, jc.DeepEquals, charm.MustParseURL("~alice/mysql1"))
	query.reply <- baseEntityReply{
		entity: &mongodoc.BaseEntity{
			URL: charm.MustParseURL("~alice/mysql1"),
		},
	}

	ok := iter.Next()
	c.Assert(ok, gc.Equals, true)
	c.Assert(iter.Entity(), jc.DeepEquals, entities[entitycache.EntityThreshold])

	// Check that all the entities and base entities are in fact cached.
	for _, want := range entities {
		got, err := cache.Entity(want.URL, nil)
		c.Assert(err, gc.IsNil)
		c.Assert(got, jc.DeepEquals, want)
		gotBase, err := cache.BaseEntity(want.URL, nil)
		c.Assert(err, gc.IsNil)
		c.Assert(gotBase, jc.DeepEquals, &mongodoc.BaseEntity{
			URL: want.BaseURL,
		})
	}
}

func (*suite) TestIterError(c *gc.C) {
	cache := entitycache.New(&staticStore{})
	fakeIter := newFakeIter()
	iter := entitycache.CacheIter(cache, fakeIter, nil)
	// Err returns nil while the iteration is in progress.
	err := iter.Err()
	c.Assert(err, gc.IsNil)

	iterq := <-fakeIter.req
	iterq.reply <- errgo.New("iterator error")

	ok := iter.Next()
	c.Assert(ok, gc.Equals, false)
	err = iter.Err()
	c.Assert(err, gc.ErrorMatches, "iterator error")
}

type iterQuery struct {
	// entity holds the entity to be filled by the Next request.
	entity *mongodoc.Entity
	// reply should be send on when the Next request has
	// completed. When the iteration is complete,
	// errIterFinished should be sent.
	reply chan error
}

// fakeIter provides a mock iterator implementation
// that sends each request for an entity to
// another goroutine for a result.
type fakeIter struct {
	closed bool
	err    error

	// req holds a channel that is sent a value
	// whenever the Next method is called.
	req chan iterQuery
}

func newFakeIter() *fakeIter {
	return &fakeIter{
		req: make(chan iterQuery, 1),
	}
}

// Next implements mgoIter.Next. The
// x parameter must be a *mongodoc.Entity.
func (i *fakeIter) Next(x interface{}) bool {
	if i.closed {
		panic("Next called after Close")
	}
	if i.err != nil {
		return false
	}
	reply := make(chan error)
	i.req <- iterQuery{
		entity: x.(*mongodoc.Entity),
		reply:  reply,
	}
	i.err = <-reply
	return i.err == nil
}

var errIterFinished = errgo.New("iteration finished")

// Close implements mgoIter.Close.
func (i *fakeIter) Close() error {
	i.closed = true
	if i.err == errIterFinished {
		return nil
	}
	return i.err
}

// Close implements mgoIter.Err.
func (i *fakeIter) Err() error {
	if i.err == errIterFinished {
		return nil
	}
	return i.err
}

// sliceIter implements mgoIter over a slice of entities,
// returning each one in turn.
type sliceIter struct {
	entities []*mongodoc.Entity
	closed   bool
}

func (iter *sliceIter) Next(x interface{}) bool {
	if iter.closed {
		panic("Next called after Close")
	}
	if len(iter.entities) == 0 {
		return false
	}
	e := x.(*mongodoc.Entity)
	*e = *iter.entities[0]
	iter.entities = iter.entities[1:]
	return true
}

func (iter *sliceIter) Err() error {
	return nil
}

func (iter *sliceIter) Close() error {
	iter.closed = true
	return nil
}

type chanStore struct {
	entityqc     chan entityQuery
	baseEntityqc chan baseEntityQuery
	*callbackStore
}

func newChanStore() *chanStore {
	entityqc := make(chan entityQuery, 1)
	baseEntityqc := make(chan baseEntityQuery, 1)
	return &chanStore{
		entityqc:     entityqc,
		baseEntityqc: baseEntityqc,
		callbackStore: &callbackStore{
			findBestEntity: func(url *charm.URL, fields map[string]int) (*mongodoc.Entity, error) {
				reply := make(chan entityReply)
				entityqc <- entityQuery{
					url:    url,
					fields: fields,
					reply:  reply,
				}
				r := <-reply
				return r.entity, r.err
			},
			findBaseEntity: func(url *charm.URL, fields map[string]int) (*mongodoc.BaseEntity, error) {
				reply := make(chan baseEntityReply)
				baseEntityqc <- baseEntityQuery{
					url:    url,
					fields: fields,
					reply:  reply,
				}
				r := <-reply
				return r.entity, r.err
			},
		},
	}
}

type callbackStore struct {
	findBestEntity func(url *charm.URL, fields map[string]int) (*mongodoc.Entity, error)
	findBaseEntity func(url *charm.URL, fields map[string]int) (*mongodoc.BaseEntity, error)
}

func (s *callbackStore) FindBestEntity(url *charm.URL, fields map[string]int) (*mongodoc.Entity, error) {
	return s.findBestEntity(url, fields)
}

func (s *callbackStore) FindBaseEntity(url *charm.URL, fields map[string]int) (*mongodoc.BaseEntity, error) {
	return s.findBaseEntity(url, fields)
}

type staticStore struct {
	entities     []*mongodoc.Entity
	baseEntities []*mongodoc.BaseEntity
}

func (s *staticStore) FindBestEntity(url *charm.URL, fields map[string]int) (*mongodoc.Entity, error) {
	for _, e := range s.entities {
		if *url == *e.URL {
			return e, nil
		}
	}
	return nil, params.ErrNotFound
}

func (s *staticStore) FindBaseEntity(url *charm.URL, fields map[string]int) (*mongodoc.BaseEntity, error) {
	for _, e := range s.baseEntities {
		if *url == *e.URL {
			return e, nil
		}
	}
	return nil, params.ErrNotFound
}

// selectFields zeros out all fields in x (which must be a pointer to
// struct) that are not mentioned in sel.
func selectFields(x interface{}, fields ...string) {
	sel := make(map[string]int)
	for _, f := range fields {
		sel[f] = 1
	}
	xv := reflect.ValueOf(x).Elem()
	xt := xv.Type()
	for i := 0; i < xt.NumField(); i++ {
		if _, ok := sel[strings.ToLower(xt.Field(i).Name)]; ok {
			continue
		}
		xv.Field(i).Set(reflect.Zero(xt.Field(i).Type))
	}
}

func denormalizeAll(es []*mongodoc.Entity, bs []*mongodoc.BaseEntity) {
	for _, e := range es {
		denormalizeEntity(e)
	}
	for _, e := range bs {
		denormalizeBaseEntity(e)
	}
}

// denormalizedEntity is a convenience function that returns
// a copy of e with its denormalized fields filled out.
func denormalizedEntity(e *mongodoc.Entity) *mongodoc.Entity {
	e1 := *e
	denormalizeEntity(&e1)
	return &e1
}

// denormalizeEntity sets all denormalized fields in e
// from their associated canonical fields.
//
// It is the responsibility of the caller to set e.SupportedSeries
// if the entity URL does not contain a series. If the entity
// URL *does* contain a series, e.SupportedSeries will
// be overwritten.
//
// This is exported for the purposes of tests that
// need to create directly into the database.
func denormalizeEntity(e *mongodoc.Entity) {
	if e.URL.User == "" {
		panic("entity with no user")
	}
	e.BaseURL = mongodoc.BaseURL(e.URL)
	e.Name = e.URL.Name
	e.User = e.URL.User
	e.Revision = e.URL.Revision
	e.Series = e.URL.Series
	if e.URL.Series != "" {
		if e.URL.Series == "bundle" {
			e.SupportedSeries = nil
		} else {
			e.SupportedSeries = []string{e.URL.Series}
		}
	}
	if e.PromulgatedURL == nil {
		e.PromulgatedRevision = -1
	} else {
		e.PromulgatedRevision = e.PromulgatedURL.Revision
	}
}

func denormalizeBaseEntity(e *mongodoc.BaseEntity) {
	if e.URL.Revision != -1 {
		panic("base entity with revision")
	}
	if e.URL.User == "" {
		panic("base entity with no user")
	}
	e.Name = e.URL.Name
	e.User = e.URL.User
}
