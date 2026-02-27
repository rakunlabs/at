package sqlite3

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/worldline-go/types"
)

// ─── API Token CRUD ───

func (s *SQLite) ListAPITokens(ctx context.Context) ([]service.APIToken, error) {
	query, _, err := s.goqu.From(s.tableAPITokens).
		Select("id", "name", "token_prefix", "allowed_providers", "allowed_models", "allowed_webhooks", "expires_at", "created_at", "last_used_at", "created_by", "updated_by").
		Order(goqu.I("created_at").Desc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list tokens query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	defer rows.Close()

	var result []service.APIToken
	for rows.Next() {
		var t service.APIToken
		if err := rows.Scan(
			&t.ID, &t.Name, &t.TokenPrefix,
			&t.AllowedProviders, &t.AllowedModels, &t.AllowedWebhooks,
			&t.ExpiresAt, &t.CreatedAt, &t.LastUsedAt, &t.CreatedBy, &t.UpdatedBy,
		); err != nil {
			return nil, fmt.Errorf("scan api_token row: %w", err)
		}
		result = append(result, t)
	}

	return result, rows.Err()
}

func (s *SQLite) GetAPITokenByHash(ctx context.Context, hash string) (*service.APIToken, error) {
	query, _, err := s.goqu.From(s.tableAPITokens).
		Select("id", "name", "token_prefix", "allowed_providers", "allowed_models", "allowed_webhooks", "expires_at", "created_at", "last_used_at", "created_by", "updated_by").
		Where(goqu.I("token_hash").Eq(hash)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get api_token query: %w", err)
	}

	var t service.APIToken
	err = s.db.QueryRowContext(ctx, query).Scan(
		&t.ID, &t.Name, &t.TokenPrefix,
		&t.AllowedProviders, &t.AllowedModels, &t.AllowedWebhooks,
		&t.ExpiresAt, &t.CreatedAt, &t.LastUsedAt, &t.CreatedBy, &t.UpdatedBy,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get api_token by hash: %w", err)
	}

	return &t, nil
}

func (s *SQLite) CreateAPIToken(ctx context.Context, token service.APIToken, tokenHash string) (*service.APIToken, error) {
	id := ulid.Make().String()
	now := types.NewTime(time.Now().UTC())

	record := goqu.Record{
		"id":                id,
		"name":              token.Name,
		"token_hash":        tokenHash,
		"token_prefix":      token.TokenPrefix,
		"allowed_providers": token.AllowedProviders,
		"allowed_models":    token.AllowedModels,
		"allowed_webhooks":  token.AllowedWebhooks,
		"expires_at":        token.ExpiresAt,
		"created_at":        now,
		"created_by":        token.CreatedBy,
		"updated_by":        token.UpdatedBy,
	}

	query, _, err := s.goqu.Insert(s.tableAPITokens).Rows(record).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert api_token query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create api_token: %w", err)
	}

	token.ID = id
	token.CreatedAt = now
	return &token, nil
}

func (s *SQLite) DeleteAPIToken(ctx context.Context, id string) error {
	query, _, err := s.goqu.Delete(s.tableAPITokens).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete api_token query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete api_token %q: %w", id, err)
	}

	return nil
}

func (s *SQLite) UpdateAPIToken(ctx context.Context, id string, token service.APIToken) (*service.APIToken, error) {
	record := goqu.Record{
		"name":              token.Name,
		"allowed_providers": token.AllowedProviders,
		"allowed_models":    token.AllowedModels,
		"allowed_webhooks":  token.AllowedWebhooks,
		"expires_at":        token.ExpiresAt,
		"updated_by":        token.UpdatedBy,
	}

	query, _, err := s.goqu.Update(s.tableAPITokens).Set(record).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update api_token query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update api_token %q: %w", id, err)
	}

	rows, _ := res.RowsAffected()
	if rows == 0 {
		return nil, fmt.Errorf("api_token %q not found", id)
	}

	// Re-fetch the updated token.
	fetchQuery, _, err := s.goqu.From(s.tableAPITokens).
		Select("id", "name", "token_prefix", "allowed_providers", "allowed_models", "allowed_webhooks", "expires_at", "created_at", "last_used_at", "created_by", "updated_by").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build fetch api_token query: %w", err)
	}

	var t service.APIToken
	err = s.db.QueryRowContext(ctx, fetchQuery).Scan(
		&t.ID, &t.Name, &t.TokenPrefix,
		&t.AllowedProviders, &t.AllowedModels, &t.AllowedWebhooks,
		&t.ExpiresAt, &t.CreatedAt, &t.LastUsedAt, &t.CreatedBy, &t.UpdatedBy,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch updated api_token %q: %w", id, err)
	}

	return &t, nil
}

func (s *SQLite) UpdateLastUsed(ctx context.Context, id string) error {
	now := time.Now().UTC()

	query, _, err := s.goqu.Update(s.tableAPITokens).Set(
		goqu.Record{"last_used_at": now.Format(time.RFC3339)},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return fmt.Errorf("build update last_used query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("update last_used for %q: %w", id, err)
	}

	return nil
}
