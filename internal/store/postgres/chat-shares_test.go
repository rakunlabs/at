package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

func TestChatShareVersioningAndLifecycle(t *testing.T) {
	p, adminCtx, workspace, admin := workspaceFixture(t)
	workspace.ExecutionEnabled = true
	if _, err := p.UpdateWorkspace(adminCtx, *workspace); err != nil {
		t.Fatal(err)
	}
	principal, _, err := p.ResolveWorkspaceAccess(adminCtx, workspace.ID, admin.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	adminCtx = service.WithAccessPrincipal(adminCtx, principal)
	conversation, err := p.CreatePlaygroundConversation(t.Context(), service.PlaygroundConversation{OwnerUserID: admin.ID, Title: "source"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.AppendPlaygroundMessages(t.Context(), admin.ID, conversation.ID, []service.PlaygroundMessage{
		{Role: "user", Data: map[string]any{"content": "hello"}},
		{Role: "assistant", Data: map[string]any{"content": "world"}},
		{Role: "user", Data: map[string]any{"content": "next"}},
		{Role: "assistant", Data: map[string]any{"content": "answer"}},
	}); err != nil {
		t.Fatal(err)
	}
	share, err := p.CreateChatShare(adminCtx, service.ChatShare{WorkspaceID: workspace.ID, SourceConversationID: conversation.ID, SourceOwnerUserID: admin.ID, ThroughSequence: 2, Payload: service.ChatSharePayload{Title: "v1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if share.CurrentVersion != 1 || share.Payload.Title != "v1" {
		t.Fatalf("created share: %+v", share)
	}
	share.ThroughSequence = 4
	share.Payload = service.ChatSharePayload{Title: "v2"}
	updated, err := p.UpdateChatShare(adminCtx, *share, nil)
	if err != nil {
		t.Fatal(err)
	}
	if updated.CurrentVersion != 2 || updated.ThroughSequence != 4 || updated.Payload.Title != "v2" {
		t.Fatalf("updated share: %+v", updated)
	}
	imported, err := p.ImportChatShare(adminCtx, share.ID, 2, admin.ID, "copy", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if imported.ImportedFromShareID != share.ID || imported.ImportedFromShareVersion != 2 {
		t.Fatalf("import provenance: %+v", imported)
	}
	var versions int
	if _, err := p.goqu.From(p.workspaceTable("chat_share_versions")).Select(goqu.COUNT("*")).Where(goqu.Ex{"share_id": share.ID}).ScanValContext(t.Context(), &versions); err != nil || versions != 2 {
		t.Fatalf("versions=%d err=%v", versions, err)
	}
	if err := p.DeletePlaygroundConversation(t.Context(), admin.ID, conversation.ID); err != nil {
		t.Fatal(err)
	}
	revoked, err := p.GetChatShare(t.Context(), share.ID)
	if err != nil || revoked == nil || revoked.RevokedAt == "" || revoked.SourceConversationID != "" {
		t.Fatalf("source deletion did not revoke share: %+v %v", revoked, err)
	}
	if _, err := p.DeleteWorkspace(adminCtx, workspace.Name); err != nil {
		t.Fatal(err)
	}
	missing, err := p.GetChatShare(t.Context(), share.ID)
	if !errors.Is(err, service.ErrChatShareNotFound) || missing != nil {
		t.Fatalf("workspace deletion retained share: %+v %v", missing, err)
	}
}

func TestChatShareConcurrentImportAndRevoke(t *testing.T) {
	p, adminCtx, workspace, admin := workspaceFixture(t)
	workspace.ExecutionEnabled = true
	if _, err := p.UpdateWorkspace(adminCtx, *workspace); err != nil {
		t.Fatal(err)
	}
	principal, _, err := p.ResolveWorkspaceAccess(adminCtx, workspace.ID, admin.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	adminCtx = service.WithAccessPrincipal(adminCtx, principal)
	conversation, err := p.CreatePlaygroundConversation(t.Context(), service.PlaygroundConversation{OwnerUserID: admin.ID, Title: "source"})
	if err != nil {
		t.Fatal(err)
	}
	share, err := p.CreateChatShare(adminCtx, service.ChatShare{WorkspaceID: workspace.ID, SourceConversationID: conversation.ID, SourceOwnerUserID: admin.ID, ThroughSequence: 1, Payload: service.ChatSharePayload{Title: "snapshot", Messages: []service.PlaygroundMessage{{Role: "assistant", Data: map[string]any{"content": "done"}}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var imported *service.PlaygroundConversation
	var importErr, revokeErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		imported, importErr = p.ImportChatShare(adminCtx, share.ID, 1, admin.ID, "copy", "", "", nil)
	}()
	go func() {
		defer wg.Done()
		<-start
		_, revokeErr = p.RevokeChatShare(context.Background(), share.ID, admin.ID)
	}()
	close(start)
	wg.Wait()
	if revokeErr != nil {
		t.Fatal(revokeErr)
	}
	if importErr != nil && !errors.Is(importErr, service.ErrChatShareConflict) {
		t.Fatalf("unexpected import error: %v", importErr)
	}
	if imported != nil {
		kept, err := p.GetPlaygroundConversation(t.Context(), admin.ID, imported.ID)
		if err != nil || kept == nil {
			t.Fatalf("successful import did not survive revoke: %+v %v", kept, err)
		}
	}
}
