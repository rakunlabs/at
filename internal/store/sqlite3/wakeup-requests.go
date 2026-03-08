package sqlite3

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

type wakeupRequestRow struct {
	ID             string         `db:"id"`
	AgentID        string         `db:"agent_id"`
	Status         string         `db:"status"`
	IdempotencyKey sql.NullString `db:"idempotency_key"`
	Context        sql.NullString `db:"context"`
	CoalescedCount int            `db:"coalesced_count"`
	RunID          sql.NullString `db:"run_id"`
	CreatedAt      string         `db:"created_at"`
	UpdatedAt      string         `db:"updated_at"`
}

var wakeupRequestColumns = []interface{}{"id", "agent_id", "status", "idempotency_key", "context", "coalesced_count", "run_id", "created_at", "updated_at"}

func scanWakeupRequestRow(scanner interface{ Scan(dest ...any) error }) (wakeupRequestRow, error) {
	var row wakeupRequestRow
	err := scanner.Scan(&row.ID, &row.AgentID, &row.Status, &row.IdempotencyKey, &row.Context, &row.CoalescedCount, &row.RunID, &row.CreatedAt, &row.UpdatedAt)

	return row, err
}

func (s *SQLite) CreateOrCoalesce(ctx context.Context, req service.WakeupRequest) (*service.WakeupRequest, error) {
	now := time.Now().UTC()

	// 1. If idempotency key is provided, check for existing request with same key.
	if req.IdempotencyKey != "" {
		existing, err := s.getWakeupByIdempotencyKey(ctx, req.IdempotencyKey)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return existing, nil
		}
	}

	// 2. Check for existing pending request for this agent — coalesce if found.
	pending, err := s.getFirstPendingWakeup(ctx, req.AgentID)
	if err != nil {
		return nil, err
	}

	if pending != nil {
		// Merge context into existing pending request.
		mergedCtx := pending.Context
		if mergedCtx == nil {
			mergedCtx = make(map[string]any)
		}
		for k, v := range req.Context {
			mergedCtx[k] = v
		}

		ctxStr := marshalJSONField(mergedCtx)

		query, _, err := s.goqu.Update(s.tableWakeupRequests).Set(
			goqu.Record{
				"context":         ctxStr,
				"coalesced_count": pending.CoalescedCount + 1,
				"updated_at":      now.Format(time.RFC3339),
			},
		).Where(goqu.I("id").Eq(pending.ID)).ToSQL()
		if err != nil {
			return nil, fmt.Errorf("build coalesce wakeup query: %w", err)
		}

		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return nil, fmt.Errorf("coalesce wakeup for agent %q: %w", req.AgentID, err)
		}

		return s.GetWakeupRequest(ctx, pending.ID)
	}

	// 3. No existing pending — create new wakeup request.
	id := ulid.Make().String()
	ctxStr := marshalJSONField(req.Context)

	query, _, err := s.goqu.Insert(s.tableWakeupRequests).Rows(
		goqu.Record{
			"id":              id,
			"agent_id":        req.AgentID,
			"status":          service.WakeupStatusPending,
			"idempotency_key": req.IdempotencyKey,
			"context":         ctxStr,
			"coalesced_count": 1,
			"run_id":          "",
			"created_at":      now.Format(time.RFC3339),
			"updated_at":      now.Format(time.RFC3339),
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert wakeup request query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create wakeup request for agent %q: %w", req.AgentID, err)
	}

	return &service.WakeupRequest{
		ID:             id,
		AgentID:        req.AgentID,
		Status:         service.WakeupStatusPending,
		IdempotencyKey: req.IdempotencyKey,
		Context:        req.Context,
		CoalescedCount: 1,
		CreatedAt:      now.Format(time.RFC3339),
		UpdatedAt:      now.Format(time.RFC3339),
	}, nil
}

func (s *SQLite) GetWakeupRequest(ctx context.Context, id string) (*service.WakeupRequest, error) {
	query, _, err := s.goqu.From(s.tableWakeupRequests).
		Select(wakeupRequestColumns...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get wakeup request query: %w", err)
	}

	row, err := scanWakeupRequestRow(s.db.QueryRowContext(ctx, query))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get wakeup request %q: %w", id, err)
	}

	return wakeupRequestRowToRecord(row)
}

func (s *SQLite) ListPendingForAgent(ctx context.Context, agentID string) ([]service.WakeupRequest, error) {
	query, _, err := s.goqu.From(s.tableWakeupRequests).
		Select(wakeupRequestColumns...).
		Where(
			goqu.I("agent_id").Eq(agentID),
			goqu.I("status").Eq(service.WakeupStatusPending),
		).
		Order(goqu.I("created_at").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list pending wakeups query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list pending wakeups for agent %q: %w", agentID, err)
	}
	defer rows.Close()

	var items []service.WakeupRequest
	for rows.Next() {
		row, err := scanWakeupRequestRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan wakeup request row: %w", err)
		}

		rec, err := wakeupRequestRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *rec)
	}

	return items, rows.Err()
}

func (s *SQLite) MarkDispatched(ctx context.Context, id, runID string) error {
	now := time.Now().UTC()

	query, _, err := s.goqu.Update(s.tableWakeupRequests).Set(
		goqu.Record{
			"status":     service.WakeupStatusDispatched,
			"run_id":     runID,
			"updated_at": now.Format(time.RFC3339),
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return fmt.Errorf("build mark dispatched query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("mark wakeup %q dispatched: %w", id, err)
	}

	return nil
}

func (s *SQLite) PromoteDeferred(ctx context.Context, agentID string) error {
	now := time.Now().UTC()

	// Find the oldest deferred wakeup for this agent.
	selectQuery, _, err := s.goqu.From(s.tableWakeupRequests).
		Select("id").
		Where(
			goqu.I("agent_id").Eq(agentID),
			goqu.I("status").Eq(service.WakeupStatusDeferredIssueExecution),
		).
		Order(goqu.I("created_at").Asc()).
		Limit(1).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build select deferred wakeup query: %w", err)
	}

	var deferredID string
	err = s.db.QueryRowContext(ctx, selectQuery).Scan(&deferredID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // nothing to promote
	}
	if err != nil {
		return fmt.Errorf("find deferred wakeup for agent %q: %w", agentID, err)
	}

	updateQuery, _, err := s.goqu.Update(s.tableWakeupRequests).Set(
		goqu.Record{
			"status":     service.WakeupStatusPending,
			"updated_at": now.Format(time.RFC3339),
		},
	).Where(goqu.I("id").Eq(deferredID)).ToSQL()
	if err != nil {
		return fmt.Errorf("build promote deferred query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, updateQuery)
	if err != nil {
		return fmt.Errorf("promote deferred wakeup %q: %w", deferredID, err)
	}

	return nil
}

// ─── Internal helpers ───

func (s *SQLite) getWakeupByIdempotencyKey(ctx context.Context, key string) (*service.WakeupRequest, error) {
	query, _, err := s.goqu.From(s.tableWakeupRequests).
		Select(wakeupRequestColumns...).
		Where(goqu.I("idempotency_key").Eq(key)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get wakeup by idempotency key query: %w", err)
	}

	row, err := scanWakeupRequestRow(s.db.QueryRowContext(ctx, query))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get wakeup by idempotency key %q: %w", key, err)
	}

	return wakeupRequestRowToRecord(row)
}

func (s *SQLite) getFirstPendingWakeup(ctx context.Context, agentID string) (*service.WakeupRequest, error) {
	query, _, err := s.goqu.From(s.tableWakeupRequests).
		Select(wakeupRequestColumns...).
		Where(
			goqu.I("agent_id").Eq(agentID),
			goqu.I("status").Eq(service.WakeupStatusPending),
		).
		Order(goqu.I("created_at").Asc()).
		Limit(1).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get first pending wakeup query: %w", err)
	}

	row, err := scanWakeupRequestRow(s.db.QueryRowContext(ctx, query))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get first pending wakeup for agent %q: %w", agentID, err)
	}

	return wakeupRequestRowToRecord(row)
}

func wakeupRequestRowToRecord(row wakeupRequestRow) (*service.WakeupRequest, error) {
	var wakeupCtx map[string]any
	if row.Context.Valid && row.Context.String != "" {
		if err := json.Unmarshal([]byte(row.Context.String), &wakeupCtx); err != nil {
			return nil, fmt.Errorf("unmarshal wakeup context for %q: %w", row.ID, err)
		}
	}

	return &service.WakeupRequest{
		ID:             row.ID,
		AgentID:        row.AgentID,
		Status:         row.Status,
		IdempotencyKey: row.IdempotencyKey.String,
		Context:        wakeupCtx,
		CoalescedCount: row.CoalescedCount,
		RunID:          row.RunID.String,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}, nil
}
