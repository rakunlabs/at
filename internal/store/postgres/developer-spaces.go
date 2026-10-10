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

var _ service.DeveloperSpaceStorer = (*Postgres)(nil)

var developerSpaceColumns = []any{
	"id", "workspace_id", "owner_user_id", "name", "status", "image",
	"cpu_limit", "memory_limit", "disk_limit_bytes", "config", "error",
	"last_active_at", "created_at", "updated_at", "execution_suspended", "active_control_id",
}

type developerSpaceRow struct {
	ExecutionSuspended bool         `db:"execution_suspended"`
	ActiveControlID    string       `db:"active_control_id"`
	ID                 string       `db:"id"`
	WorkspaceID        string       `db:"workspace_id"`
	OwnerUserID        string       `db:"owner_user_id"`
	Name               string       `db:"name"`
	Status             string       `db:"status"`
	Image              string       `db:"image"`
	CPULimit           string       `db:"cpu_limit"`
	MemoryLimit        string       `db:"memory_limit"`
	DiskLimitBytes     int64        `db:"disk_limit_bytes"`
	Config             string       `db:"config"`
	Error              string       `db:"error"`
	LastActiveAt       sql.NullTime `db:"last_active_at"`
	CreatedAt          time.Time    `db:"created_at"`
	UpdatedAt          time.Time    `db:"updated_at"`
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
		ExecutionSuspended: row.ExecutionSuspended,
		ActiveControlID:    row.ActiveControlID,
		ID:                 row.ID, WorkspaceID: row.WorkspaceID, OwnerUserID: row.OwnerUserID,
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

// EnsureDeveloperSpace returns the caller's space, creating it on first use.
// The unique (workspace, owner) index makes concurrent first requests converge
// on one row instead of racing into duplicates.
func (p *Postgres) EnsureDeveloperSpace(ctx context.Context) (*service.DeveloperSpace, error) {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(service.DefaultDeveloperSpaceConfig())
	if err != nil {
		return nil, fmt.Errorf("encode developer space config: %w", err)
	}
	if _, err := p.goqu.Insert(p.tableDeveloperSpaces).Rows(goqu.Record{
		"id": ulid.Make().String(), "workspace_id": actor.WorkspaceID, "owner_user_id": actor.UserID,
		"name": "Workspace", "status": service.DeveloperSpacePending, "config": string(encoded),
	}).OnConflict(goqu.DoNothing()).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("create developer space: %w", err)
	}
	var row developerSpaceRow
	found, err := p.goqu.From(p.tableDeveloperSpaces).Select(developerSpaceColumns...).
		Where(goqu.Ex{"workspace_id": actor.WorkspaceID, "owner_user_id": actor.UserID}).
		ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("get developer space: %w", err)
	}
	if !found {
		return nil, service.ErrDeveloperSpaceNotFound
	}
	return developerSpaceRecord(row)
}

func (p *Postgres) UpdateDeveloperSpace(ctx context.Context, space service.DeveloperSpace) (*service.DeveloperSpace, error) {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
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
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin developer space update: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := p.lockDeveloperSpace(ctx, tx, actor, space.ID); err != nil {
		return nil, err
	}
	if err := p.checkDeveloperSpaceRuns(ctx, tx, actor, space.ID); err != nil {
		return nil, err
	}
	var row developerSpaceRow
	found, err := tx.Update(p.tableDeveloperSpaces).Set(goqu.Record{
		"image": strings.TrimSpace(space.Image), "cpu_limit": strings.TrimSpace(space.CPULimit),
		"memory_limit": strings.TrimSpace(space.MemoryLimit), "disk_limit_bytes": space.DiskLimitBytes,
		"config": string(encoded), "updated_at": goqu.L("clock_timestamp()"),
	}).Where(goqu.Ex{"id": space.ID, "workspace_id": actor.WorkspaceID, "owner_user_id": actor.UserID}).
		Returning(developerSpaceColumns...).Executor().ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("update developer space: %w", err)
	}
	if !found {
		return nil, service.ErrDeveloperSpaceNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit developer space update: %w", err)
	}
	return developerSpaceRecord(row)
}

func (p *Postgres) DeleteDeveloperSpace(ctx context.Context, id string) error {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return err
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin developer space deletion: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := p.lockDeveloperSpace(ctx, tx, actor, id); err != nil {
		return err
	}
	if err := p.checkDeveloperSpaceRuns(ctx, tx, actor, id); err != nil {
		return err
	}
	result, err := tx.Delete(p.tableDeveloperSpaces).
		Where(goqu.Ex{"id": id, "workspace_id": actor.WorkspaceID, "owner_user_id": actor.UserID}).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("delete developer space: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return service.ErrDeveloperSpaceNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit developer space deletion: %w", err)
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
