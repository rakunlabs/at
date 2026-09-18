package postgres

import (
	"context"
	"fmt"

	"github.com/doug-martin/goqu/v9"
	"github.com/rakunlabs/query"
	"github.com/rakunlabs/query/adapter/adaptergoqu"

	"github.com/rakunlabs/at/internal/service"
)

func (p *Postgres) ListLLMCallConversations(ctx context.Context, q *query.Query) (*service.ListResult[service.LLMCallConversation], error) {
	// Streaming and synchronous gateway calls can be turns of the same session.
	// Token IDs prevent two clients using e.g. "session-1" from being conflated.
	family := goqu.L("CASE WHEN source IN ('gateway', 'gateway_stream', 'responses') THEN 'gateway' ELSE source END")
	key := goqu.L("CASE WHEN session_id <> '' THEN 'session:' || session_id ELSE 'trace:' || trace_id END")
	ds := p.goqu.From(p.tableLLMCalls).Where(goqu.I("trace_id").Neq(""))
	if ws, ok := traceWorkspaceScope(ctx); ok {
		ds = ds.Where(goqu.C("workspace_id").Eq(ws))
	}
	if q != nil {
		ds = ds.Where(adaptergoqu.Expression(q)...)
	}
	grouped := ds.GroupBy(key, goqu.I("token_id"), family)
	countSQL, _, err := p.goqu.From(grouped.Select(goqu.L("1")).As("conversations")).Select(goqu.COUNT("*")).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build conversation count: %w", err)
	}
	var total uint64
	if err := p.db.QueryRowContext(ctx, countSQL).Scan(&total); err != nil {
		return nil, fmt.Errorf("count conversations: %w", err)
	}
	offset, limit := getPagination(q)
	if limit == 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	dataSQL, _, err := grouped.Select(
		goqu.MIN("trace_id"), goqu.MAX("session_id"), family,
		goqu.L("COALESCE(MIN(NULLIF(name, '')), '')"), goqu.I("token_id"), goqu.L("COUNT(DISTINCT trace_id)"),
		goqu.COUNT("*"), goqu.L("SUM(CASE WHEN observation_type = 'generation' THEN 1 ELSE 0 END)"),
		goqu.SUM("input_tokens"), goqu.SUM("output_tokens"), goqu.SUM("cache_read_tokens"), goqu.SUM("cache_write_tokens"),
		goqu.SUM("cost_cents"), goqu.SUM("latency_ms"), goqu.L("SUM(CASE WHEN status = 'error' OR level = 'error' THEN 1 ELSE 0 END)"),
		goqu.MIN("created_at"), goqu.MAX("created_at"),
	).Order(goqu.MAX("created_at").Desc(), key.Asc(), goqu.I("token_id").Asc(), family.Asc()).Limit(uint(limit)).Offset(uint(offset)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build conversations query: %w", err)
	}
	rows, err := p.db.QueryContext(ctx, dataSQL)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()
	items := make([]service.LLMCallConversation, 0)
	for rows.Next() {
		var c service.LLMCallConversation
		if err := rows.Scan(&c.TraceID, &c.SessionID, &c.Source, &c.Name, &c.TokenID, &c.TraceCount, &c.ObservationCount, &c.GenerationCount,
			&c.InputTokens, &c.OutputTokens, &c.CacheReadTokens, &c.CacheWriteTokens, &c.CostCents, &c.LatencyMsTotal, &c.ErrorCount, &c.StartedAt, &c.EndedAt); err != nil {
			return nil, fmt.Errorf("scan conversation: %w", err)
		}
		items = append(items, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read conversations: %w", err)
	}
	return &service.ListResult[service.LLMCallConversation]{Data: items, Meta: service.ListMeta{Total: total, Offset: offset, Limit: limit}}, nil
}
