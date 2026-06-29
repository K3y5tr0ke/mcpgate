package server

import "net/http"

// contentSecurityPolicy is the CSP for the admin console and admin API.
//
// The SPA loads only same-origin hashed assets (no inline <script>), so
// script-src can stay 'self'. style-src needs 'unsafe-inline' because React and
// Recharts set element styles at runtime; tightening this to nonces/hashes is a
// future enhancement. connect-src 'self' covers same-origin fetch *and* the
// same-origin WebSocket log stream. frame-ancestors 'none' mirrors
// X-Frame-Options: DENY for browsers that honour CSP.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"font-src 'self' data:; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"frame-ancestors 'none'; " +
	"form-action 'self'"

// securityHeaders sets defensive HTTP response headers on browser-facing
// surfaces (the admin console SPA and the admin API). It is intentionally not
// applied to the /mcp proxy routes, whose responses are forwarded verbatim.
//
// HSTS is emitted unconditionally; browsers ignore it over plain HTTP, so it is
// a no-op in local dev and takes effect only when the console is served over TLS.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		// The console needs none of these powerful features; deny them all so a
		// compromised dependency can't reach the camera/mic/geolocation APIs.
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(), payment=(), usb=()")
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		next.ServeHTTP(w, r)
	})
}
