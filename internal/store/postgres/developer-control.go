package postgres

import (
	"context"
	"fmt"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

func developerOwned(actor service.AccessPrincipal, extra goqu.Ex) goqu.Ex {
	where := goqu.Ex{"workspace_id": actor.WorkspaceID, "owner_user_id": actor.UserID}
	for key, value := range extra {
		where[key] = value
	}
	return where
}

// Space mutation and control acquisition share this row lock.
func (p *Postgres) lockDeveloperSpace(ctx context.Context, tx *goqu.TxDatabase, actor service.AccessPrincipal, spaceID string) error {
	var id string
	found, err := tx.From(p.tableDeveloperSpaces).Select("id").Where(developerOwned(actor, goqu.Ex{"id": spaceID})).ForUpdate(goqu.Wait).ScanValContext(ctx, &id)
	if err != nil {
		return fmt.Errorf("lock developer space: %w", err)
	}
	if !found {
		return service.ErrDeveloperSpaceNotFound
	}
	return nil
}

// checkDeveloperSpaceControl refuses a write while another Start/Stop/Reset
// owns the space. The control's own owner (on the context) may proceed.
func (p *Postgres) checkDeveloperSpaceControl(ctx context.Context, tx *goqu.TxDatabase, actor service.AccessPrincipal, spaceID string) error {
	var controlID string
	if _, err := tx.From(p.tableDeveloperSpaces).Select("active_control_id").Where(developerOwned(actor, goqu.Ex{"id": spaceID})).ScanValContext(ctx, &controlID); err != nil {
		return fmt.Errorf("check developer space control: %w", err)
	}
	if controlID != "" && controlID != service.DeveloperSpaceControlFromContext(ctx) {
		return service.ErrDeveloperSpaceBusy
	}
	return nil
}

func (p *Postgres) AcquireDeveloperSpaceControl(ctx context.Context, spaceID, controlID, operation string) error {
	if controlID == "" || len(controlID) > 128 || (operation != "start" && operation != "stop" && operation != "reset") {
		return fmt.Errorf("invalid developer space control")
	}
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return err
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin developer space suspension: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := p.lockDeveloperSpace(ctx, tx, actor, spaceID); err != nil {
		return err
	}
	var current string
	if _, err := tx.From(p.tableDeveloperSpaces).Select("active_control_id").Where(goqu.Ex{"id": spaceID}).ScanValContext(ctx, &current); err != nil {
		return fmt.Errorf("check developer space control: %w", err)
	}
	if current != "" {
		return service.ErrDeveloperSpaceBusy
	}
	if _, err := tx.Update(p.tableDeveloperSpaces).Set(goqu.Record{"execution_suspended": true, "active_control_id": controlID, "updated_at": goqu.L("clock_timestamp()")}).
		Where(developerOwned(actor, goqu.Ex{"id": spaceID})).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("set developer space suspension: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit developer space suspension: %w", err)
	}
	return nil
}

func (p *Postgres) ReleaseDeveloperSpaceControl(ctx context.Context, spaceID, controlID string, suspended bool) error {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return err
	}
	if controlID == "" {
		return fmt.Errorf("developer space control id is required")
	}
	_, err = p.goqu.Update(p.tableDeveloperSpaces).Set(goqu.Record{
		"execution_suspended": suspended, "active_control_id": "", "updated_at": goqu.L("clock_timestamp()"),
	}).Where(developerOwned(actor, goqu.Ex{"id": spaceID, "active_control_id": controlID})).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("release developer space control: %w", err)
	}
	return nil
}
