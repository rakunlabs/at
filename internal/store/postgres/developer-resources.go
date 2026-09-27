package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/doug-martin/goqu/v9/exp"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

func developerOwned(actor service.AccessPrincipal, extra goqu.Ex) goqu.Ex {
	where := goqu.Ex{"workspace_id": actor.WorkspaceID, "owner_user_id": actor.UserID}
	for key, value := range extra {
		where[key] = value
	}
	return where
}

func developerParentExists(ctx context.Context, db *goqu.Database, table exp.IdentifierExpression, actor service.AccessPrincipal, id string) (bool, error) {
	count, err := db.From(table).Where(developerOwned(actor, goqu.Ex{"id": id})).CountContext(ctx)
	return count > 0, err
}

var developerSessionColumns = []any{
	"id", "space_id", "project_path", "workspace_id", "owner_user_id",
	"title", "mode", "status", "provider", "model", "config", "error",
	"started_at", "finished_at", "created_at", "updated_at",
}

type developerSessionRow struct {
	ID          string       `db:"id"`
	SpaceID     string       `db:"space_id"`
	ProjectPath string       `db:"project_path"`
	WorkspaceID string       `db:"workspace_id"`
	OwnerUserID string       `db:"owner_user_id"`
	Title       string       `db:"title"`
	Mode        string       `db:"mode"`
	Status      string       `db:"status"`
	Provider    string       `db:"provider"`
	Model       string       `db:"model"`
	Config      string       `db:"config"`
	Error       string       `db:"error"`
	StartedAt   sql.NullTime `db:"started_at"`
	FinishedAt  sql.NullTime `db:"finished_at"`
	CreatedAt   time.Time    `db:"created_at"`
	UpdatedAt   time.Time    `db:"updated_at"`
}

func developerSessionRecord(row developerSessionRow) (*service.DeveloperSession, error) {
	record := &service.DeveloperSession{
		ID: row.ID, SpaceID: row.SpaceID, ProjectPath: row.ProjectPath,
		WorkspaceID: row.WorkspaceID, OwnerUserID: row.OwnerUserID, Title: row.Title, Mode: row.Mode,
		Status: row.Status, Provider: row.Provider, Model: row.Model, Error: row.Error,
		CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if row.StartedAt.Valid {
		record.StartedAt = row.StartedAt.Time.UTC().Format(time.RFC3339)
	}
	if row.FinishedAt.Valid {
		record.FinishedAt = row.FinishedAt.Time.UTC().Format(time.RFC3339)
	}
	if err := json.Unmarshal([]byte(row.Config), &record.Config); err != nil {
		return nil, fmt.Errorf("decode developer session config: %w", err)
	}
	return record, nil
}

func (p *Postgres) ListDeveloperSessions(ctx context.Context, spaceID string) ([]service.DeveloperSession, error) {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
	var rows []developerSessionRow
	if err := p.goqu.From(p.tableDeveloperSessions).Select(developerSessionColumns...).
		Where(developerOwned(actor, goqu.Ex{"space_id": spaceID})).Order(goqu.I("updated_at").Desc()).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list developer sessions: %w", err)
	}
	result := make([]service.DeveloperSession, 0, len(rows))
	for _, row := range rows {
		record, err := developerSessionRecord(row)
		if err != nil {
			return nil, err
		}
		result = append(result, *record)
	}
	return result, nil
}

func (p *Postgres) GetDeveloperSession(ctx context.Context, id string) (*service.DeveloperSession, error) {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
	var row developerSessionRow
	found, err := p.goqu.From(p.tableDeveloperSessions).Select(developerSessionColumns...).
		Where(developerOwned(actor, goqu.Ex{"id": id})).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("get developer session: %w", err)
	}
	if !found {
		return nil, service.ErrDeveloperSpaceNotFound
	}
	return developerSessionRecord(row)
}

func (p *Postgres) CreateDeveloperSession(ctx context.Context, session service.DeveloperSession) (*service.DeveloperSession, error) {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
	if err := session.Validate(); err != nil {
		return nil, err
	}
	exists, err := developerParentExists(ctx, p.goqu, p.tableDeveloperSpaces, actor, session.SpaceID)
	if err != nil {
		return nil, fmt.Errorf("check developer space: %w", err)
	}
	if !exists {
		return nil, service.ErrDeveloperSpaceNotFound
	}
	projectPath, err := service.CleanDeveloperPath(session.ProjectPath)
	if err != nil {
		return nil, err
	}
	if session.Config == nil {
		session.Config = map[string]any{}
	}
	encoded, err := json.Marshal(session.Config)
	if err != nil {
		return nil, fmt.Errorf("encode developer session config: %w", err)
	}
	if session.ID == "" {
		session.ID = ulid.Make().String()
	}
	var row developerSessionRow
	_, err = p.goqu.Insert(p.tableDeveloperSessions).Rows(goqu.Record{
		"id": session.ID, "space_id": session.SpaceID, "project_path": projectPath,
		"workspace_id": actor.WorkspaceID, "owner_user_id": actor.UserID, "title": strings.TrimSpace(session.Title),
		"mode": session.Mode, "status": service.DeveloperSessionIdle, "provider": strings.TrimSpace(session.Provider),
		"model": strings.TrimSpace(session.Model), "config": string(encoded),
	}).Returning(developerSessionColumns...).Executor().ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("create developer session: %w", err)
	}
	return developerSessionRecord(row)
}

func (p *Postgres) RenameDeveloperSession(ctx context.Context, id, title string) (*service.DeveloperSession, error) {
	return p.updateDeveloperSession(ctx, id, goqu.Record{"title": strings.TrimSpace(title)})
}

// UpdateDeveloperSessionSettings changes the agent profile and model used by
// the next run. A running or waiting session keeps its settings until it
// finishes, so a turn never switches permissions halfway through.
func (p *Postgres) UpdateDeveloperSessionSettings(ctx context.Context, id, mode, provider, model string) (*service.DeveloperSession, error) {
	if !service.ValidDeveloperMode(mode) {
		return nil, fmt.Errorf("mode must be plan, build, or review")
	}
	if strings.TrimSpace(provider) == "" {
		return nil, fmt.Errorf("provider is required")
	}
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
	var row developerSessionRow
	found, err := p.goqu.Update(p.tableDeveloperSessions).Set(goqu.Record{
		"mode": mode, "provider": strings.TrimSpace(provider), "model": strings.TrimSpace(model), "updated_at": goqu.L("clock_timestamp()"),
	}).Where(developerOwned(actor, goqu.Ex{"id": id}), goqu.I("status").NotIn(service.DeveloperSessionRunning, service.DeveloperSessionWaitingPermission, service.DeveloperSessionWaitingQuestion)).
		Returning(developerSessionColumns...).Executor().ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("update developer session: %w", err)
	}
	if !found {
		if _, err := p.GetDeveloperSession(ctx, id); err != nil {
			return nil, err
		}
		return nil, service.ErrDeveloperSessionBusy
	}
	return developerSessionRecord(row)
}

func (p *Postgres) updateDeveloperSession(ctx context.Context, id string, set goqu.Record) (*service.DeveloperSession, error) {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
	set["updated_at"] = goqu.L("clock_timestamp()")
	var row developerSessionRow
	found, err := p.goqu.Update(p.tableDeveloperSessions).Set(set).Where(developerOwned(actor, goqu.Ex{"id": id})).
		Returning(developerSessionColumns...).Executor().ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("update developer session: %w", err)
	}
	if !found {
		return nil, service.ErrDeveloperSpaceNotFound
	}
	return developerSessionRecord(row)
}

func (p *Postgres) DeleteDeveloperSession(ctx context.Context, id string) error {
	return p.deleteDeveloperResource(ctx, p.tableDeveloperSessions, id, "session")
}

func (p *Postgres) SetDeveloperSessionRuntime(ctx context.Context, id, status, runtimeError string) (*service.DeveloperSession, error) {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
	valid := status == service.DeveloperSessionIdle || status == service.DeveloperSessionRunning ||
		status == service.DeveloperSessionWaitingPermission || status == service.DeveloperSessionWaitingQuestion ||
		status == service.DeveloperSessionCompleted || status == service.DeveloperSessionFailed || status == service.DeveloperSessionCancelled
	if !valid {
		return nil, fmt.Errorf("invalid developer session status")
	}
	set := goqu.Record{"status": status, "error": runtimeError, "updated_at": goqu.L("clock_timestamp()")}
	if status == service.DeveloperSessionRunning {
		set["started_at"] = goqu.L("COALESCE(started_at, clock_timestamp())")
		set["finished_at"] = nil
	}
	if status == service.DeveloperSessionCompleted || status == service.DeveloperSessionFailed || status == service.DeveloperSessionCancelled {
		set["finished_at"] = goqu.L("clock_timestamp()")
	}
	var row developerSessionRow
	found, err := p.goqu.Update(p.tableDeveloperSessions).Set(set).Where(developerOwned(actor, goqu.Ex{"id": id})).
		Returning(developerSessionColumns...).Executor().ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("set developer session runtime: %w", err)
	}
	if !found {
		return nil, service.ErrDeveloperSpaceNotFound
	}
	return developerSessionRecord(row)
}

func (p *Postgres) BeginDeveloperSessionRun(ctx context.Context, id string) (*service.DeveloperSession, error) {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
	var row developerSessionRow
	found, err := p.goqu.Update(p.tableDeveloperSessions).Set(goqu.Record{
		"status": service.DeveloperSessionRunning, "error": "", "started_at": goqu.L("COALESCE(started_at, clock_timestamp())"),
		"finished_at": nil, "updated_at": goqu.L("clock_timestamp()"),
	}).Where(developerOwned(actor, goqu.Ex{"id": id}), goqu.I("status").NotIn(service.DeveloperSessionRunning, service.DeveloperSessionWaitingPermission, service.DeveloperSessionWaitingQuestion)).
		Returning(developerSessionColumns...).Executor().ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("begin developer session run: %w", err)
	}
	if !found {
		if _, err := p.GetDeveloperSession(ctx, id); err != nil {
			return nil, err
		}
		return nil, service.ErrDeveloperSessionBusy
	}
	return developerSessionRecord(row)
}

var developerSessionMessageColumns = []any{"id", "session_id", "workspace_id", "owner_user_id", "role", "content", "created_at"}

type developerSessionMessageRow struct {
	ID          string    `db:"id"`
	SessionID   string    `db:"session_id"`
	WorkspaceID string    `db:"workspace_id"`
	OwnerUserID string    `db:"owner_user_id"`
	Role        string    `db:"role"`
	Content     string    `db:"content"`
	CreatedAt   time.Time `db:"created_at"`
}

func developerSessionMessageRecord(row developerSessionMessageRow) (*service.DeveloperSessionMessage, error) {
	record := &service.DeveloperSessionMessage{
		ID: row.ID, SessionID: row.SessionID, WorkspaceID: row.WorkspaceID, OwnerUserID: row.OwnerUserID,
		Role: row.Role, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339),
	}
	if row.Role == "assistant" || row.Role == "tool" {
		var blocks []service.ContentBlock
		if err := json.Unmarshal([]byte(row.Content), &blocks); err == nil {
			record.Content = blocks
			return record, nil
		}
	}
	if err := json.Unmarshal([]byte(row.Content), &record.Content); err != nil {
		return nil, fmt.Errorf("decode developer session message: %w", err)
	}
	return record, nil
}

func (p *Postgres) ListDeveloperSessionMessages(ctx context.Context, sessionID string) ([]service.DeveloperSessionMessage, error) {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := p.GetDeveloperSession(ctx, sessionID); err != nil {
		return nil, err
	}
	var rows []developerSessionMessageRow
	if err := p.goqu.From(p.tableDeveloperSessionMessages).Select(developerSessionMessageColumns...).
		Where(developerOwned(actor, goqu.Ex{"session_id": sessionID})).Order(goqu.I("created_at").Asc(), goqu.I("id").Asc()).
		ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list developer session messages: %w", err)
	}
	result := make([]service.DeveloperSessionMessage, 0, len(rows))
	for _, row := range rows {
		record, err := developerSessionMessageRecord(row)
		if err != nil {
			return nil, err
		}
		result = append(result, *record)
	}
	return result, nil
}

func (p *Postgres) AppendDeveloperSessionMessage(ctx context.Context, message service.DeveloperSessionMessage) (*service.DeveloperSessionMessage, error) {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
	if message.Role != "user" && message.Role != "assistant" && message.Role != "tool" && message.Role != "system" {
		return nil, fmt.Errorf("invalid developer session message role")
	}
	if _, err := p.GetDeveloperSession(ctx, message.SessionID); err != nil {
		return nil, err
	}
	content, err := json.Marshal(message.Content)
	if err != nil {
		return nil, fmt.Errorf("encode developer session message: %w", err)
	}
	if message.ID == "" {
		message.ID = ulid.Make().String()
	}
	var row developerSessionMessageRow
	_, err = p.goqu.Insert(p.tableDeveloperSessionMessages).Rows(goqu.Record{
		"id": message.ID, "session_id": message.SessionID, "workspace_id": actor.WorkspaceID,
		"owner_user_id": actor.UserID, "role": message.Role, "content": string(content),
	}).Returning(developerSessionMessageColumns...).Executor().ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("append developer session message: %w", err)
	}
	return developerSessionMessageRecord(row)
}

var developerSessionSnapshotColumns = []any{
	"id", "session_id", "workspace_id", "owner_user_id", "step", "phase", "head_sha", "storage_object_id", "created_at",
}

type developerSessionSnapshotRow struct {
	ID              string         `db:"id"`
	SessionID       string         `db:"session_id"`
	WorkspaceID     string         `db:"workspace_id"`
	OwnerUserID     string         `db:"owner_user_id"`
	Step            int            `db:"step"`
	Phase           string         `db:"phase"`
	HeadSHA         string         `db:"head_sha"`
	StorageObjectID sql.NullString `db:"storage_object_id"`
	CreatedAt       time.Time      `db:"created_at"`
}

func developerSessionSnapshotRecord(row developerSessionSnapshotRow) service.DeveloperSessionSnapshot {
	return service.DeveloperSessionSnapshot{
		ID: row.ID, SessionID: row.SessionID, WorkspaceID: row.WorkspaceID, OwnerUserID: row.OwnerUserID,
		Step: row.Step, Phase: row.Phase, HeadSHA: row.HeadSHA, StorageObjectID: row.StorageObjectID.String,
		CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func (p *Postgres) ListDeveloperSessionSnapshots(ctx context.Context, sessionID string) ([]service.DeveloperSessionSnapshot, error) {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := p.GetDeveloperSession(ctx, sessionID); err != nil {
		return nil, err
	}
	var rows []developerSessionSnapshotRow
	if err := p.goqu.From(p.tableDeveloperSessionSnapshots).Select(developerSessionSnapshotColumns...).
		Where(developerOwned(actor, goqu.Ex{"session_id": sessionID})).
		Order(goqu.I("step").Asc(), goqu.I("phase").Asc()).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list developer session snapshots: %w", err)
	}
	result := make([]service.DeveloperSessionSnapshot, 0, len(rows))
	for _, row := range rows {
		result = append(result, developerSessionSnapshotRecord(row))
	}
	return result, nil
}

func (p *Postgres) SaveDeveloperSessionSnapshot(ctx context.Context, snapshot service.DeveloperSessionSnapshot) (*service.DeveloperSessionSnapshot, error) {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
	if snapshot.Step < 0 || (snapshot.Phase != "before" && snapshot.Phase != "after") {
		return nil, fmt.Errorf("invalid developer session snapshot")
	}
	if _, err := p.GetDeveloperSession(ctx, snapshot.SessionID); err != nil {
		return nil, err
	}
	if snapshot.ID == "" {
		snapshot.ID = ulid.Make().String()
	}
	var storageObject any
	if snapshot.StorageObjectID != "" {
		storageObject = snapshot.StorageObjectID
	}
	var row developerSessionSnapshotRow
	_, err = p.goqu.Insert(p.tableDeveloperSessionSnapshots).Rows(goqu.Record{
		"id": snapshot.ID, "session_id": snapshot.SessionID, "workspace_id": actor.WorkspaceID,
		"owner_user_id": actor.UserID, "step": snapshot.Step, "phase": snapshot.Phase,
		"head_sha": snapshot.HeadSHA, "storage_object_id": storageObject,
	}).OnConflict(goqu.DoUpdate("session_id,step,phase", goqu.Record{
		"head_sha": snapshot.HeadSHA, "storage_object_id": storageObject,
	})).Returning(developerSessionSnapshotColumns...).Executor().ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("save developer session snapshot: %w", err)
	}
	result := developerSessionSnapshotRecord(row)
	return &result, nil
}

func (p *Postgres) GetDeveloperPendingTool(ctx context.Context, sessionID string) (*service.DeveloperPendingTool, error) {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
	var row struct {
		ToolCall  string    `db:"tool_call"`
		CreatedAt time.Time `db:"created_at"`
	}
	found, err := p.goqu.From(p.tableDeveloperPendingTools).Select("tool_call", "created_at").
		Where(developerOwned(actor, goqu.Ex{"session_id": sessionID})).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("get developer pending tool: %w", err)
	}
	if !found {
		return nil, nil
	}
	result := &service.DeveloperPendingTool{SessionID: sessionID, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339)}
	if err := json.Unmarshal([]byte(row.ToolCall), result); err != nil {
		return nil, fmt.Errorf("decode developer pending tool: %w", err)
	}
	result.SessionID = sessionID
	result.CreatedAt = row.CreatedAt.UTC().Format(time.RFC3339)
	return result, nil
}

func (p *Postgres) SaveDeveloperPendingTool(ctx context.Context, pending service.DeveloperPendingTool) (*service.DeveloperPendingTool, error) {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := p.GetDeveloperSession(ctx, pending.SessionID); err != nil {
		return nil, err
	}
	pending.State = "pending"
	pending.CreatedAt = ""
	raw, err := json.Marshal(pending)
	if err != nil {
		return nil, fmt.Errorf("encode developer pending tool: %w", err)
	}
	if _, err := p.goqu.Insert(p.tableDeveloperPendingTools).Rows(goqu.Record{
		"session_id": pending.SessionID, "workspace_id": actor.WorkspaceID, "owner_user_id": actor.UserID, "tool_call": string(raw),
	}).OnConflict(goqu.DoUpdate("session_id", goqu.Record{"tool_call": string(raw), "created_at": goqu.L("clock_timestamp()"), "workspace_id": actor.WorkspaceID, "owner_user_id": actor.UserID})).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("save developer pending tool: %w", err)
	}
	return p.GetDeveloperPendingTool(ctx, pending.SessionID)
}

func (p *Postgres) DeleteDeveloperPendingTool(ctx context.Context, sessionID string) error {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return err
	}
	if _, err := p.goqu.Delete(p.tableDeveloperPendingTools).Where(developerOwned(actor, goqu.Ex{"session_id": sessionID})).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("delete developer pending tool: %w", err)
	}
	return nil
}

func (p *Postgres) ClaimDeveloperPendingTool(ctx context.Context, sessionID string) (*service.DeveloperPendingTool, error) {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
	var row struct {
		ToolCall  string    `db:"tool_call"`
		CreatedAt time.Time `db:"created_at"`
	}
	found, err := p.goqu.Update(p.tableDeveloperPendingTools).Set(goqu.Record{
		"tool_call": goqu.L("jsonb_set(tool_call, '{state}', '\"executing\"'::jsonb, true)"),
	}).Where(developerOwned(actor, goqu.Ex{"session_id": sessionID}), goqu.L("COALESCE(tool_call->>'state', 'pending') = 'pending'")).
		Returning("tool_call", "created_at").Executor().ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("claim developer pending tool: %w", err)
	}
	if !found {
		return nil, nil
	}
	result := &service.DeveloperPendingTool{SessionID: sessionID, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339)}
	if err := json.Unmarshal([]byte(row.ToolCall), result); err != nil {
		return nil, fmt.Errorf("decode claimed developer pending tool: %w", err)
	}
	result.SessionID = sessionID
	result.CreatedAt = row.CreatedAt.UTC().Format(time.RFC3339)
	return result, nil
}

func (p *Postgres) ResolveDeveloperPendingTool(ctx context.Context, sessionID string, content []service.ContentBlock) (*service.DeveloperSessionMessage, error) {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(content)
	if err != nil {
		return nil, fmt.Errorf("encode developer tool result: %w", err)
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin developer tool resolution: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	var pendingID string
	found, err := tx.From(p.tableDeveloperPendingTools).Select("session_id").Where(
		developerOwned(actor, goqu.Ex{"session_id": sessionID}),
		goqu.L("tool_call->>'state' = 'executing'"),
	).ForUpdate(goqu.Wait).ScanValContext(ctx, &pendingID)
	if err != nil {
		return nil, fmt.Errorf("lock developer pending tool: %w", err)
	}
	if !found {
		return nil, service.ErrDeveloperSessionBusy
	}
	row := developerSessionMessageRow{}
	messageID := ulid.Make().String()
	_, err = tx.Insert(p.tableDeveloperSessionMessages).Rows(goqu.Record{
		"id": messageID, "session_id": sessionID, "workspace_id": actor.WorkspaceID,
		"owner_user_id": actor.UserID, "role": "tool", "content": string(encoded),
	}).Returning(developerSessionMessageColumns...).Executor().ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("append resolved developer tool result: %w", err)
	}
	if _, err := tx.Delete(p.tableDeveloperPendingTools).Where(developerOwned(actor, goqu.Ex{"session_id": sessionID})).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("delete resolved developer pending tool: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit developer tool resolution: %w", err)
	}
	return developerSessionMessageRecord(row)
}

func (p *Postgres) deleteDeveloperResource(ctx context.Context, table exp.IdentifierExpression, id, kind string) error {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return err
	}
	result, err := p.goqu.Delete(table).Where(developerOwned(actor, goqu.Ex{"id": id})).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("delete developer %s: %w", kind, err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return service.ErrDeveloperSpaceNotFound
	}
	return nil
}
