package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.TerminalStorer = (*Postgres)(nil)

func (p *Postgres) ListTerminals(ctx context.Context, owner string) ([]service.TerminalSession, error) {
	items := []service.TerminalSession{}
	err := p.goqu.From(p.authSecurityTable("terminal_sessions")).Where(goqu.Ex{"owner_id": owner}).Order(goqu.I("position").Asc(), goqu.I("id").Asc()).ScanStructsContext(ctx, &items)
	if err != nil {
		return nil, fmt.Errorf("list terminals: %w", err)
	}
	return items, nil
}

func (p *Postgres) GetTerminal(ctx context.Context, owner, id string) (*service.TerminalSession, error) {
	var item service.TerminalSession
	found, err := p.goqu.From(p.authSecurityTable("terminal_sessions")).Where(goqu.Ex{"owner_id": owner, "id": id}).ScanStructContext(ctx, &item)
	if err != nil {
		return nil, fmt.Errorf("get terminal: %w", err)
	}
	if !found {
		return nil, nil
	}
	return &item, nil
}

func (p *Postgres) CreateTerminal(ctx context.Context, item service.TerminalSession) error {
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin terminal create: %w", err)
	}
	defer tx.Rollback()
	var owner string
	found, err := tx.From(p.tableAuthUsers).Select("id").Where(goqu.Ex{"id": item.OwnerID, "admin": true, "disabled": false}).ForUpdate(goqu.Wait).ScanValContext(ctx, &owner)
	if err != nil {
		return fmt.Errorf("lock terminal owner: %w", err)
	}
	if !found {
		return fmt.Errorf("active administrator required")
	}
	count, err := tx.From(p.authSecurityTable("terminal_sessions")).Where(goqu.Ex{"owner_id": owner}).CountContext(ctx)
	if err != nil {
		return fmt.Errorf("count saved terminals: %w", err)
	}
	if count >= 32 {
		return fmt.Errorf("limit of 32 saved terminals reached")
	}
	_, err = tx.Insert(p.authSecurityTable("terminal_sessions")).Rows(item).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("create terminal: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit terminal create: %w", err)
	}
	return nil
}

func (p *Postgres) UpdateTerminal(ctx context.Context, owner, id, title string, position int) error {
	_, err := p.goqu.Update(p.authSecurityTable("terminal_sessions")).Set(goqu.Record{"title": title, "position": position}).Where(goqu.Ex{"owner_id": owner, "id": id}).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("update terminal: %w", err)
	}
	return nil
}

func (p *Postgres) DeleteTerminal(ctx context.Context, owner, id string) error {
	_, err := p.goqu.Delete(p.authSecurityTable("terminal_sessions")).Where(goqu.Ex{"owner_id": owner, "id": id}).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("delete terminal: %w", err)
	}
	return nil
}

func (p *Postgres) OperateTerminal(ctx context.Context, owner, id string, remove bool, run func(service.TerminalSession) error) error {
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin terminal operation: %w", err)
	}
	defer tx.Rollback()
	var item service.TerminalSession
	found, err := tx.From(p.authSecurityTable("terminal_sessions")).Where(goqu.Ex{"owner_id": owner, "id": id}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &item)
	if err != nil {
		return fmt.Errorf("lock terminal: %w", err)
	}
	if !found {
		return fmt.Errorf("terminal not found")
	}
	if err := run(item); err != nil {
		return err
	}
	if remove {
		if _, err := tx.Delete(p.authSecurityTable("terminal_sessions")).Where(goqu.Ex{"owner_id": owner, "id": id}).Executor().ExecContext(ctx); err != nil {
			return fmt.Errorf("remove stopped terminal: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit terminal operation: %w", err)
	}
	return nil
}

func (p *Postgres) GetTerminalPreferences(ctx context.Context, owner string) (service.TerminalPreferences, error) {
	item := service.TerminalPreferences{DefaultUsers: map[string]string{}}
	var data string
	found, err := p.goqu.From(p.authSecurityTable("terminal_preferences")).Select("data").Where(goqu.Ex{"owner_id": owner}).ScanValContext(ctx, &data)
	if err != nil {
		return item, fmt.Errorf("get terminal preferences: %w", err)
	}
	if found {
		err = json.Unmarshal([]byte(data), &item)
	}
	return item, err
}

func (p *Postgres) SetTerminalPreferences(ctx context.Context, owner string, item service.TerminalPreferences) error {
	data, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("encode terminal preferences: %w", err)
	}
	_, err = p.goqu.Insert(p.authSecurityTable("terminal_preferences")).Rows(goqu.Record{"owner_id": owner, "data": string(data)}).OnConflict(goqu.DoUpdate("owner_id", goqu.Record{"data": string(data)})).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("save terminal preferences: %w", err)
	}
	return nil
}
