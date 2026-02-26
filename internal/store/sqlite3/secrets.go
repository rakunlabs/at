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

// ─── Variable CRUD ───

type variableRow struct {
	ID          string `db:"id"`
	Key         string `db:"key"`
	Value       string `db:"value"`
	Description string `db:"description"`
	Secret      int    `db:"secret"`
	CreatedAt   string `db:"created_at"`
	UpdatedAt   string `db:"updated_at"`
}

func (s *SQLite) ListVariables(ctx context.Context) ([]service.Variable, error) {
	query, _, err := s.goqu.From(s.tableVariables).
		Select("id", "key", "value", "description", "secret", "created_at", "updated_at").
		Order(goqu.I("key").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list variables query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list variables: %w", err)
	}
	defer rows.Close()

	s.encKeyMu.RLock()
	encKey := s.encKey
	s.encKeyMu.RUnlock()

	var result []service.Variable
	for rows.Next() {
		var row variableRow
		if err := rows.Scan(&row.ID, &row.Key, &row.Value, &row.Description, &row.Secret, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan variable row: %w", err)
		}

		rec, err := variableRowToRecord(row, encKey)
		if err != nil {
			return nil, err
		}
		result = append(result, *rec)
	}

	return result, rows.Err()
}

func (s *SQLite) GetVariable(ctx context.Context, id string) (*service.Variable, error) {
	query, _, err := s.goqu.From(s.tableVariables).
		Select("id", "key", "value", "description", "secret", "created_at", "updated_at").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get variable query: %w", err)
	}

	var row variableRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Key, &row.Value, &row.Description, &row.Secret, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get variable %q: %w", id, err)
	}

	s.encKeyMu.RLock()
	encKey := s.encKey
	s.encKeyMu.RUnlock()

	return variableRowToRecord(row, encKey)
}

func (s *SQLite) GetVariableByKey(ctx context.Context, key string) (*service.Variable, error) {
	query, _, err := s.goqu.From(s.tableVariables).
		Select("id", "key", "value", "description", "secret", "created_at", "updated_at").
		Where(goqu.I("key").Eq(key)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get variable by key query: %w", err)
	}

	var row variableRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Key, &row.Value, &row.Description, &row.Secret, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get variable by key %q: %w", key, err)
	}

	s.encKeyMu.RLock()
	encKey := s.encKey
	s.encKeyMu.RUnlock()

	return variableRowToRecord(row, encKey)
}

func (s *SQLite) CreateVariable(ctx context.Context, v service.Variable) (*service.Variable, error) {
	s.encKeyMu.RLock()
	encKey := s.encKey
	s.encKeyMu.RUnlock()

	storeValue := v.Value
	if v.Secret {
		enc, err := encryptVariableValue(v.Value, encKey)
		if err != nil {
			return nil, err
		}
		storeValue = enc
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	secretInt := 0
	if v.Secret {
		secretInt = 1
	}

	query, _, err := s.goqu.Insert(s.tableVariables).Rows(
		goqu.Record{
			"id":          id,
			"key":         v.Key,
			"value":       storeValue,
			"description": v.Description,
			"secret":      secretInt,
			"created_at":  now.Format(time.RFC3339),
			"updated_at":  now.Format(time.RFC3339),
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert variable query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create variable %q: %w", v.Key, err)
	}

	return &service.Variable{
		ID:          id,
		Key:         v.Key,
		Value:       v.Value,
		Description: v.Description,
		Secret:      v.Secret,
		CreatedAt:   now.Format(time.RFC3339),
		UpdatedAt:   now.Format(time.RFC3339),
	}, nil
}

func (s *SQLite) UpdateVariable(ctx context.Context, id string, v service.Variable) (*service.Variable, error) {
	s.encKeyMu.RLock()
	encKey := s.encKey
	s.encKeyMu.RUnlock()

	storeValue := v.Value
	if v.Secret {
		enc, err := encryptVariableValue(v.Value, encKey)
		if err != nil {
			return nil, err
		}
		storeValue = enc
	}

	now := time.Now().UTC()

	secretInt := 0
	if v.Secret {
		secretInt = 1
	}

	query, _, err := s.goqu.Update(s.tableVariables).Set(
		goqu.Record{
			"key":         v.Key,
			"value":       storeValue,
			"description": v.Description,
			"secret":      secretInt,
			"updated_at":  now.Format(time.RFC3339),
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update variable query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update variable %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return s.GetVariable(ctx, id)
}

func (s *SQLite) DeleteVariable(ctx context.Context, id string) error {
	query, _, err := s.goqu.Delete(s.tableVariables).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete variable query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete variable %q: %w", id, err)
	}

	return nil
}

// variableRowToRecord converts a database row to a Variable, decrypting the value if secret.
func variableRowToRecord(row variableRow, encKey []byte) (*service.Variable, error) {
	value := row.Value
	if row.Secret == 1 && encKey != nil && atcrypto.IsEncrypted(value) {
		decrypted, err := atcrypto.Decrypt(value, encKey)
		if err != nil {
			return nil, fmt.Errorf("decrypt variable %q: %w", row.Key, err)
		}
		value = decrypted
	}

	return &service.Variable{
		ID:          row.ID,
		Key:         row.Key,
		Value:       value,
		Description: row.Description,
		Secret:      row.Secret == 1,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}, nil
}

// encryptVariableValue encrypts a variable value if an encryption key is available.
func encryptVariableValue(value string, encKey []byte) (string, error) {
	if encKey == nil || value == "" {
		return value, nil
	}
	encrypted, err := atcrypto.Encrypt(value, encKey)
	if err != nil {
		return "", fmt.Errorf("encrypt variable value: %w", err)
	}
	return encrypted, nil
}
