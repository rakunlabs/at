package server

import (
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestDeveloperDefaultProfileToolPolicy(t *testing.T) {
	config := service.DefaultDeveloperSpaceConfig()
	tests := []struct {
		name    string
		profile service.DeveloperAgentProfile
		call    service.ToolCall
		want    string
	}{
		{"plan reads", config.Plan, service.ToolCall{Name: "read_file", Arguments: map[string]any{"path": "main.go"}}, "allow"},
		{"plan lists", config.Plan, service.ToolCall{Name: "list_files", Arguments: map[string]any{}}, "allow"},
		{"plan searches", config.Plan, service.ToolCall{Name: "search", Arguments: map[string]any{"pattern": "TODO"}}, "allow"},
		{"plan edits denied", config.Plan, service.ToolCall{Name: "write_file", Arguments: map[string]any{"path": "main.go"}}, "deny"},
		{"plan shell denied", config.Plan, service.ToolCall{Name: "run_command", Arguments: map[string]any{"command": "go"}}, "deny"},
		{"build edits", config.Build, service.ToolCall{Name: "write_file", Arguments: map[string]any{"path": "main.go"}}, "allow"},
		{"build shell asks", config.Build, service.ToolCall{Name: "run_command", Arguments: map[string]any{"command": "go"}}, "ask"},
		{"review edits denied", config.Review, service.ToolCall{Name: "write_file", Arguments: map[string]any{"path": "main.go"}}, "deny"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := developerToolEffect(tt.profile, tt.call); got != tt.want {
				t.Fatalf("effect = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDeveloperWorktreeFilePath(t *testing.T) {
	for _, candidate := range []string{".git/config", ".git", "../outside", "/absolute"} {
		if _, err := developerWorktreeFilePath(candidate); err == nil {
			t.Fatalf("developerWorktreeFilePath(%q) unexpectedly succeeded", candidate)
		}
	}
	got, err := developerWorktreeFilePath("internal/server/main.go")
	if err != nil || got != "internal/server/main.go" {
		t.Fatalf("developerWorktreeFilePath() = %q, %v", got, err)
	}
	if got, err := developerWorktreeFilePath(".github/workflows/test.yml"); err != nil || got == "" {
		t.Fatalf(".github must remain available: %q, %v", got, err)
	}
}

func TestDeveloperContainerConfigEnforcesDefaults(t *testing.T) {
	cfg := developerContainerConfig(&service.DeveloperSpace{ID: "space", WorkspaceID: "workspace", OwnerUserID: "owner"})
	if !cfg.RequireRootless || !cfg.PersistentVolume || cfg.CPU == "" || cfg.Memory == "" || cfg.DiskLimitBytes <= 0 || cfg.PidsLimit <= 0 {
		t.Fatalf("developer container defaults are not bounded: %+v", cfg)
	}
}

func TestDeveloperToolPolicyLastMatchWins(t *testing.T) {
	profile := service.DeveloperAgentProfile{Rules: []service.DeveloperToolRule{
		{Action: "read", Resource: "*", Effect: "allow"},
		{Action: "read", Resource: "secrets/*", Effect: "deny"},
	}}
	if got := developerToolEffect(profile, service.ToolCall{Name: "read_file", Arguments: map[string]any{"path": "secrets/key"}}); got != "deny" {
		t.Fatalf("effect = %q", got)
	}
}
