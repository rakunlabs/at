package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
)

// ─── User Preference CRUD ───

type userPreferenceRow struct {
	ID        string    `db:"id"`
	UserID    string    `db:"user_id"`
	Key       string    `db:"key"`
	Value     string    `db:"value"`
	Secret    bool      `db:"secret"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

func (p *Postgres) ListUserPreferences(ctx context.Context, userID string) ([]service.UserPreference, error) {
	query, _, err := p.goqu.From(p.tableUserPreferences).
		Select("id", "user_id", "key", "value", "secret", "created_at", "updated_at").
		Where(goqu.I("user_id").Eq(userID)).
		Order(goqu.I("key").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list user preferences query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list user preferences: %w", err)
	}
	defer rows.Close()

	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

	var items []service.UserPreference
	for rows.Next() {
		var row userPreferenceRow
		if err := rows.Scan(&row.ID, &row.UserID, &row.Key, &row.Value, &row.Secret, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan user preference row: %w", err)
		}

		rec, err := userPrefRowToRecord(row, encKey)
		if err != nil {
			return nil, err
		}
		items = append(items, *rec)
	}

	return items, rows.Err()
}

func (p *Postgres) GetUserPreference(ctx context.Context, userID, key string) (*service.UserPreference, error) {
	query, _, err := p.goqu.From(p.tableUserPreferences).
		Select("id", "user_id", "key", "value", "secret", "created_at", "updated_at").
		Where(goqu.I("user_id").Eq(userID), goqu.I("key").Eq(key)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get user preference query: %w", err)
	}

	var row userPreferenceRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.UserID, &row.Key, &row.Value, &row.Secret, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user preference %q/%q: %w", userID, key, err)
	}

	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

	return userPrefRowToRecord(row, encKey)
}

func (p *Postgres) SetUserPreference(ctx context.Context, pref service.UserPreference) error {
	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

	storeValue := string(pref.Value)
	if pref.Secret {
		enc, err := encryptVariableValue(storeValue, encKey)
		if err != nil {
			return err
		}
		storeValue = enc
	}

	now := time.Now().UTC()

	// Try to find existing preference.
	existing, err := p.GetUserPreference(ctx, pref.UserID, pref.Key)
	if err != nil {
		return fmt.Errorf("check existing user preference: %w", err)
	}

	if existing != nil {
		// Update existing.
		query, _, err := p.goqu.Update(p.tableUserPreferences).Set(
			goqu.Record{
				"value":      storeValue,
				"secret":     pref.Secret,
				"updated_at": now,
			},
		).Where(goqu.I("id").Eq(existing.ID)).ToSQL()
		if err != nil {
			return fmt.Errorf("build update user preference query: %w", err)
		}

		if _, err := p.db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("update user preference %q/%q: %w", pref.UserID, pref.Key, err)
		}

		return nil
	}

	// Insert new.
	id := ulid.Make().String()

	query, _, err := p.goqu.Insert(p.tableUserPreferences).Rows(
		goqu.Record{
			"id":         id,
			"user_id":    pref.UserID,
			"key":        pref.Key,
			"value":      storeValue,
			"secret":     pref.Secret,
			"created_at": now,
			"updated_at": now,
		},
	).ToSQL()
	if err != nil {
		return fmt.Errorf("build insert user preference query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("create user preference %q/%q: %w", pref.UserID, pref.Key, err)
	}

	return nil
}

func (p *Postgres) DeleteUserPreference(ctx context.Context, userID, key string) error {
	query, _, err := p.goqu.Delete(p.tableUserPreferences).
		Where(goqu.I("user_id").Eq(userID), goqu.I("key").Eq(key)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete user preference query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete user preference %q/%q: %w", userID, key, err)
	}

	return nil
}

// userPrefRowToRecord converts a database row to a UserPreference, decrypting the value if secret.
func userPrefRowToRecord(row userPreferenceRow, encKey []byte) (*service.UserPreference, error) {
	value := row.Value
	if row.Secret && encKey != nil && atcrypto.IsEncrypted(value) {
		decrypted, err := atcrypto.Decrypt(value, encKey)
		if err != nil {
			return nil, fmt.Errorf("decrypt user preference %q/%q: %w", row.UserID, row.Key, err)
		}
		value = decrypted
	}

	return &service.UserPreference{
		ID:        row.ID,
		UserID:    row.UserID,
		Key:       row.Key,
		Value:     json.RawMessage(value),
		Secret:    row.Secret,
		CreatedAt: row.CreatedAt.Format(time.RFC3339),
		UpdatedAt: row.UpdatedAt.Format(time.RFC3339),
	}, nil
}
