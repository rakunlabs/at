package service

import (
	"context"

	"github.com/rakunlabs/query"
)

// ─── Workflow Management ───

// WorkflowGraph is the full graph definition (nodes + edges) stored as JSON.
type WorkflowGraph struct {
	Nodes []WorkflowNode `json:"nodes"`
	Edges []WorkflowEdge `json:"edges"`
}

// WorkflowNode represents a single node in a workflow graph.
type WorkflowNode struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`                  // "input", "output", "llm_call", "template", "conditional", "loop", "mcp_tool", "code", "http_request"
	Position   WorkflowPos    `json:"position"`              // {x, y} for the visual editor
	Data       map[string]any `json:"data"`                  // node-type-specific configuration
	Width      *float64       `json:"width,omitempty"`       // visual width (groups, sticky notes)
	Height     *float64       `json:"height,omitempty"`      // visual height (groups, sticky notes)
	ParentID   string         `json:"parent_id,omitempty"`   // parent group node ID
	ZIndex     *int           `json:"z_index,omitempty"`     // layer ordering (groups = 0, nodes = 1)
	NodeNumber *int           `json:"node_number,omitempty"` // user-visible sequential number for logging
}

// WorkflowPos is the x/y position of a node in the visual editor.
type WorkflowPos struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// WorkflowEdge connects two nodes via their handles/ports.
type WorkflowEdge struct {
	ID           string `json:"id"`
	Source       string `json:"source"`        // source node ID
	Target       string `json:"target"`        // target node ID
	SourceHandle string `json:"source_handle"` // output port name on source
	TargetHandle string `json:"target_handle"` // input port name on target
}

// Workflow represents a saved workflow definition.
type Workflow struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Description   string        `json:"description"`
	Graph         WorkflowGraph `json:"graph"`
	ActiveVersion *int          `json:"active_version,omitempty"`
	CreatedAt     string        `json:"created_at"`
	UpdatedAt     string        `json:"updated_at"`
	CreatedBy     string        `json:"created_by"`
	UpdatedBy     string        `json:"updated_by"`
}

// WorkflowVersion represents an immutable snapshot of a workflow at a point in time.
type WorkflowVersion struct {
	ID          string        `json:"id"`
	WorkflowID  string        `json:"workflow_id"`
	Version     int           `json:"version"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Graph       WorkflowGraph `json:"graph"`
	CreatedAt   string        `json:"created_at"`
	CreatedBy   string        `json:"created_by"`
}

// WorkflowStorer defines CRUD operations for workflow definitions.
type WorkflowStorer interface {
	ListWorkflows(ctx context.Context, q *query.Query) (*ListResult[Workflow], error)
	GetWorkflow(ctx context.Context, id string) (*Workflow, error)
	// GetWorkflowByName resolves a workflow by its (unique) name. Returns
	// nil, nil when no workflow matches. Used by agents to attach workflows
	// by name the same way they attach skills and MCP sets.
	GetWorkflowByName(ctx context.Context, name string) (*Workflow, error)
	CreateWorkflow(ctx context.Context, w Workflow) (*Workflow, error)
	UpdateWorkflow(ctx context.Context, id string, w Workflow) (*Workflow, error)
	DeleteWorkflow(ctx context.Context, id string) error
}

// WorkflowVersionStorer defines operations for workflow version history.
type WorkflowVersionStorer interface {
	ListWorkflowVersions(ctx context.Context, workflowID string) ([]WorkflowVersion, error)
	GetWorkflowVersion(ctx context.Context, workflowID string, version int) (*WorkflowVersion, error)
	CreateWorkflowVersion(ctx context.Context, v WorkflowVersion) (*WorkflowVersion, error)
	SetActiveVersion(ctx context.Context, workflowID string, version int) error
}

// ─── Trigger Management ───

// Trigger represents a workflow or RAG sync trigger (HTTP webhook or cron schedule).
// Multiple triggers can reference the same target.
type Trigger struct {
	ID          string         `json:"id"`
	WorkflowID  string         `json:"workflow_id,omitempty"`   // kept for backward compat (= TargetID when TargetType is "workflow")
	TargetType  string         `json:"target_type"`             // "workflow" or "rag_sync"
	TargetID    string         `json:"target_id"`               // workflow ID or RAG collection ID
	EntryNodeID string         `json:"entry_node_id,omitempty"` // optional: specific input node to start from (workflow targets only)
	Type        string         `json:"type"`                    // "http" or "cron"
	Config      map[string]any `json:"config"`                  // type-specific configuration
	Alias       string         `json:"alias,omitempty"`         // optional human-friendly alias (unique)
	Public      bool           `json:"public"`                  // if true, no auth required; if false, Bearer token required
	Enabled     bool           `json:"enabled"`
	CreatedAt   string         `json:"created_at"`
	UpdatedAt   string         `json:"updated_at"`
	CreatedBy   string         `json:"created_by"`
	UpdatedBy   string         `json:"updated_by"`
}

const (
	// TriggerTargetWorkflow indicates the trigger executes a workflow.
	TriggerTargetWorkflow = "workflow"
	// TriggerTargetRAGSync indicates the trigger syncs a RAG collection.
	TriggerTargetRAGSync = "rag_sync"
)

// TriggerStorer defines CRUD operations for workflow triggers.
type TriggerStorer interface {
	ListAllTriggers(ctx context.Context) ([]Trigger, error)
	ListTriggers(ctx context.Context, workflowID string) ([]Trigger, error)
	GetTrigger(ctx context.Context, id string) (*Trigger, error)
	GetTriggerByAlias(ctx context.Context, alias string) (*Trigger, error)
	CreateTrigger(ctx context.Context, t Trigger) (*Trigger, error)
	UpdateTrigger(ctx context.Context, id string, t Trigger) (*Trigger, error)
	DeleteTrigger(ctx context.Context, id string) error
	ListEnabledCronTriggers(ctx context.Context) ([]Trigger, error)
}

// ─── Node Configs ───

// NodeConfig represents a reusable configuration for workflow nodes (e.g. SMTP server settings for email nodes).
// The Data field is a JSON blob whose schema depends on the Type (e.g. type "email" stores host, port, username, password, from, tls).
// Sensitive fields within Data (like password) are encrypted at rest.
type NodeConfig struct {
	ID        string `json:"id"`
	Name      string `json:"name"` // unique human-readable name (e.g. "Production SMTP")
	Type      string `json:"type"` // config type discriminator (e.g. "email", "slack", "sms")
	Data      string `json:"data"` // JSON blob with type-specific configuration
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	CreatedBy string `json:"created_by"`
	UpdatedBy string `json:"updated_by"`
}

// NodeConfigStorer defines CRUD operations for node configs.
type NodeConfigStorer interface {
	ListNodeConfigs(ctx context.Context, q *query.Query) (*ListResult[NodeConfig], error)
	ListNodeConfigsByType(ctx context.Context, configType string) ([]NodeConfig, error)
	GetNodeConfig(ctx context.Context, id string) (*NodeConfig, error)
	CreateNodeConfig(ctx context.Context, nc NodeConfig) (*NodeConfig, error)
	UpdateNodeConfig(ctx context.Context, id string, nc NodeConfig) (*NodeConfig, error)
	DeleteNodeConfig(ctx context.Context, id string) error
}
