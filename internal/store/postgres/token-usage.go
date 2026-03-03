package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/rakunlabs/at/internal/service"
	"github.com/worldline-go/types"
)

// ─── Token Usage CRUD ───

func (p *Postgres) RecordUsage(ctx context.Context, tokenID, model string, usage service.Usage) error {
	now := time.Now().UTC()

	// Postgres INSERT ... ON CONFLICT DO UPDATE (upsert).
	query := fmt.Sprintf(
		`INSERT INTO %s (token_id, model, prompt_tokens, completion_tokens, total_tokens, request_count, last_request_at)
		 VALUES ($1, $2, $3, $4, $5, 1, $6)
		 ON CONFLICT(token_id, model) DO UPDATE SET
		     prompt_tokens = %[1]s.prompt_tokens + EXCLUDED.prompt_tokens,
		     completion_tokens = %[1]s.completion_tokens + EXCLUDED.completion_tokens,
		     total_tokens = %[1]s.total_tokens + EXCLUDED.total_tokens,
		     request_count = %[1]s.request_count + 1,
		     last_request_at = EXCLUDED.last_request_at`,
		p.tableTokenUsage.GetTable(),
	)

	_, err := p.db.ExecContext(ctx, query,
		tokenID, model,
		int64(usage.PromptTokens), int64(usage.CompletionTokens), int64(usage.TotalTokens),
		now,
	)
	if err != nil {
		return fmt.Errorf("record usage for token %q model %q: %w", tokenID, model, err)
	}

	return nil
}

func (p *Postgres) GetTokenUsage(ctx context.Context, tokenID string) ([]service.TokenUsage, error) {
	query, _, err := p.goqu.From(p.tableTokenUsage).
		Select("token_id", "model", "prompt_tokens", "completion_tokens", "total_tokens", "request_count", "last_request_at").
		Where(goqu.I("token_id").Eq(tokenID)).
		Order(goqu.I("total_tokens").Desc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get token usage query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("get token usage: %w", err)
	}
	defer rows.Close()

	var items []service.TokenUsage
	for rows.Next() {
		var u service.TokenUsage
		if err := rows.Scan(
			&u.TokenID, &u.Model,
			&u.PromptTokens, &u.CompletionTokens, &u.TotalTokens,
			&u.RequestCount, &u.LastRequestAt,
		); err != nil {
			return nil, fmt.Errorf("scan token usage row: %w", err)
		}
		items = append(items, u)
	}

	return items, rows.Err()
}

func (p *Postgres) GetTokenTotalUsage(ctx context.Context, tokenID string) (int64, error) {
	query, _, err := p.goqu.From(p.tableTokenUsage).
		Select(goqu.L("COALESCE(SUM(total_tokens), 0)")).
		Where(goqu.I("token_id").Eq(tokenID)).
		ToSQL()
	if err != nil {
		return 0, fmt.Errorf("build get token total usage query: %w", err)
	}

	var total int64
	if err := p.db.QueryRowContext(ctx, query).Scan(&total); err != nil {
		return 0, fmt.Errorf("get token total usage: %w", err)
	}

	return total, nil
}

func (p *Postgres) ResetTokenUsage(ctx context.Context, tokenID string) error {
	// Delete all usage rows for the token.
	deleteQuery, _, err := p.goqu.Delete(p.tableTokenUsage).
		Where(goqu.I("token_id").Eq(tokenID)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete token usage query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, deleteQuery); err != nil {
		return fmt.Errorf("delete token usage for %q: %w", tokenID, err)
	}

	// Update last_reset_at on the token.
	now := types.NewTime(time.Now().UTC())
	updateQuery, _, err := p.goqu.Update(p.tableAPITokens).Set(
		goqu.Record{"last_reset_at": now},
	).Where(goqu.I("id").Eq(tokenID)).ToSQL()
	if err != nil {
		return fmt.Errorf("build update last_reset_at query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, updateQuery); err != nil {
		return fmt.Errorf("update last_reset_at for %q: %w", tokenID, err)
	}

	return nil
}
