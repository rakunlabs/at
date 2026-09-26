package postgres

import (
	"errors"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestDeveloperSpacesAreUserAndWorkspaceScoped(t *testing.T) {
	p, ownerCtx, workspace, _ := workspaceFixture(t)
	created, err := p.CreateDeveloperSpace(ownerCtx, service.DeveloperSpace{Name: "Main", Image: "at-dev:latest", DiskLimitBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if created.WorkspaceID != workspace.ID || created.OwnerUserID == "" || created.Status != service.DeveloperSpacePending {
		t.Fatalf("created: %+v", created)
	}
	if created.Config.Plan.SystemPrompt == "" || created.Config.Build.SystemPrompt == "" || created.Config.Review.SystemPrompt == "" {
		t.Fatalf("default profiles missing: %+v", created.Config)
	}
	if created.Config.Plan.Rules[len(created.Config.Plan.Rules)-1].Effect != "deny" || created.Config.Build.Rules[len(created.Config.Build.Rules)-1].Resource != "push" {
		t.Fatalf("unsafe default permissions: %+v", created.Config)
	}

	if _, err := p.CreateDeveloperSpace(ownerCtx, service.DeveloperSpace{Name: "main"}); !errors.Is(err, service.ErrDeveloperSpaceConflict) {
		t.Fatalf("case-insensitive duplicate: %v", err)
	}

	other := workspaceUser(t, p, "developer-other")
	otherCtx := service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: other.ID, WorkspaceID: workspace.ID})
	if list, err := p.ListDeveloperSpaces(otherCtx); err != nil || len(list) != 0 {
		t.Fatalf("foreign list: %+v %v", list, err)
	}
	if _, err := p.GetDeveloperSpace(otherCtx, created.ID); !errors.Is(err, service.ErrDeveloperSpaceNotFound) {
		t.Fatalf("foreign get: %v", err)
	}
	if err := p.DeleteDeveloperSpace(otherCtx, created.ID); !errors.Is(err, service.ErrDeveloperSpaceNotFound) {
		t.Fatalf("foreign delete: %v", err)
	}

	created.Name = "Renamed"
	created.MemoryLimit = "2Gi"
	updated, err := p.UpdateDeveloperSpace(ownerCtx, *created)
	if err != nil || updated.Name != "Renamed" || updated.MemoryLimit != "2Gi" || updated.Status != service.DeveloperSpacePending {
		t.Fatalf("updated: %+v %v", updated, err)
	}
	if err := p.DeleteDeveloperSpace(ownerCtx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetDeveloperSpace(ownerCtx, created.ID); !errors.Is(err, service.ErrDeveloperSpaceNotFound) {
		t.Fatalf("read deleted: %v", err)
	}
}

func TestDeveloperSpaceRequiresPrincipal(t *testing.T) {
	p := newTestStore(t, nil)
	if _, err := p.ListDeveloperSpaces(t.Context()); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("list without principal: %v", err)
	}
	if _, err := p.CreateDeveloperSpace(t.Context(), service.DeveloperSpace{Name: "nope"}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("create without principal: %v", err)
	}
}

func TestDeveloperRepositoryWorktreeSessionLifecycle(t *testing.T) {
	p, ctx, _, _ := workspaceFixture(t)
	space, err := p.CreateDeveloperSpace(ctx, service.DeveloperSpace{Name: "Code"})
	if err != nil {
		t.Fatal(err)
	}
	repository, err := p.CreateDeveloperRepository(ctx, service.DeveloperRepository{
		SpaceID: space.ID, Name: "at", RemoteURL: "https://example.test/acme/at.git", DefaultBranch: "main",
	})
	if err != nil || repository.Status != service.DeveloperRepositoryPending {
		t.Fatalf("repository: %+v %v", repository, err)
	}
	worktree, err := p.CreateDeveloperWorktree(ctx, service.DeveloperWorktree{
		RepositoryID: repository.ID, Name: "feature", Branch: "feature/storage", BaseRef: "main",
	})
	if err != nil || worktree.State != service.DeveloperWorktreePending || worktree.SpaceID != space.ID {
		t.Fatalf("worktree: %+v %v", worktree, err)
	}
	session, err := p.CreateDeveloperSession(ctx, service.DeveloperSession{
		SpaceID: space.ID, WorktreeID: worktree.ID, Mode: service.DeveloperModeBuild,
		Title: "Implement storage", Provider: "openai", Model: "gpt",
	})
	if err != nil || session.Status != service.DeveloperSessionIdle || session.RepositoryID != repository.ID {
		t.Fatalf("session: %+v %v", session, err)
	}
	if _, err := p.AppendDeveloperSessionMessage(ctx, service.DeveloperSessionMessage{SessionID: session.ID, Role: "user", Content: "Implement it"}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.AppendDeveloperSessionMessage(ctx, service.DeveloperSessionMessage{SessionID: session.ID, Role: "assistant", Content: []service.ContentBlock{{Type: "text", Text: "Working"}}}); err != nil {
		t.Fatal(err)
	}
	messages, err := p.ListDeveloperSessionMessages(ctx, session.ID)
	if err != nil || len(messages) != 2 || messages[0].Content != "Implement it" {
		t.Fatalf("messages: %+v %v", messages, err)
	}
	if blocks, ok := messages[1].Content.([]service.ContentBlock); !ok || len(blocks) != 1 || blocks[0].Text != "Working" {
		t.Fatalf("assistant blocks were not restored canonically: %#v", messages[1].Content)
	}
	if _, err := p.BeginDeveloperSessionRun(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.BeginDeveloperSessionRun(ctx, session.ID); !errors.Is(err, service.ErrDeveloperSessionBusy) {
		t.Fatalf("concurrent run: %v", err)
	}
	if _, err := p.SetDeveloperSessionRuntime(ctx, session.ID, service.DeveloperSessionCompleted, ""); err != nil {
		t.Fatal(err)
	}
	pending, err := p.SaveDeveloperPendingTool(ctx, service.DeveloperPendingTool{SessionID: session.ID, Kind: "permission", TraceID: "trace-1", Step: 4, ToolCalls: []service.ToolCall{{ID: "call-1", Name: "run_command", Arguments: map[string]any{"command": "go"}}}})
	if err != nil || len(pending.ToolCalls) != 1 || pending.ToolCalls[0].Name != "run_command" || pending.Kind != "permission" || pending.TraceID != "trace-1" || pending.Step != 4 {
		t.Fatalf("pending tool: %+v %v", pending, err)
	}
	claimed, err := p.ClaimDeveloperPendingTool(ctx, session.ID)
	if err != nil || claimed == nil || len(claimed.ToolCalls) != 1 || claimed.State != "executing" {
		t.Fatalf("claimed pending tool: %+v %v", claimed, err)
	}
	if claimed, err := p.ClaimDeveloperPendingTool(ctx, session.ID); err != nil || claimed != nil {
		t.Fatalf("double claim: %+v %v", claimed, err)
	}
	if _, err := p.ResolveDeveloperPendingTool(ctx, session.ID, []service.ContentBlock{{Type: "tool_result", ToolUseID: "call-1", Content: "ok"}}); err != nil {
		t.Fatalf("resolve pending tool: %v", err)
	}
	if pending, err := p.GetDeveloperPendingTool(ctx, session.ID); err != nil || pending != nil {
		t.Fatalf("resolved pending tool: %+v %v", pending, err)
	}
	snapshot, err := p.SaveDeveloperSessionSnapshot(ctx, service.DeveloperSessionSnapshot{SessionID: session.ID, Step: 1, Phase: "before"})
	if err != nil || snapshot.Step != 1 || snapshot.Phase != "before" {
		t.Fatalf("snapshot: %+v %v", snapshot, err)
	}
	snapshots, err := p.ListDeveloperSessionSnapshots(ctx, session.ID)
	if err != nil || len(snapshots) != 1 || snapshots[0].ID != snapshot.ID {
		t.Fatalf("snapshots: %+v %v", snapshots, err)
	}

	if err := p.DeleteDeveloperWorktree(ctx, worktree.ID); err != nil {
		t.Fatal(err)
	}
	retained, err := p.GetDeveloperSession(ctx, session.ID)
	if err != nil || retained.WorktreeID != "" || retained.RepositoryID != repository.ID {
		t.Fatalf("session after worktree pruning: %+v %v", retained, err)
	}
	if err := p.DeleteDeveloperRepository(ctx, repository.ID); err != nil {
		t.Fatal(err)
	}
	retained, err = p.GetDeveloperSession(ctx, session.ID)
	if err != nil || retained.RepositoryID != "" {
		t.Fatalf("session after repository pruning: %+v %v", retained, err)
	}

	if records, err := p.ListDeveloperSessions(ctx, space.ID); err != nil || len(records) != 1 || records[0].ID != session.ID {
		t.Fatalf("session list: %+v %v", records, err)
	}
	if err := p.DeleteDeveloperSpace(ctx, space.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetDeveloperSession(ctx, session.ID); !errors.Is(err, service.ErrDeveloperSpaceNotFound) {
		t.Fatalf("session survived space deletion: %v", err)
	}
}

func TestDeveloperResourceValidation(t *testing.T) {
	for name, remote := range map[string]string{
		"host path":   "/srv/repo",
		"file URL":    "file:///srv/repo",
		"credentials": "https://token@example.test/repo.git",
	} {
		t.Run(name, func(t *testing.T) {
			if err := (service.DeveloperRepository{Name: "repo", RemoteURL: remote}).Validate(); err == nil {
				t.Fatalf("accepted %q", remote)
			}
		})
	}
	if err := (service.DeveloperRepository{Name: "repo", RemoteURL: "git@example.test:acme/repo.git"}).Validate(); err != nil {
		t.Fatalf("ssh shorthand: %v", err)
	}
	if err := (service.DeveloperWorktree{Name: "bad", Branch: "--upload-pack=evil"}).Validate(); err == nil {
		t.Fatal("accepted option-like branch")
	}
}
