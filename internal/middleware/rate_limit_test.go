package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiter_AllowsUpToLimit(t *testing.T) {
	rl := NewRateLimiter(3, time.Minute)
	now := time.Now()

	for i := range 3 {
		if !rl.allow("1.2.3.4", now) {
			t.Fatalf("allow() call %d: want true, got false", i+1)
		}
	}
}

func TestRateLimiter_BlocksOverLimit(t *testing.T) {
	rl := NewRateLimiter(3, time.Minute)
	now := time.Now()

	for range 3 {
		rl.allow("1.2.3.4", now)
	}
	if rl.allow("1.2.3.4", now) {
		t.Error("allow() call over the limit: want false, got true")
	}
}

func TestRateLimiter_DifferentKeysAreIndependent(t *testing.T) {
	rl := NewRateLimiter(1, time.Minute)
	now := time.Now()

	if !rl.allow("1.2.3.4", now) {
		t.Fatal("allow() first call for key A: want true")
	}
	if !rl.allow("5.6.7.8", now) {
		t.Error("allow() first call for key B: want true — must not share key A's budget")
	}
	if rl.allow("1.2.3.4", now) {
		t.Error("allow() second call for key A: want false, it already used its budget")
	}
}

func TestRateLimiter_ResetsAfterWindowEnds(t *testing.T) {
	rl := NewRateLimiter(1, time.Minute)
	now := time.Now()

	if !rl.allow("1.2.3.4", now) {
		t.Fatal("allow() first call: want true")
	}
	if rl.allow("1.2.3.4", now.Add(30*time.Second)) {
		t.Fatal("allow() second call within the same window: want false")
	}
	if !rl.allow("1.2.3.4", now.Add(time.Minute+time.Second)) {
		t.Error("allow() call in a fresh window: want true")
	}
}

func TestRateLimiter_Prune_RemovesOnlyExpiredWindows(t *testing.T) {
	rl := NewRateLimiter(1, time.Minute)
	now := time.Now()

	rl.allow("expired", now.Add(-2*time.Minute)) // window already ended
	rl.allow("still-active", now)                // window still open

	rl.Prune(now)

	rl.mu.Lock()
	_, expiredStillTracked := rl.counters["expired"]
	_, activeStillTracked := rl.counters["still-active"]
	rl.mu.Unlock()

	if expiredStillTracked {
		t.Error("Prune() left an expired window's key tracked")
	}
	if !activeStillTracked {
		t.Error("Prune() removed a still-active window's key")
	}
}

func TestRateLimiter_Middleware_BlocksOverLimit(t *testing.T) {
	rl := NewRateLimiter(2, time.Minute)
	called := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	})
	handler := rl.Middleware()(next)

	newReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		req.RemoteAddr = "203.0.113.7:54321"
		return req
	}

	for i := range 2 {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, newReq())
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want %d", i+1, w.Code, http.StatusOK)
		}
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newReq())
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("request over the limit: status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}
	if called != 2 {
		t.Errorf("wrapped handler was called %d times, want 2 (the third request must never reach it)", called)
	}
}

func TestRateLimiter_Middleware_DifferentIPsAreIndependent(t *testing.T) {
	rl := NewRateLimiter(1, time.Minute)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := rl.Middleware()(next)

	reqFrom := func(remoteAddr string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		req.RemoteAddr = remoteAddr
		return req
	}

	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, reqFrom("203.0.113.7:1"))
	if w1.Code != http.StatusOK {
		t.Fatalf("first client's first request: status = %d, want %d", w1.Code, http.StatusOK)
	}

	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, reqFrom("198.51.100.9:2"))
	if w2.Code != http.StatusOK {
		t.Errorf("second client's first request: status = %d, want %d (must not share the first client's budget)", w2.Code, http.StatusOK)
	}
}

func TestClientIP_StripsPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:54321"

	if got := clientIP(req); got != "203.0.113.7" {
		t.Errorf("clientIP() = %q, want %q", got, "203.0.113.7")
	}
}

func TestClientIP_FallsBackToRawRemoteAddr_WhenNotHostPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "not-a-host-port"

	if got := clientIP(req); got != "not-a-host-port" {
		t.Errorf("clientIP() = %q, want the raw RemoteAddr %q", got, "not-a-host-port")
	}
}
