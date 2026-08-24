package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"
)

// HeaderRequestID is the HTTP header used to read a client-supplied
// request ID and to return the (possibly newly generated) ID to the
// client.
const HeaderRequestID = "X-Request-ID"

// maxRequestIDLen bounds the length of a client-supplied request ID that
// RequestID is willing to reuse, so an unbounded header value is never
// echoed back or logged as-is.
const maxRequestIDLen = 128

type requestIDKeyType struct{}

var requestIDKey = requestIDKeyType{}

// RequestID assigns a request ID to every request: it reuses the value
// from the incoming X-Request-ID header when the client supplied one and
// it looks well-formed, and otherwise generates a fresh one with
// crypto/rand. The ID is stored in the request context (retrievable via
// RequestIDFromContext) and echoed back in the X-Request-ID response
// header, so callers can correlate their request with server-side logs.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if !isValidRequestID(id) {
			id = newRequestID()
		}

		w.Header().Set(HeaderRequestID, id)

		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFromContext returns the request ID stored in ctx by RequestID,
// and false if none is present (e.g. RequestID was not part of the
// middleware chain for this request).
func RequestIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(requestIDKey).(string)
	return id, ok
}

// isValidRequestID reports whether id is a client-supplied request ID that
// is safe and reasonable to reuse: non-empty, bounded in length, and
// restricted to characters that rule out header or log injection (no
// control characters, no newlines, no arbitrary bytes).
func isValidRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLen {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// newRequestID generates a request ID from 16 cryptographically random
// bytes, hex-encoded.
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read failing means the OS's entropy source is
		// broken — essentially never happens in practice. Fall back to a
		// timestamp-derived ID so the request still gets one instead of
		// the middleware panicking.
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
