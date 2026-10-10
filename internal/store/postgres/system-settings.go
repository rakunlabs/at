package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.SystemSettingsStorer = (*Postgres)(nil)

func (p *Postgres) GetSystemSettings(ctx context.Context) (*service.SystemSettings, error) {
	if !service.HasExecutionMaintenance(ctx) {
		if _, err := p.webhookServerAdmin(ctx); err != nil {
			return nil, err
		}
	}
	var row struct {
		Version int64  `db:"version"`
		Config  []byte `db:"config"`
	}
	found, err := p.goqu.From(p.workspaceTable("system_settings")).Select("version", "config").Where(goqu.Ex{"singleton": true}).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("load system settings: %w", err)
	}
	if !found {
		return nil, nil
	}
	s := service.DefaultSystemSettings()
	if err := json.Unmarshal(row.Config, &s); err != nil {
		return nil, fmt.Errorf("decode system settings: %w", err)
	}
	s.Version = row.Version
	return &s, nil
}

func (p *Postgres) SaveSystemSettings(ctx context.Context, s service.SystemSettings) (*service.SystemSettings, error) {
	if _, err := p.webhookServerAdmin(ctx); err != nil {
		return nil, err
	}
	if err := s.Normalize(); err != nil {
		return nil, err
	}
	expected := s.Version
	s.Version++
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("encode system settings: %w", err)
	}
	table := p.workspaceTable("system_settings")
	record := goqu.Record{"singleton": true, "version": s.Version, "config": goqu.L("?::jsonb", string(raw))}
	var n int64
	if expected == 0 {
		result, err := p.goqu.Insert(table).Rows(record).OnConflict(goqu.DoNothing()).Executor().ExecContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("insert system settings: %w", err)
		}
		n, err = result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("count system settings writes: %w", err)
		}
	} else {
		result, err := p.goqu.Update(table).Set(record).Where(goqu.Ex{"singleton": true, "version": expected}).Executor().ExecContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("update system settings: %w", err)
		}
		n, err = result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("count system settings writes: %w", err)
		}
	}
	if n != 1 {
		return nil, service.ErrSystemSettingsConflict
	}
	return &s, nil
}
