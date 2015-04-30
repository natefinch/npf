// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/juju/loggo"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v0/csclient/params"
	"gopkg.in/macaroon-bakery.v0/bakery"
	"gopkg.in/macaroon-bakery.v0/bakery/mgostorage"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"gopkg.in/juju/charmstore.v5-unstable/internal/blobstore"
	"gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"
	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
)

var logger = loggo.GetLogger("charmstore.internal.charmstore")

// Pool holds a connection to the underlying charm and blob
// data stores. Calling its Store method returns a new Store
// from the pool that can be used to process short-lived requests
// to access and modify the store.
type Pool struct {
	db        StoreDatabase
	blobStore *blobstore.Store
	es        *SearchIndex
	Bakery    *bakery.Service
	stats     stats
}

// NewPool returns a Pool that uses the given database
// and search index. If bakeryParams is not nil,
// the Bakery field in the resulting Store will be set
// to a new Service that stores macaroons in mongo.
func NewPool(db *mgo.Database, si *SearchIndex, bakeryParams *bakery.NewServiceParams) (*Pool, error) {
	p := &Pool{
		db:        StoreDatabase{db},
		blobStore: blobstore.New(db, "entitystore"),
		es:        si,
	}
	store := p.Store()
	defer store.Close()
	if err := store.ensureIndexes(); err != nil {
		return nil, errgo.Notef(err, "cannot ensure indexes")
	}
	if err := store.ES.ensureIndexes(false); err != nil {
		return nil, errgo.Notef(err, "cannot ensure elasticsearch indexes")
	}
	if bakeryParams != nil {
		// NB we use the pool database here because its lifetime
		// is indefinite.
		macStore, err := mgostorage.New(p.db.Macaroons())
		if err != nil {
			return nil, errgo.Notef(err, "cannot create macaroon store")
		}
		bp := *bakeryParams
		bp.Store = macStore
		bsvc, err := bakery.NewService(bp)
		if err != nil {
			return nil, errgo.Notef(err, "cannot make bakery service")
		}
		p.Bakery = bsvc
	}
	return p, nil
}

// Store returns a Store that can be used to access the data base.
//
// It must be closed (with the Close method) after use.
func (p *Pool) Store() *Store {
	s := &Store{
		DB:        p.db.Copy(),
		BlobStore: p.blobStore,
		ES:        p.es,
		Bakery:    p.Bakery,
		stats:     &p.stats,
		pool:      p,
	}
	logger.Tracef("pool %p -> copy %p", p.db.Session, s.DB.Session)
	return s
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
	closed    bool
}

// Copy returns a new store with a lifetime
// independent of s. Use this method if you
// need to use a store in an independent goroutine.
//
// It must be closed (with the Close method) after use.
func (s *Store) Copy() *Store {
	s1 := *s
	s1.DB = s.DB.Copy()
	logger.Tracef("store %p -> copy %p", s.DB.Session, s1.DB.Session)
	return &s1
}

// Close closes the store instance.
func (s *Store) Close() {
	logger.Tracef("store %p closed", s.DB.Session)
	if s.closed {
		logger.Errorf("session closed twice")
		return
	}
	s.DB.Close()
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
	go func() {
		defer s.Close()
		f(s)
	}()
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
	})
}

// AddBundleWithArchive is like AddBundle but
// also adds the charm archive to the blob store.
// This method is provided principally so that
// tests can easily create content in the store.
//
// If purl is not nil then the bundle will also be
// available at the promulgated url specified.
//
// TODO This could take a *router.ResolvedURL as an argument
// instead of two *charm.References.
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
}

// AddCharm adds a charm entities collection with the given
// parameters.
func (s *Store) AddCharm(c charm.Charm, p AddParams) (err error) {
	// Strictly speaking this test is redundant, because a ResolvedURL should
	// always be canonical, but check just in case anyway, as this is
	// final gateway before a potentially invalid url might be stored
	// in the database.
	if p.URL.URL.Series == "bundle" || p.URL.URL.User == "" || p.URL.URL.Revision == -1 || p.URL.URL.Series == "" {
		return errgo.Newf("charm added with invalid id %v", &p.URL.URL)
	}
	logger.Infof("add charm url %s; prev %d", &p.URL.URL, p.URL.PromulgatedRevision)
	entity := &mongodoc.Entity{
		URL:                     &p.URL.URL,
		BaseURL:                 baseURL(&p.URL.URL),
		User:                    p.URL.URL.User,
		Name:                    p.URL.URL.Name,
		Revision:                p.URL.URL.Revision,
		Series:                  p.URL.URL.Series,
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
		PromulgatedURL:          p.URL.PromulgatedURL(),
		PromulgatedRevision:     p.URL.PromulgatedRevision,
	}

	// Check that we're not going to create a charm that duplicates
	// the name of a bundle. This is racy, but it's the best we can do.
	entities, err := s.FindEntities(baseURL(&p.URL.URL))
	if err != nil {
		return errgo.Notef(err, "cannot check for existing entities")
	}
	for _, entity := range entities {
		if entity.URL.Series == "bundle" {
			return errgo.Newf("charm name duplicates bundle name %v", entity.URL)
		}
	}
	if err := s.insertEntity(entity); err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrDuplicateUpload))
	}
	return nil
}

var everyonePerm = []string{params.Everyone}

func (s *Store) insertEntity(entity *mongodoc.Entity) (err error) {
	// Add the base entity to the database.
	perms := []string{entity.User}
	baseEntity := &mongodoc.BaseEntity{
		URL:    entity.BaseURL,
		User:   entity.User,
		Name:   entity.Name,
		Public: false,
		ACLs: mongodoc.ACL{
			Read:  perms,
			Write: perms,
		},
		Promulgated: entity.PromulgatedURL != nil,
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
	entities, err := s.FindEntities(&url.URL, fields...)
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
// only promulgated entities will be queried.
func (s *Store) FindEntities(url *charm.Reference, fields ...string) ([]*mongodoc.Entity, error) {
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
func (s *Store) FindBestEntity(url *charm.Reference, fields ...string) (*mongodoc.Entity, error) {
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
}

// EntitiesQuery creates a mgo.Query object that can be used to find
// entities matching the given URL. If the given URL has no user then
// the produced query will only match promulgated entities.
func (s *Store) EntitiesQuery(url *charm.Reference) *mgo.Query {
	if url.User != "" && url.Series != "" && url.Revision != -1 {
		// Find a specific owned entity, for instance ~who/utopic/django-42.
		return s.DB.Entities().FindId(url)
	}
	if url.Series != "" && url.Revision != -1 {
		// Find a specific promulgated entity, for instance utopic/django-42.
		return s.DB.Entities().Find(bson.D{{"promulgated-url", url}})
	}
	// Find all entities matching the URL.
	q := make(bson.D, 0, 3)
	q = append(q, bson.DocElem{"name", url.Name})
	if url.User != "" {
		q = append(q, bson.DocElem{"user", url.User})
	} else {
		// If the URL user is empty, only search the promulgated entities.
		q = append(q, bson.DocElem{"promulgated-url", bson.D{{"$exists", true}}})
	}
	if url.Series != "" {
		q = append(q, bson.DocElem{"series", url.Series})
	}
	if url.Revision != -1 {
		if url.User != "" {
			q = append(q, bson.DocElem{"revision", url.Revision})
		} else {
			q = append(q, bson.DocElem{"promulgated-revision", url.Revision})
		}
	}
	return s.DB.Entities().Find(q)
}

// FindBaseEntity finds the base entity in the store using the given URL,
// which can either represent a fully qualified entity or a base id.
// If any fields are specified, only those fields will be populated in the
// returned base entity.
func (s *Store) FindBaseEntity(url *charm.Reference, fields ...string) (*mongodoc.BaseEntity, error) {
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

func baseURL(url *charm.Reference) *charm.Reference {
	newURL := *url
	newURL.Revision = -1
	newURL.Series = ""
	return &newURL
}

var errNotImplemented = errgo.Newf("not implemented")

// AddBundle adds a bundle to the entities collection with the given
// parameters.
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
		URL:                 &p.URL.URL,
		BaseURL:             baseURL(&p.URL.URL),
		User:                p.URL.URL.User,
		Name:                p.URL.URL.Name,
		Revision:            p.URL.URL.Revision,
		Series:              p.URL.URL.Series,
		BlobHash:            p.BlobHash,
		BlobHash256:         p.BlobHash256,
		BlobName:            p.BlobName,
		Size:                p.BlobSize,
		UploadTime:          time.Now(),
		BundleData:          bundleData,
		BundleUnitCount:     newInt(bundleUnitCount(bundleData)),
		BundleMachineCount:  newInt(bundleMachineCount(bundleData)),
		BundleReadMe:        b.ReadMe(),
		BundleCharms:        urls,
		Contents:            p.Contents,
		PromulgatedURL:      p.URL.PromulgatedURL(),
		PromulgatedRevision: p.URL.PromulgatedRevision,
	}

	// Check that we're not going to create a bundle that duplicates
	// the name of a charm. This is racy, but it's the best we can do.
	entities, err := s.FindEntities(baseURL(&p.URL.URL))
	if err != nil {
		return errgo.Notef(err, "cannot check for existing entities")
	}
	for _, entity := range entities {
		if entity.URL.Series != "bundle" {
			return errgo.Newf("bundle name duplicates charm name %s", entity.URL)
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
func (s *Store) SetPerms(id *charm.Reference, which string, acl ...string) error {
	return s.DB.BaseEntities().UpdateId(baseURL(id), bson.D{{"$set",
		bson.D{{"acls." + which, acl}},
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
func bundleCharms(data *charm.BundleData) ([]*charm.Reference, error) {
	// Use a map to de-duplicate the URL list: a bundle can include services
	// deployed by the same charm.
	urlMap := make(map[string]*charm.Reference)
	for _, service := range data.Services {
		url, err := charm.ParseReference(service.Charm)
		if err != nil {
			return nil, errgo.Mask(err)
		}
		urlMap[url.String()] = url
		// Also add the corresponding base URL.
		base := baseURL(url)
		urlMap[base.String()] = base
	}
	urls := make([]*charm.Reference, 0, len(urlMap))
	for _, url := range urlMap {
		urls = append(urls, url)
	}
	return urls, nil
}

// AddLog adds a log message to the database.
func (s *Store) AddLog(data *json.RawMessage, logLevel mongodoc.LogLevel, logType mongodoc.LogType, urls []*charm.Reference) error {
	// Encode the JSON data.
	b, err := json.Marshal(data)
	if err != nil {
		return errgo.Notef(err, "cannot marshal log data")
	}

	// Add the base URLs to the list of references associated with the log.
	// Also remove duplicate URLs while maintaining the references' order.
	var allUrls []*charm.Reference
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

// Copy copies the StoreDatabase and its underlying mgo session.
func (s StoreDatabase) Copy() StoreDatabase {
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
var allCollections = []func(StoreDatabase) *mgo.Collection{
	StoreDatabase.StatCounters,
	StoreDatabase.StatTokens,
	StoreDatabase.Entities,
	StoreDatabase.BaseEntities,
	StoreDatabase.Logs,
	StoreDatabase.Migrations,
	StoreDatabase.Macaroons,
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
// It requires the PromulgatedURL field to have been
// filled out in the entity.
func EntityResolvedURL(e *mongodoc.Entity) *router.ResolvedURL {
	promulgatedRev := -1
	if e.PromulgatedURL != nil {
		promulgatedRev = e.PromulgatedURL.Revision
	}
	return &router.ResolvedURL{
		URL:                 *e.URL,
		PromulgatedRevision: promulgatedRev,
	}
}
