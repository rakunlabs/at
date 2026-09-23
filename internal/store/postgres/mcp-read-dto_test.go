package postgres

import (
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestMCPReadDTOAllowsWritersToLoadCurrentConfiguration(t *testing.T) {
	const workspaceID = "workspace-1"
	newConfig := func() (service.MCPServerConfig, []string) {
		return service.MCPServerConfig{
			HTTPTools:    []service.MCPHTTPTool{{Name: "lookup", URL: "https://example.test", Headers: map[string]string{"Authorization": "secret"}}},
			MCPUpstreams: []service.MCPUpstream{{URL: "https://mcp.example.test", Headers: map[string]string{"Authorization": "secret"}}},
		}, []string{"https://legacy.example.test"}
	}

	reader := service.AccessPrincipal{
		WorkspaceID: workspaceID,
		Grants:      []service.AccessGrant{{Capability: "mcp.read"}},
	}
	config, urls := newConfig()
	mcpReadDTO(reader, "set-1", workspaceID, &config, &urls)
	if config.HTTPTools[0].URL != "" || config.MCPUpstreams != nil || urls != nil {
		t.Fatalf("read-only DTO retained credential-bearing configuration: %#v %#v", config, urls)
	}

	writer := service.AccessPrincipal{
		WorkspaceID: workspaceID,
		Grants:      []service.AccessGrant{{Capability: "mcp.write"}},
	}
	config, urls = newConfig()
	mcpReadDTO(writer, "set-1", workspaceID, &config, &urls)
	if len(config.MCPUpstreams) != 1 || config.MCPUpstreams[0].URL == "" || len(urls) != 1 {
		t.Fatalf("writer DTO redacted configuration needed for safe editing: %#v %#v", config, urls)
	}
}
