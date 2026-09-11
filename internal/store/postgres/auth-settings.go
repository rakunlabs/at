package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/doug-martin/goqu/v9"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.AuthSettingsStorer = (*Postgres)(nil)

func authSettingsError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && (pg.Code == "23505" || pg.Code == "23514") {
		return service.ErrAuthConflict
	}
	return fmt.Errorf("authentication settings: %w", err)
}

func (p *Postgres) InitializeAuthSettings(ctx context.Context, settings service.AuthSettings) (*service.AuthSettingsState, error) {
	// Read first so obsolete bootstrap values never invalidate an existing policy.
	state, err := p.GetAuthSettings(ctx)
	if err != nil {
		return nil, err
	}
	if state != nil {
		return state, nil
	}
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, authSettingsError(err)
	}
	defer tx.Rollback()
	var claimed bool
	found, err := tx.From(p.tableAuthBootstrap).Select("claimed").ForUpdate(goqu.Wait).ScanValContext(ctx, &claimed)
	if err != nil {
		return nil, authSettingsError(err)
	}
	if !found {
		return nil, fmt.Errorf("authentication bootstrap latch unavailable")
	}
	if err := settings.Validate(!claimed); err != nil {
		return nil, err
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("encode imported auth settings: %w", err)
	}
	_, err = tx.Insert(p.externalTable("auth_settings")).Rows(goqu.Record{"singleton": true, "version": 1, "config": string(data)}).OnConflict(goqu.DoNothing()).Executor().ExecContext(ctx)
	if err != nil {
		return nil, authSettingsError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, authSettingsError(err)
	}
	return p.GetAuthSettings(ctx)
}

func (p *Postgres) GetAuthSettings(ctx context.Context) (*service.AuthSettingsState, error) {
	var row struct {
		Version int64  `db:"version"`
		Config  string `db:"config"`
		Claimed bool   `db:"claimed"`
	}
	found, err := p.goqu.From(p.externalTable("auth_settings").As("s")).Join(p.tableAuthBootstrap.As("b"), goqu.On(goqu.I("s.singleton").Eq(goqu.I("b.singleton")))).Select("s.version", "s.config", "b.claimed").ScanStructContext(ctx, &row)
	if err != nil {
		return nil, authSettingsError(err)
	}
	if !found {
		return nil, nil
	}
	state := &service.AuthSettingsState{SetupRequired: !row.Claimed}
	if err := json.Unmarshal([]byte(row.Config), &state.Settings); err != nil {
		return nil, fmt.Errorf("decode auth settings: %w", err)
	}
	state.Settings.Version = row.Version
	if err := state.Settings.Validate(!row.Claimed); err != nil {
		return nil, fmt.Errorf("invalid persisted auth settings: %w", err)
	}
	return state, nil
}

func (p *Postgres) SaveAuthSettings(ctx context.Context, settings service.AuthSettings) (*service.AuthSettings, error) {
	if err := settings.Validate(false); err != nil {
		return nil, err
	}
	// Origin and version are compared in the write itself, not count-then-write.
	expected := settings.Version
	settings.Version++
	data, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("encode auth settings: %w", err)
	}
	result, err := p.goqu.Update(p.externalTable("auth_settings")).Set(goqu.Record{"version": settings.Version, "config": string(data)}).Where(goqu.Ex{"singleton": true, "version": expected}, goqu.L("config->>'origin' = ?", settings.Origin), goqu.L("EXISTS (?)", p.goqu.From(p.tableAuthBootstrap).Select(goqu.L("1")).Where(goqu.Ex{"claimed": true}))).Executor().ExecContext(ctx)
	if err != nil {
		return nil, authSettingsError(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, authSettingsError(err)
	}
	if n != 1 {
		return nil, service.ErrAuthConflict
	}
	return &settings, nil
}

func (p *Postgres) SetupAuth(ctx context.Context, user service.AuthUser, settings service.AuthSettings) (*service.AuthUser, error) {
	if err := settings.Validate(false); err != nil {
		return nil, err
	}
	if user.PasswordHash == "" || user.Username == "" || !settings.LocalLoginEnabled {
		return nil, service.ErrAuthConflict
	}
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, authSettingsError(err)
	}
	defer tx.Rollback()
	result, err := tx.Update(p.tableAuthBootstrap).Set(goqu.Record{"claimed": true}).Where(goqu.Ex{"singleton": true, "claimed": false}).Executor().ExecContext(ctx)
	if err != nil {
		return nil, authSettingsError(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, authSettingsError(err)
	}
	if n != 1 {
		return nil, service.ErrAuthConflict
	}
	settings.Version++
	data, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("encode setup settings: %w", err)
	}
	result, err = tx.Update(p.externalTable("auth_settings")).Set(goqu.Record{"version": settings.Version, "config": string(data)}).Where(goqu.Ex{"singleton": true, "version": settings.Version - 1}, goqu.Or(goqu.L("config->>'origin' = ''"), goqu.L("config->>'origin' = ?", settings.Origin))).Executor().ExecContext(ctx)
	if err != nil {
		return nil, authSettingsError(err)
	}
	n, err = result.RowsAffected()
	if err != nil {
		return nil, authSettingsError(err)
	}
	if n != 1 {
		return nil, service.ErrAuthConflict
	}
	user.ID, user.Admin, user.Disabled, user.SessionVersion = ulid.Make().String(), true, false, 0
	_, err = tx.Insert(p.tableAuthUsers).Rows(goqu.Record{"id": user.ID, "username": user.Username, "password_hash": user.PasswordHash, "admin": true}).Executor().ExecContext(ctx)
	if err != nil {
		return nil, authSettingsError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, authSettingsError(err)
	}
	return &user, nil
}
