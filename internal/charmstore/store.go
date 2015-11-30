// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore // import "gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/juju/loggo"
	"github.com/juju/utils/parallel"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"
	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/bakery/mgostorage"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/natefinch/lumberjack.v2"

	"gopkg.in/juju/charmstore.v5-unstable/audit"
	"gopkg.in/juju/charmstore.v5-unstable/internal/blobstore"
	"gopkg.in/juju/charmstore.v5-unstable/internal/cache"
	"gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"
	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
)

var logger = loggo.GetLogger("charmstore.internal.charmstore")

var (
	errClosed          = errgo.New("charm store has been closed")
	ErrTooManySessions = errgo.New("too many mongo sessions in use")
)

// Pool holds a connection to the underlying charm and blob
// data stores. Calling its Store method returns a new Store
// from the pool that can be used to process short-lived requests
// to access and modify the store.
type Pool struct {
	db           StoreDatabase
	es           *SearchIndex
	bakeryParams *bakery.NewServiceParams
	stats        stats
	run          *parallel.Run

	// statsCache holds a cache of AggregatedCounts
	// values, keyed by entity id. When the id has no
	// revision, the counts apply to all revisions of the
	// entity.
	statsCache *cache.Cache

	config ServerParams

	// auditEncoder encodes messages to auditLogger.
	auditEncoder *json.Encoder
	auditLogger  *lumberjack.Logger

	// reqStoreC is a buffered channel that contains allocated
	// stores that are not currently in use.
	reqStoreC chan *Store

	// mu guards the fields following it.
	mu sync.Mutex

	// storeCount holds the number of stores currently allocated.
	storeCount int

	// closed holds whether the handler has been closed.
	closed bool
}

// reqStoreCacheSize holds the maximum number of store
// instances to keep around cached when there is no
// limit specified by config.MaxMgoSessions.
const reqStoreCacheSize = 50

// maxAsyncGoroutines holds the maximum number
// of goroutines that will be started by Store.Go.
const maxAsyncGoroutines = 50

// NewPool returns a Pool that uses the given database
// and search index. If bakeryParams is not nil,
// the Bakery field in the resulting Store will be set
// to a new Service that stores macaroons in mongo.
//
// The pool must be closed (with the Close method)
// after use.
func NewPool(db *mgo.Database, si *SearchIndex, bakeryParams *bakery.NewServiceParams, config ServerParams) (*Pool, error) {
	if config.StatsCacheMaxAge == 0 {
		config.StatsCacheMaxAge = time.Hour
	}

	p := &Pool{
		db:          StoreDatabase{db}.copy(),
		es:          si,
		statsCache:  cache.New(config.StatsCacheMaxAge),
		config:      config,
		run:         parallel.NewRun(maxAsyncGoroutines),
		auditLogger: config.AuditLogger,
	}
	if config.MaxMgoSessions > 0 {
		p.reqStoreC = make(chan *Store, config.MaxMgoSessions)
	} else {
		p.reqStoreC = make(chan *Store, reqStoreCacheSize)
	}
	if bakeryParams != nil {
		bp := *bakeryParams
		// Fill out any bakery parameters explicitly here so
		// that we use the same values when each Store is
		// created. We don't fill out bp.Store field though, as
		// that needs to hold the correct mongo session which we
		// only know when the Store is created from the Pool.
		if bp.Key == nil {
			var err error
			bp.Key, err = bakery.GenerateKey()
			if err != nil {
				return nil, errgo.Notef(err, "cannot generate bakery key")
			}
		}
		if bp.Locator == nil {
			bp.Locator = bakery.PublicKeyLocatorMap(nil)
		}
		p.bakeryParams = &bp
	}

	if config.AuditLogger != nil {
		p.auditLogger = config.AuditLogger
		p.auditEncoder = json.NewEncoder(p.auditLogger)
	}

	store := p.Store()
	defer store.Close()
	if err := store.ensureIndexes(); err != nil {
		return nil, errgo.Notef(err, "cannot ensure indexes")
	}
	if err := store.ES.ensureIndexes(false); err != nil {
		return nil, errgo.Notef(err, "cannot ensure elasticsearch indexes")
	}
	return p, nil
}

// Close closes the pool. This must be called when the pool
// is finished with.
func (p *Pool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()
	p.run.Wait()
	p.db.Close()
	// Close all cached stores. Any used by
	// outstanding requests will be closed when the
	// requests complete.
	for {
		select {
		case s := <-p.reqStoreC:
			s.DB.Close()
		default:
			return
		}
	}

	p.auditLogger.Close()
}

// RequestStore returns a store for a client request. It returns
// an error with a ErrTooManySessions cause
// if too many mongo sessions are in use.
func (p *Pool) RequestStore() (*Store, error) {
	store, err := p.requestStoreNB(false)
	if store != nil {
		return store, nil
	}
	if errgo.Cause(err) != ErrTooManySessions {
		return nil, errgo.Mask(err)
	}
	// No handlers currently available - we've exceeded our concurrency limit
	// so wait for a handler to become available.
	select {
	case store := <-p.reqStoreC:
		return store, nil
	case <-time.After(p.config.HTTPRequestWaitDuration):
		return nil, errgo.Mask(err, errgo.Is(ErrTooManySessions))
	}
}

// Store returns a Store that can be used to access the database.
//
// It must be closed (with the Close method) after use.
func (p *Pool) Store() *Store {
	store, _ := p.requestStoreNB(true)
	return store
}

// requestStoreNB is like RequestStore except that it
// does not block when a Store is not immediately
// available, in which case it returns an error with
// a ErrTooManySessions cause.
//
// If always is true, it will never return an error.
func (p *Pool) requestStoreNB(always bool) (*Store, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed && !always {
		return nil, errClosed
	}
	select {
	case store := <-p.reqStoreC:
		return store, nil
	default:
	}
	if !always && p.config.MaxMgoSessions > 0 && p.storeCount >= p.config.MaxMgoSessions {
		return nil, ErrTooManySessions
	}
	p.storeCount++
	db := p.db.copy()
	store := &Store{
		DB:        db,
		BlobStore: blobstore.New(db.Database, "entitystore"),
		ES:        p.es,
		stats:     &p.stats,
		pool:      p,
	}
	if p.bakeryParams != nil {
		store.Bakery = newBakery(db, *p.bakeryParams)
	}
	return store, nil
}

func newBakery(db StoreDatabase, bp bakery.NewServiceParams) *bakery.Service {
	macStore, err := mgostorage.New(db.Macaroons())
	if err != nil {
		// Should never happen.
		panic(errgo.Newf("unexpected error from mgostorage.New: %v", err))
	}
	bp.Store = macStore
	bsvc, err := bakery.NewService(bp)
	if err != nil {
		// This should never happen because the only reason bakery.NewService
		// can fail is if it can't generate a key, and we have already made
		// sure that the key is generated.
		panic(errgo.Notef(err, "cannot make bakery service"))
	}
	return bsvc
}

// Store holds a connection to the underlying charm and blob
// data stores that is appropriate for short term use.
type Store struct {
	DB        StoreDatabase
	BlobStore *blobstore.Store
	ES        *SearchIndex
	Bakery    *bakery.Service
	stats     *stats
	pool      *Pool
}

// Copy returns a new store with a lifetime
// independent of s. Use this method if you
// need to use a store in an independent goroutine.
//
// It must be closed (with the Close method) after use.
func (s *Store) Copy() *Store {
	s1 := *s
	s1.DB = s.DB.clone()
	s1.BlobStore = blobstore.New(s1.DB.Database, "entitystore")
	if s.Bakery != nil {
		s1.Bakery = newBakery(s1.DB, *s.pool.bakeryParams)
	}

	s.pool.mu.Lock()
	s.pool.storeCount++
	s.pool.mu.Unlock()

	return &s1
}

// Close closes the store instance.
func (s *Store) Close() {
	// Refresh the mongodb session so that the
	// next time the Store is used, it will acquire
	// a new connection from the pool as if the
	// session had been copied.
	s.DB.Session.Refresh()

	s.pool.mu.Lock()
	defer s.pool.mu.Unlock()
	if !s.pool.closed && (s.pool.config.MaxMgoSessions == 0 || s.pool.storeCount <= s.pool.config.MaxMgoSessions) {
		// The pool isn't overloaded, so put the store
		// back. Note that the default case should
		// never happen when MaxMgoSessions > 0.
		select {
		case s.pool.reqStoreC <- s:
			return
		default:
			// No space for handler - this may happen when
			// the number of actual sessions has exceeded
			// the requested maximum (for example when
			// a request already at the limit uses another session,
			// or when we are imposing no limit).
		}
	}
	s.DB.Close()
	s.pool.storeCount--
}

// SetReconnectTimeout sets the length of time that
// mongo requests will block waiting to reconnect
// to a disconnected mongo server. If it is zero,
// requests may block forever.
func (s *Store) SetReconnectTimeout(d time.Duration) {
	s.DB.Session.SetSyncTimeout(d)
}

// Go runs the given function in a new goroutine,
// passing it a copy of s, which will be closed
// after the function returns.
func (s *Store) Go(f func(*Store)) {
	s = s.Copy()
	s.pool.run.Do(func() error {
		defer s.Close()
		f(s)
		return nil
	})
}

// Pool returns the pool that the store originally
// came from.
func (s *Store) Pool() *Pool {
	return s.pool
}

func (s *Store) ensureIndexes() error {
	indexes := []struct {
		c *mgo.Collection
		i mgo.Index
	}{{
		s.DB.StatCounters(),
		mgo.Index{Key: []string{"k", "t"}, Unique: true},
	}, {
		s.DB.StatTokens(),
		mgo.Index{Key: []string{"t"}, Unique: true},
	}, {
		s.DB.Entities(),
		mgo.Index{Key: []string{"baseurl"}},
	}, {
		s.DB.Entities(),
		mgo.Index{Key: []string{"uploadtime"}},
	}, {
		s.DB.Entities(),
		mgo.Index{Key: []string{"promulgated-url"}, Unique: true, Sparse: true},
	}, {
		s.DB.BaseEntities(),
		mgo.Index{Key: []string{"public"}},
	}, {
		s.DB.Logs(),
		mgo.Index{Key: []string{"urls"}},
	}, {
		s.DB.Entities(),
		mgo.Index{Key: []string{"user"}},
	}, {
		s.DB.Entities(),
		mgo.Index{Key: []string{"user", "name"}},
	}, {
		s.DB.Entities(),
		mgo.Index{Key: []string{"user", "name", "series"}},
	}, {
		s.DB.Entities(),
		mgo.Index{Key: []string{"series"}},
	}, {
		s.DB.Entities(),
		mgo.Index{Key: []string{"blobhash256"}},
	}, {
		s.DB.Entities(),
		mgo.Index{Key: []string{"_id", "name"}},
	}, {
		s.DB.Entities(),
		mgo.Index{Key: []string{"charmrequiredinterfaces"}},
	}, {
		s.DB.Entities(),
		mgo.Index{Key: []string{"charmprovidedinterfaces"}},
	}, {
		s.DB.Entities(),
		mgo.Index{Key: []string{"bundlecharms"}},
	}, {
		s.DB.BaseEntities(),
		mgo.Index{Key: []string{"name"}},
	}}
	for _, idx := range indexes {
		err := idx.c.EnsureIndex(idx.i)
		if err != nil {
			return errgo.Notef(err, "cannot ensure index with keys %v on collection %s", idx.i, idx.c.Name)
		}
	}
	return nil
}

func (s *Store) putArchive(archive blobstore.ReadSeekCloser) (blobName, blobHash, blobHash256 string, size int64, err error) {
	hash := blobstore.NewHash()
	hash256 := sha256.New()
	size, err = io.Copy(io.MultiWriter(hash, hash256), archive)
	if err != nil {
		return "", "", "", 0, errgo.Notef(err, "cannot copy archive")
	}
	if _, err = archive.Seek(0, 0); err != nil {
		return "", "", "", 0, errgo.Notef(err, "cannot seek in archive")
	}
	blobHash = fmt.Sprintf("%x", hash.Sum(nil))
	blobName = bson.NewObjectId().Hex()
	if err = s.BlobStore.PutUnchallenged(archive, blobName, size, blobHash); err != nil {
		return "", "", "", 0, errgo.Notef(err, "cannot put archive into blob store")
	}
	return blobName, blobHash, fmt.Sprintf("%x", hash256.Sum(nil)), size, nil
}

// AddCharmWithArchive is like AddCharm but
// also adds the charm archive to the blob store.
// This method is provided principally so that
// tests can easily create content in the store.
//
// If purl is not nil then the charm will also be
// available at the promulgated url specified.
func (s *Store) AddCharmWithArchive(url *router.ResolvedURL, ch charm.Charm) error {
	blobName, blobHash, blobHash256, blobSize, err := s.uploadCharmOrBundle(ch)
	if err != nil {
		return errgo.Notef(err, "cannot upload charm")
	}
	return s.AddCharm(ch, AddParams{
		URL:         url,
		BlobName:    blobName,
		BlobHash:    blobHash,
		BlobHash256: blobHash256,
		BlobSize:    blobSize,
		Development: url.Development,
	})
}

// AddBundleWithArchive is like AddBundle but
// also adds the charm archive to the blob store.
// This method is provided principally so that
// tests can easily create content in the store.
//
// If purl is not nil then the bundle will also be
// available at the promulgated url specified.
func (s *Store) AddBundleWithArchive(url *router.ResolvedURL, b charm.Bundle) error {
	blobName, blobHash, blobHash256, size, err := s.uploadCharmOrBundle(b)
	if err != nil {
		return errgo.Notef(err, "cannot upload bundle")
	}
	return s.AddBundle(b, AddParams{
		URL:         url,
		BlobName:    blobName,
		BlobHash:    blobHash,
		BlobHash256: blobHash256,
		BlobSize:    size,
		Development: url.Development,
	})
}

func (s *Store) uploadCharmOrBundle(c interface{}) (blobName, blobHash, blobHash256 string, size int64, err error) {
	archive, err := getArchive(c)
	if err != nil {
		return "", "", "", 0, errgo.Notef(err, "cannot get archive")
	}
	defer archive.Close()
	return s.putArchive(archive)
}

// AddAudit adds the given entry to the audit log.
func (s *Store) AddAudit(entry audit.Entry) {
	s.addAuditAtTime(entry, time.Now())
}

func (s *Store) addAuditAtTime(entry audit.Entry, t time.Time) {
	if s.pool.auditEncoder == nil {
		return
	}
	entry.Time = t
	err := s.pool.auditEncoder.Encode(entry)
	if err != nil {
		logger.Errorf("Cannot write audit log entry: %v", err)
	}
}

// AddParams holds parameters held in common between the
// Store.AddCharm and Store.AddBundle methods.
type AddParams struct {
	// URL holds the id to be associated with the stored entity.
	// If URL.PromulgatedRevision is not -1, the entity will
	// be promulgated.
	URL *router.ResolvedURL

	// BlobName holds the name of the entity's archive blob.
	BlobName string

	// BlobHash holds the hash of the entity's archive blob.
	BlobHash string

	// BlobHash256 holds the sha256 hash of the entity's archive blob.
	BlobHash256 string

	// BlobHash holds the size of the entity's archive blob.
	BlobSize int64

	// Contents holds references to files inside the
	// entity's archive blob.
	Contents map[mongodoc.FileId]mongodoc.ZipFile

	// Development holds whether the entity revision is in development.
	Development bool
}

// AddCharm adds a charm entities collection with the given parameters.
// If p.URL cannot be used as a name for the charm then the returned
// error will have the cause params.ErrEntityIdNotAllowed. If the charm
// duplicates an existing charm then the returned error will have the
// cause params.ErrDuplicateUpload.
func (s *Store) AddCharm(c charm.Charm, p AddParams) (err error) {
	// Strictly speaking this test is redundant, because a ResolvedURL should
	// always be canonical, but check just in case anyway, as this is
	// final gateway before a potentially invalid url might be stored
	// in the database.
	id := p.URL.URL
	if id.Series == "bundle" || id.User == "" || id.Revision == -1 {
		return errgo.Newf("charm added with invalid id %v", &id)
	}
	logger.Infof("add charm url %s; prev %d", &id, p.URL.PromulgatedRevision)
	entity := &mongodoc.Entity{
		URL:                     &id,
		PromulgatedURL:          p.URL.PromulgatedURL(),
		BlobHash:                p.BlobHash,
		BlobHash256:             p.BlobHash256,
		BlobName:                p.BlobName,
		Size:                    p.BlobSize,
		UploadTime:              time.Now(),
		CharmMeta:               c.Meta(),
		CharmConfig:             c.Config(),
		CharmActions:            c.Actions(),
		CharmProvidedInterfaces: interfacesForRelations(c.Meta().Provides),
		CharmRequiredInterfaces: interfacesForRelations(c.Meta().Requires),
		Contents:                p.Contents,
		SupportedSeries:         c.Meta().Series,
		Development:             p.Development,
	}
	denormalizeEntity(entity)

	// Check that we're not going to create a charm that duplicates
	// the name of a bundle. This is racy, but it's the best we can
	// do. Also check that there isn't an existing multi-series charm
	// that would be replaced by this one.
	entities, err := s.FindEntities(entity.BaseURL)
	if err != nil {
		return errgo.Notef(err, "cannot check for existing entities")
	}
	for _, entity := range entities {
		if entity.URL.Series == "bundle" {
			return errgo.WithCausef(err, params.ErrEntityIdNotAllowed, "charm name duplicates bundle name %v", entity.URL)
		}
		if id.Series != "" && entity.URL.Series == "" {
			return errgo.WithCausef(err, params.ErrEntityIdNotAllowed, "charm name duplicates multi-series charm name %v", entity.URL)
		}
	}
	if err := s.insertEntity(entity); err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrDuplicateUpload))
	}
	return nil
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
	e.BaseURL = baseURL(e.URL)
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

var everyonePerm = []string{params.Everyone}

func (s *Store) insertEntity(entity *mongodoc.Entity) (err error) {
	// Add the base entity to the database.
	perms := []string{entity.User}
	acls := mongodoc.ACL{
		Read:  perms,
		Write: perms,
	}
	baseEntity := &mongodoc.BaseEntity{
		URL:             entity.BaseURL,
		User:            entity.User,
		Name:            entity.Name,
		Public:          false,
		ACLs:            acls,
		DevelopmentACLs: acls,
		Promulgated:     entity.PromulgatedURL != nil,
	}
	err = s.DB.BaseEntities().Insert(baseEntity)
	if err != nil && !mgo.IsDup(err) {
		return errgo.Notef(err, "cannot insert base entity")
	}

	// Add the entity to the database.
	err = s.DB.Entities().Insert(entity)
	if mgo.IsDup(err) {
		return params.ErrDuplicateUpload
	}
	if err != nil {
		return errgo.Notef(err, "cannot insert entity")
	}
	// Ensure that if anything fails after this, that we delete
	// the entity, otherwise we will be left in an internally
	// inconsistent state.
	defer func() {
		if err != nil {
			if err := s.DB.Entities().RemoveId(entity.URL); err != nil {
				logger.Errorf("cannot remove entity after elastic search failure: %v", err)
			}
		}
	}()
	// Add entity to ElasticSearch.
	if err := s.UpdateSearch(EntityResolvedURL(entity)); err != nil {
		return errgo.Notef(err, "cannot index %s to ElasticSearch", entity.URL)
	}
	return nil
}

// FindEntity finds the entity in the store with the given URL,
// which must be fully qualified. If any fields are specified,
// only those fields will be populated in the returned entities.
// If the given URL has no user then it is assumed to be a
// promulgated entity.
func (s *Store) FindEntity(url *router.ResolvedURL, fields ...string) (*mongodoc.Entity, error) {
	entities, err := s.FindEntities(url.UserOwnedURL(), fields...)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	if len(entities) == 0 {
		return nil, errgo.WithCausef(nil, params.ErrNotFound, "entity not found")
	}
	// The URL is guaranteed to be fully qualified so we'll always
	// get exactly one result.
	return entities[0], nil
}

// FindEntities finds all entities in the store matching the given URL.
// If any fields are specified, only those fields will be
// populated in the returned entities. If the given URL has no user then
// only promulgated entities will be queried. If the given URL channel does
// not represent an entity under development then only published entities
// will be queried.
func (s *Store) FindEntities(url *charm.URL, fields ...string) ([]*mongodoc.Entity, error) {
	query := selectFields(s.EntitiesQuery(url), fields)
	var docs []*mongodoc.Entity
	err := query.All(&docs)
	if err != nil {
		return nil, errgo.Notef(err, "cannot find entities matching %s", url)
	}
	return docs, nil
}

// FindBestEntity finds the entity that provides the preferred match to
// the given URL. If any fields are specified, only those fields will be
// populated in the returned entities. If the given URL has no user then
// only promulgated entities will be queried.
func (s *Store) FindBestEntity(url *charm.URL, fields ...string) (*mongodoc.Entity, error) {
	if len(fields) > 0 {
		// Make sure we have all the fields we need to make a decision.
		fields = append(fields, "_id", "promulgated-url", "promulgated-revision", "series", "revision")
	}
	entities, err := s.FindEntities(url, fields...)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	if len(entities) == 0 {
		return nil, errgo.WithCausef(nil, params.ErrNotFound, "entity not found")
	}
	best := entities[0]
	for _, e := range entities {
		if seriesScore[e.Series] > seriesScore[best.Series] {
			best = e
			continue
		}
		if seriesScore[e.Series] < seriesScore[best.Series] {
			continue
		}
		if url.User == "" {
			if e.PromulgatedRevision > best.PromulgatedRevision {
				best = e
				continue
			}
		} else {
			if e.Revision > best.Revision {
				best = e
				continue
			}
		}
	}
	return best, nil
}

var seriesScore = map[string]int{
	"bundle":  -1,
	"lucid":   1000,
	"precise": 1001,
	"trusty":  1002,
	"quantal": 1,
	"raring":  2,
	"saucy":   3,
	"utopic":  4,
	"vivid":   5,
	"wily":    6,
	// When we find a multi-series charm (no series) we
	// will always choose it in preference to a series-specific
	// charm
	"": 5000,
}

var seriesBundleOrEmpty = bson.D{{"$or", []bson.D{{{"series", "bundle"}}, {{"series", ""}}}}}

// EntitiesQuery creates a mgo.Query object that can be used to find
// entities matching the given URL. If the given URL has no user then
// the produced query will only match promulgated entities. If the given URL
// channel is not "development" then the produced query will only match
// published entities.
func (s *Store) EntitiesQuery(url *charm.URL) *mgo.Query {
	entities := s.DB.Entities()
	query := make(bson.D, 1, 5)
	query[0] = bson.DocElem{"name", url.Name}
	if url.Channel != charm.DevelopmentChannel {
		query = append(query, bson.DocElem{"development", false})
	}
	if url.User == "" {
		if url.Revision > -1 {
			query = append(query, bson.DocElem{"promulgated-revision", url.Revision})
		} else {
			query = append(query, bson.DocElem{"promulgated-revision", bson.D{{"$gt", -1}}})
		}
	} else {
		query = append(query, bson.DocElem{"user", url.User})
		if url.Revision > -1 {
			query = append(query, bson.DocElem{"revision", url.Revision})
		}
	}
	if url.Series == "" {
		if url.Revision > -1 {
			// If we're specifying a revision we must be searching
			// for a canonical URL, so search for a multi-series
			// charm or a bundle.
			query = append(query, seriesBundleOrEmpty...)
		}
	} else if url.Series == "bundle" {
		query = append(query, bson.DocElem{"series", "bundle"})
	} else {
		query = append(query, bson.DocElem{"supportedseries", url.Series})
	}
	return entities.Find(query)
}

// FindBaseEntity finds the base entity in the store using the given URL,
// which can either represent a fully qualified entity or a base id.
// If any fields are specified, only those fields will be populated in the
// returned base entity.
func (s *Store) FindBaseEntity(url *charm.URL, fields ...string) (*mongodoc.BaseEntity, error) {
	var query *mgo.Query
	if url.User == "" {
		query = s.DB.BaseEntities().Find(bson.D{{"name", url.Name}, {"promulgated", 1}})
	} else {
		query = s.DB.BaseEntities().FindId(baseURL(url))
	}
	query = selectFields(query, fields)
	var baseEntity mongodoc.BaseEntity
	if err := query.One(&baseEntity); err != nil {
		if err == mgo.ErrNotFound {
			return nil, errgo.WithCausef(nil, params.ErrNotFound, "base entity not found")
		}
		return nil, errgo.Notef(err, "cannot find base entity %v", url)
	}
	return &baseEntity, nil
}

func selectFields(query *mgo.Query, fields []string) *mgo.Query {
	if len(fields) > 0 {
		sel := make(bson.D, len(fields))
		for i, field := range fields {
			sel[i] = bson.DocElem{field, 1}
		}
		query = query.Select(sel)
	}
	return query
}

// UpdateEntity applies the provided update to the entity described by url.
func (s *Store) UpdateEntity(url *router.ResolvedURL, update interface{}) error {
	if err := s.DB.Entities().Update(bson.D{{"_id", &url.URL}}, update); err != nil {
		if err == mgo.ErrNotFound {
			return errgo.WithCausef(err, params.ErrNotFound, "cannot update %q", url)
		}
		return errgo.Notef(err, "cannot update %q", url)
	}
	return nil
}

// UpdateBaseEntity applies the provided update to the base entity of url.
func (s *Store) UpdateBaseEntity(url *router.ResolvedURL, update interface{}) error {
	if err := s.DB.BaseEntities().Update(bson.D{{"_id", baseURL(&url.URL)}}, update); err != nil {
		if err == mgo.ErrNotFound {
			return errgo.WithCausef(err, params.ErrNotFound, "cannot update base entity for %q", url)
		}
		return errgo.Notef(err, "cannot update base entity for %q", url)
	}
	return nil
}

// SetDevelopment sets whether the entity corresponding to the given URL will
// be only available in its development version (in essence, not published).
func (s *Store) SetDevelopment(url *router.ResolvedURL, development bool) error {
	if err := s.UpdateEntity(url, bson.D{{
		"$set", bson.D{{"development", development}},
	}}); err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}
	if !development {
		// If the entity is published, update the search index.
		rurl := *url
		rurl.Development = development
		if err := s.UpdateSearch(&rurl); err != nil {
			return errgo.Notef(err, "cannot update search entities for %q", rurl)
		}
	}
	return nil
}

// SetPromulgated sets whether the base entity of url is promulgated, If
// promulgated is true it also unsets promulgated on any other base
// entity for entities with the same name. It also calculates the next
// promulgated URL for the entities owned by the new owner and sets those
// entities appropriately.
//
// Note: This code is known to have some unfortunate (but not dangerous)
// race conditions. It is possible that if one or more promulgations
// happens concurrently for the same entity name then it could result in
// more than one base entity being promulgated. If this happens then
// uploads to either user will get promulgated names, these names will
// never clash. This situation is easily remedied by setting the
// promulgated user for this charm again, even to one of the ones that is
// already promulgated. It can also result in the latest promulgated
// revision of the charm not being one created by the promulgated user.
// This will be remedied when a new charm is uploaded by the promulgated
// user. As promulgation is a rare operation, it is considered that the
// chances this will happen are slim.
func (s *Store) SetPromulgated(url *router.ResolvedURL, promulgate bool) error {
	baseEntities := s.DB.BaseEntities()
	base := baseURL(&url.URL)
	if !promulgate {
		err := baseEntities.UpdateId(
			base,
			bson.D{{"$set", bson.D{{"promulgated", mongodoc.IntBool(false)}}}},
		)
		if err != nil {
			if errgo.Cause(err) == mgo.ErrNotFound {
				return errgo.WithCausef(nil, params.ErrNotFound, "base entity %q not found", base)
			}
			return errgo.Notef(err, "cannot unpromulgate base entity %q", base)
		}
		if err := s.UpdateSearchBaseURL(base); err != nil {
			return errgo.Notef(err, "cannot update search entities for %q", base)
		}
		return nil
	}

	// Find any currently promulgated base entities for this charm name.
	// Under normal circumstances there should be a maximum of one of these,
	// but we should attempt to recover if there is an error condition.
	iter := baseEntities.Find(
		bson.D{
			{"_id", bson.D{{"$ne", base}}},
			{"name", base.Name},
			{"promulgated", mongodoc.IntBool(true)},
		},
	).Iter()
	defer iter.Close()
	var baseEntity mongodoc.BaseEntity
	for iter.Next(&baseEntity) {
		err := baseEntities.UpdateId(
			baseEntity.URL,
			bson.D{{"$set", bson.D{{"promulgated", mongodoc.IntBool(false)}}}},
		)
		if err != nil {
			return errgo.Notef(err, "cannot unpromulgate base entity %q", baseEntity.URL)
		}
		if err := s.UpdateSearchBaseURL(baseEntity.URL); err != nil {
			return errgo.Notef(err, "cannot update search entities for %q", baseEntity.URL)
		}
	}
	if err := iter.Close(); err != nil {
		return errgo.Notef(err, "cannot close mgo iterator")
	}

	// Set the promulgated flag on the base entity.
	err := s.DB.BaseEntities().UpdateId(base, bson.D{{"$set", bson.D{{"promulgated", mongodoc.IntBool(true)}}}})
	if err != nil {
		if errgo.Cause(err) == mgo.ErrNotFound {
			return errgo.WithCausef(nil, params.ErrNotFound, "base entity %q not found", base)
		}
		return errgo.Notef(err, "cannot promulgate base entity %q", base)
	}

	type result struct {
		Series   string `bson:"_id"`
		Revision int
	}

	// Find the latest revision in each series of entities with the promulgated base URL.
	var latestOwned []result
	err = s.DB.Entities().Pipe([]bson.D{
		{{"$match", bson.D{{"baseurl", base}}}},
		{{"$group", bson.D{{"_id", "$series"}, {"revision", bson.D{{"$max", "$revision"}}}}}},
	}).All(&latestOwned)
	if err != nil {
		return errgo.Notef(err, "cannot find latest revision for promulgated URL")
	}

	// Find the latest revision in each series of the promulgated entitities
	// with the same name as the base entity. Note that this works because:
	//     1) promulgated URLs always have the same charm name as their
	//     non-promulgated counterparts.
	//     2) bundles cannot have names that overlap with charms.
	// Because of 1), we are sure that selecting on the entity name will
	// select all entities with a matching promulgated URL name. Because of
	// 2) we are sure that we are only updating all charms or the single
	// bundle entity.
	latestPromulgated := make(map[string]int)
	iter = s.DB.Entities().Pipe([]bson.D{
		{{"$match", bson.D{{"name", base.Name}}}},
		{{"$group", bson.D{{"_id", "$series"}, {"revision", bson.D{{"$max", "$promulgated-revision"}}}}}},
	}).Iter()
	var res result
	for iter.Next(&res) {
		latestPromulgated[res.Series] = res.Revision
	}
	if err := iter.Close(); err != nil {
		return errgo.Notef(err, "cannot close mgo iterator")
	}

	// Update the newest entity in each series with a base URL that matches the newly promulgated
	// base entity to have a promulgated URL, if it does not already have one.
	for _, r := range latestOwned {
		id := *base
		id.Series = r.Series
		id.Revision = r.Revision
		pID := id
		pID.User = ""
		pID.Revision = latestPromulgated[r.Series] + 1
		err := s.DB.Entities().Update(
			bson.D{
				{"_id", &id},
				{"promulgated-revision", -1},
			},
			bson.D{
				{"$set", bson.D{
					{"promulgated-url", &pID},
					{"promulgated-revision", pID.Revision},
				}},
			},
		)
		if err != nil && err != mgo.ErrNotFound {
			// If we get NotFound it is most likely because the latest owned revision is
			// already promulgated, so carry on.
			return errgo.Notef(err, "cannot update promulgated URLs")
		}
	}

	// Update the search record for the newest entity.
	if err := s.UpdateSearchBaseURL(base); err != nil {
		return errgo.Notef(err, "cannot update search entities for %q", base)
	}
	return nil
}

func interfacesForRelations(rels map[string]charm.Relation) []string {
	// Eliminate duplicates by storing interface names into a map.
	interfaces := make(map[string]bool)
	for _, rel := range rels {
		interfaces[rel.Interface] = true
	}
	result := make([]string, 0, len(interfaces))
	for iface := range interfaces {
		result = append(result, iface)
	}
	return result
}

func baseURL(url *charm.URL) *charm.URL {
	newURL := *url
	newURL.Revision = -1
	newURL.Series = ""
	newURL.Channel = ""
	return &newURL
}

var errNotImplemented = errgo.Newf("not implemented")

// AddBundle adds a bundle to the entities collection with the given
// parameters. If p.URL cannot be used as a name for the bundle then the
// returned error will have the cause params.ErrEntityIdNotAllowed. If
// the bundle duplicates an existing bundle then the returned error will
// have the cause params.ErrDuplicateUpload.
func (s *Store) AddBundle(b charm.Bundle, p AddParams) error {
	// Strictly speaking this test is redundant, because a ResolvedURL should
	// always be canonical, but check just in case anyway, as this is
	// final gateway before a potentially invalid url might be stored
	// in the database.
	if p.URL.URL.Series != "bundle" || p.URL.URL.User == "" || p.URL.URL.Revision == -1 || p.URL.URL.Series == "" {
		return errgo.Newf("bundle added with invalid id %v", p.URL)
	}
	bundleData := b.Data()
	urls, err := bundleCharms(bundleData)
	if err != nil {
		return errgo.Mask(err)
	}
	entity := &mongodoc.Entity{
		URL:                &p.URL.URL,
		BlobHash:           p.BlobHash,
		BlobHash256:        p.BlobHash256,
		BlobName:           p.BlobName,
		Size:               p.BlobSize,
		UploadTime:         time.Now(),
		BundleData:         bundleData,
		BundleUnitCount:    newInt(bundleUnitCount(bundleData)),
		BundleMachineCount: newInt(bundleMachineCount(bundleData)),
		BundleReadMe:       b.ReadMe(),
		BundleCharms:       urls,
		Contents:           p.Contents,
		PromulgatedURL:     p.URL.PromulgatedURL(),
		Development:        p.Development,
	}
	denormalizeEntity(entity)

	// Check that we're not going to create a bundle that duplicates
	// the name of a charm. This is racy, but it's the best we can do.
	entities, err := s.FindEntities(entity.BaseURL)
	if err != nil {
		return errgo.Notef(err, "cannot check for existing entities")
	}
	for _, entity := range entities {
		if entity.URL.Series != "bundle" {
			return errgo.WithCausef(err, params.ErrEntityIdNotAllowed, "bundle name duplicates charm name %s", entity.URL)
		}
	}
	if err := s.insertEntity(entity); err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrDuplicateUpload))
	}
	return nil
}

// OpenBlob opens a blob given its entity id; it returns the blob's
// data source, its size and its hash. It returns a params.ErrNotFound
// error if the entity does not exist.
func (s *Store) OpenBlob(id *router.ResolvedURL) (r blobstore.ReadSeekCloser, size int64, hash string, err error) {
	blobName, hash, err := s.BlobNameAndHash(id)
	if err != nil {
		return nil, 0, "", errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}
	r, size, err = s.BlobStore.Open(blobName)
	if err != nil {
		return nil, 0, "", errgo.Notef(err, "cannot open archive data for %s", id)
	}
	return r, size, hash, nil
}

// BlobNameAndHash returns the name that is used to store the blob
// for the entity with the given id and its hash. It returns a params.ErrNotFound
// error if the entity does not exist.
func (s *Store) BlobNameAndHash(id *router.ResolvedURL) (name, hash string, err error) {
	entity, err := s.FindEntity(id, "blobname", "blobhash")
	if err != nil {
		if errgo.Cause(err) == params.ErrNotFound {
			return "", "", errgo.WithCausef(nil, params.ErrNotFound, "entity not found")
		}
		return "", "", errgo.Notef(err, "cannot get %s", id)
	}
	return entity.BlobName, entity.BlobHash, nil
}

// OpenCachedBlobFile opens a file from the given entity's archive blob.
// The file is identified by the provided fileId. If the file has not
// previously been opened on this entity, the isFile function will be
// used to determine which file in the zip file to use. The result will
// be cached for the next time.
//
// When retrieving the entity, at least the BlobName and
// Contents fields must be populated.
func (s *Store) OpenCachedBlobFile(
	entity *mongodoc.Entity,
	fileId mongodoc.FileId,
	isFile func(f *zip.File) bool,
) (_ io.ReadCloser, err error) {
	if entity.BlobName == "" {
		// We'd like to check that the Contents field was populated
		// here but we can't because it doesn't necessarily
		// exist in the entity.
		return nil, errgo.New("provided entity does not have required fields")
	}
	zipf, ok := entity.Contents[fileId]
	if ok && !zipf.IsValid() {
		return nil, errgo.WithCausef(nil, params.ErrNotFound, "")
	}
	blob, size, err := s.BlobStore.Open(entity.BlobName)
	if err != nil {
		return nil, errgo.Notef(err, "cannot open archive blob")
	}
	defer func() {
		// When there's an error, we want to close
		// the blob, otherwise we need to keep the blob
		// open because it's used by the returned Reader.
		if err != nil {
			blob.Close()
		}
	}()
	if !ok {
		// We haven't already searched the archive for the icon,
		// so find its archive now.
		zipf, err = s.findZipFile(blob, size, isFile)
		if err != nil && errgo.Cause(err) != params.ErrNotFound {
			return nil, errgo.Mask(err)
		}
	}
	// We update the content entry regardless of whether we've
	// found a file, so that the next time that serveIcon is called
	// it can know that we've already looked.
	err = s.DB.Entities().UpdateId(
		entity.URL,
		bson.D{{"$set",
			bson.D{{"contents." + string(fileId), zipf}},
		}},
	)
	if err != nil {
		return nil, errgo.Notef(err, "cannot update %q", entity.URL)
	}
	if !zipf.IsValid() {
		// We searched for the file and didn't find it.
		return nil, errgo.WithCausef(nil, params.ErrNotFound, "")
	}

	// We know where the icon is stored. Now serve it up.
	r, err := ZipFileReader(blob, zipf)
	if err != nil {
		return nil, errgo.Notef(err, "cannot make zip file reader")
	}
	// We return a ReadCloser that reads from the newly created
	// zip file reader, but when closed, will close the originally
	// opened blob.
	return struct {
		io.Reader
		io.Closer
	}{r, blob}, nil
}

func (s *Store) findZipFile(blob io.ReadSeeker, size int64, isFile func(f *zip.File) bool) (mongodoc.ZipFile, error) {
	zipReader, err := zip.NewReader(&readerAtSeeker{blob}, size)
	if err != nil {
		return mongodoc.ZipFile{}, errgo.Notef(err, "cannot read archive data")
	}
	for _, f := range zipReader.File {
		if isFile(f) {
			return NewZipFile(f)
		}
	}
	return mongodoc.ZipFile{}, params.ErrNotFound
}

// SetPerms sets the permissions for the base entity with
// the given id for "which" operations ("read" or "write")
// to the given ACL. This is mostly provided for testing.
func (s *Store) SetPerms(id *charm.URL, which string, acl ...string) error {
	field := "acls"
	if id.Channel == charm.DevelopmentChannel {
		field = "developmentacls"
	}
	return s.DB.BaseEntities().UpdateId(baseURL(id), bson.D{{"$set",
		bson.D{{field + "." + which, acl}},
	}})
}

func newInt(x int) *int {
	return &x
}

// bundleUnitCount returns the number of units created by the bundle.
func bundleUnitCount(b *charm.BundleData) int {
	count := 0
	for _, service := range b.Services {
		count += service.NumUnits
	}
	return count
}

// bundleMachineCount returns the number of machines
// that will be created or used by the bundle.
func bundleMachineCount(b *charm.BundleData) int {
	count := len(b.Machines)
	for _, service := range b.Services {
		// The default placement is "new".
		placement := &charm.UnitPlacement{
			Machine: "new",
		}
		// Check for "new" placements, which means a new machine
		// must be added.
		for _, location := range service.To {
			var err error
			placement, err = charm.ParsePlacement(location)
			if err != nil {
				// Ignore invalid placements - a bundle should always
				// be verified before adding to the charm store so this
				// should never happen in practice.
				continue
			}
			if placement.Machine == "new" {
				count++
			}
		}
		// If there are less elements in To than NumUnits, the last placement
		// element is replicated. For this reason, if the last element is
		// "new", we need to add more machines.
		if placement != nil && placement.Machine == "new" {
			count += service.NumUnits - len(service.To)
		}
	}
	return count
}

// bundleCharms returns all the charm URLs used by a bundle,
// without duplicates.
func bundleCharms(data *charm.BundleData) ([]*charm.URL, error) {
	// Use a map to de-duplicate the URL list: a bundle can include services
	// deployed by the same charm.
	urlMap := make(map[string]*charm.URL)
	for _, service := range data.Services {
		url, err := charm.ParseURL(service.Charm)
		if err != nil {
			return nil, errgo.Mask(err)
		}
		urlMap[url.String()] = url
		// Also add the corresponding base URL.
		base := baseURL(url)
		urlMap[base.String()] = base
	}
	urls := make([]*charm.URL, 0, len(urlMap))
	for _, url := range urlMap {
		urls = append(urls, url)
	}
	return urls, nil
}

// AddLog adds a log message to the database.
func (s *Store) AddLog(data *json.RawMessage, logLevel mongodoc.LogLevel, logType mongodoc.LogType, urls []*charm.URL) error {
	// Encode the JSON data.
	b, err := json.Marshal(data)
	if err != nil {
		return errgo.Notef(err, "cannot marshal log data")
	}

	// Add the base URLs to the list of references associated with the log.
	// Also remove duplicate URLs while maintaining the references' order.
	var allUrls []*charm.URL
	urlMap := make(map[string]bool)
	for _, url := range urls {
		urlStr := url.String()
		if ok, _ := urlMap[urlStr]; !ok {
			urlMap[urlStr] = true
			allUrls = append(allUrls, url)
		}
		base := baseURL(url)
		urlStr = base.String()
		if ok, _ := urlMap[urlStr]; !ok {
			urlMap[urlStr] = true
			allUrls = append(allUrls, base)
		}
	}

	// Add the log to the database.
	log := &mongodoc.Log{
		Data:  b,
		Level: logLevel,
		Type:  logType,
		URLs:  allUrls,
		Time:  time.Now(),
	}
	if err := s.DB.Logs().Insert(log); err != nil {
		return errgo.Mask(err)
	}
	return nil
}

// StoreDatabase wraps an mgo.DB ands adds a few convenience methods.
type StoreDatabase struct {
	*mgo.Database
}

// clone copies the StoreDatabase, cloning the underlying mgo session.
func (s StoreDatabase) clone() StoreDatabase {
	return StoreDatabase{
		&mgo.Database{
			Name:    s.Name,
			Session: s.Session.Clone(),
		},
	}
}

// copy copies the StoreDatabase, copying the underlying mgo session.
func (s StoreDatabase) copy() StoreDatabase {
	return StoreDatabase{
		&mgo.Database{
			Name:    s.Name,
			Session: s.Session.Copy(),
		},
	}
}

// Close closes the store database's underlying session.
func (s StoreDatabase) Close() {
	s.Session.Close()
}

// Entities returns the mongo collection where entities are stored.
func (s StoreDatabase) Entities() *mgo.Collection {
	return s.C("entities")
}

// BaseEntities returns the mongo collection where base entities are stored.
func (s StoreDatabase) BaseEntities() *mgo.Collection {
	return s.C("base_entities")
}

// Logs returns the Mongo collection where charm store logs are stored.
func (s StoreDatabase) Logs() *mgo.Collection {
	return s.C("logs")
}

// Migrations returns the Mongo collection where the migration info is stored.
func (s StoreDatabase) Migrations() *mgo.Collection {
	return s.C("migrations")
}

func (s StoreDatabase) Macaroons() *mgo.Collection {
	return s.C("macaroons")
}

// allCollections holds for each collection used by the charm store a
// function returns that collection.
// The macaroons collection is omitted because it does
// not exist until a macaroon is actually created.
var allCollections = []func(StoreDatabase) *mgo.Collection{
	StoreDatabase.StatCounters,
	StoreDatabase.StatTokens,
	StoreDatabase.Entities,
	StoreDatabase.BaseEntities,
	StoreDatabase.Logs,
	StoreDatabase.Migrations,
}

// Collections returns a slice of all the collections used
// by the charm store.
func (s StoreDatabase) Collections() []*mgo.Collection {
	cs := make([]*mgo.Collection, len(allCollections))
	for i, f := range allCollections {
		cs[i] = f(s)
	}
	return cs
}

type readerAtSeeker struct {
	r io.ReadSeeker
}

func (r *readerAtSeeker) ReadAt(buf []byte, p int64) (int, error) {
	if _, err := r.r.Seek(p, 0); err != nil {
		return 0, errgo.Notef(err, "cannot seek")
	}
	return r.r.Read(buf)
}

// ReaderAtSeeker adapts r so that it can be used as
// a ReaderAt. Note that, unlike some implementations
// of ReaderAt, it is not OK to use concurrently.
func ReaderAtSeeker(r io.ReadSeeker) io.ReaderAt {
	return &readerAtSeeker{r}
}

// Search searches the store for the given SearchParams.
// It returns a SearchResult containing the results of the search.
func (store *Store) Search(sp SearchParams) (SearchResult, error) {
	result, err := store.ES.search(sp)
	if err != nil {
		return SearchResult{}, errgo.Mask(err)
	}
	return result, nil
}

var listFilters = map[string]string{
	"name":        "name",
	"owner":       "user",
	"series":      "serties",
	"type":        "type",
	"promulgated": "promulgated-revision",
}

func prepareList(sp SearchParams) (filters map[string]interface{}, sort []string, err error) {
	filters = make(map[string]interface{})
	for k, v := range sp.Filters {
		switch k {
		case "name":
			filters[k] = v[0]
		case "owner":
			filters["user"] = v[0]
		case "series":
			filters["series"] = v[0]
		case "type":
			if v[0] == "bundle" {
				filters["series"] = "bundle"
			} else {
				filters["series"] = map[string]interface{}{"$ne": "bundle"}
			}
		case "promulgated":
			if v[0] != "0" {
				filters["promulgated-revision"] = map[string]interface{}{"$gt": 0}
			} else {
				filters["promulgated-revision"] = map[string]interface{}{"$lt": 0}
			}
		default:
			return nil, nil, errgo.Newf("filter %q not allowed", k)
		}
	}

	sort, err = createMongoSort(sp)
	if err != nil {
		return nil, nil, errgo.Newf("invalid parameters: %s", err)
	}
	return filters, sort, nil
}

// sortFields contains a mapping from api fieldnames to the entity fields to search.
var sortMongoFields = map[string]string{
	"name":   "name",
	"owner":  "user",
	"series": "series",
}

// createSort creates a sort query parameters for mongo out of a Sort parameter.
func createMongoSort(sp SearchParams) ([]string, error) {
	sort := make([]string, len(sp.sort))
	for i, s := range sp.sort {
		field := sortMongoFields[s.Field]
		sort[i] = field
		if field == "" {
			return nil, errgo.Newf("sort %q not allowed", s.Field)
		}
		if s.Order == sortDescending {
			sort[i] = "-" + field
		}
	}
	return sort, nil
}

// List lists the store for the given ListParams.
// It returns a ListResult containing the results of the list.
func (store *Store) List(sp SearchParams) (ListResult, error) {
	filters, sort, err := prepareList(sp)
	if err != nil {
		return ListResult{}, errgo.Mask(err)
	}
	query := store.DB.Entities().Find(filters)
	if len(sort) == 0 {
		query = query.Sort("_id")
	} else {
		query = query.Sort(sort...)
	}

	//Only select needed field
	query = query.Select(bson.D{{"_id", 1}, {"url", 1}, {"development", 1}, {"promulgated-url", 1}})

	r := ListResult{
		Results: make([]*router.ResolvedURL, 0),
	}
	var entity mongodoc.Entity
	iter := query.Iter()
	for iter.Next(&entity) {
		r.Results = append(r.Results, EntityResolvedURL(&entity))
	}
	if err := iter.Close(); err != nil {
		return ListResult{}, errgo.Mask(err)
	}
	return r, nil
}

// SynchroniseElasticsearch creates new indexes in elasticsearch
// and populates them with the current data from the mongodb database.
func (s *Store) SynchroniseElasticsearch() error {
	if err := s.ES.ensureIndexes(true); err != nil {
		return errgo.Notef(err, "cannot create indexes")
	}
	if err := s.syncSearch(); err != nil {
		return errgo.Notef(err, "cannot synchronise indexes")
	}
	return nil
}

// EntityResolvedURL returns the ResolvedURL for the entity.
// It requires PromulgatedURL and Development fields to have been
// filled out in the entity.
func EntityResolvedURL(e *mongodoc.Entity) *router.ResolvedURL {
	rurl := &router.ResolvedURL{
		URL:                 *e.URL,
		PromulgatedRevision: -1,
		Development:         e.Development,
	}
	if e.PromulgatedURL != nil {
		rurl.PromulgatedRevision = e.PromulgatedURL.Revision
	}
	return rurl
}
