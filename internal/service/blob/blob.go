// Package blob stores and serves opaque byte payloads for AT's configurable
// media storage. Two backends exist: the local filesystem and any
// S3-compatible object store. The S3 backend signs its own requests with
// hand-rolled AWS Signature Version 4 over net/http, mirroring
// internal/service/llm/bedrock: the repository deliberately carries no cloud
// SDK dependency.
package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// ErrDisabled is returned by New when no backend is configured. Callers turn
// it into a 503 telling the administrator to configure media storage.
var ErrDisabled = errors.New("media storage is disabled")

// KeyMaxBytes bounds a storage key. It matches the S3 object-key limit so a
// key accepted by the filesystem backend can always be migrated to S3.
const KeyMaxBytes = 1024

// Store is the backend-independent blob contract. Keys are caller-generated,
// '/'-separated relative paths; every method validates the key itself rather
// than trusting its caller.
type Store interface {
	Put(ctx context.Context, key, contentType string, data []byte) error
	// Get returns the payload reader and the content type the backend
	// reports. The filesystem backend has nowhere to keep a content type and
	// returns an empty string; the database record is authoritative either
	// way, so callers must not depend on this value.
	Get(ctx context.Context, key string) (io.ReadCloser, string, error)
	// Delete is idempotent: a missing object is not an error, because the
	// database row is deleted in the same flow and a retry must converge.
	Delete(ctx context.Context, key string) error
	// Check probes connectivity and writability so an administrator gets a
	// real error at configuration time instead of at a user's first upload.
	Check(ctx context.Context) error
}

// New builds the backend described by settings. A disabled or invalid
// configuration yields a nil Store and an error; it never returns a Store that
// would fail on every call.
func New(s service.MediaSettings) (Store, error) {
	s = s.Normalized()
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	switch s.Backend {
	case service.MediaBackendFilesystem:
		return newFilesystem(s.Filesystem)
	case service.MediaBackendS3:
		return newS3(s.S3)
	default:
		return nil, fmt.Errorf("unknown media storage backend %q", s.Backend)
	}
}

// ValidateKey rejects anything that could escape the configured root or
// prefix. Backends call it on every operation: a key is data, and the only
// safe assumption about data is that it is hostile.
func ValidateKey(key string) error {
	if key == "" {
		return errors.New("storage key must not be empty")
	}
	if len(key) > KeyMaxBytes {
		return fmt.Errorf("storage key must not exceed %d bytes", KeyMaxBytes)
	}
	if strings.ContainsRune(key, '\\') {
		return errors.New("storage key must not contain a backslash")
	}
	if strings.HasPrefix(key, "/") {
		return errors.New("storage key must be relative")
	}
	// Windows drive-relative keys ("c:/x") are absolute on one platform and
	// relative on another, so they are refused everywhere.
	if len(key) > 1 && key[1] == ':' {
		return errors.New("storage key must be relative")
	}
	for _, segment := range strings.Split(key, "/") {
		switch segment {
		case "":
			return errors.New("storage key must not contain an empty segment")
		case ".", "..":
			return errors.New("storage key must not contain a relative segment")
		}
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x20 || key[i] == 0x7f {
			return errors.New("storage key must not contain control characters")
		}
	}
	return nil
}
