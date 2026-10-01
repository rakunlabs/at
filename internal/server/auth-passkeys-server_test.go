package server

import (
	"context"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/nativeauth/nativeauthtest"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

// A software ES256 authenticator: private key exists only in test memory. Both
// packed self-attestation and assertions are signed, using no verifier mocks.

// Observe the server-held deadline at both admissions; optionally delay the
// second admission until it expires without cancelling the request context.

func TestNativePasskeyProductionBasePath(t *testing.T) {
	p := postgrestest.New(t, nil)
	// Claim the installation: passkey capability is only advertised once the
	// deployment has a first administrator and a live policy version.
	_, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "reader", PasswordHash: nativeauthtest.PasswordHash}, true)
	if err != nil {
		t.Fatal(err)
	}
	cfg := nativeauthtest.Config()
	cfg.Workspace = &config.Workspace{Root: t.TempDir(), TTLHours: -1}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s, err := New(ctx, cfg, nil, p, "postgres", nil, nil, "test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	w := nativeauthtest.Request(s.server, "GET", "/at/auth/status", "", "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"passkeys":true`) || !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
	session := nativeauthtest.LoginCookie(t, s.server, "reader")
	challenge, ceremony := nativeauthtest.PasskeyBegin(t, s.server, true, session, false)
	k := nativeauthtest.NewSoftwarePasskey(t)
	w = nativeauthtest.PasskeyRequest(s.server, "/at/auth/passkeys/enroll/finish", k.Response(t, true, challenge, cfg.NativeAuth.Origin, "at.example", "", 0x45, 0, false), cfg.NativeAuth.Origin, ceremony, session)
	if w.Code != 204 {
		t.Fatalf("production enrollment: %d %s", w.Code, w.Body)
	}
	challenge, ceremony = nativeauthtest.PasskeyBegin(t, s.server, false, nil, true)
	w = nativeauthtest.PasskeyRequest(s.server, "/at/auth/passkeys/login/finish", k.Response(t, false, challenge, cfg.NativeAuth.Origin, "at.example", "", 5, 1, false), cfg.NativeAuth.Origin, ceremony)
	if w.Code != 200 {
		t.Fatalf("production assertion: %d %s", w.Code, w.Body)
	}
	if w := nativeauthtest.Request(s.server, "GET", "/auth/passkeys", "", "", session); w.Code == 200 {
		t.Fatal("unprefixed endpoint exposed")
	}
}
