package postgres

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
	ID          string    `db:"id"`
	Key         string    `db:"key"`
	Value       string    `db:"value"`
	Description string    `db:"description"`
	Secret      bool      `db:"secret"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

func (p *Postgres) ListVariables(ctx context.Context) ([]service.Variable, error) {
	query, _, err := p.goqu.From(p.tableVariables).
		Select("id", "key", "value", "description", "secret", "created_at", "updated_at").
		Order(goqu.I("key").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list variables query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list variables: %w", err)
	}
	defer rows.Close()

	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

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

func (p *Postgres) GetVariable(ctx context.Context, id string) (*service.Variable, error) {
	query, _, err := p.goqu.From(p.tableVariables).
		Select("id", "key", "value", "description", "secret", "created_at", "updated_at").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get variable query: %w", err)
	}

	var row variableRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Key, &row.Value, &row.Description, &row.Secret, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get variable %q: %w", id, err)
	}

	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

	return variableRowToRecord(row, encKey)
}

func (p *Postgres) GetVariableByKey(ctx context.Context, key string) (*service.Variable, error) {
	query, _, err := p.goqu.From(p.tableVariables).
		Select("id", "key", "value", "description", "secret", "created_at", "updated_at").
		Where(goqu.I("key").Eq(key)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get variable by key query: %w", err)
	}

	var row variableRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Key, &row.Value, &row.Description, &row.Secret, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get variable by key %q: %w", key, err)
	}

	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

	return variableRowToRecord(row, encKey)
}

func (p *Postgres) CreateVariable(ctx context.Context, v service.Variable) (*service.Variable, error) {
	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

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

	query, _, err := p.goqu.Insert(p.tableVariables).Rows(
		goqu.Record{
			"id":          id,
			"key":         v.Key,
			"value":       storeValue,
			"description": v.Description,
			"secret":      v.Secret,
			"created_at":  now,
			"updated_at":  now,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert variable query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
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

func (p *Postgres) UpdateVariable(ctx context.Context, id string, v service.Variable) (*service.Variable, error) {
	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

	storeValue := v.Value
	if v.Secret {
		enc, err := encryptVariableValue(v.Value, encKey)
		if err != nil {
			return nil, err
		}
		storeValue = enc
	}

	now := time.Now().UTC()

	query, _, err := p.goqu.Update(p.tableVariables).Set(
		goqu.Record{
			"key":         v.Key,
			"value":       storeValue,
			"description": v.Description,
			"secret":      v.Secret,
			"updated_at":  now,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update variable query: %w", err)
	}

	res, err := p.db.ExecContext(ctx, query)
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

	return p.GetVariable(ctx, id)
}

func (p *Postgres) DeleteVariable(ctx context.Context, id string) error {
	query, _, err := p.goqu.Delete(p.tableVariables).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete variable query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete variable %q: %w", id, err)
	}

	return nil
}

// variableRowToRecord converts a database row to a Variable, decrypting the value if secret.
func variableRowToRecord(row variableRow, encKey []byte) (*service.Variable, error) {
	value := row.Value
	if row.Secret && encKey != nil && atcrypto.IsEncrypted(value) {
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
		Secret:      row.Secret,
		CreatedAt:   row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   row.UpdatedAt.Format(time.RFC3339),
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
