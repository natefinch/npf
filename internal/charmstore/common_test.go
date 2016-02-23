// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore // import "gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"

import (
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/macaroon-bakery.v1/bakery"

	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
	"gopkg.in/juju/charmstore.v5-unstable/internal/storetesting"
)

type commonSuite struct {
	storetesting.IsolatedMgoESSuite
	index string
}

// addRequiredCharms adds any charms required by the given
// bundle that are not already in the store.
func (s *commonSuite) addRequiredCharms(c *gc.C, bundle charm.Bundle) {
	store := s.newStore(c, true)
	defer store.Close()
	for _, svc := range bundle.Data().Services {
		u := charm.MustParseURL(svc.Charm)
		if _, err := store.FindBestEntity(u, StableChannel, nil); err == nil {
			continue
		}
		if u.Revision == -1 {
			u.Revision = 0
		}
		var rurl router.ResolvedURL
		rurl.URL = *u
		ch := storetesting.Charms.CharmDir(u.Name)
		if len(ch.Meta().Series) == 0 && u.Series == "" {
			rurl.URL.Series = "trusty"
		}
		if u.User == "" {
			rurl.URL.User = "charmers"
			rurl.PromulgatedRevision = rurl.URL.Revision
		} else {
			rurl.PromulgatedRevision = -1
		}
		err := store.AddCharmWithArchive(&rurl, ch)
		c.Assert(err, gc.IsNil)
		err = store.Publish(&rurl, StableChannel)
		c.Assert(err, gc.IsNil)
	}
}

func (s *commonSuite) newStore(c *gc.C, withES bool) *Store {
	var si *SearchIndex
	if withES {
		si = &SearchIndex{s.ES, s.TestIndex}
	}
	p, err := NewPool(s.Session.DB("juju_test"), si, &bakery.NewServiceParams{}, ServerParams{})
	c.Assert(err, gc.IsNil)
	store := p.Store()
	p.Close()
	return store
}
