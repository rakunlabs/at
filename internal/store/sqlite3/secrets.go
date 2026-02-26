package sqlite3

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
)

// ─── Secret CRUD ───

type secretRow struct {
	ID          string `db:"id"`
	Key         string `db:"key"`
	Value       string `db:"value"`
	Description string `db:"description"`
	CreatedAt   string `db:"created_at"`
	UpdatedAt   string `db:"updated_at"`
}

func (s *SQLite) ListSecrets(ctx context.Context) ([]service.Secret, error) {
	query, _, err := s.goqu.From(s.tableSecrets).
		Select("id", "key", "value", "description", "created_at", "updated_at").
		Order(goqu.I("key").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list secrets query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}
	defer rows.Close()

	s.encKeyMu.RLock()
	encKey := s.encKey
	s.encKeyMu.RUnlock()

	var result []service.Secret
	for rows.Next() {
		var row secretRow
		if err := rows.Scan(&row.ID, &row.Key, &row.Value, &row.Description, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan secret row: %w", err)
		}

		rec, err := secretRowToRecord(row, encKey)
		if err != nil {
			return nil, err
		}
		result = append(result, *rec)
	}

	return result, rows.Err()
}

func (s *SQLite) GetSecret(ctx context.Context, id string) (*service.Secret, error) {
	query, _, err := s.goqu.From(s.tableSecrets).
		Select("id", "key", "value", "description", "created_at", "updated_at").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get secret query: %w", err)
	}

	var row secretRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Key, &row.Value, &row.Description, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get secret %q: %w", id, err)
	}

	s.encKeyMu.RLock()
	encKey := s.encKey
	s.encKeyMu.RUnlock()

	return secretRowToRecord(row, encKey)
}

func (s *SQLite) GetSecretByKey(ctx context.Context, key string) (*service.Secret, error) {
	query, _, err := s.goqu.From(s.tableSecrets).
		Select("id", "key", "value", "description", "created_at", "updated_at").
		Where(goqu.I("key").Eq(key)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get secret by key query: %w", err)
	}

	var row secretRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Key, &row.Value, &row.Description, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get secret by key %q: %w", key, err)
	}

	s.encKeyMu.RLock()
	encKey := s.encKey
	s.encKeyMu.RUnlock()

	return secretRowToRecord(row, encKey)
}

func (s *SQLite) CreateSecret(ctx context.Context, sec service.Secret) (*service.Secret, error) {
	s.encKeyMu.RLock()
	encKey := s.encKey
	s.encKeyMu.RUnlock()

	encValue, err := encryptSecretValue(sec.Value, encKey)
	if err != nil {
		return nil, err
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := s.goqu.Insert(s.tableSecrets).Rows(
		goqu.Record{
			"id":          id,
			"key":         sec.Key,
			"value":       encValue,
			"description": sec.Description,
			"created_at":  now.Format(time.RFC3339),
			"updated_at":  now.Format(time.RFC3339),
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert secret query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create secret %q: %w", sec.Key, err)
	}

	return &service.Secret{
		ID:          id,
		Key:         sec.Key,
		Value:       sec.Value,
		Description: sec.Description,
		CreatedAt:   now.Format(time.RFC3339),
		UpdatedAt:   now.Format(time.RFC3339),
	}, nil
}

func (s *SQLite) UpdateSecret(ctx context.Context, id string, sec service.Secret) (*service.Secret, error) {
	s.encKeyMu.RLock()
	encKey := s.encKey
	s.encKeyMu.RUnlock()

	encValue, err := encryptSecretValue(sec.Value, encKey)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	query, _, err := s.goqu.Update(s.tableSecrets).Set(
		goqu.Record{
			"key":         sec.Key,
			"value":       encValue,
			"description": sec.Description,
			"updated_at":  now.Format(time.RFC3339),
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update secret query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update secret %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return s.GetSecret(ctx, id)
}

func (s *SQLite) DeleteSecret(ctx context.Context, id string) error {
	query, _, err := s.goqu.Delete(s.tableSecrets).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete secret query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete secret %q: %w", id, err)
	}

	return nil
}

// secretRowToRecord converts a database row to a Secret, decrypting the value.
func secretRowToRecord(row secretRow, encKey []byte) (*service.Secret, error) {
	value := row.Value
	if encKey != nil && atcrypto.IsEncrypted(value) {
		decrypted, err := atcrypto.Decrypt(value, encKey)
		if err != nil {
			return nil, fmt.Errorf("decrypt secret %q: %w", row.Key, err)
		}
		value = decrypted
	}

	return &service.Secret{
		ID:          row.ID,
		Key:         row.Key,
		Value:       value,
		Description: row.Description,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}, nil
}

// encryptSecretValue encrypts a secret value if an encryption key is available.
func encryptSecretValue(value string, encKey []byte) (string, error) {
	if encKey == nil || value == "" {
		return value, nil
	}
	encrypted, err := atcrypto.Encrypt(value, encKey)
	if err != nil {
		return "", fmt.Errorf("encrypt secret value: %w", err)
	}
	return encrypted, nil
}
