package typeinfo

import (
	"reflect"
	"sync"
)

// Cache stores one result per type so callers can reuse extracted metadata without walking the
// type on every reload.
type Cache[V any] struct {
	mu sync.Mutex
	m  map[reflect.Type]V
}

// NewCache creates a new Cache.
func NewCache[V any]() *Cache[V] { return &Cache[V]{m: map[reflect.Type]V{}} }

// GetOrCompute returns the cached value for t or computes it via fn. fn runs while the cache lock
// is held, so it must be deterministic and must not call GetOrCompute on the same Cache.
func (c *Cache[V]) GetOrCompute(t reflect.Type, fn func() V) V {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.m[t]; ok {
		return v
	}
	v := fn()
	c.m[t] = v
	return v
}
