package utils

import (
	"sync"
	"time"
)

const NoExpiry = time.Duration(-1)

type CacheItem[V any] struct {
	Value      V
	Expiration time.Time
}

func (i CacheItem[V]) Expired() bool {
	return !i.Expiration.IsZero() && time.Now().After(i.Expiration)
}

type Cache[K comparable, V any] struct {
	mu         sync.RWMutex
	items      map[K]CacheItem[V]
	defaultTTL time.Duration
}

func NewCache[K comparable, V any](defaultTTL time.Duration) *Cache[K, V] {
	return &Cache[K, V]{
		items:      make(map[K]CacheItem[V]),
		defaultTTL: defaultTTL,
	}
}

func (c *Cache[K, V]) Set(key K, value V) {
	var exp time.Time

	if c.defaultTTL > 0 {
		exp = time.Now().Add(c.defaultTTL)
	}

	c.mu.Lock()
	c.items[key] = CacheItem[V]{
		Value:      value,
		Expiration: exp,
	}
	c.mu.Unlock()
}

func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.RLock()
	item, ok := c.items[key]
	c.mu.RUnlock()

	if !ok {
		var zero V
		return zero, false
	}

	if item.Expired() {
		c.mu.Lock()
		delete(c.items, key)
		c.mu.Unlock()

		var zero V
		return zero, false
	}

	return item.Value, true
}

// GetOrCreate returns the cached value for key if present and not
// expired; otherwise it calls create to produce a new value, stores
// it, and returns it. The whole check-then-store happens under a
// single write lock, so concurrent callers racing on the same key
// can never end up creating two separate values for it.
func (c *Cache[K, V]) GetOrCreate(key K, create func() V) V {
	c.mu.Lock()
	defer c.mu.Unlock()

	if item, ok := c.items[key]; ok && !item.Expired() {
		return item.Value
	}

	value := create()

	var exp time.Time
	if c.defaultTTL > 0 {
		exp = time.Now().Add(c.defaultTTL)
	}

	c.items[key] = CacheItem[V]{
		Value:      value,
		Expiration: exp,
	}

	return value
}

func (c *Cache[K, V]) Delete(key K) {
	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()
}
