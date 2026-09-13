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
	"github.com/rakunlabs/query"
	"github.com/worldline-go/types"
)

// ─── API Token CRUD ───

func (p *Postgres) ListAPITokens(ctx context.Context, q *query.Query) (*service.ListResult[service.APIToken], error) {
	sql, total, err := p.buildListQuery(ctx, p.tableAPITokens, q, "id", "name", "token_prefix", "allowed_providers_mode", "allowed_providers", "allowed_models_mode", "allowed_models", "allowed_webhooks_mode", "allowed_webhooks", "allowed_mcps_mode", "allowed_mcps", "expires_at", "total_token_limit", "spend_limit_cents", "limit_reset_interval", "last_reset_at", "created_at", "last_used_at", "created_by", "updated_by", "workspace_id")
	if err != nil {
		return nil, fmt.Errorf("build list tokens query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	defer rows.Close()

	var items []service.APIToken
	for rows.Next() {
		var t service.APIToken
		if err := rows.Scan(
			&t.ID, &t.Name, &t.TokenPrefix,
			&t.AllowedProvidersMode, &t.AllowedProviders,
			&t.AllowedModelsMode, &t.AllowedModels,
			&t.AllowedWebhooksMode, &t.AllowedWebhooks,
			&t.AllowedMCPsMode, &t.AllowedMCPs,
			&t.ExpiresAt, &t.TotalTokenLimit, &t.SpendLimitCents, &t.LimitResetInterval, &t.LastResetAt,
			&t.CreatedAt, &t.LastUsedAt, &t.CreatedBy, &t.UpdatedBy,
			&t.WorkspaceID,
		); err != nil {
			return nil, fmt.Errorf("scan api_token row: %w", err)
		}
		items = append(items, t)
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.APIToken]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (p *Postgres) AuthorizeAPITokenManagement(ctx context.Context, id, capability string) error {
	if capability != "tokens.read" && capability != "tokens.write" {
		return service.ErrAccessDenied
	}
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return err
	}
	if !a.Allows(capability, service.AccessResource{WorkspaceID: a.WorkspaceID, ID: id}) {
		return service.ErrAccessDenied
	}
	var owned string
	found, err := p.goqu.From(p.tableAPITokens).Select("id").Where(goqu.Ex{"id": id, "workspace_id": a.WorkspaceID}).ScanValContext(ctx, &owned)
	if err != nil {
		return fmt.Errorf("authorize token management: %w", err)
	}
	if !found {
		return service.ErrAccessResourceNotFound
	}
	return nil
}

func (p *Postgres) GetAPITokenByHash(ctx context.Context, hash string) (*service.APIToken, error) {
	query, _, err := p.goqu.From(p.tableAPITokens).
		Select("id", "name", "token_prefix", "allowed_providers_mode", "allowed_providers", "allowed_models_mode", "allowed_models", "allowed_webhooks_mode", "allowed_webhooks", "allowed_mcps_mode", "allowed_mcps", "expires_at", "total_token_limit", "spend_limit_cents", "limit_reset_interval", "last_reset_at", "created_at", "last_used_at", "created_by", "updated_by", "workspace_id").
		Where(goqu.I("token_hash").Eq(hash)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get api_token query: %w", err)
	}

	var t service.APIToken
	err = p.db.QueryRowContext(ctx, query).Scan(
		&t.ID, &t.Name, &t.TokenPrefix,
		&t.AllowedProvidersMode, &t.AllowedProviders,
		&t.AllowedModelsMode, &t.AllowedModels,
		&t.AllowedWebhooksMode, &t.AllowedWebhooks,
		&t.AllowedMCPsMode, &t.AllowedMCPs,
		&t.ExpiresAt, &t.TotalTokenLimit, &t.SpendLimitCents, &t.LimitResetInterval, &t.LastResetAt,
		&t.CreatedAt, &t.LastUsedAt, &t.CreatedBy, &t.UpdatedBy,
		&t.WorkspaceID,
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
	w, err := p.beginBusinessWrite(ctx, p.tableAPITokens, "tokens.write", "")
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()
	if token.WorkspaceID != "" && token.WorkspaceID != w.actor.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	token.WorkspaceID = w.actor.WorkspaceID
	id := ulid.Make().String()
	now := types.NewTime(time.Now().UTC())

	record := goqu.Record{
		"workspace_id":           token.WorkspaceID,
		"id":                     id,
		"name":                   token.Name,
		"token_hash":             tokenHash,
		"token_prefix":           token.TokenPrefix,
		"allowed_providers_mode": token.AllowedProvidersMode,
		"allowed_providers":      token.AllowedProviders,
		"allowed_models_mode":    token.AllowedModelsMode,
		"allowed_models":         token.AllowedModels,
		"allowed_webhooks_mode":  token.AllowedWebhooksMode,
		"allowed_webhooks":       token.AllowedWebhooks,
		"allowed_mcps_mode":      token.AllowedMCPsMode,
		"allowed_mcps":           token.AllowedMCPs,
		"expires_at":             token.ExpiresAt,
		"total_token_limit":      token.TotalTokenLimit,
		"spend_limit_cents":      token.SpendLimitCents,
		"limit_reset_interval":   token.LimitResetInterval,
		"last_reset_at":          token.LastResetAt,
		"created_at":             now,
		"created_by":             token.CreatedBy,
		"updated_by":             token.UpdatedBy,
	}

	query, _, err := p.goqu.Insert(p.tableAPITokens).Rows(record).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert api_token query: %w", err)
	}

	if _, err := w.tx.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create api_token: %w", err)
	}
	if err := w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit api_token: %w", err)
	}

	token.ID = id
	token.CreatedAt = now
	return &token, nil
}

func (p *Postgres) DeleteAPIToken(ctx context.Context, id string) error {
	w, err := p.beginBusinessWrite(ctx, p.tableAPITokens, "tokens.write", id)
	if err != nil {
		return err
	}
	defer w.tx.Rollback()
	query, _, err := p.goqu.Delete(p.tableAPITokens).
		Where(w.predicate, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete api_token query: %w", err)
	}

	_, err = w.tx.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete api_token %q: %w", id, err)
	}

	if err := w.tx.Commit(); err != nil {
		return fmt.Errorf("commit delete api_token: %w", err)
	}
	return nil
}

func (p *Postgres) UpdateAPIToken(ctx context.Context, id string, token service.APIToken) (*service.APIToken, error) {
	w, err := p.beginBusinessWrite(ctx, p.tableAPITokens, "tokens.write", id)
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()
	if token.WorkspaceID != "" && token.WorkspaceID != w.actor.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	record := goqu.Record{
		"name":                   token.Name,
		"allowed_providers_mode": token.AllowedProvidersMode,
		"allowed_providers":      token.AllowedProviders,
		"allowed_models_mode":    token.AllowedModelsMode,
		"allowed_models":         token.AllowedModels,
		"allowed_webhooks_mode":  token.AllowedWebhooksMode,
		"allowed_webhooks":       token.AllowedWebhooks,
		"allowed_mcps_mode":      token.AllowedMCPsMode,
		"allowed_mcps":           token.AllowedMCPs,
		"expires_at":             token.ExpiresAt,
		"total_token_limit":      token.TotalTokenLimit,
		"spend_limit_cents":      token.SpendLimitCents,
		"limit_reset_interval":   token.LimitResetInterval,
		"updated_by":             token.UpdatedBy,
	}

	query, _, err := p.goqu.Update(p.tableAPITokens).Set(record).
		Where(w.predicate, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update api_token query: %w", err)
	}

	res, err := w.tx.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update api_token %q: %w", id, err)
	}

	rows, _ := res.RowsAffected()
	if rows == 0 {
		return nil, fmt.Errorf("api_token %q not found", id)
	}
	if err := w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit update api_token: %w", err)
	}

	// Re-fetch the updated token.
	fetchQuery, _, err := p.goqu.From(p.tableAPITokens).
		Select("id", "name", "token_prefix", "allowed_providers_mode", "allowed_providers", "allowed_models_mode", "allowed_models", "allowed_webhooks_mode", "allowed_webhooks", "allowed_mcps_mode", "allowed_mcps", "expires_at", "total_token_limit", "spend_limit_cents", "limit_reset_interval", "last_reset_at", "created_at", "last_used_at", "created_by", "updated_by").
		Where(w.predicate, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build fetch api_token query: %w", err)
	}

	var t service.APIToken
	err = p.db.QueryRowContext(ctx, fetchQuery).Scan(
		&t.ID, &t.Name, &t.TokenPrefix,
		&t.AllowedProvidersMode, &t.AllowedProviders,
		&t.AllowedModelsMode, &t.AllowedModels,
		&t.AllowedWebhooksMode, &t.AllowedWebhooks,
		&t.AllowedMCPsMode, &t.AllowedMCPs,
		&t.ExpiresAt, &t.TotalTokenLimit, &t.SpendLimitCents, &t.LimitResetInterval, &t.LastResetAt,
		&t.CreatedAt, &t.LastUsedAt, &t.CreatedBy, &t.UpdatedBy,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch updated api_token %q: %w", id, err)
	}

	t.WorkspaceID = w.actor.WorkspaceID
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
