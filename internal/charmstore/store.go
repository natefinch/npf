// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore

import (
	"fmt"
	"io"

	"github.com/juju/errgo"
	"gopkg.in/juju/charm.v2"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/internal/blobstore"
	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/params"
)

// Store represents the underlying charm and blob data stores.
type Store struct {
	DB        StoreDatabase
	BlobStore *blobstore.Store
}

// NewStore returns a Store that uses the given database.
func NewStore(db *mgo.Database) *Store {
	return &Store{
		DB:        StoreDatabase{db},
		BlobStore: blobstore.New(db, "entitystore"),
	}
}

func (s *Store) putArchive(archive blobstore.ReadSeekCloser) (string, int64, error) {
	hash := blobstore.NewHash()
	size, err := io.Copy(hash, archive)
	if err != nil {
		return "", 0, errgo.Mask(err)
	}
	if _, err = archive.Seek(0, 0); err != nil {
		return "", 0, errgo.Mask(err)
	}
	blobHash := fmt.Sprintf("%x", hash.Sum(nil))
	if err = s.BlobStore.PutUnchallenged(archive, size, blobHash); err != nil {
		return "", 0, errgo.Mask(err)
	}
	return blobHash, size, nil
}

// AddCharm adds a charm to the blob store and to the entities collection
// associated with the given URL.
func (s *Store) AddCharm(url *charm.URL, c charm.Charm) error {
	// Insert the charm archive into the blob store.
	archive, err := getArchive(c)
	if err != nil {
		return errgo.Mask(err)
	}
	defer archive.Close()
	blobHash, size, err := s.putArchive(archive)
	if err != nil {
		// Add charm metadata to the entities collection.
		return errgo.Mask(err)
	}

	charmUrl := (*params.CharmURL)(url)
	return s.DB.Entities().Insert(&mongodoc.Entity{
		URL:                     charmUrl,
		BaseURL:                 baseURL(charmUrl),
		BlobHash:                blobHash,
		Size:                    size,
		CharmMeta:               c.Meta(),
		CharmConfig:             c.Config(),
		CharmActions:            c.Actions(),
		CharmProvidedInterfaces: interfacesForRelations(c.Meta().Provides),
		CharmRequiredInterfaces: interfacesForRelations(c.Meta().Requires),
	})
}

// ExpandURL returns all the URLs that the given URL may refer to.
func (s *Store) ExpandURL(url *charm.URL) ([]*charm.URL, error) {
	var docs []mongodoc.Entity
	err := s.DB.Entities().Find(bson.D{{
		"baseurl", baseURL((*params.CharmURL)(url)),
	}}).Select(bson.D{{"_id", 1}}).All(&docs)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	urls := make([]*charm.URL, 0, len(docs))
	for _, doc := range docs {
		if matchURL((*charm.URL)(doc.URL), url) {
			urls = append(urls, (*charm.URL)(doc.URL))
		}
	}
	return urls, nil
}

func matchURL(url, pattern *charm.URL) bool {
	if pattern.Series != "" && url.Series != pattern.Series {
		return false
	}
	if pattern.Revision != -1 && url.Revision != pattern.Revision {
		return false
	}
	// Check the name for completness only - the
	// query should only be returning URLs with
	// matching names.
	return url.Name == pattern.Name
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

func baseURL(url *params.CharmURL) *params.CharmURL {
	newURL := *url
	newURL.Revision = -1
	newURL.Series = ""
	return &newURL
}

var errNotImplemented = errgo.Newf("not implemented")

// AddBundle adds a bundle to the blob store and to the entities collection
// associated with the given URL.
func (s *Store) AddBundle(url *charm.URL, b charm.Bundle) error {
	// Insert the bundle archive into the blob store.
	archive, err := getArchive(b)
	if err != nil {
		return errgo.Mask(err)
	}
	defer archive.Close()
	blobHash, size, err := s.putArchive(archive)
	if err != nil {
		// Add charm metadata to the entities collection.
		return errgo.Mask(err)
	}

	charmUrl := (*params.CharmURL)(url)
	bundleData := b.Data()
	urls, err := bundleCharms(bundleData)
	if err != nil {
		return errgo.Mask(err)
	}
	return s.DB.Entities().Insert(&mongodoc.Entity{
		URL:          charmUrl,
		BaseURL:      baseURL(charmUrl),
		BlobHash:     blobHash,
		Size:         size,
		BundleData:   bundleData,
		BundleReadMe: b.ReadMe(),
		BundleCharms: urls,
	})
}

func bundleCharms(data *charm.BundleData) ([]*params.CharmURL, error) {
	// Use a map to de-duplicate the URL list: a bundle can include services
	// deployed by the same charm.
	urlMap := make(map[string]*params.CharmURL)
	for _, service := range data.Services {
		url, err := params.ParseURL(service.Charm)
		if err != nil {
			return nil, errgo.Mask(err)
		}
		urlMap[url.String()] = url
	}
	urls := make([]*params.CharmURL, 0, len(urlMap))
	for _, url := range urlMap {
		urls = append(urls, url)
	}
	return urls, nil
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

// Entities returns the mongo collection where entities are stored.
func (s StoreDatabase) Entities() *mgo.Collection {
	return s.C("entities")
}
