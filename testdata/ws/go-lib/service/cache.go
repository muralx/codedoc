package service

// Cache keeps looked-up values in memory.
type Cache struct {
	items map[string]string
}

// Get returns the cached value for key.
func (c *Cache) Get(key string) (string, bool) {
	v, ok := c.items[key]
	return v, ok
}

// Has reports whether key is cached.
func (c *Cache) Has(key string) bool {
	_, ok := c.Get(key)
	return ok
}
