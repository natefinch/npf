package entitycache

type TestIter interface {
	SetFields(fields map[string]int)
	mgoIter
}

func CacheIter(c *Cache, iter TestIter, fields map[string]int) *Iter {
	c.entities.mu.Lock()
	defer c.entities.mu.Unlock()
	// Note: this is exactly the same as Cache.Iter except that
	// it doesn't actually call the Query methods.
	c.entities.addFields(fields)
	iter.SetFields(c.entities.fields)
	return c.iter(iter)
}

var (
	RequiredEntityFields     = requiredEntityFields
	RequiredBaseEntityFields = requiredBaseEntityFields
)

const (
	EntityThreshold     = entityThreshold
	BaseEntityThreshold = baseEntityThreshold
)
