package auth

import (
	"net/http"
	"net/url"
	"strings"
)

// CrossSite reports whether an unsafe request came from somewhere other than
// Silt's own pages.
//
// The session cookie is SameSite=Lax, which already stops a cross-site form
// POST from carrying it. This is the second line, for the cases Lax does not
// cover on its own: a browser too old to enforce it, and a request from a
// page on the same site but a different origin — http against https, or
// another service on the same host under a different port.
//
// Sec-Fetch-Site is the reliable signal where it exists; Origin is the
// fallback. A request with neither is allowed through, because that is what a
// curl or a script looks like and Silt's API is meant to be usable from one —
// those carry no ambient cookie, so there is no confused deputy to exploit.
func CrossSite(r *http.Request, allowedOrigins []string) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}

	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return false
	case "cross-site", "same-site":
		return true
	}

	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" || origin == "null" {
		// No Origin at all: not a browser page request.
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return true
	}
	if strings.EqualFold(parsed.Host, r.Host) {
		return false
	}
	for _, allowed := range allowedOrigins {
		if allowed == "" {
			continue
		}
		if strings.EqualFold(strings.TrimRight(allowed, "/"), strings.TrimRight(origin, "/")) {
			return false
		}
	}
	return true
}

// SecureRequest reports whether the request reached Silt over TLS.
//
// X-Forwarded-Proto is believed because the documented deployment terminates
// TLS at a reverse proxy, so it is the only way Silt can know. It decides
// nothing but the Secure flag on a cookie, so a client that lies about it only
// makes its own cookie stricter.
func SecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if idx := strings.Index(proto, ","); idx >= 0 {
		proto = proto[:idx]
	}
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

// SecurityHeaders sets the response headers every page gets.
//
// The content security policy is tight because it can be: Silt serves one
// self-contained bundle from its own origin and talks to nothing else. Styles
// need 'unsafe-inline' — Svelte writes inline style attributes for its
// transitions and the density strip sizes its canvas that way — but scripts do
// not, which is the half that matters.
func SecurityHeaders(next http.Handler) http.Handler {
	const policy = "default-src 'self'; " +
		"script-src 'self'; " +
		"style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data:; " +
		"font-src 'self' data:; " +
		"connect-src 'self'; " +
		"form-action 'self'; " +
		"frame-ancestors 'none'; " +
		"base-uri 'none'; " +
		"object-src 'none'"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", policy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		// Silt asks for none of these; saying so keeps an injected script from
		// asking on its behalf.
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		next.ServeHTTP(w, r)
	})
}
