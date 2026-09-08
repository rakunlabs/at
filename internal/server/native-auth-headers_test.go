package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func TestNativeSPAFrameProtectionPreservesCSP(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Add("Content-Security-Policy", "script-src 'self' 'unsafe-inline'; frame-ancestors 'self'")
	w.Header().Add("Content-Security-Policy", "img-src https: data:")
	nativeSPAFrameProtection(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	want := []string{"script-src 'self' 'unsafe-inline'; frame-ancestors 'self'", "img-src https: data:", "frame-ancestors 'none'"}
	if !slices.Equal(w.Result().Header.Values("Content-Security-Policy"), want) || w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("CSP overwritten or framing allowed", w.Header())
	}
}

func TestNativeSPAFrameProtectionProduction(t *testing.T) {
	p := postgrestest.New(t, nil)
	for _, base := range []string{"", "/at"} {
		for _, enabled := range []bool{true, false} {
			cfg := nativeTestConfig()
			cfg.BasePath = base
			cfg.NativeAuth.Enabled = enabled
			cfg.Workspace = &config.Workspace{Root: t.TempDir(), TTLHours: -1}
			ctx, cancel := context.WithCancel(t.Context())
			s, err := New(ctx, cfg, nil, p, "postgres", nil, nil, "test", "", "")
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			for _, path := range []string{"/", "/index.html", "/ui", "/ui/mobile-authorize", "/mobile-authorize"} {
				w := nativeRequest(s.server, "GET", base+path, "", "", nil)
				if w.Code != 200 && w.Code != 301 && w.Code != 302 {
					t.Fatalf("SPA %s: %d", base+path, w.Code)
				}
				if enabled {
					if w.Header().Get("X-Frame-Options") != "DENY" || !slices.Contains(w.Header().Values("Content-Security-Policy"), "frame-ancestors 'none'") {
						t.Fatal("unprotected SPA", base+path, w.Header())
					}
				} else if w.Header().Get("X-Frame-Options") != "" || w.Header().Get("Content-Security-Policy") != "" {
					t.Fatal("changed legacy headers", base+path)
				}
			}
			w := nativeRequest(s.server, "GET", base+"/gateway/v1/health", "", "", nil)
			if w.Code != 200 || w.Header().Get("Content-Security-Policy") != "" || w.Header().Get("X-Frame-Options") != "" {
				t.Fatal("changed gateway", w.Code, w.Header())
			}
			cancel()
		}
	}
}
