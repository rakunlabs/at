package service

import "context"

// Storage is the installation-wide durable object backend used by AT. Media
// attachments are its first consumer; Files, assets and developer-space
// snapshots can adopt the same policy without learning backend credentials.
//
// Settings aliases retain one backend configuration shape while object
// metadata is unified separately by StoredObject and its namespace.
const (
	StorageBackendDisabled   = MediaBackendDisabled
	StorageBackendFilesystem = MediaBackendFilesystem
	StorageBackendS3         = MediaBackendS3
	StorageS3DefaultRegion   = MediaS3DefaultRegion
)

var (
	ErrStorageNotFound = ErrMediaNotFound
	ErrStorageConflict = ErrMediaConflict
)

type StorageSettings = MediaSettings
type StorageFilesystemSettings = MediaFilesystemSettings
type StorageS3Settings = MediaS3Settings

func DefaultStorageSettings() StorageSettings { return DefaultMediaSettings() }

func NormalizeStoragePrefix(prefix string) string { return NormalizeMediaPrefix(prefix) }

// StorageSettingsStorer is the backend-neutral configuration seam. It is
// intentionally separate from MediaStorer: reading storage policy must not
// grant access to owner-scoped chat attachments.
type StorageSettingsStorer interface {
	GetStorageSettings(ctx context.Context) (*StorageSettings, error)
	SaveStorageSettings(ctx context.Context, settings StorageSettings) (*StorageSettings, error)
}
