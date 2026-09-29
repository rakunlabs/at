package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/rakunlabs/at/internal/service"
)

// MovePersonalProviderToWorkspace converts an account-owned provider into an
// ordinary provider of the selected workspace, in place. The row keeps its ID,
// encrypted config and OAuth state; ownership moves from the account to the
// workspace, so it appears in the workspace list and is managed through the
// same permissions as any workspace provider.
//
// This is deliberately different from workspace *scope*, which only grants
// model use and leaves the credential with its owner. That grant is removed
// here, since the row is no longer personal.
//
// Callers reference a personal provider as "provider:<id>" and a workspace
// provider by its key. The stored references this workspace owns (agents,
// workflow graphs and developer sessions) are rewritten in the same
// transaction so the move does not leave them pointing at a reference that no
// longer resolves.
func (p *Postgres) MovePersonalProviderToWorkspace(ctx context.Context, id, updatedBy string) (*service.ProviderRecord, error) {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin personal provider move: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "personal_providers.manage")
	if err != nil {
		return nil, err
	}
	resource := service.AccessResource{WorkspaceID: a.WorkspaceID}
	if !a.Allows("providers.write", resource) || !a.Allows("credentials.manage", resource) {
		return nil, service.ErrAccessDenied
	}

	var row struct {
		Owner string `db:"owner_user_id"`
		Key   string `db:"key"`
	}
	found, err := tx.From(p.tableProviders).Select("owner_user_id", "key").
		Where(goqu.Ex{"id": id, "workspace_id": nil}).
		ForUpdate(goqu.Wait).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("lock personal provider move: %w", err)
	}
	if !found || row.Owner != a.UserID {
		return nil, service.ErrAccessResourceNotFound
	}

	var existingID string
	taken, err := tx.From(p.tableProviders).Select("id").
		Where(goqu.Ex{"workspace_id": a.WorkspaceID, "owner_user_id": "", "key": row.Key}).
		ScanValContext(ctx, &existingID)
	if err != nil {
		return nil, fmt.Errorf("check workspace provider key: %w", err)
	}
	if taken {
		return nil, fmt.Errorf("%w: this workspace already has a provider named %q; rename one of them first", service.ErrWorkspaceConflict, row.Key)
	}

	if _, err = tx.Delete(p.workspaceTable("personal_provider_grants")).
		Where(goqu.Ex{"provider_id": id}).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("clear personal provider grants: %w", err)
	}
	if _, err = tx.Update(p.tableProviders).Set(goqu.Record{
		"workspace_id": a.WorkspaceID, "owner_user_id": "",
		"updated_at": time.Now().UTC(), "updated_by": updatedBy,
	}).Where(goqu.Ex{"id": id, "owner_user_id": a.UserID, "workspace_id": nil}).Executor().ExecContext(ctx); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, fmt.Errorf("%w: this workspace already has a provider named %q", service.ErrWorkspaceConflict, row.Key)
		}
		return nil, fmt.Errorf("move personal provider: %w", err)
	}

	if err := p.rewriteProviderReferences(ctx, tx, a.WorkspaceID, service.PersonalProviderReference(id), row.Key); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit personal provider move: %w", err)
	}

	return p.GetProvider(ctx, row.Key)
}

// rewriteProviderReferences replaces a provider reference inside the records
// one workspace owns. References held elsewhere — another workspace, or
// another account's personal chats — are left alone: the provider is no longer
// reachable from there, and rewriting them to a key that resolves to a
// different workspace's provider would be worse than failing.
func (p *Postgres) rewriteProviderReferences(ctx context.Context, tx *goqu.TxDatabase, workspaceID, from, to string) error {
	inWorkspace := goqu.Ex{"workspace_id": workspaceID}

	// Agent config carries the key at config.provider.
	if _, err := tx.Update(p.tableAgents).Set(goqu.Record{
		"config": goqu.L("jsonb_set(config, '{provider}', to_jsonb(?::text))", to),
	}).Where(inWorkspace, goqu.L("config->>'provider' = ?", from)).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("rewrite agent provider reference: %w", err)
	}

	if _, err := tx.Update(p.tableDeveloperSessions).Set(goqu.Record{"provider": to}).
		Where(inWorkspace, goqu.Ex{"provider": from}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("rewrite developer session provider: %w", err)
	}

	// Workflow nodes store it at data.provider. Only graphs that mention the
	// reference are rebuilt; node order and every other field are kept.
	nodeRewrite := `jsonb_set(graph, '{nodes}', COALESCE((
		SELECT jsonb_agg(CASE WHEN n->'data'->>'provider' = ?
			THEN jsonb_set(n, '{data,provider}', to_jsonb(?::text)) ELSE n END ORDER BY ord)
		FROM jsonb_array_elements(graph->'nodes') WITH ORDINALITY AS e(n, ord)), '[]'::jsonb))`
	mentions := goqu.L("jsonb_typeof(graph->'nodes') = 'array' AND EXISTS (SELECT 1 FROM jsonb_array_elements(graph->'nodes') n WHERE n->'data'->>'provider' = ?)", from)
	for _, table := range []any{p.tableWorkflows, p.tableWorkflowVersions} {
		if _, err := tx.Update(table).Set(goqu.Record{
			"graph": goqu.L(nodeRewrite, from, to),
		}).Where(inWorkspace, mentions).Executor().ExecContext(ctx); err != nil {
			return fmt.Errorf("rewrite workflow provider reference: %w", err)
		}
	}

	return nil
}
