package user

import (
	"context"
	"testing"
	"time"
)

func newTestLoginBackoff() (*loginBackoff, *fakeClock) {
	clock := &fakeClock{now: time.Now()}
	b := newLoginBackoff()
	b.now = clock.Now
	return b, clock
}

func TestLoginBackoff_Delay_ZeroWithNoHistory(t *testing.T) {
	b, _ := newTestLoginBackoff()

	if d := b.delay("user@example.com"); d != 0 {
		t.Errorf("delay() with no prior attempts = %v, want 0", d)
	}
}

// TestLoginBackoff_Delay_ZeroBelowThreshold pins the "a typo or two costs
// nothing" guarantee: loginBackoffThreshold failures must accumulate
// before any delay appears at all.
func TestLoginBackoff_Delay_ZeroBelowThreshold(t *testing.T) {
	b, _ := newTestLoginBackoff()

	for i := 0; i < loginBackoffThreshold-1; i++ {
		b.recordFailure("user@example.com")
	}

	if d := b.delay("user@example.com"); d != 0 {
		t.Errorf("delay() after %d failures (threshold %d) = %v, want 0", loginBackoffThreshold-1, loginBackoffThreshold, d)
	}
}

// TestLoginBackoff_Delay_DoublesFromThreshold pins the exact curve this
// profile was chosen for: loginBackoffBase * 2^(failures-threshold),
// starting the moment failures reaches loginBackoffThreshold.
func TestLoginBackoff_Delay_DoublesFromThreshold(t *testing.T) {
	b, _ := newTestLoginBackoff()

	wantAtFailureCount := map[int]time.Duration{
		loginBackoffThreshold:     loginBackoffBase,     // 250ms
		loginBackoffThreshold + 1: loginBackoffBase * 2, // 500ms
		loginBackoffThreshold + 2: loginBackoffBase * 4, // 1s
		loginBackoffThreshold + 3: loginBackoffBase * 8, // 2s
	}

	for failures := 1; failures <= loginBackoffThreshold+3; failures++ {
		b.recordFailure("user@example.com")
		if want, ok := wantAtFailureCount[failures]; ok {
			if got := b.delay("user@example.com"); got != want {
				t.Errorf("delay() after %d failures = %v, want %v", failures, got, want)
			}
		}
	}
}

func TestLoginBackoff_Delay_CapsAtMaximum(t *testing.T) {
	b, _ := newTestLoginBackoff()

	// Comfortably past where the doubling curve would exceed the cap.
	for i := 0; i < loginBackoffThreshold+10; i++ {
		b.recordFailure("user@example.com")
	}

	if d := b.delay("user@example.com"); d != loginBackoffCap {
		t.Errorf("delay() after many failures = %v, want the cap %v", d, loginBackoffCap)
	}
}

func TestLoginBackoff_RecordSuccess_ClearsTheAccount(t *testing.T) {
	b, _ := newTestLoginBackoff()

	for i := 0; i < loginBackoffThreshold+2; i++ {
		b.recordFailure("user@example.com")
	}
	if d := b.delay("user@example.com"); d == 0 {
		t.Fatal("delay() should be nonzero before recordSuccess — test setup is wrong")
	}

	b.recordSuccess("user@example.com")

	if d := b.delay("user@example.com"); d != 0 {
		t.Errorf("delay() after recordSuccess = %v, want 0", d)
	}
}

// TestLoginBackoff_IdleReset_ForgivesOldFailures pins
// loginBackoffIdleReset: an account that stops being attempted is no
// longer under suspicion once enough real time has passed, independent
// of ever succeeding.
func TestLoginBackoff_IdleReset_ForgivesOldFailures(t *testing.T) {
	b, clock := newTestLoginBackoff()

	for i := 0; i < loginBackoffThreshold+2; i++ {
		b.recordFailure("user@example.com")
	}
	if d := b.delay("user@example.com"); d == 0 {
		t.Fatal("delay() should be nonzero before the idle window elapses — test setup is wrong")
	}

	clock.now = clock.now.Add(loginBackoffIdleReset + time.Second)

	if d := b.delay("user@example.com"); d != 0 {
		t.Errorf("delay() after the idle reset window = %v, want 0", d)
	}

	// The reset must actually clear the counter, not just make delay()
	// answer 0 while secretly still counting from where it left off.
	b.recordFailure("user@example.com")
	if d := b.delay("user@example.com"); d != 0 {
		t.Errorf("delay() after a single failure post-reset = %v, want 0 (below threshold again)", d)
	}
}

func TestLoginBackoff_Delay_DoesNotConfuseDifferentAccounts(t *testing.T) {
	b, _ := newTestLoginBackoff()

	for i := 0; i < loginBackoffThreshold+2; i++ {
		b.recordFailure("attacked@example.com")
	}

	if d := b.delay("innocent@example.com"); d != 0 {
		t.Errorf("delay() for an unrelated account = %v, want 0", d)
	}
}

// TestLoginBackoff_Sweep_RemovesOnlyIdleEntries is the memory-hygiene
// pass this shares with tokenCache.sweep — delay() already treats an
// idle-reset entry as gone on its own, so this only has to prove sweep
// doesn't also discard something still within the idle window.
func TestLoginBackoff_Sweep_RemovesOnlyIdleEntries(t *testing.T) {
	b, clock := newTestLoginBackoff()
	b.recordFailure("idle@example.com")

	clock.now = clock.now.Add(loginBackoffIdleReset + time.Second)
	b.recordFailure("active@example.com") // recorded *after* the clock advance

	b.sweep()

	if len(b.entries) != 1 {
		t.Fatalf("entries after sweep = %d, want 1", len(b.entries))
	}
	if _, stillThere := b.entries["idle@example.com"]; stillThere {
		t.Error("sweep() left the idle entry in place")
	}
	if _, stillThere := b.entries["active@example.com"]; !stillThere {
		t.Error("sweep() removed a still-active entry")
	}
}

func TestSleepUnlessDone_ZeroDurationReturnsImmediately(t *testing.T) {
	start := time.Now()
	sleepUnlessDone(t.Context(), 0)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("sleepUnlessDone(0) took %v, want effectively instant", elapsed)
	}
}

func TestSleepUnlessDone_BlocksForApproximatelyTheDuration(t *testing.T) {
	const d = 30 * time.Millisecond
	start := time.Now()
	sleepUnlessDone(t.Context(), d)
	if elapsed := time.Since(start); elapsed < d {
		t.Errorf("sleepUnlessDone(%v) returned after %v, want at least %v", d, elapsed, d)
	}
}

// TestSleepUnlessDone_ReturnsEarlyWhenContextCancelled pins the reason
// this isn't a plain time.Sleep: a cancelled context (client
// disconnected, server shutting down) must not hold the goroutine for
// the full delay.
func TestSleepUnlessDone_ReturnsEarlyWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	start := time.Now()
	sleepUnlessDone(ctx, time.Hour)
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("sleepUnlessDone() with an already-cancelled context took %v, want effectively instant", elapsed)
	}
}
