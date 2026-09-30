package service

import (
	"context"
	"errors"
	"time"
)

// ErrInvalidWorkflowRunQuery reports an unsupported run-history filter.
var ErrInvalidWorkflowRunQuery = errors.New("invalid workflow run query")

// Workflow run history covers non-durable runs (cron, webhook, API, the
// workflow_run tool), which execute on in-memory goroutines. Durable runs keep
// their own record in WorkflowExecution.
const (
	WorkflowRunRunning     = "running"
	WorkflowRunCompleted   = "completed"
	WorkflowRunFailed      = "failed"
	WorkflowRunCancelled   = "cancelled"
	WorkflowRunInterrupted = "interrupted" // owning process stopped heartbeating
)

// WorkflowRunHandledError is a node failure the run survived (on_error
// continue/error_output). It is recorded because a run that "completed" while
// skipping a failed step is exactly what nobody notices otherwise.
type WorkflowRunHandledError struct {
	NodeID   string `json:"node_id"`
	NodeType string `json:"node_type"`
	Error    string `json:"error"`
}

type WorkflowRun struct {
	ID             string                    `json:"id"`
	WorkspaceID    string                    `json:"workspace_id"`
	WorkflowID     string                    `json:"workflow_id"`
	OwnerUserID    string                    `json:"owner_user_id"`
	Source         string                    `json:"source"`
	TriggerID      string                    `json:"trigger_id"`
	Status         string                    `json:"status"`
	Error          string                    `json:"error"`
	FailedNodeID   string                    `json:"failed_node_id"`
	FailedNodeType string                    `json:"failed_node_type"`
	HandledErrors  []WorkflowRunHandledError `json:"handled_errors"`
	StartedAt      time.Time                 `json:"started_at"`
	FinishedAt     *time.Time                `json:"finished_at"`
}

// WorkflowRunFinish is the terminal state a run is closed with.
type WorkflowRunFinish struct {
	Status         string
	Error          string
	FailedNodeID   string
	FailedNodeType string
	HandledErrors  []WorkflowRunHandledError
}

type WorkflowRunQuery struct {
	WorkflowID string
	Status     string // optional; "failed" also matches interrupted runs
	Limit      int
}

type WorkflowRunStorer interface {
	// StartWorkflowRun and FinishWorkflowRun are recorder operations bound to
	// the run's execution identity; they are never exposed as an HTTP write.
	StartWorkflowRun(ctx context.Context, run WorkflowRun) error
	FinishWorkflowRun(ctx context.Context, id string, finish WorkflowRunFinish) error
	HeartbeatWorkflowRuns(ctx context.Context, ids []string) error
	ListWorkflowRuns(ctx context.Context, q WorkflowRunQuery) ([]WorkflowRun, error)
	// SweepWorkflowRuns marks running rows whose heartbeat is older than
	// staleAfter as interrupted and deletes rows past their retention.
	SweepWorkflowRuns(ctx context.Context, staleAfter, keepCompleted, keepFailed time.Duration) (interrupted, deleted int64, err error)
}
