package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

type workspaceChatCommandRow struct {
	ID          string    `db:"id"`
	WorkspaceID string    `db:"workspace_id"`
	OwnerUserID string    `db:"owner_user_id"`
	Name        string    `db:"name"`
	Description string    `db:"description"`
	Template    string    `db:"template"`
	Model       string    `db:"model"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

var _ service.WorkspaceChatCommandStorer = (*Postgres)(nil)

var workspaceChatCommandColumns = []interface{}{
	"id", "workspace_id", "owner_user_id", "name", "description", "template", "model", "created_at", "updated_at",
}

func scanWorkspaceChatCommand(scanner interface{ Scan(...interface{}) error }, row *workspaceChatCommandRow) error {
	return scanner.Scan(&row.ID, &row.WorkspaceID, &row.OwnerUserID, &row.Name, &row.Description, &row.Template, &row.Model, &row.CreatedAt, &row.UpdatedAt)
}

func workspaceChatCommandRecord(row workspaceChatCommandRow, actor string) *service.WorkspaceChatCommand {
	return &service.WorkspaceChatCommand{
		ChatCommand: service.ChatCommand{
			ID: row.ID, Name: row.Name, Description: row.Description, Template: row.Template, Model: row.Model,
			CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339),
		},
		WorkspaceID: row.WorkspaceID,
		OwnerUserID: row.OwnerUserID,
		CanEdit:     row.OwnerUserID == actor,
	}
}

func (p *Postgres) workspaceChatCommandActor(ctx context.Context, workspace string) (service.AccessPrincipal, error) {
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

func workspaceChatCommandStoreError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return service.ErrChatCommandNameConflict
	}

	return err
}

func (p *Postgres) ListWorkspaceChatCommands(ctx context.Context, workspaceID string) ([]service.WorkspaceChatCommand, error) {
	actor, err := p.workspaceChatCommandActor(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	sqlStr, _, err := p.goqu.From(p.tableWorkspaceChatCommands).
		Select(workspaceChatCommandColumns...).
		Where(goqu.I("workspace_id").Eq(workspaceID)).
		Order(goqu.I("name").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list workspace chat commands query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sqlStr)
	if err != nil {
		return nil, fmt.Errorf("list workspace chat commands: %w", err)
	}
	defer rows.Close()

	items := []service.WorkspaceChatCommand{}
	for rows.Next() {
		var row workspaceChatCommandRow
		if err := scanWorkspaceChatCommand(rows, &row); err != nil {
			return nil, fmt.Errorf("scan workspace chat command: %w", err)
		}
		items = append(items, *workspaceChatCommandRecord(row, actor.UserID))
	}

	return items, rows.Err()
}

func (p *Postgres) CreateWorkspaceChatCommand(ctx context.Context, command service.WorkspaceChatCommand) (*service.WorkspaceChatCommand, error) {
	actor, err := p.workspaceChatCommandActor(ctx, command.WorkspaceID)
	if err != nil {
		return nil, err
	}

	id := ulid.Make().String()
	sqlStr, _, err := p.goqu.Insert(p.tableWorkspaceChatCommands).Rows(goqu.Record{
		"id": id, "workspace_id": actor.WorkspaceID, "owner_user_id": actor.UserID,
		"name": command.Name, "description": command.Description, "template": command.Template, "model": command.Model,
	}).Returning(workspaceChatCommandColumns...).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build create workspace chat command query: %w", err)
	}

	var row workspaceChatCommandRow
	if err := scanWorkspaceChatCommand(p.db.QueryRowContext(ctx, sqlStr), &row); err != nil {
		return nil, fmt.Errorf("create workspace chat command: %w", workspaceChatCommandStoreError(err))
	}

	return workspaceChatCommandRecord(row, actor.UserID), nil
}

func (p *Postgres) UpdateWorkspaceChatCommand(ctx context.Context, id string, command service.WorkspaceChatCommand) (*service.WorkspaceChatCommand, error) {
	actor, err := p.workspaceChatCommandActor(ctx, command.WorkspaceID)
	if err != nil {
		return nil, err
	}

	sqlStr, _, err := p.goqu.Update(p.tableWorkspaceChatCommands).Set(goqu.Record{
		"name": command.Name, "description": command.Description, "template": command.Template, "model": command.Model, "updated_at": goqu.L("clock_timestamp()"),
	}).Where(
		goqu.I("id").Eq(id), goqu.I("workspace_id").Eq(actor.WorkspaceID), goqu.I("owner_user_id").Eq(actor.UserID),
	).Returning(workspaceChatCommandColumns...).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update workspace chat command query: %w", err)
	}

	var row workspaceChatCommandRow
	err = scanWorkspaceChatCommand(p.db.QueryRowContext(ctx, sqlStr), &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("update workspace chat command: %w", workspaceChatCommandStoreError(err))
	}

	return workspaceChatCommandRecord(row, actor.UserID), nil
}

func (p *Postgres) DeleteWorkspaceChatCommand(ctx context.Context, id string) error {
	actor, err := p.workspaceChatCommandActor(ctx, "")
	if err != nil {
		return err
	}
	sqlStr, _, err := p.goqu.Delete(p.tableWorkspaceChatCommands).Where(
		goqu.I("id").Eq(id), goqu.I("workspace_id").Eq(actor.WorkspaceID), goqu.I("owner_user_id").Eq(actor.UserID),
	).ToSQL()
	if err != nil {
		return fmt.Errorf("build delete workspace chat command query: %w", err)
	}
	result, err := p.db.ExecContext(ctx, sqlStr)
	if err != nil {
		return fmt.Errorf("delete workspace chat command: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("read deleted workspace chat command count: %w", err)
	} else if count == 0 {
		return sql.ErrNoRows
	}

	return nil
}
