package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
	"github.com/rakunlabs/query/adapter/adaptergoqu"
)

type llmCallRow struct {
	ID                  string `db:"id"`
	ObservationType     string `db:"observation_type"`
	ParentObservationID string `db:"parent_observation_id"`
	Name                string `db:"name"`
	Input               string `db:"input"`
	Output              string `db:"output"`
	Level               string `db:"level"`
	Metadata            string `db:"metadata"`

	TraceID            string    `db:"trace_id"`
	SessionID          string    `db:"session_id"`
	Source             string    `db:"source"`
	Endpoint           string    `db:"endpoint"`
	TokenID            string    `db:"token_id"`
	AgentID            string    `db:"agent_id"`
	TaskID             string    `db:"task_id"`
	RunID              string    `db:"run_id"`
	OrganizationID     string    `db:"organization_id"`
	Provider           string    `db:"provider"`
	Model              string    `db:"model"`
	RequestedModel     string    `db:"requested_model"`
	RequestBody        string    `db:"request_body"`
	ResponseBody       string    `db:"response_body"`
	RequestBytes       int64     `db:"request_bytes"`
	ResponseBytes      int64     `db:"response_bytes"`
	RequestTruncated   bool      `db:"request_truncated"`
	ResponseTruncated  bool      `db:"response_truncated"`
	RequestRef         string    `db:"request_ref"`
	ResponseRef        string    `db:"response_ref"`
	Streamed           bool      `db:"streamed"`
	InputTokens        int64     `db:"input_tokens"`
	OutputTokens       int64     `db:"output_tokens"`
	CacheReadTokens    int64     `db:"cache_read_tokens"`
	CacheWriteTokens   int64     `db:"cache_write_tokens"`
	ReasoningTokens    int64     `db:"reasoning_tokens"`
	CostCents          float64   `db:"cost_cents"`
	LatencyMs          int64     `db:"latency_ms"`
	TimeToFirstTokenMs int64     `db:"time_to_first_token_ms"`
	Status             string    `db:"status"`
	ErrorCode          string    `db:"error_code"`
	ErrorMessage       string    `db:"error_message"`
	FinishReason       string    `db:"finish_reason"`
	UserField          string    `db:"user_field"`
	UserID             string    `db:"user_id"`
	Environment        string    `db:"environment"`
	Release            string    `db:"release"`
	StartedAt          time.Time `db:"started_at"`
	EndedAt            time.Time `db:"ended_at"`
	CreatedAt          string    `db:"created_at"`
}

// llmCallListColumns clips the body and IO columns to a preview so list
// queries stay light; full payloads come from GetLLMCall.
func llmCallListColumns() []interface{} {
	return []interface{}{
		"id", "observation_type", "parent_observation_id", "name",
		goqu.L(fmt.Sprintf("substr(input, 1, %d)", service.LLMCallPreviewBytes)).As("input"),
		goqu.L(fmt.Sprintf("substr(output, 1, %d)", service.LLMCallPreviewBytes)).As("output"),
		"level", llmCallMetadataColumn(),
		"trace_id", "session_id", "source", "endpoint",
		"token_id", "agent_id", "task_id", "run_id", "organization_id",
		"provider", "model", "requested_model",
		goqu.L(fmt.Sprintf("substr(request_body, 1, %d)", service.LLMCallPreviewBytes)).As("request_body"),
		goqu.L(fmt.Sprintf("substr(response_body, 1, %d)", service.LLMCallPreviewBytes)).As("response_body"),
		"request_bytes", "response_bytes", "request_truncated", "response_truncated",
		"request_ref", "response_ref", "streamed",
		"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "reasoning_tokens",
		"cost_cents", "latency_ms", "time_to_first_token_ms",
		"status", "error_code", "error_message", "finish_reason", "user_field",
		"user_id", "environment", "release", "started_at", "ended_at",
		"created_at",
	}
}

// llmCallMetadataColumn reads the JSONB metadata column as text so a NULL
// (no metadata) scans into an empty string.
func llmCallMetadataColumn() interface{} {
	return goqu.L("COALESCE(metadata::text, '')").As("metadata")
}

var llmCallColumns = []interface{}{
	"id", "observation_type", "parent_observation_id", "name",
	"input", "output", "level", llmCallMetadataColumn(),
	"trace_id", "session_id", "source", "endpoint",
	"token_id", "agent_id", "task_id", "run_id", "organization_id",
	"provider", "model", "requested_model",
	"request_body", "response_body",
	"request_bytes", "response_bytes", "request_truncated", "response_truncated",
	"request_ref", "response_ref", "streamed",
	"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "reasoning_tokens",
	"cost_cents", "latency_ms", "time_to_first_token_ms",
	"status", "error_code", "error_message", "finish_reason", "user_field",
	"user_id", "environment", "release", "started_at", "ended_at",
	"created_at",
}

func scanLLMCallRow(scanner interface{ Scan(dest ...any) error }) (llmCallRow, error) {
	var row llmCallRow
	err := scanner.Scan(
		&row.ID, &row.ObservationType, &row.ParentObservationID, &row.Name,
		&row.Input, &row.Output, &row.Level, &row.Metadata,
		&row.TraceID, &row.SessionID, &row.Source, &row.Endpoint,
		&row.TokenID, &row.AgentID, &row.TaskID, &row.RunID, &row.OrganizationID,
		&row.Provider, &row.Model, &row.RequestedModel,
		&row.RequestBody, &row.ResponseBody,
		&row.RequestBytes, &row.ResponseBytes, &row.RequestTruncated, &row.ResponseTruncated,
		&row.RequestRef, &row.ResponseRef, &row.Streamed,
		&row.InputTokens, &row.OutputTokens, &row.CacheReadTokens, &row.CacheWriteTokens, &row.ReasoningTokens,
		&row.CostCents, &row.LatencyMs, &row.TimeToFirstTokenMs,
		&row.Status, &row.ErrorCode, &row.ErrorMessage, &row.FinishReason, &row.UserField,
		&row.UserID, &row.Environment, &row.Release, &row.StartedAt, &row.EndedAt,
		&row.CreatedAt,
	)

	return row, err
}

func (p *Postgres) RecordLLMCall(ctx context.Context, call service.LLMCall) error {
	id := call.ID
	if id == "" {
		id = ulid.Make().String()
	}

	status := call.Status
	if status == "" {
		status = "ok"
	}

	createdAt := call.CreatedAt
	if createdAt == "" {
		createdAt = time.Now().UTC().Format(time.RFC3339)
	}

	obsType := call.ObservationType
	if obsType == "" {
		obsType = service.ObservationGeneration
	}

	level := call.Level
	if level == "" {
		level = service.ObservationLevelDefault
	}

	metadata := any(nil)
	if len(call.Metadata) > 0 {
		if b, err := json.Marshal(call.Metadata); err == nil {
			metadata = string(b)
		}
	}
	endedAt, startedAt := observationTimes(call)
	workspaceID := llmCallWriteWorkspace(ctx, call)

	query, _, err := p.goqu.Insert(p.tableLLMCalls).Rows(
		goqu.Record{
			"id":                     id,
			"workspace_id":           workspaceID,
			"observation_type":       obsType,
			"parent_observation_id":  call.ParentObservationID,
			"name":                   call.Name,
			"input":                  call.Input,
			"output":                 call.Output,
			"level":                  level,
			"metadata":               metadata,
			"trace_id":               call.TraceID,
			"session_id":             call.SessionID,
			"source":                 call.Source,
			"endpoint":               call.Endpoint,
			"token_id":               call.TokenID,
			"agent_id":               call.AgentID,
			"task_id":                call.TaskID,
			"run_id":                 call.RunID,
			"organization_id":        call.OrganizationID,
			"provider":               call.Provider,
			"model":                  call.Model,
			"requested_model":        call.RequestedModel,
			"request_body":           call.RequestBody,
			"response_body":          call.ResponseBody,
			"request_bytes":          call.RequestBytes,
			"response_bytes":         call.ResponseBytes,
			"request_truncated":      call.RequestTruncated,
			"response_truncated":     call.ResponseTruncated,
			"request_ref":            call.RequestRef,
			"response_ref":           call.ResponseRef,
			"streamed":               call.Streamed,
			"input_tokens":           call.InputTokens,
			"output_tokens":          call.OutputTokens,
			"cache_read_tokens":      call.CacheReadTokens,
			"cache_write_tokens":     call.CacheWriteTokens,
			"reasoning_tokens":       call.ReasoningTokens,
			"cost_cents":             call.CostCents,
			"latency_ms":             call.LatencyMs,
			"time_to_first_token_ms": call.TimeToFirstTokenMs,
			"status":                 status,
			"error_code":             call.ErrorCode,
			"error_message":          truncatePGString(call.ErrorMessage, 500),
			"finish_reason":          call.FinishReason,
			"user_field":             call.UserField,
			"user_id":                call.UserID,
			"environment":            truncatePGString(call.Environment, 128),
			"release":                truncatePGString(call.Release, 128),
			"started_at":             startedAt,
			"ended_at":               endedAt,
			"created_at":             createdAt,
		},
	).ToSQL()
	if err != nil {
		return fmt.Errorf("build insert llm call query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("record llm call: %w", err)
	}

	if call.TraceID != "" && !call.Trace.Empty() {
		if err := p.upsertLLMTrace(ctx, workspaceID, call.TraceID, call.Trace); err != nil {
			return err
		}
	}

	return nil
}

// observationTimes resolves the stored end and start of an observation. The
// end defaults to now; a missing or unparsable start is derived from latency,
// which is what readers computed before explicit start times existed.
func observationTimes(call service.LLMCall) (time.Time, time.Time) {
	ended := time.Now().UTC()
	if t, err := time.Parse(time.RFC3339Nano, call.EndedAt); err == nil {
		ended = t.UTC()
	}
	started := ended.Add(-time.Duration(max(call.LatencyMs, 0)) * time.Millisecond)
	if t, err := time.Parse(time.RFC3339Nano, call.StartedAt); err == nil && !t.After(ended) {
		started = t.UTC()
	}
	return ended, started
}

// upsertLLMTrace merges trace-level attributes: the first non-empty name,
// end user and input win (the trace's opening request), the last non-empty
// output wins (its final answer) and tags accumulate without duplicates.
func (p *Postgres) upsertLLMTrace(ctx context.Context, workspaceID, traceID string, u *service.TraceUpdate) error {
	tbl := p.tableLLMTraces.GetTable()
	tags := normalizeTraceTags(u.Tags)
	query := fmt.Sprintf(`INSERT INTO %[1]s AS t (workspace_id, trace_id, name, end_user, tags, input, output)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (workspace_id, trace_id) DO UPDATE SET
  name = CASE WHEN t.name = '' THEN EXCLUDED.name ELSE t.name END,
  end_user = CASE WHEN t.end_user = '' THEN EXCLUDED.end_user ELSE t.end_user END,
  tags = ARRAY(SELECT DISTINCT unnest(t.tags || EXCLUDED.tags) ORDER BY 1),
  input = CASE WHEN t.input = '' THEN EXCLUDED.input ELSE t.input END,
  output = CASE WHEN EXCLUDED.output <> '' THEN EXCLUDED.output ELSE t.output END,
  updated_at = clock_timestamp()`, tbl)
	if _, err := p.db.ExecContext(ctx, query, workspaceID, traceID,
		truncatePGString(u.Name, 256), truncatePGString(u.EndUser, 256), tags,
		clipTracePreview(u.Input), clipTracePreview(u.Output)); err != nil {
		return fmt.Errorf("upsert llm trace: %w", err)
	}
	return nil
}

// normalizeTraceTags trims, bounds and de-duplicates tags. The value is
// client-influenced (gateway headers), so both count and length are capped.
func normalizeTraceTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || len(tag) > 64 || seen[tag] || strings.ContainsFunc(tag, unicode.IsControl) {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
		if len(out) == 20 {
			break
		}
	}
	return out
}

func clipTracePreview(s string) string {
	if len(s) <= service.TracePreviewBytes {
		return s
	}
	cut := service.TracePreviewBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func llmCallWriteWorkspace(ctx context.Context, call service.LLMCall) string {
	if p, _, ok := service.ExecutionFromContext(ctx); ok {
		return p.WorkspaceID
	}
	if p, ok := service.AccessPrincipalFromContext(ctx); ok {
		return p.WorkspaceID
	}
	if call.WorkspaceID != "" {
		return call.WorkspaceID
	}
	return service.DefaultWorkspaceID
}

func (p *Postgres) ListLLMCalls(ctx context.Context, q *query.Query) (*service.ListResult[service.LLMCall], error) {
	sql, total, err := p.buildListQuery(ctx, p.tableLLMCalls, q, llmCallListColumns()...)
	if err != nil {
		return nil, fmt.Errorf("build list llm calls query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list llm calls: %w", err)
	}
	defer rows.Close()

	var items []service.LLMCall
	for rows.Next() {
		row, err := scanLLMCallRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan llm call row: %w", err)
		}

		items = append(items, llmCallRowToRecord(row))
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.LLMCall]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

// traceWorkspaceScope bounds a trace view to the caller's workspace.
// ListLLMCalls already derives this from businessReadScope; the detail and
// aggregate queries repeat it so one page cannot mix scopes. A caller without
// a scoped principal (the legacy installation view) is unbounded, which is
// what those views have always been.
func traceWorkspaceScope(ctx context.Context) (string, bool) {
	if a, ok := service.AccessPrincipalFromContext(ctx); ok && a.WorkspaceID != "" {
		return a.WorkspaceID, true
	}
	return "", false
}

func (p *Postgres) GetLLMCall(ctx context.Context, id string) (*service.LLMCall, error) {
	ds := p.goqu.From(p.tableLLMCalls).
		Select(llmCallColumns...).
		Where(goqu.I("id").Eq(id))
	if ws, ok := traceWorkspaceScope(ctx); ok {
		ds = ds.Where(goqu.C("workspace_id").Eq(ws))
	}
	query, _, err := ds.ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get llm call query: %w", err)
	}

	row, err := scanLLMCallRow(p.db.QueryRowContext(ctx, query))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}

		return nil, fmt.Errorf("get llm call %q: %w", id, err)
	}

	record := llmCallRowToRecord(row)

	return &record, nil
}

// ListLLMCallTraces aggregates observations into one row per trace_id,
// newest-first. Filters from q apply to the underlying observation rows
// (source, session_id, task_id, agent_id, organization_id, status,
// created_at ranges); pagination applies to the grouped result.
func (p *Postgres) ListLLMCallTraces(ctx context.Context, q *query.Query) (*service.ListResult[service.LLMCallTrace], error) {
	tbl := p.tableLLMCalls.GetTable()

	ds := p.goqu.From(p.tableLLMCalls).Where(goqu.I("trace_id").Neq(""))
	if ws, ok := traceWorkspaceScope(ctx); ok {
		ds = ds.Where(goqu.C("workspace_id").Eq(ws))
	}
	if q != nil {
		if exprs := adaptergoqu.Expression(q); len(exprs) > 0 {
			ds = ds.Where(exprs...)
		}
	}

	countSQL, _, err := ds.Select(goqu.L("COUNT(DISTINCT trace_id)")).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build count llm call traces query: %w", err)
	}

	var total uint64
	if err := p.db.QueryRowContext(ctx, countSQL).Scan(&total); err != nil {
		return nil, fmt.Errorf("count llm call traces: %w", err)
	}

	offset, limit := getPagination(q)
	if limit == 0 {
		limit = 50
	}

	dataDs := ds.Select(
		goqu.I("trace_id"),
		goqu.MAX("session_id").As("session_id"),
		goqu.MAX("source").As("source"),
		goqu.L("(SELECT c2.name FROM "+tbl+" c2 WHERE c2.trace_id = "+tbl+".trace_id AND c2.name != '' ORDER BY c2.id ASC LIMIT 1)").As("name"),
		goqu.MAX("task_id").As("task_id"),
		goqu.MAX("agent_id").As("agent_id"),
		goqu.MAX("organization_id").As("organization_id"),
		goqu.COUNT("*").As("observation_count"),
		goqu.L("SUM(CASE WHEN observation_type = 'generation' THEN 1 ELSE 0 END)").As("generation_count"),
		goqu.L("COALESCE(SUM(input_tokens), 0)").As("input_tokens"),
		goqu.L("COALESCE(SUM(output_tokens), 0)").As("output_tokens"),
		goqu.L("COALESCE(SUM(cache_read_tokens), 0)").As("cache_read_tokens"),
		goqu.L("COALESCE(SUM(cache_write_tokens), 0)").As("cache_write_tokens"),
		goqu.L("COALESCE(SUM(cost_cents), 0)").As("cost_cents"),
		goqu.L("COALESCE(SUM(latency_ms), 0)").As("latency_ms_total"),
		goqu.L("SUM(CASE WHEN status = 'error' OR level = 'error' THEN 1 ELSE 0 END)").As("error_count"),
		goqu.MIN("created_at").As("started_at"),
		goqu.MAX("created_at").As("ended_at"),
	).
		GroupBy(goqu.I("trace_id")).
		Order(goqu.L("MIN(created_at)").Desc()).
		Limit(uint(limit)).
		Offset(uint(offset))

	dataSQL, _, err := dataDs.ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list llm call traces query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, dataSQL)
	if err != nil {
		return nil, fmt.Errorf("list llm call traces: %w", err)
	}
	defer rows.Close()

	var items []service.LLMCallTrace
	for rows.Next() {
		var t service.LLMCallTrace
		var name sql.NullString
		if err := rows.Scan(
			&t.TraceID, &t.SessionID, &t.Source, &name,
			&t.TaskID, &t.AgentID, &t.OrganizationID,
			&t.ObservationCount, &t.GenerationCount,
			&t.InputTokens, &t.OutputTokens, &t.CacheReadTokens, &t.CacheWriteTokens,
			&t.CostCents, &t.LatencyMsTotal,
			&t.ErrorCount, &t.StartedAt, &t.EndedAt,
		); err != nil {
			return nil, fmt.Errorf("scan llm call trace row: %w", err)
		}
		t.Name = name.String
		items = append(items, t)
	}

	return &service.ListResult[service.LLMCallTrace]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

// ExpireLLMCallBodiesBefore blanks the heavy body columns and the spill
// references (which held full tool IO) on rows older than cutoff, keeping the
// skeleton, including the bounded tool IO previews. Trace-level input/output
// previews are captured only with body capture on, so they expire here too.
func (p *Postgres) ExpireLLMCallBodiesBefore(ctx context.Context, cutoff string) (int64, error) {
	query, _, err := p.goqu.Update(p.tableLLMCalls).
		Set(goqu.Record{
			"request_body":       "",
			"response_body":      "",
			"request_truncated":  false,
			"response_truncated": false,
			"request_ref":        "",
			"response_ref":       "",
		}).
		Where(
			goqu.I("created_at").Lt(cutoff),
			goqu.L("(request_body != '' OR response_body != '' OR request_ref != '' OR response_ref != '')"),
		).
		ToSQL()
	if err != nil {
		return 0, fmt.Errorf("build expire llm call bodies query: %w", err)
	}

	res, err := p.db.ExecContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("expire llm call bodies before %q: %w", cutoff, err)
	}
	if err := p.expireTracePreviews(ctx, cutoff); err != nil {
		return 0, err
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil //nolint:nilerr // count is informational
	}

	return n, nil
}

func (p *Postgres) DeleteLLMCallsBefore(ctx context.Context, cutoff string) (int64, error) {
	query, _, err := p.goqu.Delete(p.tableLLMCalls).
		Where(goqu.I("created_at").Lt(cutoff)).
		ToSQL()
	if err != nil {
		return 0, fmt.Errorf("build delete llm calls query: %w", err)
	}

	res, err := p.db.ExecContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("delete llm calls before %q: %w", cutoff, err)
	}
	if err := p.deleteOrphanTraceRows(ctx, cutoff); err != nil {
		return 0, err
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil //nolint:nilerr // count is informational
	}

	return n, nil
}

func llmCallRowToRecord(row llmCallRow) service.LLMCall {
	var metadata map[string]any
	if row.Metadata != "" {
		_ = json.Unmarshal([]byte(row.Metadata), &metadata)
	}

	return service.LLMCall{
		ID:                  row.ID,
		ObservationType:     row.ObservationType,
		ParentObservationID: row.ParentObservationID,
		Name:                row.Name,
		Input:               row.Input,
		Output:              row.Output,
		Level:               row.Level,
		Metadata:            metadata,

		TraceID:            row.TraceID,
		SessionID:          row.SessionID,
		Source:             row.Source,
		Endpoint:           row.Endpoint,
		TokenID:            row.TokenID,
		AgentID:            row.AgentID,
		TaskID:             row.TaskID,
		RunID:              row.RunID,
		OrganizationID:     row.OrganizationID,
		Provider:           row.Provider,
		Model:              row.Model,
		RequestedModel:     row.RequestedModel,
		RequestBody:        row.RequestBody,
		ResponseBody:       row.ResponseBody,
		RequestBytes:       row.RequestBytes,
		ResponseBytes:      row.ResponseBytes,
		RequestTruncated:   row.RequestTruncated,
		ResponseTruncated:  row.ResponseTruncated,
		RequestRef:         row.RequestRef,
		ResponseRef:        row.ResponseRef,
		Streamed:           row.Streamed,
		InputTokens:        row.InputTokens,
		OutputTokens:       row.OutputTokens,
		CacheReadTokens:    row.CacheReadTokens,
		CacheWriteTokens:   row.CacheWriteTokens,
		ReasoningTokens:    row.ReasoningTokens,
		CostCents:          row.CostCents,
		LatencyMs:          row.LatencyMs,
		TimeToFirstTokenMs: row.TimeToFirstTokenMs,
		Status:             row.Status,
		ErrorCode:          row.ErrorCode,
		ErrorMessage:       row.ErrorMessage,
		FinishReason:       row.FinishReason,
		UserField:          row.UserField,
		UserID:             row.UserID,
		Environment:        row.Environment,
		Release:            row.Release,
		StartedAt:          row.StartedAt.UTC().Format(time.RFC3339Nano),
		EndedAt:            row.EndedAt.UTC().Format(time.RFC3339Nano),
		CreatedAt:          row.CreatedAt,
	}
}
