package sqlite3

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

type llmCallRow struct {
	ID                 string  `db:"id"`
	TraceID            string  `db:"trace_id"`
	SessionID          string  `db:"session_id"`
	Source             string  `db:"source"`
	Endpoint           string  `db:"endpoint"`
	TokenID            string  `db:"token_id"`
	AgentID            string  `db:"agent_id"`
	TaskID             string  `db:"task_id"`
	RunID              string  `db:"run_id"`
	OrganizationID     string  `db:"organization_id"`
	Provider           string  `db:"provider"`
	Model              string  `db:"model"`
	RequestedModel     string  `db:"requested_model"`
	RequestBody        string  `db:"request_body"`
	ResponseBody       string  `db:"response_body"`
	RequestBytes       int64   `db:"request_bytes"`
	ResponseBytes      int64   `db:"response_bytes"`
	RequestTruncated   bool    `db:"request_truncated"`
	ResponseTruncated  bool    `db:"response_truncated"`
	RequestRef         string  `db:"request_ref"`
	ResponseRef        string  `db:"response_ref"`
	Streamed           bool    `db:"streamed"`
	InputTokens        int64   `db:"input_tokens"`
	OutputTokens       int64   `db:"output_tokens"`
	CacheReadTokens    int64   `db:"cache_read_tokens"`
	CacheWriteTokens   int64   `db:"cache_write_tokens"`
	ReasoningTokens    int64   `db:"reasoning_tokens"`
	CostCents          float64 `db:"cost_cents"`
	LatencyMs          int64   `db:"latency_ms"`
	TimeToFirstTokenMs int64   `db:"time_to_first_token_ms"`
	Status             string  `db:"status"`
	ErrorCode          string  `db:"error_code"`
	ErrorMessage       string  `db:"error_message"`
	FinishReason       string  `db:"finish_reason"`
	UserField          string  `db:"user_field"`
	CreatedAt          string  `db:"created_at"`
}

// llmCallListColumns clips the two body columns to a preview so list
// queries stay light; full bodies come from GetLLMCall.
func llmCallListColumns() []interface{} {
	return []interface{}{
		"id", "trace_id", "session_id", "source", "endpoint",
		"token_id", "agent_id", "task_id", "run_id", "organization_id",
		"provider", "model", "requested_model",
		goqu.L(fmt.Sprintf("substr(request_body, 1, %d)", service.LLMCallPreviewBytes)).As("request_body"),
		goqu.L(fmt.Sprintf("substr(response_body, 1, %d)", service.LLMCallPreviewBytes)).As("response_body"),
		"request_bytes", "response_bytes", "request_truncated", "response_truncated",
		"request_ref", "response_ref", "streamed",
		"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "reasoning_tokens",
		"cost_cents", "latency_ms", "time_to_first_token_ms",
		"status", "error_code", "error_message", "finish_reason", "user_field",
		"created_at",
	}
}

var llmCallColumns = []interface{}{
	"id", "trace_id", "session_id", "source", "endpoint",
	"token_id", "agent_id", "task_id", "run_id", "organization_id",
	"provider", "model", "requested_model",
	"request_body", "response_body",
	"request_bytes", "response_bytes", "request_truncated", "response_truncated",
	"request_ref", "response_ref", "streamed",
	"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "reasoning_tokens",
	"cost_cents", "latency_ms", "time_to_first_token_ms",
	"status", "error_code", "error_message", "finish_reason", "user_field",
	"created_at",
}

func scanLLMCallRow(scanner interface{ Scan(dest ...any) error }) (llmCallRow, error) {
	var row llmCallRow
	err := scanner.Scan(
		&row.ID, &row.TraceID, &row.SessionID, &row.Source, &row.Endpoint,
		&row.TokenID, &row.AgentID, &row.TaskID, &row.RunID, &row.OrganizationID,
		&row.Provider, &row.Model, &row.RequestedModel,
		&row.RequestBody, &row.ResponseBody,
		&row.RequestBytes, &row.ResponseBytes, &row.RequestTruncated, &row.ResponseTruncated,
		&row.RequestRef, &row.ResponseRef, &row.Streamed,
		&row.InputTokens, &row.OutputTokens, &row.CacheReadTokens, &row.CacheWriteTokens, &row.ReasoningTokens,
		&row.CostCents, &row.LatencyMs, &row.TimeToFirstTokenMs,
		&row.Status, &row.ErrorCode, &row.ErrorMessage, &row.FinishReason, &row.UserField,
		&row.CreatedAt,
	)

	return row, err
}

func (s *SQLite) RecordLLMCall(ctx context.Context, call service.LLMCall) error {
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

	query, _, err := s.goqu.Insert(s.tableLLMCalls).Rows(
		goqu.Record{
			"id":                     id,
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
			"error_message":          truncateString(call.ErrorMessage, 500),
			"finish_reason":          call.FinishReason,
			"user_field":             call.UserField,
			"created_at":             createdAt,
		},
	).ToSQL()
	if err != nil {
		return fmt.Errorf("build insert llm call query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("record llm call: %w", err)
	}

	return nil
}

func (s *SQLite) ListLLMCalls(ctx context.Context, q *query.Query) (*service.ListResult[service.LLMCall], error) {
	sql, total, err := s.buildListQuery(ctx, s.tableLLMCalls, q, llmCallListColumns()...)
	if err != nil {
		return nil, fmt.Errorf("build list llm calls query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, sql)
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

func (s *SQLite) GetLLMCall(ctx context.Context, id string) (*service.LLMCall, error) {
	query, _, err := s.goqu.From(s.tableLLMCalls).
		Select(llmCallColumns...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get llm call query: %w", err)
	}

	row, err := scanLLMCallRow(s.db.QueryRowContext(ctx, query))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}

		return nil, fmt.Errorf("get llm call %q: %w", id, err)
	}

	record := llmCallRowToRecord(row)

	return &record, nil
}

func (s *SQLite) DeleteLLMCallsBefore(ctx context.Context, cutoff string) (int64, error) {
	query, _, err := s.goqu.Delete(s.tableLLMCalls).
		Where(goqu.I("created_at").Lt(cutoff)).
		ToSQL()
	if err != nil {
		return 0, fmt.Errorf("build delete llm calls query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("delete llm calls before %q: %w", cutoff, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil //nolint:nilerr // count is informational
	}

	return n, nil
}

func llmCallRowToRecord(row llmCallRow) service.LLMCall {
	return service.LLMCall{
		ID:                 row.ID,
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
		CreatedAt:          row.CreatedAt,
	}
}
