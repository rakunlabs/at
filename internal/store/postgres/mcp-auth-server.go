package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.MCPAuthServerStorer = (*Postgres)(nil)

type mcpAuthClientRow struct {
	ID           string         `db:"id"`
	ClientID     string         `db:"client_id"`
	WorkspaceID  sql.NullString `db:"workspace_id"`
	MCPServerID  string         `db:"mcp_server_id"`
	Name         string         `db:"name"`
	RedirectURIs []byte         `db:"redirect_uris"`
	SecretHash   string         `db:"secret_hash"`
	Dynamic      bool           `db:"dynamic"`
	CreatedBy    string         `db:"created_by"`
	CreatedAt    time.Time      `db:"created_at"`
	LastUsedAt   sql.NullTime   `db:"last_used_at"`
}

var mcpAuthClientColumns = []any{"id", "client_id", "workspace_id", "mcp_server_id", "name", "redirect_uris", "secret_hash", "dynamic", "created_by", "created_at", "last_used_at"}

func mcpAuthClientRowToRecord(row mcpAuthClientRow) service.MCPAuthClient {
	c := service.MCPAuthClient{
		ID: row.ID, ClientID: row.ClientID, WorkspaceID: row.WorkspaceID.String, MCPServerID: row.MCPServerID,
		Name: row.Name, SecretHash: row.SecretHash, Confidential: row.SecretHash != "", Dynamic: row.Dynamic,
		CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339),
	}
	_ = json.Unmarshal(row.RedirectURIs, &c.RedirectURIs)
	if c.RedirectURIs == nil {
		c.RedirectURIs = []string{}
	}
	if row.LastUsedAt.Valid {
		c.LastUsedAt = row.LastUsedAt.Time.UTC().Format(time.RFC3339)
	}
	return c
}

type mcpAuthGrantRow struct {
	ID          string       `db:"id"`
	WorkspaceID string       `db:"workspace_id"`
	UserID      string       `db:"user_id"`
	MCPServerID string       `db:"mcp_server_id"`
	ClientID    string       `db:"client_id"`
	ClientName  string       `db:"client_name"`
	Resource    string       `db:"resource"`
	Revoked     bool         `db:"revoked"`
	CreatedAt   time.Time    `db:"created_at"`
	LastUsedAt  sql.NullTime `db:"last_used_at"`
}

var mcpAuthGrantColumns = []any{"id", "workspace_id", "user_id", "mcp_server_id", "client_id", "client_name", "resource", "revoked", "created_at", "last_used_at"}

func mcpAuthGrantRowToRecord(row mcpAuthGrantRow) service.MCPAuthGrant {
	g := service.MCPAuthGrant{
		ID: row.ID, WorkspaceID: row.WorkspaceID, UserID: row.UserID, MCPServerID: row.MCPServerID,
		ClientID: row.ClientID, ClientName: row.ClientName, Resource: row.Resource, Revoked: row.Revoked,
		CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339),
	}
	if row.LastUsedAt.Valid {
		g.LastUsedAt = row.LastUsedAt.Time.UTC().Format(time.RFC3339)
	}
	return g
}

// RegisterDynamicMCPAuthClient stores an RFC 7591 registration. It is
// unauthenticated, so it is bounded: past the limit the oldest clients that
// never obtained a grant are pruned first, and registration fails only when
// every stored client is in use.
func (p *Postgres) RegisterDynamicMCPAuthClient(ctx context.Context, c service.MCPAuthClient) (*service.MCPAuthClient, error) {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin client registration: %w", err)
	}
	defer tx.Rollback()
	table := p.workspaceTable("mcp_auth_clients")
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", fmt.Sprintf("%v:dynamic", table)); err != nil {
		return nil, fmt.Errorf("lock client registration: %w", err)
	}
	var count int64
	if _, err := tx.From(table).Select(goqu.COUNT("*")).Where(goqu.Ex{"dynamic": true}).ScanValContext(ctx, &count); err != nil {
		return nil, fmt.Errorf("count dynamic clients: %w", err)
	}
	if count >= service.MCPAuthDynamicClientLimit {
		grants := p.workspaceTable("mcp_auth_grants")
		unused := tx.From(table).Select("id").Where(
			goqu.Ex{"dynamic": true},
			goqu.C("client_id").NotIn(tx.From(grants).Select("client_id").Where(goqu.Ex{"revoked": false})),
		).Order(goqu.C("created_at").Asc()).Limit(uint(count - service.MCPAuthDynamicClientLimit + 1))
		res, err := tx.Delete(table).Where(goqu.C("id").In(unused)).Executor().ExecContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("prune dynamic clients: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, errors.New("too many registered clients")
		}
	}
	c.ID, c.Dynamic, c.WorkspaceID, c.MCPServerID = ulid.Make().String(), true, "", ""
	if err := p.insertMCPAuthClient(ctx, tx, c); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit client registration: %w", err)
	}
	c.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	c.Confidential = c.SecretHash != ""
	return &c, nil
}

func (p *Postgres) insertMCPAuthClient(ctx context.Context, tx *goqu.TxDatabase, c service.MCPAuthClient) error {
	uris, err := json.Marshal(c.RedirectURIs)
	if err != nil {
		return err
	}
	var workspace any
	if c.WorkspaceID != "" {
		workspace = c.WorkspaceID
	}
	_, err = tx.Insert(p.workspaceTable("mcp_auth_clients")).Rows(goqu.Record{
		"id": c.ID, "client_id": c.ClientID, "workspace_id": workspace, "mcp_server_id": c.MCPServerID,
		"name": c.Name, "redirect_uris": goqu.L("?::jsonb", string(uris)), "secret_hash": c.SecretHash,
		"dynamic": c.Dynamic, "created_by": c.CreatedBy,
	}).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("create MCP auth client: %w", err)
	}
	return nil
}

// CreateMCPAuthClient pre-registers a client for one MCP server of the
// caller's workspace. Requires mcp.write.
func (p *Postgres) CreateMCPAuthClient(ctx context.Context, c service.MCPAuthClient) (*service.MCPAuthClient, error) {
	w, err := p.beginBusinessWrite(ctx, p.tableMCPServers, "mcp.write", c.MCPServerID)
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()
	var id string
	found, err := w.tx.From(p.tableMCPServers).Select("id").Where(goqu.Ex{"workspace_id": w.actor.WorkspaceID, "id": c.MCPServerID}).ForKeyShare(goqu.Wait).ScanValContext(ctx, &id)
	if err != nil {
		return nil, fmt.Errorf("lock MCP server: %w", err)
	}
	if !found {
		return nil, service.ErrAccessResourceNotFound
	}
	c.ID, c.Dynamic, c.WorkspaceID = ulid.Make().String(), false, w.actor.WorkspaceID
	if err := p.insertMCPAuthClient(ctx, w.tx, c); err != nil {
		return nil, err
	}
	if err := w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit MCP auth client: %w", err)
	}
	c.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	c.Confidential = c.SecretHash != ""
	return &c, nil
}

// ListMCPAuthClients lists the pre-registered clients of one MCP server of
// the caller's workspace. Requires mcp.write: client metadata is management.
func (p *Postgres) ListMCPAuthClients(ctx context.Context, mcpServerID string) ([]service.MCPAuthClient, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if !a.Allows("mcp.write", service.AccessResource{WorkspaceID: a.WorkspaceID, ID: mcpServerID}) {
		return nil, service.ErrAccessDenied
	}
	var rows []mcpAuthClientRow
	if err := p.goqu.From(p.workspaceTable("mcp_auth_clients")).Select(mcpAuthClientColumns...).
		Where(goqu.Ex{"workspace_id": a.WorkspaceID, "mcp_server_id": mcpServerID, "dynamic": false}).
		Order(goqu.C("created_at").Asc()).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list MCP auth clients: %w", err)
	}
	out := make([]service.MCPAuthClient, 0, len(rows))
	for _, row := range rows {
		out = append(out, mcpAuthClientRowToRecord(row))
	}
	return out, nil
}

// DeleteMCPAuthClient removes a pre-registered client and revokes every grant
// issued to it.
func (p *Postgres) DeleteMCPAuthClient(ctx context.Context, id string) error {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return err
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin MCP auth client delete: %w", err)
	}
	defer tx.Rollback()
	var row mcpAuthClientRow
	found, err := tx.From(p.workspaceTable("mcp_auth_clients")).Select(mcpAuthClientColumns...).
		Where(goqu.Ex{"id": id, "workspace_id": a.WorkspaceID, "dynamic": false}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &row)
	if err != nil {
		return fmt.Errorf("lock MCP auth client: %w", err)
	}
	if !found {
		return service.ErrAccessResourceNotFound
	}
	if !a.Allows("mcp.write", service.AccessResource{WorkspaceID: a.WorkspaceID, ID: row.MCPServerID}) {
		return service.ErrAccessDenied
	}
	if _, err := tx.Delete(p.workspaceTable("mcp_auth_grants")).Where(goqu.Ex{"client_id": row.ClientID}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("revoke MCP auth client grants: %w", err)
	}
	if _, err := tx.Delete(p.workspaceTable("mcp_auth_clients")).Where(goqu.Ex{"id": id}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("delete MCP auth client: %w", err)
	}
	return tx.Commit()
}

func (p *Postgres) GetMCPAuthClient(ctx context.Context, clientID string) (*service.MCPAuthClient, error) {
	var row mcpAuthClientRow
	found, err := p.goqu.From(p.workspaceTable("mcp_auth_clients")).Select(mcpAuthClientColumns...).
		Where(goqu.Ex{"client_id": clientID}).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("get MCP auth client: %w", err)
	}
	if !found {
		return nil, nil
	}
	c := mcpAuthClientRowToRecord(row)
	return &c, nil
}

// IssueMCPAuthCode records the signed-in account's consent and a code. The
// account, workspace and session come from the context principal, which the
// caller resolved from the browser session; mcp.use on the server is
// re-checked here so a stale page cannot consent past a revoked permission.
func (p *Postgres) IssueMCPAuthCode(ctx context.Context, client service.MCPAuthClient, mcpServerID, resource, codeHash string, code service.MCPAuthCode) (*service.MCPAuthGrant, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if a.UserID == "" || a.SessionID == "" {
		return nil, service.ErrAccessDenied
	}
	if !a.Allows("mcp.read", service.AccessResource{WorkspaceID: a.WorkspaceID, ID: mcpServerID}) {
		return nil, service.ErrAccessDenied
	}
	if !client.Dynamic && (client.WorkspaceID != a.WorkspaceID || client.MCPServerID != mcpServerID) {
		return nil, service.ErrAccessDenied
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin MCP authorization: %w", err)
	}
	defer tx.Rollback()
	var serverID string
	found, err := tx.From(p.tableMCPServers).Select("id").Where(goqu.Ex{"workspace_id": a.WorkspaceID, "id": mcpServerID}).ForKeyShare(goqu.Wait).ScanValContext(ctx, &serverID)
	if err != nil {
		return nil, fmt.Errorf("lock MCP server: %w", err)
	}
	if !found {
		return nil, service.ErrAccessResourceNotFound
	}
	grants := p.workspaceTable("mcp_auth_grants")
	var row mcpAuthGrantRow
	found, err = tx.From(grants).Select(mcpAuthGrantColumns...).Where(goqu.Ex{
		"workspace_id": a.WorkspaceID, "user_id": a.UserID, "mcp_server_id": mcpServerID,
		"client_id": client.ClientID, "resource": resource, "revoked": false,
	}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("load MCP grant: %w", err)
	}
	if !found {
		row = mcpAuthGrantRow{ID: ulid.Make().String(), WorkspaceID: a.WorkspaceID, UserID: a.UserID, MCPServerID: mcpServerID, ClientID: client.ClientID, ClientName: client.Name, Resource: resource, CreatedAt: time.Now().UTC()}
		if _, err := tx.Insert(grants).Rows(goqu.Record{
			"id": row.ID, "workspace_id": row.WorkspaceID, "user_id": row.UserID, "mcp_server_id": row.MCPServerID,
			"client_id": row.ClientID, "client_name": row.ClientName, "resource": row.Resource,
		}).Executor().ExecContext(ctx); err != nil {
			return nil, fmt.Errorf("create MCP grant: %w", err)
		}
	}
	codes := p.workspaceTable("mcp_auth_codes")
	if _, err := tx.Delete(codes).Where(goqu.C("expires_at").Lte(goqu.L("clock_timestamp()"))).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("expire MCP codes: %w", err)
	}
	if _, err := tx.Insert(codes).Rows(goqu.Record{
		"code_hash": codeHash, "grant_id": row.ID, "workspace_id": a.WorkspaceID,
		"redirect_uri": code.RedirectURI, "code_challenge": code.CodeChallenge, "expires_at": code.ExpiresAt,
	}).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("store MCP code: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit MCP authorization: %w", err)
	}
	g := mcpAuthGrantRowToRecord(row)
	return &g, nil
}

// ExchangeMCPAuthCode consumes a code exactly once. Replaying a code revokes
// the grant it was issued under (RFC 6749 §4.1.2), because a second
// presentation means the code leaked.
func (p *Postgres) ExchangeMCPAuthCode(ctx context.Context, codeHash, clientID, redirectURI string, verify func(string) bool, accessHash, refreshHash string) (*service.MCPAuthGrant, error) {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin MCP code exchange: %w", err)
	}
	defer tx.Rollback()
	var code struct {
		GrantID       string    `db:"grant_id"`
		RedirectURI   string    `db:"redirect_uri"`
		CodeChallenge string    `db:"code_challenge"`
		ExpiresAt     time.Time `db:"expires_at"`
	}
	found, err := tx.Delete(p.workspaceTable("mcp_auth_codes")).Where(goqu.Ex{"code_hash": codeHash}).
		Returning("grant_id", "redirect_uri", "code_challenge", "expires_at").Executor().ScanStructContext(ctx, &code)
	if err != nil {
		return nil, fmt.Errorf("consume MCP code: %w", err)
	}
	if !found {
		return nil, service.ErrMCPAuthInvalidGrant
	}
	grant, err := p.lockMCPAuthGrant(ctx, tx, code.GrantID)
	if err != nil {
		return nil, err
	}
	if grant == nil || grant.Revoked || grant.ClientID != clientID || code.RedirectURI != redirectURI || !time.Now().Before(code.ExpiresAt) || !verify(code.CodeChallenge) {
		// A failed exchange still consumed the code; commit that.
		_ = tx.Commit()
		return nil, service.ErrMCPAuthInvalidGrant
	}
	if err := p.insertMCPAuthTokens(ctx, tx, grant, accessHash, refreshHash); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit MCP code exchange: %w", err)
	}
	g := mcpAuthGrantRowToRecord(*grant)
	return &g, nil
}

func (p *Postgres) lockMCPAuthGrant(ctx context.Context, tx *goqu.TxDatabase, id string) (*mcpAuthGrantRow, error) {
	var row mcpAuthGrantRow
	found, err := tx.From(p.workspaceTable("mcp_auth_grants")).Select(mcpAuthGrantColumns...).Where(goqu.Ex{"id": id}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("lock MCP grant: %w", err)
	}
	if !found {
		return nil, nil
	}
	return &row, nil
}

func (p *Postgres) insertMCPAuthTokens(ctx context.Context, tx *goqu.TxDatabase, grant *mcpAuthGrantRow, accessHash, refreshHash string) error {
	now := time.Now().UTC()
	tokens := p.workspaceTable("mcp_auth_tokens")
	if _, err := tx.Delete(tokens).Where(goqu.Ex{"grant_id": grant.ID}, goqu.C("expires_at").Lte(goqu.L("clock_timestamp()"))).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("expire MCP tokens: %w", err)
	}
	if _, err := tx.Insert(tokens).Rows(
		goqu.Record{"token_hash": accessHash, "grant_id": grant.ID, "workspace_id": grant.WorkspaceID, "kind": "access", "expires_at": now.Add(service.MCPAuthAccessLifetime)},
		goqu.Record{"token_hash": refreshHash, "grant_id": grant.ID, "workspace_id": grant.WorkspaceID, "kind": "refresh", "expires_at": now.Add(service.MCPAuthRefreshLifetime)},
	).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("store MCP tokens: %w", err)
	}
	if _, err := tx.Update(p.workspaceTable("mcp_auth_clients")).Set(goqu.Record{"last_used_at": now}).Where(goqu.Ex{"client_id": grant.ClientID}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("touch MCP client: %w", err)
	}
	return nil
}

// RefreshMCPAuthTokens rotates a refresh token. The presented token is
// consumed; presenting an already-rotated token is not distinguishable from
// an unknown one and is refused.
func (p *Postgres) RefreshMCPAuthTokens(ctx context.Context, refreshHash, clientID, accessHash, newRefreshHash string) (*service.MCPAuthGrant, error) {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin MCP refresh: %w", err)
	}
	defer tx.Rollback()
	var token struct {
		GrantID   string    `db:"grant_id"`
		ExpiresAt time.Time `db:"expires_at"`
	}
	found, err := tx.Delete(p.workspaceTable("mcp_auth_tokens")).Where(goqu.Ex{"token_hash": refreshHash, "kind": "refresh"}).
		Returning("grant_id", "expires_at").Executor().ScanStructContext(ctx, &token)
	if err != nil {
		return nil, fmt.Errorf("consume MCP refresh token: %w", err)
	}
	if !found || !time.Now().Before(token.ExpiresAt) {
		return nil, service.ErrMCPAuthInvalidGrant
	}
	grant, err := p.lockMCPAuthGrant(ctx, tx, token.GrantID)
	if err != nil {
		return nil, err
	}
	if grant == nil || grant.Revoked || grant.ClientID != clientID {
		return nil, service.ErrMCPAuthInvalidGrant
	}
	if err := p.insertMCPAuthTokens(ctx, tx, grant, accessHash, newRefreshHash); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit MCP refresh: %w", err)
	}
	g := mcpAuthGrantRowToRecord(*grant)
	return &g, nil
}

func (p *Postgres) ResolveMCPAuthAccessToken(ctx context.Context, accessHash string) (*service.MCPAuthAccess, error) {
	tokens, grants := p.workspaceTable("mcp_auth_tokens"), p.workspaceTable("mcp_auth_grants")
	var row mcpAuthGrantRow
	found, err := p.goqu.From(grants).Select(mcpAuthGrantColumns...).Where(
		goqu.C("revoked").Eq(false),
		goqu.C("id").In(p.goqu.From(tokens).Select("grant_id").Where(
			goqu.Ex{"token_hash": accessHash, "kind": "access"},
			goqu.C("expires_at").Gt(goqu.L("clock_timestamp()")),
		)),
	).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("resolve MCP access token: %w", err)
	}
	if !found {
		return nil, nil
	}
	if !row.LastUsedAt.Valid || time.Since(row.LastUsedAt.Time) > 5*time.Minute {
		_, _ = p.goqu.Update(grants).Set(goqu.Record{"last_used_at": time.Now().UTC()}).Where(goqu.Ex{"id": row.ID}).Executor().ExecContext(ctx)
	}
	return &service.MCPAuthAccess{Grant: mcpAuthGrantRowToRecord(row)}, nil
}

// RevokeMCPAuthToken implements RFC 7009: an unknown token, or one issued to
// another client, is a silent no-op.
func (p *Postgres) RevokeMCPAuthToken(ctx context.Context, tokenHash, clientID string) error {
	tokens, grants := p.workspaceTable("mcp_auth_tokens"), p.workspaceTable("mcp_auth_grants")
	_, err := p.goqu.Delete(grants).Where(
		goqu.Ex{"client_id": clientID},
		goqu.C("id").In(p.goqu.From(tokens).Select("grant_id").Where(goqu.Ex{"token_hash": tokenHash})),
	).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("revoke MCP token: %w", err)
	}
	return nil
}

// ListMCPAuthGrants lists the signed-in account's own grants.
func (p *Postgres) ListMCPAuthGrants(ctx context.Context) ([]service.MCPAuthGrant, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if a.UserID == "" {
		return nil, service.ErrAccessDenied
	}
	grants := p.workspaceTable("mcp_auth_grants")
	var rows []struct {
		mcpAuthGrantRow
		ServerName sql.NullString `db:"server_name"`
	}
	cols := make([]any, 0, len(mcpAuthGrantColumns)+1)
	for _, c := range mcpAuthGrantColumns {
		cols = append(cols, goqu.T(grants.GetTable()).Col(c.(string)))
	}
	cols = append(cols, goqu.T(p.tableMCPServers.GetTable()).Col("name").As("server_name"))
	if err := p.goqu.From(grants).Select(cols...).
		LeftJoin(p.tableMCPServers, goqu.On(goqu.T(p.tableMCPServers.GetTable()).Col("id").Eq(goqu.T(grants.GetTable()).Col("mcp_server_id")))).
		Where(goqu.T(grants.GetTable()).Col("workspace_id").Eq(a.WorkspaceID), goqu.T(grants.GetTable()).Col("user_id").Eq(a.UserID), goqu.T(grants.GetTable()).Col("revoked").Eq(false)).
		Order(goqu.T(grants.GetTable()).Col("created_at").Desc()).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list MCP grants: %w", err)
	}
	out := make([]service.MCPAuthGrant, 0, len(rows))
	for _, row := range rows {
		g := mcpAuthGrantRowToRecord(row.mcpAuthGrantRow)
		g.ServerName = row.ServerName.String
		out = append(out, g)
	}
	return out, nil
}

// RevokeMCPAuthGrant deletes one of the signed-in account's grants, and with
// it every token issued under it.
func (p *Postgres) RevokeMCPAuthGrant(ctx context.Context, id string) error {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return err
	}
	res, err := p.goqu.Delete(p.workspaceTable("mcp_auth_grants")).Where(goqu.Ex{"id": id, "workspace_id": a.WorkspaceID, "user_id": a.UserID}).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("revoke MCP grant: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return service.ErrAccessResourceNotFound
	}
	return nil
}

func (p *Postgres) MCPAuthGrantLive(ctx context.Context, grantID, userID, workspaceID string) (bool, error) {
	n, err := p.goqu.From(p.workspaceTable("mcp_auth_grants")).Where(goqu.Ex{
		"id": grantID, "user_id": userID, "workspace_id": workspaceID, "revoked": false,
	}).CountContext(ctx)
	if err != nil {
		return false, fmt.Errorf("check MCP grant: %w", err)
	}
	return n == 1, nil
}

// GetOAuthMCPRoute resolves a gateway MCP server by name across workspaces
// for the unauthenticated half of the OAuth flow. It returns configuration
// but is never exposed as an API response; callers read only the OAuth
// switch, ID, workspace and name from it.
func (p *Postgres) GetOAuthMCPRoute(ctx context.Context, name string) (*service.MCPServer, error) {
	var rows []mcpServerRow
	if err := p.goqu.From(p.tableMCPServers).Select("id", "workspace_id", "name", "description", "public", "config", "servers", "urls", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.Ex{"name": name}, goqu.L("COALESCE(config->'oauth'->>'enabled','') = 'true'")).Limit(2).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("resolve OAuth MCP route: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if len(rows) != 1 {
		return nil, service.ErrWorkspaceConflict
	}
	return mcpServerRowToRecord(rows[0])
}
