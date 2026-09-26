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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.DeveloperSpaceStorer = (*Postgres)(nil)

var developerSpaceColumns = []any{
	"id", "workspace_id", "owner_user_id", "name", "status", "image",
	"cpu_limit", "memory_limit", "disk_limit_bytes", "config", "error",
	"last_active_at", "created_at", "updated_at",
}

type developerSpaceRow struct {
	ID             string       `db:"id"`
	WorkspaceID    string       `db:"workspace_id"`
	OwnerUserID    string       `db:"owner_user_id"`
	Name           string       `db:"name"`
	Status         string       `db:"status"`
	Image          string       `db:"image"`
	CPULimit       string       `db:"cpu_limit"`
	MemoryLimit    string       `db:"memory_limit"`
	DiskLimitBytes int64        `db:"disk_limit_bytes"`
	Config         string       `db:"config"`
	Error          string       `db:"error"`
	LastActiveAt   sql.NullTime `db:"last_active_at"`
	CreatedAt      time.Time    `db:"created_at"`
	UpdatedAt      time.Time    `db:"updated_at"`
}

func developerSpaceActor(ctx context.Context) (service.AccessPrincipal, error) {
	actor, ok := service.AccessPrincipalFromContext(ctx)
	if !ok || actor.UserID == "" || actor.WorkspaceID == "" {
		return service.AccessPrincipal{}, service.ErrAccessDenied
	}
	return actor, nil
}

func developerSpaceRecord(row developerSpaceRow) (*service.DeveloperSpace, error) {
	record := &service.DeveloperSpace{
		ID: row.ID, WorkspaceID: row.WorkspaceID, OwnerUserID: row.OwnerUserID,
		Name: row.Name, Status: row.Status, Image: row.Image, CPULimit: row.CPULimit,
		MemoryLimit: row.MemoryLimit, DiskLimitBytes: row.DiskLimitBytes, Error: row.Error,
		CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if row.LastActiveAt.Valid {
		record.LastActiveAt = row.LastActiveAt.Time.UTC().Format(time.RFC3339)
	}
	if err := json.Unmarshal([]byte(row.Config), &record.Config); err != nil {
		return nil, fmt.Errorf("decode developer space config: %w", err)
	}
	return record, nil
}

func developerSpaceStoreError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return service.ErrDeveloperSpaceConflict
	}
	return err
}

func (p *Postgres) ListDeveloperSpaces(ctx context.Context) ([]service.DeveloperSpace, error) {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
	var rows []developerSpaceRow
	if err := p.goqu.From(p.tableDeveloperSpaces).Select(developerSpaceColumns...).
		Where(goqu.Ex{"workspace_id": actor.WorkspaceID, "owner_user_id": actor.UserID}).
		Order(goqu.I("updated_at").Desc(), goqu.I("id").Desc()).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list developer spaces: %w", err)
	}
	result := make([]service.DeveloperSpace, 0, len(rows))
	for _, row := range rows {
		record, err := developerSpaceRecord(row)
		if err != nil {
			return nil, err
		}
		result = append(result, *record)
	}
	return result, nil
}

func (p *Postgres) GetDeveloperSpace(ctx context.Context, id string) (*service.DeveloperSpace, error) {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
	var row developerSpaceRow
	found, err := p.goqu.From(p.tableDeveloperSpaces).Select(developerSpaceColumns...).
		Where(goqu.Ex{"id": id, "workspace_id": actor.WorkspaceID, "owner_user_id": actor.UserID}).
		ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("get developer space: %w", err)
	}
	if !found {
		return nil, service.ErrDeveloperSpaceNotFound
	}
	return developerSpaceRecord(row)
}

func (p *Postgres) CreateDeveloperSpace(ctx context.Context, space service.DeveloperSpace) (*service.DeveloperSpace, error) {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
	space.Name = strings.TrimSpace(space.Name)
	if err := space.Validate(); err != nil {
		return nil, err
	}
	config := space.Config
	if config.Plan.SystemPrompt == "" && config.Build.SystemPrompt == "" && config.Review.SystemPrompt == "" &&
		config.Plan.MaxIterations == 0 && config.Build.MaxIterations == 0 && config.Review.MaxIterations == 0 &&
		config.Plan.ToolTimeoutSeconds == 0 && config.Build.ToolTimeoutSeconds == 0 && config.Review.ToolTimeoutSeconds == 0 &&
		len(config.Plan.Rules) == 0 && len(config.Build.Rules) == 0 && len(config.Review.Rules) == 0 {
		config = service.DefaultDeveloperSpaceConfig()
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode developer space config: %w", err)
	}
	if space.ID == "" {
		space.ID = ulid.Make().String()
	}
	space.Status = service.DeveloperSpacePending
	var row developerSpaceRow
	_, err = p.goqu.Insert(p.tableDeveloperSpaces).Rows(goqu.Record{
		"id": space.ID, "workspace_id": actor.WorkspaceID, "owner_user_id": actor.UserID,
		"name": space.Name, "status": space.Status, "image": strings.TrimSpace(space.Image),
		"cpu_limit": strings.TrimSpace(space.CPULimit), "memory_limit": strings.TrimSpace(space.MemoryLimit),
		"disk_limit_bytes": space.DiskLimitBytes, "config": string(encoded), "error": space.Error,
	}).Returning(developerSpaceColumns...).Executor().ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("create developer space: %w", developerSpaceStoreError(err))
	}
	return developerSpaceRecord(row)
}

func (p *Postgres) UpdateDeveloperSpace(ctx context.Context, space service.DeveloperSpace) (*service.DeveloperSpace, error) {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
	space.Name = strings.TrimSpace(space.Name)
	if space.ID == "" {
		return nil, service.ErrDeveloperSpaceNotFound
	}
	if err := space.Validate(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(space.Config)
	if err != nil {
		return nil, fmt.Errorf("encode developer space config: %w", err)
	}
	var row developerSpaceRow
	found, err := p.goqu.Update(p.tableDeveloperSpaces).Set(goqu.Record{
		"name": space.Name, "image": strings.TrimSpace(space.Image), "cpu_limit": strings.TrimSpace(space.CPULimit),
		"memory_limit": strings.TrimSpace(space.MemoryLimit), "disk_limit_bytes": space.DiskLimitBytes,
		"config": string(encoded), "updated_at": goqu.L("clock_timestamp()"),
	}).Where(goqu.Ex{"id": space.ID, "workspace_id": actor.WorkspaceID, "owner_user_id": actor.UserID}).
		Returning(developerSpaceColumns...).Executor().ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("update developer space: %w", developerSpaceStoreError(err))
	}
	if !found {
		return nil, service.ErrDeveloperSpaceNotFound
	}
	return developerSpaceRecord(row)
}

func (p *Postgres) DeleteDeveloperSpace(ctx context.Context, id string) error {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return err
	}
	result, err := p.goqu.Delete(p.tableDeveloperSpaces).
		Where(goqu.Ex{"id": id, "workspace_id": actor.WorkspaceID, "owner_user_id": actor.UserID}).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("delete developer space: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return service.ErrDeveloperSpaceNotFound
	}
	return nil
}

func (p *Postgres) SetDeveloperSpaceRuntime(ctx context.Context, id, status, runtimeError string) (*service.DeveloperSpace, error) {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
	if status != service.DeveloperSpacePending && status != service.DeveloperSpaceReady && status != service.DeveloperSpaceStopped && status != service.DeveloperSpaceError {
		return nil, errors.New("invalid developer space status")
	}
	set := goqu.Record{"status": status, "error": runtimeError, "updated_at": goqu.L("clock_timestamp()")}
	if status == service.DeveloperSpaceReady {
		set["last_active_at"] = goqu.L("clock_timestamp()")
	}
	var row developerSpaceRow
	found, err := p.goqu.Update(p.tableDeveloperSpaces).Set(set).
		Where(goqu.Ex{"id": id, "workspace_id": actor.WorkspaceID, "owner_user_id": actor.UserID}).
		Returning(developerSpaceColumns...).Executor().ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("set developer space runtime: %w", err)
	}
	if !found {
		return nil, service.ErrDeveloperSpaceNotFound
	}
	return developerSpaceRecord(row)
}
