package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	"github.com/worldline-go/types"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

type quotaPool struct {
	Model  string    `json:"model"`
	Period string    `json:"period"`
	Start  time.Time `json:"start"`
	Reset  time.Time `json:"reset"`
	RLimit int64     `json:"requests"`
	TLimit int64     `json:"tokens"`
}

func quotaPools(q *config.ProviderQuotaConfig, model string, now time.Time) []quotaPool {
	if q == nil {
		return nil
	}
	now = now.UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	var pools []quotaPool
	add := func(m string, v config.QuotaLimits) {
		if v.RequestsPerDay > 0 || v.TokensPerDay > 0 {
			pools = append(pools, quotaPool{m, "day", day, day.AddDate(0, 0, 1), v.RequestsPerDay, v.TokensPerDay})
		}
		if v.RequestsPerMonth > 0 || v.TokensPerMonth > 0 {
			pools = append(pools, quotaPool{m, "month", month, month.AddDate(0, 1, 0), v.RequestsPerMonth, v.TokensPerMonth})
		}
	}
	add("", q.QuotaLimits)
	if model != "" {
		add(model, q.Models[model])
	}
	return pools
}

func (p *Postgres) ReserveProviderQuota(ctx context.Context, providerID, model string, estimate int64) (*service.ProviderQuotaReservation, error) {
	if providerID == "" {
		return nil, nil
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin provider quota: %w", err)
	}
	defer tx.Rollback()
	// One physical account row serializes admission and settlement across models,
	// users and replicas; never lock by a workspace-local bare key.
	var raw string
	found, err := tx.From(p.tableProviders).Select("config").Where(goqu.C("id").Eq(providerID)).ForUpdate(goqu.Wait).ScanValContext(ctx, &raw)
	if err != nil {
		return nil, fmt.Errorf("lock quota provider: %w", err)
	}
	if !found {
		return nil, service.ErrAccessResourceNotFound
	}
	var cfg config.LLMConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, fmt.Errorf("decode quota config: %w", err)
	}
	if err := cfg.Quota.Validate(); err != nil {
		return nil, err
	}
	if !cfg.Quota.HasLimits() {
		return nil, nil
	}
	if model == "" && len(cfg.Quota.Models) > 0 {
		return nil, fmt.Errorf("model-specific quotas cannot admit an opaque passthrough without a resolved model: %w", service.ErrUnsupportedOperation)
	}
	var now time.Time
	if _, err := tx.Select(goqu.L("clock_timestamp()")).ScanValContext(ctx, &now); err != nil {
		return nil, fmt.Errorf("quota clock: %w", err)
	}
	pools := quotaPools(cfg.Quota, model, now)
	if len(pools) == 0 {
		return &service.ProviderQuotaReservation{GuardOverrides: true, TokenLimited: cfg.Quota.HasTokenLimits()}, nil
	}
	estimate = max(0, estimate)
	usageTable := p.workspaceTable("provider_quota_usage")
	for _, pool := range pools {
		if pool.TLimit > 0 && estimate == 0 {
			return nil, fmt.Errorf("token quota requires measurable input/output; use request quotas for opaque passthrough or media calls: %w", service.ErrUnsupportedOperation)
		}
		where := goqu.Ex{"provider_id": providerID, "model": pool.Model, "period": pool.Period}
		var row struct {
			Start    time.Time `db:"window_start"`
			Requests int64     `db:"requests"`
			Tokens   int64     `db:"tokens"`
			Reserved int64     `db:"reserved"`
		}
		_, err := tx.From(usageTable).Select("window_start", "requests", "tokens", "reserved").Where(where).ScanStructContext(ctx, &row)
		if err != nil {
			return nil, fmt.Errorf("read quota pool: %w", err)
		}
		if !row.Start.Equal(pool.Start) {
			row.Requests, row.Tokens, row.Reserved = 0, 0, 0
		}
		if pool.RLimit > 0 && row.Requests >= pool.RLimit || pool.TLimit > 0 && row.Tokens+row.Reserved+estimate > pool.TLimit {
			return nil, &service.ProviderQuotaError{ResetAt: pool.Reset}
		}
		values := goqu.Record{"window_start": pool.Start, "requests": row.Requests + 1, "tokens": row.Tokens, "reserved": row.Reserved + estimate}
		insert := goqu.Record{"provider_id": providerID, "model": pool.Model, "period": pool.Period}
		for k, v := range values {
			insert[k] = v
		}
		if _, err := tx.Insert(usageTable).Rows(insert).OnConflict(goqu.DoUpdate("provider_id,model,period", values)).Executor().ExecContext(ctx); err != nil {
			return nil, fmt.Errorf("reserve quota pool: %w", err)
		}
	}
	id := ulid.Make().String()
	data, _ := json.Marshal(pools)
	if _, err := tx.Insert(p.workspaceTable("provider_quota_reservations")).Rows(goqu.Record{"id": id, "provider_id": providerID, "pools": types.RawJSON(data), "estimate": estimate}).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("save quota reservation: %w", err)
	}
	// Old receipts cannot affect any live window; no timer/janitor required.
	if _, err := tx.Delete(p.workspaceTable("provider_quota_reservations")).Where(goqu.C("provider_id").Eq(providerID), goqu.C("created_at").Lt(now.AddDate(0, -2, 0))).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("prune quota receipts: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit provider quota: %w", err)
	}
	return &service.ProviderQuotaReservation{ID: id, GuardOverrides: true, TokenLimited: cfg.Quota.HasTokenLimits()}, nil
}

// attempted=false undoes admission if an earlier policy prevented the upstream
// call. Every actual attempt consumes one request, even an upstream failure.
// Missing usage keeps the estimate, rather than silently treating it as free.
func (p *Postgres) SettleProviderQuota(ctx context.Context, id string, tokens int64, attempted bool) error {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin quota settlement: %w", err)
	}
	defer tx.Rollback()
	table := p.workspaceTable("provider_quota_reservations")
	var providerID string
	found, err := tx.From(table).Select("provider_id").Where(goqu.C("id").Eq(id)).ScanValContext(ctx, &providerID)
	if err != nil {
		return fmt.Errorf("read quota receipt: %w", err)
	}
	if !found {
		return nil
	}
	var locked string
	if _, err := tx.From(p.tableProviders).Select("id").Where(goqu.C("id").Eq(providerID)).ForUpdate(goqu.Wait).ScanValContext(ctx, &locked); err != nil {
		return fmt.Errorf("lock quota settlement: %w", err)
	}
	var row struct {
		Pools    string `db:"pools"`
		Estimate int64  `db:"estimate"`
	}
	found, err = tx.From(table).Select("pools", "estimate").Where(goqu.C("id").Eq(id)).ScanStructContext(ctx, &row)
	if err != nil {
		return fmt.Errorf("reload quota receipt: %w", err)
	}
	if !found {
		return nil
	}
	var pools []quotaPool
	if err := json.Unmarshal([]byte(row.Pools), &pools); err != nil {
		return fmt.Errorf("decode quota receipt: %w", err)
	}
	requests := 0
	if !attempted {
		tokens, requests = 0, -1
	} else if tokens <= 0 {
		tokens = row.Estimate
	}
	for _, pool := range pools {
		_, err := tx.Update(p.workspaceTable("provider_quota_usage")).Set(goqu.Record{
			"reserved": goqu.L("GREATEST(0, reserved - ?)", row.Estimate),
			"tokens":   goqu.L("tokens + ?", tokens), "requests": goqu.L("GREATEST(0, requests + ?)", requests),
		}).Where(goqu.Ex{"provider_id": providerID, "model": pool.Model, "period": pool.Period, "window_start": pool.Start}).Executor().ExecContext(ctx)
		if err != nil {
			return fmt.Errorf("settle quota pool: %w", err)
		}
	}
	if _, err := tx.Delete(table).Where(goqu.C("id").Eq(id)).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("release quota receipt: %w", err)
	}
	return tx.Commit()
}

// ProviderQuotaUsage is a management read: resolve the provider first so an
// arbitrary provider ID cannot reveal another account's consumption.
func (p *Postgres) ProviderQuotaUsage(ctx context.Context, id string) ([]service.ProviderQuotaUsage, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	var row providerRow
	found, err := p.goqu.From(p.tableProviders).Select(personalProviderSelect()...).Where(goqu.C("id").Eq(id), goqu.Or(goqu.Ex{"workspace_id": a.WorkspaceID, "owner_user_id": ""}, goqu.Ex{"workspace_id": nil, "owner_user_id": a.UserID})).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("resolve quota management: %w", err)
	}
	if !found {
		return nil, service.ErrAccessResourceNotFound
	}
	cap := "providers.read"
	if row.OwnerUserID != "" {
		cap = "personal_providers.manage"
	}
	if !a.Allows(cap, service.AccessResource{WorkspaceID: a.WorkspaceID, ID: id}) {
		return nil, service.ErrAccessDenied
	}
	var cfg config.LLMConfig
	if err := json.Unmarshal([]byte(row.Config), &cfg); err != nil {
		return nil, err
	}
	var now time.Time
	if _, err := p.goqu.Select(goqu.L("clock_timestamp()")).ScanValContext(ctx, &now); err != nil {
		return nil, err
	}
	models := []string{""}
	if cfg.Quota != nil {
		for model := range cfg.Quota.Models {
			models = append(models, model)
		}
	}
	out := []service.ProviderQuotaUsage{}
	for _, model := range models {
		for _, pool := range quotaPools(cfg.Quota, model, now) {
			if model != "" && pool.Model == "" {
				continue
			}
			v := service.ProviderQuotaUsage{Model: pool.Model, Period: pool.Period, ResetAt: pool.Reset, RequestLimit: pool.RLimit, TokenLimit: pool.TLimit}
			var used struct {
				Requests int64 `db:"requests"`
				Tokens   int64 `db:"tokens"`
				Reserved int64 `db:"reserved"`
			}
			_, err := p.goqu.From(p.workspaceTable("provider_quota_usage")).Select("requests", "tokens", "reserved").Where(goqu.Ex{"provider_id": id, "model": pool.Model, "period": pool.Period, "window_start": pool.Start}).ScanStructContext(ctx, &used)
			if err != nil {
				return nil, err
			}
			v.Requests, v.Tokens, v.Reserved = used.Requests, used.Tokens, used.Reserved
			out = append(out, v)
		}
	}
	return out, nil
}
