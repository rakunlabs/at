package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/nativeauth"
	"github.com/rakunlabs/at/internal/nativeauth/nativeauthtest"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func TestMobileProductionRoutes(t *testing.T) {
	p := postgrestest.New(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cfg := nativeauthtest.Config()
	cfg.Workspace = &config.Workspace{Root: t.TempDir(), TTLHours: -1}
	s, err := New(ctx, cfg, nil, p, "postgres", nil, nil, "test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// An unclaimed installation reports setup, not a usable login descriptor.
	w := nativeauthtest.Request(s.server, "GET", "/at/auth/status", "", "", nil)
	var status struct {
		SetupRequired bool           `json:"setup_required"`
		Mobile        map[string]any `json:"mobile_auth"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.SetupRequired || status.Mobile != nil {
		t.Fatal("unclaimed installation exposed mobile auth", status)
	}
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "admin", PasswordHash: nativeauthtest.PasswordHash}, true)
	if err != nil {
		t.Fatal(err)
	}
	w = nativeauthtest.Request(s.server, "GET", "/at/auth/status", "", "", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.SetupRequired || status.Mobile["version"] != float64(1) || status.Mobile["issuer"] != "https://at.example/at" || status.Mobile["callback_uri"] != "atmobile://auth/callback" {
		t.Fatal(status)
	}
	access, refresh, err := nativeauth.CredentialPair()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.CreateAuthSession(t.Context(), service.AuthSession{Hash: "mobile", UserID: u.ID, Version: u.SessionVersion, Transport: "mobile", AccessHash: nativeauth.SessionHash(access), RefreshHash: nativeauth.SessionHash(refresh), ExpiresAt: time.Now().Add(time.Hour), AccessExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/at/auth/me", "", 200},
		{"GET", "/at/api/v1/info", "", 200},
		{"GET", "/at/gateway/v1/models", "", 401},
		{"POST", "/at/auth/users", `{"username":"created-mobile","password":"a sufficiently long password"}`, 201},
		{"POST", "/at/auth/password", `{}`, 401},
	} {
		r := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
		r.Header.Set("Authorization", "Bearer "+access)
		r.Header.Set("Content-Type", "application/json")
		if tt.path == "/at/api/v1/info" {
			r.Header.Set("X-AT-Workspace-ID", "legacy-default")
		}
		w := httptest.NewRecorder()
		s.server.ServeHTTP(w, r)
		if w.Code != tt.status {
			t.Fatalf("%s %d: %s", tt.path, w.Code, w.Body)
		}
		if len(w.Result().Cookies()) != 0 {
			t.Fatal("bearer request set cookies", tt.path)
		}
	}
}
