package blob

import (
	"errors"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestValidateKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		ok   bool
	}{
		{"plain", "01HX/01HY.png", true},
		{"nested", "prefix/owner/object.webp", true},
		{"space and unicode", "owner/a b ü.png", true},
		{"empty", "", false},
		{"absolute", "/owner/a.png", false},
		{"windows drive", "c:/owner/a.png", false},
		{"backslash", "owner\\a.png", false},
		{"parent segment", "owner/../../etc/passwd", false},
		{"leading parent", "../a.png", false},
		{"dot segment", "owner/./a.png", false},
		{"empty segment", "owner//a.png", false},
		{"trailing slash", "owner/", false},
		{"control character", "owner/a\nb.png", false},
		{"too long", string(make([]byte, KeyMaxBytes+1)), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateKey(tt.key)
			if tt.ok != (err == nil) {
				t.Fatalf("ValidateKey(%q) = %v, want ok=%v", tt.key, err, tt.ok)
			}
		})
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name     string
		settings service.MediaSettings
		wantErr  error
		wantType string
	}{
		{name: "disabled", settings: service.MediaSettings{Version: 1}, wantErr: ErrDisabled},
		{
			name:     "filesystem",
			settings: service.MediaSettings{Version: 1, Backend: service.MediaBackendFilesystem, Filesystem: service.MediaFilesystemSettings{Root: t.TempDir()}},
			wantType: "*blob.filesystemStore",
		},
		{
			name:     "filesystem relative root",
			settings: service.MediaSettings{Version: 1, Backend: service.MediaBackendFilesystem, Filesystem: service.MediaFilesystemSettings{Root: "relative/media"}},
		},
		{
			name: "s3",
			settings: service.MediaSettings{Version: 1, Backend: service.MediaBackendS3, S3: service.MediaS3Settings{
				Endpoint: "https://s3.example.test", Region: "us-east-1", Bucket: "media", AccessKeyID: "id", SecretAccessKey: "secret",
			}},
			wantType: "*blob.s3Store",
		},
		{
			name: "s3 missing secret",
			settings: service.MediaSettings{Version: 1, Backend: service.MediaBackendS3, S3: service.MediaS3Settings{
				Endpoint: "https://s3.example.test", Region: "us-east-1", Bucket: "media", AccessKeyID: "id",
			}},
		},
		{
			name: "s3 bad scheme",
			settings: service.MediaSettings{Version: 1, Backend: service.MediaBackendS3, S3: service.MediaS3Settings{
				Endpoint: "ftp://s3.example.test", Region: "us-east-1", Bucket: "media", AccessKeyID: "id", SecretAccessKey: "secret",
			}},
		},
		{name: "unknown backend", settings: service.MediaSettings{Version: 1, Backend: "gcs"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := New(tt.settings)
			if tt.wantType == "" {
				if err == nil {
					t.Fatalf("expected error, got %T", store)
				}
				if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
					t.Fatalf("error %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := typeName(store); got != tt.wantType {
				t.Fatalf("type %s, want %s", got, tt.wantType)
			}
		})
	}
}

func typeName(v any) string {
	switch v.(type) {
	case *filesystemStore:
		return "*blob.filesystemStore"
	case *s3Store:
		return "*blob.s3Store"
	default:
		return "unknown"
	}
}
