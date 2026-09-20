package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/doug-martin/goqu/v9"

	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
)

func encodeTraceExport(c service.TraceExportSettings, key []byte) (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("encode trace export settings: %w", err)
	}
	if key == nil {
		return string(b), nil
	}
	return atcrypto.Encrypt(string(b), key)
}

func decodeTraceExport(raw string, key []byte) (service.TraceExportSettings, error) {
	c := service.DefaultTraceExportSettings()
	if atcrypto.IsEncrypted(raw) {
		var err error
		raw, err = atcrypto.Decrypt(raw, key)
		if err != nil {
			return c, fmt.Errorf("decrypt trace export settings: %w", err)
		}
	}
	err := json.Unmarshal([]byte(raw), &c)
	return c, err
}

func (p *Postgres) LoadTraceExportSettings(ctx context.Context, workspace string) (service.TraceExportSettings, error) {
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	c := service.DefaultTraceExportSettings()
	if workspace == "" {
		return c, service.ErrWorkspaceRequired
	}
	var row struct {
		Config  string `db:"config"`
		Version int64  `db:"version"`
	}
	// An archived workspace must not continue exporting queued observations.
	t := p.workspaceTable("trace_export_settings").As("t")
	w := p.workspaceTable("workspaces").As("w")
	found, err := p.goqu.From(t).Join(w, goqu.On(goqu.I("t.workspace_id").Eq(goqu.I("w.id")))).Select(goqu.I("t.config"), goqu.I("t.version")).Where(goqu.Ex{"w.id": workspace, "w.archived": false}).ScanStructContext(ctx, &row)
	if err != nil {
		return c, fmt.Errorf("load trace export settings: %w", err)
	}
	if !found {
		return c, nil
	}
	c, err = decodeTraceExport(row.Config, p.encKey)
	c.Version = row.Version
	return c, err
}

func (p *Postgres) SaveTraceExportSettings(ctx context.Context, c service.TraceExportSettings) (service.TraceExportSettings, error) {
	if err := c.Validate(); err != nil {
		return c, err
	}
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return c, fmt.Errorf("begin trace export save: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "workspace.write")
	if err != nil {
		return c, err
	}
	expected := c.Version
	c.Version++
	raw, err := encodeTraceExport(c, p.encKey)
	if err != nil {
		return c, err
	}
	table := p.workspaceTable("trace_export_settings")
	var result sql.Result
	if expected == 0 {
		result, err = tx.Insert(table).Rows(goqu.Record{"workspace_id": a.WorkspaceID, "version": c.Version, "config": raw}).OnConflict(goqu.DoNothing()).Executor().ExecContext(ctx)
	} else {
		result, err = tx.Update(table).Set(goqu.Record{"version": c.Version, "config": raw}).Where(goqu.Ex{"workspace_id": a.WorkspaceID, "version": expected}).Executor().ExecContext(ctx)
	}
	if err != nil {
		return c, fmt.Errorf("save trace export settings: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return c, err
	}
	if n != 1 {
		return c, service.ErrTraceExportConflict
	}
	return c, tx.Commit()
}

func (p *Postgres) rotateTraceExportKey(ctx context.Context, tx *sql.Tx, oldKey, newKey []byte) error {
	table := p.workspaceTable("trace_export_settings")
	q, _, err := p.goqu.From(table).Select("workspace_id", "config").ForUpdate(goqu.Wait).ToSQL()
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, q)
	if err != nil {
		return err
	}
	values := map[string]string{}
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		values[id] = raw
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for id, raw := range values {
		c, err := decodeTraceExport(raw, oldKey)
		if err != nil {
			return err
		}
		raw, err = encodeTraceExport(c, newKey)
		if err != nil {
			return err
		}
		q, _, err := p.goqu.Update(table).Set(goqu.Record{"config": raw}).Where(goqu.Ex{"workspace_id": id}).ToSQL()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}
