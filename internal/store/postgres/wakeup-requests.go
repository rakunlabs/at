package postgres

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

// ─── Wakeup Requests ───

type wakeupRequestRow struct {
	ID             string          `db:"id"`
	AgentID        string          `db:"agent_id"`
	Status         string          `db:"status"`
	IdempotencyKey sql.NullString  `db:"idempotency_key"`
	Context        json.RawMessage `db:"context"`
	CoalescedCount int             `db:"coalesced_count"`
	RunID          sql.NullString  `db:"run_id"`
	CreatedAt      time.Time       `db:"created_at"`
	UpdatedAt      time.Time       `db:"updated_at"`
}

var wakeupRequestColumns = []interface{}{
	"id", "agent_id", "status", "idempotency_key", "context",
	"coalesced_count", "run_id", "created_at", "updated_at",
}

func scanWakeupRequestRow(scanner interface {
	Scan(dest ...interface{}) error
}, row *wakeupRequestRow) error {
	return scanner.Scan(
		&row.ID, &row.AgentID, &row.Status, &row.IdempotencyKey, &row.Context,
		&row.CoalescedCount, &row.RunID, &row.CreatedAt, &row.UpdatedAt,
	)
}

func (p *Postgres) CreateOrCoalesce(ctx context.Context, req service.WakeupRequest) (*service.WakeupRequest, error) {
	now := time.Now().UTC()

	// 1. If idempotencyKey is non-empty, check for existing request with same key.
	if req.IdempotencyKey != "" {
		existingQuery, _, err := p.goqu.From(p.tableWakeupRequests).
			Select(wakeupRequestColumns...).
			Where(goqu.I("idempotency_key").Eq(req.IdempotencyKey)).
			ToSQL()
		if err != nil {
			return nil, fmt.Errorf("build idempotency check query: %w", err)
		}

		var row wakeupRequestRow
		err = scanWakeupRequestRow(p.db.QueryRowContext(ctx, existingQuery), &row)
		if err == nil {
			// Found existing — return it.
			return wakeupRequestRowToRecord(row)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("check idempotency key %q: %w", req.IdempotencyKey, err)
		}
	}

	// 2. Look for pending request for same agent and coalesce.
	pendingQuery, _, err := p.goqu.From(p.tableWakeupRequests).
		Select(wakeupRequestColumns...).
		Where(
			goqu.I("agent_id").Eq(req.AgentID),
			goqu.I("status").Eq("pending"),
		).
		Order(goqu.I("created_at").Asc()).
		Limit(1).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build pending check query: %w", err)
	}

	var pendingRow wakeupRequestRow
	err = scanWakeupRequestRow(p.db.QueryRowContext(ctx, pendingQuery), &pendingRow)
	if err == nil {
		// Found pending request — merge context and increment count.
		var existingCtx map[string]any
		if len(pendingRow.Context) > 0 {
			if err := json.Unmarshal(pendingRow.Context, &existingCtx); err != nil {
				return nil, fmt.Errorf("unmarshal existing context: %w", err)
			}
		}
		if existingCtx == nil {
			existingCtx = make(map[string]any)
		}

		// Merge incoming context into existing.
		for k, v := range req.Context {
			existingCtx[k] = v
		}

		mergedJSON, err := json.Marshal(existingCtx)
		if err != nil {
			return nil, fmt.Errorf("marshal merged context: %w", err)
		}

		updateQuery, _, err := p.goqu.Update(p.tableWakeupRequests).Set(
			goqu.Record{
				"context":         mergedJSON,
				"coalesced_count": pendingRow.CoalescedCount + 1,
				"updated_at":      now,
			},
		).Where(goqu.I("id").Eq(pendingRow.ID)).ToSQL()
		if err != nil {
			return nil, fmt.Errorf("build coalesce update query: %w", err)
		}

		if _, err := p.db.ExecContext(ctx, updateQuery); err != nil {
			return nil, fmt.Errorf("coalesce wakeup request: %w", err)
		}

		return p.GetWakeupRequest(ctx, pendingRow.ID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("check pending wakeup requests: %w", err)
	}

	// 3. No existing request — create new.
	id := ulid.Make().String()

	contextJSON, err := json.Marshal(req.Context)
	if err != nil {
		return nil, fmt.Errorf("marshal wakeup context: %w", err)
	}

	insertQuery, _, err := p.goqu.Insert(p.tableWakeupRequests).Rows(
		goqu.Record{
			"id":              id,
			"agent_id":        req.AgentID,
			"status":          "pending",
			"idempotency_key": nullString(req.IdempotencyKey),
			"context":         contextJSON,
			"coalesced_count": 0,
			"run_id":          nil,
			"created_at":      now,
			"updated_at":      now,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert wakeup request query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, insertQuery); err != nil {
		return nil, fmt.Errorf("create wakeup request: %w", err)
	}

	return &service.WakeupRequest{
		ID:             id,
		AgentID:        req.AgentID,
		Status:         "pending",
		IdempotencyKey: req.IdempotencyKey,
		Context:        req.Context,
		CoalescedCount: 0,
		CreatedAt:      now.Format(time.RFC3339),
		UpdatedAt:      now.Format(time.RFC3339),
	}, nil
}

func (p *Postgres) GetWakeupRequest(ctx context.Context, id string) (*service.WakeupRequest, error) {
	query, _, err := p.goqu.From(p.tableWakeupRequests).
		Select(wakeupRequestColumns...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get wakeup request query: %w", err)
	}

	var row wakeupRequestRow
	err = scanWakeupRequestRow(p.db.QueryRowContext(ctx, query), &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get wakeup request %q: %w", id, err)
	}

	return wakeupRequestRowToRecord(row)
}

func (p *Postgres) ListPendingForAgent(ctx context.Context, agentID string) ([]service.WakeupRequest, error) {
	query, _, err := p.goqu.From(p.tableWakeupRequests).
		Select(wakeupRequestColumns...).
		Where(
			goqu.I("agent_id").Eq(agentID),
			goqu.I("status").Eq("pending"),
		).
		Order(goqu.I("created_at").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list pending wakeup requests query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list pending wakeup requests for agent %q: %w", agentID, err)
	}
	defer rows.Close()

	var items []service.WakeupRequest
	for rows.Next() {
		var row wakeupRequestRow
		if err := scanWakeupRequestRow(rows, &row); err != nil {
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

func (p *Postgres) MarkDispatched(ctx context.Context, id, runID string) error {
	now := time.Now().UTC()

	query, _, err := p.goqu.Update(p.tableWakeupRequests).Set(
		goqu.Record{
			"status":     "dispatched",
			"run_id":     runID,
			"updated_at": now,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return fmt.Errorf("build mark dispatched query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("mark wakeup request %q as dispatched: %w", id, err)
	}

	return nil
}

func (p *Postgres) PromoteDeferred(ctx context.Context, agentID string) error {
	now := time.Now().UTC()

	// Find the oldest deferred request for this agent.
	selectQuery, _, err := p.goqu.From(p.tableWakeupRequests).
		Select("id").
		Where(
			goqu.I("agent_id").Eq(agentID),
			goqu.I("status").Eq("deferred_issue_execution"),
		).
		Order(goqu.I("created_at").Asc()).
		Limit(1).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build select deferred query: %w", err)
	}

	var deferredID string
	err = p.db.QueryRowContext(ctx, selectQuery).Scan(&deferredID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // nothing to promote
	}
	if err != nil {
		return fmt.Errorf("find deferred wakeup request: %w", err)
	}

	updateQuery, _, err := p.goqu.Update(p.tableWakeupRequests).Set(
		goqu.Record{
			"status":     "pending",
			"updated_at": now,
		},
	).Where(goqu.I("id").Eq(deferredID)).ToSQL()
	if err != nil {
		return fmt.Errorf("build promote deferred query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, updateQuery)
	if err != nil {
		return fmt.Errorf("promote deferred wakeup request %q: %w", deferredID, err)
	}

	return nil
}

func wakeupRequestRowToRecord(row wakeupRequestRow) (*service.WakeupRequest, error) {
	var reqContext map[string]any
	if len(row.Context) > 0 {
		if err := json.Unmarshal(row.Context, &reqContext); err != nil {
			return nil, fmt.Errorf("unmarshal wakeup context for %q: %w", row.ID, err)
		}
	}

	return &service.WakeupRequest{
		ID:             row.ID,
		AgentID:        row.AgentID,
		Status:         row.Status,
		IdempotencyKey: row.IdempotencyKey.String,
		Context:        reqContext,
		CoalescedCount: row.CoalescedCount,
		RunID:          row.RunID.String,
		CreatedAt:      row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:      row.UpdatedAt.Format(time.RFC3339),
	}, nil
}
