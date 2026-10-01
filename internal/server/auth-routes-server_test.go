package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/ada/middleware/auth/identity"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/nativeauth/nativeauthtest"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func TestNativeAuthProductionRoutesPostgres(t *testing.T) {
	store := postgrestest.New(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cfg := nativeauthtest.Config()
	cfg.Workspace = &config.Workspace{Root: t.TempDir(), TTLHours: -1}
	s, err := New(ctx, cfg, nil, store, "postgres", nil, nil, "test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		method, path string
		code         int
	}{
		{"GET", "/at/", 200}, {"GET", "/at/auth/status", 200},
		// An unclaimed installation refuses management before authentication.
		{"GET", "/at/api/v1/info", 403}, {"GET", "/at/api/v1/providers", 403},
		{"POST", "/at/api/v1/settings/rotate-key", 403}, {"POST", "/at/internal/v1/mcp/test", 403},
		{"GET", "/at/gateway/v1/health", 200},
	} {
		w := nativeauthtest.Request(s.server, tt.method, tt.path, "{}", "", nil)
		if w.Code != tt.code {
			t.Errorf("%s: %d want %d %s", tt.path, w.Code, tt.code, w.Body)
		}
	}
	if _, err := store.CreateAuthUser(ctx, service.AuthUser{Username: "admin", PasswordHash: nativeauthtest.PasswordHash}, true); err != nil {
		t.Fatal(err)
	}
	admin := nativeauthtest.LoginCookie(t, s.server, "admin")
	if w := nativeauthtest.Request(s.server, "GET", "/at/api/v1/info", "", "", admin); w.Code != http.StatusBadRequest {
		t.Fatalf("info without selected workspace: %d %s", w.Code, w.Body)
	}
	if w := nativeauthtest.Request(s.server, "GET", "/at/api/v1/info", "", "", admin, func(r *http.Request) {
		r.Header.Set("X-AT-Workspace-ID", "legacy-default")
	}); w.Code != 200 {
		t.Fatalf("admin info: %d %s", w.Code, w.Body)
	}
	if w := nativeauthtest.Request(s.server, "GET", "/at/gateway/v1/models", "", "", admin); w.Code != 401 {
		t.Fatal("management cookie authenticated gateway")
	}
	// Even a raw upstream provider sees no browser cookies in native mode.
	// The production route's request object is stripped before dispatch/auth.
	r := httptest.NewRequest("GET", "/at/gateway/v1/models", nil)
	r.AddCookie(admin)
	w := httptest.NewRecorder()
	s.server.ServeHTTP(w, r)
	if r.Header.Get("Cookie") != "" {
		t.Fatal("gateway retained management cookie")
	}
	w = nativeauthtest.Request(s.server, "POST", "/at/auth/users", `{"username":"reader","password":"`+strings.Repeat("x", 32)+`"}`, "https://at.example", admin)
	if w.Code != 201 {
		t.Fatalf("create reader: %d %s", w.Code, w.Body)
	}
	var readerID identity.Identity
	if err := json.Unmarshal(w.Body.Bytes(), &readerID); err != nil {
		t.Fatal(err)
	}
	reader := nativeauthtest.LoginCookie(t, s.server, "reader")
	// Files are a scoped runtime resource: a signed-in user with no selected
	// workspace cannot browse the host, and naming one it cannot access fails.
	if w := nativeauthtest.Request(s.server, "GET", "/at/api/v1/files/browse", "", "", reader); w.Code != 400 {
		t.Fatalf("reader file access without workspace: %d", w.Code)
	}
	scoped := nativeauthtest.Request(s.server, "GET", "/at/api/v1/files/browse", "", "", reader, func(r *http.Request) {
		r.Header.Set("X-AT-Workspace-ID", "legacy-default")
	})
	if scoped.Code != 403 && scoped.Code != 404 {
		t.Fatalf("reader file access with foreign workspace: %d %s", scoped.Code, scoped.Body)
	}
	if w := nativeauthtest.Request(s.server, "POST", "/at/auth/users/"+readerID.Subject+"/disable", "", "https://at.example", admin); w.Code != 204 {
		t.Fatalf("disable: %d %s", w.Code, w.Body)
	}
	if w := nativeauthtest.Request(s.server, "GET", "/at/auth/me", "", "", reader); w.Code != 401 {
		t.Fatal("disabled reader session remained active")
	}
	if w := nativeauthtest.Request(s.server, "POST", "/at/auth/users/"+readerID.Subject+"/revoke-sessions", "", "https://at.example", admin); w.Code != 204 {
		t.Fatalf("revoke: %d %s", w.Code, w.Body)
	}
	// Human authentication is no longer opt-in: dropping the obsolete YAML
	// block must not reopen anonymous management on a claimed installation.
	cfg.NativeAuth = nil
	legacy, err := New(ctx, cfg, nil, store, "postgres", nil, nil, "test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		method, path string
		code         int
	}{
		{"GET", "/at/api/v1/info", 401},
		{"POST", "/at/api/v1/settings/rotate-key", 401},
		{"GET", "/at/gateway/v1/models", 401},
	} {
		w := nativeauthtest.Request(legacy.server, tt.method, tt.path, "{}", "", nil)
		if w.Code != tt.code {
			t.Errorf("legacy %s: %d want %d", tt.path, w.Code, tt.code)
		}
	}
}
