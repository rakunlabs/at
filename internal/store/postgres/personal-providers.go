package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/query"
	"github.com/rakunlabs/query/adapter/adaptergoqu"
	"github.com/worldline-go/types"

	"github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
)

var _ service.PersonalProviderStorer = (*Postgres)(nil)

func (p *Postgres) personalProviderActor(ctx context.Context) (service.AccessPrincipal, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return service.AccessPrincipal{}, err
	}
	if a.UserID == "" || !a.Allows("personal_providers.manage", service.AccessResource{WorkspaceID: a.WorkspaceID}) {
		return service.AccessPrincipal{}, service.ErrAccessDenied
	}
	return a, nil
}

func personalProviderSelect() []any {
	return []any{
		goqu.L("COALESCE(workspace_id, '')").As("workspace_id"),
		"owner_user_id", "id", "key", "config", "created_at", "updated_at", "created_by", "updated_by",
	}
}

func (p *Postgres) ListPersonalProviders(ctx context.Context, q *query.Query) (*service.ListResult[service.ProviderRecord], error) {
	a, err := p.personalProviderActor(ctx)
	if err != nil {
		return nil, err
	}
	ds := p.goqu.From(p.tableProviders).Where(goqu.Ex{"owner_user_id": a.UserID, "workspace_id": nil})
	countDS := ds
	if q != nil {
		if exprs := adaptergoqu.Expression(q); len(exprs) > 0 {
			countDS = countDS.Where(exprs...)
		}
	}
	var total uint64
	if _, err = countDS.Select(goqu.COUNT("*")).ScanValContext(ctx, &total); err != nil {
		return nil, fmt.Errorf("count personal providers: %w", err)
	}
	ds = adaptergoqu.Select(q, ds, adaptergoqu.WithParameterized(false))
	var rows []providerRow
	if err = ds.Select(personalProviderSelect()...).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list personal providers: %w", err)
	}

	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()
	items := make([]service.ProviderRecord, 0, len(rows))
	for _, row := range rows {
		record, err := rowToRecord(row, encKey)
		if err != nil {
			return nil, err
		}
		if err := p.decoratePersonalProvider(ctx, a.WorkspaceID, record); err != nil {
			return nil, err
		}
		items = append(items, *record)
	}
	offset, limit := getPagination(q)
	return &service.ListResult[service.ProviderRecord]{Data: items, Meta: service.ListMeta{Total: total, Offset: offset, Limit: limit}}, nil
}

func (p *Postgres) GetPersonalProvider(ctx context.Context, id string) (*service.ProviderRecord, error) {
	a, err := p.personalProviderActor(ctx)
	if err != nil {
		return nil, err
	}
	var row providerRow
	found, err := p.goqu.From(p.tableProviders).
		Select(personalProviderSelect()...).
		Where(goqu.Ex{"id": id, "owner_user_id": a.UserID, "workspace_id": nil}).
		ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("get personal provider: %w", err)
	}
	if !found {
		return nil, nil
	}
	p.encKeyMu.RLock()
	record, err := rowToRecord(row, p.encKey)
	p.encKeyMu.RUnlock()
	if err != nil {
		return nil, err
	}
	if err := p.decoratePersonalProvider(ctx, a.WorkspaceID, record); err != nil {
		return nil, err
	}
	return record, nil
}

func (p *Postgres) CreatePersonalProvider(ctx context.Context, record service.ProviderRecord) (*service.ProviderRecord, error) {
	scope := record.Scope
	if scope == "" {
		scope = service.ProviderScopePersonal
	}
	if !service.ValidProviderScope(scope) {
		return nil, fmt.Errorf("invalid provider scope %q", scope)
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin personal provider create: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "personal_providers.manage")
	if err != nil {
		return nil, err
	}
	if scope == service.ProviderScopeWorkspace && !a.Allows("providers.write", service.AccessResource{WorkspaceID: a.WorkspaceID}) {
		return nil, service.ErrAccessDenied
	}
	if scope == service.ProviderScopeGlobal && !a.PlatformAdmin {
		return nil, service.ErrAccessDenied
	}
	p.encKeyMu.RLock()
	storeCfg, err := crypto.EncryptLLMConfig(record.Config, p.encKey)
	p.encKeyMu.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("encrypt personal provider: %w", err)
	}
	configJSON, err := json.Marshal(storeCfg)
	if err != nil {
		return nil, fmt.Errorf("marshal personal provider: %w", err)
	}
	id := ulid.Make().String()
	now := time.Now().UTC()
	_, err = tx.Insert(p.tableProviders).Rows(goqu.Record{
		"workspace_id": nil, "owner_user_id": a.UserID, "id": id, "key": record.Key,
		"config": types.RawJSON(configJSON), "created_at": now, "updated_at": now,
		"created_by": record.CreatedBy, "updated_by": record.UpdatedBy,
	}).Executor().ExecContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("create personal provider %q: %w", record.Key, err)
	}
	if err := p.insertPersonalProviderGrant(ctx, tx, id, a.UserID, scope, a.WorkspaceID, record.CreatedBy); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit personal provider create: %w", err)
	}
	return p.GetPersonalProvider(ctx, id)
}

func (p *Postgres) UpdatePersonalProvider(ctx context.Context, id string, record service.ProviderRecord) (*service.ProviderRecord, error) {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin personal provider update: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "personal_providers.manage")
	if err != nil {
		return nil, err
	}
	var owner string
	found, err := tx.From(p.tableProviders).Select("owner_user_id").Where(goqu.Ex{"id": id, "workspace_id": nil}).ForUpdate(goqu.Wait).ScanValContext(ctx, &owner)
	if err != nil {
		return nil, fmt.Errorf("lock personal provider: %w", err)
	}
	if !found {
		return nil, nil
	}
	if owner != a.UserID {
		return nil, service.ErrAccessResourceNotFound
	}
	p.encKeyMu.RLock()
	storeCfg, err := crypto.EncryptLLMConfig(record.Config, p.encKey)
	p.encKeyMu.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("encrypt personal provider: %w", err)
	}
	configJSON, err := json.Marshal(storeCfg)
	if err != nil {
		return nil, fmt.Errorf("marshal personal provider: %w", err)
	}
	if _, err = tx.Update(p.tableProviders).Set(goqu.Record{
		"key": record.Key, "config": types.RawJSON(configJSON), "updated_at": time.Now().UTC(), "updated_by": record.UpdatedBy,
	}).Where(goqu.Ex{"id": id, "owner_user_id": a.UserID, "workspace_id": nil}).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("update personal provider: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit personal provider update: %w", err)
	}
	return p.GetPersonalProvider(ctx, id)
}

func (p *Postgres) DeletePersonalProvider(ctx context.Context, id string) error {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin personal provider delete: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "personal_providers.manage")
	if err != nil {
		return err
	}
	var owner string
	found, err := tx.From(p.tableProviders).Select("owner_user_id").Where(goqu.Ex{"id": id, "workspace_id": nil}).ForUpdate(goqu.Wait).ScanValContext(ctx, &owner)
	if err != nil {
		return fmt.Errorf("lock personal provider delete: %w", err)
	}
	if !found || owner != a.UserID {
		return service.ErrAccessResourceNotFound
	}
	if _, err := tx.Delete(p.tableProviders).Where(goqu.Ex{"id": id, "owner_user_id": a.UserID, "workspace_id": nil}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("delete personal provider: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit personal provider delete: %w", err)
	}
	return nil
}

func (p *Postgres) SetPersonalProviderScope(ctx context.Context, id, scope, createdBy string) (*service.ProviderRecord, error) {
	if !service.ValidProviderScope(scope) {
		return nil, fmt.Errorf("invalid provider scope %q", scope)
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin personal provider scope update: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "personal_providers.manage")
	if err != nil {
		return nil, err
	}
	if scope == service.ProviderScopeWorkspace && !a.Allows("providers.write", service.AccessResource{WorkspaceID: a.WorkspaceID}) {
		return nil, service.ErrAccessDenied
	}
	if scope == service.ProviderScopeGlobal && !a.PlatformAdmin {
		return nil, service.ErrAccessDenied
	}
	var owner string
	found, err := tx.From(p.tableProviders).Select("owner_user_id").Where(goqu.Ex{"id": id, "workspace_id": nil}).ForUpdate(goqu.Wait).ScanValContext(ctx, &owner)
	if err != nil {
		return nil, fmt.Errorf("lock personal provider scope: %w", err)
	}
	if !found {
		return nil, service.ErrAccessResourceNotFound
	}
	if owner != a.UserID {
		return nil, service.ErrAccessResourceNotFound
	}
	grants := p.workspaceTable("personal_provider_grants")
	if _, err = tx.Delete(grants).Where(goqu.Ex{"provider_id": id}).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("clear personal provider grants: %w", err)
	}
	if err := p.insertPersonalProviderGrant(ctx, tx, id, owner, scope, a.WorkspaceID, createdBy); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit personal provider scope: %w", err)
	}
	return p.GetPersonalProvider(ctx, id)
}

func (p *Postgres) SetPersonalProviderDisabled(ctx context.Context, id string, disabled bool, updatedBy string) error {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin personal provider availability update: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "personal_providers.manage")
	if err != nil {
		return err
	}
	var owner string
	found, err := tx.From(p.tableProviders).Select("owner_user_id").Where(goqu.Ex{"id": id, "workspace_id": nil}).ForUpdate(goqu.Wait).ScanValContext(ctx, &owner)
	if err != nil {
		return fmt.Errorf("lock personal provider availability: %w", err)
	}
	if !found || owner != a.UserID {
		return service.ErrAccessResourceNotFound
	}
	if _, err := tx.Update(p.tableProviders).Set(goqu.Record{
		"config":     goqu.L("jsonb_set(config, '{disabled}', ?::jsonb, true)", fmt.Sprintf("%t", disabled)),
		"updated_at": time.Now().UTC(), "updated_by": updatedBy,
	}).Where(goqu.Ex{"id": id, "owner_user_id": a.UserID, "workspace_id": nil}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("set personal provider availability: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit personal provider availability: %w", err)
	}
	return nil
}

func (p *Postgres) insertPersonalProviderGrant(ctx context.Context, tx *goqu.TxDatabase, providerID, ownerUserID, scope, workspaceID, createdBy string) error {
	if scope == service.ProviderScopePersonal {
		return nil
	}
	row := goqu.Record{"id": ulid.Make().String(), "provider_id": providerID, "owner_user_id": ownerUserID, "global": scope == service.ProviderScopeGlobal, "created_by": createdBy}
	if scope == service.ProviderScopeWorkspace {
		row["workspace_id"] = workspaceID
	} else {
		row["workspace_id"] = nil
	}
	if _, err := tx.Insert(p.workspaceTable("personal_provider_grants")).Rows(row).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("publish personal provider: %w", err)
	}
	return nil
}

func (p *Postgres) decoratePersonalProvider(ctx context.Context, workspaceID string, record *service.ProviderRecord) error {
	record.Reference = service.PersonalProviderReference(record.ID)
	record.Scope = service.ProviderScopePersonal
	grants := p.workspaceTable("personal_provider_grants")
	var global bool
	if _, err := p.goqu.From(grants).Select("global").Where(goqu.Ex{"provider_id": record.ID, "global": true}).Limit(1).ScanValContext(ctx, &global); err != nil {
		return fmt.Errorf("read personal provider global grant: %w", err)
	}
	if global {
		record.Scope = service.ProviderScopeGlobal
		return nil
	}
	var id string
	found, err := p.goqu.From(grants).Select("id").Where(goqu.Ex{"provider_id": record.ID, "workspace_id": workspaceID, "global": false}).Limit(1).ScanValContext(ctx, &id)
	if err != nil {
		return fmt.Errorf("read personal provider workspace grant: %w", err)
	}
	if found {
		record.Scope = service.ProviderScopeWorkspace
	}
	return nil
}
