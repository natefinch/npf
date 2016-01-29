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
	cache.AddBaseEntityFields(map[string]int{"name": 1})

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
		e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-1"), map[string]int{"blobname": 1})
		c.Check(err, gc.IsNil)
		c.Check(e, jc.DeepEquals, selectEntityFields(entity, entityFields("blobname")))
	}()

	// Acquire both the queries before replying so that we know they've been
	// issued concurrently.
	query1 := <-store.entityqc
	c.Assert(query1.url, jc.DeepEquals, charm.MustParseURL("~bob/wordpress-1"))
	c.Assert(query1.fields, jc.DeepEquals, entityFields("blobname"))
	query2 := <-store.baseEntityqc
	c.Assert(query2.url, jc.DeepEquals, charm.MustParseURL("~bob/wordpress"))
	c.Assert(query2.fields, jc.DeepEquals, baseEntityFields("name"))
	query1.reply <- entityReply{
		entity: entity,
	}
	query2.reply <- baseEntityReply{
		entity: baseEntity,
	}
	<-queryDone

	// Accessing the same entity again and the base entity should
	// not call any method on the store - if it does, then it'll send
	// on the query channels and we won't receive it, so the test
	// will deadlock.
	e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-1"), map[string]int{"baseurl": 1, "blobname": 1})
	c.Check(err, gc.IsNil)
	c.Check(e, jc.DeepEquals, selectEntityFields(entity, entityFields("blobname")))

	be, err := cache.BaseEntity(charm.MustParseURL("~bob/wordpress"), map[string]int{"name": 1})
	c.Check(err, gc.IsNil)
	c.Check(be, jc.DeepEquals, selectBaseEntityFields(baseEntity, baseEntityFields("name")))
}

func (*suite) TestEntityIssuesBaseEntityQuerySequentiallyForPromulgatedURL(c *gc.C) {
	store := newChanStore()
	cache := entitycache.New(store)
	defer cache.Close()
	cache.AddBaseEntityFields(map[string]int{"name": 1})

	entity := &mongodoc.Entity{
		URL:            charm.MustParseURL("~bob/wordpress-1"),
		PromulgatedURL: charm.MustParseURL("wordpress-5"),
		BaseURL:        charm.MustParseURL("~bob/wordpress"),
		BlobName:       "w1",
		Size:           1,
	}
	baseEntity := &mongodoc.BaseEntity{
		URL:  charm.MustParseURL("~bob/wordpress"),
		Name: "wordpress",
	}
	queryDone := make(chan struct{})
	go func() {
		defer close(queryDone)
		e, err := cache.Entity(charm.MustParseURL("wordpress-1"), map[string]int{"blobname": 1})
		c.Check(err, gc.IsNil)
		c.Check(e, jc.DeepEquals, selectEntityFields(entity, entityFields("blobname")))
	}()

	// Acquire both the queries before replying so that we know they've been
	// issued concurrently.
	query1 := <-store.entityqc
	c.Assert(query1.url, jc.DeepEquals, charm.MustParseURL("wordpress-1"))
	c.Assert(query1.fields, jc.DeepEquals, entityFields("blobname"))
	query1.reply <- entityReply{
		entity: entity,
	}
	<-queryDone

	// The base entity query is only issued when the original entity
	// is received. We can tell this because the URL in the query
	// contains the ~bob user which can't be inferred from the
	// original URL.
	query2 := <-store.baseEntityqc
	c.Assert(query2.url, jc.DeepEquals, charm.MustParseURL("~bob/wordpress"))
	c.Assert(query2.fields, jc.DeepEquals, baseEntityFields("name"))
	query2.reply <- baseEntityReply{
		entity: baseEntity,
	}

	// Accessing the same entity again and the base entity should
	// not call any method on the store - if it does, then it'll send
	// on the query channels and we won't receive it, so the test
	// will deadlock.
	e, err := cache.Entity(charm.MustParseURL("wordpress-1"), map[string]int{"baseurl": 1, "blobname": 1})
	c.Check(err, gc.IsNil)
	c.Check(e, jc.DeepEquals, selectEntityFields(entity, entityFields("blobname")))

	be, err := cache.BaseEntity(charm.MustParseURL("~bob/wordpress"), map[string]int{"name": 1})
	c.Check(err, gc.IsNil)
	c.Check(be, jc.DeepEquals, selectBaseEntityFields(baseEntity, baseEntityFields("name")))
}

func (*suite) TestFetchWhenFieldsChangeBeforeQueryResult(c *gc.C) {
	store := newChanStore()
	cache := entitycache.New(store)
	defer cache.Close()
	cache.AddBaseEntityFields(map[string]int{"name": 1})

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
		c.Check(fields, jc.DeepEquals, baseEntityFields("name"))
		return baseEntity, nil
	}

	queryDone := make(chan struct{})
	go func() {
		defer close(queryDone)
		e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-1"), nil)
		c.Check(err, gc.IsNil)
		c.Check(e, jc.DeepEquals, selectEntityFields(entity, entityFields()))
	}()

	query1 := <-store.entityqc
	c.Assert(query1.url, jc.DeepEquals, charm.MustParseURL("~bob/wordpress-1"))
	c.Assert(query1.fields, jc.DeepEquals, entityFields())
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
		e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-1"), map[string]int{"size": 1})
		c.Check(err, gc.IsNil)
		c.Check(e, jc.DeepEquals, selectEntityFields(entity2, entityFields("size")))
	}()
	// The second query should be sent immediately without waiting
	// for the first because it invalidates the cache..
	query2 := <-store.entityqc
	c.Assert(query2.url, jc.DeepEquals, charm.MustParseURL("~bob/wordpress-1"))
	c.Assert(query2.fields, jc.DeepEquals, entityFields("size"))
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
	e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-1"), nil)
	c.Check(err, gc.IsNil)
	c.Check(e, jc.DeepEquals, selectEntityFields(entity2, entityFields("size")))
}

func (*suite) TestSecondFetchesWaitForFirst(c *gc.C) {
	store := newChanStore()
	cache := entitycache.New(store)
	defer cache.Close()
	cache.AddBaseEntityFields(map[string]int{"name": 1})

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
		c.Check(fields, jc.DeepEquals, baseEntityFields("name"))
		return baseEntity, nil
	}

	var initialRequestGroup sync.WaitGroup
	initialRequestGroup.Add(1)
	go func() {
		defer initialRequestGroup.Done()
		e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-1"), nil)
		c.Check(err, gc.IsNil)
		c.Check(e, jc.DeepEquals, selectEntityFields(entity, entityFields()))
	}()

	query1 := <-store.entityqc
	c.Assert(query1.url, jc.DeepEquals, charm.MustParseURL("~bob/wordpress-1"))
	c.Assert(query1.fields, jc.DeepEquals, entityFields())

	// Send some more queries for the same charm. These should not send a
	// store request but instead wait for the first one.
	for i := 0; i < 5; i++ {
		initialRequestGroup.Add(1)
		go func() {
			defer initialRequestGroup.Done()
			e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-1"), nil)
			c.Check(err, gc.IsNil)
			c.Check(e, jc.DeepEquals, selectEntityFields(entity, entityFields()))
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
		e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-2"), nil)
		c.Check(err, gc.IsNil)
		c.Check(e, jc.DeepEquals, selectEntityFields(entity2, entityFields()))
	}()
	query2 := <-store.entityqc
	c.Assert(query2.url, jc.DeepEquals, charm.MustParseURL("~bob/wordpress-2"))
	c.Assert(query2.fields, jc.DeepEquals, entityFields())
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
		c.Check(e, jc.DeepEquals, selectEntityFields(entity, entityFields()))
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
		c.Check(e, jc.DeepEquals, selectEntityFields(entity, entityFields("blobname", "size")))

		// Adding existing entity fields should have no effect.
		cache.AddEntityFields(map[string]int{"blobname": 1, "size": 1})

		e, err = cache.Entity(charm.MustParseURL("cs:~bob/wordpress-1"), map[string]int{"size": 1})
		c.Check(err, gc.IsNil)
		c.Check(e, jc.DeepEquals, selectEntityFields(entity, entityFields("blobname", "size")))

		// Adding a new field should will cause the cache to be invalidated
		// and a new fetch to take place.

		cache.AddEntityFields(map[string]int{"blobhash": 1})
		e, err = cache.Entity(charm.MustParseURL("cs:~bob/wordpress-1"), nil)
		c.Check(err, gc.IsNil)
		c.Check(e, jc.DeepEquals, selectEntityFields(entity, entityFields("blobname", "size", "blobhash")))
	}()

	query1 := <-store.entityqc
	c.Assert(query1.url, jc.DeepEquals, charm.MustParseURL("~bob/wordpress-1"))
	c.Assert(query1.fields, jc.DeepEquals, entityFields("blobname", "size"))
	query1.reply <- entityReply{
		entity: entity,
	}

	// When the entity fields are added, we expect another query
	// because that invalidates the cache.
	query2 := <-store.entityqc
	c.Assert(query2.url, jc.DeepEquals, charm.MustParseURL("~bob/wordpress-1"))
	c.Assert(query2.fields, jc.DeepEquals, entityFields("blobhash", "blobname", "size"))
	query2.reply <- entityReply{
		entity: entity,
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
	c.Check(e, jc.DeepEquals, selectEntityFields(entity, entityFields()))

	oldEntity := e

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
	iter := cache.CustomIter(fakeIter, map[string]int{"size": 1, "blobsize": 1})
	nextDone := make(chan struct{})
	go func() {
		defer close(nextDone)
		ok := iter.Next()
		c.Assert(ok, gc.Equals, true)
	}()
	replyc := <-fakeIter.req
	entity := &mongodoc.Entity{
		URL:      charm.MustParseURL("~bob/wordpress-1"),
		BaseURL:  charm.MustParseURL("~bob/wordpress"),
		BlobName: "w1",
	}
	replyc <- iterReply{
		entity: entity,
	}

	// The iterator should batch up entities so make sure that
	// it does not return the entry immediately.
	select {
	case <-nextDone:
		c.Fatalf("Next returned early - no batching?")
	case <-time.After(10 * time.Millisecond):
	}

	// Get the next iterator query and reply to signal that
	// the iterator has completed.
	replyc = <-fakeIter.req
	replyc <- iterReply{
		err: errIterFinished,
	}

	// The base entity should be requested asynchronously now.
	baseQuery := <-store.baseEntityqc

	// ... but the initial reply shouldn't be held up by that.
	<-nextDone

	// Check that the entity is the one we expect.
	cachedEntity := iter.Entity()
	c.Assert(cachedEntity, jc.DeepEquals, selectEntityFields(entity, entityFields("size", "blobsize")))

	// Check that the entity can now be fetched from the cache.
	e, err := cache.Entity(charm.MustParseURL("~bob/wordpress-1"), nil)
	c.Assert(err, gc.IsNil)
	c.Assert(e, gc.Equals, cachedEntity)

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
		c.Check(e, jc.DeepEquals, selectBaseEntityFields(baseEntity, baseEntityFields()))
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
	c.Check(e, jc.DeepEquals, selectEntityFields(store.entities[0], entityFields("size", "blobsize")))
	cachedEntity := e

	be, err := cache.BaseEntity(charm.MustParseURL("~bob/wordpress"), nil)
	c.Assert(err, gc.IsNil)
	c.Check(be, jc.DeepEquals, selectBaseEntityFields(store.baseEntities[0], baseEntityFields()))
	cachedBaseEntity := be

	iterEntity := &mongodoc.Entity{
		URL:      charm.MustParseURL("~bob/wordpress-1"),
		BaseURL:  charm.MustParseURL("~bob/wordpress"),
		BlobName: "w2",
	}

	fakeIter := newFakeIter()
	iter := cache.CustomIter(fakeIter, map[string]int{"size": 1, "blobsize": 1})
	iterDone := make(chan struct{})
	go func() {
		defer close(iterDone)
		ok := iter.Next()
		c.Check(ok, gc.Equals, true)
		// Even though the entity is in the cache, we still
		// receive the entity returned from the iterator.
		// We can't actually tell this though.
		c.Check(iter.Entity(), jc.DeepEquals, selectEntityFields(iterEntity, entityFields("size", "blobsize")))

		ok = iter.Next()
		c.Check(ok, gc.Equals, false)
	}()

	// Provide the iterator request with an entity that's already
	// in the cache.
	replyc := <-fakeIter.req
	replyc <- iterReply{
		entity: iterEntity,
	}

	replyc = <-fakeIter.req
	replyc <- iterReply{
		err: errIterFinished,
	}
	<-iterDone

	// The original cached entities should still be there.
	e, err = cache.Entity(charm.MustParseURL("~bob/wordpress-1"), nil)
	c.Assert(err, gc.IsNil)
	c.Assert(e, gc.Equals, cachedEntity)

	be, err = cache.BaseEntity(charm.MustParseURL("~bob/wordpress"), nil)
	c.Assert(err, gc.IsNil)
	c.Assert(be, gc.Equals, cachedBaseEntity)
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
	iter := cache.CustomIter(fakeIter, map[string]int{"blobname": 1})
	iter.Close()
	c.Assert(iter.Next(), gc.Equals, false)
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
	iter := cache.CustomIter(fakeIter, map[string]int{"blobname": 1})

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
	iter := cache.CustomIter(fakeIter, nil)
	// Err returns nil while the iteration is in progress.
	err := iter.Err()
	c.Assert(err, gc.IsNil)

	replyc := <-fakeIter.req
	replyc <- iterReply{
		err: errgo.New("iterator error"),
	}

	ok := iter.Next()
	c.Assert(ok, gc.Equals, false)
	err = iter.Err()
	c.Assert(err, gc.ErrorMatches, "iterator error")
}

// iterReply holds a reply from a request from a fakeIter
// for the next item.
type iterReply struct {
	// entity holds the entity to be replied with.
	// Any fields not specified when creating the
	// iterator will be omitted from the result
	// sent to the entitycache code.
	entity *mongodoc.Entity

	// err holds any iteration error. When the iteration is complete,
	// errIterFinished should be sent.
	err error
}

// fakeIter provides a mock iterator implementation
// that sends each request for an entity to
// another goroutine for a result.
type fakeIter struct {
	closed bool
	fields map[string]int
	err    error

	// req holds a channel that is sent a value
	// whenever the Next method is called.
	req chan chan iterReply
}

func newFakeIter() *fakeIter {
	return &fakeIter{
		req: make(chan chan iterReply, 1),
	}
}

func (i *fakeIter) Iter(fields map[string]int) entitycache.StoreIter {
	i.fields = fields
	return i
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
	replyc := make(chan iterReply)
	i.req <- replyc
	reply := <-replyc
	i.err = reply.err
	if i.err == nil {
		*(x.(*mongodoc.Entity)) = *selectEntityFields(reply.entity, i.fields)
	} else if reply.entity != nil {
		panic("entity with non-nil error")
	}

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
	fields   map[string]int
	entities []*mongodoc.Entity
	closed   bool
}

func (i *sliceIter) Iter(fields map[string]int) entitycache.StoreIter {
	i.fields = fields
	return i
}

func (iter *sliceIter) Next(x interface{}) bool {
	if iter.closed {
		panic("Next called after Close")
	}
	if len(iter.entities) == 0 {
		return false
	}
	e := x.(*mongodoc.Entity)
	*e = *selectEntityFields(iter.entities[0], iter.fields)
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
	e, err := s.findBestEntity(url, fields)
	if err != nil {
		return nil, err
	}
	return selectEntityFields(e, fields), nil
}

func (s *callbackStore) FindBaseEntity(url *charm.URL, fields map[string]int) (*mongodoc.BaseEntity, error) {
	e, err := s.findBaseEntity(url, fields)
	if err != nil {
		return nil, err
	}
	return selectBaseEntityFields(e, fields), nil
}

type staticStore struct {
	entities     []*mongodoc.Entity
	baseEntities []*mongodoc.BaseEntity
}

func (s *staticStore) FindBestEntity(url *charm.URL, fields map[string]int) (*mongodoc.Entity, error) {
	for _, e := range s.entities {
		if *url == *e.URL {
			return selectEntityFields(e, fields), nil
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

func selectEntityFields(x *mongodoc.Entity, fields map[string]int) *mongodoc.Entity {
	e := selectFields(x, fields).(*mongodoc.Entity)
	if e.URL == nil {
		panic("url empty after selectfields")
	}
	return e
}

func selectBaseEntityFields(x *mongodoc.BaseEntity, fields map[string]int) *mongodoc.BaseEntity {
	return selectFields(x, fields).(*mongodoc.BaseEntity)
}

// selectFields returns a copy of x (which must
// be a pointer to struct) with all fields zeroed
// except those mentioned in fields.
func selectFields(x interface{}, fields map[string]int) interface{} {
	xv := reflect.ValueOf(x).Elem()
	xt := xv.Type()
	dv := reflect.New(xt).Elem()
	dv.Set(xv)
	for i := 0; i < xt.NumField(); i++ {
		f := xt.Field(i)
		if _, ok := fields[bsonFieldName(f)]; ok {
			continue
		}
		dv.Field(i).Set(reflect.Zero(f.Type))
	}
	return dv.Addr().Interface()
}

func bsonFieldName(f reflect.StructField) string {
	t := f.Tag.Get("bson")
	if t == "" {
		return strings.ToLower(f.Name)
	}
	if i := strings.Index(t, ","); i >= 0 {
		t = t[0:i]
	}
	if t != "" {
		return t
	}
	return strings.ToLower(f.Name)
}

func entityFields(fields ...string) map[string]int {
	return addFields(entitycache.RequiredEntityFields, fields...)
}

func baseEntityFields(fields ...string) map[string]int {
	return addFields(entitycache.RequiredBaseEntityFields, fields...)
}

func addFields(fields map[string]int, extra ...string) map[string]int {
	fields1 := make(map[string]int)
	for f := range fields {
		fields1[f] = 1
	}
	for _, f := range extra {
		fields1[f] = 1
	}
	return fields1
}
