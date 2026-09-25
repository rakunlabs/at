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
	"github.com/rakunlabs/query"
	"github.com/worldline-go/types"

	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
)

// ─── Variable CRUD ───

type variableRow struct {
	WorkspaceID  string        `db:"workspace_id"`
	ID           string        `db:"id"`
	Key          string        `db:"key"`
	Value        string        `db:"value"`
	Description  string        `db:"description"`
	Secret       bool          `db:"secret"`
	AllowedTools types.RawJSON `db:"allowed_tools"`
	AllowedHosts types.RawJSON `db:"allowed_hosts"`
	CreatedAt    time.Time     `db:"created_at"`
	UpdatedAt    time.Time     `db:"updated_at"`
	CreatedBy    string        `db:"created_by"`
	UpdatedBy    string        `db:"updated_by"`
}

func (p *Postgres) ListVariables(ctx context.Context, q *query.Query) (*service.ListResult[service.Variable], error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	sql, total, err := p.buildListQuery(ctx, p.tableVariables, q, "id", "key", "value", "description", "secret", "allowed_tools", "allowed_hosts", "created_at", "updated_at", "created_by", "updated_by", "workspace_id")
	if err != nil {
		return nil, fmt.Errorf("build list variables query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list variables: %w", err)
	}
	defer rows.Close()

	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

	var items []service.Variable
	for rows.Next() {
		var row variableRow
		if err := rows.Scan(&row.ID, &row.Key, &row.Value, &row.Description, &row.Secret, &row.AllowedTools, &row.AllowedHosts, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy, &row.WorkspaceID); err != nil {
			return nil, fmt.Errorf("scan variable row: %w", err)
		}

		if row.Secret && !a.Allows("credentials.manage", service.AccessResource{WorkspaceID: row.WorkspaceID, ID: row.ID}) {
			row.Value = "***"
		}
		rec, err := variableRowToRecord(row, encKey)
		if err != nil {
			return nil, err
		}
		items = append(items, *rec)
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.Variable]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (p *Postgres) GetVariable(ctx context.Context, id string) (*service.Variable, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	scope, err := p.businessReadScope(ctx, p.tableVariables)
	if err != nil {
		return nil, err
	}
	query, _, err := p.goqu.From(p.tableVariables).
		Select("id", "key", "value", "description", "secret", "allowed_tools", "allowed_hosts", "created_at", "updated_at", "created_by", "updated_by", "workspace_id").
		Where(scope, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get variable query: %w", err)
	}

	var row variableRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Key, &row.Value, &row.Description, &row.Secret, &row.AllowedTools, &row.AllowedHosts, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy, &row.WorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get variable %q: %w", id, err)
	}

	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

	if row.Secret && !a.Allows("credentials.manage", service.AccessResource{WorkspaceID: row.WorkspaceID, ID: row.ID}) {
		row.Value = "***"
	}
	return variableRowToRecord(row, encKey)
}

func (p *Postgres) GetVariableByKey(ctx context.Context, key string) (*service.Variable, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	scope, err := p.businessReadScope(ctx, p.tableVariables)
	if err != nil {
		return nil, err
	}
	query, _, err := p.goqu.From(p.tableVariables).
		Select("id", "key", "value", "description", "secret", "allowed_tools", "allowed_hosts", "created_at", "updated_at", "created_by", "updated_by", "workspace_id").
		Where(scope, goqu.I("key").Eq(key)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get variable by key query: %w", err)
	}

	var row variableRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Key, &row.Value, &row.Description, &row.Secret, &row.AllowedTools, &row.AllowedHosts, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy, &row.WorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get variable by key %q: %w", key, err)
	}

	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

	if row.Secret && !a.Allows("credentials.manage", service.AccessResource{WorkspaceID: row.WorkspaceID, ID: row.ID}) {
		row.Value = "***"
	}
	return variableRowToRecord(row, encKey)
}

func (p *Postgres) CreateVariable(ctx context.Context, v service.Variable) (*service.Variable, error) {
	if err := service.ValidateVariableUsePolicy(v); err != nil {
		return nil, err
	}
	w, err := p.beginBusinessWrite(ctx, p.tableVariables, "variables.write", "")
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()
	if v.WorkspaceID != "" && v.WorkspaceID != w.actor.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	if v.Secret && !w.actor.Allows("credentials.manage", service.AccessResource{WorkspaceID: w.actor.WorkspaceID}) {
		return nil, service.ErrAccessDenied
	}
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
	allowedTools, err := json.Marshal(v.AllowedTools)
	if err != nil {
		return nil, fmt.Errorf("marshal variable allowed tools: %w", err)
	}
	allowedHosts, err := json.Marshal(v.AllowedHosts)
	if err != nil {
		return nil, fmt.Errorf("marshal variable allowed hosts: %w", err)
	}

	query, _, err := p.goqu.Insert(p.tableVariables).Rows(
		goqu.Record{
			"workspace_id":  w.actor.WorkspaceID,
			"id":            id,
			"key":           v.Key,
			"value":         storeValue,
			"description":   v.Description,
			"secret":        v.Secret,
			"allowed_tools": types.RawJSON(allowedTools),
			"allowed_hosts": types.RawJSON(allowedHosts),
			"created_at":    now,
			"updated_at":    now,
			"created_by":    v.CreatedBy,
			"updated_by":    v.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert variable query: %w", err)
	}

	if _, err := w.tx.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create variable %q: %w", v.Key, err)
	}
	if err = w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit variable: %w", err)
	}

	return &service.Variable{
		WorkspaceID:  w.actor.WorkspaceID,
		ID:           id,
		Key:          v.Key,
		Value:        v.Value,
		Description:  v.Description,
		Secret:       v.Secret,
		AllowedTools: v.AllowedTools,
		AllowedHosts: v.AllowedHosts,
		CreatedAt:    now.Format(time.RFC3339),
		UpdatedAt:    now.Format(time.RFC3339),
		CreatedBy:    v.CreatedBy,
		UpdatedBy:    v.UpdatedBy,
	}, nil
}

func (p *Postgres) UpdateVariable(ctx context.Context, id string, v service.Variable) (*service.Variable, error) {
	if err := service.ValidateVariableUsePolicy(v); err != nil {
		return nil, err
	}
	w, err := p.beginBusinessWrite(ctx, p.tableVariables, "variables.write", id)
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()
	if v.WorkspaceID != "" && v.WorkspaceID != w.actor.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	var old variableRow
	found, err := w.tx.From(p.tableVariables).Where(w.predicate, goqu.C("id").Eq(id)).ForUpdate(goqu.Wait).ScanStructContext(ctx, &old)
	if err != nil {
		return nil, fmt.Errorf("lock variable: %w", err)
	}
	if !found {
		return nil, nil
	}
	if (v.Secret || old.Secret) && !w.actor.Allows("credentials.manage", service.AccessResource{WorkspaceID: w.actor.WorkspaceID, ID: id}) {
		return nil, service.ErrAccessDenied
	}
	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

	storeValue := v.Value
	if v.Value == "***" && old.Secret {
		if !v.Secret {
			return nil, service.ErrAccessDenied
		}
		storeValue = old.Value
	}
	if v.Secret && v.Value != "***" {
		enc, err := encryptVariableValue(v.Value, encKey)
		if err != nil {
			return nil, err
		}
		storeValue = enc
	}

	now := time.Now().UTC()
	allowedTools, err := json.Marshal(v.AllowedTools)
	if err != nil {
		return nil, fmt.Errorf("marshal variable allowed tools: %w", err)
	}
	allowedHosts, err := json.Marshal(v.AllowedHosts)
	if err != nil {
		return nil, fmt.Errorf("marshal variable allowed hosts: %w", err)
	}

	query, _, err := p.goqu.Update(p.tableVariables).Set(
		goqu.Record{
			"key":           v.Key,
			"value":         storeValue,
			"description":   v.Description,
			"secret":        v.Secret,
			"allowed_tools": types.RawJSON(allowedTools),
			"allowed_hosts": types.RawJSON(allowedHosts),
			"updated_at":    now,
			"updated_by":    v.UpdatedBy,
		},
	).Where(w.predicate, goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update variable query: %w", err)
	}

	res, err := w.tx.ExecContext(ctx, query)
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
	if err = w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit variable update: %w", err)
	}

	return p.GetVariable(ctx, id)
}

func (p *Postgres) DeleteVariable(ctx context.Context, id string) error {
	w, err := p.beginBusinessWrite(ctx, p.tableVariables, "variables.write", id)
	if err != nil {
		return err
	}
	defer w.tx.Rollback()
	query, _, err := p.goqu.Delete(p.tableVariables).
		Where(w.predicate, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete variable query: %w", err)
	}

	_, err = w.tx.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete variable %q: %w", id, err)
	}

	return w.tx.Commit()
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

	var allowedTools, allowedHosts []string
	if len(row.AllowedTools) > 0 {
		if err := json.Unmarshal(row.AllowedTools, &allowedTools); err != nil {
			return nil, fmt.Errorf("unmarshal variable allowed tools for %q: %w", row.Key, err)
		}
	}
	if len(row.AllowedHosts) > 0 {
		if err := json.Unmarshal(row.AllowedHosts, &allowedHosts); err != nil {
			return nil, fmt.Errorf("unmarshal variable allowed hosts for %q: %w", row.Key, err)
		}
	}

	return &service.Variable{
		WorkspaceID:  row.WorkspaceID,
		ID:           row.ID,
		Key:          row.Key,
		Value:        value,
		Description:  row.Description,
		Secret:       row.Secret,
		AllowedTools: allowedTools,
		AllowedHosts: allowedHosts,
		CreatedAt:    row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:    row.UpdatedAt.Format(time.RFC3339),
		CreatedBy:    row.CreatedBy,
		UpdatedBy:    row.UpdatedBy,
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
