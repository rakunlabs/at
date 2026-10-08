package service

import "testing"

// The host rule is what keeps a browser-dialled MCP from becoming a way around
// workspace execution policy: a public endpoint routed through the page runs
// with no CheckExecution, no mcp_tool admission and no server-side trace.
func TestValidateLocalMCPURL(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"loopback v4", "http://127.0.0.1:3000/mcp", false},
		{"loopback name", "http://localhost:8787/mcp", false},
		{"loopback name subdomain", "http://tools.localhost:8787/mcp", false},
		{"loopback v6", "http://[::1]:3000/mcp", false},
		{"private v4", "http://192.168.1.20:3000/mcp", false},
		{"private 10", "http://10.1.2.3/mcp", false},
		{"cgnat", "http://100.101.102.103:3000/mcp", false},
		{"link local", "http://169.254.4.5/mcp", false},
		{"mdns", "http://studio.local:3000/mcp", false},
		{"https private", "https://192.168.1.20:8443/mcp", false},
		{"nested path", "http://127.0.0.1:3000/mcp/api", false},
		{"remote AT gateway", "https://at.example.com/gateway/v1/mcp/tools", false},
		{"remote AT gateway mcp suffix", "https://at.example.com/gateway/v1/mcp/tools/mcp", false},
		{"remote AT gateway under base path", "https://example.com/at/gateway/v1/mcp/tools", false},

		{"public host", "https://mcp.example.com/mcp", true},
		{"remote AT gateway over http", "http://at.example.com/gateway/v1/mcp/tools", true},
		{"remote AT gateway without name", "https://at.example.com/gateway/v1/mcp/", true},
		{"remote AT gateway other route", "https://at.example.com/gateway/v1/mcp/tools/ws", true},
		{"remote AT gateway chat", "https://at.example.com/gateway/v1/chat/completions", true},
		{"public v4", "http://8.8.8.8/mcp", true},
		{"docker host alias is not local to the browser", "http://host.docker.internal:3000/mcp", true},
		{"scheme", "ws://127.0.0.1:3000/mcp", true},
		{"stdio has no URL", "", true},
		{"no host", "http:///mcp", true},
		{"credentials in url", "http://user:pass@127.0.0.1:3000/mcp", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateLocalMCPURL(tt.in)
			if tt.wantErr && err == nil {
				t.Fatalf("ValidateLocalMCPURL(%q) = nil, want error", tt.in)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ValidateLocalMCPURL(%q) = %v, want nil", tt.in, err)
			}
		})
	}
}

func TestNormalizeLocalMCPServers(t *testing.T) {
	t.Run("trims and drops empty header maps", func(t *testing.T) {
		out, err := NormalizeLocalMCPServers([]LocalMCPServer{
			{Name: "  local  ", URL: "  http://127.0.0.1:3000/mcp/api  ", Headers: map[string]string{}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if out[0].Name != "local" {
			t.Errorf("name = %q", out[0].Name)
		}
		// The URL is dialled exactly as entered; nothing appends /mcp.
		if out[0].URL != "http://127.0.0.1:3000/mcp/api" {
			t.Errorf("url = %q", out[0].URL)
		}
		if out[0].Headers != nil {
			t.Errorf("headers = %v, want nil", out[0].Headers)
		}
	})

	t.Run("names must be distinct", func(t *testing.T) {
		_, err := NormalizeLocalMCPServers([]LocalMCPServer{
			{Name: "Local", URL: "http://127.0.0.1:3000/mcp"},
			{Name: "local", URL: "http://127.0.0.1:3001/mcp"},
		})
		if err == nil {
			t.Fatal("duplicate name accepted; the name qualifies tool names on collision")
		}
	})

	t.Run("bounded", func(t *testing.T) {
		many := make([]LocalMCPServer, LocalMCPMaxServers+1)
		for i := range many {
			many[i] = LocalMCPServer{Name: string(rune('a' + i)), URL: "http://127.0.0.1:3000/mcp"}
		}
		if _, err := NormalizeLocalMCPServers(many); err == nil {
			t.Fatal("unbounded registry accepted")
		}
	})

	t.Run("remote AT gateway needs a token", func(t *testing.T) {
		const remote = "https://at.example.com/gateway/v1/mcp/tools"
		for _, headers := range []map[string]string{
			nil,
			{"Authorization": ""},
			{"Authorization": "Bearer "},
			{"Authorization": "Basic abc"},
			{"X-Other": "at_123"},
		} {
			if _, err := NormalizeLocalMCPServers([]LocalMCPServer{{Name: "remote", URL: remote, Headers: headers}}); err == nil {
				t.Errorf("headers %v accepted without a gateway token", headers)
			}
		}
		for _, headers := range []map[string]string{
			{"Authorization": "Bearer at_123"},
			{"x-api-key": "at_123"},
		} {
			if _, err := NormalizeLocalMCPServers([]LocalMCPServer{{Name: "remote", URL: remote, Headers: headers}}); err != nil {
				t.Errorf("headers %v rejected: %v", headers, err)
			}
		}
		// Local records keep working without any header.
		if _, err := NormalizeLocalMCPServers([]LocalMCPServer{{Name: "local", URL: "http://127.0.0.1:3000/mcp"}}); err != nil {
			t.Errorf("local record rejected: %v", err)
		}
	})
}
