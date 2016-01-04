package entitycache_test

import (
	"testing"

	"gopkg.in/juju/charm.v6-unstable"

	"gopkg.in/juju/charmstore.v5-unstable/internal/entitycache"
	"gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"
)

func BenchmarkSingleRequest(b *testing.B) {
	// This benchmarks the common case of getting a single entity and its
	// base entity, so that we get an idea of the baseline overhead
	// in this simple case.
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
			return entity, nil
		},
		findBaseEntity: func(url *charm.URL, fields map[string]int) (*mongodoc.BaseEntity, error) {
			return baseEntity, nil
		},
	}
	url := charm.MustParseURL("~bob/wordpress-1")
	baseURL := charm.MustParseURL("~bob/wordpress")
	for i := 0; i < b.N; i++ {
		c := entitycache.New(store)
		c.AddEntityFields("size", "blobname")
		e, err := c.Entity(url)
		if err != nil || e != entity {
			b.Fatalf("get returned unexpected entity (err %v)", err)
		}
		be, err := c.BaseEntity(baseURL)
		if err != nil || be != baseEntity {
			b.Fatalf("get returned unexpected base entity (err %v)", err)
		}
	}
}
