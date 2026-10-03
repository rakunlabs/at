package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.TraceStorer = (*Postgres)(nil)

// traceListPreviewBytes bounds the input/output preview on list rows.
const traceListPreviewBytes = 512

// textArray passes a Go string slice as a Postgres text[] parameter.
func textArray(v []string) any {
	if v == nil {
		v = []string{}
	}
	return v
}

// textArrayScan scans a Postgres text[] value. pgx's database/sql driver
// reports arrays in their text form, so this parses the literal.
type textArrayScan []string

func (a *textArrayScan) Scan(src any) error {
	var s string
	switch v := src.(type) {
	case nil:
		*a = nil
		return nil
	case string:
		s = v
	case []byte:
		s = string(v)
	case []string:
		*a = v
		return nil
	default:
		return fmt.Errorf("unsupported text array type %T", src)
	}
	out, err := parseTextArray(s)
	if err != nil {
		return err
	}
	*a = out
	return nil
}

// parseTextArray parses a one-dimensional Postgres array literal.
func parseTextArray(s string) ([]string, error) {
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return nil, fmt.Errorf("invalid text array %q", s)
	}
	body := s[1 : len(s)-1]
	out := []string{}
	if body == "" {
		return out, nil
	}
	var cur strings.Builder
	quoted, inQuotes, escaped := false, false, false
	flush := func() {
		v := cur.String()
		if !quoted && v == "NULL" {
			v = ""
		}
		out = append(out, v)
		cur.Reset()
		quoted = false
	}
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case escaped:
			cur.WriteByte(c)
			escaped = false
		case c == '\\':
			escaped = true
		case c == '"':
			inQuotes = !inQuotes
			quoted = true
		case c == ',' && !inQuotes:
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out, nil
}

// sqlArgs accumulates positional parameters for hand-built statements.
type sqlArgs []any

func (a *sqlArgs) add(v any) string {
	*a = append(*a, v)
	return fmt.Sprintf("$%d", len(*a))
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// traceScopeWorkspace returns the workspace a trace read is bounded to. A
// caller without a scoped principal (the legacy installation view) is
// unbounded, matching the existing trace endpoints.
func traceScopeWorkspace(ctx context.Context) (string, bool) {
	return traceWorkspaceScope(ctx)
}

// traceAggregateSQL builds the per-trace aggregate over observations. The
// window bounds observations; attribute filters are applied by the caller as
// HAVING terms so they select whole traces.
func (p *Postgres) traceAggregateSQL(ctx context.Context, args *sqlArgs, from, to time.Time, traceIDs []string) string {
	calls := p.tableLLMCalls.GetTable()
	where := []string{"c.trace_id <> ''"}
	if ws, ok := traceScopeWorkspace(ctx); ok {
		where = append(where, "c.workspace_id = "+args.add(ws))
	}
	if !from.IsZero() {
		where = append(where, "c.started_at >= "+args.add(from))
	}
	if !to.IsZero() {
		where = append(where, "c.started_at < "+args.add(to))
	}
	if len(traceIDs) > 0 {
		where = append(where, "c.trace_id = ANY("+args.add(textArray(traceIDs))+")")
	}
	return `SELECT c.workspace_id, c.trace_id,
  MIN(c.started_at) AS started_at, MAX(c.ended_at) AS ended_at,
  MAX(c.session_id) AS session_id,
  (array_agg(c.source ORDER BY c.started_at, c.id))[1] AS source,
  MAX(c.task_id) AS task_id, MAX(c.agent_id) AS agent_id, MAX(c.organization_id) AS organization_id,
  MAX(c.token_id) AS token_id, MAX(c.user_id) AS user_id, MAX(c.user_field) AS user_field,
  MAX(c.environment) AS environment, MAX(c.release) AS release,
  COALESCE((array_agg(c.name ORDER BY c.started_at, c.id) FILTER (WHERE c.name <> ''))[1], '') AS first_name,
  COALESCE(array_agg(DISTINCT c.model) FILTER (WHERE c.model <> ''), '{}') AS models,
  COUNT(*) AS observation_count,
  COUNT(*) FILTER (WHERE c.observation_type = 'generation') AS generation_count,
  COALESCE(SUM(c.input_tokens), 0) AS input_tokens,
  COALESCE(SUM(c.output_tokens), 0) AS output_tokens,
  COALESCE(SUM(c.cache_read_tokens), 0) AS cache_read_tokens,
  COALESCE(SUM(c.cache_write_tokens), 0) AS cache_write_tokens,
  COALESCE(SUM(c.cost_cents), 0) AS cost_cents,
  COALESCE(SUM(c.latency_ms) FILTER (WHERE c.observation_type IN ('generation', 'tool', 'embedding')), 0) AS latency_ms_total,
  COUNT(*) FILTER (WHERE c.status = 'error' OR c.level = 'error') AS error_count,
  bool_or(true) AS any_row
FROM ` + calls + ` c
WHERE ` + strings.Join(where, " AND ") + `
GROUP BY c.workspace_id, c.trace_id`
}

// traceHaving turns the attribute and aggregate filters into predicates over
// the aggregate (alias a) joined with llm_traces (alias t).
func (p *Postgres) traceFilters(ctx context.Context, args *sqlArgs, q service.TraceQuery) []string {
	calls := p.tableLLMCalls.GetTable()
	var where []string
	// Attribute filters select a trace when any observation matches.
	anyObs := func(column string, values []string) {
		if len(values) == 0 {
			return
		}
		where = append(where, fmt.Sprintf("EXISTS (SELECT 1 FROM %s o WHERE o.workspace_id = a.workspace_id AND o.trace_id = a.trace_id AND o.%s = ANY(%s))", calls, column, args.add(textArray(values))))
	}
	anyObs("model", q.Models)
	anyObs("source", q.Sources)
	anyObs("task_id", q.TaskIDs)
	anyObs("agent_id", q.AgentIDs)
	anyObs("user_id", q.UserIDs)
	anyObs("environment", q.Environments)
	anyObs("release", q.Releases)
	anyObs("token_id", q.TokenIDs)
	if len(q.SessionIDs) > 0 {
		where = append(where, "a.session_id = ANY("+args.add(textArray(q.SessionIDs))+")")
	}
	if len(q.EndUsers) > 0 {
		where = append(where, "COALESCE(NULLIF(t.end_user, ''), a.user_field) = ANY("+args.add(textArray(q.EndUsers))+")")
	}
	if len(q.Names) > 0 {
		where = append(where, "COALESCE(NULLIF(t.name, ''), a.first_name) = ANY("+args.add(textArray(q.Names))+")")
	}
	if len(q.Tags) > 0 {
		where = append(where, "t.tags && "+args.add(textArray(q.Tags))+"::text[]")
	}
	switch q.Status {
	case "error":
		where = append(where, "a.error_count > 0")
	case "ok":
		where = append(where, "a.error_count = 0")
	}
	if q.Search != "" {
		like := args.add("%" + escapeLike(q.Search) + "%")
		where = append(where, fmt.Sprintf("(a.trace_id ILIKE %[1]s OR a.session_id ILIKE %[1]s OR COALESCE(NULLIF(t.name, ''), a.first_name) ILIKE %[1]s OR COALESCE(t.input, '') ILIKE %[1]s)", like))
	}
	duration := "(EXTRACT(EPOCH FROM (a.ended_at - a.started_at)) * 1000)"
	if q.MinLatencyMs != nil {
		where = append(where, duration+" >= "+args.add(*q.MinLatencyMs))
	}
	if q.MaxLatencyMs != nil {
		where = append(where, duration+" <= "+args.add(*q.MaxLatencyMs))
	}
	if q.MinCostCents != nil {
		where = append(where, "a.cost_cents >= "+args.add(*q.MinCostCents))
	}
	if q.MaxCostCents != nil {
		where = append(where, "a.cost_cents <= "+args.add(*q.MaxCostCents))
	}
	tokens := "(a.input_tokens + a.output_tokens)"
	if q.MinTokens != nil {
		where = append(where, tokens+" >= "+args.add(*q.MinTokens))
	}
	if q.MaxTokens != nil {
		where = append(where, tokens+" <= "+args.add(*q.MaxTokens))
	}
	if q.ScoreName != "" {
		cond := []string{"s.workspace_id = a.workspace_id", "s.trace_id = a.trace_id", "s.name = " + args.add(q.ScoreName)}
		if q.MinScore != nil {
			cond = append(cond, "s.value >= "+args.add(*q.MinScore))
		}
		if q.MaxScore != nil {
			cond = append(cond, "s.value <= "+args.add(*q.MaxScore))
		}
		where = append(where, fmt.Sprintf("EXISTS (SELECT 1 FROM %s s WHERE %s)", p.tableTraceScores.GetTable(), strings.Join(cond, " AND ")))
	}
	if q.Bookmarked {
		where = append(where, fmt.Sprintf("EXISTS (SELECT 1 FROM %s b WHERE b.workspace_id = a.workspace_id AND b.trace_id = a.trace_id AND b.user_id = %s)", p.tableTraceBookmarks.GetTable(), args.add(traceBookmarkUser(ctx))))
	}
	return where
}

func traceBookmarkUser(ctx context.Context) string {
	if a, ok := service.AccessPrincipalFromContext(ctx); ok {
		return a.UserID
	}
	return ""
}

func traceOrder(q service.TraceQuery) string {
	dir := "ASC"
	if q.Desc || q.Sort == "" {
		dir = "DESC"
	}
	col := "a.started_at"
	switch q.Sort {
	case "duration":
		col = "(a.ended_at - a.started_at)"
	case "latency":
		col = "a.latency_ms_total"
	case "cost":
		col = "a.cost_cents"
	case "tokens":
		col = "(a.input_tokens + a.output_tokens)"
	case "errors":
		col = "a.error_count"
	case "observations":
		col = "a.observation_count"
	}
	return fmt.Sprintf("%s %s, a.trace_id %s", col, dir, dir)
}

func (p *Postgres) ListTraces(ctx context.Context, q service.TraceQuery) (*service.ListResult[service.TraceSummary], error) {
	limit := q.Limit
	if limit == 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	var args sqlArgs
	agg := p.traceAggregateSQL(ctx, &args, q.From, q.To, q.TraceIDs)
	filters := p.traceFilters(ctx, &args, q)
	base := fmt.Sprintf("FROM (%s) a LEFT JOIN %s t ON t.workspace_id = a.workspace_id AND t.trace_id = a.trace_id", agg, p.tableLLMTraces.GetTable())
	if len(filters) > 0 {
		base += " WHERE " + strings.Join(filters, " AND ")
	}

	var total uint64
	if err := p.db.QueryRowContext(ctx, "SELECT COUNT(*) "+base, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count traces: %w", err)
	}

	userParam := args.add(traceBookmarkUser(ctx))
	limitParam := args.add(int64(limit))
	offsetParam := args.add(int64(q.Offset))
	query := fmt.Sprintf(`SELECT a.workspace_id, a.trace_id, a.session_id, a.source,
  COALESCE(NULLIF(t.name, ''), a.first_name), a.task_id, a.agent_id, a.organization_id,
  a.token_id, a.user_id, COALESCE(NULLIF(t.end_user, ''), a.user_field), a.environment, a.release,
  COALESCE(t.tags, '{}'), a.models,
  substr(COALESCE(t.input, ''), 1, %[1]d), substr(COALESCE(t.output, ''), 1, %[1]d),
  a.observation_count, a.generation_count, a.input_tokens, a.output_tokens, a.cache_read_tokens, a.cache_write_tokens,
  a.cost_cents, a.latency_ms_total, a.error_count, a.started_at, a.ended_at,
  EXISTS (SELECT 1 FROM %[2]s b WHERE b.workspace_id = a.workspace_id AND b.trace_id = a.trace_id AND b.user_id = %[3]s)
%[4]s ORDER BY %[5]s LIMIT %[6]s OFFSET %[7]s`,
		traceListPreviewBytes, p.tableTraceBookmarks.GetTable(), userParam, base, traceOrder(q), limitParam, offsetParam)
	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list traces: %w", err)
	}
	defer rows.Close()

	items := make([]service.TraceSummary, 0)
	type key struct{ ws, trace string }
	var keys []key
	for rows.Next() {
		var t service.TraceSummary
		var ws string
		var started, ended time.Time
		var tags, models textArrayScan
		if err := rows.Scan(&ws, &t.TraceID, &t.SessionID, &t.Source,
			&t.Name, &t.TaskID, &t.AgentID, &t.OrganizationID,
			&t.TokenID, &t.UserID, &t.EndUser, &t.Environment, &t.Release,
			&tags, &models, &t.Input, &t.Output,
			&t.ObservationCount, &t.GenerationCount, &t.InputTokens, &t.OutputTokens, &t.CacheReadTokens, &t.CacheWriteTokens,
			&t.CostCents, &t.LatencyMsTotal, &t.ErrorCount, &started, &ended, &t.Bookmarked); err != nil {
			return nil, fmt.Errorf("scan trace: %w", err)
		}
		t.Tags = append([]string{}, tags...)
		t.Models = append([]string{}, models...)
		t.StartedAt = started.UTC().Format(time.RFC3339Nano)
		t.EndedAt = ended.UTC().Format(time.RFC3339Nano)
		t.DurationMs = ended.Sub(started).Milliseconds()
		t.TotalTokens = t.InputTokens + t.OutputTokens
		t.Scores = []service.TraceScoreSummary{}
		items = append(items, t)
		keys = append(keys, key{ws, t.TraceID})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read traces: %w", err)
	}

	if len(items) > 0 {
		ids := make([]string, len(keys))
		for i, k := range keys {
			ids[i] = k.trace
		}
		summaries, err := p.traceScoreSummaries(ctx, ids)
		if err != nil {
			return nil, err
		}
		for i := range items {
			if s, ok := summaries[keys[i]]; ok {
				items[i].Scores = s
			}
		}
	}

	return &service.ListResult[service.TraceSummary]{Data: items, Meta: service.ListMeta{Total: total, Offset: q.Offset, Limit: limit}}, nil
}

// traceScoreSummaries aggregates scores per (workspace, trace, name).
func (p *Postgres) traceScoreSummaries(ctx context.Context, traceIDs []string) (map[struct{ ws, trace string }][]service.TraceScoreSummary, error) {
	var args sqlArgs
	where := []string{"trace_id = ANY(" + args.add(textArray(traceIDs)) + ")"}
	if ws, ok := traceScopeWorkspace(ctx); ok {
		where = append(where, "workspace_id = "+args.add(ws))
	}
	query := fmt.Sprintf(`SELECT workspace_id, trace_id, name, MAX(data_type), AVG(value), COALESCE(mode() WITHIN GROUP (ORDER BY string_value), ''), COUNT(*)
FROM %s WHERE %s GROUP BY workspace_id, trace_id, name ORDER BY name`, p.tableTraceScores.GetTable(), strings.Join(where, " AND "))
	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list trace score summaries: %w", err)
	}
	defer rows.Close()
	out := map[struct{ ws, trace string }][]service.TraceScoreSummary{}
	for rows.Next() {
		var ws, trace string
		var s service.TraceScoreSummary
		var avg sql.NullFloat64
		if err := rows.Scan(&ws, &trace, &s.Name, &s.DataType, &avg, &s.StringValue, &s.Count); err != nil {
			return nil, fmt.Errorf("scan trace score summary: %w", err)
		}
		if avg.Valid {
			v := avg.Float64
			s.Average = &v
		}
		if s.DataType != service.ScoreCategorical {
			s.StringValue = ""
		}
		k := struct{ ws, trace string }{ws, trace}
		out[k] = append(out[k], s)
	}
	return out, rows.Err()
}

func (p *Postgres) GetTrace(ctx context.Context, traceID string) (*service.TraceDetail, error) {
	if traceID == "" {
		return nil, nil
	}
	list, err := p.ListTraces(ctx, service.TraceQuery{TraceIDs: []string{traceID}, Limit: 2})
	if err != nil {
		return nil, err
	}
	if len(list.Data) == 0 {
		return nil, nil
	}
	detail := &service.TraceDetail{Trace: list.Data[0], Observations: []service.LLMCall{}, Scores: []service.TraceScore{}}

	ds := p.goqu.From(p.tableLLMCalls).Select(llmCallListColumns()...).
		Where(goqu.C("trace_id").Eq(traceID)).
		Order(goqu.C("started_at").Asc(), goqu.C("id").Asc()).
		Limit(service.TraceObservationLimit + 1)
	if ws, ok := traceScopeWorkspace(ctx); ok {
		ds = ds.Where(goqu.C("workspace_id").Eq(ws))
	}
	query, _, err := ds.ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build trace observations query: %w", err)
	}
	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list trace observations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		row, err := scanLLMCallRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan trace observation: %w", err)
		}
		detail.Observations = append(detail.Observations, llmCallRowToRecord(row))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read trace observations: %w", err)
	}
	if len(detail.Observations) > service.TraceObservationLimit {
		detail.Observations = detail.Observations[:service.TraceObservationLimit]
		detail.Truncated = true
	}

	// The full trace-level input/output (list rows carry a short preview).
	var args sqlArgs
	where := []string{"trace_id = " + args.add(traceID)}
	if ws, ok := traceScopeWorkspace(ctx); ok {
		where = append(where, "workspace_id = "+args.add(ws))
	}
	var input, output string
	err = p.db.QueryRowContext(ctx, fmt.Sprintf("SELECT input, output FROM %s WHERE %s LIMIT 1", p.tableLLMTraces.GetTable(), strings.Join(where, " AND ")), args...).Scan(&input, &output)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read trace attributes: %w", err)
	}
	if err == nil {
		detail.Trace.Input, detail.Trace.Output = input, output
	}

	scores, err := p.listTraceScores(ctx, traceID)
	if err != nil {
		return nil, err
	}
	detail.Scores = scores
	return detail, nil
}

func (p *Postgres) listTraceScores(ctx context.Context, traceID string) ([]service.TraceScore, error) {
	var args sqlArgs
	where := []string{"trace_id = " + args.add(traceID)}
	if ws, ok := traceScopeWorkspace(ctx); ok {
		where = append(where, "workspace_id = "+args.add(ws))
	}
	rows, err := p.db.QueryContext(ctx, fmt.Sprintf(`SELECT id, trace_id, observation_id, name, data_type, value, string_value, source, comment, author_user_id, token_id, created_at
FROM %s WHERE %s ORDER BY created_at, id`, p.tableTraceScores.GetTable(), strings.Join(where, " AND ")), args...)
	if err != nil {
		return nil, fmt.Errorf("list trace scores: %w", err)
	}
	defer rows.Close()
	out := make([]service.TraceScore, 0)
	for rows.Next() {
		s, err := scanTraceScore(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func scanTraceScore(scanner interface{ Scan(...any) error }) (service.TraceScore, error) {
	var s service.TraceScore
	var value sql.NullFloat64
	var created time.Time
	if err := scanner.Scan(&s.ID, &s.TraceID, &s.ObservationID, &s.Name, &s.DataType, &value, &s.StringValue, &s.Source, &s.Comment, &s.AuthorUserID, &s.TokenID, &created); err != nil {
		return s, fmt.Errorf("scan trace score: %w", err)
	}
	if value.Valid {
		v := value.Float64
		s.Value = &v
	}
	s.CreatedAt = created.UTC().Format(time.RFC3339Nano)
	return s, nil
}

func (p *Postgres) ListTraceSessions(ctx context.Context, q service.TraceSessionQuery) (*service.ListResult[service.TraceSession], error) {
	limit := q.Limit
	if limit == 0 {
		limit = 25
	}
	if limit > 200 {
		limit = 200
	}
	var args sqlArgs
	where := []string{"c.session_id <> ''", "c.trace_id <> ''"}
	if ws, ok := traceScopeWorkspace(ctx); ok {
		where = append(where, "c.workspace_id = "+args.add(ws))
	}
	if !q.From.IsZero() {
		where = append(where, "c.started_at >= "+args.add(q.From))
	}
	if !q.To.IsZero() {
		where = append(where, "c.started_at < "+args.add(q.To))
	}
	var having []string
	if len(q.UserIDs) > 0 {
		having = append(having, "bool_or(c.user_id = ANY("+args.add(textArray(q.UserIDs))+"))")
	}
	if len(q.Sources) > 0 {
		having = append(having, "bool_or(c.source = ANY("+args.add(textArray(q.Sources))+"))")
	}
	if q.Search != "" {
		like := args.add("%" + escapeLike(q.Search) + "%")
		having = append(having, fmt.Sprintf("(c.session_id ILIKE %[1]s OR bool_or(c.name ILIKE %[1]s) OR bool_or(c.user_field ILIKE %[1]s))", like))
	}
	grouped := fmt.Sprintf(`FROM %s c WHERE %s GROUP BY c.session_id, c.token_id`, p.tableLLMCalls.GetTable(), strings.Join(where, " AND "))
	if len(having) > 0 {
		grouped += " HAVING " + strings.Join(having, " AND ")
	}
	var total uint64
	if err := p.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM (SELECT 1 "+grouped+") s", args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count trace sessions: %w", err)
	}
	limitParam := args.add(int64(limit))
	offsetParam := args.add(int64(q.Offset))
	rows, err := p.db.QueryContext(ctx, fmt.Sprintf(`SELECT c.session_id, c.token_id, MAX(c.user_id), MAX(c.user_field),
  COALESCE(array_agg(DISTINCT c.source), '{}'),
  COALESCE((array_agg(c.name ORDER BY c.started_at, c.id) FILTER (WHERE c.name <> ''))[1], ''),
  COUNT(DISTINCT c.trace_id), COUNT(*), COUNT(*) FILTER (WHERE c.observation_type = 'generation'),
  COALESCE(SUM(c.input_tokens), 0), COALESCE(SUM(c.output_tokens), 0), COALESCE(SUM(c.cost_cents), 0),
  COUNT(*) FILTER (WHERE c.status = 'error' OR c.level = 'error'), MIN(c.started_at), MAX(c.ended_at)
%s ORDER BY MAX(c.ended_at) DESC, c.session_id, c.token_id LIMIT %s OFFSET %s`, grouped, limitParam, offsetParam), args...)
	if err != nil {
		return nil, fmt.Errorf("list trace sessions: %w", err)
	}
	defer rows.Close()
	items := make([]service.TraceSession, 0)
	for rows.Next() {
		s, err := scanTraceSession(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read trace sessions: %w", err)
	}
	return &service.ListResult[service.TraceSession]{Data: items, Meta: service.ListMeta{Total: total, Offset: q.Offset, Limit: limit}}, nil
}

func scanTraceSession(scanner interface{ Scan(...any) error }) (service.TraceSession, error) {
	var s service.TraceSession
	var sources textArrayScan
	var started, ended time.Time
	if err := scanner.Scan(&s.SessionID, &s.TokenID, &s.UserID, &s.EndUser, &sources, &s.Name,
		&s.TraceCount, &s.ObservationCount, &s.GenerationCount, &s.InputTokens, &s.OutputTokens, &s.CostCents,
		&s.ErrorCount, &started, &ended); err != nil {
		return s, fmt.Errorf("scan trace session: %w", err)
	}
	s.Sources = append([]string{}, sources...)
	s.StartedAt = started.UTC().Format(time.RFC3339Nano)
	s.EndedAt = ended.UTC().Format(time.RFC3339Nano)
	return s, nil
}

// traceSessionTraceLimit bounds the traces returned for one session replay.
const traceSessionTraceLimit = 500

func (p *Postgres) GetTraceSession(ctx context.Context, sessionID, tokenID string) (*service.TraceSessionDetail, error) {
	if sessionID == "" {
		return nil, nil
	}
	list, err := p.ListTraces(ctx, service.TraceQuery{
		SessionIDs: []string{sessionID},
		TokenIDs:   []string{tokenID},
		Sort:       "started_at",
		Limit:      traceSessionTraceLimit,
	})
	if err != nil {
		return nil, err
	}
	if len(list.Data) == 0 {
		return nil, nil
	}
	traces := list.Data
	// Full previews for the replay (list rows clip them).
	ids := make([]string, len(traces))
	for i, t := range traces {
		ids[i] = t.TraceID
	}
	var args sqlArgs
	where := []string{"trace_id = ANY(" + args.add(textArray(ids)) + ")"}
	if ws, ok := traceScopeWorkspace(ctx); ok {
		where = append(where, "workspace_id = "+args.add(ws))
	}
	rows, err := p.db.QueryContext(ctx, fmt.Sprintf("SELECT trace_id, input, output FROM %s WHERE %s", p.tableLLMTraces.GetTable(), strings.Join(where, " AND ")), args...)
	if err != nil {
		return nil, fmt.Errorf("read session trace attributes: %w", err)
	}
	full := map[string][2]string{}
	for rows.Next() {
		var id, in, out string
		if err := rows.Scan(&id, &in, &out); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan session trace attributes: %w", err)
		}
		full[id] = [2]string{in, out}
	}
	rows.Close()
	session := service.TraceSession{SessionID: sessionID, TokenID: tokenID, Sources: []string{}}
	seenSource := map[string]bool{}
	for i := range traces {
		t := &traces[i]
		if v, ok := full[t.TraceID]; ok {
			t.Input, t.Output = v[0], v[1]
		}
		if session.UserID == "" {
			session.UserID = t.UserID
		}
		if session.EndUser == "" {
			session.EndUser = t.EndUser
		}
		if session.Name == "" {
			session.Name = t.Name
		}
		if !seenSource[t.Source] {
			seenSource[t.Source] = true
			session.Sources = append(session.Sources, t.Source)
		}
		session.TraceCount++
		session.ObservationCount += t.ObservationCount
		session.GenerationCount += t.GenerationCount
		session.InputTokens += t.InputTokens
		session.OutputTokens += t.OutputTokens
		session.CostCents += t.CostCents
		session.ErrorCount += t.ErrorCount
		if session.StartedAt == "" || t.StartedAt < session.StartedAt {
			session.StartedAt = t.StartedAt
		}
		if t.EndedAt > session.EndedAt {
			session.EndedAt = t.EndedAt
		}
	}
	if list.Meta.Total > uint64(len(traces)) {
		session.TraceCount = int64(list.Meta.Total)
	}
	return &service.TraceSessionDetail{Session: session, Traces: traces}, nil
}

// traceFacetLimit bounds each facet list.
const traceFacetLimit = 200

func (p *Postgres) GetTraceFacets(ctx context.Context, from time.Time) (*service.TraceFacets, error) {
	facets := &service.TraceFacets{}
	ws, scoped := traceScopeWorkspace(ctx)
	distinct := func(table, column, extra string, since string) ([]string, error) {
		var args sqlArgs
		where := []string{column + " <> ''"}
		if extra != "" {
			where = append(where, extra)
		}
		if scoped {
			where = append(where, "workspace_id = "+args.add(ws))
		}
		if !from.IsZero() && since != "" {
			where = append(where, since+" >= "+args.add(from))
		}
		rows, err := p.db.QueryContext(ctx, fmt.Sprintf("SELECT DISTINCT %s FROM %s WHERE %s ORDER BY 1 LIMIT %d", column, table, strings.Join(where, " AND "), traceFacetLimit), args...)
		if err != nil {
			return nil, fmt.Errorf("list trace facet %s: %w", column, err)
		}
		defer rows.Close()
		out := make([]string, 0)
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				return nil, fmt.Errorf("scan trace facet %s: %w", column, err)
			}
			out = append(out, v)
		}
		return out, rows.Err()
	}
	calls := p.tableLLMCalls.GetTable()
	traces := p.tableLLMTraces.GetTable()
	var err error
	if facets.Models, err = distinct(calls, "model", "", "started_at"); err != nil {
		return nil, err
	}
	if facets.Sources, err = distinct(calls, "source", "", "started_at"); err != nil {
		return nil, err
	}
	if facets.Environments, err = distinct(calls, "environment", "", "started_at"); err != nil {
		return nil, err
	}
	if facets.Releases, err = distinct(calls, "release", "", "started_at"); err != nil {
		return nil, err
	}
	if facets.UserIDs, err = distinct(calls, "user_id", "", "started_at"); err != nil {
		return nil, err
	}
	if facets.EndUsers, err = distinct(calls, "user_field", "", "started_at"); err != nil {
		return nil, err
	}
	if facets.Names, err = distinct(traces, "name", "", "updated_at"); err != nil {
		return nil, err
	}
	if facets.Tags, err = distinct("(SELECT workspace_id, updated_at, unnest(tags) AS tag FROM "+traces+") tg", "tag", "", "updated_at"); err != nil {
		return nil, err
	}
	if facets.ScoreNames, err = distinct(p.tableTraceScores.GetTable(), "name", "", "created_at"); err != nil {
		return nil, err
	}
	return facets, nil
}

// traceExists reports the workspace holding traceID (and, when given, that
// observationID belongs to it), bounded by the caller's scope.
func (p *Postgres) traceWorkspace(ctx context.Context, traceID, observationID, tokenID, workspaceID string) (string, error) {
	var args sqlArgs
	where := []string{"trace_id = " + args.add(traceID)}
	if workspaceID != "" {
		where = append(where, "workspace_id = "+args.add(workspaceID))
	} else if ws, ok := traceScopeWorkspace(ctx); ok {
		where = append(where, "workspace_id = "+args.add(ws))
	}
	if observationID != "" {
		where = append(where, "id = "+args.add(observationID))
	}
	if tokenID != "" {
		where = append(where, fmt.Sprintf("EXISTS (SELECT 1 FROM %[1]s o WHERE o.workspace_id = %[1]s.workspace_id AND o.trace_id = %[1]s.trace_id AND o.token_id = %[2]s)", p.tableLLMCalls.GetTable(), args.add(tokenID)))
	}
	var ws string
	err := p.db.QueryRowContext(ctx, fmt.Sprintf("SELECT workspace_id FROM %s WHERE %s LIMIT 1", p.tableLLMCalls.GetTable(), strings.Join(where, " AND ")), args...).Scan(&ws)
	if errors.Is(err, sql.ErrNoRows) {
		return "", service.ErrTraceNotFound
	}
	if err != nil {
		return "", fmt.Errorf("resolve trace: %w", err)
	}
	return ws, nil
}

func (p *Postgres) insertTraceScore(ctx context.Context, workspaceID string, s service.TraceScore) (*service.TraceScore, error) {
	s.ID = ulid.Make().String()
	var value any
	if s.Value != nil {
		value = *s.Value
	}
	row := p.db.QueryRowContext(ctx, fmt.Sprintf(`INSERT INTO %s (id, workspace_id, trace_id, observation_id, name, data_type, value, string_value, source, comment, author_user_id, token_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING id, trace_id, observation_id, name, data_type, value, string_value, source, comment, author_user_id, token_id, created_at`, p.tableTraceScores.GetTable()),
		s.ID, workspaceID, s.TraceID, s.ObservationID, s.Name, s.DataType, value, s.StringValue, s.Source, s.Comment, s.AuthorUserID, s.TokenID)
	out, err := scanTraceScore(row)
	if err != nil {
		return nil, fmt.Errorf("create trace score: %w", err)
	}
	return &out, nil
}

func (p *Postgres) CreateTraceScore(ctx context.Context, score service.TraceScore) (*service.TraceScore, error) {
	if err := service.ValidateTraceScore(&score); err != nil {
		return nil, err
	}
	ws, err := p.traceWorkspace(ctx, score.TraceID, score.ObservationID, "", "")
	if err != nil {
		return nil, err
	}
	score.Source = service.ScoreSourceAnnotation
	score.TokenID = ""
	score.AuthorUserID = ""
	if a, ok := service.AccessPrincipalFromContext(ctx); ok {
		score.AuthorUserID = a.UserID
	}
	return p.insertTraceScore(ctx, ws, score)
}

func (p *Postgres) CreateTraceScoreForToken(ctx context.Context, workspaceID, tokenID string, score service.TraceScore) (*service.TraceScore, error) {
	if err := service.ValidateTraceScore(&score); err != nil {
		return nil, err
	}
	if workspaceID == "" || tokenID == "" {
		return nil, service.ErrTraceNotFound
	}
	ws, err := p.traceWorkspace(ctx, score.TraceID, score.ObservationID, tokenID, workspaceID)
	if err != nil {
		return nil, err
	}
	score.Source = service.ScoreSourceAPI
	score.TokenID = tokenID
	score.AuthorUserID = ""
	return p.insertTraceScore(ctx, ws, score)
}

func (p *Postgres) DeleteTraceScore(ctx context.Context, id string) error {
	var args sqlArgs
	where := []string{"id = " + args.add(id)}
	if ws, ok := traceScopeWorkspace(ctx); ok {
		where = append(where, "workspace_id = "+args.add(ws))
	}
	var source, author string
	err := p.db.QueryRowContext(ctx, fmt.Sprintf("SELECT source, author_user_id FROM %s WHERE %s", p.tableTraceScores.GetTable(), strings.Join(where, " AND ")), args...).Scan(&source, &author)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrTraceNotFound
	}
	if err != nil {
		return fmt.Errorf("read trace score: %w", err)
	}
	if a, ok := service.AccessPrincipalFromContext(ctx); ok && !a.PlatformAdmin {
		mayModerate := service.WorkspaceRoleRank(a.Role) >= service.WorkspaceRoleRank("admin")
		if source == service.ScoreSourceAnnotation && author != a.UserID && !mayModerate {
			return service.ErrAccessDenied
		}
		if source == service.ScoreSourceAPI && !mayModerate {
			return service.ErrAccessDenied
		}
	}
	if _, err := p.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE %s", p.tableTraceScores.GetTable(), strings.Join(where, " AND ")), args...); err != nil {
		return fmt.Errorf("delete trace score: %w", err)
	}
	return nil
}

func (p *Postgres) SetTraceBookmark(ctx context.Context, traceID string, bookmarked bool) error {
	a, ok := service.AccessPrincipalFromContext(ctx)
	if !ok || a.UserID == "" {
		return service.ErrAccessDenied
	}
	ws, err := p.traceWorkspace(ctx, traceID, "", "", "")
	if err != nil {
		return err
	}
	tbl := p.tableTraceBookmarks.GetTable()
	if bookmarked {
		_, err = p.db.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (workspace_id, trace_id, user_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING", tbl), ws, traceID, a.UserID)
	} else {
		_, err = p.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE workspace_id = $1 AND trace_id = $2 AND user_id = $3", tbl), ws, traceID, a.UserID)
	}
	if err != nil {
		return fmt.Errorf("set trace bookmark: %w", err)
	}
	return nil
}

// expireTracePreviews blanks trace-level input/output on the same schedule as
// observation bodies: both are prompt content captured under llm_audit.
func (p *Postgres) expireTracePreviews(ctx context.Context, cutoff string) error {
	if _, err := p.db.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET input = '', output = '' WHERE updated_at < $1::timestamptz AND (input <> '' OR output <> '')", p.tableLLMTraces.GetTable()), cutoff); err != nil {
		return fmt.Errorf("expire trace previews: %w", err)
	}
	return nil
}

// deleteOrphanTraceRows removes trace attributes, scores and bookmarks whose
// observations have all been swept, once they are older than the cutoff.
func (p *Postgres) deleteOrphanTraceRows(ctx context.Context, cutoff string) error {
	calls := p.tableLLMCalls.GetTable()
	for _, t := range []struct{ table, column string }{
		{p.tableLLMTraces.GetTable(), "updated_at"},
		{p.tableTraceScores.GetTable(), "created_at"},
		{p.tableTraceBookmarks.GetTable(), "created_at"},
	} {
		query := fmt.Sprintf(`DELETE FROM %[1]s x WHERE x.%[2]s < $1::timestamptz
AND NOT EXISTS (SELECT 1 FROM %[3]s c WHERE c.workspace_id = x.workspace_id AND c.trace_id = x.trace_id)`, t.table, t.column, calls)
		if _, err := p.db.ExecContext(ctx, query, cutoff); err != nil {
			return fmt.Errorf("delete orphan trace rows: %w", err)
		}
	}
	return nil
}
