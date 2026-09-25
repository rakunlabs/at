package postgres

import (
	"errors"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestPersonalSkillsAndMCPSetsVisibilityAndPublish(t *testing.T) {
	p, ctx, workspace, _ := workspaceFixture(t)
	alice := workspaceUser(t, p, "personal-resources-alice")
	bob := workspaceUser(t, p, "personal-resources-bob")
	aliceCtx := workspaceMember(t, p, ctx, workspace.ID, alice, "admin")
	bobCtx := workspaceMember(t, p, ctx, workspace.ID, bob, "member")

	personalSkill, err := p.CreateSkill(aliceCtx, service.Skill{Name: "alice-skill", OwnerUserID: alice.ID, SystemPrompt: "private"})
	if err != nil || personalSkill.Scope != "personal" {
		t.Fatalf("create personal skill: %+v %v", personalSkill, err)
	}
	if got, err := p.GetSkill(bobCtx, personalSkill.ID); err != nil || got != nil {
		t.Fatalf("foreign personal skill visible: %+v %v", got, err)
	}
	if got, err := p.UpdateSkill(bobCtx, personalSkill.ID, service.Skill{Name: "stolen"}); err != nil || got != nil {
		t.Fatalf("foreign personal skill update: %+v %v", got, err)
	}
	if _, err := p.CreateAgent(aliceCtx, service.Agent{Name: "personal-skill-agent", OwnerUserID: alice.ID, Config: service.AgentConfig{Skills: []service.SkillRef{{ID: personalSkill.Name}}}}); err != nil {
		t.Fatalf("personal agent should accept owner's skill: %v", err)
	}
	if _, err := p.CreateAgent(aliceCtx, service.Agent{Name: "workspace-skill-agent", Config: service.AgentConfig{Skills: []service.SkillRef{{ID: personalSkill.Name}}}}); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("workspace agent accepted personal skill: %v", err)
	}
	workspaceSkill, err := p.PublishSkillToWorkspace(aliceCtx, personalSkill.ID, "alice")
	if err != nil || workspaceSkill == nil || workspaceSkill.OwnerUserID != "" || workspaceSkill.ID == personalSkill.ID {
		t.Fatalf("publish skill: %+v %v", workspaceSkill, err)
	}
	if got, err := p.GetSkill(bobCtx, workspaceSkill.ID); err != nil || got == nil {
		t.Fatalf("published skill not visible: %+v %v", got, err)
	}

	personalMCP, err := p.CreateMCPSet(aliceCtx, service.MCPSet{
		Name: "alice-mcp", OwnerUserID: alice.ID,
		Config: service.MCPServerConfig{MCPUpstreams: []service.MCPUpstream{{URL: "https://example.com/mcp"}}},
	})
	if err != nil || personalMCP.Scope != "personal" {
		t.Fatalf("create personal MCP: %+v %v", personalMCP, err)
	}
	if got, err := p.GetMCPSet(bobCtx, personalMCP.ID); err != nil || got != nil {
		t.Fatalf("foreign personal MCP visible: %+v %v", got, err)
	}
	workspaceMCP, err := p.PublishMCPSetToWorkspace(aliceCtx, personalMCP.ID, "alice")
	if err != nil || workspaceMCP == nil || workspaceMCP.OwnerUserID != "" || workspaceMCP.ID == personalMCP.ID {
		t.Fatalf("publish MCP: %+v %v", workspaceMCP, err)
	}
	if len(workspaceMCP.Config.MCPUpstreams) != 1 || workspaceMCP.Config.MCPUpstreams[0].URL == "" {
		t.Fatalf("published MCP lost raw config: %+v", workspaceMCP.Config)
	}
	if got, err := p.GetMCPSet(bobCtx, workspaceMCP.ID); err != nil || got == nil {
		t.Fatalf("published MCP not visible: %+v %v", got, err)
	}
}
