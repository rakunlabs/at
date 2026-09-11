package postgres

import (
	"context"
	"fmt"
	"slices"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

// This lookup is machine-admission metadata only; it does not authenticate the
// caller and is never exposed as a bearer-by-ID HTTP operation.
func (p *Postgres) GetExecutionServiceBinding(ctx context.Context, kind, id string) (*service.ExecutionServiceBinding, error) {
	if kind != "bot" && kind != "trigger" && kind != "id" {
		return nil, service.ErrExecutionDenied
	}
	var b service.ExecutionServiceBinding
	where := goqu.Ex{"kind": kind, "subject_id": id}
	if kind == "id" {
		where = goqu.Ex{"id": id}
	}
	found, err := p.goqu.From(p.executionTable("execution_service_bindings")).Where(where).ScanStructContext(ctx, &b)
	if err != nil {
		return nil, fmt.Errorf("load execution service binding: %w", err)
	}
	if !found {
		return nil, nil
	}
	return &b, nil
}

func (p *Postgres) ListExecutionServiceBindings(ctx context.Context, kind string) ([]service.ExecutionServiceBinding, error) {
	if !service.HasExecutionMaintenance(ctx) || (kind != "bot" && kind != "trigger") {
		return nil, service.ErrExecutionDenied
	}
	var rows []service.ExecutionServiceBinding
	if err := p.goqu.From(p.executionTable("execution_service_bindings")).Where(goqu.Ex{"kind": kind, "revoked": false}).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("enumerate execution services: %w", err)
	}
	return rows, nil
}

func (p *Postgres) SaveExecutionServiceBinding(ctx context.Context, b service.ExecutionServiceBinding) (*service.ExecutionServiceBinding, error) {
	actor, ok := service.AccessPrincipalFromContext(ctx)
	if !ok || actor.SessionID == "" {
		return nil, service.ErrExecutionDenied
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "execution.run"}); err != nil {
		return nil, err
	}
	actor, _, err := p.ResolveWorkspaceAccess(ctx, actor.WorkspaceID, actor.UserID, actor.SessionID)
	if err != nil {
		return nil, err
	}
	kind, cap := "bots", "bots.write"
	if b.Kind == "trigger" {
		kind, cap = "triggers", "workflows.write"
	} else if b.Kind != "bot" {
		return nil, service.ErrExecutionDenied
	}
	ctx = service.WithAccessPrincipal(ctx, actor)
	resource, err := p.ResolveExecutionResource(ctx, kind, b.SubjectID)
	if err != nil || !actor.Allows(cap, resource) {
		return nil, service.ErrExecutionDenied
	}
	policy, err := p.GetExecutionPolicy(ctx, actor.WorkspaceID)
	if err != nil {
		return nil, err
	}
	existing, err := p.GetExecutionServiceBinding(ctx, b.Kind, b.SubjectID)
	if err != nil {
		return nil, err
	}
	if existing != nil && (existing.WorkspaceID != actor.WorkspaceID || (!actor.PlatformAdmin && existing.OwnerUserID != actor.UserID) || existing.Version != b.Version) {
		return nil, service.ErrExecutionDenied
	}
	if existing == nil && b.Version != 0 {
		return nil, service.ErrExecutionDenied
	}
	runAs := b.UserID
	if runAs == "" {
		runAs = actor.UserID
		if existing != nil {
			runAs = existing.UserID
		}
	}
	if runAs != actor.UserID && !actor.PlatformAdmin {
		return nil, service.ErrExecutionDenied
	}
	var delegate service.AccessPrincipal
	if b.Revoked {
		if existing == nil {
			return nil, service.ErrExecutionDenied
		}
		runAs = existing.UserID
		delegate.MembershipVersion = existing.MembershipVersion
	} else {
		delegate, _, err = p.ResolveWorkspaceAccess(ctx, actor.WorkspaceID, runAs, "")
		if err != nil {
			return nil, err
		}
		// A machine identity must never inherit installation-admin status from its
		// backing account. Platform operators explicitly select a non-platform
		// workspace member; membership/version remains the renewable ceiling.
		if delegate.PlatformAdmin {
			return nil, service.ErrExecutionDenied
		}
		for _, grant := range delegate.Grants {
			if slices.Contains(delegate.Denied, grant.Capability) {
				continue
			}
			if !service.CanDelegateAccess(actor, grant) {
				return nil, service.ErrExecutionDenied
			}
		}
	}
	expected := b.Version
	b.ID = ulid.Make().String()
	if existing != nil {
		b.ID = existing.ID
	}
	b.WorkspaceID, b.UserID = actor.WorkspaceID, runAs
	b.OwnerUserID = actor.UserID
	if existing != nil {
		b.OwnerUserID = existing.OwnerUserID
	}
	b.MembershipVersion, b.PolicyVersion = delegate.MembershipVersion, policy.Version
	b.Version++
	var changed int64
	if existing == nil {
		result, err := p.goqu.Insert(p.executionTable("execution_service_bindings")).Rows(b).OnConflict(goqu.DoNothing()).Executor().ExecContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("create execution service binding: %w", err)
		}
		changed, err = result.RowsAffected()
		if err != nil {
			return nil, err
		}
	} else {
		result, err := p.goqu.Update(p.executionTable("execution_service_bindings")).Set(b).Where(goqu.Ex{"id": b.ID, "version": expected}).Executor().ExecContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("renew execution service binding: %w", err)
		}
		changed, err = result.RowsAffected()
		if err != nil {
			return nil, err
		}
	}
	if changed != 1 {
		return nil, service.ErrExecutionDenied
	}
	return &b, nil
}
