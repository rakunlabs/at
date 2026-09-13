package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.AuthLoginEventStorer = (*Postgres)(nil)

type authLoginEventRow struct {
	ID        string    `db:"id"`
	UserID    string    `db:"user_id"`
	Action    string    `db:"action"`
	SourceIP  string    `db:"source_ip"`
	UserAgent string    `db:"user_agent"`
	ActorID   string    `db:"actor_id"`
	CreatedAt time.Time `db:"created_at"`
}

func authLoginEventRowToRecord(row authLoginEventRow) service.AuthLoginEvent {
	return service.AuthLoginEvent{ID: row.ID, UserID: row.UserID, Action: row.Action, SourceIP: row.SourceIP, UserAgent: row.UserAgent, ActorID: row.ActorID, CreatedAt: row.CreatedAt}
}

func (p *Postgres) RecordAuthLoginEvent(ctx context.Context, event service.AuthLoginEvent) error {
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin login event: %w", err)
	}
	defer tx.Rollback()
	var recorded authLoginEventRow
	if _, err := tx.Insert(p.authSecurityTable("auth_login_events")).Rows(goqu.Record{
		"id": ulid.Make().String(), "user_id": event.UserID, "action": event.Action,
		"source_ip": event.SourceIP, "user_agent": event.UserAgent, "actor_id": event.ActorID,
	}).Returning("created_at").Executor().ScanStructContext(ctx, &recorded); err != nil {
		return fmt.Errorf("record login event: %w", err)
	}
	if event.Action == "login_success" {
		if _, err := tx.Update(p.tableAuthUsers).Set(goqu.Record{"last_login_at": recorded.CreatedAt, "last_login_ip": event.SourceIP}).Where(goqu.Ex{"id": event.UserID}, goqu.Or(goqu.I("last_login_at").IsNull(), goqu.I("last_login_at").Lt(recorded.CreatedAt))).Executor().ExecContext(ctx); err != nil {
			return fmt.Errorf("update last login: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit login event: %w", err)
	}
	return nil
}

func (p *Postgres) ListAuthLoginEvents(ctx context.Context, userID string, limit uint) ([]service.AuthLoginEvent, error) {
	if limit == 0 || limit > 100 {
		limit = 50
	}
	var rows []authLoginEventRow
	if err := p.goqu.From(p.authSecurityTable("auth_login_events")).Select("id", "user_id", "action", "source_ip", "user_agent", "actor_id", "created_at").Where(goqu.Ex{"user_id": userID}).Order(goqu.I("created_at").Desc(), goqu.I("id").Desc()).Limit(limit).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list login events: %w", err)
	}
	result := make([]service.AuthLoginEvent, 0, len(rows))
	for _, row := range rows {
		result = append(result, authLoginEventRowToRecord(row))
	}
	return result, nil
}

func (p *Postgres) ListAuthLastLogins(ctx context.Context, ids []string) (map[string]service.AuthLastLogin, error) {
	result := make(map[string]service.AuthLastLogin)
	if len(ids) == 0 {
		return result, nil
	}
	var rows []struct {
		UserID   string    `db:"id"`
		At       time.Time `db:"last_login_at"`
		SourceIP string    `db:"last_login_ip"`
	}
	if err := p.goqu.From(p.tableAuthUsers).Select("id", "last_login_at", "last_login_ip").Where(goqu.I("id").In(ids), goqu.I("last_login_at").IsNotNull()).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list last logins: %w", err)
	}
	for _, row := range rows {
		result[row.UserID] = service.AuthLastLogin{At: row.At, SourceIP: row.SourceIP}
	}
	return result, nil
}

func (p *Postgres) CleanupAuthLoginEvents(ctx context.Context, limit uint) error {
	if limit == 0 || limit > 500 {
		limit = 500
	}
	table := p.authSecurityTable("auth_login_events")
	expired := p.goqu.From(table).Select("id").Where(goqu.I("created_at").Lt(goqu.L("clock_timestamp() - interval '90 days'"))).Order(goqu.I("created_at").Asc()).Limit(limit)
	if _, err := p.goqu.Delete(table).Where(goqu.I("id").In(expired)).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("expire login events: %w", err)
	}
	return nil
}
