package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.PlaygroundRunStorer = (*Postgres)(nil)

var playgroundRunStale = goqu.L("clock_timestamp() - interval '" + service.PlaygroundRunStaleAfter + "'")

// AcquirePlaygroundRun takes the conversation for a background run. A live
// receipt held by another run refuses; a stale one is taken over, and the
// previous owner's writes are fenced by run ID from then on.
func (p *Postgres) AcquirePlaygroundRun(ctx context.Context, owner, id, runID, workspaceID string) error {
	if runID == "" || len(runID) > 128 {
		return fmt.Errorf("invalid playground run id: %w", service.ErrPlaygroundConflict)
	}
	return p.playgroundLocked(ctx, owner, id, func(tx *goqu.TxDatabase, _ *playgroundConversationRow) error {
		result, err := tx.Update(p.tablePlaygroundChats).Set(goqu.Record{
			"active_run_id":               runID,
			"active_run_workspace_id":     workspaceID,
			"active_run_heartbeat_at":     goqu.L("clock_timestamp()"),
			"active_run_cancel_requested": false,
		}).Where(goqu.Ex{"id": id, "owner_user_id": owner}, goqu.Or(
			goqu.Ex{"active_run_id": ""},
			goqu.I("active_run_heartbeat_at").IsNull(),
			goqu.I("active_run_heartbeat_at").Lt(playgroundRunStale),
		)).Executor().ExecContext(ctx)
		if err != nil {
			return fmt.Errorf("acquire playground run: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read playground run acquisition: %w", err)
		}
		if count == 0 {
			return service.ErrPlaygroundRunBusy
		}
		return nil
	})
}

func (p *Postgres) HeartbeatPlaygroundRun(ctx context.Context, owner, id, runID string) error {
	result, err := p.goqu.Update(p.tablePlaygroundChats).Set(goqu.Record{"active_run_heartbeat_at": goqu.L("clock_timestamp()")}).
		Where(goqu.Ex{"id": id, "owner_user_id": owner, "active_run_id": runID, "active_run_cancel_requested": false}).
		Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("heartbeat playground run: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read playground run heartbeat: %w", err)
	}
	if count == 0 {
		return service.ErrPlaygroundRunBusy
	}
	return nil
}

func (p *Postgres) ReleasePlaygroundRun(ctx context.Context, owner, id, runID string) error {
	_, err := p.goqu.Update(p.tablePlaygroundChats).Set(goqu.Record{
		"active_run_id": "", "active_run_workspace_id": "", "active_run_heartbeat_at": nil, "active_run_cancel_requested": false,
	}).Where(goqu.Ex{"id": id, "owner_user_id": owner, "active_run_id": runID}).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("release playground run: %w", err)
	}
	return nil
}

func (p *Postgres) CancelPlaygroundRun(ctx context.Context, owner, id, runID string) error {
	if _, err := p.GetPlaygroundConversation(ctx, owner, id); err != nil {
		return err
	}
	match := goqu.Ex{"id": id, "owner_user_id": owner}
	if runID != "" {
		match["active_run_id"] = runID
	}
	_, err := p.goqu.Update(p.tablePlaygroundChats).Set(goqu.Record{"active_run_cancel_requested": true}).
		Where(match, goqu.I("active_run_id").Neq("")).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("cancel playground run: %w", err)
	}
	return nil
}

func (p *Postgres) GetPlaygroundRun(ctx context.Context, owner, id string) (*service.PlaygroundRun, error) {
	if owner == "" || id == "" {
		return nil, service.ErrPlaygroundNotFound
	}
	var row struct {
		ID          string       `db:"active_run_id"`
		WorkspaceID string       `db:"active_run_workspace_id"`
		Heartbeat   sql.NullTime `db:"active_run_heartbeat_at"`
		Cancelled   bool         `db:"active_run_cancel_requested"`
		Stale       bool         `db:"stale"`
	}
	found, err := p.goqu.From(p.tablePlaygroundChats).Select(
		"active_run_id", "active_run_workspace_id", "active_run_heartbeat_at", "active_run_cancel_requested",
		goqu.L("COALESCE(active_run_heartbeat_at < ?, true)", playgroundRunStale).As("stale"),
	).Where(goqu.Ex{"id": id, "owner_user_id": owner}).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("read playground run: %w", err)
	}
	if !found {
		return nil, service.ErrPlaygroundNotFound
	}
	if row.ID == "" {
		return nil, nil
	}
	run := &service.PlaygroundRun{ID: row.ID, WorkspaceID: row.WorkspaceID, CancelRequested: row.Cancelled, Stale: row.Stale}
	if row.Heartbeat.Valid {
		run.HeartbeatAt = row.Heartbeat.Time.UTC().Format(time.RFC3339Nano)
	}
	return run, nil
}

// checkPlaygroundRunWrite runs under the conversation row lock. A browser write
// is refused while a live run owns the transcript; a run write is accepted only
// from the run holding the receipt, so a superseded run cannot interleave rows.
func (p *Postgres) checkPlaygroundRunWrite(ctx context.Context, tx *goqu.TxDatabase, id string) error {
	var row struct {
		ID    string `db:"active_run_id"`
		Stale bool   `db:"stale"`
	}
	if _, err := tx.From(p.tablePlaygroundChats).Select("active_run_id",
		goqu.L("COALESCE(active_run_heartbeat_at < ?, true)", playgroundRunStale).As("stale"),
	).Where(goqu.Ex{"id": id}).ScanStructContext(ctx, &row); err != nil {
		return fmt.Errorf("check playground run: %w", err)
	}
	if run := service.PlaygroundRunFromContext(ctx); run != "" {
		if row.ID != run {
			return service.ErrPlaygroundRunBusy
		}
		return nil
	}
	if row.ID != "" && !row.Stale {
		return service.ErrPlaygroundRunBusy
	}
	return nil
}
