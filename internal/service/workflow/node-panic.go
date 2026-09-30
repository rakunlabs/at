package workflow

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/rakunlabs/logi"
)

// ErrNodePanic marks a node implementation that panicked. Workflow runs are
// launched on goroutines outside the HTTP recover middleware (webhooks, cron,
// async runs, fan-out branches), so an unrecovered panic there terminates the
// whole AT process. It is converted into an ordinary node error instead: the
// run fails, or on_error continue/error_output handles it. It is never
// retried, because a panic is a defect rather than a transient condition.
var ErrNodePanic = errors.New("node panicked")

// runNode is the single call site of Noder.Run.
func runNode(ctx context.Context, st *nodeState, reg *Registry, inputs map[string]any) (result NodeResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			logi.Ctx(ctx).Error("workflow node panicked",
				"node_id", st.node.ID,
				"node_type", st.noder.Type(),
				"panic", fmt.Sprint(r),
				"stack", string(debug.Stack()))
			result, err = nil, fmt.Errorf("%w: %v", ErrNodePanic, r)
		}
	}()
	return st.noder.Run(ctx, reg, inputs)
}
