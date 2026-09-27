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

func TestDeveloperProjectFilePath(t *testing.T) {
	for _, candidate := range []string{".git/config", ".git", "../outside", "/absolute", ""} {
		if _, err := developerProjectFilePath(candidate); err == nil {
			t.Fatalf("developerProjectFilePath(%q) unexpectedly succeeded", candidate)
		}
	}
	got, err := developerProjectFilePath("internal/server/main.go")
	if err != nil || got != "internal/server/main.go" {
		t.Fatalf("developerProjectFilePath() = %q, %v", got, err)
	}
	if got, err := developerProjectFilePath(".github/workflows/test.yml"); err != nil || got == "" {
		t.Fatalf(".github must remain available: %q, %v", got, err)
	}
}

func TestDeveloperEditIsAnEdit(t *testing.T) {
	config := service.DefaultDeveloperSpaceConfig()
	call := service.ToolCall{Name: "edit_file", Arguments: map[string]any{"path": "main.go"}}
	if got := developerToolEffect(config.Plan, call); got != "deny" {
		t.Fatalf("plan edit_file = %q, want deny", got)
	}
	if got := developerToolEffect(config.Build, call); got != "allow" {
		t.Fatalf("build edit_file = %q, want allow", got)
	}
}

func TestDeveloperContainerConfigEnforcesDefaults(t *testing.T) {
	cfg := developerContainerConfig(&service.DeveloperSpace{ID: "space", WorkspaceID: "workspace", OwnerUserID: "owner"})
	if !cfg.PreferRootless || !cfg.PersistentVolume || cfg.CPU == "" || cfg.Memory == "" || cfg.DiskLimitBytes <= 0 || cfg.PidsLimit <= 0 {
		t.Fatalf("developer container defaults are not bounded: %+v", cfg)
	}
	if cfg.Image != service.DefaultDeveloperImage || !cfg.KeepAlive || !cfg.RetainWhenIdle {
		t.Fatalf("developer container should default to a retained, kept-alive %s: %+v", service.DefaultDeveloperImage, cfg)
	}
	custom := developerContainerConfig(&service.DeveloperSpace{Image: " ghcr.io/org/dev:1 ", CPULimit: "4", MemoryLimit: "8g"})
	if custom.Image != "ghcr.io/org/dev:1" || custom.CPU != "4" || custom.Memory != "8g" {
		t.Fatalf("developer container ignores the space settings: %+v", custom)
	}
}

func TestDeveloperSpaceValidateRuntimeSettings(t *testing.T) {
	tests := []struct {
		name  string
		space service.DeveloperSpace
		ok    bool
	}{
		{"defaults", service.DeveloperSpace{}, true},
		{"image with registry and tag", service.DeveloperSpace{Image: "ghcr.io/org/dev-env:1.2"}, true},
		{"image digest", service.DeveloperSpace{Image: "debian@sha256:abc123"}, true},
		{"image with space", service.DeveloperSpace{Image: "debian:13 AS x"}, false},
		{"image newline injection", service.DeveloperSpace{Image: "debian\nRUN id"}, false},
		{"image flag", service.DeveloperSpace{Image: "--privileged"}, false},
		{"fractional cpu", service.DeveloperSpace{CPULimit: "0.5"}, true},
		{"bad cpu", service.DeveloperSpace{CPULimit: "two"}, false},
		{"zero cpu", service.DeveloperSpace{CPULimit: "0"}, false},
		{"memory", service.DeveloperSpace{MemoryLimit: "512m"}, true},
		{"bad memory", service.DeveloperSpace{MemoryLimit: "4 GB"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.space.Validate(); (err == nil) != tt.ok {
				t.Fatalf("Validate() error = %v, want ok=%v", err, tt.ok)
			}
		})
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
