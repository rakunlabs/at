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
	"github.com/rakunlabs/query"
	"github.com/worldline-go/types"
)

type connectionRow struct {
	ID           string         `db:"id"`
	Provider     string         `db:"provider"`
	Name         string         `db:"name"`
	AccountLabel string         `db:"account_label"`
	Description  string         `db:"description"`
	Credentials  string         `db:"credentials"`
	Metadata     types.RawJSON  `db:"metadata"`
	CreatedAt    time.Time      `db:"created_at"`
	UpdatedAt    time.Time      `db:"updated_at"`
	CreatedBy    sql.NullString `db:"created_by"`
	UpdatedBy    sql.NullString `db:"updated_by"`
}

func (p *Postgres) ListConnections(ctx context.Context, q *query.Query) (*service.ListResult[service.Connection], error) {
	sqlStr, total, err := p.buildListQuery(ctx, p.tableConnections, q,
		"id", "provider", "name", "account_label", "description",
		"credentials", "metadata", "created_at", "updated_at", "created_by", "updated_by")
	if err != nil {
		return nil, fmt.Errorf("build list connections query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sqlStr)
	if err != nil {
		return nil, fmt.Errorf("list connections: %w", err)
	}
	defer rows.Close()

	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

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

func (p *Postgres) ListConnectionsByProvider(ctx context.Context, provider string) ([]service.Connection, error) {
	sqlStr, _, err := p.goqu.From(p.tableConnections).
		Select("id", "provider", "name", "account_label", "description",
			"credentials", "metadata", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("provider").Eq(provider)).
		Order(goqu.I("name").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list connections by provider query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sqlStr)
	if err != nil {
		return nil, fmt.Errorf("list connections by provider: %w", err)
	}
	defer rows.Close()

	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

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

func (p *Postgres) GetConnection(ctx context.Context, id string) (*service.Connection, error) {
	sqlStr, _, err := p.goqu.From(p.tableConnections).
		Select("id", "provider", "name", "account_label", "description",
			"credentials", "metadata", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get connection query: %w", err)
	}

	var row connectionRow
	err = p.db.QueryRowContext(ctx, sqlStr).Scan(&row.ID, &row.Provider, &row.Name, &row.AccountLabel, &row.Description,
		&row.Credentials, &row.Metadata, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get connection %q: %w", id, err)
	}

	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

	return connectionRowToRecord(row, encKey)
}

func (p *Postgres) GetConnectionByName(ctx context.Context, provider, name string) (*service.Connection, error) {
	sqlStr, _, err := p.goqu.From(p.tableConnections).
		Select("id", "provider", "name", "account_label", "description",
			"credentials", "metadata", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("provider").Eq(provider), goqu.I("name").Eq(name)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get connection by name query: %w", err)
	}

	var row connectionRow
	err = p.db.QueryRowContext(ctx, sqlStr).Scan(&row.ID, &row.Provider, &row.Name, &row.AccountLabel, &row.Description,
		&row.Credentials, &row.Metadata, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get connection by name (%s, %s): %w", provider, name, err)
	}

	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

	return connectionRowToRecord(row, encKey)
}

func (p *Postgres) CreateConnection(ctx context.Context, c service.Connection) (*service.Connection, error) {
	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

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
	now := time.Now().UTC()

	sqlStr, _, err := p.goqu.Insert(p.tableConnections).Rows(
		goqu.Record{
			"id":            id,
			"provider":      c.Provider,
			"name":          c.Name,
			"account_label": c.AccountLabel,
			"description":   c.Description,
			"credentials":   credsStr,
			"metadata":      types.RawJSON(metaJSON),
			"created_at":    now,
			"updated_at":    now,
			"created_by":    c.CreatedBy,
			"updated_by":    c.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert connection query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, sqlStr); err != nil {
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
		CreatedAt:    now.Format(time.RFC3339),
		UpdatedAt:    now.Format(time.RFC3339),
		CreatedBy:    c.CreatedBy,
		UpdatedBy:    c.UpdatedBy,
	}, nil
}

func (p *Postgres) UpdateConnection(ctx context.Context, id string, c service.Connection) (*service.Connection, error) {
	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

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

	now := time.Now().UTC()

	sqlStr, _, err := p.goqu.Update(p.tableConnections).Set(
		goqu.Record{
			"provider":      c.Provider,
			"name":          c.Name,
			"account_label": c.AccountLabel,
			"description":   c.Description,
			"credentials":   credsStr,
			"metadata":      types.RawJSON(metaJSON),
			"updated_at":    now,
			"updated_by":    c.UpdatedBy,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update connection query: %w", err)
	}

	res, err := p.db.ExecContext(ctx, sqlStr)
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

	return p.GetConnection(ctx, id)
}

func (p *Postgres) DeleteConnection(ctx context.Context, id string) error {
	sqlStr, _, err := p.goqu.Delete(p.tableConnections).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete connection query: %w", err)
	}
	if _, err := p.db.ExecContext(ctx, sqlStr); err != nil {
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
	if len(row.Metadata) > 0 {
		if err := json.Unmarshal(row.Metadata, &metadata); err != nil {
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
		CreatedAt:    row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:    row.UpdatedAt.Format(time.RFC3339),
		CreatedBy:    row.CreatedBy.String,
		UpdatedBy:    row.UpdatedBy.String,
	}, nil
}
