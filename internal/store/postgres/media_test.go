package postgres

import (
	"errors"
	"strings"
	"testing"

	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
)

func mediaS3Settings(version int64, secret string) service.MediaSettings {
	return service.MediaSettings{
		Version: version,
		Backend: service.MediaBackendS3,
		S3: service.MediaS3Settings{
			Endpoint:        "https://s3.example.test",
			Region:          "us-east-1",
			Bucket:          "at-media",
			Prefix:          "/playground/",
			AccessKeyID:     "AKIAEXAMPLE",
			SecretAccessKey: secret,
			UsePathStyle:    true,
		},
	}
}

func mediaRawConfig(t *testing.T, p *Postgres) string {
	t.Helper()
	var raw string
	row := p.db.QueryRowContext(t.Context(), "SELECT config FROM "+p.tableMediaSettings.GetTable()+" WHERE singleton")
	if err := row.Scan(&raw); err != nil {
		t.Fatalf("scan raw config: %v", err)
	}
	return raw
}

func TestMediaSettingsRoundTrip(t *testing.T) {
	key, err := atcrypto.DeriveKey("media-test-key")
	if err != nil {
		t.Fatal(err)
	}
	p := newTestStore(t, key)
	ctx := t.Context()

	// An installation that never configured media storage reads the disabled
	// default, never (nil, nil).
	current, err := p.GetMediaSettings(ctx)
	if err != nil || current == nil || current.Backend != service.MediaBackendDisabled || current.Version != 1 {
		t.Fatalf("default settings: %+v %v", current, err)
	}

	saved, err := p.SaveMediaSettings(ctx, mediaS3Settings(current.Version, "super-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version != 2 || saved.S3.SecretAccessKey != "super-secret" {
		t.Fatalf("saved: %+v", saved)
	}
	// The prefix is normalised on the way in.
	if saved.S3.Prefix != "playground/" {
		t.Fatalf("prefix %q", saved.S3.Prefix)
	}

	got, err := p.GetMediaSettings(ctx)
	if err != nil || got.Version != 2 || got.Backend != service.MediaBackendS3 || got.S3.SecretAccessKey != "super-secret" || !got.S3.UsePathStyle {
		t.Fatalf("read back: %+v %v", got, err)
	}

	// The secret must be encrypted at rest, and no part of the blob may be
	// readable as plaintext.
	raw := mediaRawConfig(t, p)
	if !atcrypto.IsEncrypted(raw) || strings.Contains(raw, "super-secret") || strings.Contains(raw, "at-media") {
		t.Fatalf("config is not encrypted at rest: %q", raw)
	}

	// Key rotation must carry the secret over; it is the only copy.
	newKey, err := atcrypto.DeriveKey("media-test-key-2")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.RotateEncryptionKey(ctx, newKey); err != nil {
		t.Fatal(err)
	}
	rotated, err := p.GetMediaSettings(ctx)
	if err != nil || rotated.S3.SecretAccessKey != "super-secret" {
		t.Fatalf("after rotation: %+v %v", rotated, err)
	}
	if raw2 := mediaRawConfig(t, p); raw2 == raw || !atcrypto.IsEncrypted(raw2) {
		t.Fatalf("rotation did not re-encrypt: %q", raw2)
	}
}

func TestMediaSettingsWithoutEncryptionKey(t *testing.T) {
	p := newTestStore(t, nil)
	// Without a configured key the blob is plaintext, exactly like connection
	// credentials on such an installation.
	if _, err := p.SaveMediaSettings(t.Context(), mediaS3Settings(1, "plain-secret")); err != nil {
		t.Fatal(err)
	}
	raw := mediaRawConfig(t, p)
	if atcrypto.IsEncrypted(raw) || !strings.Contains(raw, "plain-secret") {
		t.Fatalf("unexpected storage form: %q", raw)
	}
}

func TestMediaSettingsOptimisticConcurrency(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := t.Context()

	// The first write must carry the version of the disabled default.
	if _, err := p.SaveMediaSettings(ctx, mediaS3Settings(7, "s")); !errors.Is(err, service.ErrMediaConflict) {
		t.Fatalf("insert with a stale version: %v", err)
	}
	saved, err := p.SaveMediaSettings(ctx, mediaS3Settings(1, "s"))
	if err != nil {
		t.Fatal(err)
	}
	// A second writer holding the same version it read loses the race.
	if _, err := p.SaveMediaSettings(ctx, mediaS3Settings(1, "s")); !errors.Is(err, service.ErrMediaConflict) {
		t.Fatalf("stale update: %v", err)
	}
	if _, err := p.SaveMediaSettings(ctx, mediaS3Settings(saved.Version, "s")); err != nil {
		t.Fatal(err)
	}

	// Invalid configurations never reach the table.
	for name, settings := range map[string]service.MediaSettings{
		"filesystem relative root": {Version: 3, Backend: service.MediaBackendFilesystem, Filesystem: service.MediaFilesystemSettings{Root: "media"}},
		"unknown backend":          {Version: 3, Backend: "gcs"},
		"s3 without bucket":        mediaS3WithoutBucket(3),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := p.SaveMediaSettings(ctx, settings); err == nil || errors.Is(err, service.ErrMediaConflict) {
				t.Fatalf("expected a validation error, got %v", err)
			}
		})
	}
}

func mediaS3WithoutBucket(version int64) service.MediaSettings {
	s := mediaS3Settings(version, "s")
	s.S3.Bucket = ""
	return s
}

func TestMediaObjectOwnerScoping(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := t.Context()

	if _, err := p.CreateMediaObject(ctx, service.MediaObject{Backend: service.MediaBackendFilesystem, StorageKey: "k", ContentType: "image/png"}); !errors.Is(err, service.ErrMediaNotFound) {
		t.Fatalf("ownerless create: %v", err)
	}
	created, err := p.CreateMediaObject(ctx, service.MediaObject{
		OwnerUserID: "user-a",
		Backend:     service.MediaBackendFilesystem,
		StorageKey:  "user-a/01HX.png",
		ContentType: "image/png",
		SizeBytes:   9,
		Checksum:    "abc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.CreatedAt == "" || created.SizeBytes != 9 {
		t.Fatalf("created: %+v", created)
	}

	got, err := p.GetMediaObject(ctx, "user-a", created.ID)
	if err != nil || got.StorageKey != "user-a/01HX.png" || got.ContentType != "image/png" || got.Checksum != "abc" {
		t.Fatalf("get: %+v %v", got, err)
	}
	// A foreign or unknown object is indistinguishable from a missing one.
	for name, fn := range map[string]func() error{
		"foreign get":     func() error { _, e := p.GetMediaObject(ctx, "user-b", created.ID); return e },
		"foreign delete":  func() error { _, e := p.DeleteMediaObject(ctx, "user-b", created.ID); return e },
		"unknown get":     func() error { _, e := p.GetMediaObject(ctx, "user-a", "nope"); return e },
		"empty owner get": func() error { _, e := p.GetMediaObject(ctx, "", created.ID); return e },
		"empty id delete": func() error { _, e := p.DeleteMediaObject(ctx, "user-a", ""); return e },
		"unknown delete":  func() error { _, e := p.DeleteMediaObject(ctx, "user-a", "nope"); return e },
	} {
		t.Run(name, func(t *testing.T) {
			if err := fn(); !errors.Is(err, service.ErrMediaNotFound) {
				t.Fatalf("got %v", err)
			}
		})
	}

	// Delete returns the removed row so the caller can remove the blob.
	deleted, err := p.DeleteMediaObject(ctx, "user-a", created.ID)
	if err != nil || deleted.StorageKey != "user-a/01HX.png" || deleted.Backend != service.MediaBackendFilesystem {
		t.Fatalf("delete: %+v %v", deleted, err)
	}
	if _, err := p.GetMediaObject(ctx, "user-a", created.ID); !errors.Is(err, service.ErrMediaNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}
