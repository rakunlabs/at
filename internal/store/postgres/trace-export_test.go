package postgres

import (
	"errors"
	"strings"
	"testing"

	"github.com/doug-martin/goqu/v9"
	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
)

func TestTraceExportWorkspaceSettingsPostgres(t *testing.T) {
	p, ctx, ws, admin := workspaceFixture(t)
	key, err := atcrypto.DeriveKey("trace-export-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.RotateEncryptionKey(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	other, err := p.CreateWorkspace(ctx, "Other traces", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := p.LoadTraceExportSettings(ctx, ws.ID)
	if err != nil || c.Enabled || c.Version != 0 {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	c.Enabled = true
	c.Endpoint = "https://collector.example/v1/traces"
	c.Headers["Authorization"] = "secret-trace-header"
	workspaceAdmin := workspaceUser(t, p, "trace-workspace-admin")
	workspaceAdminCtx := workspaceMember(t, p, ctx, ws.ID, workspaceAdmin, "admin")
	saved, err := p.SaveTraceExportSettings(workspaceAdminCtx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.SaveTraceExportSettings(ctx, c); !errors.Is(err, service.ErrTraceExportConflict) {
		t.Fatalf("stale update: %v", err)
	}
	var raw string
	_, err = p.goqu.From(p.workspaceTable("trace_export_settings")).Select("config").Where(goqu.Ex{"workspace_id": ws.ID}).ScanValContext(ctx, &raw)
	if err != nil || !atcrypto.IsEncrypted(raw) || strings.Contains(raw, "secret-trace-header") {
		t.Fatalf("not encrypted: %v", err)
	}
	blank, err := p.LoadTraceExportSettings(ctx, other.ID)
	if err != nil || blank.Enabled || len(blank.Headers) != 0 {
		t.Fatal("settings leaked to other workspace")
	}
	member := workspaceUser(t, p, "trace-member")
	memberCtx := workspaceMember(t, p, ctx, ws.ID, member, "member")
	if _, err := p.SaveTraceExportSettings(memberCtx, saved); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("member save: %v", err)
	}
	key2, _ := atcrypto.DeriveKey("trace-export-test-rotated")
	if err := p.RotateEncryptionKey(t.Context(), key2); err != nil {
		t.Fatal(err)
	}
	loaded, err := p.LoadTraceExportSettings(ctx, ws.ID)
	if err != nil || loaded.Headers["Authorization"] != "secret-trace-header" || loaded.Version != saved.Version {
		t.Fatalf("rotation lost config: %v", err)
	}
	loaded.Enabled = false
	if _, err := p.SaveTraceExportSettings(ctx, loaded); err != nil {
		t.Fatal(err)
	}
	// The same workspace identity used for delivery must also reach persisted traces.
	if err := p.RecordLLMCall(ctx, service.LLMCall{ID: "trace-scoped", TraceID: "scope", WorkspaceID: other.ID}); err != nil {
		t.Fatal(err)
	}
	var recorded string
	_, err = p.goqu.From(p.tableLLMCalls).Select("workspace_id").Where(goqu.Ex{"id": "trace-scoped"}).ScanValContext(ctx, &recorded)
	if err != nil || recorded != ws.ID {
		t.Fatalf("record scope: %q %v", recorded, err)
	}
}
