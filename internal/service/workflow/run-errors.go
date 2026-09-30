package workflow

import (
	"errors"
	"fmt"

	"github.com/rakunlabs/at/internal/service"
)

// NodeError identifies the node that ended a run. Its message is exactly the
// historical "node ... : cause" text, so logs and API errors are unchanged;
// the structured fields are what run history records.
type NodeError struct {
	NodeID   string
	NodeType string
	msg      string
	cause    error
}

func (e *NodeError) Error() string { return e.msg }
func (e *NodeError) Unwrap() error { return e.cause }

func newNodeError(st *nodeState, err error) error {
	return &NodeError{NodeID: st.node.ID, NodeType: st.noder.Type(), msg: fmt.Sprintf("%s: %v", nodeRef(st), err), cause: err}
}

// FailedNode reports the node that ended a run, if the error names one.
func FailedNode(err error) (nodeID, nodeType string) {
	var nodeErr *NodeError
	if errors.As(err, &nodeErr) {
		return nodeErr.NodeID, nodeErr.NodeType
	}
	return "", ""
}

// maxHandledErrors bounds what one run collects: a Loop over thousands of
// items with on_error=continue must not grow the registry without limit.
const maxHandledErrors = 20

// recordHandledError keeps a failure the run survived via on_error.
func (r *Registry) recordHandledError(st *nodeState, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handledCount++
	if len(r.handled) < maxHandledErrors {
		r.handled = append(r.handled, service.WorkflowRunHandledError{NodeID: st.node.ID, NodeType: st.noder.Type(), Error: err.Error()})
	}
}

// HandledErrors returns the recorded handled failures and the total count,
// which can exceed the recorded list.
func (r *Registry) HandledErrors() ([]service.WorkflowRunHandledError, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]service.WorkflowRunHandledError(nil), r.handled...), r.handledCount
}

// mergeHandled folds a fan-out branch's handled failures into its parent.
func (r *Registry) mergeHandled(branch *Registry) {
	items, count := branch.HandledErrors()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handledCount += count
	for _, item := range items {
		if len(r.handled) >= maxHandledErrors {
			break
		}
		r.handled = append(r.handled, item)
	}
}
