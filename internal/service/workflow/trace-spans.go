package workflow

import (
	"context"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

// ContextWithWorkflowTrace names the workflow a run belongs to, so its trace
// is labelled with the workflow rather than an anonymous run.
func ContextWithWorkflowTrace(ctx context.Context, workflowID, name string) context.Context {
	return context.WithValue(ctx, workflowTraceKey{}, workflowTraceInfo{id: workflowID, name: name})
}

type workflowTraceKey struct{}

type workflowTraceInfo struct{ id, name string }

// traceSpan is one lazily recorded span. Workflows run constantly (cron,
// webhooks) and most nodes do no traceable work, so a run or node span is
// written only when an observation was recorded beneath it; otherwise it
// would bury the traces that matter under empty ones.
type traceSpan struct {
	parent  *service.TraceParent
	obsType string
	name    string
	started time.Time
	meta    map[string]any
	record  RecordObservationFunc
}

// startWorkflowRunSpan opens the run span. A run started inside another trace
// (a workflow tool called by an agent) nests beneath that caller.
func startWorkflowRunSpan(ctx context.Context, record RecordObservationFunc) (context.Context, *traceSpan) {
	if record == nil {
		return ctx, nil
	}
	info, _ := ctx.Value(workflowTraceKey{}).(workflowTraceInfo)
	name := info.name
	if name == "" {
		name = "workflow"
	}
	traceID, sessionID := ulid.Make().String(), ""
	if parent, ok := service.TraceParentFromContext(ctx); ok {
		traceID, sessionID = parent.TraceID, parent.SessionID
	}
	ctx, tp := service.WithTraceParent(ctx, traceID, ulid.Make().String(), sessionID)
	meta := map[string]any{}
	if info.id != "" {
		meta["workflow_id"] = info.id
	}
	return ctx, &traceSpan{parent: tp, obsType: service.ObservationSpan, name: name, started: time.Now(), meta: meta, record: record}
}

// startNodeSpan opens a span for one node execution beneath the run.
func startNodeSpan(ctx context.Context, record RecordObservationFunc, st *nodeState) (context.Context, *traceSpan) {
	run, ok := service.TraceParentFromContext(ctx)
	if record == nil || !ok {
		return ctx, nil
	}
	ctx, tp := service.WithTraceParent(ctx, run.TraceID, ulid.Make().String(), run.SessionID)
	name := st.noder.Type()
	if label, _ := st.node.Data["label"].(string); strings.TrimSpace(label) != "" {
		name = strings.TrimSpace(label)
	}
	meta := map[string]any{"node_id": st.node.ID, "node_type": st.noder.Type()}
	return ctx, &traceSpan{parent: tp, obsType: service.ObservationSpan, name: name, started: time.Now(), meta: meta, record: record}
}

// finish records the span if anything nested beneath it.
func (s *traceSpan) finish(ctx context.Context, err error) {
	if s == nil || !s.parent.Used() {
		return
	}
	ended := time.Now()
	obs := service.LLMCall{
		ID:              s.parent.ObservationID,
		ObservationType: s.obsType,
		Name:            s.name,
		Source:          "workflow",
		TraceID:         s.parent.TraceID,
		SessionID:       s.parent.SessionID,
		Level:           service.ObservationLevelDefault,
		Metadata:        s.meta,
		LatencyMs:       ended.Sub(s.started).Milliseconds(),
		StartedAt:       s.started.UTC().Format(time.RFC3339Nano),
		EndedAt:         ended.UTC().Format(time.RFC3339Nano),
	}
	if outer := s.parent.Parent(); outer != nil {
		obs.ParentObservationID = outer.ObservationID
	} else {
		obs.Trace = &service.TraceUpdate{Name: s.name}
	}
	if err != nil {
		obs.Status = "error"
		obs.Level = service.ObservationLevelError
		obs.ErrorMessage = err.Error()
	}
	s.record(context.WithoutCancel(ctx), obs)
}
