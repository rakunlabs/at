package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// mcpOAuthAccountTarget is one OAuth upstream the caller may connect an
// account to, plus the caller's own account for it. It never carries the
// upstream URL beyond its origin, headers or tokens: management reads redact
// upstreams for non-writers, and this list must not undo that.
type mcpOAuthAccountTarget struct {
	SetID         string                   `json:"set_id"`
	SetName       string                   `json:"set_name"`
	SetScope      string                   `json:"set_scope"`
	UpstreamIndex int                      `json:"upstream_index"`
	Provider      string                   `json:"provider"`
	Server        string                   `json:"server"`
	Accounts      []string                 `json:"accounts"`
	Account       *mcpOAuthAccountStatus   `json:"account,omitempty"`
	Shared        *mcpOAuthSharedReference `json:"shared,omitempty"`
}

type mcpOAuthAccountStatus struct {
	ConnectionID string `json:"connection_id"`
	Name         string `json:"name"`
	Label        string `json:"label,omitempty"`
	NeedsReauth  bool   `json:"needs_reauth,omitempty"`
}

type mcpOAuthSharedReference struct {
	ConnectionID string `json:"connection_id"`
}

// MCPOAuthAccountsAPI handles GET /api/v1/mcp/oauth/accounts. It lists the
// OAuth upstreams of every MCP set the caller may use, so a member who cannot
// edit a set can still connect their own account to it.
func (s *Server) MCPOAuthAccountsAPI(w http.ResponseWriter, r *http.Request) {
	if s.mcpSetStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	resolver, ok := s.mcpSetStore.(service.WorkspaceCredentialStorer)
	if !ok {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	principal, ok := service.AccessPrincipalFromContext(r.Context())
	if !ok || principal.UserID == "" {
		httpResponse(w, "MCP accounts require a signed-in account", http.StatusForbidden)
		return
	}
	runCtx, err := s.bindRuntimePrincipal(r.Context(), "mcp-oauth-accounts")
	if err != nil {
		httpResponse(w, "runtime identity unavailable", http.StatusForbidden)
		return
	}
	sets, err := s.mcpSetStore.ListMCPSets(r.Context(), nil)
	if err != nil {
		httpResponse(w, "failed to list MCP sets", http.StatusInternalServerError)
		return
	}

	out := []mcpOAuthAccountTarget{}
	if sets == nil {
		httpResponseJSON(w, out, http.StatusOK)
		return
	}
	connections := map[string][]service.Connection{}
	for _, listed := range sets.Data {
		if service.CheckExecution(runCtx, service.ExecutionAction{Kind: "resource", Name: "mcp.use", ResourceID: listed.Name}) != nil {
			continue
		}
		full, err := resolver.ResolveMCPSetForUse(runCtx, listed.Name)
		if err != nil || full == nil || full.ID != listed.ID {
			continue
		}
		for i, upstream := range full.Config.MCPUpstreams {
			if upstream.Auth == nil {
				continue
			}
			one := []service.MCPUpstream{upstream}
			if service.NormalizeMCPUpstreamAuth(one) != nil {
				continue
			}
			auth := one[0].Auth
			target := mcpOAuthAccountTarget{
				SetID: full.ID, SetName: full.Name, SetScope: service.ConnectionScope(full.OwnerUserID),
				UpstreamIndex: i, Provider: auth.Provider, Server: mcpServerOrigin(upstream.URL),
				Accounts: auth.Accounts,
			}
			if auth.SharedConnectionID != "" {
				target.Shared = &mcpOAuthSharedReference{ConnectionID: auth.SharedConnectionID}
			}
			list, cached := connections[auth.Provider]
			if !cached {
				list = s.personalMCPConnections(r.Context(), auth.Provider, principal.UserID)
				connections[auth.Provider] = list
			}
			for _, c := range list {
				if service.SameMCPResource(c.Credentials.MCPOAuth.MCPURL, upstream.URL) {
					target.Account = &mcpOAuthAccountStatus{ConnectionID: c.ID, Name: c.Name, Label: c.AccountLabel, NeedsReauth: c.Credentials.MCPOAuth.NeedsReauth}
					break
				}
			}
			out = append(out, target)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SetName != out[j].SetName {
			return out[i].SetName < out[j].SetName
		}
		return out[i].UpstreamIndex < out[j].UpstreamIndex
	})
	httpResponseJSON(w, out, http.StatusOK)
}

// personalMCPConnections returns the caller's own MCP OAuth connections for a
// provider. Other accounts' rows are never considered, even for an
// administrator who may list them.
func (s *Server) personalMCPConnections(ctx context.Context, provider, userID string) []service.Connection {
	if s.connectionStore == nil {
		return nil
	}
	list, err := s.connectionStore.ListConnectionsByProvider(ctx, provider)
	if err != nil {
		slog.Warn("list MCP OAuth connections failed", "provider", provider, "error", err)
		return nil
	}
	var mine []service.Connection
	for _, c := range list {
		if c.OwnerUserID == userID && c.Credentials.MCPOAuth != nil {
			mine = append(mine, c)
		}
	}
	return mine
}

// mcpServerOrigin reduces an upstream URL to scheme://host, which identifies
// the server without exposing paths or query parameters that may carry secrets.
func mcpServerOrigin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}
