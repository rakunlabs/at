package postgres

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/doug-martin/goqu/v9"

	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
)

var _ service.MCPOAuthPendingStorer = (*Postgres)(nil)

func validMCPOAuthStateHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}

func (p *Postgres) SaveMCPOAuthPending(ctx context.Context, hash string, payload json.RawMessage) error {
	if !validMCPOAuthStateHash(hash) || len(payload) == 0 || len(payload) > 128<<10 || !json.Valid(payload) {
		return fmt.Errorf("invalid MCP OAuth pending state")
	}
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	// Unlike public configuration, a PKCE verifier must never land in plaintext.
	if len(p.encKey) == 0 {
		return fmt.Errorf("MCP OAuth requires an installation encryption key")
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin MCP OAuth state save: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "mcp.use")
	if err != nil {
		return err
	}
	if a.UserID == "" || a.SessionID == "" {
		return service.ErrAccessDenied
	}
	table := p.workspaceTable("mcp_oauth_pending")
	// Serialize starts by account, including starts arriving through replicas.
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", fmt.Sprintf("%v:%s:%s", table, a.WorkspaceID, a.UserID)); err != nil {
		return fmt.Errorf("lock MCP OAuth pending quota: %w", err)
	}
	_, err = tx.Delete(table).Where(goqu.C("expires_at").Lte(goqu.L("clock_timestamp()"))).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("expire MCP OAuth states: %w", err)
	}
	count, err := tx.From(table).Where(goqu.Ex{"workspace_id": a.WorkspaceID, "user_id": a.UserID}).CountContext(ctx)
	if err != nil {
		return fmt.Errorf("count MCP OAuth states: %w", err)
	}
	if count >= 8 {
		return fmt.Errorf("too many pending MCP OAuth authorizations; wait ten minutes")
	}
	raw, err := atcrypto.Encrypt(string(payload), p.encKey)
	if err != nil {
		return fmt.Errorf("encrypt MCP OAuth state: %w", err)
	}
	_, err = tx.Insert(table).Rows(goqu.Record{
		"state_hash": hash, "workspace_id": a.WorkspaceID, "user_id": a.UserID,
		"session_id": a.SessionID, "payload": raw,
		"expires_at": goqu.L("clock_timestamp() + interval '10 minutes'"),
	}).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("save MCP OAuth state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit MCP OAuth state: %w", err)
	}
	return nil
}

func (p *Postgres) TakeMCPOAuthPending(ctx context.Context, hash string) (json.RawMessage, error) {
	if !validMCPOAuthStateHash(hash) {
		return nil, nil
	}
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin MCP OAuth state consume: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "mcp.use")
	if err != nil {
		return nil, err
	}
	if a.UserID == "" || a.SessionID == "" {
		return nil, service.ErrAccessDenied
	}
	var row struct {
		Payload string `db:"payload"`
	}
	found, err := tx.Delete(p.workspaceTable("mcp_oauth_pending")).Where(
		goqu.Ex{"state_hash": hash, "workspace_id": a.WorkspaceID, "user_id": a.UserID, "session_id": a.SessionID},
		goqu.C("expires_at").Gt(goqu.L("clock_timestamp()")),
	).Returning("payload").Executor().ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("consume MCP OAuth state: %w", err)
	}
	if !found {
		return nil, nil
	}
	raw, err := atcrypto.Decrypt(row.Payload, p.encKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt MCP OAuth state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit MCP OAuth state consume: %w", err)
	}
	return json.RawMessage(raw), nil
}

func (p *Postgres) rotateMCPOAuthPendingKey(ctx context.Context, tx *sql.Tx, oldKey, newKey []byte) error {
	table := p.workspaceTable("mcp_oauth_pending")
	if len(newKey) == 0 {
		// Disabling encryption invalidates pending ceremonies rather than exposing
		// their verifiers. Completed credentials retain their existing policy.
		q, _, err := p.goqu.Delete(table).ToSQL()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, q)
		return err
	}
	q, _, err := p.goqu.From(table).Select("state_hash", "payload").ForUpdate(goqu.Wait).ToSQL()
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, q)
	if err != nil {
		return err
	}
	values := map[string]string{}
	for rows.Next() {
		var hash, raw string
		if err := rows.Scan(&hash, &raw); err != nil {
			rows.Close()
			return err
		}
		values[hash] = raw
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for hash, raw := range values {
		raw, err := atcrypto.Decrypt(raw, oldKey)
		if err != nil {
			return err
		}
		raw, err = atcrypto.Encrypt(raw, newKey)
		if err != nil {
			return err
		}
		q, _, err := p.goqu.Update(table).Set(goqu.Record{"payload": raw}).Where(goqu.Ex{"state_hash": hash}).ToSQL()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}
