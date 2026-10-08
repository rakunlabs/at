package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/mcpauth"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// mcpOAuthAccount is the connection one OAuth upstream authenticates with for
// one run: which account source matched and the connection it named. It never
// holds a token; tokens are read under the row lock on every request.
type mcpOAuthAccount struct {
	Source       string
	ConnectionID string
	Provider     string
	// identity installs the principal the connection is read with. It is the
	// run's own context except for a personal gateway token, whose owner's
	// account is used to read (only) the owner's personal connection.
	identity func(context.Context) (context.Context, error)
}

// mcpUpstreamClientOptions returns the HTTP client options for an upstream:
// static headers, plus OAuth when the upstream declares it. A failure to find
// a usable account is an error, never a fallback to static headers.
func (s *Server) mcpUpstreamClientOptions(ctx context.Context, upstream service.MCPUpstream) ([]service.HTTPMCPClientOption, error) {
	var opts []service.HTTPMCPClientOption
	if len(upstream.Headers) > 0 {
		opts = append(opts, service.WithHeaders(upstream.Headers))
	}
	if upstream.Auth == nil {
		return opts, nil
	}
	source, err := s.resolveMCPUpstreamToken(ctx, upstream)
	if err != nil {
		return nil, err
	}
	return append(opts, service.WithMCPAccessToken(source)), nil
}

// resolveMCPUpstreamToken walks the upstream's ordered account sources and
// returns a token source for the first usable connection. An unlisted source
// is never consulted, so a shared account is used only when named.
func (s *Server) resolveMCPUpstreamToken(ctx context.Context, upstream service.MCPUpstream) (service.MCPAccessTokenSource, error) {
	auth := upstream.Auth
	if auth == nil {
		return nil, errors.New("MCP upstream has no OAuth configuration")
	}
	normalized := []service.MCPUpstream{upstream}
	normalized[0].Auth = &service.MCPUpstreamAuth{}
	*normalized[0].Auth = *auth
	if err := service.NormalizeMCPUpstreamAuth(normalized); err != nil {
		return nil, err
	}
	upstream = normalized[0]
	account, err := s.findMCPOAuthAccount(ctx, upstream)
	if err != nil {
		return nil, err
	}
	return s.mcpOAuthTokenSource(ctx, upstream, account), nil
}

func (s *Server) findMCPOAuthAccount(ctx context.Context, upstream service.MCPUpstream) (*mcpOAuthAccount, error) {
	auth := upstream.Auth
	runtime, ok := s.store.(service.MCPOAuthConnectionStorer)
	if !ok {
		return nil, errors.New("MCP OAuth requires the connection store")
	}
	credentials, _ := s.store.(service.WorkspaceCredentialStorer)
	_, _, bound := service.ExecutionFromContext(ctx)
	runIdentity := func(c context.Context) (context.Context, error) { return c, nil }

	for _, source := range auth.Accounts {
		var (
			conn     *service.Connection
			identity = runIdentity
			err      error
		)
		switch source {
		case service.MCPAccountUser:
			identity, err = s.mcpUserIdentity(ctx)
			if err != nil {
				return nil, err
			}
			if identity == nil {
				continue
			}
			userCtx, idErr := identity(ctx)
			if idErr != nil {
				return nil, idErr
			}
			conn, err = runtime.ResolvePersonalConnectionForUse(userCtx, auth.Provider)
		case service.MCPAccountAgent:
			bindings, _ := workflow.AgentConnectionsFromContext(ctx, "")
			id := bindings[auth.Provider]
			if id == "" {
				continue
			}
			conn, err = s.mcpConnectionForUse(ctx, credentials, bound, id)
		case service.MCPAccountShared:
			conn, err = s.mcpConnectionForUse(ctx, credentials, bound, auth.SharedConnectionID)
			if err == nil && conn != nil && conn.OwnerUserID != "" {
				// A shared source must be a workspace connection; a personal one
				// would hand one account's identity to everyone using the set.
				conn = nil
			}
		}
		if err != nil {
			if errors.Is(err, service.ErrAccessDenied) || errors.Is(err, service.ErrExecutionDenied) {
				continue
			}
			return nil, fmt.Errorf("resolve %s MCP account: %w", source, err)
		}
		if conn == nil || conn.Provider != auth.Provider || conn.Credentials.MCPOAuth == nil {
			continue
		}
		// Tokens are audience-bound to one server; a connection authorized for
		// another URL is not this upstream's account even under the same key.
		if !service.SameMCPResource(conn.Credentials.MCPOAuth.MCPURL, upstream.URL) {
			continue
		}
		if conn.Credentials.MCPOAuth.NeedsReauth {
			return nil, mcpReauthError(auth.Provider, source)
		}
		return &mcpOAuthAccount{Source: source, ConnectionID: conn.ID, Provider: auth.Provider, identity: identity}, nil
	}
	return nil, fmt.Errorf("%w for %s (account sources: %v): connect your account under Connections, or use the Connect button on the MCP set's upstream", service.ErrMCPAccountMissing, auth.Provider, auth.Accounts)
}

func mcpReauthError(provider, source string) error {
	return fmt.Errorf("%w: the %s account for %s must be reconnected under Connections", service.ErrMCPOAuthReauthRequired, source, provider)
}

// mcpUserIdentity decides whose personal connection the user source reads.
// A personal gateway API token uses its owner; everything else (sessions,
// bots, triggers, workspace tokens) uses the account the run executes as.
// Nil means the run has no account to look up.
func (s *Server) mcpUserIdentity(ctx context.Context) (func(context.Context) (context.Context, error), error) {
	if token := gatewayTokenFromContext(ctx); token != nil && token.OwnerUserID != "" {
		workspaces, ok := s.store.(service.WorkspaceStorer)
		if !ok {
			return nil, service.ErrAccessDenied
		}
		workspaceID, ownerID := token.WorkspaceID, token.OwnerUserID
		// The owner's principal is resolved live on every read. It is used
		// only to read the owner's own personal connection, so the token
		// owner never gains the Run as account's authority, and the Run as
		// account never reads the owner's credentials.
		return func(c context.Context) (context.Context, error) {
			principal, _, err := workspaces.ResolveWorkspaceAccess(c, workspaceID, ownerID, "")
			if err != nil {
				return nil, err
			}
			return service.WithAccessPrincipal(c, principal), nil
		}, nil
	}
	if p, _, ok := service.ExecutionFromContext(ctx); ok && p.UserID != "" {
		return func(c context.Context) (context.Context, error) {
			if err := service.CheckExecution(c, service.ExecutionAction{Kind: "resource", Name: "execution.run"}); err != nil {
				return nil, err
			}
			return c, nil
		}, nil
	}
	if p, ok := service.AccessPrincipalFromContext(ctx); ok && p.UserID != "" {
		return func(c context.Context) (context.Context, error) { return c, nil }, nil
	}
	return nil, nil
}

func (s *Server) mcpConnectionForUse(ctx context.Context, store service.WorkspaceCredentialStorer, bound bool, id string) (*service.Connection, error) {
	if id == "" || store == nil {
		return nil, nil
	}
	if bound {
		if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "connections.use", ResourceID: id}); err != nil {
			return nil, err
		}
	}
	return store.ResolveConnectionForUse(ctx, id)
}

// mcpOAuthTokenSource closes over the connection ID, never a token. Each call
// re-admits the account (live membership, connections.use, ownership) and
// reads the stored credential under its row lock, refreshing at most once
// per rejected token across every replica.
func (s *Server) mcpOAuthTokenSource(base context.Context, upstream service.MCPUpstream, account *mcpOAuthAccount) service.MCPAccessTokenSource {
	runtime := s.store.(service.MCPOAuthConnectionStorer)
	auth := upstream.Auth
	var traced sync.Map
	return func(reqCtx context.Context, rejected string) (string, error) {
		// Identity comes from the run that built the client; cancellation and
		// trace placement from the request being authorized.
		ctx := mcpIdentityContext{Context: reqCtx, values: base}
		if account.Source != service.MCPAccountUser {
			if _, _, bound := service.ExecutionFromContext(ctx); bound {
				if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "connections.use", ResourceID: account.ConnectionID}); err != nil {
					return "", err
				}
			}
		}
		storeCtx, err := account.identity(ctx)
		if err != nil {
			return "", err
		}
		cred, err := runtime.WithMCPOAuthTokens(storeCtx, account.ConnectionID, func(c *service.MCPOAuthCredential) error {
			if !service.SameMCPResource(c.MCPURL, upstream.URL) {
				return fmt.Errorf("the %s connection is authorized for a different MCP server", auth.Provider)
			}
			if c.NeedsReauth {
				return service.ErrMCPOAuthReauthRequired
			}
			now := time.Now()
			if c.Fresh(now) && (rejected == "" || c.AccessToken != rejected) {
				// Fresh, or another request/replica already replaced the token
				// the server rejected: adopt it without refreshing again.
				return nil
			}
			if c.RefreshToken == "" {
				return service.ErrMCPOAuthReauthRequired
			}
			tok, err := mcpauth.Refresh(reqCtx, mcpauth.Client(mcpauth.AllowsPrivate(c.MCPURL)), c.TokenEndpoint, c.ClientID, c.ClientSecret, c.RefreshToken, c.Resource)
			if err != nil {
				if mcpauth.IsInvalidGrant(err) {
					return service.ErrMCPOAuthReauthRequired
				}
				return fmt.Errorf("refresh %s MCP token: %w", auth.Provider, err)
			}
			c.AccessToken = tok.AccessToken
			if tok.RefreshToken != "" {
				c.RefreshToken = tok.RefreshToken
			}
			c.ExpiresAt = tok.ExpiresAt(now)
			return nil
		})
		if err != nil {
			if errors.Is(err, service.ErrMCPOAuthReauthRequired) {
				slog.Warn("MCP OAuth account needs re-authorization", "provider", auth.Provider, "connection_id", account.ConnectionID, "source", account.Source)
				return "", mcpReauthError(auth.Provider, account.Source)
			}
			return "", err
		}
		s.traceMCPOAuthAccount(reqCtx, &traced, account)
		return cred.AccessToken, nil
	}
}

// traceMCPOAuthAccount records which account authenticated a tool call, once
// per enclosing observation, as an event beneath it. The connection ID and
// source are recorded; tokens never are.
func (s *Server) traceMCPOAuthAccount(ctx context.Context, seen *sync.Map, account *mcpOAuthAccount) {
	tp, ok := service.TraceParentFromContext(ctx)
	if !ok || s.llmCallStore == nil {
		return
	}
	if _, loaded := seen.LoadOrStore(tp.ObservationID, true); loaded {
		return
	}
	s.recordLLMCallAsync(ctx, llmAuditParams{
		source:  "mcp",
		obsType: service.ObservationEvent,
		name:    "mcp_oauth_account",
		metadata: map[string]any{
			"provider":       account.Provider,
			"account_source": account.Source,
			"connection_id":  account.ConnectionID,
		},
	})
}

// mcpIdentityContext carries the values (principal, execution authority,
// agent bindings, gateway token) of the context that built an MCP client
// while honouring the cancellation of the request currently being sent.
type mcpIdentityContext struct {
	context.Context
	values context.Context
}

func (c mcpIdentityContext) Value(key any) any { return c.values.Value(key) }
