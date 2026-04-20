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
	"github.com/rakunlabs/query"
	"github.com/worldline-go/types"
)

// ─── Chat Session CRUD ───

type chatSessionRow struct {
	ID             string         `db:"id"`
	AgentID        string         `db:"agent_id"`
	TaskID         string         `db:"task_id"`
	OrganizationID string         `db:"organization_id"`
	Name           string         `db:"name"`
	Config         types.RawJSON  `db:"config"`
	CreatedAt      time.Time      `db:"created_at"`
	UpdatedAt      time.Time      `db:"updated_at"`
	CreatedBy      sql.NullString `db:"created_by"`
	UpdatedBy      sql.NullString `db:"updated_by"`
}

type chatMessageRow struct {
	ID        string        `db:"id"`
	SessionID string        `db:"session_id"`
	Role      string        `db:"role"`
	Data      types.RawJSON `db:"data"`
	CreatedAt time.Time     `db:"created_at"`
}

func (p *Postgres) ListChatSessions(ctx context.Context, q *query.Query) (*service.ListResult[service.ChatSession], error) {
	sql, total, err := p.buildListQuery(ctx, p.tableChatSessions, q, "id", "agent_id", "task_id", "organization_id", "name", "config", "created_at", "updated_at", "created_by", "updated_by")
	if err != nil {
		return nil, fmt.Errorf("build list chat sessions query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list chat sessions: %w", err)
	}
	defer rows.Close()

	var items []service.ChatSession
	for rows.Next() {
		var row chatSessionRow
		if err := rows.Scan(&row.ID, &row.AgentID, &row.TaskID, &row.OrganizationID, &row.Name, &row.Config, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy); err != nil {
			return nil, fmt.Errorf("scan chat session row: %w", err)
		}

		rec, err := chatSessionRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *rec)
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.ChatSession]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (p *Postgres) GetChatSession(ctx context.Context, id string) (*service.ChatSession, error) {
	query, _, err := p.goqu.From(p.tableChatSessions).
		Select("id", "agent_id", "task_id", "organization_id", "name", "config", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get chat session query: %w", err)
	}

	var row chatSessionRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.AgentID, &row.TaskID, &row.OrganizationID, &row.Name, &row.Config, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get chat session %q: %w", id, err)
	}

	return chatSessionRowToRecord(row)
}

func (p *Postgres) GetChatSessionByPlatform(ctx context.Context, platform, platformUserID, platformChannelID string) (*service.ChatSession, error) {
	query, _, err := p.goqu.From(p.tableChatSessions).
		Select("id", "agent_id", "task_id", "organization_id", "name", "config", "created_at", "updated_at", "created_by", "updated_by").
		Where(
			goqu.L("config->>'platform'").Eq(platform),
			goqu.L("config->>'platform_user_id'").Eq(platformUserID),
			goqu.L("config->>'platform_channel_id'").Eq(platformChannelID),
		).
		Limit(1).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get chat session by platform query: %w", err)
	}

	var row chatSessionRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.AgentID, &row.TaskID, &row.OrganizationID, &row.Name, &row.Config, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get chat session by platform: %w", err)
	}

	return chatSessionRowToRecord(row)
}

func (p *Postgres) GetChatSessionByTaskID(ctx context.Context, taskID string) (*service.ChatSession, error) {
	query, _, err := p.goqu.From(p.tableChatSessions).
		Select("id", "agent_id", "task_id", "organization_id", "name", "config", "created_at", "updated_at", "created_by", "updated_by").
		Where(
			goqu.I("task_id").Eq(taskID),
			goqu.I("task_id").Neq(""),
		).
		Limit(1).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get chat session by task id query: %w", err)
	}

	var row chatSessionRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.AgentID, &row.TaskID, &row.OrganizationID, &row.Name, &row.Config, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get chat session by task id %q: %w", taskID, err)
	}

	return chatSessionRowToRecord(row)
}

func (p *Postgres) CreateChatSession(ctx context.Context, session service.ChatSession) (*service.ChatSession, error) {
	configJSON, err := json.Marshal(session.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal chat session config: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := p.goqu.Insert(p.tableChatSessions).Rows(
		goqu.Record{
			"id":              id,
			"agent_id":        session.AgentID,
			"task_id":         session.TaskID,
			"organization_id": session.OrganizationID,
			"name":            session.Name,
			"config":          types.RawJSON(configJSON),
			"created_at":      now,
			"updated_at":      now,
			"created_by":      session.CreatedBy,
			"updated_by":      session.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert chat session query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create chat session: %w", err)
	}

	return &service.ChatSession{
		ID:             id,
		AgentID:        session.AgentID,
		TaskID:         session.TaskID,
		OrganizationID: session.OrganizationID,
		Name:           session.Name,
		Config:         session.Config,
		CreatedAt:      now.Format(time.RFC3339),
		UpdatedAt:      now.Format(time.RFC3339),
		CreatedBy:      session.CreatedBy,
		UpdatedBy:      session.UpdatedBy,
	}, nil
}

func (p *Postgres) UpdateChatSession(ctx context.Context, id string, session service.ChatSession) (*service.ChatSession, error) {
	configJSON, err := json.Marshal(session.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal chat session config: %w", err)
	}

	now := time.Now().UTC()

	record := goqu.Record{
		"updated_at": now,
		"updated_by": session.UpdatedBy,
	}
	if session.Name != "" {
		record["name"] = session.Name
	}
	if session.AgentID != "" {
		record["agent_id"] = session.AgentID
	}
	if session.TaskID != "" {
		record["task_id"] = session.TaskID
	}
	if session.OrganizationID != "" {
		record["organization_id"] = session.OrganizationID
	}
	// Only update config if any platform field is set (avoids wiping config on partial updates).
	if session.Config.Platform != "" || session.Config.PlatformUserID != "" || session.Config.PlatformChannelID != "" {
		record["config"] = types.RawJSON(configJSON)
	}

	query, _, err := p.goqu.Update(p.tableChatSessions).Set(record).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update chat session query: %w", err)
	}

	res, err := p.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update chat session %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return p.GetChatSession(ctx, id)
}

func (p *Postgres) DeleteChatSession(ctx context.Context, id string) error {
	query, _, err := p.goqu.Delete(p.tableChatSessions).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete chat session query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete chat session %q: %w", id, err)
	}

	return nil
}

func (p *Postgres) ListChatMessages(ctx context.Context, sessionID string) ([]service.ChatMessage, error) {
	query, _, err := p.goqu.From(p.tableChatMessages).
		Select("id", "session_id", "role", "data", "created_at").
		Where(goqu.I("session_id").Eq(sessionID)).
		Order(goqu.I("created_at").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list chat messages query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list chat messages: %w", err)
	}
	defer rows.Close()

	var items []service.ChatMessage
	for rows.Next() {
		var row chatMessageRow
		if err := rows.Scan(&row.ID, &row.SessionID, &row.Role, &row.Data, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan chat message row: %w", err)
		}

		msg, err := chatMessageRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *msg)
	}

	return items, rows.Err()
}

func (p *Postgres) CreateChatMessage(ctx context.Context, msg service.ChatMessage) (*service.ChatMessage, error) {
	dataJSON, err := json.Marshal(msg.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal chat message data: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := p.goqu.Insert(p.tableChatMessages).Rows(
		goqu.Record{
			"id":         id,
			"session_id": msg.SessionID,
			"role":       msg.Role,
			"data":       types.RawJSON(dataJSON),
			"created_at": now,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert chat message query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create chat message: %w", err)
	}

	// Bump the owning session's updated_at so the UI can surface the
	// most-recently-active session at the top of the list.
	if msg.SessionID != "" {
		if err := p.touchChatSession(ctx, nil, msg.SessionID, now); err != nil {
			return nil, err
		}
	}

	return &service.ChatMessage{
		ID:        id,
		SessionID: msg.SessionID,
		Role:      msg.Role,
		Data:      msg.Data,
		CreatedAt: now.Format(time.RFC3339),
	}, nil
}

// touchChatSession updates the session's updated_at to now. Runs inside the
// given transaction when tx is non-nil, otherwise against the main DB handle.
func (p *Postgres) touchChatSession(ctx context.Context, tx *sql.Tx, sessionID string, now time.Time) error {
	q, _, err := p.goqu.Update(p.tableChatSessions).
		Set(goqu.Record{"updated_at": now}).
		Where(goqu.I("id").Eq(sessionID)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build touch chat session query: %w", err)
	}
	if tx != nil {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("touch chat session: %w", err)
		}
		return nil
	}
	if _, err := p.db.ExecContext(ctx, q); err != nil {
		return fmt.Errorf("touch chat session: %w", err)
	}
	return nil
}

func (p *Postgres) CreateChatMessages(ctx context.Context, msgs []service.ChatMessage) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	now := time.Now().UTC()
	touched := make(map[string]struct{}, len(msgs))

	for _, msg := range msgs {
		dataJSON, err := json.Marshal(msg.Data)
		if err != nil {
			return fmt.Errorf("marshal chat message data: %w", err)
		}

		id := ulid.Make().String()

		query, _, err := p.goqu.Insert(p.tableChatMessages).Rows(
			goqu.Record{
				"id":         id,
				"session_id": msg.SessionID,
				"role":       msg.Role,
				"data":       types.RawJSON(dataJSON),
				"created_at": now,
			},
		).ToSQL()
		if err != nil {
			return fmt.Errorf("build insert chat message query: %w", err)
		}

		if _, err := tx.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("create chat message: %w", err)
		}

		if msg.SessionID != "" {
			touched[msg.SessionID] = struct{}{}
		}
	}

	// Bump updated_at on every session that received a new message so
	// recently-active sessions sort to the top of the UI list.
	for sessionID := range touched {
		if err := p.touchChatSession(ctx, tx, sessionID, now); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (p *Postgres) DeleteChatMessages(ctx context.Context, sessionID string) error {
	query, _, err := p.goqu.Delete(p.tableChatMessages).
		Where(goqu.I("session_id").Eq(sessionID)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete chat messages query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete chat messages for session %q: %w", sessionID, err)
	}

	return nil
}

func chatSessionRowToRecord(row chatSessionRow) (*service.ChatSession, error) {
	var cfg service.ChatSessionConfig
	if len(row.Config) > 0 {
		if err := json.Unmarshal(row.Config, &cfg); err != nil {
			return nil, fmt.Errorf("unmarshal chat session config for %q: %w", row.ID, err)
		}
	}

	return &service.ChatSession{
		ID:             row.ID,
		AgentID:        row.AgentID,
		TaskID:         row.TaskID,
		OrganizationID: row.OrganizationID,
		Name:           row.Name,
		Config:         cfg,
		CreatedAt:      row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:      row.UpdatedAt.Format(time.RFC3339),
		CreatedBy:      row.CreatedBy.String,
		UpdatedBy:      row.UpdatedBy.String,
	}, nil
}

func chatMessageRowToRecord(row chatMessageRow) (*service.ChatMessage, error) {
	var data service.ChatMessageData
	if len(row.Data) > 0 {
		if err := json.Unmarshal(row.Data, &data); err != nil {
			return nil, fmt.Errorf("unmarshal chat message data for %q: %w", row.ID, err)
		}
	}

	return &service.ChatMessage{
		ID:        row.ID,
		SessionID: row.SessionID,
		Role:      row.Role,
		Data:      data,
		CreatedAt: row.CreatedAt.Format(time.RFC3339),
	}, nil
}
