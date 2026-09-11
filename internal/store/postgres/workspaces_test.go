package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

func workspaceFixture(t *testing.T) (*Postgres, context.Context, *service.Workspace, *service.AuthUser) {
	t.Helper()
	p := newTestStore(t, nil)
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "workspace-admin", PasswordHash: "hash", Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx := service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: u.ID})
	w, err := p.CreateWorkspace(ctx, "Alpha", u.ID)
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := p.ResolveWorkspaceAccess(ctx, w.ID, u.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	return p, service.WithAccessPrincipal(ctx, a), w, u
}
func workspaceUser(t *testing.T, p *Postgres, name string) *service.AuthUser {
	t.Helper()
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: name, PasswordHash: "hash"}, false)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
func workspaceMember(t *testing.T, p *Postgres, ctx context.Context, w string, u *service.AuthUser, role string) context.Context {
	t.Helper()
	if err := p.SetWorkspaceMember(ctx, service.WorkspaceMembership{WorkspaceID: w, UserID: u.ID, Role: role, Status: "active"}); err != nil {
		t.Fatal(err)
	}
	a, _, err := p.ResolveWorkspaceAccess(ctx, w, u.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	return service.WithAccessPrincipal(ctx, a)
}

func TestWorkspacePostgresIsolationPermissionsAndRevocation(t *testing.T) {
	p, ctx, w, admin := workspaceFixture(t)
	user := workspaceUser(t, p, "viewer")
	viewer := workspaceMember(t, p, ctx, w.ID, user, "viewer")
	workspaceMember(t, p, ctx, w.ID, user, "viewer")
	members, err := p.ListWorkspaceMembers(ctx)
	if err != nil || len(members) != 2 {
		t.Fatalf("duplicate membership: %+v %v", members, err)
	}
	for _, m := range members {
		if m.UserID == user.ID && m.Version != 2 {
			t.Fatalf("membership update did not version: %+v", m)
		}
	}
	other, err := p.CreateWorkspace(ctx, "Alpha", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = p.ResolveWorkspaceAccess(viewer, other.ID, user.ID, ""); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("foreign membership: %v", err)
	}
	rows, err := p.ListWorkspaces(viewer)
	if err != nil || len(rows) != 1 || rows[0].ID != w.ID {
		t.Fatalf("list leaked: %+v %v", rows, err)
	}
	b, err := p.SavePermission(ctx, service.PermissionBundle{Key: "media", Name: "Media", Keys: []string{"files.write"}, KeyPatterns: map[string][]string{"files.write": {"media/*.png"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = p.SetUserPermissions(ctx, user.ID, []string{b.ID}); err != nil {
		t.Fatal(err)
	}
	a, report, err := p.ResolveWorkspaceAccess(ctx, w.ID, user.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !a.Allows("files.write", service.AccessResource{WorkspaceID: w.ID, Path: "media/a.png"}) || a.Allows("files.write", service.AccessResource{WorkspaceID: w.ID, Path: "secret/a.png"}) {
		t.Fatal("selector not enforced")
	}
	if len(report.Sources) == 0 {
		t.Fatal("missing provenance")
	}
	if err = p.SetUserDenied(ctx, user.ID, []string{"files.write"}); err != nil {
		t.Fatal(err)
	}
	a, _, err = p.ResolveWorkspaceAccess(ctx, w.ID, user.ID, "")
	if err != nil || a.Allows("files.write", service.AccessResource{WorkspaceID: w.ID, Path: "media/a.png"}) {
		t.Fatalf("deny failed: %v", err)
	}
	foreign, _, _ := p.ResolveWorkspaceAccess(ctx, other.ID, admin.ID, "")
	foreignCtx := service.WithAccessPrincipal(ctx, foreign)
	if _, err = p.SavePermission(foreignCtx, *b); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("guessed update: %v", err)
	}
	if err = p.SetUserPermissions(foreignCtx, user.ID, []string{b.ID}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("foreign reference: %v", err)
	}
	if err = p.SetWorkspaceMember(ctx, service.WorkspaceMembership{WorkspaceID: w.ID, UserID: user.ID, Role: "viewer", Status: "revoked"}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ListPermissions(viewer); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("stale principal survived: %v", err)
	}
	if _, err = p.ListPermissions(t.Context()); !errors.Is(err, service.ErrWorkspaceRequired) {
		t.Fatalf("blank context accepted: %v", err)
	}
}

func TestWorkspacePostgresConcurrentOwnersAndInvitations(t *testing.T) {
	p, ctx, w, _ := workspaceFixture(t)
	one := workspaceUser(t, p, "one")
	two := workspaceUser(t, p, "two")
	workspaceMember(t, p, ctx, w.ID, one, "owner")
	workspaceMember(t, p, ctx, w.ID, two, "owner")
	admin, _ := service.AccessPrincipalFromContext(ctx)
	if err := p.SetWorkspaceMember(ctx, service.WorkspaceMembership{WorkspaceID: w.ID, UserID: admin.UserID, Role: "admin", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for _, u := range []*service.AuthUser{one, two} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := p.SetWorkspaceMember(ctx, service.WorkspaceMembership{WorkspaceID: w.ID, UserID: u.ID, Role: "member", Status: "active"})
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, service.ErrWorkspaceConflict) {
				t.Errorf("demotion: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("owner wins %d", wins.Load())
	}
	invitee := workspaceUser(t, p, "invitee")
	hash := strings.Repeat("a", 64)
	_, err := p.CreateWorkspaceInvitation(ctx, service.WorkspaceInvitation{WorkspaceID: w.ID, Role: "member", UserID: invitee.ID, TokenHash: hash, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	wrong := service.WithAccessPrincipal(ctx, service.AccessPrincipal{UserID: one.ID})
	if _, err = p.AcceptWorkspaceInvitation(wrong, hash); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("wrong invitee: %v", err)
	}
	accept := service.WithAccessPrincipal(ctx, service.AccessPrincipal{UserID: invitee.ID})
	wins.Store(0)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.AcceptWorkspaceInvitation(accept, hash); err == nil {
				wins.Add(1)
			} else if !errors.Is(err, service.ErrAccessDenied) {
				t.Errorf("accept: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("invitation wins %d", wins.Load())
	}
	if _, err = p.AcceptWorkspaceInvitation(accept, hash); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("replay: %v", err)
	}
}

func TestWorkspacePostgresProviderQualifiedMapping(t *testing.T) {
	p, ctx, w, _ := workspaceFixture(t)
	u := workspaceUser(t, p, "mapped")
	workspaceMember(t, p, ctx, w.ID, u, "viewer")
	// Persist verified link metadata in the same shape as the external coordinator.
	for _, id := range []string{"provider-a", "provider-b"} {
		if _, err := p.goqu.Insert(p.workspaceTable("auth_identity_providers")).Rows(goqu.Record{"id": id, "enabled": true, "config": "{}"}).Executor().ExecContext(ctx); err != nil {
			t.Fatal(err)
		}
	}
	assertions, _ := json.Marshal(map[string]any{"provider_id": "provider-a", "claims": map[string]any{"groups": []string{"editors"}}})
	if _, err := p.goqu.Insert(p.workspaceTable("auth_identity_links")).Rows(goqu.Record{"id": "link", "provider_id": "provider-a", "issuer": "https://a.test", "subject": "sub", "user_id": u.ID, "email": "verified@example.test", "email_verified": true, "asserted_permissions": string(assertions)}).Executor().ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	b, err := p.SavePermission(ctx, service.PermissionBundle{Key: "edit", Keys: []string{"tasks.write"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.SavePermissionMapping(ctx, service.PermissionMapping{ProviderID: "provider-b", ClaimKind: "groups", ClaimValue: "editors", PermissionID: b.ID})
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := p.ResolveWorkspaceAccess(ctx, w.ID, u.ID, "")
	if err != nil || a.Allows("tasks.write", service.AccessResource{WorkspaceID: w.ID}) {
		t.Fatalf("cross provider grant: %v", err)
	}
	_, err = p.SavePermissionMapping(ctx, service.PermissionMapping{ProviderID: "provider-a", ClaimKind: "groups", ClaimValue: "editors", PermissionID: b.ID})
	if err != nil {
		t.Fatal(err)
	}
	a, report, err := p.ResolveWorkspaceAccess(ctx, w.ID, u.ID, "")
	if err != nil || !a.Allows("tasks.write", service.AccessResource{WorkspaceID: w.ID}) {
		t.Fatalf("mapping absent: %v", err)
	}
	found := false
	for _, src := range report.Sources {
		if src.Source == "mapping" && src.ProviderID == "provider-a" {
			found = true
		}
	}
	if !found {
		t.Fatal("mapping provenance absent")
	}
	if err = p.SetUserPermissions(ctx, u.ID, []string{b.ID}); err != nil {
		t.Fatal(err)
	}
	_, report, err = p.ResolveWorkspaceAccess(ctx, w.ID, u.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	direct, mapped := false, false
	for _, src := range report.Sources {
		if src.PermissionID == b.ID {
			direct = direct || src.Source == "direct"
			mapped = mapped || src.Source == "mapping"
		}
	}
	if !direct || !mapped {
		t.Fatal("union lost direct or mapped provenance")
	}
	if err = p.SetUserDenied(ctx, u.ID, []string{"tasks.write"}); err != nil {
		t.Fatal(err)
	}
	a, _, err = p.ResolveWorkspaceAccess(ctx, w.ID, u.ID, "")
	if err != nil || a.Allows("tasks.write", service.AccessResource{WorkspaceID: w.ID}) {
		t.Fatalf("mapped deny failed: %v", err)
	}
	if _, err = p.goqu.Update(p.workspaceTable("auth_identity_providers")).Set(goqu.Record{"enabled": false}).Where(goqu.Ex{"id": "provider-a"}).Executor().ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	if err = p.SetUserPermissions(ctx, u.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err = p.SetUserDenied(ctx, u.ID, nil); err != nil {
		t.Fatal(err)
	}
	a, _, err = p.ResolveWorkspaceAccess(ctx, w.ID, u.ID, "")
	if err != nil || a.Allows("tasks.write", service.AccessResource{WorkspaceID: w.ID}) {
		t.Fatalf("disabled provider mapping: %v", err)
	}
}

func TestWorkspacePostgresRoleCeilingsAndExecutionSwitch(t *testing.T) {
	p, ctx, w, _ := workspaceFixture(t)
	owner := workspaceUser(t, p, "owner")
	ownerCtx := workspaceMember(t, p, ctx, w.ID, owner, "owner")
	admin := workspaceUser(t, p, "workspace-admin-only")
	adminCtx := workspaceMember(t, p, ownerCtx, w.ID, admin, "admin")
	member := workspaceUser(t, p, "member")
	memberCtx := workspaceMember(t, p, adminCtx, w.ID, member, "member")
	if _, err := p.CreateWorkspace(ownerCtx, "forbidden", owner.ID); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("owner created installation workspace: %v", err)
	}
	if err := p.SetWorkspaceMember(adminCtx, service.WorkspaceMembership{WorkspaceID: w.ID, UserID: member.ID, Role: "owner", Status: "active"}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("admin granted owner: %v", err)
	}
	if _, err := p.SavePermission(memberCtx, service.PermissionBundle{Key: "escalate", Keys: []string{"permissions.manage"}}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("member escalated: %v", err)
	}
	if _, err := p.SavePermission(ownerCtx, service.PermissionBundle{Key: "platform", Keys: []string{"platform.execute"}}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("bundled platform grant: %v", err)
	}
	if err := p.SetUserDenied(ownerCtx, admin.ID, []string{"tasks.write"}); err != nil {
		t.Fatal(err)
	}
	a, _, err := p.ResolveWorkspaceAccess(ctx, w.ID, admin.ID, "")
	if err != nil || a.Allows("tasks.write", service.AccessResource{WorkspaceID: w.ID}) {
		t.Fatalf("admin deny not effective: %v", err)
	}
	if _, err = p.SavePermission(adminCtx, service.PermissionBundle{Key: "denied-delegate", Keys: []string{"tasks.write"}}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("delegated denied capability: %v", err)
	}
	if err = p.SetUserDenied(adminCtx, admin.ID, nil); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("self removed deny outside ceiling: %v", err)
	}
	a, _, err = p.ResolveWorkspaceAccess(ctx, w.ID, member.ID, "")
	if err != nil || a.Allows("models.use", service.AccessResource{WorkspaceID: w.ID}) {
		t.Fatalf("disabled execution admitted: %v", err)
	}
	w.ExecutionEnabled = true
	if _, err = p.UpdateWorkspace(ownerCtx, *w); err != nil {
		t.Fatal(err)
	}
	a, _, err = p.ResolveWorkspaceAccess(ctx, w.ID, member.ID, "")
	if err != nil || !a.Allows("models.use", service.AccessResource{WorkspaceID: w.ID}) {
		t.Fatalf("enabled execution denied: %v", err)
	}
	w.Archived = true
	if _, err = p.UpdateWorkspace(adminCtx, *w); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("admin archived: %v", err)
	}
	if _, err = p.UpdateWorkspace(ownerCtx, *w); err != nil {
		t.Fatal(err)
	}
	if _, _, err = p.ResolveWorkspaceAccess(ctx, w.ID, member.ID, ""); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("archived admitted: %v", err)
	}
}

func TestWorkspacePostgresVerifiedEmailInvitationAndExpiry(t *testing.T) {
	p, ctx, w, _ := workspaceFixture(t)
	local := workspaceUser(t, p, "same@example.test")
	external := workspaceUser(t, p, "external")
	if _, err := p.goqu.Insert(p.workspaceTable("auth_identity_providers")).Rows(goqu.Record{"id": "email-provider", "enabled": true, "config": "{}"}).Executor().ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := p.goqu.Insert(p.workspaceTable("auth_identity_links")).Rows(goqu.Record{"id": "email-link", "provider_id": "email-provider", "issuer": "https://email.test", "subject": "sub", "user_id": external.ID, "email": "same@example.test", "email_verified": false}).Executor().ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("b", 64)
	inv, err := p.CreateWorkspaceInvitation(ctx, service.WorkspaceInvitation{WorkspaceID: w.ID, Role: "viewer", Email: "same@example.test", TokenHash: hash, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []*service.AuthUser{local, external} {
		if _, err = p.AcceptWorkspaceInvitation(service.WithAccessPrincipal(ctx, service.AccessPrincipal{UserID: u.ID}), hash); !errors.Is(err, service.ErrAccessDenied) {
			t.Fatalf("unverified/name email accepted: %v", err)
		}
	}
	if _, err = p.goqu.Update(p.workspaceTable("auth_identity_links")).Set(goqu.Record{"email_verified": true}).Where(goqu.Ex{"id": "email-link"}).Executor().ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	m, err := p.AcceptWorkspaceInvitation(service.WithAccessPrincipal(ctx, service.AccessPrincipal{UserID: external.ID}), hash)
	if err != nil || m.UserID != external.ID || m.UserID == local.ID {
		t.Fatalf("verified membership merged: %+v %v", m, err)
	}
	expired := strings.Repeat("c", 64)
	inv, err = p.CreateWorkspaceInvitation(ctx, service.WorkspaceInvitation{WorkspaceID: w.ID, Role: "viewer", UserID: local.ID, TokenHash: expired, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.goqu.Update(p.workspaceTable("workspace_invitations")).Set(goqu.Record{"expires_at": time.Now().Add(-time.Second)}).Where(goqu.Ex{"id": inv.ID}).Executor().ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = p.AcceptWorkspaceInvitation(service.WithAccessPrincipal(ctx, service.AccessPrincipal{UserID: local.ID}), expired); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("expired invitation accepted: %v", err)
	}
}

func TestWorkspacePostgresResourceReferencesAndPrivateOwner(t *testing.T) {
	p, ctx, w, admin := workspaceFixture(t)
	other, err := p.CreateWorkspace(ctx, "Other", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	user := workspaceUser(t, p, "resource-member")
	member := workspaceMember(t, p, ctx, w.ID, user, "member")
	for _, row := range []goqu.Record{{"id": "own-agent", "name": "same", "workspace_id": w.ID, "created_at": time.Now(), "updated_at": time.Now()}, {"id": "foreign-agent", "name": "same", "workspace_id": other.ID, "created_at": time.Now(), "updated_at": time.Now()}} {
		if _, err = p.goqu.Insert(p.workspaceTable("agents")).Rows(row).Executor().ExecContext(ctx); err != nil {
			t.Fatal(err)
		}
	}
	resource, err := p.GetWorkspaceAccessResource(member, "agents", "own-agent")
	if err != nil || resource.WorkspaceID != w.ID {
		t.Fatalf("own lookup: %+v %v", resource, err)
	}
	if _, err = p.GetWorkspaceAccessResource(member, "agents", "foreign-agent"); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("foreign metadata: %v", err)
	}
	if _, err = p.GetWorkspaceAccessResource(t.Context(), "agents", "own-agent"); !errors.Is(err, service.ErrWorkspaceRequired) {
		t.Fatalf("absent principal metadata: %v", err)
	}
	if _, err = p.SavePermission(ctx, service.PermissionBundle{Key: "foreign-id", Keys: []string{"agents.write"}, ResourceIDs: map[string][]string{"agents.write": {"foreign-agent"}}}); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("foreign grant reference accepted: %v", err)
	}
	if _, err = p.SavePermission(ctx, service.PermissionBundle{Key: "own-id", Keys: []string{"agents.write"}, ResourceIDs: map[string][]string{"agents.write": {"own-agent"}}}); err != nil {
		t.Fatal(err)
	}
	// "privatechat" was retired with the personal chat feature; unknown kinds
	// must resolve to nothing rather than inheriting authority from a wildcard.
	if _, err = p.GetWorkspaceAccessResource(member, "privatechat", "own-agent"); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("retired resource kind resolved: %v", err)
	}
}
