package middleware

import (
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"
)

// RateLimiter is a simple fixed-window rate limiter keyed by client IP.
// It exists to blunt credential-stuffing / brute-force attempts against
// endpoints like login and registration — but nothing about it is
// domain-specific; callers choose which routes to apply it to via
// Middleware(), the same way they choose which routes get RequireAuth.
//
// It is intentionally an in-process, single-instance limiter: a fixed
// window per IP, held in memory, with no coordination across replicas.
// That's a real limitation for a multi-instance deployment (each replica
// enforces its own independent limit) — a distributed version would need
// a shared store (e.g. Redis) instead of the map below. Even without
// that, it still meaningfully raises the cost of an attack against a
// single instance, which is what this project currently is; see
// docs/ARCHITECTURE.md's Future Improvements.
type RateLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	counters map[string]*rateLimitWindow
}

// rateLimitWindow tracks one client's request count within its current
// fixed window.
type rateLimitWindow struct {
	count      int
	windowEnds time.Time
}

// NewRateLimiter returns a RateLimiter allowing at most limit requests
// per distinct client key within any window-long period. A client's
// count resets the moment its window ends — this is a fixed window, not
// a sliding one, so a client can in principle make up to 2*limit
// requests across a window boundary (limit at the end of one window,
// limit again right after the next begins). That imprecision is an
// accepted trade-off for the simplicity of not tracking a rolling
// history per client.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		limit:    limit,
		window:   window,
		counters: make(map[string]*rateLimitWindow),
	}
}

// Middleware returns a Middleware that responds 429 once its client's IP
// has made more than limit requests within the current window, and
// otherwise lets the request through (counting it toward that window).
func (rl *RateLimiter) Middleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !rl.allow(clientIP(r), time.Now()) {
				writeTooManyRequests(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// allow reports whether a request from key is allowed at now, recording
// it against key's current window if so.
func (rl *RateLimiter) allow(key string, now time.Time) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	w, ok := rl.counters[key]
	if !ok || now.After(w.windowEnds) {
		rl.counters[key] = &rateLimitWindow{count: 1, windowEnds: now.Add(rl.window)}
		return true
	}
	if w.count >= rl.limit {
		return false
	}
	w.count++
	return true
}

// Prune deletes every tracked key whose window has already ended, so a
// long-running process doesn't accumulate one entry per distinct client
// IP it has ever seen — the same unbounded-growth concern
// user.Service.PruneExpiredSessions addresses for sessions, applied here
// to this limiter's own in-memory state. Intended to be called
// periodically (see cmd/api/main.go's runPeriodicCleanup); safe to call
// concurrently with Middleware()'s own requests.
func (rl *RateLimiter) Prune(now time.Time) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	for key, w := range rl.counters {
		if now.After(w.windowEnds) {
			delete(rl.counters, key)
		}
	}
}

// clientIP extracts the request's client IP from RemoteAddr. It
// deliberately does not consult X-Forwarded-For or similar headers —
// those are trivially spoofable by the client unless a reverse proxy is
// configured to strip/overwrite them first, which this project does not
// currently assume anywhere. Deployed behind such a proxy, this needs to
// read that header's (trusted, proxy-set) value instead.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// writeTooManyRequests writes a 429 with the same {"error": "..."} JSON
// envelope every domain Handler uses for its own error responses.
func writeTooManyRequests(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "too many requests, please try again later"})
}
