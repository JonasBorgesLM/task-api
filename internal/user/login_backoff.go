package user

import (
	"context"
	"sync"
	"time"
)

// Login backoff curve (issue #220, 15.B3) — the "moderado" profile,
// levada ao usuário porque a issue nomeava explicitamente a curva como
// decisão em aberto. See docs/DECISIONS.md § "Atraso progressivo por
// conta" for the full reasoning and the profiles rejected.
const (
	// loginBackoffThreshold is how many consecutive failures an account
	// tolerates before any delay kicks in — a typo or two costs nothing.
	loginBackoffThreshold = 3
	// loginBackoffBase and loginBackoffCap define the curve from there:
	// loginBackoffBase * 2^(failures-loginBackoffThreshold), capped.
	// failures=3 -> 250ms, 4 -> 500ms, 5 -> 1s, 6 -> 2s, 7+ -> capped 4s.
	loginBackoffBase = 250 * time.Millisecond
	loginBackoffCap  = 4 * time.Second
	// loginBackoffIdleReset forgives an account's past failures once
	// this long has passed since the last attempt — a distinct concern
	// from the delay curve itself: without it, a handful of failures
	// years ago would still be "remembered" against an account that
	// never actually came under sustained attack.
	loginBackoffIdleReset = 15 * time.Minute
)

type loginBackoffEntry struct {
	failures    int
	lastAttempt time.Time
}

// loginBackoff tracks consecutive authentication failures per account —
// keyed by normalized email, the only identifier available before
// Service.Authenticate even knows whether the account exists — and
// computes the delay to apply before responding to the *next* attempt.
//
// That delay is applied to every attempt against the account, successful
// or not (see Authenticate), never just to failures: the issue's own
// first pitfall is that the response must stay indistinguishable from
// "invalid credential", and a delay that only appeared on failure would
// itself be the timing oracle. The second pitfall — a hard lock is
// denial-of-service against the legitimate owner — is why this only ever
// slows an account down (capped at loginBackoffCap) and never refuses a
// correct password outright.
//
// In-process, per-replica, the same shape as tokenCache and the
// existing moat/ratelimit tiers in cmd/api/main.go — see
// docs/DECISIONS.md for why making this span the whole deployment is a
// distinct architectural discussion this issue deliberately did not
// open.
type loginBackoff struct {
	mu      sync.Mutex
	entries map[string]loginBackoffEntry
	now     func() time.Time
}

func newLoginBackoff() *loginBackoff {
	return &loginBackoff{
		entries: make(map[string]loginBackoffEntry),
		now:     time.Now,
	}
}

// delay returns how long Authenticate should wait before responding to
// the attempt currently in flight for email, based on that account's
// failure count *before* this attempt — it never records anything
// itself. Authenticate calls this before evaluating the credential, then
// recordFailure/recordSuccess afterward, so the delay reflects prior
// state rather than the outcome of the attempt it is applied to.
func (b *loginBackoff) delay(email string) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry, ok := b.entries[email]
	if !ok {
		return 0
	}
	if b.now().Sub(entry.lastAttempt) > loginBackoffIdleReset {
		delete(b.entries, email)
		return 0
	}
	if entry.failures < loginBackoffThreshold {
		return 0
	}

	shift := entry.failures - loginBackoffThreshold
	d := loginBackoffBase << shift // loginBackoffBase * 2^shift
	if d > loginBackoffCap || d <= 0 {
		// d <= 0 covers the shift overflowing time.Duration (int64) for
		// a pathologically large failure count — unreachable in
		// practice given loginBackoffIdleReset, but the cap must never
		// be silently bypassed by an overflowed-to-negative duration.
		return loginBackoffCap
	}
	return d
}

// recordFailure increments email's consecutive-failure count, resetting
// it first if the account had gone quiet longer than loginBackoffIdleReset.
func (b *loginBackoff) recordFailure(email string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	entry, ok := b.entries[email]
	if !ok || now.Sub(entry.lastAttempt) > loginBackoffIdleReset {
		entry = loginBackoffEntry{}
	}
	entry.failures++
	entry.lastAttempt = now
	b.entries[email] = entry
}

// recordSuccess clears email's failure count. A real login resets the
// slate — the "increasing delay, never a hard lock" middle ground the
// issue asks for means a correct password always eventually gets in,
// and once it does the account is no longer under suspicion.
func (b *loginBackoff) recordSuccess(email string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.entries, email)
}

// sweep removes every entry idle long enough that delay() would already
// treat it as reset on the next lookup anyway — memory hygiene only,
// same reasoning as tokenCache.sweep: an account that fails a few times
// and is never attempted again would otherwise sit in this map forever.
func (b *loginBackoff) sweep() {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	for email, entry := range b.entries {
		if now.Sub(entry.lastAttempt) > loginBackoffIdleReset {
			delete(b.entries, email)
		}
	}
}

// sleepUnlessDone blocks for d or until ctx is done, whichever comes
// first — so a backoff delay never holds a connection open past client
// disconnect or server shutdown any longer than an ordinary bcrypt
// comparison already would.
func sleepUnlessDone(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}
