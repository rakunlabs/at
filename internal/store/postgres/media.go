package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/oklog/ulid/v2"

	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
)

var _ service.MediaStorer = (*Postgres)(nil)

var mediaObjectColumns = []any{"id", "owner_user_id", "backend", "storage_key", "content_type", "size_bytes", "checksum", "created_at"}

type mediaObjectRow struct {
	ID          string    `db:"id"`
	OwnerUserID string    `db:"owner_user_id"`
	Backend     string    `db:"backend"`
	StorageKey  string    `db:"storage_key"`
	ContentType string    `db:"content_type"`
	SizeBytes   int64     `db:"size_bytes"`
	Checksum    string    `db:"checksum"`
	CreatedAt   time.Time `db:"created_at"`
}

func mediaObjectRowToRecord(row mediaObjectRow) service.MediaObject {
	return service.MediaObject{
		ID:          row.ID,
		OwnerUserID: row.OwnerUserID,
		Backend:     row.Backend,
		StorageKey:  row.StorageKey,
		ContentType: row.ContentType,
		SizeBytes:   row.SizeBytes,
		Checksum:    row.Checksum,
		CreatedAt:   row.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// A unique-violation on the singleton primary key means a concurrent writer
// created the row first, which is the same race the version check guards.
func mediaError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return service.ErrMediaConflict
	}
	return fmt.Errorf("media storage: %w", err)
}

// The whole settings blob is encrypted, not just the secret field: an
// encrypted JSON document cannot be partially readable, and this mirrors
// encryptConnectionCredentials. A nil key stores plaintext, which is the
// documented behaviour of an installation without an encryption key.
func encryptMediaSettings(settings service.MediaSettings, encKey []byte) (string, error) {
	raw, err := json.Marshal(settings)
	if err != nil {
		return "", fmt.Errorf("marshal media settings: %w", err)
	}
	if encKey == nil {
		return string(raw), nil
	}
	sealed, err := atcrypto.Encrypt(string(raw), encKey)
	if err != nil {
		return "", fmt.Errorf("encrypt media settings: %w", err)
	}
	return sealed, nil
}

func decryptMediaSettings(stored string, encKey []byte) (service.MediaSettings, error) {
	var settings service.MediaSettings
	plain := stored
	if encKey != nil && atcrypto.IsEncrypted(stored) {
		decrypted, err := atcrypto.Decrypt(stored, encKey)
		if err != nil {
			return settings, fmt.Errorf("decrypt media settings: %w", err)
		}
		plain = decrypted
	}
	if plain == "" {
		return settings, nil
	}
	if err := json.Unmarshal([]byte(plain), &settings); err != nil {
		return settings, fmt.Errorf("unmarshal media settings: %w", err)
	}
	return settings, nil
}

// GetMediaSettings never answers (nil, nil): an installation that never
// configured media storage reads the disabled default, so every caller can
// treat the result as authoritative policy.
func (p *Postgres) GetMediaSettings(ctx context.Context) (*service.MediaSettings, error) {
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	var row struct {
		Version int64  `db:"version"`
		Config  string `db:"config"`
	}
	found, err := p.goqu.From(p.tableMediaSettings).Select("version", "config").Where(goqu.Ex{"singleton": true}).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, mediaError(err)
	}
	if !found {
		settings := service.DefaultMediaSettings()
		return &settings, nil
	}
	settings, err := decryptMediaSettings(row.Config, p.encKey)
	if err != nil {
		return nil, err
	}
	// The column, not the blob, is the authoritative version.
	settings.Version = row.Version
	return &settings, nil
}

// SaveMediaSettings is optimistic: the submitted Version must still be the
// stored one (or 1, the version of the disabled default, when no row exists
// yet). The comparison happens inside the write transaction, never as a
// read-then-write.
func (p *Postgres) SaveMediaSettings(ctx context.Context, settings service.MediaSettings) (*service.MediaSettings, error) {
	settings = settings.Normalized()
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	expected := settings.Version
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, mediaError(err)
	}
	defer tx.Rollback() //nolint:errcheck // rolled back unless committed

	var current int64
	found, err := tx.From(p.tableMediaSettings).Select("version").Where(goqu.Ex{"singleton": true}).ForUpdate(goqu.Wait).ScanValContext(ctx, &current)
	if err != nil {
		return nil, mediaError(err)
	}
	if found && current != expected {
		return nil, service.ErrMediaConflict
	}
	if !found && expected != service.DefaultMediaSettings().Version {
		return nil, service.ErrMediaConflict
	}

	settings.Version = expected + 1
	config, err := encryptMediaSettings(settings, p.encKey)
	if err != nil {
		return nil, err
	}
	if !found {
		if _, err := tx.Insert(p.tableMediaSettings).Rows(goqu.Record{"singleton": true, "version": settings.Version, "config": config}).Executor().ExecContext(ctx); err != nil {
			return nil, mediaError(err)
		}
	} else {
		result, err := tx.Update(p.tableMediaSettings).Set(goqu.Record{"version": settings.Version, "config": config}).Where(goqu.Ex{"singleton": true, "version": expected}).Executor().ExecContext(ctx)
		if err != nil {
			return nil, mediaError(err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return nil, mediaError(err)
		}
		if n != 1 {
			return nil, service.ErrMediaConflict
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, mediaError(err)
	}
	return &settings, nil
}

func (p *Postgres) CreateMediaObject(ctx context.Context, object service.MediaObject) (*service.MediaObject, error) {
	if object.OwnerUserID == "" {
		return nil, service.ErrMediaNotFound
	}
	record := goqu.Record{
		"id":            ulid.Make().String(),
		"owner_user_id": object.OwnerUserID,
		"backend":       object.Backend,
		"storage_key":   object.StorageKey,
		"content_type":  object.ContentType,
		"size_bytes":    object.SizeBytes,
		"checksum":      object.Checksum,
		"created_at":    goqu.L("clock_timestamp()"),
	}
	var row mediaObjectRow
	if _, err := p.goqu.Insert(p.tableMediaObjects).Rows(record).Returning(mediaObjectColumns...).Executor().ScanStructContext(ctx, &row); err != nil {
		return nil, mediaError(err)
	}
	out := mediaObjectRowToRecord(row)
	return &out, nil
}

// GetMediaObject is owner scoped: a foreign object is indistinguishable from a
// missing one, so ownership cannot be probed.
func (p *Postgres) GetMediaObject(ctx context.Context, owner, id string) (*service.MediaObject, error) {
	if owner == "" || id == "" {
		return nil, service.ErrMediaNotFound
	}
	var row mediaObjectRow
	found, err := p.goqu.From(p.tableMediaObjects).Select(mediaObjectColumns...).Where(goqu.Ex{"id": id, "owner_user_id": owner}).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, mediaError(err)
	}
	if !found {
		return nil, service.ErrMediaNotFound
	}
	out := mediaObjectRowToRecord(row)
	return &out, nil
}

// DeleteMediaObject returns the row it removed so the caller can delete the
// blob from the backend that row names. The record is dropped first on
// purpose: a blob without a row is unreachable garbage the administrator can
// sweep, while a row without a blob is a broken image in the UI.
func (p *Postgres) DeleteMediaObject(ctx context.Context, owner, id string) (*service.MediaObject, error) {
	if owner == "" || id == "" {
		return nil, service.ErrMediaNotFound
	}
	var row mediaObjectRow
	found, err := p.goqu.Delete(p.tableMediaObjects).Where(goqu.Ex{"id": id, "owner_user_id": owner}).Returning(mediaObjectColumns...).Executor().ScanStructContext(ctx, &row)
	if err != nil {
		return nil, mediaError(err)
	}
	if !found {
		return nil, service.ErrMediaNotFound
	}
	out := mediaObjectRowToRecord(row)
	return &out, nil
}

// rotateMediaSettingsKey is called inside RotateEncryptionKey while encKeyMu is
// held, in the same transaction as the provider and authentication secrets: a
// partial rotation would strand the S3 secret access key under a key nobody
// holds any more.
func (p *Postgres) rotateMediaSettingsKey(ctx context.Context, tx *sql.Tx, oldKey, newKey []byte) error {
	query, args, err := p.goqu.From(p.tableMediaSettings).Select("config").Where(goqu.Ex{"singleton": true}).ForUpdate(goqu.Wait).Prepared(true).ToSQL()
	if err != nil {
		return fmt.Errorf("build media rotation: %w", err)
	}
	var stored string
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&stored); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("read media rotation: %w", err)
	}
	settings, err := decryptMediaSettings(stored, oldKey)
	if err != nil {
		return err
	}
	sealed, err := encryptMediaSettings(settings, newKey)
	if err != nil {
		return err
	}
	query, args, err = p.goqu.Update(p.tableMediaSettings).Set(goqu.Record{"config": sealed}).Where(goqu.Ex{"singleton": true}).Prepared(true).ToSQL()
	if err != nil {
		return fmt.Errorf("build media rotation update: %w", err)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("write media rotation: %w", err)
	}
	return nil
}
