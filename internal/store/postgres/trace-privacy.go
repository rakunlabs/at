package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/doug-martin/goqu/v9/exp"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.TracePrivacyStorer = (*Postgres)(nil)

// tracePrivacyApplyBatch bounds one delete/update statement when a rule is
// applied to stored traces, so a large history does not hold one long lock.
const tracePrivacyApplyBatch = 500

// tracePrivacyRedactSet clears every column that carries content.
const tracePrivacyRedactSet = `request_body = '', response_body = '', request_truncated = FALSE, response_truncated = FALSE,
request_ref = '', response_ref = '', input = '', output = '', error_message = ''`

type tracePrivacyRuleRow struct {
	ID          string         `db:"id"`
	WorkspaceID sql.NullString `db:"workspace_id"`
	Description string         `db:"description"`
	UserID      string         `db:"user_id"`
	TokenID     string         `db:"token_id"`
	Provider    string         `db:"provider"`
	Model       string         `db:"model"`
	Source      string         `db:"source"`
	Action      string         `db:"action"`
	Enabled     bool           `db:"enabled"`
	CreatedBy   string         `db:"created_by"`
	CreatedAt   time.Time      `db:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at"`
}

func tracePrivacyRuleRecord(row tracePrivacyRuleRow) service.TracePrivacyRule {
	r := service.TracePrivacyRule{
		ID:          row.ID,
		WorkspaceID: row.WorkspaceID.String,
		Description: row.Description,
		UserID:      row.UserID,
		TokenID:     row.TokenID,
		Provider:    row.Provider,
		Model:       row.Model,
		Source:      row.Source,
		Action:      row.Action,
		Enabled:     row.Enabled,
		CreatedBy:   row.CreatedBy,
		CreatedAt:   row.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   row.UpdatedAt.UTC().Format(time.RFC3339),
	}
	r.Scope = "workspace"
	if r.WorkspaceID == "" {
		r.Scope = "installation"
	}
	return r
}

var tracePrivacyRuleColumns = []any{"id", "workspace_id", "description", "user_id", "token_id", "provider", "model", "source", "action", "enabled", "created_by", "created_at", "updated_at"}

// LoadTracePrivacyPolicy reads every enabled rule, the installation settings
// and (only when opt-out is allowed) the accounts that opted out. It is an
// internal lookup for the recorder and performs no authorization.
func (p *Postgres) LoadTracePrivacyPolicy(ctx context.Context) (service.TracePrivacyPolicy, error) {
	policy := service.TracePrivacyPolicy{OptedOutUsers: map[string]bool{}}
	var rows []tracePrivacyRuleRow
	if err := p.goqu.From(p.workspaceTable("trace_privacy_rules")).Select(tracePrivacyRuleColumns...).
		Where(goqu.Ex{"enabled": true}).Order(goqu.I("created_at").Asc()).ScanStructsContext(ctx, &rows); err != nil {
		return policy, fmt.Errorf("load trace privacy rules: %w", err)
	}
	for _, row := range rows {
		policy.Rules = append(policy.Rules, tracePrivacyRuleRecord(row))
	}
	settings, err := p.loadTracePrivacySettings(ctx)
	if err != nil {
		return policy, err
	}
	policy.Settings = settings
	if settings.AllowUserOptOut {
		var users []string
		if err := p.goqu.From(p.tableUserPreferences).Select("user_id").
			Where(goqu.Ex{"key": service.TracePrivacyOptOutKey, "secret": false}, goqu.L("value = 'true'::jsonb")).
			ScanValsContext(ctx, &users); err != nil {
			return policy, fmt.Errorf("load trace opt-outs: %w", err)
		}
		for _, u := range users {
			policy.OptedOutUsers[u] = true
		}
	}
	return policy, nil
}

func (p *Postgres) loadTracePrivacySettings(ctx context.Context) (service.TracePrivacySettings, error) {
	var row struct {
		Allow     bool      `db:"allow_user_opt_out"`
		UpdatedAt time.Time `db:"updated_at"`
	}
	found, err := p.goqu.From(p.workspaceTable("trace_privacy_settings")).Select("allow_user_opt_out", "updated_at").
		Where(goqu.Ex{"id": "default"}).ScanStructContext(ctx, &row)
	if err != nil {
		return service.TracePrivacySettings{}, fmt.Errorf("load trace privacy settings: %w", err)
	}
	if !found {
		return service.TracePrivacySettings{}, nil
	}
	return service.TracePrivacySettings{AllowUserOptOut: row.Allow, UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339)}, nil
}

// tracePrivacyActor returns the caller's principal. Workspace rules need
// workspace.write in the selected workspace; installation rules need a
// platform administrator.
func tracePrivacyActor(ctx context.Context) (service.AccessPrincipal, error) {
	a, ok := service.AccessPrincipalFromContext(ctx)
	if !ok || a.WorkspaceID == "" {
		return a, service.ErrWorkspaceRequired
	}
	return a, nil
}

func tracePrivacyMayManage(a service.AccessPrincipal, workspaceID string) bool {
	if workspaceID == "" {
		return a.PlatformAdmin
	}
	return workspaceID == a.WorkspaceID && a.Allows("workspace.write", service.AccessResource{WorkspaceID: a.WorkspaceID})
}

// ListTracePrivacyRules returns the selected workspace's rules, plus the
// installation rules for a platform administrator.
func (p *Postgres) ListTracePrivacyRules(ctx context.Context) ([]service.TracePrivacyRule, error) {
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
	var rows []tracePrivacyRuleRow
	if err := p.goqu.From(p.workspaceTable("trace_privacy_rules")).Select(tracePrivacyRuleColumns...).
		Where(goqu.Or(scope...)).Order(goqu.I("created_at").Asc(), goqu.I("id").Asc()).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list trace privacy rules: %w", err)
	}
	out := make([]service.TracePrivacyRule, 0, len(rows))
	for _, row := range rows {
		out = append(out, tracePrivacyRuleRecord(row))
	}
	return out, nil
}

func (p *Postgres) getTracePrivacyRule(ctx context.Context, id string) (*service.TracePrivacyRule, error) {
	var row tracePrivacyRuleRow
	found, err := p.goqu.From(p.workspaceTable("trace_privacy_rules")).Select(tracePrivacyRuleColumns...).
		Where(goqu.Ex{"id": id}).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("get trace privacy rule: %w", err)
	}
	if !found {
		return nil, nil
	}
	r := tracePrivacyRuleRecord(row)
	return &r, nil
}

// visibleTracePrivacyRule loads a rule the caller may manage; anything else
// (unknown, another workspace's, an installation rule for a non-administrator)
// is reported as not found so rule IDs cannot be probed.
func (p *Postgres) visibleTracePrivacyRule(ctx context.Context, a service.AccessPrincipal, id string) (*service.TracePrivacyRule, error) {
	r, err := p.getTracePrivacyRule(ctx, id)
	if err != nil {
		return nil, err
	}
	if r == nil || !tracePrivacyMayManage(a, r.WorkspaceID) {
		return nil, service.ErrTracePrivacyRuleNotFound
	}
	return r, nil
}

// SaveTracePrivacyRule creates (empty ID) or replaces a rule. The scope is
// fixed at creation: Scope "installation" creates an installation rule,
// anything else a rule in the selected workspace.
func (p *Postgres) SaveTracePrivacyRule(ctx context.Context, rule service.TracePrivacyRule) (*service.TracePrivacyRule, error) {
	a, err := tracePrivacyActor(ctx)
	if err != nil {
		return nil, err
	}
	table := p.workspaceTable("trace_privacy_rules")
	now := time.Now().UTC()
	if rule.ID == "" {
		rule.WorkspaceID = a.WorkspaceID
		if rule.Scope == "installation" {
			rule.WorkspaceID = ""
		}
		if !tracePrivacyMayManage(a, rule.WorkspaceID) {
			return nil, service.ErrAccessDenied
		}
		rule, err = service.NormalizeTracePrivacyRule(rule)
		if err != nil {
			return nil, err
		}
		rule.ID = ulid.Make().String()
		var workspace any
		if rule.WorkspaceID != "" {
			workspace = rule.WorkspaceID
		}
		if _, err := p.goqu.Insert(table).Rows(goqu.Record{
			"id": rule.ID, "workspace_id": workspace, "description": rule.Description,
			"user_id": rule.UserID, "token_id": rule.TokenID, "provider": rule.Provider, "model": rule.Model, "source": rule.Source,
			"action": rule.Action, "enabled": rule.Enabled, "created_by": a.UserID, "created_at": now, "updated_at": now,
		}).Executor().ExecContext(ctx); err != nil {
			return nil, fmt.Errorf("create trace privacy rule: %w", err)
		}
		return p.getTracePrivacyRule(ctx, rule.ID)
	}
	current, err := p.visibleTracePrivacyRule(ctx, a, rule.ID)
	if err != nil {
		return nil, err
	}
	rule.WorkspaceID = current.WorkspaceID
	rule, err = service.NormalizeTracePrivacyRule(rule)
	if err != nil {
		return nil, err
	}
	if _, err := p.goqu.Update(table).Set(goqu.Record{
		"description": rule.Description, "user_id": rule.UserID, "token_id": rule.TokenID, "provider": rule.Provider,
		"model": rule.Model, "source": rule.Source, "action": rule.Action, "enabled": rule.Enabled, "updated_at": now,
	}).Where(goqu.Ex{"id": rule.ID}).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("update trace privacy rule: %w", err)
	}
	return p.getTracePrivacyRule(ctx, rule.ID)
}

func (p *Postgres) DeleteTracePrivacyRule(ctx context.Context, id string) error {
	a, err := tracePrivacyActor(ctx)
	if err != nil {
		return err
	}
	if _, err := p.visibleTracePrivacyRule(ctx, a, id); err != nil {
		return err
	}
	if _, err := p.goqu.Delete(p.workspaceTable("trace_privacy_rules")).Where(goqu.Ex{"id": id}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("delete trace privacy rule: %w", err)
	}
	return nil
}

func (p *Postgres) GetTracePrivacySettings(ctx context.Context) (service.TracePrivacySettings, error) {
	return p.loadTracePrivacySettings(ctx)
}

func (p *Postgres) SaveTracePrivacySettings(ctx context.Context, s service.TracePrivacySettings) (service.TracePrivacySettings, error) {
	a, err := tracePrivacyActor(ctx)
	if err != nil {
		return s, err
	}
	if !a.PlatformAdmin {
		return s, service.ErrAccessDenied
	}
	if _, err := p.goqu.Insert(p.workspaceTable("trace_privacy_settings")).Rows(goqu.Record{
		"id": "default", "allow_user_opt_out": s.AllowUserOptOut, "updated_by": a.UserID, "updated_at": time.Now().UTC(),
	}).OnConflict(goqu.DoUpdate("id", goqu.Record{
		"allow_user_opt_out": s.AllowUserOptOut, "updated_by": a.UserID, "updated_at": time.Now().UTC(),
	})).Executor().ExecContext(ctx); err != nil {
		return s, fmt.Errorf("save trace privacy settings: %w", err)
	}
	return p.loadTracePrivacySettings(ctx)
}

// tracePrivacyMatchSQL builds the predicate that selects the observations a
// rule matches, mirroring TracePrivacyRule.Matches.
func (p *Postgres) tracePrivacyMatchSQL(r service.TracePrivacyRule, alias string) (string, []any) {
	var args sqlArgs
	col := func(c string) string { return alias + "." + c }
	where := []string{}
	if r.WorkspaceID != "" {
		where = append(where, col("workspace_id")+" = "+args.add(r.WorkspaceID))
	}
	for c, v := range map[string]string{"user_id": r.UserID, "token_id": r.TokenID, "provider": r.Provider, "source": r.Source} {
		if v != "" {
			where = append(where, col(c)+" = "+args.add(v))
		}
	}
	if r.Model != "" {
		subject := col("model")
		if strings.Contains(r.Model, "/") {
			subject = col("provider") + " || '/' || " + col("model")
		}
		where = append(where, col("model")+" <> ''", "lower("+subject+") LIKE "+args.add(service.GlobToLike(strings.ToLower(r.Model)))+` ESCAPE '\'`)
	}
	if len(where) == 0 {
		where = append(where, "TRUE")
	}
	return strings.Join(where, " AND "), args
}

// ApplyTracePrivacyRule applies a rule to traces already stored. A skip rule
// deletes every trace containing a matching observation (all its
// observations, attributes, scores and bookmarks); a redact rule strips the
// content of matching observations. A dry run only counts.
func (p *Postgres) ApplyTracePrivacyRule(ctx context.Context, id string, dryRun bool) (*service.TracePrivacyApplyResult, error) {
	a, err := tracePrivacyActor(ctx)
	if err != nil {
		return nil, err
	}
	rule, err := p.visibleTracePrivacyRule(ctx, a, id)
	if err != nil {
		return nil, err
	}
	calls := p.tableLLMCalls.GetTable()
	match, args := p.tracePrivacyMatchSQL(*rule, "c")
	res := &service.TracePrivacyApplyResult{DryRun: dryRun, Action: rule.Action}

	// Both actions apply to whole traces containing a match, the same unit the
	// recorder suppresses: a trace's tool results and run output carry the
	// conversation as much as the matching generation does.
	countSQL := fmt.Sprintf(`WITH m AS (SELECT DISTINCT c.workspace_id, c.trace_id FROM %[1]s c WHERE %[2]s)
SELECT (SELECT count(*) FROM m), (SELECT count(*) FROM %[1]s x JOIN m ON x.workspace_id = m.workspace_id AND x.trace_id = m.trace_id)`, calls, match)
	if err := p.db.QueryRowContext(ctx, countSQL, args...).Scan(&res.Traces, &res.Observations); err != nil {
		return nil, fmt.Errorf("count trace privacy matches: %w", err)
	}
	if dryRun {
		return res, nil
	}

	if rule.Action == service.TracePrivacyRedact {
		refsSQL := fmt.Sprintf(`SELECT x.request_ref, x.response_ref FROM %[1]s x
JOIN (SELECT DISTINCT c.workspace_id, c.trace_id FROM %[1]s c WHERE %[2]s) m ON x.workspace_id = m.workspace_id AND x.trace_id = m.trace_id
WHERE x.request_ref <> '' OR x.response_ref <> ''`, calls, match)
		refs, err := p.collectSpillRefs(ctx, refsSQL, args)
		if err != nil {
			return nil, err
		}
		res.SpillRefs = refs
		tracesSQL := fmt.Sprintf(`UPDATE %s t SET input = '', output = '' FROM (SELECT DISTINCT c.workspace_id, c.trace_id FROM %s c WHERE %s) m
WHERE t.workspace_id = m.workspace_id AND t.trace_id = m.trace_id`, p.tableLLMTraces.GetTable(), calls, match)
		if _, err := p.db.ExecContext(ctx, tracesSQL, args...); err != nil {
			return nil, fmt.Errorf("redact trace previews: %w", err)
		}
		updateSQL := fmt.Sprintf(`UPDATE %[1]s x SET %[3]s FROM (SELECT DISTINCT c.workspace_id, c.trace_id FROM %[1]s c WHERE %[2]s) m
WHERE x.workspace_id = m.workspace_id AND x.trace_id = m.trace_id`, calls, match, tracePrivacyRedactSet)
		if _, err := p.db.ExecContext(ctx, updateSQL, args...); err != nil {
			return nil, fmt.Errorf("redact observations: %w", err)
		}
		slog.Info("trace privacy rule applied", "rule_id", rule.ID, "action", rule.Action, "actor", a.UserID, "traces", res.Traces, "observations", res.Observations)
		return res, nil
	}

	pairSQL := fmt.Sprintf(`SELECT DISTINCT c.workspace_id, c.trace_id FROM %s c WHERE %s LIMIT %d`, calls, match, tracePrivacyApplyBatch)
	for {
		rows, err := p.db.QueryContext(ctx, pairSQL, args...)
		if err != nil {
			return nil, fmt.Errorf("select traces to delete: %w", err)
		}
		var pairs [][2]string
		for rows.Next() {
			var pair [2]string
			if err := rows.Scan(&pair[0], &pair[1]); err != nil {
				rows.Close()
				return nil, err
			}
			pairs = append(pairs, pair)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if len(pairs) == 0 {
			break
		}
		for _, pair := range pairs {
			refs, err := p.deleteTrace(ctx, pair[0], pair[1])
			if err != nil {
				return nil, err
			}
			res.SpillRefs = append(res.SpillRefs, refs...)
		}
	}
	slog.Info("trace privacy rule applied", "rule_id", rule.ID, "action", rule.Action, "actor", a.UserID, "traces", res.Traces, "observations", res.Observations)
	return res, nil
}

func (p *Postgres) collectSpillRefs(ctx context.Context, query string, args []any) ([]string, error) {
	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("collect spill refs: %w", err)
	}
	defer rows.Close()
	var refs []string
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			return nil, err
		}
		for _, v := range []string{a, b} {
			if v != "" {
				refs = append(refs, v)
			}
		}
	}
	return refs, rows.Err()
}

// deleteTrace removes one trace with everything hanging off it and returns
// the spill files its observations referenced.
func (p *Postgres) deleteTrace(ctx context.Context, workspaceID, traceID string) ([]string, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin trace delete: %w", err)
	}
	defer tx.Rollback()
	calls := p.tableLLMCalls.GetTable()
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE workspace_id = $1 AND trace_id = $2 RETURNING request_ref, response_ref`, calls), workspaceID, traceID)
	if err != nil {
		return nil, fmt.Errorf("delete trace observations: %w", err)
	}
	var refs []string
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			rows.Close()
			return nil, err
		}
		for _, v := range []string{a, b} {
			if v != "" {
				refs = append(refs, v)
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, table := range []string{p.tableTraceScores.GetTable(), p.tableTraceBookmarks.GetTable(), p.tableLLMTraces.GetTable()} {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE workspace_id = $1 AND trace_id = $2`, table), workspaceID, traceID); err != nil {
			return nil, fmt.Errorf("delete trace rows: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit trace delete: %w", err)
	}
	return refs, nil
}

// ApplyTracePrivacyToTrace is the recorder's retroactive path: observations of
// a trace written before a later observation decided the trace must not be
// kept (or kept without content). Internal; performs no authorization.
func (p *Postgres) ApplyTracePrivacyToTrace(ctx context.Context, workspaceID, traceID, action string) ([]string, error) {
	if workspaceID == "" || traceID == "" {
		return nil, errors.New("workspace and trace are required")
	}
	switch action {
	case service.TracePrivacySkip:
		return p.deleteTrace(ctx, workspaceID, traceID)
	case service.TracePrivacyRedact:
		calls := p.tableLLMCalls.GetTable()
		refs, err := p.collectSpillRefs(ctx, fmt.Sprintf(`SELECT request_ref, response_ref FROM %s WHERE workspace_id = $1 AND trace_id = $2 AND (request_ref <> '' OR response_ref <> '')`, calls), []any{workspaceID, traceID})
		if err != nil {
			return nil, err
		}
		if _, err := p.db.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET %s WHERE workspace_id = $1 AND trace_id = $2`, calls, tracePrivacyRedactSet), workspaceID, traceID); err != nil {
			return nil, fmt.Errorf("redact trace observations: %w", err)
		}
		if _, err := p.db.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET input = '', output = '' WHERE workspace_id = $1 AND trace_id = $2`, p.tableLLMTraces.GetTable()), workspaceID, traceID); err != nil {
			return nil, fmt.Errorf("redact trace preview: %w", err)
		}
		return refs, nil
	}
	return nil, nil
}
