package postgres

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

func (p *Postgres) ListAPITokens(ctx context.Context) ([]service.APIToken, error) {
	query, _, err := p.goqu.From(p.tableAPITokens).
		Select("id", "name", "token_prefix", "allowed_providers", "allowed_models", "expires_at", "created_at", "last_used_at").
		Order(goqu.I("created_at").Desc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list tokens query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	defer rows.Close()

	var result []service.APIToken
	for rows.Next() {
		var t service.APIToken
		if err := rows.Scan(
			&t.ID, &t.Name, &t.TokenPrefix,
			&t.AllowedProviders, &t.AllowedModels,
			&t.ExpiresAt, &t.CreatedAt, &t.LastUsedAt,
		); err != nil {
			return nil, fmt.Errorf("scan api_token row: %w", err)
		}
		result = append(result, t)
	}

	return result, rows.Err()
}

func (p *Postgres) GetAPITokenByHash(ctx context.Context, hash string) (*service.APIToken, error) {
	query, _, err := p.goqu.From(p.tableAPITokens).
		Select("id", "name", "token_prefix", "allowed_providers", "allowed_models", "expires_at", "created_at", "last_used_at").
		Where(goqu.I("token_hash").Eq(hash)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get api_token query: %w", err)
	}

	var t service.APIToken
	err = p.db.QueryRowContext(ctx, query).Scan(
		&t.ID, &t.Name, &t.TokenPrefix,
		&t.AllowedProviders, &t.AllowedModels,
		&t.ExpiresAt, &t.CreatedAt, &t.LastUsedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get api_token by hash: %w", err)
	}

	return &t, nil
}

func (p *Postgres) CreateAPIToken(ctx context.Context, token service.APIToken, tokenHash string) (*service.APIToken, error) {
	id := ulid.Make().String()
	now := types.NewTime(time.Now().UTC())

	record := goqu.Record{
		"id":                id,
		"name":              token.Name,
		"token_hash":        tokenHash,
		"token_prefix":      token.TokenPrefix,
		"allowed_providers": token.AllowedProviders,
		"allowed_models":    token.AllowedModels,
		"expires_at":        token.ExpiresAt,
		"created_at":        now,
	}

	query, _, err := p.goqu.Insert(p.tableAPITokens).Rows(record).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert api_token query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create api_token: %w", err)
	}

	token.ID = id
	token.CreatedAt = now
	return &token, nil
}

func (p *Postgres) DeleteAPIToken(ctx context.Context, id string) error {
	query, _, err := p.goqu.Delete(p.tableAPITokens).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete api_token query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete api_token %q: %w", id, err)
	}

	return nil
}

func (p *Postgres) UpdateAPIToken(ctx context.Context, id string, token service.APIToken) (*service.APIToken, error) {
	record := goqu.Record{
		"name":              token.Name,
		"allowed_providers": token.AllowedProviders,
		"allowed_models":    token.AllowedModels,
		"expires_at":        token.ExpiresAt,
	}

	query, _, err := p.goqu.Update(p.tableAPITokens).Set(record).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update api_token query: %w", err)
	}

	res, err := p.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update api_token %q: %w", id, err)
	}

	rows, _ := res.RowsAffected()
	if rows == 0 {
		return nil, fmt.Errorf("api_token %q not found", id)
	}

	// Re-fetch the updated token.
	fetchQuery, _, err := p.goqu.From(p.tableAPITokens).
		Select("id", "name", "token_prefix", "allowed_providers", "allowed_models", "expires_at", "created_at", "last_used_at").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build fetch api_token query: %w", err)
	}

	var t service.APIToken
	err = p.db.QueryRowContext(ctx, fetchQuery).Scan(
		&t.ID, &t.Name, &t.TokenPrefix,
		&t.AllowedProviders, &t.AllowedModels,
		&t.ExpiresAt, &t.CreatedAt, &t.LastUsedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch updated api_token %q: %w", id, err)
	}

	return &t, nil
}

func (p *Postgres) UpdateLastUsed(ctx context.Context, id string) error {
	now := time.Now().UTC()

	query, _, err := p.goqu.Update(p.tableAPITokens).Set(
		goqu.Record{"last_used_at": now},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return fmt.Errorf("build update last_used query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("update last_used for %q: %w", id, err)
	}

	return nil
}
