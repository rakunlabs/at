package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestBotUpdateAddsCustomCommand(t *testing.T) {
	f := newMachineFixture(t)
	bot, err := f.store.CreateBotConfig(f.ctx, service.BotConfig{
		Name: "Command test", Platform: "telegram", Token: "fixture-token",
		CustomCommands: []service.BotCustomCommand{{Command: "existing", Brief: "Existing command"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.s.execBotUpdate(f.ctx, map[string]any{
		"id": bot.ID,
		"custom_commands": []any{
			map[string]any{"command": "existing", "brief": "Existing command"},
			map[string]any{"command": "/new_long", "description": "New long video", "brief": "Make a video about {args}", "max_iterations": float64(0)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var updated service.BotConfig
	if err := json.Unmarshal([]byte(result), &updated); err != nil {
		t.Fatal(err)
	}
	if len(updated.CustomCommands) != 2 || updated.CustomCommands[1].Command != "new_long" {
		t.Fatalf("unexpected commands: %+v", updated.CustomCommands)
	}
	stored, err := f.store.GetBotConfig(f.ctx, bot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored == nil || stored.Name != bot.Name || stored.Token != bot.Token || len(stored.CustomCommands) != 2 {
		t.Fatalf("update did not preserve the bot and persist commands: %+v", stored)
	}
}

func TestBotUpdateReportsInvalidCommandReference(t *testing.T) {
	f := newMachineFixture(t)
	bot, err := f.store.CreateBotConfig(f.ctx, service.BotConfig{Name: "Command test", Platform: "telegram", Token: "fixture-token"})
	if err != nil {
		t.Fatal(err)
	}
	args := map[string]any{
		"id": bot.ID,
		"custom_commands": []any{map[string]any{
			"command": "documentary", "organization_id": "01M1SJEE8B6HC78WECHGYBMZZZ", "brief": "Documentary about {args}",
		}},
	}
	_, err = f.s.execBotUpdate(f.ctx, args)
	if !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("expected missing reference, got %v", err)
	}
	for _, detail := range []string{"custom_commands[0]", "documentary", "organization_id", "01M1SJEE8B6HC78WECHGYBMZZZ"} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("missing diagnostic %q: %v", detail, err)
		}
	}
	wrapped := fmt.Errorf("all MCP routes failed: %w", errors.Join(fmt.Errorf("builtin: %w", err)))
	if status := mcpToolErrorStatus(wrapped); status != http.StatusNotFound {
		t.Fatalf("MCP status = %d, want 404", status)
	}
	stored, err := f.store.GetBotConfig(f.ctx, bot.ID)
	if err != nil || len(stored.CustomCommands) != 0 {
		t.Fatalf("invalid update changed commands: %v", err)
	}
	org, err := f.store.CreateOrganization(f.ctx, service.Organization{Name: "Documentaries"})
	if err != nil {
		t.Fatal(err)
	}
	args["custom_commands"].([]any)[0].(map[string]any)["organization_id"] = org.ID
	if _, err := f.s.execBotUpdate(f.ctx, args); err != nil {
		t.Fatalf("valid same-workspace organization was rejected: %v", err)
	}
}

func TestMCPToolErrorStatus(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{service.ErrAccessResourceNotFound, 404},
		{service.ErrAccessDenied, 403},
		{service.ErrWorkspaceRequired, 400},
		{service.ErrWorkspaceConflict, 409},
		{errors.New("database unavailable"), 500},
	} {
		if got := mcpToolErrorStatus(fmt.Errorf("route: %w", tc.err)); got != tc.status {
			t.Errorf("%v: got %d, want %d", tc.err, got, tc.status)
		}
	}
}

func TestBotUpdateCanAddCommandWithDeletedDefaultAgent(t *testing.T) {
	f := newMachineFixture(t)
	agent, err := f.store.CreateAgent(f.ctx, service.Agent{Name: "Old default"})
	if err != nil {
		t.Fatal(err)
	}
	org, err := f.store.CreateOrganization(f.ctx, service.Organization{Name: "Documentaries"})
	if err != nil {
		t.Fatal(err)
	}
	oldOrg, err := f.store.CreateOrganization(f.ctx, service.Organization{Name: "Old organization"})
	if err != nil {
		t.Fatal(err)
	}
	bot, err := f.store.CreateBotConfig(f.ctx, service.BotConfig{
		Name: "Single bot", Platform: "telegram", Token: "fixture-token", DefaultAgentID: agent.ID,
		ChannelAgents: map[string]string{"old-channel": agent.ID}, AllowedAgentIDs: []string{agent.ID},
		CustomCommands: []service.BotCustomCommand{{Command: "old_command", OrganizationID: oldOrg.ID, AgentID: agent.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.DeleteAgent(f.ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DeleteOrganization(f.ctx, oldOrg.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.s.execBotUpdate(f.ctx, map[string]any{
		"id": bot.ID,
		"custom_commands": []any{
			map[string]any{"command": "old_command", "organization_id": oldOrg.ID, "agent_id": agent.ID},
			map[string]any{"command": "documentary", "organization_id": org.ID, "brief": "Documentary about {args}"},
		},
	})
	if err != nil {
		t.Fatalf("an unchanged deleted default agent blocked an independent command: %v", err)
	}
	stored, err := f.store.GetBotConfig(f.ctx, bot.ID)
	if err != nil || stored == nil {
		t.Fatalf("read bot after edit: %v", err)
	}
	if stored.DefaultAgentID != agent.ID || stored.ChannelAgents["old-channel"] != agent.ID || len(stored.AllowedAgentIDs) != 1 || stored.AllowedAgentIDs[0] != agent.ID || len(stored.CustomCommands) != 2 || stored.CustomCommands[0].OrganizationID != oldOrg.ID || stored.CustomCommands[1].OrganizationID != org.ID {
		t.Fatal("update must preserve omitted settings and save the new command")
	}
	// Preserving an existing reference must never authorize assigning a new
	// unavailable resource, even after an unrelated edit has succeeded.
	if _, err := f.s.execBotUpdate(f.ctx, map[string]any{"id": bot.ID, "default_agent_id": "missing-agent"}); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("new missing agent was accepted: %v", err)
	}
	if _, err := f.s.execBotUpdate(f.ctx, map[string]any{
		"id": bot.ID, "custom_commands": []any{map[string]any{"command": "new_command", "organization_id": oldOrg.ID}},
	}); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("a new command reused an unavailable old reference: %v", err)
	}
}
