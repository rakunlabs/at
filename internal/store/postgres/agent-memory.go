package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
)

// ─── Agent Memory ───

type agentMemoryRow struct {
	ID             string         `db:"id"`
	AgentID        string         `db:"agent_id"`
	OrganizationID string         `db:"organization_id"`
	TaskID         string         `db:"task_id"`
	TaskIdentifier sql.NullString `db:"task_identifier"`
	SummaryL0      string         `db:"summary_l0"`
	SummaryL1      string         `db:"summary_l1"`
	Tags           sql.NullString `db:"tags"` // JSON array
	CreatedAt      string         `db:"created_at"`
}

var agentMemoryCols = []any{
	"id", "agent_id", "organization_id", "task_id", "task_identifier",
	"summary_l0", "summary_l1", "tags", "created_at",
}

func (p *Postgres) scanAgentMemoryRow(scanner interface{ Scan(...any) error }) (agentMemoryRow, error) {
	var row agentMemoryRow
	err := scanner.Scan(
		&row.ID, &row.AgentID, &row.OrganizationID, &row.TaskID, &row.TaskIdentifier,
		&row.SummaryL0, &row.SummaryL1, &row.Tags, &row.CreatedAt,
	)

	return row, err
}

func agentMemoryRowToRecord(row agentMemoryRow) service.AgentMemory {
	var tags []string
	if row.Tags.Valid && row.Tags.String != "" {
		_ = json.Unmarshal([]byte(row.Tags.String), &tags)
	}

	return service.AgentMemory{
		ID:             row.ID,
		AgentID:        row.AgentID,
		OrganizationID: row.OrganizationID,
		TaskID:         row.TaskID,
		TaskIdentifier: row.TaskIdentifier.String,
		SummaryL0:      row.SummaryL0,
		SummaryL1:      row.SummaryL1,
		Tags:           tags,
		CreatedAt:      row.CreatedAt,
	}
}

func (p *Postgres) CreateAgentMemory(ctx context.Context, mem service.AgentMemory) (*service.AgentMemory, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	var tagsJSON string
	if len(mem.Tags) > 0 {
		raw, _ := json.Marshal(mem.Tags)
		tagsJSON = string(raw)
	}

	q, _, err := p.goqu.Insert(p.tableAgentMemory).Rows(
		goqu.Record{
			"id":              id,
			"agent_id":        mem.AgentID,
			"organization_id": mem.OrganizationID,
			"task_id":         mem.TaskID,
			"task_identifier": nullString(mem.TaskIdentifier),
			"summary_l0":      mem.SummaryL0,
			"summary_l1":      mem.SummaryL1,
			"tags":            nullString(tagsJSON),
			"created_at":      now,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert agent memory query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, q); err != nil {
		return nil, fmt.Errorf("create agent memory: %w", err)
	}

	return &service.AgentMemory{
		ID:             id,
		AgentID:        mem.AgentID,
		OrganizationID: mem.OrganizationID,
		TaskID:         mem.TaskID,
		TaskIdentifier: mem.TaskIdentifier,
		SummaryL0:      mem.SummaryL0,
		SummaryL1:      mem.SummaryL1,
		Tags:           mem.Tags,
		CreatedAt:      now,
	}, nil
}

func (p *Postgres) GetAgentMemory(ctx context.Context, id string) (*service.AgentMemory, error) {
	q, _, err := p.goqu.From(p.tableAgentMemory).
		Select(agentMemoryCols...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get agent memory query: %w", err)
	}

	row, err := p.scanAgentMemoryRow(p.db.QueryRowContext(ctx, q))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get agent memory %q: %w", id, err)
	}

	rec := agentMemoryRowToRecord(row)

	return &rec, nil
}

func (p *Postgres) ListAgentMemories(ctx context.Context, agentID, orgID string) ([]service.AgentMemory, error) {
	q, _, err := p.goqu.From(p.tableAgentMemory).
		Select(agentMemoryCols...).
		Where(goqu.I("agent_id").Eq(agentID), goqu.I("organization_id").Eq(orgID)).
		Order(goqu.I("created_at").Desc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list agent memories query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list agent memories: %w", err)
	}
	defer rows.Close()

	var items []service.AgentMemory
	for rows.Next() {
		row, err := p.scanAgentMemoryRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent memory row: %w", err)
		}
		items = append(items, agentMemoryRowToRecord(row))
	}

	return items, rows.Err()
}

func (p *Postgres) ListOrgMemories(ctx context.Context, orgID string) ([]service.AgentMemory, error) {
	q, _, err := p.goqu.From(p.tableAgentMemory).
		Select(agentMemoryCols...).
		Where(goqu.I("organization_id").Eq(orgID)).
		Order(goqu.I("created_at").Desc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list org memories query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list org memories: %w", err)
	}
	defer rows.Close()

	var items []service.AgentMemory
	for rows.Next() {
		row, err := p.scanAgentMemoryRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent memory row: %w", err)
		}
		items = append(items, agentMemoryRowToRecord(row))
	}

	return items, rows.Err()
}

func (p *Postgres) SearchAgentMemories(ctx context.Context, agentID, orgID, query string) ([]service.AgentMemory, error) {
	queryLike := "%" + strings.ToLower(query) + "%"

	ds := p.goqu.From(p.tableAgentMemory).
		Select(agentMemoryCols...).
		Where(
			goqu.I("organization_id").Eq(orgID),
			goqu.Or(
				goqu.L("LOWER(summary_l0) LIKE ?", queryLike),
				goqu.L("LOWER(summary_l1) LIKE ?", queryLike),
				goqu.L("LOWER(tags) LIKE ?", queryLike),
			),
		).
		Order(goqu.I("created_at").Desc())

	if agentID != "" {
		ds = ds.Where(goqu.I("agent_id").Eq(agentID))
	}

	q, _, err := ds.ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build search agent memories query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("search agent memories: %w", err)
	}
	defer rows.Close()

	var items []service.AgentMemory
	for rows.Next() {
		row, err := p.scanAgentMemoryRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent memory row: %w", err)
		}
		items = append(items, agentMemoryRowToRecord(row))
	}

	return items, rows.Err()
}

func (p *Postgres) DeleteAgentMemory(ctx context.Context, id string) error {
	// Delete messages first.
	qMsgs, _, err := p.goqu.Delete(p.tableAgentMemoryMessages).
		Where(goqu.I("memory_id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete agent memory messages query: %w", err)
	}
	if _, err := p.db.ExecContext(ctx, qMsgs); err != nil {
		return fmt.Errorf("delete agent memory messages for %q: %w", id, err)
	}

	q, _, err := p.goqu.Delete(p.tableAgentMemory).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete agent memory query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, q)
	if err != nil {
		return fmt.Errorf("delete agent memory %q: %w", id, err)
	}

	return nil
}

func (p *Postgres) GetAgentMemoryMessages(ctx context.Context, memoryID string) (*service.AgentMemoryMessages, error) {
	q, _, err := p.goqu.From(p.tableAgentMemoryMessages).
		Select("memory_id", "messages").
		Where(goqu.I("memory_id").Eq(memoryID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get agent memory messages query: %w", err)
	}

	var mid string
	var messagesJSON sql.NullString
	err = p.db.QueryRowContext(ctx, q).Scan(&mid, &messagesJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get agent memory messages for %q: %w", memoryID, err)
	}

	var messages []service.Message
	if messagesJSON.Valid && messagesJSON.String != "" {
		if err := json.Unmarshal([]byte(messagesJSON.String), &messages); err != nil {
			return nil, fmt.Errorf("unmarshal messages for memory %q: %w", memoryID, err)
		}
	}

	return &service.AgentMemoryMessages{
		MemoryID: mid,
		Messages: messages,
	}, nil
}

func (p *Postgres) CreateAgentMemoryMessages(ctx context.Context, msgs service.AgentMemoryMessages) error {
	messagesJSON, err := json.Marshal(msgs.Messages)
	if err != nil {
		return fmt.Errorf("marshal messages: %w", err)
	}

	q, _, err := p.goqu.Insert(p.tableAgentMemoryMessages).Rows(
		goqu.Record{
			"memory_id": msgs.MemoryID,
			"messages":  string(messagesJSON),
		},
	).ToSQL()
	if err != nil {
		return fmt.Errorf("build insert agent memory messages query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, q); err != nil {
		return fmt.Errorf("create agent memory messages: %w", err)
	}

	return nil
}
