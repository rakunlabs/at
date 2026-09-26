package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.StorageObjectStorer = (*Postgres)(nil)

var storageObjectColumns = []any{"id", "workspace_id", "owner_user_id", "namespace", "path", "backend", "storage_key", "content_type", "size_bytes", "checksum", "created_at", "updated_at"}

type storageObjectRow struct {
	ID          string    `db:"id"`
	WorkspaceID string    `db:"workspace_id"`
	OwnerUserID string    `db:"owner_user_id"`
	Namespace   string    `db:"namespace"`
	Path        string    `db:"path"`
	Backend     string    `db:"backend"`
	StorageKey  string    `db:"storage_key"`
	ContentType string    `db:"content_type"`
	SizeBytes   int64     `db:"size_bytes"`
	Checksum    string    `db:"checksum"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

func storageObjectRecord(row storageObjectRow) service.StoredObject {
	return service.StoredObject{
		ID: row.ID, WorkspaceID: row.WorkspaceID, OwnerUserID: row.OwnerUserID,
		Namespace: row.Namespace, Path: row.Path, Backend: row.Backend,
		StorageKey: row.StorageKey, ContentType: row.ContentType,
		SizeBytes: row.SizeBytes, Checksum: row.Checksum,
		CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func (p *Postgres) PutStorageObject(ctx context.Context, object service.StoredObject) (*service.StoredObject, error) {
	if object.WorkspaceID == "" || object.Namespace == "" || object.Path == "" || object.StorageKey == "" {
		return nil, service.ErrStorageObjectNotFound
	}
	if object.ID == "" {
		object.ID = ulid.Make().String()
	}
	record := goqu.Record{
		"id": object.ID, "workspace_id": object.WorkspaceID, "owner_user_id": object.OwnerUserID,
		"namespace": object.Namespace, "path": object.Path, "backend": object.Backend,
		"storage_key": object.StorageKey, "content_type": object.ContentType,
		"size_bytes": object.SizeBytes, "checksum": object.Checksum,
		"created_at": goqu.L("clock_timestamp()"), "updated_at": goqu.L("clock_timestamp()"),
	}
	update := goqu.Record{
		"owner_user_id": object.OwnerUserID, "backend": object.Backend,
		"storage_key": object.StorageKey, "content_type": object.ContentType,
		"size_bytes": object.SizeBytes, "checksum": object.Checksum,
		"updated_at": goqu.L("clock_timestamp()"),
	}
	var row storageObjectRow
	_, err := p.goqu.Insert(p.tableStorageObjects).Rows(record).
		OnConflict(goqu.DoUpdate("workspace_id,namespace,path", update)).
		Returning(storageObjectColumns...).Executor().ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("put storage object: %w", err)
	}
	out := storageObjectRecord(row)
	return &out, nil
}

func (p *Postgres) GetStorageObject(ctx context.Context, workspace, namespace, path string) (*service.StoredObject, error) {
	var row storageObjectRow
	found, err := p.goqu.From(p.tableStorageObjects).Select(storageObjectColumns...).Where(goqu.Ex{"workspace_id": workspace, "namespace": namespace, "path": path}).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("get storage object: %w", err)
	}
	if !found {
		return nil, service.ErrStorageObjectNotFound
	}
	out := storageObjectRecord(row)
	return &out, nil
}

func (p *Postgres) ListStorageObjects(ctx context.Context, workspace, namespace, prefix string) ([]service.StoredObject, error) {
	query := p.goqu.From(p.tableStorageObjects).Select(storageObjectColumns...).Where(goqu.Ex{"workspace_id": workspace, "namespace": namespace})
	if prefix != "" {
		query = query.Where(goqu.L("starts_with(path, ?)", prefix))
	}
	var rows []storageObjectRow
	if err := query.Order(goqu.I("path").Asc()).Limit(10000).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list storage objects: %w", err)
	}
	out := make([]service.StoredObject, 0, len(rows))
	for _, row := range rows {
		out = append(out, storageObjectRecord(row))
	}
	return out, nil
}

func (p *Postgres) DeleteStorageObject(ctx context.Context, workspace, namespace, path string) (*service.StoredObject, error) {
	var row storageObjectRow
	found, err := p.goqu.Delete(p.tableStorageObjects).Where(goqu.Ex{"workspace_id": workspace, "namespace": namespace, "path": path}).Returning(storageObjectColumns...).Executor().ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("delete storage object: %w", err)
	}
	if !found {
		return nil, service.ErrStorageObjectNotFound
	}
	out := storageObjectRecord(row)
	return &out, nil
}
