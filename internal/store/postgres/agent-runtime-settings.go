package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

func (p *Postgres) GetAgentRuntimeSettings(ctx context.Context) (*service.AgentRuntimeSettings, error) {
	settings := service.DefaultAgentRuntimeSettings()
	found, err := p.goqu.From(p.tableAgentRuntimeSettings).
		Select("version", "max_background_subagents_per_owner").
		Where(goqu.Ex{"singleton": true}).
		ScanStructContext(ctx, &settings)
	if err != nil {
		return nil, fmt.Errorf("get agent runtime settings: %w", err)
	}
	if !found {
		settings = service.DefaultAgentRuntimeSettings()
	}
	return &settings, nil
}

func (p *Postgres) SaveAgentRuntimeSettings(ctx context.Context, settings service.AgentRuntimeSettings) (*service.AgentRuntimeSettings, error) {
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	expected := settings.Version
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin agent runtime settings update: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rolled back unless committed

	var current int64
	found, err := tx.From(p.tableAgentRuntimeSettings).Select("version").Where(goqu.Ex{"singleton": true}).ForUpdate(goqu.Wait).ScanValContext(ctx, &current)
	if err != nil {
		return nil, fmt.Errorf("lock agent runtime settings: %w", err)
	}
	if (found && current != expected) || (!found && expected != service.DefaultAgentRuntimeSettings().Version) {
		return nil, service.ErrAgentRuntimeSettingsConflict
	}

	settings.Version = expected + 1
	record := goqu.Record{
		"singleton":                          true,
		"version":                            settings.Version,
		"max_background_subagents_per_owner": settings.MaxBackgroundSubagentsPerOwner,
	}
	if !found {
		_, err = tx.Insert(p.tableAgentRuntimeSettings).Rows(record).Executor().ExecContext(ctx)
	} else {
		var result sql.Result
		result, err = tx.Update(p.tableAgentRuntimeSettings).Set(record).
			Where(goqu.Ex{"singleton": true, "version": expected}).Executor().ExecContext(ctx)
		if err == nil {
			var affected int64
			affected, err = result.RowsAffected()
			if err == nil && affected != 1 {
				err = service.ErrAgentRuntimeSettingsConflict
			}
		}
	}
	if err != nil {
		if errors.Is(err, service.ErrAgentRuntimeSettingsConflict) {
			return nil, err
		}
		return nil, fmt.Errorf("save agent runtime settings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit agent runtime settings: %w", err)
	}
	return &settings, nil
}
