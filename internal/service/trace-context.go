package service

import (
	"context"
	"sync/atomic"
)

// TraceParent is the span that observations recorded under a context nest
// into. Agent loops and workflow runs place one on the context they hand to
// their children, so a workflow started by a tool, an agent_call node or a
// subagent joins the surrounding trace instead of starting a disconnected one.
//
// A parent may be recorded lazily: a workflow node span is only worth a row
// when something was recorded beneath it, so Used reports whether any
// descendant observation claimed it.
type TraceParent struct {
	TraceID       string
	ObservationID string
	SessionID     string

	parent *TraceParent
	used   atomic.Bool
}

type traceParentKey struct{}

// WithTraceParent nests a new span under any parent already on ctx.
func WithTraceParent(ctx context.Context, traceID, observationID, sessionID string) (context.Context, *TraceParent) {
	tp := &TraceParent{TraceID: traceID, ObservationID: observationID, SessionID: sessionID}
	if parent, ok := TraceParentFromContext(ctx); ok && parent.TraceID == traceID {
		tp.parent = parent
	}
	return context.WithValue(ctx, traceParentKey{}, tp), tp
}

// TraceParentFromContext returns the innermost trace parent on ctx.
func TraceParentFromContext(ctx context.Context) (*TraceParent, bool) {
	if ctx == nil {
		return nil, false
	}
	tp, ok := ctx.Value(traceParentKey{}).(*TraceParent)
	return tp, ok && tp != nil && tp.TraceID != ""
}

// MarkUsed records that an observation nests beneath this span, and therefore
// beneath every enclosing span of the same trace.
func (tp *TraceParent) MarkUsed() {
	for p := tp; p != nil; p = p.parent {
		if p.used.Swap(true) {
			return
		}
	}
}

// Used reports whether any observation nested beneath this span.
func (tp *TraceParent) Used() bool { return tp != nil && tp.used.Load() }

// Parent returns the enclosing span of the same trace, if any.
func (tp *TraceParent) Parent() *TraceParent {
	if tp == nil {
		return nil
	}
	return tp.parent
}
