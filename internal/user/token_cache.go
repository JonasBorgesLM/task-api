package user

import (
	"sync"
	"time"
)

// tokenCacheTTL bounds how long Service.ValidateToken trusts a cached
// result before checking Repository again. Fixed, not a
// config.Config-exposed setting: this number embodies a deliberate
// security/performance trade-off (see docs/DECISIONS.md § "Cache de
// ValidateToken"), and making it an environment variable would let an
// operator silently widen the revocation-delay window that decision
// was about, without the same reasoning being revisited.
const tokenCacheTTL = 2 * time.Second

// tokenCacheEntry is one cached ValidateToken result.
type tokenCacheEntry struct {
	userID    string
	expiresAt time.Time
}

// tokenCache is a short-lived, in-process cache of Service.ValidateToken's
// result, keyed by the same tokenHash Repository.FindSessionByTokenHash
// already looks up by. See docs/DECISIONS.md § "Cache de ValidateToken"
// for the full reasoning and the trade-off it accepts — in short: this
// eliminates the one database read on almost every authenticated
// request, at the cost of a revocation (logout, logout-all, password
// change) taking up to tokenCacheTTL to actually take effect on a
// *different* process than the one that issued it. The same process
// invalidates immediately — see delete/deleteAllForUser/
// deleteAllForUserExcept below — so the residual window is bounded to
// cross-replica staleness, in a deployment that otherwise runs a single
// instance (see docs/DECISIONS.md § "Topologia de deploy") except for
// the brief overlap a rolling update creates.
//
// A fixed expiry from the moment of the last real Repository check, not
// a sliding one refreshed on every cache hit: a sliding window could let
// a revoked-elsewhere session keep validating indefinitely as long as
// requests kept arriving faster than the TTL, which would defeat the
// bound this cache is supposed to guarantee. Every entry's staleness is
// capped at tokenCacheTTL from when Repository last actually confirmed
// it, full stop.
//
// A sync.RWMutex-guarded map, not sync.Map: reads dominate writes, but
// deleteAllForUser/deleteAllForUserExcept need to scan for every entry
// belonging to one user, which sync.Map has no efficient way to express,
// and sweep needs the same full-scan shape.
type tokenCache struct {
	mu      sync.RWMutex
	entries map[string]tokenCacheEntry // key: tokenHash
	ttl     time.Duration
	// now exists so tests can advance time without a real sleep — see
	// token_cache_test.go. Always time.Now in production; newTokenCache
	// sets it and nothing outside this file ever overrides it again.
	now func() time.Time
}

func newTokenCache(ttl time.Duration) *tokenCache {
	return &tokenCache{
		entries: make(map[string]tokenCacheEntry),
		ttl:     ttl,
		now:     time.Now,
	}
}

// get returns the cached userID for tokenHash, and whether the entry was
// present and not yet past its ttl. A stale entry is treated exactly
// like a missing one — get never deletes it itself; sweep and outright
// overwriting via a later set are what actually reclaim the map entry,
// so a concurrent reader never has to take the write lock just to notice
// staleness.
func (c *tokenCache) get(tokenHash string) (userID string, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, found := c.entries[tokenHash]
	if !found || c.now().After(entry.expiresAt) {
		return "", false
	}
	return entry.userID, true
}

// set records a successful Repository.FindSessionByTokenHash lookup,
// starting a fresh ttl window from now.
func (c *tokenCache) set(tokenHash, userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[tokenHash] = tokenCacheEntry{userID: userID, expiresAt: c.now().Add(c.ttl)}
}

// delete removes tokenHash's entry, if any. Called from Logout so a
// revoked token stops validating from cache immediately in *this*
// process — the only staleness this cache is meant to ever tolerate is
// the cross-process kind ttl bounds, never "the same process that just
// revoked it still accepts it."
func (c *tokenCache) delete(tokenHash string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, tokenHash)
}

// deleteAllForUser removes every cached entry belonging to userID.
// Called from LogoutAll and DeleteAccount, both of which revoke every
// session a user has — same immediacy reasoning as delete.
func (c *tokenCache) deleteAllForUser(userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for hash, entry := range c.entries {
		if entry.userID == userID {
			delete(c.entries, hash)
		}
	}
}

// deleteAllForUserExcept is deleteAllForUser's ChangePassword-shaped
// sibling: every entry belonging to userID is removed except the one
// keyed by keepTokenHash — the session that made the password-change
// call itself, which Repository.DeleteSessionsForUserExcept already
// leaves alive for the identical reason (see Service.ChangePassword).
func (c *tokenCache) deleteAllForUserExcept(userID, keepTokenHash string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for hash, entry := range c.entries {
		if entry.userID == userID && hash != keepTokenHash {
			delete(c.entries, hash)
		}
	}
}

// sweep removes every entry whose ttl has already passed. A memory-
// hygiene pass only, never a correctness requirement: get already
// treats a stale entry as a miss on its own, so a token validated once
// and never again would otherwise sit harmlessly in the map forever.
// Called from Service.PruneExpiredSessions, the same periodic job that
// already exists to bound the real sessions table's own growth from
// abandoned tokens — see that method's doc comment.
func (c *tokenCache) sweep() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for hash, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, hash)
		}
	}
}
