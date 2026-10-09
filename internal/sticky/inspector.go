package sticky

import (
	"sync"
	"time"
)

// ListFunc returns the host's runtime state for every credential it can describe, keyed by
// auth ID. A credential missing from the map is unknown, not gone: the host omits some
// credentials (for example config API keys without a backing file).
type ListFunc func() (map[string]AuthState, error)

// CachedInspector answers Inspect from one host listing that it reuses for ttl, so a burst of
// picks while a pinned credential is absent costs one host call.
type CachedInspector struct {
	list ListFunc
	ttl  time.Duration
	now  func() time.Time

	mu        sync.Mutex
	fetchedAt time.Time
	states    map[string]AuthState
	fetched   bool
}

// NewCachedInspector wraps list with a cache that expires after ttl.
func NewCachedInspector(list ListFunc, ttl time.Duration) *CachedInspector {
	return &CachedInspector{list: list, ttl: ttl, now: time.Now}
}

// Inspect implements Inspector. A failed listing is cached like a successful one, so a broken
// host callback is retried once per ttl rather than on every pick.
func (c *CachedInspector) Inspect(authID string) (AuthState, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if !c.fetched || now.Sub(c.fetchedAt) >= c.ttl {
		states, errList := c.list()
		if errList != nil {
			states = nil
		}
		c.states, c.fetchedAt, c.fetched = states, now, true
	}
	state, ok := c.states[authID]
	return state, ok
}
