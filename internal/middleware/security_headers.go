package middleware

import (
	"fmt"
	"net/http"
	"time"
)

// The response security headers this API sets unconditionally.
//
// cspAPIPolicy is deliberately the tightest policy CSP can express, not an
// adaptation of the usual web-application advice: this API only ever
// returns JSON, so there is no document here that legitimately loads a
// script, stylesheet, image or font. "default-src 'none'" denies every
// fetch directive at once; a policy naming script-src/style-src instead
// would be strictly looser while protecting nothing extra. It matters
// despite the JSON-only content type because a browser coaxed into
// rendering a response as a document (a MIME-sniffing bug, a
// Content-Type an intermediary rewrote) finds a policy that forbids
// executing anything it may have found inside.
//
// frame-ancestors 'none' and X-Frame-Options: DENY say the same thing to
// two generations of browser — frame-ancestors is the modern form and the
// one that wins where both are understood, X-Frame-Options the fallback
// for clients that never implemented it. Sending both costs one header.
const (
	cspAPIPolicy       = "default-src 'none'; frame-ancestors 'none'"
	frameOptionsDeny   = "DENY"
	contentTypeNoSniff = "nosniff"
)

// SecurityHeaders returns a Middleware that sets the response security
// headers appropriate for a JSON API on every response.
//
// Always set: Content-Security-Policy, X-Frame-Options and
// X-Content-Type-Options (see the constants above for what each one buys a
// JSON API specifically).
//
// Strict-Transport-Security is set only when hstsMaxAge is positive, and
// is therefore off by default. That is not an oversight:
//
//   - Per RFC 6797 §7.2 a user agent MUST ignore an HSTS header received
//     over anything but a secure transport, and this binary always serves
//     plain HTTP — the scratch image carries no certificates, and every
//     real deployment terminates TLS at a proxy in front of it. Sending
//     the header unconditionally would therefore be inert exactly where
//     it is emitted and meaningful only on a hop this process cannot
//     observe, so only the operator can know whether it is truthful.
//   - HSTS is close to a one-way door. A client that has cached
//     max-age=31536000 refuses plaintext to this host for a year, and the
//     only way to walk that back is to keep serving HTTPS with max-age=0
//     until every client has been seen again. Turning that on for someone
//     by default — including for anyone running this locally — is not a
//     default's decision to make.
//
// The value is emitted as a bare max-age with neither includeSubDomains
// nor preload. Both reach past this service to every host under the
// domain, and preload is in practice irreversible; an operator who wants
// them can add them at the same proxy that terminates the TLS they
// describe, whereas hardcoding them here would leave an operator who
// wants HSTS for this host alone no way to say so.
//
// The headers are set on the way in, before the wrapped handler runs, so
// they are already on the ResponseWriter for every response that never
// reaches a handler at all — a CORS preflight answered by CORS itself,
// a 500 written by Recovery after a panic, a 404/405 from the router.
func SecurityHeaders(hstsMaxAge time.Duration) Middleware {
	// Precomputed once rather than formatted per request: the value is
	// fixed for the process's lifetime.
	var hstsValue string
	if hstsMaxAge > 0 {
		hstsValue = fmt.Sprintf("max-age=%d", int64(hstsMaxAge.Seconds()))
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", cspAPIPolicy)
			h.Set("X-Frame-Options", frameOptionsDeny)
			h.Set("X-Content-Type-Options", contentTypeNoSniff)
			if hstsValue != "" {
				h.Set("Strict-Transport-Security", hstsValue)
			}

			next.ServeHTTP(w, r)
		})
	}
}
