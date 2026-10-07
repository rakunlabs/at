package postgres

import (
	"errors"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestAgentRuntimeSettingsPostgres(t *testing.T) {
	p := newTestStore(t, nil)

	settings, err := p.GetAgentRuntimeSettings(t.Context())
	if err != nil {
		t.Fatalf("get defaults: %v", err)
	}
	if settings.Version != 1 || settings.MaxBackgroundSubagentsPerOwner != service.DefaultMaxBackgroundSubagentsPerOwner {
		t.Fatalf("unexpected defaults: %+v", settings)
	}

	settings.MaxBackgroundSubagentsPerOwner = 24
	saved, err := p.SaveAgentRuntimeSettings(t.Context(), *settings)
	if err != nil {
		t.Fatalf("save settings: %v", err)
	}
	if saved.Version != 2 || saved.MaxBackgroundSubagentsPerOwner != 24 {
		t.Fatalf("unexpected saved settings: %+v", saved)
	}

	if _, err := p.SaveAgentRuntimeSettings(t.Context(), *settings); !errors.Is(err, service.ErrAgentRuntimeSettingsConflict) {
		t.Fatalf("stale save error = %v", err)
	}

	saved.TrustedLocalMCP = []string{" http://127.0.0.1:8890/mcp/ ", "http://127.0.0.1:8890/mcp", ""}
	saved, err = p.SaveAgentRuntimeSettings(t.Context(), *saved)
	if err != nil {
		t.Fatalf("save trusted local MCP: %v", err)
	}
	reloaded, err := p.GetAgentRuntimeSettings(t.Context())
	if err != nil || len(reloaded.TrustedLocalMCP) != 1 || reloaded.TrustedLocalMCP[0] != "http://127.0.0.1:8890/mcp" || reloaded.MaxBackgroundSubagentsPerOwner != 24 {
		t.Fatalf("trusted local MCP round trip: %+v %v", reloaded, err)
	}
	for _, bad := range []string{"https://example.com/mcp", "ftp://127.0.0.1/x", "not a url"} {
		reloaded.TrustedLocalMCP = []string{bad}
		if _, err := p.SaveAgentRuntimeSettings(t.Context(), *reloaded); err == nil {
			t.Fatalf("%q accepted as a trusted local MCP endpoint", bad)
		}
	}
}
