// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore

import (
	"fmt"

	"gopkg.in/juju/charm.v2"
	"labix.org/v2/mgo"

	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/router"
	"github.com/juju/charmstore/params"
)

// Store represents the underlying charm store data store.
type Store struct {
	DB StoreDatabase
}

// NewStore returns a Store that uses the given database.
func NewStore(db *mgo.Database) *Store {
	s := &Store{
		DB: StoreDatabase{db},
	}
	router.RegisterCollection(s.DB.Entities().Name, (*mongodoc.Entity)(nil))
	return s
}

// AddCharm adds a charm to the entities collection
// associated with the given URL.
// The charm does not have any associated content.
// TODO fix this to add content too.
func (s *Store) AddCharm(url *charm.URL, c charm.Charm) error {
	charmUrl := (*params.CharmURL)(url)
	return s.DB.Entities().Insert(&mongodoc.Entity{
		URL:                     charmUrl,
		BaseURL:                 baseURL(charmUrl),
		CharmMeta:               c.Meta(),
		CharmConfig:             c.Config(),
		CharmActions:            c.Actions(),
		CharmProvidedInterfaces: interfacesForRelations(c.Meta().Provides),
		CharmRequiredInterfaces: interfacesForRelations(c.Meta().Requires),
	})
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

var errNotImplemented = fmt.Errorf("not implemented")

// AddBundle adds a bundle to the entities collection
// associated with the given URL.
// The bundle does not have any associated content.
// TODO fix this to add content too.
func (s *Store) AddBundle(url *charm.URL, b charm.Bundle) error {
	charmUrl := (*params.CharmURL)(url)
	bundleData := b.Data()
	urls, err := bundleCharms(bundleData)
	if err != nil {
		return err
	}
	return s.DB.Entities().Insert(&mongodoc.Entity{
		URL:          charmUrl,
		BaseURL:      baseURL(charmUrl),
		BundleData:   bundleData,
		BundleReadMe: b.ReadMe(),
		BundleCharms: urls,
	})
}

func bundleCharms(data *charm.BundleData) ([]*params.CharmURL, error) {
	urls := make([]*params.CharmURL, 0, len(data.Services))
	for _, service := range data.Services {
		url, err := params.ParseURL(service.Charm)
		if err != nil {
			return nil, err
		}
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
