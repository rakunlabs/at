package postgres

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

func TestNativeAuthUserAdministration(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := t.Context()
	admin, err := p.CreateAuthUser(ctx, service.AuthUser{Username: "admin", PasswordHash: "admin-hash"}, true)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := p.CreateAuthUser(ctx, service.AuthUser{Username: "reader", PasswordHash: "reader-hash"}, false)
	if err != nil {
		t.Fatal(err)
	}
	page, err := p.ListAuthUsers(ctx, service.AuthUserQuery{Limit: 1})
	if err != nil || len(page) != 1 || page[0].ID != admin.ID || page[0].PasswordHash != "" || page[0].SessionVersion != 0 {
		t.Fatalf("first page: %+v %v", page, err)
	}
	page, err = p.ListAuthUsers(ctx, service.AuthUserQuery{After: page[0].ID, Limit: 101})
	if err != nil || len(page) != 1 || page[0].ID != reader.ID || page[0].Admin {
		t.Fatalf("second page: %+v %v", page, err)
	}
	page, err = p.ListAuthUsers(ctx, service.AuthUserQuery{After: reader.ID, Limit: 1})
	if err != nil || page == nil || len(page) != 0 {
		t.Fatalf("empty page: %+v %v", page, err)
	}
	for _, limit := range []uint{0, 102} {
		if _, err := p.ListAuthUsers(ctx, service.AuthUserQuery{Limit: limit}); err == nil {
			t.Fatal("unbounded list allowed")
		}
	}
	if u, err := p.GetAuthUserByID(ctx, "missing"); err != nil || u != nil {
		t.Fatalf("missing ID: %+v %v", u, err)
	}
	if found, err := p.EnableAuthUser(ctx, "missing"); err != nil || found {
		t.Fatalf("enable missing: %v %v", found, err)
	}
	if found, err := p.SetAuthUserPassword(ctx, "missing", "hash", nil); err != nil || found {
		t.Fatalf("reset missing: %v %v", found, err)
	}
	if _, err := p.SetAuthUserPassword(ctx, reader.ID, "", nil); err == nil {
		t.Fatal("empty hash accepted")
	}
	s := service.AuthSession{Hash: "reader-session", UserID: reader.ID, Version: reader.SessionVersion, ExpiresAt: time.Now().Add(time.Hour)}
	if err := p.CreateAuthSession(ctx, s); err != nil {
		t.Fatal(err)
	}
	if found, err := p.SetAuthUserPassword(ctx, reader.ID, "new-hash", &reader.SessionVersion); err != nil || !found {
		t.Fatalf("self change: %v %v", found, err)
	}
	if _, err := p.SetAuthUserPassword(ctx, reader.ID, "stale-hash", &reader.SessionVersion); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("stale password verification accepted: %v", err)
	}
	u, err := p.GetAuthUserByID(ctx, reader.ID)
	if err != nil || u == nil || u.PasswordHash != "new-hash" || u.SessionVersion != 1 || u.Disabled || u.Admin {
		t.Fatalf("changed user: %+v %v", u, err)
	}
	if count, err := p.goqu.From(p.tableAuthSessions).Where(goqu.Ex{"user_id": reader.ID}).CountContext(ctx); err != nil || count != 0 {
		t.Fatalf("sessions not deleted: %d %v", count, err)
	}
	if err := p.CreateAuthSession(ctx, s); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("password reset stale issuer: %v", err)
	}
	if _, err := p.InvalidateAuthUser(ctx, reader.ID, true); err != nil {
		t.Fatal(err)
	}
	u, err = p.GetAuthUserByID(ctx, reader.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.SetAuthUserPassword(ctx, reader.ID, "self-hash", &u.SessionVersion); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("disabled self change: %v", err)
	}
	if _, err := p.SetAuthUserPassword(ctx, reader.ID, "admin-reset", nil); err != nil {
		t.Fatal(err)
	}
	u, err = p.GetAuthUserByID(ctx, reader.ID)
	if err != nil || !u.Disabled || u.PasswordHash != "admin-reset" || u.SessionVersion != 3 {
		t.Fatalf("reset enabled disabled user: %+v %v", u, err)
	}
	if _, err := p.EnableAuthUser(ctx, reader.ID); err != nil {
		t.Fatal(err)
	}
	u, err = p.GetAuthUserByID(ctx, reader.ID)
	if err != nil || u.Disabled || u.Admin || u.PasswordHash != "admin-reset" || u.SessionVersion != 4 {
		t.Fatalf("enable: %+v %v", u, err)
	}
	if resolved, _, err := p.ResolveAuthSession(ctx, s.Hash); err != nil || resolved != nil {
		t.Fatalf("enable revived session: %+v %v", resolved, err)
	}
	s.Version = u.SessionVersion
	if err := p.CreateAuthSession(ctx, s); err != nil {
		t.Fatal(err)
	}
	if _, err := p.EnableAuthUser(ctx, reader.ID); err != nil {
		t.Fatal(err)
	}
	if resolved, _, err := p.ResolveAuthSession(ctx, s.Hash); err != nil || resolved != nil {
		t.Fatalf("enable active user failed to revoke: %+v %v", resolved, err)
	}
	// Resetting the last administrator keeps its role and active state. Disabling
	// that account must still be blocked after both reset and re-enable.
	if _, err := p.SetAuthUserPassword(ctx, admin.ID, "new-admin-hash", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := p.EnableAuthUser(ctx, admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.InvalidateAuthUser(ctx, admin.ID, true); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("last admin protection lost: %v", err)
	}
}

// Search and deletion are the two administrator operations that reach outside
// auth_users: search joins the identity link an external account is named by,
// and deletion has to sweep every table keyed on the account, including the
// ones whose foreign key does not cascade.
func TestNativeAuthUserSearchDirectoryAndDeletion(t *testing.T) {
	p, ctx, w, admin := workspaceFixture(t)
	external := workspaceUser(t, p, "external-01hzzz")
	workspaceMember(t, p, ctx, w.ID, external, "member")
	if _, err := p.goqu.Insert(p.workspaceTable("auth_identity_providers")).Rows(goqu.Record{"id": "keycloak", "enabled": true, "config": "{}"}).Executor().ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := p.goqu.Insert(p.workspaceTable("auth_identity_links")).Rows(goqu.Record{"id": "kc-link", "provider_id": "keycloak", "issuer": "oauth2:keycloak", "subject": "sub-1", "user_id": external.ID, "email": "Ada@Example.COM", "email_verified": true, "asserted_permissions": "{}"}).Executor().ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	// The generated username carries no information, so the email on the link is
	// the only searchable name such an account has.
	for _, term := range []string{"ada@example", "ADA@EXAMPLE.com", "external-01h", external.ID} {
		page, err := p.ListAuthUsers(ctx, service.AuthUserQuery{Search: term, Limit: 10})
		if err != nil || len(page) != 1 || page[0].ID != external.ID {
			t.Fatalf("search %q: %+v %v", term, page, err)
		}
	}
	if page, err := p.ListAuthUsers(ctx, service.AuthUserQuery{Search: "workspace-admin", Limit: 10}); err != nil || len(page) != 1 || page[0].ID != admin.ID {
		t.Fatalf("username search: %+v %v", page, err)
	}
	// Metacharacters are matched literally: a pattern would match everything.
	if page, err := p.ListAuthUsers(ctx, service.AuthUserQuery{Search: "%", Limit: 10}); err != nil || len(page) != 0 {
		t.Fatalf("wildcard search: %+v %v", page, err)
	}
	identities, err := p.ListAuthUserIdentities(ctx, []string{external.ID, admin.ID})
	if err != nil || len(identities[external.ID]) != 1 || identities[external.ID][0].Email != "Ada@Example.COM" || len(identities[admin.ID]) != 0 {
		t.Fatalf("identity directory: %+v %v", identities, err)
	}
	spaces, err := p.ListAuthUserWorkspaces(ctx, external.ID)
	if err != nil || len(spaces) != 1 || spaces[0].WorkspaceID != w.ID || spaces[0].Role != "member" || spaces[0].Status != "active" {
		t.Fatalf("workspace directory: %+v %v", spaces, err)
	}
	if found, err := p.DeleteAuthUser(ctx, "missing"); err != nil || found {
		t.Fatalf("delete missing: %v %v", found, err)
	}
	if _, err := p.DeleteAuthUser(ctx, admin.ID); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("last administrator deleted: %v", err)
	}
	// A membership has no ON DELETE CASCADE, so deletion must sweep it or the
	// account cannot be removed at all.
	found, err := p.DeleteAuthUser(ctx, external.ID)
	if err != nil || !found {
		t.Fatalf("delete: %v %v", found, err)
	}
	if u, err := p.GetAuthUserByID(ctx, external.ID); err != nil || u != nil {
		t.Fatalf("account survived deletion: %+v %v", u, err)
	}
	for _, table := range []string{"workspace_memberships", "auth_identity_links", "auth_login_events"} {
		column := "user_id"
		count, err := p.goqu.From(p.workspaceTable(table)).Where(goqu.Ex{column: external.ID}).CountContext(ctx)
		if err != nil || count != 0 {
			t.Fatalf("%s retained rows: %d %v", table, count, err)
		}
	}
}

func TestNativeAuthPasswordUpdateConcurrentIssuance(t *testing.T) {
	p := newTestStore(t, nil)
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "admin", PasswordHash: "hash"}, true)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 8 {
		start := make(chan struct{})
		var wg sync.WaitGroup
		s := service.AuthSession{Hash: fmt.Sprintf("session-%d", i), UserID: u.ID, Version: int64(i), ExpiresAt: time.Now().Add(time.Hour)}
		wg.Go(func() {
			<-start
			if err := p.CreateAuthSession(t.Context(), s); err != nil && !errors.Is(err, service.ErrAuthConflict) {
				t.Error(err)
			}
		})
		wg.Go(func() {
			<-start
			if found, err := p.SetAuthUserPassword(t.Context(), u.ID, fmt.Sprintf("hash-%d", i), nil); err != nil || !found {
				t.Errorf("reset: %v %v", found, err)
			}
		})
		close(start)
		wg.Wait()
		if resolved, _, err := p.ResolveAuthSession(t.Context(), s.Hash); err != nil || resolved != nil {
			t.Fatalf("reset lost to session issuance: %+v %v", resolved, err)
		}
	}
}

func TestNativeAuthConcurrentPasswordChanges(t *testing.T) {
	p := newTestStore(t, nil)
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "admin", PasswordHash: "hash"}, true)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for i := range 2 {
		go func() {
			_, err := p.SetAuthUserPassword(t.Context(), u.ID, fmt.Sprintf("hash-%d", i), &u.SessionVersion)
			results <- err
		}()
	}
	a, b := <-results, <-results
	if !(a == nil && errors.Is(b, service.ErrAuthConflict) || b == nil && errors.Is(a, service.ErrAuthConflict)) {
		t.Fatalf("expected one winner: %v %v", a, b)
	}
}
