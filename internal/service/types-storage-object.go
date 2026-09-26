package service

import (
	"context"
	"errors"
)

const (
	StorageNamespaceFiles     = "files"
	StorageNamespaceMedia     = "media"
	StorageNamespaceAssets    = "assets"
	StorageNamespaceSnapshots = "snapshots"
)

var ErrStorageObjectNotFound = errors.New("storage object not found")

// StoredObject is one durable blob addressed by a logical, workspace-scoped
// path. StorageKey is backend-internal; Path is the stable user-facing name.
type StoredObject struct {
	ID          string `json:"id" db:"id"`
	WorkspaceID string `json:"workspace_id" db:"workspace_id"`
	OwnerUserID string `json:"owner_user_id,omitempty" db:"owner_user_id"`
	Namespace   string `json:"namespace" db:"namespace"`
	Path        string `json:"path" db:"path"`
	Backend     string `json:"backend" db:"backend"`
	StorageKey  string `json:"storage_key" db:"storage_key"`
	ContentType string `json:"content_type" db:"content_type"`
	SizeBytes   int64  `json:"size_bytes" db:"size_bytes"`
	Checksum    string `json:"checksum" db:"checksum"`
	CreatedAt   string `json:"created_at" db:"created_at"`
	UpdatedAt   string `json:"updated_at" db:"updated_at"`
}

// StorageObjectStorer owns metadata only. Blob bytes remain behind blob.Store,
// so database listing never exposes filesystem roots or S3 credentials.
type StorageObjectStorer interface {
	PutStorageObject(ctx context.Context, object StoredObject) (*StoredObject, error)
	GetStorageObject(ctx context.Context, workspace, namespace, path string) (*StoredObject, error)
	ListStorageObjects(ctx context.Context, workspace, namespace, prefix string) ([]StoredObject, error)
	DeleteStorageObject(ctx context.Context, workspace, namespace, path string) (*StoredObject, error)
}
