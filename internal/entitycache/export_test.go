package entitycache

func CacheIter(c *Cache, mgoIter mgoIter, fields map[string]int) *Iter {
	c.entities.mu.Lock()
	defer c.entities.mu.Unlock()
	// Note: this is exactly the same as Cache.Iter except that
	// it doesn't actually call the Query methods.
	c.entities.addFields(fields)
	return c.iter(mgoIter)
}

const (
	EntityThreshold     = entityThreshold
	BaseEntityThreshold = baseEntityThreshold
)
