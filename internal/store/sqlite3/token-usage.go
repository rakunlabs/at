package sqlite3

import (
	"context"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/rakunlabs/at/internal/service"
	"github.com/worldline-go/types"
)

// ─── Token Usage CRUD ───

func (s *SQLite) RecordUsage(ctx context.Context, tokenID, model string, usage service.Usage) error {
	now := time.Now().UTC().Format(time.RFC3339)

	// SQLite INSERT ... ON CONFLICT DO UPDATE (upsert).
	query := fmt.Sprintf(
		`INSERT INTO %s (token_id, model, prompt_tokens, completion_tokens, total_tokens, request_count, last_request_at)
		 VALUES (?, ?, ?, ?, ?, 1, ?)
		 ON CONFLICT(token_id, model) DO UPDATE SET
		     prompt_tokens = prompt_tokens + excluded.prompt_tokens,
		     completion_tokens = completion_tokens + excluded.completion_tokens,
		     total_tokens = total_tokens + excluded.total_tokens,
		     request_count = request_count + 1,
		     last_request_at = excluded.last_request_at`,
		s.tableTokenUsage.GetTable(),
	)

	_, err := s.db.ExecContext(ctx, query,
		tokenID, model,
		int64(usage.PromptTokens), int64(usage.CompletionTokens), int64(usage.TotalTokens),
		now,
	)
	if err != nil {
		return fmt.Errorf("record usage for token %q model %q: %w", tokenID, model, err)
	}

	return nil
}

func (s *SQLite) GetTokenUsage(ctx context.Context, tokenID string) ([]service.TokenUsage, error) {
	query, _, err := s.goqu.From(s.tableTokenUsage).
		Select("token_id", "model", "prompt_tokens", "completion_tokens", "total_tokens", "request_count", "last_request_at").
		Where(goqu.I("token_id").Eq(tokenID)).
		Order(goqu.I("total_tokens").Desc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get token usage query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
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

func (s *SQLite) GetTokenTotalUsage(ctx context.Context, tokenID string) (int64, error) {
	query, _, err := s.goqu.From(s.tableTokenUsage).
		Select(goqu.L("COALESCE(SUM(total_tokens), 0)")).
		Where(goqu.I("token_id").Eq(tokenID)).
		ToSQL()
	if err != nil {
		return 0, fmt.Errorf("build get token total usage query: %w", err)
	}

	var total int64
	if err := s.db.QueryRowContext(ctx, query).Scan(&total); err != nil {
		return 0, fmt.Errorf("get token total usage: %w", err)
	}

	return total, nil
}

func (s *SQLite) ResetTokenUsage(ctx context.Context, tokenID string) error {
	// Delete all usage rows for the token.
	deleteQuery, _, err := s.goqu.Delete(s.tableTokenUsage).
		Where(goqu.I("token_id").Eq(tokenID)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete token usage query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, deleteQuery); err != nil {
		return fmt.Errorf("delete token usage for %q: %w", tokenID, err)
	}

	// Update last_reset_at on the token.
	now := types.NewTime(time.Now().UTC())
	updateQuery, _, err := s.goqu.Update(s.tableAPITokens).Set(
		goqu.Record{"last_reset_at": now},
	).Where(goqu.I("id").Eq(tokenID)).ToSQL()
	if err != nil {
		return fmt.Errorf("build update last_reset_at query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, updateQuery); err != nil {
		return fmt.Errorf("update last_reset_at for %q: %w", tokenID, err)
	}

	return nil
}
