package service

import "testing"

func TestMCPAuthRedirectRules(t *testing.T) {
	for _, tt := range []struct {
		uri string
		ok  bool
	}{
		{"https://claude.ai/api/mcp/auth_callback", true},
		{"http://127.0.0.1:43110/callback", true},
		{"http://localhost:8080/cb", true},
		{"http://[::1]:9/cb", true},
		{"http://example.com/cb", false},
		{"https://example.com/cb#frag", false},
		{"https://user:pw@example.com/cb", false},
		{"javascript:alert(1)", false},
		{"/relative", false},
	} {
		if got := ValidMCPAuthRedirectURI(tt.uri) == nil; got != tt.ok {
			t.Errorf("ValidMCPAuthRedirectURI(%q) = %v, want %v", tt.uri, got, tt.ok)
		}
	}

	registered := []string{"http://127.0.0.1:1234/callback", "https://app.example/cb"}
	for _, tt := range []struct {
		uri string
		ok  bool
	}{
		{"https://app.example/cb", true},
		{"https://app.example/cb2", false},
		{"http://127.0.0.1:5555/callback", true}, // loopback: any port (RFC 8252)
		{"http://127.0.0.1:5555/other", false},
		{"http://localhost:1234/callback", false}, // host must match
	} {
		if got := MCPAuthRedirectMatches(registered, tt.uri); got != tt.ok {
			t.Errorf("MCPAuthRedirectMatches(%q) = %v, want %v", tt.uri, got, tt.ok)
		}
	}

	patterns := []string{"http://127.0.0.1*", "https://claude.ai/api/mcp/auth_callback"}
	for _, tt := range []struct {
		uri string
		ok  bool
	}{
		{"http://127.0.0.1:9/cb", true},
		{"https://claude.ai/api/mcp/auth_callback", true},
		{"https://evil.example/cb", false},
	} {
		if got := MCPAuthRedirectAllowed(patterns, tt.uri); got != tt.ok {
			t.Errorf("MCPAuthRedirectAllowed(%q) = %v, want %v", tt.uri, got, tt.ok)
		}
	}
	if !MCPAuthRedirectAllowed(nil, "https://anything.example/cb") || MCPAuthRedirectAllowed(nil, "http://example.com/cb") {
		t.Error("empty pattern list must admit exactly https and loopback")
	}
	if err := NormalizeMCPServerOAuth(&MCPServerOAuth{RedirectPatterns: []string{"not a url"}}); err == nil {
		t.Error("relative redirect pattern accepted")
	}
}
