// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package storetesting // import "gopkg.in/juju/charmstore.v5-unstable/internal/storetesting"

import (
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/mgo.v2"

	"gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"
)

// EntityBuilder provides a convenient way to describe a mongodoc.Entity
// for tests that is correctly formed and contains the desired
// information.
type EntityBuilder struct {
	entity *mongodoc.Entity
}

// NewEntity creates a new EntityBuilder for the provided URL.
func NewEntity(url string) EntityBuilder {
	URL := charm.MustParseURL(url)
	return EntityBuilder{
		entity: &mongodoc.Entity{
			URL:                 URL,
			Name:                URL.Name,
			Series:              URL.Series,
			Revision:            URL.Revision,
			User:                URL.User,
			BaseURL:             mongodoc.BaseURL(URL),
			PromulgatedRevision: -1,
		},
	}
}

func copyURL(id *charm.URL) *charm.URL {
	if id == nil {
		return nil
	}
	id1 := *id
	return &id1
}

func (b EntityBuilder) copy() EntityBuilder {
	e := *b.entity
	e.PromulgatedURL = copyURL(e.PromulgatedURL)
	e.URL = copyURL(e.URL)
	e.BaseURL = copyURL(e.BaseURL)
	return EntityBuilder{&e}
}

// WithPromulgatedURL sets the PromulgatedURL and PromulgatedRevision of the
// entity being built.
func (b EntityBuilder) WithPromulgatedURL(url string) EntityBuilder {
	b = b.copy()
	if url == "" {
		b.entity.PromulgatedURL = nil
		b.entity.PromulgatedRevision = -1
	} else {
		b.entity.PromulgatedURL = charm.MustParseURL(url)
		b.entity.PromulgatedRevision = b.entity.PromulgatedURL.Revision
	}
	return b
}

// Build creates a mongodoc.Entity from the EntityBuilder.
func (b EntityBuilder) Build() *mongodoc.Entity {
	return b.copy().entity
}

// AssertEntity checks that db contains an entity that matches expect.
func AssertEntity(c *gc.C, db *mgo.Collection, expect *mongodoc.Entity) {
	var entity mongodoc.Entity
	err := db.FindId(expect.URL).One(&entity)
	c.Assert(err, gc.IsNil)
	c.Assert(&entity, jc.DeepEquals, expect)
}

// BaseEntityBuilder provides a convenient way to describe a
// mongodoc.BaseEntity for tests that is correctly formed and contains the
// desired information.
type BaseEntityBuilder struct {
	baseEntity *mongodoc.BaseEntity
}

// NewBaseEntity creates a new BaseEntityBuilder for the provided URL.
func NewBaseEntity(url string) BaseEntityBuilder {
	URL := charm.MustParseURL(url)
	return BaseEntityBuilder{
		baseEntity: &mongodoc.BaseEntity{
			URL:  URL,
			Name: URL.Name,
			User: URL.User,
		},
	}
}

func (b BaseEntityBuilder) copy() BaseEntityBuilder {
	e := *b.baseEntity
	e.URL = copyURL(e.URL)
	return BaseEntityBuilder{&e}
}

// WithPromulgated sets the promulgated flag on the BaseEntity.
func (b BaseEntityBuilder) WithPromulgated(promulgated bool) BaseEntityBuilder {
	b = b.copy()
	b.baseEntity.Promulgated = mongodoc.IntBool(promulgated)
	return b
}

// WithACLs sets the ACLs field on the BaseEntity.
func (b BaseEntityBuilder) WithACLs(acls mongodoc.ACL) BaseEntityBuilder {
	b = b.copy()
	b.baseEntity.ACLs = acls
	return b
}

// Build creates a mongodoc.BaseEntity from the BaseEntityBuilder.
func (b BaseEntityBuilder) Build() *mongodoc.BaseEntity {
	return b.copy().baseEntity
}

// AssertBaseEntity checks that db contains a base entity that matches expect.
func AssertBaseEntity(c *gc.C, db *mgo.Collection, expect *mongodoc.BaseEntity) {
	var baseEntity mongodoc.BaseEntity
	err := db.FindId(expect.URL).One(&baseEntity)
	c.Assert(err, gc.IsNil)
	c.Assert(&baseEntity, jc.DeepEquals, NormalizeBaseEntity(expect))
}

// NormalizeBaseEntity modifies a base entity so that it can be compared
// with a base entity retrieved from mongodb using jc.DeepEquals. If
// either StableSeries or DevelopmentSeries are nil maps then they will
// be modified to be empty maps.
func NormalizeBaseEntity(be *mongodoc.BaseEntity) *mongodoc.BaseEntity {
	if be.StableSeries != nil && be.DevelopmentSeries != nil {
		return be
	}
	be1 := *be
	if be1.DevelopmentSeries == nil {
		be1.DevelopmentSeries = make(map[string]*charm.URL)
	}
	if be1.StableSeries == nil {
		be1.StableSeries = make(map[string]*charm.URL)
	}
	return &be1
}
