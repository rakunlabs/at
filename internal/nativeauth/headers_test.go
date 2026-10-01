package nativeauth

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestNativeSPAFrameProtectionPreservesCSP(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Add("Content-Security-Policy", "script-src 'self' 'unsafe-inline'; frame-ancestors 'self'")
	w.Header().Add("Content-Security-Policy", "img-src https: data:")
	SPAFrameProtection(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	want := []string{"script-src 'self' 'unsafe-inline'; frame-ancestors 'self'", "img-src https: data:", "frame-ancestors 'none'"}
	if !slices.Equal(w.Result().Header.Values("Content-Security-Policy"), want) || w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("CSP overwritten or framing allowed", w.Header())
	}
}
