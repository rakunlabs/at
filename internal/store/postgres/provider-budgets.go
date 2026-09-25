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

var _ service.ProviderBudgetStorer = (*Postgres)(nil)

type providerBudgetPolicyRow struct {
	ID                    string    `db:"id"`
	ResourceKind          string    `db:"resource_kind"`
	ResourceID            string    `db:"resource_id"`
	TotalLimitCents       float64   `db:"total_limit_cents"`
	DefaultUserLimitCents float64   `db:"default_user_limit_cents"`
	BudgetPeriod          string    `db:"budget_period"`
	BudgetResetDay        int       `db:"budget_reset_day"`
	BudgetResetTime       string    `db:"budget_reset_time"`
	BudgetTimezone        string    `db:"budget_timezone"`
	EnforceUnpriced       bool      `db:"enforce_unpriced"`
	CreatedAt             time.Time `db:"created_at"`
	UpdatedAt             time.Time `db:"updated_at"`
	CreatedBy             string    `db:"created_by"`
	UpdatedBy             string    `db:"updated_by"`
}

func providerBudgetPolicyRecord(row providerBudgetPolicyRow) *service.ProviderBudgetPolicy {
	return &service.ProviderBudgetPolicy{
		ID: row.ID, ResourceKind: row.ResourceKind, ResourceID: row.ResourceID,
		TotalLimitCents: row.TotalLimitCents, DefaultUserLimitCents: row.DefaultUserLimitCents,
		BudgetSchedule:  service.BudgetSchedule{BudgetPeriod: row.BudgetPeriod, BudgetResetDay: row.BudgetResetDay, BudgetResetTime: row.BudgetResetTime, BudgetTimezone: row.BudgetTimezone},
		EnforceUnpriced: row.EnforceUnpriced, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339),
		CreatedBy: row.CreatedBy, UpdatedBy: row.UpdatedBy,
	}
}

func (p *Postgres) providerBudgetActor(ctx context.Context, tx *goqu.TxDatabase, kind, id, capability string) (service.AccessPrincipal, error) {
	a, _, err := p.providerBudgetAccess(ctx, tx, kind, id, capability, false)
	return a, err
}

// providerBudgetOverrideGrant is the delegation a recipient workspace holds
// over a shared virtual provider's per-user overrides.
type providerBudgetOverrideGrant struct {
	MaxUserLimitCents float64
}

// providerBudgetAccess authorizes the owner of a budgeted resource. When
// allowRecipient is set, a workspace that received a virtual provider through
// a grant with allow_user_overrides is also admitted and the grant is returned,
// so the caller can confine it to its own members and the grant's ceiling.
func (p *Postgres) providerBudgetAccess(ctx context.Context, tx *goqu.TxDatabase, kind, id, capability string, allowRecipient bool) (service.AccessPrincipal, *providerBudgetOverrideGrant, error) {
	a, err := p.workspaceActor(ctx, tx, capability)
	if err != nil {
		return service.AccessPrincipal{}, nil, err
	}
	var ownerWorkspace, ownerUser string
	var found bool
	switch kind {
	case service.BudgetResourceProvider:
		var owner struct {
			WorkspaceID sql.NullString `db:"workspace_id"`
			OwnerUserID string         `db:"owner_user_id"`
		}
		found, err = tx.From(p.tableProviders).Select("workspace_id", "owner_user_id").Where(goqu.Ex{"id": id}).ScanStructContext(ctx, &owner)
		ownerUser = owner.OwnerUserID
		if owner.WorkspaceID.Valid {
			ownerWorkspace = owner.WorkspaceID.String
		}
	case service.BudgetResourceVirtualProvider:
		found, err = tx.From(p.tableVirtualProviders).Select("workspace_id").Where(goqu.Ex{"id": id}).ScanValContext(ctx, &ownerWorkspace)
	default:
		return service.AccessPrincipal{}, nil, service.ErrAccessDenied
	}
	if err != nil {
		return service.AccessPrincipal{}, nil, fmt.Errorf("resolve budget resource: %w", err)
	}
	if !found {
		return service.AccessPrincipal{}, nil, service.ErrAccessResourceNotFound
	}
	if a.PlatformAdmin || ownerWorkspace == a.WorkspaceID || (ownerUser != "" && ownerUser == a.UserID) {
		return a, nil, nil
	}
	if allowRecipient && kind == service.BudgetResourceVirtualProvider {
		var grant struct {
			Allow bool    `db:"allow_user_overrides"`
			Max   float64 `db:"max_user_limit_cents"`
		}
		ok, grantErr := tx.From(p.tableVirtualProviderGrants).Select("allow_user_overrides", "max_user_limit_cents").Where(goqu.Ex{"virtual_provider_id": id, "workspace_id": a.WorkspaceID}).ScanStructContext(ctx, &grant)
		if grantErr != nil {
			return service.AccessPrincipal{}, nil, fmt.Errorf("resolve virtual provider grant: %w", grantErr)
		}
		if ok && grant.Allow {
			return a, &providerBudgetOverrideGrant{MaxUserLimitCents: grant.Max}, nil
		}
	}
	return service.AccessPrincipal{}, nil, service.ErrAccessDenied
}

func (p *Postgres) GetProviderBudgetPolicy(ctx context.Context, kind, id string) (*service.ProviderBudgetPolicy, error) {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin provider budget read: %w", err)
	}
	defer tx.Rollback()
	if _, _, err = p.providerBudgetAccess(ctx, tx, kind, id, "providers.read", true); err != nil {
		return nil, err
	}
	var row providerBudgetPolicyRow
	found, err := tx.From(p.tableProviderBudgetPolicies).Where(goqu.Ex{"resource_kind": kind, "resource_id": id}).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("get provider budget policy: %w", err)
	}
	if !found {
		return nil, nil
	}
	return providerBudgetPolicyRecord(row), nil
}

func (p *Postgres) SaveProviderBudgetPolicy(ctx context.Context, policy service.ProviderBudgetPolicy) (*service.ProviderBudgetPolicy, error) {
	schedule, err := service.NormalizeBudgetSchedule(policy.BudgetSchedule)
	if err != nil {
		return nil, err
	}
	if policy.TotalLimitCents < 0 || policy.DefaultUserLimitCents < 0 {
		return nil, fmt.Errorf("budget limits must be zero or positive")
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin provider budget save: %w", err)
	}
	defer tx.Rollback()
	a, err := p.providerBudgetActor(ctx, tx, policy.ResourceKind, policy.ResourceID, "providers.write")
	if err != nil {
		return nil, err
	}
	if policy.ID == "" {
		policy.ID = ulid.Make().String()
	}
	actor := a.UserID
	_, err = tx.Insert(p.tableProviderBudgetPolicies).Rows(goqu.Record{
		"id": policy.ID, "resource_kind": policy.ResourceKind, "resource_id": policy.ResourceID,
		"total_limit_cents": policy.TotalLimitCents, "default_user_limit_cents": policy.DefaultUserLimitCents,
		"budget_period": schedule.BudgetPeriod, "budget_reset_day": schedule.BudgetResetDay,
		"budget_reset_time": schedule.BudgetResetTime, "budget_timezone": schedule.BudgetTimezone,
		"enforce_unpriced": policy.EnforceUnpriced, "created_by": actor, "updated_by": actor,
	}).OnConflict(goqu.DoUpdate("resource_kind,resource_id", goqu.Record{
		"total_limit_cents": policy.TotalLimitCents, "default_user_limit_cents": policy.DefaultUserLimitCents,
		"budget_period": schedule.BudgetPeriod, "budget_reset_day": schedule.BudgetResetDay,
		"budget_reset_time": schedule.BudgetResetTime, "budget_timezone": schedule.BudgetTimezone,
		"enforce_unpriced": policy.EnforceUnpriced, "updated_at": goqu.L("NOW()"), "updated_by": actor,
	})).Executor().ExecContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("save provider budget policy: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit provider budget policy: %w", err)
	}
	return p.GetProviderBudgetPolicy(ctx, policy.ResourceKind, policy.ResourceID)
}

func (p *Postgres) DeleteProviderBudgetPolicy(ctx context.Context, kind, id string) error {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin provider budget delete: %w", err)
	}
	defer tx.Rollback()
	if _, err = p.providerBudgetActor(ctx, tx, kind, id, "providers.write"); err != nil {
		return err
	}
	if _, err = tx.Delete(p.tableProviderBudgetPolicies).Where(goqu.Ex{"resource_kind": kind, "resource_id": id}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("delete provider budget policy: %w", err)
	}
	return tx.Commit()
}

func (p *Postgres) ListProviderBudgetOverrides(ctx context.Context, policyID string) ([]service.ProviderBudgetOverride, error) {
	policy, a, grant, err := p.authorizedProviderBudgetPolicy(ctx, policyID, "providers.read")
	if err != nil || policy == nil {
		return nil, err
	}
	var rows []struct {
		PolicyID   string    `db:"policy_id"`
		UserID     string    `db:"user_id"`
		Mode       string    `db:"mode"`
		LimitCents float64   `db:"limit_cents"`
		UpdatedAt  time.Time `db:"updated_at"`
		UpdatedBy  string    `db:"updated_by"`
	}
	q := p.goqu.From(p.tableProviderBudgetOverrides).Where(goqu.Ex{"policy_id": policyID})
	if grant != nil {
		// A recipient workspace sees only the overrides of its own members.
		q = q.Where(goqu.C("user_id").In(p.goqu.From(p.workspaceTable("workspace_memberships")).Select("user_id").Where(goqu.Ex{"workspace_id": a.WorkspaceID, "status": "active"})))
	}
	if err = q.Order(goqu.C("user_id").Asc()).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list provider budget overrides: %w", err)
	}
	out := make([]service.ProviderBudgetOverride, 0, len(rows))
	for _, row := range rows {
		out = append(out, service.ProviderBudgetOverride{PolicyID: row.PolicyID, UserID: row.UserID, Mode: row.Mode, LimitCents: row.LimitCents, UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339), UpdatedBy: row.UpdatedBy})
	}
	return out, nil
}

// authorizedProviderBudgetPolicy resolves a policy for override management. A
// non-nil grant means the caller is a recipient workspace acting under a
// virtual provider grant's delegation rather than the resource owner.
func (p *Postgres) authorizedProviderBudgetPolicy(ctx context.Context, policyID, capability string) (*service.ProviderBudgetPolicy, service.AccessPrincipal, *providerBudgetOverrideGrant, error) {
	var row providerBudgetPolicyRow
	found, err := p.goqu.From(p.tableProviderBudgetPolicies).Where(goqu.Ex{"id": policyID}).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, service.AccessPrincipal{}, nil, fmt.Errorf("get provider budget policy: %w", err)
	}
	if !found {
		return nil, service.AccessPrincipal{}, nil, service.ErrAccessResourceNotFound
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, service.AccessPrincipal{}, nil, err
	}
	defer tx.Rollback()
	a, grant, err := p.providerBudgetAccess(ctx, tx, row.ResourceKind, row.ResourceID, capability, true)
	if err != nil {
		return nil, service.AccessPrincipal{}, nil, err
	}
	return providerBudgetPolicyRecord(row), a, grant, nil
}

// checkDelegatedOverride confines a recipient workspace to its own active
// members and to the ceiling its grant allows.
func (p *Postgres) checkDelegatedOverride(ctx context.Context, a service.AccessPrincipal, grant *providerBudgetOverrideGrant, override service.ProviderBudgetOverride) error {
	if grant == nil {
		return nil
	}
	var member string
	found, err := p.goqu.From(p.workspaceTable("workspace_memberships")).Select("user_id").Where(goqu.Ex{"workspace_id": a.WorkspaceID, "user_id": override.UserID, "status": "active"}).ScanValContext(ctx, &member)
	if err != nil {
		return fmt.Errorf("check override member: %w", err)
	}
	if !found {
		return service.ErrAccessDenied
	}
	if grant.MaxUserLimitCents > 0 {
		switch override.Mode {
		case service.BudgetOverrideUnlimited:
			return fmt.Errorf("this workspace may not grant unlimited use; the maximum is %.0f cents", grant.MaxUserLimitCents)
		case service.BudgetOverrideCustom:
			if override.LimitCents > grant.MaxUserLimitCents {
				return fmt.Errorf("limit exceeds the %.0f cent maximum this workspace may grant", grant.MaxUserLimitCents)
			}
		}
	}
	return nil
}

func (p *Postgres) SaveProviderBudgetOverride(ctx context.Context, override service.ProviderBudgetOverride) error {
	if override.UserID == "" || (override.Mode != service.BudgetOverrideCustom && override.Mode != service.BudgetOverrideUnlimited && override.Mode != service.BudgetOverrideBlocked) {
		return fmt.Errorf("user_id and a valid override mode are required")
	}
	if override.Mode == service.BudgetOverrideCustom && override.LimitCents <= 0 {
		return fmt.Errorf("custom limit must be greater than zero")
	}
	policy, a, grant, err := p.authorizedProviderBudgetPolicy(ctx, override.PolicyID, "providers.write")
	if err != nil {
		return err
	}
	if err = p.checkDelegatedOverride(ctx, a, grant, override); err != nil {
		return err
	}
	_, err = p.goqu.Insert(p.tableProviderBudgetOverrides).Rows(goqu.Record{"policy_id": policy.ID, "user_id": override.UserID, "mode": override.Mode, "limit_cents": override.LimitCents, "updated_by": a.UserID}).OnConflict(goqu.DoUpdate("policy_id,user_id", goqu.Record{"mode": override.Mode, "limit_cents": override.LimitCents, "updated_at": goqu.L("NOW()"), "updated_by": a.UserID})).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("save provider budget override: %w", err)
	}
	return nil
}

func (p *Postgres) DeleteProviderBudgetOverride(ctx context.Context, policyID, userID string) error {
	_, a, grant, err := p.authorizedProviderBudgetPolicy(ctx, policyID, "providers.write")
	if err != nil {
		return err
	}
	if err = p.checkDelegatedOverride(ctx, a, grant, service.ProviderBudgetOverride{UserID: userID, Mode: service.BudgetOverrideBlocked}); err != nil {
		return err
	}
	_, err = p.goqu.Delete(p.tableProviderBudgetOverrides).Where(goqu.Ex{"policy_id": policyID, "user_id": userID}).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("delete provider budget override: %w", err)
	}
	return nil
}

func (p *Postgres) GetProviderBudgetStatus(ctx context.Context, kind, id, userID string) (*service.ProviderBudgetStatus, error) {
	policy, err := p.GetProviderBudgetPolicy(ctx, kind, id)
	if err != nil || policy == nil {
		return nil, err
	}
	start, end, err := service.BudgetPeriodBounds(time.Now(), policy.BudgetSchedule)
	if err != nil {
		return nil, err
	}
	status := &service.ProviderBudgetStatus{ProviderBudgetPolicy: *policy, PeriodStart: start.UTC().Format(time.RFC3339), PeriodEnd: end.UTC().Format(time.RFC3339)}
	read := func(uid string) (float64, float64, error) {
		var usage struct {
			Spent    float64 `db:"spent_cents"`
			Reserved float64 `db:"reserved_cents"`
		}
		found, readErr := p.goqu.From(p.tableProviderBudgetUsage).Select("spent_cents", "reserved_cents").Where(goqu.Ex{"policy_id": policy.ID, "period_start": start.UTC(), "user_id": uid}).ScanStructContext(ctx, &usage)
		if readErr != nil {
			return 0, 0, readErr
		}
		if !found {
			return 0, 0, nil
		}
		return usage.Spent, usage.Reserved, nil
	}
	status.SpentCents, status.ReservedCents, err = read("")
	if err != nil {
		return nil, fmt.Errorf("read provider budget status: %w", err)
	}
	if userID != "" {
		status.UserSpentCents, status.UserReservedCents, err = read(userID)
		if err != nil {
			return nil, fmt.Errorf("read provider user budget status: %w", err)
		}
		status.EffectiveUserLimit = policy.DefaultUserLimitCents
		var override struct {
			Mode  string  `db:"mode"`
			Limit float64 `db:"limit_cents"`
		}
		if found, readErr := p.goqu.From(p.tableProviderBudgetOverrides).Select("mode", "limit_cents").Where(goqu.Ex{"policy_id": policy.ID, "user_id": userID}).ScanStructContext(ctx, &override); readErr != nil {
			return nil, readErr
		} else if found {
			switch override.Mode {
			case service.BudgetOverrideCustom:
				status.EffectiveUserLimit = override.Limit
			case service.BudgetOverrideUnlimited:
				status.EffectiveUserLimit = 0
			case service.BudgetOverrideBlocked:
				status.EffectiveUserLimit = -1
			}
		}
	}
	return status, nil
}

func (p *Postgres) ReserveProviderBudget(ctx context.Context, resources []service.BudgetResource, userID string, estimate *float64) (*service.ProviderBudgetReservation, error) {
	if len(resources) == 0 {
		return nil, nil
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin provider budget reservation: %w", err)
	}
	defer tx.Rollback()
	// A process can disappear after reserving but before settlement. Release
	// abandoned reservations so a crash cannot permanently consume a budget.
	if _, err = tx.ExecContext(ctx, fmt.Sprintf(`
		WITH stale AS (
			DELETE FROM %s WHERE created_at < NOW() - INTERVAL '1 hour'
			RETURNING policy_id, period_start, user_id, reserved_cents
		), released_users AS (
			SELECT policy_id, period_start, user_id, SUM(reserved_cents) AS amount
			FROM stale WHERE user_id <> '' GROUP BY policy_id, period_start, user_id
		), released_total AS (
			SELECT policy_id, period_start, '' AS user_id, SUM(reserved_cents) AS amount
			FROM stale GROUP BY policy_id, period_start
		), released AS (
			SELECT * FROM released_users UNION ALL SELECT * FROM released_total
		)
		UPDATE %s u SET reserved_cents = GREATEST(0, u.reserved_cents - r.amount), updated_at = NOW()
		FROM released r WHERE u.policy_id = r.policy_id AND u.period_start = r.period_start
		AND u.user_id = r.user_id`, p.tableProviderBudgetReservations.GetTable(), p.tableProviderBudgetUsage.GetTable())); err != nil {
		return nil, fmt.Errorf("release stale provider budget reservations: %w", err)
	}
	reservationID := ulid.Make().String()
	reserved := 0.0
	if estimate != nil && *estimate > 0 {
		reserved = *estimate
	}
	policyCount := 0
	for _, resource := range resources {
		var row providerBudgetPolicyRow
		found, readErr := tx.From(p.tableProviderBudgetPolicies).Where(goqu.Ex{"resource_kind": resource.Kind, "resource_id": resource.ID}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &row)
		if readErr != nil {
			return nil, fmt.Errorf("read provider budget for reservation: %w", readErr)
		}
		if !found {
			continue
		}
		policyCount++
		if estimate == nil && row.EnforceUnpriced {
			return nil, service.ErrProviderPricingRequired
		}
		policy := providerBudgetPolicyRecord(row)
		start, _, boundsErr := service.BudgetPeriodBounds(time.Now(), policy.BudgetSchedule)
		if boundsErr != nil {
			return nil, boundsErr
		}
		start = start.UTC()
		if _, err = tx.Insert(p.tableProviderBudgetUsage).Rows(goqu.Record{"policy_id": row.ID, "period_start": start, "user_id": ""}).OnConflict(goqu.DoNothing()).Executor().ExecContext(ctx); err != nil {
			return nil, err
		}
		var total struct {
			Spent    float64 `db:"spent_cents"`
			Reserved float64 `db:"reserved_cents"`
		}
		if _, err = tx.From(p.tableProviderBudgetUsage).Select("spent_cents", "reserved_cents").Where(goqu.Ex{"policy_id": row.ID, "period_start": start, "user_id": ""}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &total); err != nil {
			return nil, err
		}
		if row.TotalLimitCents > 0 && total.Spent+total.Reserved+reserved > row.TotalLimitCents {
			return nil, service.ErrProviderBudgetExceeded
		}
		if userID != "" {
			limit := row.DefaultUserLimitCents
			var override struct {
				Mode  string  `db:"mode"`
				Limit float64 `db:"limit_cents"`
			}
			if overrideFound, overrideErr := tx.From(p.tableProviderBudgetOverrides).Select("mode", "limit_cents").Where(goqu.Ex{"policy_id": row.ID, "user_id": userID}).ScanStructContext(ctx, &override); overrideErr != nil {
				return nil, overrideErr
			} else if overrideFound {
				switch override.Mode {
				case service.BudgetOverrideBlocked:
					return nil, service.ErrProviderUserBlocked
				case service.BudgetOverrideUnlimited:
					limit = 0
				case service.BudgetOverrideCustom:
					limit = override.Limit
				}
			}
			if _, err = tx.Insert(p.tableProviderBudgetUsage).Rows(goqu.Record{"policy_id": row.ID, "period_start": start, "user_id": userID}).OnConflict(goqu.DoNothing()).Executor().ExecContext(ctx); err != nil {
				return nil, err
			}
			var userUsage struct {
				Spent    float64 `db:"spent_cents"`
				Reserved float64 `db:"reserved_cents"`
			}
			if _, err = tx.From(p.tableProviderBudgetUsage).Select("spent_cents", "reserved_cents").Where(goqu.Ex{"policy_id": row.ID, "period_start": start, "user_id": userID}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &userUsage); err != nil {
				return nil, err
			}
			if limit > 0 && userUsage.Spent+userUsage.Reserved+reserved > limit {
				return nil, service.ErrProviderUserLimit
			}
			if _, err = tx.Update(p.tableProviderBudgetUsage).Set(goqu.Record{"reserved_cents": goqu.L("reserved_cents + ?", reserved), "updated_at": goqu.L("NOW()")}).Where(goqu.Ex{"policy_id": row.ID, "period_start": start, "user_id": userID}).Executor().ExecContext(ctx); err != nil {
				return nil, err
			}
		}
		if _, err = tx.Update(p.tableProviderBudgetUsage).Set(goqu.Record{"reserved_cents": goqu.L("reserved_cents + ?", reserved), "updated_at": goqu.L("NOW()")}).Where(goqu.Ex{"policy_id": row.ID, "period_start": start, "user_id": ""}).Executor().ExecContext(ctx); err != nil {
			return nil, err
		}
		if _, err = tx.Insert(p.tableProviderBudgetReservations).Rows(goqu.Record{"reservation_id": reservationID, "policy_id": row.ID, "period_start": start, "user_id": userID, "reserved_cents": reserved}).Executor().ExecContext(ctx); err != nil {
			return nil, err
		}
	}
	if policyCount == 0 {
		return nil, nil
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit provider budget reservation: %w", err)
	}
	return &service.ProviderBudgetReservation{ID: reservationID}, nil
}

func (p *Postgres) SettleProviderBudget(ctx context.Context, reservationID string, actualCents float64) error {
	if reservationID == "" {
		return nil
	}
	if actualCents < 0 {
		actualCents = 0
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin provider budget settlement: %w", err)
	}
	defer tx.Rollback()
	var rows []struct {
		PolicyID    string    `db:"policy_id"`
		PeriodStart time.Time `db:"period_start"`
		UserID      string    `db:"user_id"`
		Reserved    float64   `db:"reserved_cents"`
	}
	if err = tx.From(p.tableProviderBudgetReservations).Where(goqu.Ex{"reservation_id": reservationID}).ForUpdate(goqu.Wait).ScanStructsContext(ctx, &rows); err != nil {
		return fmt.Errorf("read provider budget reservation: %w", err)
	}
	for _, row := range rows {
		userIDs := []string{""}
		if row.UserID != "" {
			userIDs = append(userIDs, row.UserID)
		}
		for _, uid := range userIDs {
			_, err = tx.Update(p.tableProviderBudgetUsage).Set(goqu.Record{
				"reserved_cents": goqu.L("GREATEST(0, reserved_cents - ?)", row.Reserved),
				"spent_cents":    goqu.L("spent_cents + ?", actualCents), "updated_at": goqu.L("NOW()"),
			}).Where(goqu.Ex{"policy_id": row.PolicyID, "period_start": row.PeriodStart, "user_id": uid}).Executor().ExecContext(ctx)
			if err != nil {
				return fmt.Errorf("settle provider budget usage: %w", err)
			}
		}
	}
	if _, err = tx.Delete(p.tableProviderBudgetReservations).Where(goqu.Ex{"reservation_id": reservationID}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("delete provider budget reservation: %w", err)
	}
	return tx.Commit()
}
