package service

import (
	"context"
	"errors"
	"time"
)

var ErrWorkflowExecutionConflict = errors.New("workflow execution changed or is not actionable")

type WorkflowNodeCheckpoint struct {
	Kind      string         `json:"kind"`
	Data      map[string]any `json:"data,omitempty"`
	Selection []string       `json:"selection,omitempty"`
}

type WorkflowWaitState struct {
	NodeID    string         `json:"node_id"`
	Mode      string         `json:"mode"`
	Prompt    string         `json:"prompt,omitempty"`
	WakeAt    *time.Time     `json:"wake_at,omitempty"`
	ExpiresAt *time.Time     `json:"expires_at,omitempty"`
	Data      map[string]any `json:"data"`
}

type WorkflowCheckpoint struct {
	Version  int                               `json:"version"`
	Nodes    map[string]WorkflowNodeCheckpoint `json:"nodes"`
	Outputs  map[string]any                    `json:"outputs"`
	InFlight string                            `json:"in_flight,omitempty"`
	Waiting  *WorkflowWaitState                `json:"waiting,omitempty"`
}

type WorkflowExecutionPayload struct {
	Source       string         `json:"source"`
	Graph        WorkflowGraph  `json:"graph"`
	Inputs       map[string]any `json:"inputs"`
	EntryNodeIDs []string       `json:"entry_node_ids"`
}

// Internal record. HTTP handlers expose a summary, never provenance/session data.
type WorkflowExecution struct {
	ID              string
	WorkspaceID     string
	WorkflowID      string
	OwnerUserID     string
	Status          string
	Revision        int64
	LeaseOwner      string
	LeaseUntil      *time.Time
	ResumeRequested bool
	WaitNodeID      string
	WaitMode        string
	WaitPrompt      string
	WakeAt          *time.Time
	ExpiresAt       *time.Time
	DecisionBy      string
	Error           string
	Payload         WorkflowExecutionPayload
	Checkpoint      WorkflowCheckpoint
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type WorkflowExecutionStorer interface {
	CreateWorkflowExecution(context.Context, WorkflowExecution) (*WorkflowExecution, error)
	ListWorkflowExecutions(context.Context, string) ([]WorkflowExecution, error)
	GetWorkflowExecution(context.Context, string, string) (*WorkflowExecution, error)
	DecideWorkflowExecution(context.Context, string, string, int64, string) error
	// Worker-only operations require the private execution-maintenance marker.
	ClaimWorkflowExecution(context.Context, string, ...string) (*WorkflowExecution, error)
	SaveWorkflowExecutionCheckpoint(context.Context, string, string, int64, string, WorkflowCheckpoint, string) (int64, error)
	RenewWorkflowExecutionLease(context.Context, string, string) error
}
