package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

type workspaceChatPresetRow struct {
	ID          string    `db:"id"`
	WorkspaceID string    `db:"workspace_id"`
	OwnerUserID string    `db:"owner_user_id"`
	Name        string    `db:"name"`
	Setup       string    `db:"setup"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

var _ service.WorkspaceChatPresetStorer = (*Postgres)(nil)

var workspaceChatPresetColumns = []interface{}{
	"id", "workspace_id", "owner_user_id", "name", "setup", "created_at", "updated_at",
}

func scanWorkspaceChatPreset(scanner interface{ Scan(...interface{}) error }, row *workspaceChatPresetRow) error {
	return scanner.Scan(&row.ID, &row.WorkspaceID, &row.OwnerUserID, &row.Name, &row.Setup, &row.CreatedAt, &row.UpdatedAt)
}

func workspaceChatPresetRecord(row workspaceChatPresetRow, actor string) (*service.WorkspaceChatPreset, error) {
	var setup service.ChatWorkbenchSetup
	if err := json.Unmarshal([]byte(row.Setup), &setup); err != nil {
		return nil, fmt.Errorf("decode workspace chat preset setup: %w", err)
	}

	return &service.WorkspaceChatPreset{
		ChatPreset: service.ChatPreset{
			ID: row.ID, Name: row.Name, ChatWorkbenchSetup: setup,
			CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339),
		},
		WorkspaceID: row.WorkspaceID,
		OwnerUserID: row.OwnerUserID,
		CanEdit:     row.OwnerUserID == actor,
	}, nil
}

func (p *Postgres) workspaceChatPresetActor(ctx context.Context, workspace string) (service.AccessPrincipal, error) {
	actor, err := p.businessPrincipal(ctx)
	if err != nil {
		return service.AccessPrincipal{}, err
	}
	if actor.UserID == "" || actor.WorkspaceID == "" || (workspace != "" && actor.WorkspaceID != workspace) {
		return service.AccessPrincipal{}, service.ErrAccessDenied
	}
	if !actor.PlatformAdmin && !actor.Allows("models.use", service.AccessResource{WorkspaceID: actor.WorkspaceID}) {
		return service.AccessPrincipal{}, service.ErrAccessDenied
	}

	return actor, nil
}

func workspaceChatPresetStoreError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return service.ErrChatPresetNameConflict
	}

	return err
}

func (p *Postgres) ListWorkspaceChatPresets(ctx context.Context, workspaceID string) ([]service.WorkspaceChatPreset, error) {
	actor, err := p.workspaceChatPresetActor(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	sqlStr, _, err := p.goqu.From(p.tableWorkspaceChatPresets).
		Select(workspaceChatPresetColumns...).
		Where(goqu.I("workspace_id").Eq(workspaceID)).
		Order(goqu.L("lower(name)").Asc(), goqu.I("created_at").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list workspace chat presets query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sqlStr)
	if err != nil {
		return nil, fmt.Errorf("list workspace chat presets: %w", err)
	}
	defer rows.Close()

	items := []service.WorkspaceChatPreset{}
	for rows.Next() {
		var row workspaceChatPresetRow
		if err := scanWorkspaceChatPreset(rows, &row); err != nil {
			return nil, fmt.Errorf("scan workspace chat preset: %w", err)
		}
		record, err := workspaceChatPresetRecord(row, actor.UserID)
		if err != nil {
			return nil, err
		}
		items = append(items, *record)
	}

	return items, rows.Err()
}

func (p *Postgres) CreateWorkspaceChatPreset(ctx context.Context, preset service.WorkspaceChatPreset) (*service.WorkspaceChatPreset, error) {
	actor, err := p.workspaceChatPresetActor(ctx, preset.WorkspaceID)
	if err != nil {
		return nil, err
	}
	setup, err := json.Marshal(preset.ChatWorkbenchSetup)
	if err != nil {
		return nil, fmt.Errorf("encode workspace chat preset setup: %w", err)
	}

	id := ulid.Make().String()
	sqlStr, _, err := p.goqu.Insert(p.tableWorkspaceChatPresets).Rows(goqu.Record{
		"id": id, "workspace_id": actor.WorkspaceID, "owner_user_id": actor.UserID,
		"name": preset.Name, "setup": string(setup),
	}).Returning(workspaceChatPresetColumns...).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build create workspace chat preset query: %w", err)
	}

	var row workspaceChatPresetRow
	if err := scanWorkspaceChatPreset(p.db.QueryRowContext(ctx, sqlStr), &row); err != nil {
		return nil, fmt.Errorf("create workspace chat preset: %w", workspaceChatPresetStoreError(err))
	}

	return workspaceChatPresetRecord(row, actor.UserID)
}

func (p *Postgres) UpdateWorkspaceChatPreset(ctx context.Context, id string, preset service.WorkspaceChatPreset) (*service.WorkspaceChatPreset, error) {
	actor, err := p.workspaceChatPresetActor(ctx, preset.WorkspaceID)
	if err != nil {
		return nil, err
	}
	setup, err := json.Marshal(preset.ChatWorkbenchSetup)
	if err != nil {
		return nil, fmt.Errorf("encode workspace chat preset setup: %w", err)
	}

	sqlStr, _, err := p.goqu.Update(p.tableWorkspaceChatPresets).Set(goqu.Record{
		"name": preset.Name, "setup": string(setup), "updated_at": goqu.L("clock_timestamp()"),
	}).Where(
		goqu.I("id").Eq(id), goqu.I("workspace_id").Eq(actor.WorkspaceID), goqu.I("owner_user_id").Eq(actor.UserID),
	).Returning(workspaceChatPresetColumns...).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update workspace chat preset query: %w", err)
	}

	var row workspaceChatPresetRow
	err = scanWorkspaceChatPreset(p.db.QueryRowContext(ctx, sqlStr), &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("update workspace chat preset: %w", workspaceChatPresetStoreError(err))
	}

	return workspaceChatPresetRecord(row, actor.UserID)
}

func (p *Postgres) DeleteWorkspaceChatPreset(ctx context.Context, id string) error {
	actor, err := p.workspaceChatPresetActor(ctx, "")
	if err != nil {
		return err
	}
	sqlStr, _, err := p.goqu.Delete(p.tableWorkspaceChatPresets).Where(
		goqu.I("id").Eq(id), goqu.I("workspace_id").Eq(actor.WorkspaceID), goqu.I("owner_user_id").Eq(actor.UserID),
	).ToSQL()
	if err != nil {
		return fmt.Errorf("build delete workspace chat preset query: %w", err)
	}
	result, err := p.db.ExecContext(ctx, sqlStr)
	if err != nil {
		return fmt.Errorf("delete workspace chat preset: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("read deleted workspace chat preset count: %w", err)
	} else if count == 0 {
		return sql.ErrNoRows
	}

	return nil
}
