package server

import "net/http"

func nativeSPAFrameProtection(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Separate CSP policies are enforced together, preserving any existing
		// resource directives without restricting the SPA's scripts or styles.
		w.Header().Add("Content-Security-Policy", "frame-ancestors 'none'")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
