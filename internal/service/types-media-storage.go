package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// Media storage backends. The empty string is "disabled" so a zero-value
// MediaSettings is a disabled installation: no upload endpoint works until an
// administrator picks a backend explicitly.
const (
	MediaBackendDisabled   = ""
	MediaBackendFilesystem = "filesystem"
	MediaBackendS3         = "s3"
)

var (
	// ErrMediaNotFound is returned for unknown objects and for objects owned
	// by another user. The two cases are deliberately indistinguishable so
	// ownership cannot be probed.
	ErrMediaNotFound = errors.New("media object not found")
	// ErrMediaConflict is returned when a settings write loses the
	// optimistic-concurrency race on version.
	ErrMediaConflict = errors.New("media settings conflict")
)

// MediaSettings is a versioned installation policy for media storage, never a
// per-request value. Version is monotonic and is the optimistic-concurrency
// token: a writer submits the version it read and the store accepts the write
// only while that is still current.
type MediaSettings struct {
	Version    int64                   `json:"version"`
	Backend    string                  `json:"backend"`
	Filesystem MediaFilesystemSettings `json:"filesystem"`
	S3         MediaS3Settings         `json:"s3"`
}

// MediaFilesystemSettings stores blobs on the local filesystem.
type MediaFilesystemSettings struct {
	// Root must be absolute. A relative root would resolve against the
	// process working directory, which silently differs between a dev run,
	// a systemd unit and a container, so it is rejected outright.
	Root string `json:"root"`
}

// MediaS3Settings addresses any S3-compatible object store. Requests are
// signed with hand-rolled AWS SigV4 (see internal/service/blob/s3.go); the
// repository deliberately carries no cloud SDK.
type MediaS3Settings struct {
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	Prefix          string `json:"prefix"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	// UsePathStyle addresses the bucket as https://host/bucket/key instead of
	// https://bucket.host/key. MinIO, Ceph RGW and Cloudflare R2 need it.
	UsePathStyle bool `json:"use_path_style"`
}

// MediaObject records one stored blob. ContentType and Checksum are derived
// from the bytes the server actually received, never from client claims.
type MediaObject struct {
	ID          string `json:"id" db:"id"`
	OwnerUserID string `json:"owner_user_id" db:"owner_user_id"`
	Backend     string `json:"backend" db:"backend"`
	StorageKey  string `json:"storage_key" db:"storage_key"`
	ContentType string `json:"content_type" db:"content_type"`
	SizeBytes   int64  `json:"size_bytes" db:"size_bytes"`
	Checksum    string `json:"checksum" db:"checksum"`
	CreatedAt   string `json:"created_at" db:"created_at"`
}

// DefaultMediaSettings is the state of an installation that never configured
// media storage: disabled, version 1.
func DefaultMediaSettings() MediaSettings {
	return MediaSettings{Version: 1, Backend: MediaBackendDisabled}
}

// Enabled reports whether a backend is selected.
func (s MediaSettings) Enabled() bool { return s.Backend != MediaBackendDisabled }

// Validate checks administrator-supplied configuration strictly: a
// half-configured backend must fail at the settings endpoint, not later on a
// user's upload.
func (s MediaSettings) Validate() error {
	if s.Version < 1 {
		return fmt.Errorf("media settings version must be positive")
	}
	switch s.Backend {
	case MediaBackendDisabled:
		// Every other field is ignored while storage is off, so stale
		// leftovers from a previous backend are not an error.
		return nil
	case MediaBackendFilesystem:
		root := strings.TrimSpace(s.Filesystem.Root)
		if root == "" {
			return fmt.Errorf("filesystem media storage requires a root directory")
		}
		if !filepath.IsAbs(root) {
			return fmt.Errorf("filesystem media storage root must be an absolute path")
		}
		return nil
	case MediaBackendS3:
		if strings.TrimSpace(s.S3.Bucket) == "" {
			return fmt.Errorf("s3 media storage requires a bucket")
		}
		if err := validateMediaS3Endpoint(s.S3.Endpoint); err != nil {
			return err
		}
		if strings.TrimSpace(s.S3.Region) == "" {
			return fmt.Errorf("s3 media storage requires a region")
		}
		if strings.TrimSpace(s.S3.AccessKeyID) == "" || strings.TrimSpace(s.S3.SecretAccessKey) == "" {
			return fmt.Errorf("s3 media storage requires an access key id and secret access key")
		}
		return nil
	default:
		return fmt.Errorf("media storage backend must be one of filesystem, s3 or empty")
	}
}

func validateMediaS3Endpoint(endpoint string) error {
	raw := strings.TrimSpace(endpoint)
	if raw == "" {
		return fmt.Errorf("s3 media storage requires an endpoint")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("s3 media storage endpoint must be an absolute http(s) URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("s3 media storage endpoint must use http or https")
	}
	if strings.Trim(u.Path, "/") != "" {
		return fmt.Errorf("s3 media storage endpoint must not contain a path")
	}
	return nil
}

// Normalized returns a copy with whitespace trimmed and the S3 prefix in its
// canonical form: no leading slash, and either empty or exactly one trailing
// slash so a key can be appended directly.
func (s MediaSettings) Normalized() MediaSettings {
	out := s
	out.Backend = strings.TrimSpace(out.Backend)
	out.Filesystem.Root = strings.TrimSpace(out.Filesystem.Root)
	out.S3.Endpoint = strings.TrimRight(strings.TrimSpace(out.S3.Endpoint), "/")
	out.S3.Region = strings.TrimSpace(out.S3.Region)
	out.S3.Bucket = strings.TrimSpace(out.S3.Bucket)
	out.S3.AccessKeyID = strings.TrimSpace(out.S3.AccessKeyID)
	out.S3.Prefix = NormalizeMediaPrefix(out.S3.Prefix)
	return out
}

// NormalizeMediaPrefix strips leading slashes and collapses the trailing one
// to a single separator (or none for an empty prefix).
func NormalizeMediaPrefix(prefix string) string {
	p := strings.Trim(strings.TrimSpace(prefix), "/")
	if p == "" {
		return ""
	}
	return p + "/"
}

// MediaStorer deliberately does not extend the composite Storer: the server
// type-asserts it so a backend without media support degrades to 503 instead
// of failing to satisfy Storer. Object methods take the owner as their first
// scoping argument and must filter on it.
type MediaStorer interface {
	// GetMediaSettings never returns (nil, nil): an installation with no row
	// yet resolves to DefaultMediaSettings.
	GetMediaSettings(ctx context.Context) (*MediaSettings, error)
	// SaveMediaSettings accepts the write only when the submitted Version is
	// the stored one, then returns the persisted settings with the bumped
	// version. A stale version yields ErrMediaConflict.
	SaveMediaSettings(ctx context.Context, settings MediaSettings) (*MediaSettings, error)
	CreateMediaObject(ctx context.Context, object MediaObject) (*MediaObject, error)
	GetMediaObject(ctx context.Context, owner, id string) (*MediaObject, error)
	// DeleteMediaObject returns the row it removed so the caller can delete
	// the matching blob from the backend that row names.
	DeleteMediaObject(ctx context.Context, owner, id string) (*MediaObject, error)
}
