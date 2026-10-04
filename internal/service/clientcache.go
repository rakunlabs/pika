package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
)

// clientCache memoizes external backend clients by a key derived from
// their full configuration. Entries may carry a release func (e.g. to
// stop a background renewal goroutine) that runs when the cache is
// purged.
type clientCache[V any] struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry[V]
}

type cacheEntry[V any] struct {
	value   V
	release func()
}

func newClientCache[V any]() *clientCache[V] {
	return &clientCache[V]{entries: make(map[string]cacheEntry[V])}
}

// get returns the cached value for key, building it with create on a
// miss. create runs under the write lock so concurrent misses build the
// client only once.
func (c *clientCache[V]) get(key string, create func() (V, func(), error)) (V, error) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if ok {
		return e.value, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok = c.entries[key]; ok {
		return e.value, nil
	}

	v, release, err := create()
	if err != nil {
		var zero V
		return zero, err
	}

	c.entries[key] = cacheEntry[V]{value: v, release: release}
	return v, nil
}

// purge drops every cached client and runs their release funcs.
func (c *clientCache[V]) purge() {
	c.mu.Lock()
	entries := c.entries
	c.entries = make(map[string]cacheEntry[V])
	c.mu.Unlock()

	for _, e := range entries {
		if e.release != nil {
			e.release()
		}
	}
}

// configCacheKey hashes the full JSON form of cfg (credentials included)
// so a credential change maps to a new client and secrets are never kept
// verbatim as map keys.
func configCacheKey(cfg any) string {
	b, err := json.Marshal(cfg)
	if err != nil {
		// Config types are plain structs; this should never happen.
		// Fall back to a key that never collides with a valid one.
		return "unhashable:" + err.Error()
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
