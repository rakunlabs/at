package postgres

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/doug-martin/goqu/v9"

	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
)

func TestWebhookServersPostgres(t *testing.T) {
	p, ctx, w, admin := workspaceFixture(t)
	p.encKey = bytes.Repeat([]byte{7}, 32)
	other, err := p.CreateWorkspace(ctx, "Beta", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := p.ResolveWorkspaceAccess(ctx, other.ID, admin.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	ctxB := service.WithAccessPrincipal(ctx, a)

	// Members cannot manage servers.
	memberUser := workspaceUser(t, p, "webhook-member")
	member := workspaceMember(t, p, ctx, w.ID, memberUser, "admin")
	if _, err := p.CreateWebhookServer(member, service.WebhookServer{Name: "nope", Port: 5050}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("member created a server: %v", err)
	}

	srv, err := p.CreateWebhookServer(ctx, service.WebhookServer{Name: "hooks", Port: 5050, Enabled: true, WorkspaceIDs: []string{w.ID}, AllowedCIDRs: []string{"10.0.0.0/8", " 127.0.0.1 "}, BasePath: "/in/"})
	if err != nil {
		t.Fatal(err)
	}
	if srv.BasePath != "/in" || len(srv.AllowedCIDRs) != 2 || srv.AllowedCIDRs[1] != "127.0.0.1" {
		t.Fatalf("not normalized: %+v", srv)
	}
	if _, err := p.CreateWebhookServer(ctx, service.WebhookServer{Name: "dup", Port: 5050}); !errors.Is(err, service.ErrWebhookServerConflict) {
		t.Fatalf("duplicate address accepted: %v", err)
	}
	if _, err := p.CreateWebhookServer(ctx, service.WebhookServer{Name: "low", Port: 80}); !errors.Is(err, service.ErrWebhookServerConflict) {
		t.Fatalf("privileged port accepted: %v", err)
	}
	var raw string
	if _, err := p.goqu.From(p.workspaceTable("webhook_servers")).Select("config").Where(goqu.Ex{"id": srv.ID}).ScanValContext(ctx, &raw); err != nil || !atcrypto.IsEncrypted(raw) {
		t.Fatalf("server settings not encrypted: %q %v", raw, err)
	}

	// Member view is limited to servers open to the selected workspace.
	visible, err := p.ListWebhookServers(member)
	if err != nil || len(visible) != 1 || len(visible[0].WorkspaceIDs) != 0 || len(visible[0].AllowedCIDRs) != 0 {
		t.Fatalf("member view: %+v %v", visible, err)
	}
	if hidden, err := p.ListWebhookServers(ctxB); err != nil || len(hidden) != 1 {
		// ctxB is a platform administrator; members of B would see none.
		t.Fatalf("admin view: %+v %v", hidden, err)
	}

	graph := service.WorkflowGraph{Nodes: []service.WorkflowNode{{ID: "in", Type: "input"}}}
	wf, err := p.CreateWorkflow(ctx, service.Workflow{Name: "hooked", Graph: graph})
	if err != nil {
		t.Fatal(err)
	}
	wfB, err := p.CreateWorkflow(ctxB, service.Workflow{Name: "hooked", Graph: graph})
	if err != nil {
		t.Fatal(err)
	}

	// A workspace the server is not open to cannot bind to it.
	if _, err := p.CreateTrigger(ctxB, service.Trigger{Type: "http", TargetID: wfB.ID, Enabled: true, WebhookRoutes: []service.WebhookRoute{{ServerID: srv.ID}}}); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("foreign workspace bound to server: %v", err)
	}
	if _, err := p.CreateTrigger(ctx, service.Trigger{Type: "http", TargetID: wf.ID, HideFromMain: true}); !errors.Is(err, service.ErrWorkspaceConflict) {
		t.Fatalf("hidden trigger without servers accepted: %v", err)
	}

	tr, err := p.CreateTrigger(ctx, service.Trigger{
		Type: "http", TargetID: wf.ID, Enabled: true, Alias: "orders", HideFromMain: true,
		Config:        map[string]any{"methods": []any{"post", "PUT"}},
		WebhookRoutes: []service.WebhookRoute{{ServerID: srv.ID, Path: "/github/push/"}},
		Signature:     &service.WebhookSignature{Scheme: "github", Secret: "s3cret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Signature == nil || tr.Signature.Secret != service.WebhookSecretSentinel || tr.WebhookRoutes[0].Path != "github/push" {
		t.Fatalf("created trigger: %+v", tr)
	}
	got, err := p.GetTrigger(ctx, tr.ID)
	if err != nil || !got.HideFromMain || len(got.WebhookRoutes) != 1 || got.Signature.Header != "X-Hub-Signature-256" {
		t.Fatalf("read back: %+v %v", got, err)
	}

	// A second trigger cannot take the same custom path.
	if _, err := p.CreateTrigger(ctx, service.Trigger{Type: "http", TargetID: wf.ID, WebhookRoutes: []service.WebhookRoute{{ServerID: srv.ID, Path: "github/push"}}}); !errors.Is(err, service.ErrWebhookRouteConflict) {
		t.Fatalf("duplicate path accepted: %v", err)
	}

	// Routing is machine metadata resolved without a principal.
	anon := t.Context()
	match, err := p.ResolveWebhookRoute(anon, srv.ID, "github/push")
	if err != nil || match == nil || match.TriggerID != tr.ID || match.Signature == nil || match.Signature.Secret != "s3cret" {
		t.Fatalf("custom path route: %+v %v", match, err)
	}
	if strings.Join(match.Methods, ",") != "POST,PUT" {
		t.Fatalf("methods: %v", match.Methods)
	}
	if m, _ := p.ResolveWebhookRoute(anon, srv.ID, "orders"); m != nil {
		t.Fatal("custom path must replace alias routing on that server")
	}
	if m, err := p.ResolveMainWebhookRoute(anon, "orders"); err != nil || m == nil || !m.HideFromMain {
		t.Fatalf("main route metadata: %+v %v", m, err)
	}

	// Default binding resolves by alias and by ID.
	tr2, err := p.CreateTrigger(ctx, service.Trigger{Type: "http", TargetID: wf.ID, Enabled: true, Alias: "plain", WebhookRoutes: []service.WebhookRoute{{ServerID: srv.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"plain", tr2.ID} {
		if m, err := p.ResolveWebhookRoute(anon, srv.ID, key); err != nil || m == nil || m.TriggerID != tr2.ID {
			t.Fatalf("default route %s: %+v %v", key, m, err)
		}
	}

	// Update without routes/signature preserves them; sentinel keeps secret.
	got.Public = true
	got.WebhookRoutes, got.Signature = nil, nil
	if _, err := p.UpdateTrigger(ctx, tr.ID, *got); err != nil {
		t.Fatal(err)
	}
	if m, _ := p.ResolveWebhookRoute(anon, srv.ID, "github/push"); m == nil || !m.Public || m.Signature.Secret != "s3cret" || !m.HideFromMain {
		t.Fatalf("preserving update lost state: %+v", m)
	}
	got.Signature = &service.WebhookSignature{Scheme: "github", Secret: service.WebhookSecretSentinel}
	if _, err := p.UpdateTrigger(ctx, tr.ID, *got); err != nil {
		t.Fatal(err)
	}
	if m, _ := p.ResolveWebhookRoute(anon, srv.ID, "github/push"); m.Signature.Secret != "s3cret" {
		t.Fatal("sentinel replaced the secret")
	}

	// Rotation re-encrypts the signing secret and server settings.
	if err := p.RotateEncryptionKey(t.Context(), bytes.Repeat([]byte{9}, 32)); err != nil {
		t.Fatal(err)
	}
	if m, _ := p.ResolveWebhookRoute(anon, srv.ID, "github/push"); m.Signature.Secret != "s3cret" {
		t.Fatal("secret lost by rotation")
	}

	// Narrowing the server's audience unpublishes routes of excluded workspaces.
	srv.WorkspaceIDs = []string{other.ID}
	if _, err := p.UpdateWebhookServer(ctx, srv.ID, *srv); err != nil {
		t.Fatal(err)
	}
	if m, _ := p.ResolveWebhookRoute(anon, srv.ID, "plain"); m != nil {
		t.Fatal("route survived audience change")
	}

	// Deliveries are kept per trigger and bounded.
	for i := 0; i < service.WebhookDeliveryRetention+5; i++ {
		if err := p.RecordWebhookDelivery(t.Context(), service.WebhookDelivery{WorkspaceID: w.ID, TriggerID: tr.ID, Status: 202}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := p.ListWebhookDeliveries(ctx, tr.ID, 0)
	if err != nil || len(list) != service.WebhookDeliveryRetention {
		t.Fatalf("deliveries: %d %v", len(list), err)
	}
	if _, err := p.ListWebhookDeliveries(ctxB, tr.ID, 0); err == nil {
		t.Fatal("foreign workspace read deliveries")
	}

	if err := p.DeleteWebhookServer(ctx, srv.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := p.GetTrigger(ctx, tr.ID); len(got.WebhookRoutes) != 0 {
		t.Fatalf("routes survived server deletion: %+v", got.WebhookRoutes)
	}
}
