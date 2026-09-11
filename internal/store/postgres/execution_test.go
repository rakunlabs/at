package postgres

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

func TestExecutionPostgresPolicyAdminFenceAndProvenance(t *testing.T) {
	p := newTestStore(t, nil)
	var admin atomic.Bool
	validate := func(ctx context.Context, who service.ExecutionProvenance, action service.ExecutionAction) (service.ExecutionValidation, error) {
		policy, err := p.GetExecutionPolicy(ctx, who.WorkspaceID)
		if err != nil {
			return service.ExecutionValidation{}, err
		}
		return service.ExecutionValidation{Allowed: true, PlatformAdmin: admin.Load(), MembershipVersion: 1, Policy: *policy}, nil
	}
	provenance := service.ExecutionProvenance{RunID: "r", UserID: "u", WorkspaceID: "w", Source: "test", MembershipVersion: 1}
	ctx, err := service.BindExecution(t.Context(), provenance, t.TempDir(), validate)
	if err != nil {
		t.Fatal(err)
	}
	policy := service.ExecutionPolicy{WorkspaceID: "w", Mode: service.ExecutionTrustedHost, GrantedBy: "forged-user", AllowedTools: []string{"bash_execute"}}
	if err := p.SaveExecutionPolicy(ctx, policy); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatalf("workspace admin elevated host authority: %v", err)
	}
	if err := p.CreateExecutionProvenance(ctx, provenance); err != nil {
		t.Fatal(err)
	}
	if err := p.CreateExecutionProvenance(ctx, provenance); err == nil {
		t.Fatal("provenance was overwritten")
	}
	forged := provenance
	forged.WorkspaceID = "other"
	if err := p.CreateExecutionProvenance(ctx, forged); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatal("persisted forged workspace")
	}
	loaded, err := p.GetExecutionProvenance(ctx, "r")
	if err != nil || loaded == nil || *loaded != provenance {
		t.Fatalf("provenance roundtrip: %+v %v", loaded, err)
	}
	admin.Store(true)
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if p.SaveExecutionPolicy(ctx, policy) == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("optimistic policy fence accepted %d writes", successes.Load())
	}
	current, err := p.GetExecutionPolicy(t.Context(), "w")
	if err != nil {
		t.Fatal(err)
	}
	if current.Version != 1 || current.GrantedBy != "u" || current.Mode != service.ExecutionTrustedHost {
		t.Fatalf("policy actor/version: %+v", current)
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "tool", Name: "bash_execute"}); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatalf("policy edit elevated an existing run: %v", err)
	}
}

func TestExecutionPostgresScopedTasksAndBotSessions(t *testing.T) {
	p := newTestStore(t, nil)
	actor, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "runtime-seed", Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	seedCtx := service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: actor.ID, WorkspaceID: "legacy-default", PlatformAdmin: true})
	agent, err := p.CreateAgent(seedCtx, service.Agent{Name: "runtime-agent"})
	if err != nil {
		t.Fatal(err)
	}
	org, err := p.CreateOrganization(seedCtx, service.Organization{Name: "runtime-org", HeadAgentID: agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := p.CreateTask(seedCtx, service.Task{Title: "parent", Status: "open", OrganizationID: org.ID, AssignedAgentID: agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	principal := service.AccessPrincipal{UserID: actor.ID, WorkspaceID: "legacy-default", PlatformAdmin: true}
	base := service.WithAccessPrincipal(t.Context(), principal)
	ctx, err := service.BindExecution(base, service.ExecutionProvenance{RunID: "run", UserID: principal.UserID, WorkspaceID: principal.WorkspaceID, Source: "test"}, t.TempDir(), func(ctx context.Context, who service.ExecutionProvenance, action service.ExecutionAction) (service.ExecutionValidation, error) {
		v := service.ExecutionValidation{Allowed: true, Policy: service.ExecutionPolicy{WorkspaceID: who.WorkspaceID}}
		kind := map[string]string{"tasks.run": "tasks", "agents.run": "agents", "organizations.run": "organizations", "bots.use": "bots"}[action.Name]
		if kind != "" {
			_, err := p.ResolveExecutionResource(ctx, kind, action.ResourceID)
			return v, err
		}
		return v, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := p.CreateExecutionChildTask(ctx, service.Task{ParentID: parent.ID, AssignedAgentID: agent.ID, OrganizationID: org.ID, Title: "child", Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	resource, err := p.ResolveExecutionResource(ctx, "tasks", child.ID)
	if err != nil || resource.WorkspaceID != "legacy-default" {
		t.Fatalf("child scope: %+v %v", resource, err)
	}
	provenance, err := p.GetExecutionProvenance(ctx, "task/"+child.ID)
	if err != nil || provenance == nil || provenance.UserID != actor.ID {
		t.Fatalf("missing atomic child initiator: %+v %v", provenance, err)
	}
	before, err := p.goqu.From(p.tableTasks).CountContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.goqu.Insert(p.executionTable("workspaces")).Rows(goqu.Record{"id": "other-workspace", "name": "Other"}).Executor().ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	foreign, err := p.CreateAgent(seedCtx, service.Agent{Name: "foreign-agent"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.goqu.Update(p.tableAgents).Set(goqu.Record{"workspace_id": "other-workspace"}).Where(goqu.Ex{"id": foreign.ID}).Executor().ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateExecutionChildTask(ctx, service.Task{ParentID: parent.ID, AssignedAgentID: foreign.ID, OrganizationID: org.ID, Title: "denied", Status: "open"}); err == nil {
		t.Fatal("foreign reference admitted")
	}
	after, err := p.goqu.From(p.tableTasks).CountContext(ctx)
	if err != nil || before != after {
		t.Fatal("denied child partially inserted")
	}
	bot, err := p.CreateBotConfig(seedCtx, service.BotConfig{Name: "runtime-bot", Platform: "telegram"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := p.CreateExecutionBotSession(ctx, service.ChatSession{AgentID: agent.ID, Name: "runtime-chat", Config: service.ChatSessionConfig{BotConfigID: bot.ID, Platform: "telegram", PlatformUserID: "transport-user", PlatformChannelID: "channel"}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := p.GetExecutionBotSession(ctx, "telegram", "transport-user", "channel", bot.ID)
	if err != nil || loaded == nil || loaded.ID != session.ID {
		t.Fatalf("scoped bot session: %+v %v", loaded, err)
	}
	var workspace string
	_, err = p.goqu.From(p.tableChatSessions).Select("workspace_id").Where(goqu.Ex{"id": session.ID}).ScanValContext(ctx, &workspace)
	if err != nil || workspace != principal.WorkspaceID {
		t.Fatalf("bot session workspace %q %v", workspace, err)
	}
}
