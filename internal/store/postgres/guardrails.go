package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/doug-martin/goqu/v9/exp"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.GuardrailStorer = (*Postgres)(nil)

type guardrailPolicyRow struct {
	ID          string         `db:"id"`
	WorkspaceID sql.NullString `db:"workspace_id"`
	Name        string         `db:"name"`
	Description string         `db:"description"`
	Enabled     bool           `db:"enabled"`
	Mode        string         `db:"mode"`
	UserID      string         `db:"user_id"`
	TokenID     string         `db:"token_id"`
	Provider    string         `db:"provider"`
	Model       string         `db:"model"`
	Source      string         `db:"source"`
	Config      []byte         `db:"config"`
	CreatedBy   string         `db:"created_by"`
	CreatedAt   time.Time      `db:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at"`
}

var guardrailPolicyColumns = []any{"id", "workspace_id", "name", "description", "enabled", "mode", "user_id", "token_id", "provider", "model", "source", "config", "created_by", "created_at", "updated_at"}

func guardrailPolicyRecord(row guardrailPolicyRow) service.GuardrailPolicy {
	p := service.GuardrailPolicy{
		ID:          row.ID,
		WorkspaceID: row.WorkspaceID.String,
		Name:        row.Name,
		Description: row.Description,
		Enabled:     row.Enabled,
		Mode:        row.Mode,
		UserID:      row.UserID,
		TokenID:     row.TokenID,
		Provider:    row.Provider,
		Model:       row.Model,
		Source:      row.Source,
		CreatedBy:   row.CreatedBy,
		CreatedAt:   row.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   row.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if len(row.Config) > 0 {
		if err := json.Unmarshal(row.Config, &p.Config); err != nil {
			// A config that no longer decodes must not silently become an
			// empty (permissive) policy: keep it enabled with nothing that
			// could pass, by refusing every call it matches.
			p.Config = service.GuardrailConfig{Input: service.GuardrailInputFilter{Deny: []service.GuardrailPattern{{Name: "invalid_config", Pattern: "(?s).*"}}}}
		}
	}
	p.Scope = "workspace"
	if p.WorkspaceID == "" {
		p.Scope = "installation"
	}
	return p
}

// LoadGuardrailPolicies reads every enabled policy. It is an internal lookup
// for enforcement and performs no authorization.
func (p *Postgres) LoadGuardrailPolicies(ctx context.Context) ([]service.GuardrailPolicy, error) {
	var rows []guardrailPolicyRow
	if err := p.goqu.From(p.workspaceTable("guardrail_policies")).Select(guardrailPolicyColumns...).
		Where(goqu.Ex{"enabled": true}).Order(goqu.I("created_at").Asc(), goqu.I("id").Asc()).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("load guardrail policies: %w", err)
	}
	out := make([]service.GuardrailPolicy, 0, len(rows))
	for _, row := range rows {
		out = append(out, guardrailPolicyRecord(row))
	}
	return out, nil
}

// ListGuardrailPolicies returns the selected workspace's policies, plus the
// installation policies for a platform administrator.
func (p *Postgres) ListGuardrailPolicies(ctx context.Context) ([]service.GuardrailPolicy, error) {
	a, err := tracePrivacyActor(ctx)
	if err != nil {
		return nil, err
	}
	canWorkspace := tracePrivacyMayManage(a, a.WorkspaceID)
	if !canWorkspace && !a.PlatformAdmin {
		return nil, service.ErrAccessDenied
	}
	var scope []exp.Expression
	if canWorkspace {
		scope = append(scope, goqu.Ex{"workspace_id": a.WorkspaceID})
	}
	if a.PlatformAdmin {
		scope = append(scope, goqu.C("workspace_id").IsNull())
	}
	var rows []guardrailPolicyRow
	if err := p.goqu.From(p.workspaceTable("guardrail_policies")).Select(guardrailPolicyColumns...).
		Where(goqu.Or(scope...)).Order(goqu.I("created_at").Asc(), goqu.I("id").Asc()).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list guardrail policies: %w", err)
	}
	out := make([]service.GuardrailPolicy, 0, len(rows))
	for _, row := range rows {
		out = append(out, guardrailPolicyRecord(row))
	}
	return out, nil
}

func (p *Postgres) getGuardrailPolicy(ctx context.Context, id string) (*service.GuardrailPolicy, error) {
	var row guardrailPolicyRow
	found, err := p.goqu.From(p.workspaceTable("guardrail_policies")).Select(guardrailPolicyColumns...).
		Where(goqu.Ex{"id": id}).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("get guardrail policy: %w", err)
	}
	if !found {
		return nil, nil
	}
	r := guardrailPolicyRecord(row)
	return &r, nil
}

// visibleGuardrailPolicy loads a policy the caller may manage; anything else
// is reported as not found so policy IDs cannot be probed.
func (p *Postgres) visibleGuardrailPolicy(ctx context.Context, a service.AccessPrincipal, id string) (*service.GuardrailPolicy, error) {
	r, err := p.getGuardrailPolicy(ctx, id)
	if err != nil {
		return nil, err
	}
	if r == nil || !tracePrivacyMayManage(a, r.WorkspaceID) {
		return nil, service.ErrGuardrailPolicyNotFound
	}
	return r, nil
}

// SaveGuardrailPolicy creates (empty ID) or replaces a policy. The scope is
// fixed at creation: Scope "installation" creates an installation policy,
// anything else a policy in the selected workspace.
func (p *Postgres) SaveGuardrailPolicy(ctx context.Context, policy service.GuardrailPolicy) (*service.GuardrailPolicy, error) {
	a, err := tracePrivacyActor(ctx)
	if err != nil {
		return nil, err
	}
	table := p.workspaceTable("guardrail_policies")
	now := time.Now().UTC()
	if policy.ID == "" {
		policy.WorkspaceID = a.WorkspaceID
		if policy.Scope == "installation" {
			policy.WorkspaceID = ""
		}
		if !tracePrivacyMayManage(a, policy.WorkspaceID) {
			return nil, service.ErrAccessDenied
		}
	} else {
		current, err := p.visibleGuardrailPolicy(ctx, a, policy.ID)
		if err != nil {
			return nil, err
		}
		policy.WorkspaceID = current.WorkspaceID
	}
	policy, err = service.NormalizeGuardrailPolicy(policy)
	if err != nil {
		return nil, err
	}
	config, err := json.Marshal(policy.Config)
	if err != nil {
		return nil, fmt.Errorf("encode guardrail config: %w", err)
	}
	fields := goqu.Record{
		"name": policy.Name, "description": policy.Description, "enabled": policy.Enabled, "mode": policy.Mode,
		"user_id": policy.UserID, "token_id": policy.TokenID, "provider": policy.Provider, "model": policy.Model,
		"source": policy.Source, "config": string(config), "updated_at": now,
	}
	if policy.ID == "" {
		policy.ID = ulid.Make().String()
		var workspace any
		if policy.WorkspaceID != "" {
			workspace = policy.WorkspaceID
		}
		fields["id"], fields["workspace_id"], fields["created_by"], fields["created_at"] = policy.ID, workspace, a.UserID, now
		if _, err := p.goqu.Insert(table).Rows(fields).Executor().ExecContext(ctx); err != nil {
			return nil, fmt.Errorf("create guardrail policy: %w", err)
		}
	} else if _, err := p.goqu.Update(table).Set(fields).Where(goqu.Ex{"id": policy.ID}).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("update guardrail policy: %w", err)
	}
	return p.getGuardrailPolicy(ctx, policy.ID)
}

func (p *Postgres) DeleteGuardrailPolicy(ctx context.Context, id string) error {
	a, err := tracePrivacyActor(ctx)
	if err != nil {
		return err
	}
	if _, err := p.visibleGuardrailPolicy(ctx, a, id); err != nil {
		return err
	}
	if _, err := p.goqu.Delete(p.workspaceTable("guardrail_policies")).Where(goqu.Ex{"id": id}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("delete guardrail policy: %w", err)
	}
	return nil
}
