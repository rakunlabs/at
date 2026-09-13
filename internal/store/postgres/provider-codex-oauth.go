package postgres

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"maps"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/worldline-go/types"

	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
)

var _ service.CodexOAuthTokenStorer = (*Postgres)(nil)

func (p *Postgres) WithCodexOAuthTokens(ctx context.Context, workspace, key string, change func(*service.CodexOAuthTokens) error) (*service.CodexOAuthTokens, error) {
	if workspace == "" || key == "" || change == nil {
		return nil, service.ErrAccessDenied
	}
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin Codex credential rotation: %w", err)
	}
	defer tx.Rollback()
	var row providerRow
	found, err := tx.From(p.tableProviders).Where(goqu.Ex{"workspace_id": workspace, "key": key}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("lock Codex provider: %w", err)
	}
	if !found {
		return nil, service.ErrAccessDenied
	}
	record, err := rowToRecord(row, p.encKey)
	if err != nil {
		return nil, err
	}
	if record.Config.AuthType != "chatgpt" {
		return nil, service.ErrAccessDenied
	}
	expires, _ := time.Parse(time.RFC3339, record.Config.TokenExpiresAt)
	tokens := &service.CodexOAuthTokens{AccessToken: record.Config.APIKey, RefreshToken: record.Config.RefreshToken, AccountID: record.Config.ExtraHeaders["ChatGPT-Account-ID"], ExpiresAt: expires}
	before := *tokens
	if err := change(tokens); err != nil {
		return nil, err
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" || (before.AccountID != "" && tokens.AccountID != before.AccountID) {
		return nil, service.ErrAccessDenied
	}
	if tokens.AccessToken == before.AccessToken && tokens.RefreshToken == before.RefreshToken && tokens.AccountID == before.AccountID && tokens.ExpiresAt.Equal(before.ExpiresAt) {
		return tokens, nil
	}
	tokens.PreviousRefreshToken = before.RefreshToken
	record.Config.APIKey, record.Config.RefreshToken = tokens.AccessToken, tokens.RefreshToken
	if !tokens.ExpiresAt.IsZero() {
		record.Config.TokenExpiresAt = tokens.ExpiresAt.UTC().Format(time.RFC3339)
	}
	record.Config.ExtraHeaders = maps.Clone(record.Config.ExtraHeaders)
	if record.Config.ExtraHeaders == nil {
		record.Config.ExtraHeaders = map[string]string{}
	}
	record.Config.ExtraHeaders["ChatGPT-Account-ID"] = tokens.AccountID
	cfg, err := atcrypto.EncryptLLMConfig(record.Config, p.encKey)
	if err != nil {
		return tokens, fmt.Errorf("encrypt Codex rotation: %w", err)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return tokens, fmt.Errorf("encode Codex rotation: %w", err)
	}
	if _, err := tx.Update(p.tableProviders).Set(goqu.Record{"config": types.RawJSON(data), "updated_at": time.Now().UTC(), "updated_by": "system:oauth-refresh"}).Where(goqu.Ex{"id": row.ID, "workspace_id": workspace}).Executor().ExecContext(ctx); err != nil {
		return tokens, fmt.Errorf("persist Codex rotation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return tokens, fmt.Errorf("commit Codex rotation: %w", err)
	}
	return tokens, nil
}

func (p *Postgres) RotateCodexOAuthTokens(ctx context.Context, workspace, key, previous string, next service.CodexOAuthTokens) error {
	if previous == "" {
		return service.ErrAccessDenied
	}
	_, err := p.WithCodexOAuthTokens(ctx, workspace, key, func(current *service.CodexOAuthTokens) error {
		if current.AccessToken == next.AccessToken && current.RefreshToken == next.RefreshToken {
			return nil
		}
		if subtle.ConstantTimeCompare([]byte(current.RefreshToken), []byte(previous)) != 1 {
			return service.ErrAccessDenied
		}
		*current = next
		return nil
	})
	return err
}
