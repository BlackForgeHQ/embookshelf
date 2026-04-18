package middleware

import "net/http"

// IsHTMX reports whether the request was initiated by HTMX.
func IsHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// IsHTMXBoosted reports whether the request came from hx-boost.
func IsHTMXBoosted(r *http.Request) bool {
	return r.Header.Get("HX-Boosted") == "true"
}
