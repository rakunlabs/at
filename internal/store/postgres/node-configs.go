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

// ─── Node Config CRUD ───

type nodeConfigRow struct {
	ID        string    `db:"id"`
	Name      string    `db:"name"`
	Type      string    `db:"type"`
	Data      string    `db:"data"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// sensitiveFields lists fields that should be encrypted/decrypted within
// the JSON data blob, keyed by config type.
var sensitiveFields = map[string][]string{
	"email": {"password"},
}

func (p *Postgres) ListNodeConfigs(ctx context.Context) ([]service.NodeConfig, error) {
	query, _, err := p.goqu.From(p.tableNodeConfigs).
		Select("id", "name", "type", "data", "created_at", "updated_at").
		Order(goqu.I("name").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list node configs query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list node configs: %w", err)
	}
	defer rows.Close()

	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

	var result []service.NodeConfig
	for rows.Next() {
		var row nodeConfigRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Type, &row.Data, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan node config row: %w", err)
		}

		rec, err := nodeConfigRowToRecord(row, encKey)
		if err != nil {
			return nil, err
		}
		result = append(result, *rec)
	}

	return result, rows.Err()
}

func (p *Postgres) ListNodeConfigsByType(ctx context.Context, configType string) ([]service.NodeConfig, error) {
	query, _, err := p.goqu.From(p.tableNodeConfigs).
		Select("id", "name", "type", "data", "created_at", "updated_at").
		Where(goqu.I("type").Eq(configType)).
		Order(goqu.I("name").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list node configs by type query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list node configs by type %q: %w", configType, err)
	}
	defer rows.Close()

	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

	var result []service.NodeConfig
	for rows.Next() {
		var row nodeConfigRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Type, &row.Data, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan node config row: %w", err)
		}

		rec, err := nodeConfigRowToRecord(row, encKey)
		if err != nil {
			return nil, err
		}
		result = append(result, *rec)
	}

	return result, rows.Err()
}

func (p *Postgres) GetNodeConfig(ctx context.Context, id string) (*service.NodeConfig, error) {
	query, _, err := p.goqu.From(p.tableNodeConfigs).
		Select("id", "name", "type", "data", "created_at", "updated_at").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get node config query: %w", err)
	}

	var row nodeConfigRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Type, &row.Data, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get node config %q: %w", id, err)
	}

	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

	return nodeConfigRowToRecord(row, encKey)
}

func (p *Postgres) CreateNodeConfig(ctx context.Context, nc service.NodeConfig) (*service.NodeConfig, error) {
	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

	storeData, err := encryptNodeConfigData(nc.Type, nc.Data, encKey)
	if err != nil {
		return nil, err
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := p.goqu.Insert(p.tableNodeConfigs).Rows(
		goqu.Record{
			"id":         id,
			"name":       nc.Name,
			"type":       nc.Type,
			"data":       storeData,
			"created_at": now,
			"updated_at": now,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert node config query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create node config %q: %w", nc.Name, err)
	}

	return &service.NodeConfig{
		ID:        id,
		Name:      nc.Name,
		Type:      nc.Type,
		Data:      nc.Data,
		CreatedAt: now.Format(time.RFC3339),
		UpdatedAt: now.Format(time.RFC3339),
	}, nil
}

func (p *Postgres) UpdateNodeConfig(ctx context.Context, id string, nc service.NodeConfig) (*service.NodeConfig, error) {
	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

	storeData, err := encryptNodeConfigData(nc.Type, nc.Data, encKey)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	query, _, err := p.goqu.Update(p.tableNodeConfigs).Set(
		goqu.Record{
			"name":       nc.Name,
			"type":       nc.Type,
			"data":       storeData,
			"updated_at": now,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update node config query: %w", err)
	}

	res, err := p.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update node config %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return p.GetNodeConfig(ctx, id)
}

func (p *Postgres) DeleteNodeConfig(ctx context.Context, id string) error {
	query, _, err := p.goqu.Delete(p.tableNodeConfigs).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete node config query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete node config %q: %w", id, err)
	}

	return nil
}

// nodeConfigRowToRecord converts a database row to a NodeConfig, decrypting sensitive fields.
func nodeConfigRowToRecord(row nodeConfigRow, encKey []byte) (*service.NodeConfig, error) {
	data, err := decryptNodeConfigData(row.Type, row.Data, encKey)
	if err != nil {
		return nil, err
	}

	return &service.NodeConfig{
		ID:        row.ID,
		Name:      row.Name,
		Type:      row.Type,
		Data:      data,
		CreatedAt: row.CreatedAt.Format(time.RFC3339),
		UpdatedAt: row.UpdatedAt.Format(time.RFC3339),
	}, nil
}

// encryptNodeConfigData encrypts sensitive fields within the JSON data blob.
func encryptNodeConfigData(configType, data string, encKey []byte) (string, error) {
	fields, ok := sensitiveFields[configType]
	if !ok || encKey == nil || data == "" {
		return data, nil
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return data, nil // not valid JSON, store as-is
	}

	for _, field := range fields {
		val, ok := m[field].(string)
		if !ok || val == "" {
			continue
		}
		enc, err := atcrypto.Encrypt(val, encKey)
		if err != nil {
			return "", fmt.Errorf("encrypt node config field %q: %w", field, err)
		}
		m[field] = enc
	}

	out, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("marshal encrypted node config data: %w", err)
	}
	return string(out), nil
}

// decryptNodeConfigData decrypts sensitive fields within the JSON data blob.
func decryptNodeConfigData(configType, data string, encKey []byte) (string, error) {
	fields, ok := sensitiveFields[configType]
	if !ok || encKey == nil || data == "" {
		return data, nil
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return data, nil // not valid JSON, return as-is
	}

	for _, field := range fields {
		val, ok := m[field].(string)
		if !ok || val == "" {
			continue
		}
		if !atcrypto.IsEncrypted(val) {
			continue
		}
		dec, err := atcrypto.Decrypt(val, encKey)
		if err != nil {
			return "", fmt.Errorf("decrypt node config field %q: %w", field, err)
		}
		m[field] = dec
	}

	out, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("marshal decrypted node config data: %w", err)
	}
	return string(out), nil
}

// redactNodeConfigData replaces sensitive fields with "***" for list responses.
func redactNodeConfigData(configType, data string) string {
	fields, ok := sensitiveFields[configType]
	if !ok || data == "" {
		return data
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return data
	}

	for _, field := range fields {
		if _, ok := m[field]; ok {
			m[field] = "***"
		}
	}

	out, err := json.Marshal(m)
	if err != nil {
		return data
	}
	return string(out)
}
