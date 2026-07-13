package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

var llmTraceFilterFields = []string{
	"trace_id",
	"session_id",
	"task_id",
	"agent_id",
	"organization_id",
	"run_id",
	"source",
	"status",
	"observation_type",
	"level",
}

func llmTraceQuery(args map[string]any, defaultLimit, maxLimit uint64) (*query.Query, error) {
	values := url.Values{}
	for _, field := range llmTraceFilterFields {
		if value, _ := args[field].(string); value != "" {
			values.Set(field, value)
		}
	}

	q, err := query.Parse(values.Encode())
	if err != nil {
		return nil, fmt.Errorf("build trace query: %w", err)
	}

	limit, err := llmTraceUintArg(args, "limit", defaultLimit)
	if err != nil {
		return nil, err
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	offset, err := llmTraceUintArg(args, "offset", 0)
	if err != nil {
		return nil, err
	}
	q.SetLimit(limit).SetOffset(offset)

	return q, nil
}

func llmTraceUintArg(args map[string]any, name string, fallback uint64) (uint64, error) {
	value, ok := args[name].(float64)
	if !ok {
		return fallback, nil
	}
	if value < 0 {
		return 0, fmt.Errorf("%s must not be negative", name)
	}
	if value == 0 {
		return fallback, nil
	}
	return uint64(value), nil
}

func (s *Server) execLLMTraceList(ctx context.Context, args map[string]any) (string, error) {
	if s.llmCallStore == nil {
		return "", fmt.Errorf("LLM call store not configured")
	}

	q, err := llmTraceQuery(args, 20, 100)
	if err != nil {
		return "", err
	}
	records, err := s.llmCallStore.ListLLMCallTraces(ctx, q)
	if err != nil {
		return "", fmt.Errorf("list LLM traces: %w", err)
	}
	if records == nil {
		records = &service.ListResult[service.LLMCallTrace]{Data: []service.LLMCallTrace{}}
	}
	if records.Data == nil {
		records.Data = []service.LLMCallTrace{}
	}

	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return "", fmt.Errorf("serialize LLM traces: %w", err)
	}
	return string(data), nil
}

func (s *Server) execLLMTraceGet(ctx context.Context, args map[string]any) (string, error) {
	if s.llmCallStore == nil {
		return "", fmt.Errorf("LLM call store not configured")
	}

	traceID, _ := args["trace_id"].(string)
	if traceID == "" {
		return "", fmt.Errorf("trace_id is required")
	}

	queryArgs := make(map[string]any, len(args)+1)
	for key, value := range args {
		queryArgs[key] = value
	}
	queryArgs["trace_id"] = traceID

	q, err := llmTraceQuery(queryArgs, 100, 500)
	if err != nil {
		return "", err
	}
	q.Sort = []query.ExpressionSort{{Field: "created_at"}}
	records, err := s.llmCallStore.ListLLMCalls(ctx, q)
	if err != nil {
		return "", fmt.Errorf("list trace observations: %w", err)
	}
	if records == nil {
		records = &service.ListResult[service.LLMCall]{Data: []service.LLMCall{}}
	}
	if records.Data == nil {
		records.Data = []service.LLMCall{}
	}

	includeBodies := boolValue(args["include_bodies"])
	if !includeBodies {
		for i := range records.Data {
			records.Data[i].Input = ""
			records.Data[i].Output = ""
			records.Data[i].RequestBody = ""
			records.Data[i].ResponseBody = ""
			records.Data[i].RequestRef = ""
			records.Data[i].ResponseRef = ""
		}
	}

	traceQuery, err := llmTraceQuery(map[string]any{"trace_id": traceID, "limit": float64(1)}, 1, 1)
	if err != nil {
		return "", err
	}
	traces, err := s.llmCallStore.ListLLMCallTraces(ctx, traceQuery)
	if err != nil {
		return "", fmt.Errorf("get LLM trace summary: %w", err)
	}
	var trace *service.LLMCallTrace
	if traces != nil && len(traces.Data) > 0 {
		trace = &traces.Data[0]
	}

	result := map[string]any{
		"trace_id":       traceID,
		"trace":          trace,
		"observations":   records.Data,
		"meta":           records.Meta,
		"include_bodies": includeBodies,
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("serialize LLM trace: %w", err)
	}
	return string(data), nil
}

func (s *Server) execLLMObservationGet(ctx context.Context, args map[string]any) (string, error) {
	if s.llmCallStore == nil {
		return "", fmt.Errorf("LLM call store not configured")
	}

	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	call, err := s.llmCallStore.GetLLMCall(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get LLM observation: %w", err)
	}
	if call == nil {
		return "", fmt.Errorf("LLM observation %q not found", id)
	}
	rehydrateLLMCall(call)

	data, err := json.MarshalIndent(call, "", "  ")
	if err != nil {
		return "", fmt.Errorf("serialize LLM observation: %w", err)
	}
	return string(data), nil
}
