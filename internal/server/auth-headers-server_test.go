package server

import (
	"context"
	"slices"
	"testing"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/nativeauth/nativeauthtest"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func TestNativeSPAFrameProtectionProduction(t *testing.T) {
	p := postgrestest.New(t, nil)
	for _, base := range []string{"", "/at"} {
		for _, enabled := range []bool{true, false} {
			cfg := nativeauthtest.Config()
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
				w := nativeauthtest.Request(s.server, "GET", base+path, "", "", nil)
				if w.Code != 200 && w.Code != 301 && w.Code != 302 {
					t.Fatalf("SPA %s: %d", base+path, w.Code)
				}
				// Human auth is always native now, so the SPA is framed-protected
				// regardless of the obsolete native_auth.enabled config flag.
				_ = enabled
				if w.Header().Get("X-Frame-Options") != "DENY" || !slices.Contains(w.Header().Values("Content-Security-Policy"), "frame-ancestors 'none'") {
					t.Fatal("unprotected SPA", base+path, w.Header())
				}
			}
			w := nativeauthtest.Request(s.server, "GET", base+"/gateway/v1/health", "", "", nil)
			if w.Code != 200 || w.Header().Get("Content-Security-Policy") != "" || w.Header().Get("X-Frame-Options") != "" {
				t.Fatal("changed gateway", w.Code, w.Header())
			}
			cancel()
		}
	}
}
