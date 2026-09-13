package postgres

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/worldline-go/types"

	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
)

func (p *Postgres) RotateClaudeOAuthTokens(ctx context.Context, workspace, key, previousRefresh, access, refresh string, expiry time.Time) error {
	if workspace == "" || key == "" || previousRefresh == "" || access == "" || refresh == "" {
		return service.ErrAccessDenied
	}
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin OAuth rotation: %w", err)
	}
	defer tx.Rollback()
	var row providerRow
	found, err := tx.From(p.tableProviders).Where(goqu.Ex{"workspace_id": workspace, "key": key}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &row)
	if err != nil {
		return fmt.Errorf("load OAuth provider: %w", err)
	}
	if !found {
		return service.ErrAccessDenied
	}
	record, err := rowToRecord(row, p.encKey)
	if err != nil {
		return err
	}
	if record.Config.AuthType != "claude-code" {
		return service.ErrAccessDenied
	}
	// An acknowledged commit may have lost its response. Retrying the exact
	// rotation is harmless, but an old process must not overwrite newer login data.
	if record.Config.RefreshToken == refresh && record.Config.APIKey == access {
		return nil
	}
	if subtle.ConstantTimeCompare([]byte(record.Config.RefreshToken), []byte(previousRefresh)) != 1 {
		return service.ErrAccessDenied
	}
	record.Config.APIKey, record.Config.RefreshToken = access, refresh
	if !expiry.IsZero() {
		record.Config.TokenExpiresAt = expiry.UTC().Format(time.RFC3339)
	}
	cfg, err := atcrypto.EncryptLLMConfig(record.Config, p.encKey)
	if err != nil {
		return fmt.Errorf("encrypt rotated OAuth credentials: %w", err)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode rotated OAuth credentials: %w", err)
	}
	if _, err := tx.Update(p.tableProviders).Set(goqu.Record{"config": types.RawJSON(data), "updated_at": time.Now().UTC(), "updated_by": "system:oauth-refresh"}).Where(goqu.Ex{"id": row.ID, "workspace_id": workspace}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("save rotated OAuth credentials: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit OAuth rotation: %w", err)
	}
	return nil
}
