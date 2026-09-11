package postgres

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

func authSettingsFixture(t *testing.T) (*Postgres, service.AuthSettings) {
	t.Helper()
	p := newTestStore(t, nil)
	v := service.DefaultAuthSettings()
	state, err := p.InitializeAuthSettings(t.Context(), v)
	if err != nil || state == nil || !state.SetupRequired {
		t.Fatalf("initialize: %+v %v", state, err)
	}
	v.Origin = "https://at.example"
	return p, v
}

func TestAuthSettingsSetupAtomicAndPermanent(t *testing.T) {
	p, v := authSettingsFixture(t)
	other := &Postgres{db: p.db, goqu: p.goqu, tableAuthUsers: p.tableAuthUsers, tableAuthBootstrap: p.tableAuthBootstrap}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			store := p
			if i%2 != 0 {
				store = other
			}
			u, err := store.SetupAuth(t.Context(), service.AuthUser{Username: fmt.Sprintf("admin%d", i), PasswordHash: "hash"}, v)
			if err == nil {
				winners.Add(1)
				if !u.Admin || u.PasswordHash != "hash" {
					t.Error("not a local admin")
				}
			} else if !errors.Is(err, service.ErrAuthConflict) {
				t.Errorf("setup: %v", err)
			}
		})
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("setup winners: %d", winners.Load())
	}
	state, err := other.GetAuthSettings(t.Context())
	if err != nil || state.SetupRequired || state.Settings.Origin != v.Origin || state.Settings.Version != 2 {
		t.Fatalf("state: %+v %v", state, err)
	}
	if _, err := p.goqu.Delete(p.tableAuthUsers).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	state, err = other.GetAuthSettings(t.Context())
	if err != nil || state.SetupRequired {
		t.Fatalf("deleted admin reopened setup: %+v %v", state, err)
	}
	if _, err := p.SetupAuth(t.Context(), service.AuthUser{Username: "again", PasswordHash: "hash"}, state.Settings); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("reopened: %v", err)
	}
}

func TestAuthSettingsSetupRollbackAndOriginPin(t *testing.T) {
	p, v := authSettingsFixture(t)
	if _, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "taken", PasswordHash: "hash"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := p.SetupAuth(t.Context(), service.AuthUser{Username: "taken", PasswordHash: "hash"}, v); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatal(err)
	}
	state, err := p.GetAuthSettings(t.Context())
	if err != nil || !state.SetupRequired || state.Settings.Origin != "" || state.Settings.Version != 1 {
		t.Fatalf("partial claim: %+v %v", state, err)
	}
	// Emulate an operator-pinned pre-setup import.
	if _, err := p.goqu.Update(p.externalTable("auth_settings")).Set(goqu.Record{"config": goqu.L("jsonb_set(config, '{origin}', to_jsonb(?::text))", v.Origin)}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	bad := v
	bad.Origin = "https://evil.example"
	if _, err := p.SetupAuth(t.Context(), service.AuthUser{Username: "first", PasswordHash: "hash"}, bad); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("origin pin overwritten: %v", err)
	}
	if _, err := p.SetupAuth(t.Context(), service.AuthUser{Username: "first", PasswordHash: "hash"}, v); err != nil {
		t.Fatal(err)
	}
}

func TestAuthSettingsVersionImportAndSessionExpiry(t *testing.T) {
	p, v := authSettingsFixture(t)
	u, err := p.SetupAuth(t.Context(), service.AuthUser{Username: "admin", PasswordHash: "hash"}, v)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	if err := p.CreateAuthSession(t.Context(), service.AuthSession{Hash: "family", UserID: u.ID, ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}
	state, _ := p.GetAuthSettings(t.Context())
	v = state.Settings
	v.SessionTTLSeconds = 600
	saved, err := p.SaveAuthSettings(t.Context(), v)
	if err != nil || saved.Version != 3 {
		t.Fatalf("save: %+v %v", saved, err)
	}
	if _, err := p.SaveAuthSettings(t.Context(), v); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("stale write: %v", err)
	}
	v = *saved
	v.Origin = "https://evil.example"
	if _, err := p.SaveAuthSettings(t.Context(), v); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("origin edit: %v", err)
	}
	imported, err := p.InitializeAuthSettings(t.Context(), service.AuthSettings{})
	if err != nil || imported.Settings.SessionTTLSeconds != 600 {
		t.Fatalf("restart overwrote DB: %+v %v", imported, err)
	}
	_, got, err := p.ResolveAuthSession(t.Context(), "family")
	if err != nil || !got.Equal(expires) {
		t.Fatalf("session expiry changed: %v %v", got, err)
	}
}

func TestAuthSettingsExternalAdminLockoutGuard(t *testing.T) {
	p, v := authSettingsFixture(t)
	u, err := p.SetupAuth(t.Context(), service.AuthUser{Username: "admin", PasswordHash: "hash"}, v)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := p.GetAuthSettings(t.Context())
	v = state.Settings
	v.LocalLoginEnabled = false
	if _, err := p.SaveAuthSettings(t.Context(), v); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("disabled last primary: %v", err)
	}
	// A configured provider alone is insufficient: a live admin must be linked.
	providers, links := p.externalTable("auth_identity_providers"), p.externalTable("auth_identity_links")
	if _, err := p.goqu.Insert(providers).Rows(goqu.Record{"id": "idp", "enabled": true, "config": "{}"}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.SaveAuthSettings(t.Context(), v); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("unlinked provider bypass: %v", err)
	}
	if _, err := p.goqu.Insert(links).Rows(goqu.Record{"id": "link", "provider_id": "idp", "issuer": "https://idp.example", "subject": "subject", "user_id": u.ID}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	saved, err := p.SaveAuthSettings(t.Context(), v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.goqu.Update(providers).Set(goqu.Record{"enabled": false}).Executor().ExecContext(t.Context()); err == nil {
		t.Fatal("last external provider disabled")
	}
	if _, err := p.goqu.Delete(links).Executor().ExecContext(t.Context()); err == nil {
		t.Fatal("last external link removed")
	}
	if _, err := p.goqu.Update(p.tableAuthUsers).Set(goqu.Record{"disabled": true}).Executor().ExecContext(t.Context()); err == nil {
		t.Fatal("last external admin disabled")
	}
	if err := p.CreateAuthSession(t.Context(), service.AuthSession{Hash: "late-local", UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)}); err == nil {
		t.Fatal("in-flight local session bypassed disabled login")
	}
	// Mobile creation is separately gated by live-browser approval/redemption;
	// disabling primary login does not invalidate that existing session authority.
	if err := p.CreateAuthSession(t.Context(), service.AuthSession{Hash: "approved-mobile", Transport: "mobile", UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("session-derived mobile family rejected: %v", err)
	}
	saved.LocalLoginEnabled = true
	if _, err := p.SaveAuthSettings(t.Context(), *saved); err != nil {
		t.Fatal(err)
	}
	if _, err := p.goqu.Delete(links).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestAuthSettingsClaimedImportNeedsOriginWithoutPoisoning(t *testing.T) {
	p := newTestStore(t, nil)
	if _, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "legacy", PasswordHash: "hash"}, true); err != nil {
		t.Fatal(err)
	}
	v := service.DefaultAuthSettings()
	if _, err := p.InitializeAuthSettings(t.Context(), v); err == nil {
		t.Fatal("claimed store accepted no canonical origin")
	}
	v.Origin = "https://legacy.example"
	v.SessionTTLSeconds = 1800
	state, err := p.InitializeAuthSettings(t.Context(), v)
	if err != nil || state.SetupRequired || state.Settings.SessionTTLSeconds != 1800 {
		t.Fatalf("legacy import: %+v %v", state, err)
	}
}

func TestAuthSettingsMigrationSealsLegacyLocalAdmin(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := t.Context()
	u, err := p.CreateAuthUser(ctx, service.AuthUser{Username: "imported-admin", PasswordHash: "hash", Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the exact pre-37 state and execute migration 37 through the same
	// os.Expand semantics as muz; existing user/session rows must be preserved.
	prefix := strings.TrimSuffix(fmt.Sprint(p.tableAuthUsers.GetTable()), "auth_users")
	for _, statement := range []string{
		fmt.Sprintf("DROP TABLE %sauth_settings CASCADE", prefix),
		fmt.Sprintf("DROP FUNCTION %sauth_settings_primary_guard() CASCADE", prefix),
		fmt.Sprintf("DROP FUNCTION %sauth_settings_session_guard() CASCADE", prefix),
	} {
		if _, err := p.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	body, err := migrationFS.ReadFile("migrations/37_auth_settings.sql")
	if err != nil {
		t.Fatal(err)
	}
	expanded := os.Expand(string(body), func(key string) string {
		if key == "TABLE_PREFIX" {
			return prefix
		}
		return ""
	})
	if _, err := p.db.ExecContext(ctx, expanded); err != nil {
		t.Fatal(err)
	}
	v := service.DefaultAuthSettings()
	v.Origin = "https://legacy.example"
	state, err := p.InitializeAuthSettings(ctx, v)
	if err != nil || state.SetupRequired {
		t.Fatalf("legacy admin not sealed: %+v %v", state, err)
	}
	got, err := p.GetAuthUserByID(ctx, u.ID)
	if err != nil || got == nil || got.PasswordHash != "hash" {
		t.Fatalf("legacy admin modified: %+v %v", got, err)
	}
}

func TestAuthSettingsConcurrentExternalDisable(t *testing.T) {
	p, v := authSettingsFixture(t)
	u, err := p.SetupAuth(t.Context(), service.AuthUser{Username: "admin", PasswordHash: "hash"}, v)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second"} {
		if _, err := p.goqu.Insert(p.externalTable("auth_identity_providers")).Rows(goqu.Record{"id": id, "enabled": true, "config": "{}"}).Executor().ExecContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := p.goqu.Insert(p.externalTable("auth_identity_links")).Rows(goqu.Record{"id": id, "provider_id": id, "issuer": "https://idp.example", "subject": id, "user_id": u.ID}).Executor().ExecContext(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	state, _ := p.GetAuthSettings(t.Context())
	state.Settings.LocalLoginEnabled = false
	if _, err := p.SaveAuthSettings(t.Context(), state.Settings); err != nil {
		t.Fatal(err)
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, id := range []string{"first", "second"} {
		wg.Go(func() {
			<-start
			_, err := p.goqu.Update(p.externalTable("auth_identity_providers")).Set(goqu.Record{"enabled": false}).Where(goqu.Ex{"id": id}).Executor().ExecContext(t.Context())
			if err == nil {
				successes.Add(1)
			} else if !errors.Is(authSettingsError(err), service.ErrAuthConflict) {
				t.Errorf("disable: %v", err)
			}
		})
	}
	close(start)
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("concurrent disables = %d; wanted exactly one", successes.Load())
	}
}
