// Package authcache holds the small TTL cache that tracks whether a given
// user_id is currently allowed to use the panel. It lives in its own
// leaf package so both the auth middleware (reader + writer) and the
// user service (cache invalidator on suspend / delete / activate) can
// depend on it without creating a middleware ↔ services import cycle.
package authcache

import (
	"sync"
	"time"
)

// Status is what we cache — whether the user is currently allowed to
// authenticate, and when the entry should be re-checked against Mongo.
type Status struct {
	Allowed   bool
	ExpiresAt time.Time
}

var (
	mu    sync.RWMutex
	cache = map[string]Status{}
)

// Get returns the cached status for userID if it exists. Callers should
// treat a zero-value Status as "no entry" and re-populate via Put.
func Get(userID string) (Status, bool) {
	mu.RLock()
	defer mu.RUnlock()
	s, ok := cache[userID]
	return s, ok
}

// Put replaces or inserts an entry for userID.
func Put(userID string, s Status) {
	if userID == "" {
		return
	}
	mu.Lock()
	cache[userID] = s
	mu.Unlock()
}

// Invalidate removes the cache entry for a single user_id, forcing the
// next authenticated request to re-check the DB. Called by suspend /
// delete / activate so the change takes effect within milliseconds
// rather than waiting for the TTL.
func Invalidate(userID string) {
	if userID == "" {
		return
	}
	mu.Lock()
	delete(cache, userID)
	mu.Unlock()
}

// InvalidateAll nukes the entire cache — used after bulk operations
// (e.g. tenant cascade delete) where invalidating each user_id
// individually would be wasteful.
func InvalidateAll() {
	mu.Lock()
	cache = map[string]Status{}
	mu.Unlock()
}
