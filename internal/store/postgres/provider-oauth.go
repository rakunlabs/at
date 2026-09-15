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

var _ service.ClaudeOAuthTokenStorer = (*Postgres)(nil)

// WithClaudeOAuthTokens holds the provider row lock across reload, change and
// persistence. Anthropic invalidates the previous refresh credential on every
// exchange, so the exchange must observe the currently stored credential rather
// than a per-source in-memory copy.
func (p *Postgres) WithClaudeOAuthTokens(ctx context.Context, workspace, key string, change func(*service.ClaudeOAuthTokens) error) (*service.ClaudeOAuthTokens, error) {
	if workspace == "" || key == "" || change == nil {
		return nil, service.ErrAccessDenied
	}
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin OAuth rotation: %w", err)
	}
	defer tx.Rollback()
	var row providerRow
	found, err := tx.From(p.tableProviders).Where(goqu.Ex{"workspace_id": workspace, "key": key}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("load OAuth provider: %w", err)
	}
	if !found {
		return nil, service.ErrAccessDenied
	}
	record, err := rowToRecord(row, p.encKey)
	if err != nil {
		return nil, err
	}
	if record.Config.AuthType != "claude-code" {
		return nil, service.ErrAccessDenied
	}
	expires, _ := time.Parse(time.RFC3339, record.Config.TokenExpiresAt)
	tokens := &service.ClaudeOAuthTokens{AccessToken: record.Config.APIKey, RefreshToken: record.Config.RefreshToken, ExpiresAt: expires}
	before := *tokens
	if err := change(tokens); err != nil {
		return nil, err
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		return nil, service.ErrAccessDenied
	}
	if tokens.AccessToken == before.AccessToken && tokens.RefreshToken == before.RefreshToken && tokens.ExpiresAt.Equal(before.ExpiresAt) {
		return tokens, nil
	}
	tokens.PreviousRefreshToken = before.RefreshToken
	record.Config.APIKey, record.Config.RefreshToken = tokens.AccessToken, tokens.RefreshToken
	if !tokens.ExpiresAt.IsZero() {
		record.Config.TokenExpiresAt = tokens.ExpiresAt.UTC().Format(time.RFC3339)
	}
	cfg, err := atcrypto.EncryptLLMConfig(record.Config, p.encKey)
	if err != nil {
		return tokens, fmt.Errorf("encrypt rotated OAuth credentials: %w", err)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return tokens, fmt.Errorf("encode rotated OAuth credentials: %w", err)
	}
	if _, err := tx.Update(p.tableProviders).Set(goqu.Record{"config": types.RawJSON(data), "updated_at": time.Now().UTC(), "updated_by": "system:oauth-refresh"}).Where(goqu.Ex{"id": row.ID, "workspace_id": workspace}).Executor().ExecContext(ctx); err != nil {
		return tokens, fmt.Errorf("save rotated OAuth credentials: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return tokens, fmt.Errorf("commit OAuth rotation: %w", err)
	}
	return tokens, nil
}

func (p *Postgres) RotateClaudeOAuthTokens(ctx context.Context, workspace, key, previousRefresh, access, refresh string, expiry time.Time) error {
	if previousRefresh == "" || access == "" || refresh == "" {
		return service.ErrAccessDenied
	}
	_, err := p.WithClaudeOAuthTokens(ctx, workspace, key, func(current *service.ClaudeOAuthTokens) error {
		// An acknowledged commit may have lost its response. Retrying the exact
		// rotation is harmless, but an old process must not overwrite newer login data.
		if current.AccessToken == access && current.RefreshToken == refresh {
			return nil
		}
		if subtle.ConstantTimeCompare([]byte(current.RefreshToken), []byte(previousRefresh)) != 1 {
			return service.ErrAccessDenied
		}
		current.AccessToken, current.RefreshToken = access, refresh
		if !expiry.IsZero() {
			current.ExpiresAt = expiry
		}
		return nil
	})
	return err
}
