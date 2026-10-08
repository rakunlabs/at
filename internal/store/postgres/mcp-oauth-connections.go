package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.MCPOAuthConnectionStorer = (*Postgres)(nil)

// ResolvePersonalConnectionForUse returns the caller's newest personal
// connection for provider. Only the owner ever reaches it, administrators
// included, and it still requires connections.use in the workspace.
func (p *Postgres) ResolvePersonalConnectionForUse(ctx context.Context, provider string) (*service.Connection, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if a.UserID == "" || provider == "" {
		return nil, nil
	}
	scope, err := businessPredicate(a, "connections.use", "id")
	if err != nil {
		return nil, err
	}
	var row connectionRow
	found, err := p.goqu.From(p.tableConnections).
		Where(scope, goqu.Ex{"owner_user_id": a.UserID, "provider": provider}).
		Order(goqu.C("created_at").Desc(), goqu.C("id").Desc()).
		Limit(1).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("resolve personal connection: %w", err)
	}
	if !found {
		return nil, nil
	}
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	return connectionRowToRecord(row, p.encKey)
}

// WithMCPOAuthTokens serializes token use and refresh on the connection row.
// The callback sees the stored credential (never a caller's cached copy), so
// a replica that lost the race adopts the token another one already rotated
// instead of spending the same refresh token again.
func (p *Postgres) WithMCPOAuthTokens(ctx context.Context, connectionID string, change func(*service.MCPOAuthCredential) error) (*service.MCPOAuthCredential, error) {
	if connectionID == "" || change == nil {
		return nil, service.ErrAccessDenied
	}
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	scope, err := businessPredicate(a, "connections.use", "id")
	if err != nil {
		return nil, err
	}
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin MCP OAuth token use: %w", err)
	}
	defer tx.Rollback()
	var row connectionRow
	found, err := tx.From(p.tableConnections).
		Where(scope, connectionUsePredicate(a), goqu.C("id").Eq(connectionID)).
		ForUpdate(goqu.Wait).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("lock MCP OAuth connection: %w", err)
	}
	if !found {
		return nil, service.ErrAccessResourceNotFound
	}
	creds, err := decryptConnectionCredentials(row.Credentials, p.encKey)
	if err != nil {
		return nil, err
	}
	if creds.MCPOAuth == nil {
		return nil, fmt.Errorf("connection %q holds no MCP authorization", connectionID)
	}
	before := *creds.MCPOAuth
	current := *creds.MCPOAuth
	changeErr := change(&current)
	if changeErr != nil && !errors.Is(changeErr, service.ErrMCPOAuthReauthRequired) {
		return nil, changeErr
	}
	if changeErr != nil {
		current = before
		current.NeedsReauth = true
		current.AccessToken = ""
	}
	if current.AccessToken == before.AccessToken && current.RefreshToken == before.RefreshToken &&
		current.ExpiresAt.Equal(before.ExpiresAt) && current.NeedsReauth == before.NeedsReauth {
		return &current, changeErr
	}
	// The pinned endpoint, client and resource never change through refresh.
	current.TokenEndpoint, current.ClientID, current.ClientSecret = before.TokenEndpoint, before.ClientID, before.ClientSecret
	current.Issuer, current.Resource, current.MCPURL = before.Issuer, before.Resource, before.MCPURL
	creds.MCPOAuth = &current
	sealed, err := encryptConnectionCredentials(creds, p.encKey)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Update(p.tableConnections).Set(goqu.Record{
		"credentials": sealed, "updated_at": time.Now().UTC(), "updated_by": "system:mcp-oauth-refresh",
	}).Where(goqu.C("id").Eq(connectionID)).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("save MCP OAuth tokens: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit MCP OAuth tokens: %w", err)
	}
	return &current, changeErr
}
