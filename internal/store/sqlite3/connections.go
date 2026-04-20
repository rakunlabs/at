package sqlite3

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
	"github.com/rakunlabs/query"
)

type connectionRow struct {
	ID           string         `db:"id"`
	Provider     string         `db:"provider"`
	Name         string         `db:"name"`
	AccountLabel string         `db:"account_label"`
	Description  string         `db:"description"`
	Credentials  string         `db:"credentials"`
	Metadata     sql.NullString `db:"metadata"`
	CreatedAt    string         `db:"created_at"`
	UpdatedAt    string         `db:"updated_at"`
	CreatedBy    string         `db:"created_by"`
	UpdatedBy    string         `db:"updated_by"`
}

func (s *SQLite) ListConnections(ctx context.Context, q *query.Query) (*service.ListResult[service.Connection], error) {
	sqlStr, total, err := s.buildListQuery(ctx, s.tableConnections, q,
		"id", "provider", "name", "account_label", "description",
		"credentials", "metadata", "created_at", "updated_at", "created_by", "updated_by")
	if err != nil {
		return nil, fmt.Errorf("build list connections query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, sqlStr)
	if err != nil {
		return nil, fmt.Errorf("list connections: %w", err)
	}
	defer rows.Close()

	s.encKeyMu.RLock()
	encKey := s.encKey
	s.encKeyMu.RUnlock()

	var items []service.Connection
	for rows.Next() {
		var row connectionRow
		if err := rows.Scan(&row.ID, &row.Provider, &row.Name, &row.AccountLabel, &row.Description,
			&row.Credentials, &row.Metadata, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy); err != nil {
			return nil, fmt.Errorf("scan connection row: %w", err)
		}
		rec, err := connectionRowToRecord(row, encKey)
		if err != nil {
			return nil, err
		}
		items = append(items, *rec)
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.Connection]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (s *SQLite) ListConnectionsByProvider(ctx context.Context, provider string) ([]service.Connection, error) {
	q, _, err := s.goqu.From(s.tableConnections).
		Select("id", "provider", "name", "account_label", "description",
			"credentials", "metadata", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("provider").Eq(provider)).
		Order(goqu.I("name").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list connections by provider query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list connections by provider: %w", err)
	}
	defer rows.Close()

	s.encKeyMu.RLock()
	encKey := s.encKey
	s.encKeyMu.RUnlock()

	var items []service.Connection
	for rows.Next() {
		var row connectionRow
		if err := rows.Scan(&row.ID, &row.Provider, &row.Name, &row.AccountLabel, &row.Description,
			&row.Credentials, &row.Metadata, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy); err != nil {
			return nil, fmt.Errorf("scan connection row: %w", err)
		}
		rec, err := connectionRowToRecord(row, encKey)
		if err != nil {
			return nil, err
		}
		items = append(items, *rec)
	}

	return items, rows.Err()
}

func (s *SQLite) GetConnection(ctx context.Context, id string) (*service.Connection, error) {
	q, _, err := s.goqu.From(s.tableConnections).
		Select("id", "provider", "name", "account_label", "description",
			"credentials", "metadata", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get connection query: %w", err)
	}

	var row connectionRow
	err = s.db.QueryRowContext(ctx, q).Scan(&row.ID, &row.Provider, &row.Name, &row.AccountLabel, &row.Description,
		&row.Credentials, &row.Metadata, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get connection %q: %w", id, err)
	}

	s.encKeyMu.RLock()
	encKey := s.encKey
	s.encKeyMu.RUnlock()

	return connectionRowToRecord(row, encKey)
}

func (s *SQLite) GetConnectionByName(ctx context.Context, provider, name string) (*service.Connection, error) {
	q, _, err := s.goqu.From(s.tableConnections).
		Select("id", "provider", "name", "account_label", "description",
			"credentials", "metadata", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("provider").Eq(provider), goqu.I("name").Eq(name)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get connection by name query: %w", err)
	}

	var row connectionRow
	err = s.db.QueryRowContext(ctx, q).Scan(&row.ID, &row.Provider, &row.Name, &row.AccountLabel, &row.Description,
		&row.Credentials, &row.Metadata, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get connection by name (%s, %s): %w", provider, name, err)
	}

	s.encKeyMu.RLock()
	encKey := s.encKey
	s.encKeyMu.RUnlock()

	return connectionRowToRecord(row, encKey)
}

func (s *SQLite) CreateConnection(ctx context.Context, c service.Connection) (*service.Connection, error) {
	s.encKeyMu.RLock()
	encKey := s.encKey
	s.encKeyMu.RUnlock()

	credsStr, err := encryptConnectionCredentials(c.Credentials, encKey)
	if err != nil {
		return nil, err
	}

	metaJSON, err := json.Marshal(c.Metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal connection metadata: %w", err)
	}
	if string(metaJSON) == "null" {
		metaJSON = []byte("{}")
	}

	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	q, _, err := s.goqu.Insert(s.tableConnections).Rows(
		goqu.Record{
			"id":            id,
			"provider":      c.Provider,
			"name":          c.Name,
			"account_label": c.AccountLabel,
			"description":   c.Description,
			"credentials":   credsStr,
			"metadata":      string(metaJSON),
			"created_at":    now,
			"updated_at":    now,
			"created_by":    c.CreatedBy,
			"updated_by":    c.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert connection query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, q); err != nil {
		return nil, fmt.Errorf("create connection (%s, %s): %w", c.Provider, c.Name, err)
	}

	return &service.Connection{
		ID:           id,
		Provider:     c.Provider,
		Name:         c.Name,
		AccountLabel: c.AccountLabel,
		Description:  c.Description,
		Credentials:  c.Credentials,
		Metadata:     c.Metadata,
		CreatedAt:    now,
		UpdatedAt:    now,
		CreatedBy:    c.CreatedBy,
		UpdatedBy:    c.UpdatedBy,
	}, nil
}

func (s *SQLite) UpdateConnection(ctx context.Context, id string, c service.Connection) (*service.Connection, error) {
	s.encKeyMu.RLock()
	encKey := s.encKey
	s.encKeyMu.RUnlock()

	credsStr, err := encryptConnectionCredentials(c.Credentials, encKey)
	if err != nil {
		return nil, err
	}

	metaJSON, err := json.Marshal(c.Metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal connection metadata: %w", err)
	}
	if string(metaJSON) == "null" {
		metaJSON = []byte("{}")
	}

	now := time.Now().UTC().Format(time.RFC3339)

	q, _, err := s.goqu.Update(s.tableConnections).Set(
		goqu.Record{
			"provider":      c.Provider,
			"name":          c.Name,
			"account_label": c.AccountLabel,
			"description":   c.Description,
			"credentials":   credsStr,
			"metadata":      string(metaJSON),
			"updated_at":    now,
			"updated_by":    c.UpdatedBy,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update connection query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("update connection %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return s.GetConnection(ctx, id)
}

func (s *SQLite) DeleteConnection(ctx context.Context, id string) error {
	q, _, err := s.goqu.Delete(s.tableConnections).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete connection query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, q); err != nil {
		return fmt.Errorf("delete connection %q: %w", id, err)
	}
	return nil
}

// ─── Helpers ───

func encryptConnectionCredentials(creds service.ConnectionCredentials, encKey []byte) (string, error) {
	raw, err := json.Marshal(creds)
	if err != nil {
		return "", fmt.Errorf("marshal connection credentials: %w", err)
	}
	if encKey == nil {
		return string(raw), nil
	}
	enc, err := atcrypto.Encrypt(string(raw), encKey)
	if err != nil {
		return "", fmt.Errorf("encrypt connection credentials: %w", err)
	}
	return enc, nil
}

func decryptConnectionCredentials(stored string, encKey []byte) (service.ConnectionCredentials, error) {
	var creds service.ConnectionCredentials
	plain := stored
	if encKey != nil && atcrypto.IsEncrypted(stored) {
		decrypted, err := atcrypto.Decrypt(stored, encKey)
		if err != nil {
			return creds, fmt.Errorf("decrypt connection credentials: %w", err)
		}
		plain = decrypted
	}
	if plain == "" {
		return creds, nil
	}
	if err := json.Unmarshal([]byte(plain), &creds); err != nil {
		return creds, fmt.Errorf("unmarshal connection credentials: %w", err)
	}
	return creds, nil
}

func connectionRowToRecord(row connectionRow, encKey []byte) (*service.Connection, error) {
	creds, err := decryptConnectionCredentials(row.Credentials, encKey)
	if err != nil {
		return nil, fmt.Errorf("connection %q: %w", row.ID, err)
	}

	var metadata map[string]any
	if row.Metadata.Valid && row.Metadata.String != "" {
		if err := json.Unmarshal([]byte(row.Metadata.String), &metadata); err != nil {
			return nil, fmt.Errorf("unmarshal connection metadata for %q: %w", row.ID, err)
		}
	}

	return &service.Connection{
		ID:           row.ID,
		Provider:     row.Provider,
		Name:         row.Name,
		AccountLabel: row.AccountLabel,
		Description:  row.Description,
		Credentials:  creds,
		Metadata:     metadata,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
		CreatedBy:    row.CreatedBy,
		UpdatedBy:    row.UpdatedBy,
	}, nil
}
