package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

func TestMCPOAuthAccountResolution(t *testing.T) {
	const mcpURL = "https://mcp.example/mcp"
	f := newMachineFixture(t)
	f.s.connectionStore = f.store
	cred := func(access string) service.ConnectionCredentials {
		return service.ConnectionCredentials{MCPOAuth: &service.MCPOAuthCredential{
			AccessToken: access, ExpiresAt: time.Now().Add(time.Hour), ClientID: "c",
			Issuer: "https://as.example", TokenEndpoint: "https://as.example/token", MCPURL: mcpURL,
		}}
	}
	admin := mustPrincipal(t, f.ctx)
	// The administrator's own personal account, plus a shared and an
	// agent-bound workspace account.
	mine, err := f.store.CreateConnection(f.ctx, service.Connection{OwnerUserID: admin.UserID, Provider: "mcp-gh", Name: "mine", Credentials: cred("user-token")})
	if err != nil {
		t.Fatal(err)
	}
	shared, err := f.store.CreateConnection(f.ctx, service.Connection{Provider: "mcp-gh", Name: "shared", Credentials: cred("shared-token")})
	if err != nil {
		t.Fatal(err)
	}
	agentConn, err := f.store.CreateConnection(f.ctx, service.Connection{Provider: "mcp-gh", Name: "bot", Credentials: cred("agent-token")})
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.store.CreateConnection(f.ctx, service.Connection{Provider: "mcp-gh", Name: "other-server", Credentials: service.ConnectionCredentials{MCPOAuth: &service.MCPOAuthCredential{AccessToken: "wrong-audience", ClientID: "c", MCPURL: "https://elsewhere.example/mcp"}}})
	if err != nil {
		t.Fatal(err)
	}
	_ = mine
	upstream := func(accounts []string, sharedID string) service.MCPUpstream {
		return service.MCPUpstream{URL: mcpURL, Auth: &service.MCPUpstreamAuth{Provider: "mcp-gh", Accounts: accounts, SharedConnectionID: sharedID}}
	}
	token := func(ctx context.Context, u service.MCPUpstream) (string, error) {
		source, err := f.s.resolveMCPUpstreamToken(ctx, u)
		if err != nil {
			return "", err
		}
		return source(ctx, "")
	}
	withAgent := workflow.ContextWithAgentConnections(f.ctx, map[string]string{"mcp-gh": agentConn.ID}, nil)

	for _, tt := range []struct {
		name     string
		ctx      context.Context
		upstream service.MCPUpstream
		want     string
		err      string
	}{
		{"default is user", f.ctx, upstream(nil, ""), "user-token", ""},
		{"agent first", withAgent, upstream([]string{"agent", "user"}, ""), "agent-token", ""},
		{"agent missing falls back", f.ctx, upstream([]string{"agent", "user"}, ""), "user-token", ""},
		{"shared listed", f.ctx, upstream([]string{"shared"}, shared.ID), "shared-token", ""},
		{"user before shared", f.ctx, upstream([]string{"user", "shared"}, shared.ID), "user-token", ""},
		// Configured but not listed: never used.
		{"unlisted shared ignored", withAgent, upstream([]string{"agent"}, shared.ID), "agent-token", ""},
		{"agent only without binding", f.ctx, upstream([]string{"agent"}, shared.ID), "", "connect your account"},
		{"other audience never sent", f.ctx, upstream([]string{"shared"}, other.ID), "", "connect your account"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := token(tt.ctx, tt.upstream)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) || !errors.Is(err, service.ErrMCPAccountMissing) {
					t.Fatalf("want %q, got %q %v", tt.err, got, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q %v, want %q", got, err, tt.want)
			}
		})
	}

	t.Run("gateway identities", func(t *testing.T) {
		// The MCP server runs as the member (Run as). The member holds no
		// account, the token owner (the administrator) does.
		srv, err := f.store.CreateMCPServer(f.ctx, service.MCPServer{Name: "gh-gateway"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.saveRuntimeBinding(f.ctx, "mcp", srv.ID, false, f.member); err != nil {
			t.Fatal(err)
		}
		runAs, err := f.s.ResumeRuntimeSubject(t.Context(), "mcp", srv.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		u := upstream(nil, "")
		// Workspace token: the Run as account, which has none.
		workspaceToken := contextWithGatewayToken(runAs, &service.APIToken{ID: "t1", WorkspaceID: f.workspace})
		if _, err := token(workspaceToken, u); !errors.Is(err, service.ErrMCPAccountMissing) {
			t.Fatalf("workspace token used another account: %v", err)
		}
		// Personal token: the owner's own account, read as the owner only.
		personalToken := contextWithGatewayToken(runAs, &service.APIToken{ID: "t2", WorkspaceID: f.workspace, OwnerUserID: admin.UserID})
		if got, err := token(personalToken, u); err != nil || got != "user-token" {
			t.Fatalf("personal token: %q %v", got, err)
		}
		// The Run as account never reads the owner's personal connection by ID.
		if got, err := token(runAs, upstream([]string{"shared"}, mine.ID)); !errors.Is(err, service.ErrMCPAccountMissing) {
			t.Fatalf("run-as read a personal connection: %q %v", got, err)
		}
	})
}

func TestMCPUpstreamAuthValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		up   service.MCPUpstream
		err  string
	}{
		{"stdio", service.MCPUpstream{Command: "npx", URL: "https://x.example/mcp", Auth: &service.MCPUpstreamAuth{}}, "HTTP MCP servers only"},
		{"static authorization", service.MCPUpstream{URL: "https://x.example/mcp", Headers: map[string]string{"authorization": "Bearer x"}, Auth: &service.MCPUpstreamAuth{}}, "Authorization header"},
		{"bad source", service.MCPUpstream{URL: "https://x.example/mcp", Auth: &service.MCPUpstreamAuth{Accounts: []string{"everyone"}}}, "unknown account source"},
		{"duplicate source", service.MCPUpstream{URL: "https://x.example/mcp", Auth: &service.MCPUpstreamAuth{Accounts: []string{"user", "user"}}}, "listed twice"},
		{"shared without connection", service.MCPUpstream{URL: "https://x.example/mcp", Auth: &service.MCPUpstreamAuth{Accounts: []string{"shared"}}}, "needs a shared connection"},
		{"bad provider", service.MCPUpstream{URL: "https://x.example/mcp", Auth: &service.MCPUpstreamAuth{Provider: "Bad Key"}}, "provider key"},
		{"type", service.MCPUpstream{URL: "https://x.example/mcp", Auth: &service.MCPUpstreamAuth{Type: "basic"}}, "unsupported"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := service.NormalizeMCPUpstreamAuth([]service.MCPUpstream{tt.up}); err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Fatalf("want %q, got %v", tt.err, err)
			}
		})
	}
	ups := []service.MCPUpstream{{URL: "https://API.GitHubCopilot.com/mcp/", Auth: &service.MCPUpstreamAuth{SharedConnectionID: "x", Scopes: []string{"repo  read:org", "repo"}}}}
	if err := service.NormalizeMCPUpstreamAuth(ups); err != nil {
		t.Fatal(err)
	}
	a := ups[0].Auth
	if a.Type != "oauth2" || a.Provider != "mcp-api-githubcopilot-com" || len(a.Accounts) != 1 || a.Accounts[0] != "user" || a.SharedConnectionID != "" || len(a.Scopes) != 2 {
		t.Fatalf("normalized: %+v", a)
	}

	f := newMachineFixture(t)
	personal, err := f.store.CreateConnection(f.ctx, service.Connection{OwnerUserID: mustPrincipal(t, f.ctx).UserID, Provider: "mcp-x", Name: "p"})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"name":"bad","config":{"mcp_upstreams":[{"url":"https://x.example/mcp","auth":{"type":"oauth2","accounts":["shared"],"shared_connection_id":"` + personal.ID + `"}}]}}`
	f.s.mcpSetStore = f.store
	w := httptest.NewRecorder()
	f.s.CreateMCPSetAPI(w, httptest.NewRequest(http.MethodPost, "/api/v1/mcp/sets", strings.NewReader(body)).WithContext(f.ctx))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "not a workspace connection") {
		t.Fatalf("personal connection accepted as shared: %d %s", w.Code, w.Body.String())
	}
}
