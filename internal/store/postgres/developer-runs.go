package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/doug-martin/goqu/v9/exp"

	"github.com/rakunlabs/at/internal/service"
)

func developerRunWriteCondition(ctx context.Context) exp.Expression {
	if id := service.DeveloperRunFromContext(ctx); id != "" {
		return goqu.And(goqu.Ex{"active_run_id": id, "active_run_cancel_requested": false}, goqu.I("active_run_heartbeat_at").Gt(goqu.L("clock_timestamp() - interval '60 seconds'")))
	}
	// External cancellation uses CancelDeveloperRun; ordinary runtime writes
	// cannot bypass a live receipt, even when writing the cancelled status.
	return goqu.Ex{"active_run_id": ""}
}

// Lock the session before dependent history/pending writes. Cancellation and
// release update this same row, so a checked write cannot slip past either.
func (p *Postgres) lockDeveloperRunWrite(ctx context.Context, tx *goqu.TxDatabase, actor service.AccessPrincipal, sessionID string) error {
	var id string
	found, err := tx.From(p.tableDeveloperSessions).Select("id").Where(
		developerOwned(actor, goqu.Ex{"id": sessionID}), developerRunWriteCondition(ctx),
	).ForUpdate(goqu.Wait).ScanValContext(ctx, &id)
	if err != nil {
		return fmt.Errorf("lock developer run write: %w", err)
	}
	if !found {
		return service.ErrDeveloperSessionBusy
	}
	return nil
}

func (p *Postgres) AcquireDeveloperRun(ctx context.Context, sessionID, runID string) error {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return err
	}
	if runID == "" || len(runID) > 128 {
		return fmt.Errorf("invalid developer run id")
	}
	session, err := p.GetDeveloperSession(ctx, sessionID)
	if err != nil {
		return err
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin developer run acquisition: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := p.lockDeveloperSpace(ctx, tx, actor, session.SpaceID); err != nil {
		return err
	}
	var suspended bool
	if _, err := tx.From(p.tableDeveloperSpaces).Select("execution_suspended").Where(goqu.Ex{"id": session.SpaceID}).ScanValContext(ctx, &suspended); err != nil {
		return fmt.Errorf("check developer space admission: %w", err)
	}
	if suspended {
		return fmt.Errorf("space stopped; start it explicitly before running work: %w", service.ErrDeveloperSessionBusy)
	}
	result, err := tx.Update(p.tableDeveloperSessions).Set(goqu.Record{
		"active_run_id": runID, "active_run_heartbeat_at": goqu.L("clock_timestamp()"), "active_run_cancel_requested": false,
	}).Where(developerOwned(actor, goqu.Ex{"id": sessionID, "active_run_id": ""}), goqu.I("status").Neq(service.DeveloperSessionRunning)).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("acquire developer run: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read developer run acquisition: %w", err)
	}
	if count == 0 {
		// The owner was resolved before BeginTx. Do not acquire a second pool
		// connection while holding the parent lock: concurrent waiters can fill
		// the pool and deadlock the lock holder's not-found probe.
		return service.ErrDeveloperSessionBusy
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit developer run acquisition: %w", err)
	}
	return nil
}

// Space mutation and run acquisition share the parent lock, always acquired
// before a session lock. Deleting/reconfiguring cannot erase a live or stale
// receipt; a stale heartbeat is not evidence that remote work terminated.
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

func (p *Postgres) checkDeveloperSpaceRuns(ctx context.Context, tx *goqu.TxDatabase, actor service.AccessPrincipal, spaceID string) error {
	var controlID string
	if _, err := tx.From(p.tableDeveloperSpaces).Select("active_control_id").Where(developerOwned(actor, goqu.Ex{"id": spaceID})).ScanValContext(ctx, &controlID); err != nil {
		return fmt.Errorf("check developer space control: %w", err)
	}
	if controlID != "" && controlID != service.DeveloperSpaceControlFromContext(ctx) {
		return service.ErrDeveloperSessionBusy
	}
	var id string
	found, err := tx.From(p.tableDeveloperSessions).Select("id").Where(developerOwned(actor, goqu.Ex{"space_id": spaceID}),
		goqu.Or(goqu.I("active_run_id").Neq(""), goqu.Ex{"status": service.DeveloperSessionRunning})).Limit(1).ScanValContext(ctx, &id)
	if err != nil {
		return fmt.Errorf("check developer space runs: %w", err)
	}
	if found {
		return service.ErrDeveloperSessionBusy
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
		return fmt.Errorf("space has an unresolved control operation: %w", service.ErrDeveloperSessionBusy)
	}
	if operation == "start" {
		if err := p.checkDeveloperSpaceRuns(ctx, tx, actor, spaceID); err != nil {
			return err
		}
	}
	if _, err := tx.Update(p.tableDeveloperSpaces).Set(goqu.Record{"execution_suspended": true, "active_control_id": controlID, "updated_at": goqu.L("clock_timestamp()")}).
		Where(developerOwned(actor, goqu.Ex{"id": spaceID})).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("set developer space suspension: %w", err)
	}
	if operation != "start" {
		if _, err := tx.Update(p.tableDeveloperSessions).Set(goqu.Record{
			"active_run_cancel_requested": goqu.L("active_run_id <> ''"),
			"status":                      service.DeveloperSessionCancelled, "error": "cancelled by space stop/reset",
			"finished_at": goqu.L("clock_timestamp()"), "updated_at": goqu.L("clock_timestamp()"),
		}).Where(developerOwned(actor, goqu.Ex{"space_id": spaceID}), goqu.Or(goqu.I("active_run_id").Neq(""),
			goqu.I("status").In(service.DeveloperSessionRunning, service.DeveloperSessionWaitingPermission, service.DeveloperSessionWaitingQuestion))).Executor().ExecContext(ctx); err != nil {
			return fmt.Errorf("cancel developer space runs: %w", err)
		}
		if _, err := tx.Delete(p.tableDeveloperPendingTools).Where(developerOwned(actor, goqu.Ex{"session_id": tx.From(p.tableDeveloperSessions).Select("id").Where(developerOwned(actor, goqu.Ex{"space_id": spaceID}))})).Executor().ExecContext(ctx); err != nil {
			return fmt.Errorf("cancel developer space approvals: %w", err)
		}
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

func (p *Postgres) HeartbeatDeveloperRun(ctx context.Context, sessionID, runID string) error {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return err
	}
	result, err := p.goqu.Update(p.tableDeveloperSessions).Set(goqu.Record{"active_run_heartbeat_at": goqu.L("clock_timestamp()")}).
		Where(developerOwned(actor, goqu.Ex{"id": sessionID, "active_run_id": runID, "active_run_cancel_requested": false}),
			goqu.I("active_run_heartbeat_at").Gt(goqu.L("clock_timestamp() - interval '60 seconds'"))).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("heartbeat developer run: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read developer run heartbeat: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("developer run ownership lost or cancelled: %w", service.ErrDeveloperSessionBusy)
	}
	return nil
}

func (p *Postgres) ReleaseDeveloperRun(ctx context.Context, sessionID, runID string) error {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return err
	}
	// The owner may finish cancellation, but cannot overwrite a cancellation
	// recorded by another request or claim that an unexplained running exit succeeded.
	_, err = p.goqu.Update(p.tableDeveloperSessions).Set(goqu.Record{
		"status":        goqu.L("CASE WHEN active_run_cancel_requested THEN ? WHEN status = ? THEN ? ELSE status END", service.DeveloperSessionCancelled, service.DeveloperSessionRunning, service.DeveloperSessionFailed),
		"error":         goqu.L("CASE WHEN active_run_cancel_requested THEN 'cancelled by user' WHEN status = ? THEN 'Run interrupted; inspect saved history and files before starting new work' ELSE error END", service.DeveloperSessionRunning),
		"finished_at":   goqu.L("CASE WHEN active_run_cancel_requested OR status = ? THEN clock_timestamp() ELSE finished_at END", service.DeveloperSessionRunning),
		"active_run_id": "", "active_run_heartbeat_at": nil, "active_run_cancel_requested": false,
		"updated_at": goqu.L("clock_timestamp()"),
	}).Where(developerOwned(actor, goqu.Ex{"id": sessionID, "active_run_id": runID})).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("release developer run: %w", err)
	}
	return nil
}

func (p *Postgres) CancelDeveloperRun(ctx context.Context, sessionID string) error {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return err
	}
	if _, err := p.GetDeveloperSession(ctx, sessionID); err != nil {
		return err
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin developer cancellation: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	_, err = tx.Update(p.tableDeveloperSessions).Set(goqu.Record{
		"active_run_cancel_requested": goqu.L("active_run_id <> ''"),
		"status":                      service.DeveloperSessionCancelled, "error": "cancelled by user",
		"finished_at": goqu.L("clock_timestamp()"), "updated_at": goqu.L("clock_timestamp()"),
	}).Where(developerOwned(actor, goqu.Ex{"id": sessionID})).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("cancel developer run: %w", err)
	}
	if _, err := tx.Delete(p.tableDeveloperPendingTools).Where(developerOwned(actor, goqu.Ex{"session_id": sessionID})).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("cancel pending developer tools: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit developer cancellation: %w", err)
	}
	return nil
}

func (p *Postgres) GetDeveloperRun(ctx context.Context, sessionID string) (*service.DeveloperRun, error) {
	actor, err := developerSpaceActor(ctx)
	if err != nil {
		return nil, err
	}
	// Report interruption without clearing the receipt: neither a timeout nor
	// GET proves an old process/remote tool has stopped. Waiting approvals stay intact.
	_, err = p.goqu.Update(p.tableDeveloperSessions).Set(goqu.Record{
		"status": service.DeveloperSessionFailed, "error": "Run interrupted: owner heartbeat expired. Verify the old process and tools have stopped, inspect history/files, then use a new session. No tools were replayed.",
		"finished_at": goqu.L("clock_timestamp()"), "updated_at": goqu.L("clock_timestamp()"),
	}).Where(developerOwned(actor, goqu.Ex{"id": sessionID, "status": service.DeveloperSessionRunning}),
		goqu.I("active_run_id").Neq(""), goqu.I("active_run_heartbeat_at").Lt(goqu.L("clock_timestamp() - interval '60 seconds'"))).Executor().ExecContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("recover interrupted developer run: %w", err)
	}
	var row struct {
		ID          string       `db:"active_run_id"`
		Heartbeat   sql.NullTime `db:"active_run_heartbeat_at"`
		Cancelled   bool         `db:"active_run_cancel_requested"`
		Interrupted bool         `db:"interrupted"`
	}
	found, err := p.goqu.From(p.tableDeveloperSessions).Select("active_run_id", "active_run_heartbeat_at", "active_run_cancel_requested",
		goqu.L("COALESCE(active_run_heartbeat_at < clock_timestamp() - interval '60 seconds', false)").As("interrupted")).
		Where(developerOwned(actor, goqu.Ex{"id": sessionID})).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("read developer run: %w", err)
	}
	if !found {
		return nil, service.ErrDeveloperSpaceNotFound
	}
	if row.ID == "" {
		return nil, nil
	}
	run := &service.DeveloperRun{ID: row.ID, CancelRequested: row.Cancelled, Interrupted: row.Interrupted}
	if row.Heartbeat.Valid {
		run.HeartbeatAt = row.Heartbeat.Time.UTC().Format(time.RFC3339Nano)
	}
	return run, nil
}
