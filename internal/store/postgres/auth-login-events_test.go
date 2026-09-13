package postgres

import (
	"testing"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

func TestAuthLoginEventRetentionPreservesLastLogin(t *testing.T) {
	p := newTestStore(t, nil)
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "audit-user", PasswordHash: "hash"}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"login_success", "login_failed", "login_locked"} {
		if err := p.RecordAuthLoginEvent(t.Context(), service.AuthLoginEvent{UserID: u.ID, Action: action, SourceIP: "198.51.100.2", UserAgent: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	last, err := p.ListAuthLastLogins(t.Context(), []string{u.ID})
	if err != nil || last[u.ID].At.IsZero() || last[u.ID].SourceIP != "198.51.100.2" {
		t.Fatalf("last login: %v %v", last, err)
	}
	if _, err := p.goqu.Update(p.authSecurityTable("auth_login_events")).Set(goqu.Record{"created_at": goqu.L("clock_timestamp() - interval '91 days'")}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := p.CleanupAuthLoginEvents(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	events, err := p.ListAuthLoginEvents(t.Context(), u.ID, 100)
	if err != nil || len(events) != 2 {
		t.Fatalf("bounded cleanup: %d %v", len(events), err)
	}
	if err := p.CleanupAuthLoginEvents(t.Context(), 100); err != nil {
		t.Fatal(err)
	}
	events, err = p.ListAuthLoginEvents(t.Context(), u.ID, 100)
	if err != nil || len(events) != 0 {
		t.Fatalf("cleanup: %d %v", len(events), err)
	}
	after, err := p.ListAuthLastLogins(t.Context(), []string{u.ID})
	if err != nil || after[u.ID] != last[u.ID] {
		t.Fatal("retention erased last successful login")
	}
}
