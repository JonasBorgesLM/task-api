package user

import (
	"testing"
	"time"
)

// fakeClock lets a test advance time without a real sleep — tokenCache's
// own ttl (2s in production) would otherwise make expiry tests slow and,
// under load, flaky.
type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time { return c.now }

func newTestCache(ttl time.Duration) (*tokenCache, *fakeClock) {
	clock := &fakeClock{now: time.Now()}
	c := newTokenCache(ttl)
	c.now = clock.Now
	return c, clock
}

func TestTokenCache_GetMiss_OnEmptyCache(t *testing.T) {
	c, _ := newTestCache(time.Minute)

	if _, ok := c.get("missing"); ok {
		t.Error("get() on an empty cache = hit, want miss")
	}
}

func TestTokenCache_SetThenGet_Hits(t *testing.T) {
	c, _ := newTestCache(time.Minute)

	c.set("hash1", "user1")

	userID, ok := c.get("hash1")
	if !ok {
		t.Fatal("get() after set() = miss, want hit")
	}
	if userID != "user1" {
		t.Errorf("get() userID = %q, want %q", userID, "user1")
	}
}

// TestTokenCache_EntryExpires_AfterTTL is the fixed-window guarantee
// tokenCache's own doc comment states: an entry answers hit for
// strictly less than ttl, then miss — never refreshed just because it
// was read.
func TestTokenCache_EntryExpires_AfterTTL(t *testing.T) {
	c, clock := newTestCache(2 * time.Second)
	c.set("hash1", "user1")

	clock.now = clock.now.Add(2*time.Second - time.Nanosecond)
	if _, ok := c.get("hash1"); !ok {
		t.Error("get() one nanosecond before ttl elapsed = miss, want hit")
	}

	clock.now = clock.now.Add(2 * time.Nanosecond)
	if _, ok := c.get("hash1"); ok {
		t.Error("get() after ttl elapsed = hit, want miss")
	}
}

// TestTokenCache_FixedWindow_NotSlidingOnRead pins the specific
// guarantee the "fixed, not sliding" doc comment on tokenCache makes:
// repeatedly reading an entry must never push its expiry further out —
// only a fresh set() does that. A sliding window would let a
// revoked-elsewhere token keep validating indefinitely as long as
// requests arrived faster than ttl, defeating the bound this cache
// exists to guarantee.
func TestTokenCache_FixedWindow_NotSlidingOnRead(t *testing.T) {
	c, clock := newTestCache(2 * time.Second)
	c.set("hash1", "user1")

	// Two reads, one second apart, both still inside the 2-second
	// window — each is also what a sliding-window implementation would
	// use to push expiresAt further out, which is exactly what must not
	// happen here.
	clock.now = clock.now.Add(time.Second) // t = 1s
	if _, ok := c.get("hash1"); !ok {
		t.Fatal("get() at 1s against a 2s ttl = miss, want hit")
	}
	clock.now = clock.now.Add(time.Second) // t = 2s: still not *past* ttl
	if _, ok := c.get("hash1"); !ok {
		t.Fatal("get() at 2s against a 2s ttl = miss, want hit (boundary, not yet elapsed)")
	}

	// A third second passes — 3s total since the original set(), one
	// second past the 2-second ttl. A sliding window refreshed by either
	// read above would still show a hit here; a fixed one must not.
	clock.now = clock.now.Add(time.Second) // t = 3s
	if _, ok := c.get("hash1"); ok {
		t.Error("get() at 3s against a 2s ttl (never refreshed) = hit, want miss — window must not slide on read")
	}
}

func TestTokenCache_Delete_RemovesTheEntry(t *testing.T) {
	c, _ := newTestCache(time.Minute)
	c.set("hash1", "user1")

	c.delete("hash1")

	if _, ok := c.get("hash1"); ok {
		t.Error("get() after delete() = hit, want miss")
	}
}

func TestTokenCache_Delete_UnknownHash_IsANoOp(t *testing.T) {
	c, _ := newTestCache(time.Minute)
	c.delete("never-set") // must not panic
}

func TestTokenCache_DeleteAllForUser_RemovesOnlyThatUsersEntries(t *testing.T) {
	c, _ := newTestCache(time.Minute)
	c.set("hash-a1", "userA")
	c.set("hash-a2", "userA")
	c.set("hash-b1", "userB")

	c.deleteAllForUser("userA")

	if _, ok := c.get("hash-a1"); ok {
		t.Error("userA's first session still cached after deleteAllForUser")
	}
	if _, ok := c.get("hash-a2"); ok {
		t.Error("userA's second session still cached after deleteAllForUser")
	}
	if _, ok := c.get("hash-b1"); !ok {
		t.Error("userB's session was removed by userA's deleteAllForUser")
	}
}

func TestTokenCache_DeleteAllForUserExcept_KeepsOnlyTheNamedHash(t *testing.T) {
	c, _ := newTestCache(time.Minute)
	c.set("hash-keep", "userA")
	c.set("hash-drop", "userA")
	c.set("hash-other-user", "userB")

	c.deleteAllForUserExcept("userA", "hash-keep")

	if _, ok := c.get("hash-keep"); !ok {
		t.Error("the excepted hash was removed too")
	}
	if _, ok := c.get("hash-drop"); ok {
		t.Error("userA's other session still cached after deleteAllForUserExcept")
	}
	if _, ok := c.get("hash-other-user"); !ok {
		t.Error("a different user's session was removed by userA's deleteAllForUserExcept")
	}
}

// TestTokenCache_Sweep_RemovesOnlyExpiredEntries is the memory-hygiene
// pass tokenCache's own doc comment describes: get() already treats a
// stale entry as a miss on its own, so this only has to prove sweep
// doesn't also discard something still live.
func TestTokenCache_Sweep_RemovesOnlyExpiredEntries(t *testing.T) {
	c, clock := newTestCache(time.Second)
	c.set("expired", "user1")

	clock.now = clock.now.Add(2 * time.Second)
	c.set("fresh", "user1") // set *after* the clock advance: not expired

	c.sweep()

	if len(c.entries) != 1 {
		t.Fatalf("entries after sweep = %d, want 1", len(c.entries))
	}
	if _, stillThere := c.entries["expired"]; stillThere {
		t.Error("sweep() left the expired entry in place")
	}
	if _, stillThere := c.entries["fresh"]; !stillThere {
		t.Error("sweep() removed a still-live entry")
	}
}
