package service

import (
	"context"

	"github.com/rakunlabs/query"
)

// ─── Tasks (Ticket System) ───

// Task status constants.
const (
	TaskStatusBacklog    = "backlog"
	TaskStatusOpen       = "open"
	TaskStatusTodo       = "todo"
	TaskStatusInProgress = "in_progress"
	TaskStatusInReview   = "in_review"
	TaskStatusBlocked    = "blocked"
	TaskStatusReview     = "review"
	TaskStatusCompleted  = "completed"
	TaskStatusDone       = "done"
	TaskStatusCancelled  = "cancelled"
)

// Task priority constants.
const (
	TaskPriorityCritical = "critical"
	TaskPriorityHigh     = "high"
	TaskPriorityMedium   = "medium"
	TaskPriorityLow      = "low"
)

// Task represents a unit of work (issue) assigned to an agent, linked to a goal.
type Task struct {
	ID              string `json:"id"`
	OrganizationID  string `json:"organization_id,omitempty"`
	ProjectID       string `json:"project_id,omitempty"`
	GoalID          string `json:"goal_id,omitempty"`
	ParentID        string `json:"parent_id,omitempty"`
	AssignedAgentID string `json:"assigned_agent_id,omitempty"`
	Identifier      string `json:"identifier,omitempty"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	Status          string `json:"status"`
	PriorityLevel   string `json:"priority_level,omitempty"`
	Priority        int    `json:"priority"`
	Result          string `json:"result,omitempty"`
	BillingCode     string `json:"billing_code,omitempty"`
	RequestDepth    int    `json:"request_depth,omitempty"`
	// MaxIterations overrides agent.Config.MaxIterations for this specific
	// task. 0 = use the agent's default. Use this to give complex tasks a
	// higher iteration budget without affecting the agent's other tasks.
	// The iteration counter always starts fresh at 0 for each runOrgDelegation
	// invocation, so creating a brand-new task always begins with iteration 0
	// regardless of any previous task's progress.
	MaxIterations int    `json:"max_iterations,omitempty"`
	CheckedOutBy  string `json:"checked_out_by,omitempty"`
	CheckedOutAt  string `json:"checked_out_at,omitempty"`
	StartedAt     string `json:"started_at,omitempty"`
	CompletedAt   string `json:"completed_at,omitempty"`
	CancelledAt   string `json:"cancelled_at,omitempty"`
	HiddenAt      string `json:"hidden_at,omitempty"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	CreatedBy     string `json:"created_by"`
	UpdatedBy     string `json:"updated_by"`
}

// TaskStorer defines CRUD operations for tasks.
type TaskStorer interface {
	ListTasks(ctx context.Context, q *query.Query) (*ListResult[Task], error)
	GetTask(ctx context.Context, id string) (*Task, error)
	CreateTask(ctx context.Context, task Task) (*Task, error)
	UpdateTask(ctx context.Context, id string, task Task) (*Task, error)
	DeleteTask(ctx context.Context, id string) error
	ListTasksByAgent(ctx context.Context, agentID string) ([]Task, error)
	ListTasksByGoal(ctx context.Context, goalID string) ([]Task, error)
	CheckoutTask(ctx context.Context, taskID, agentID string) error
	ReleaseTask(ctx context.Context, taskID string) error
	ListChildTasks(ctx context.Context, parentID string) ([]Task, error)
	UpdateTaskStatus(ctx context.Context, id string, status string, result string) error
}

// ─── Issue Comments ───

// IssueComment represents a threaded comment on a task/issue.
type IssueComment struct {
	ID         string `json:"id"`
	TaskID     string `json:"task_id"`
	AuthorType string `json:"author_type"`
	AuthorID   string `json:"author_id"`
	Body       string `json:"body"`
	ParentID   string `json:"parent_id,omitempty"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// IssueCommentStorer defines operations for issue comments.
type IssueCommentStorer interface {
	ListCommentsByTask(ctx context.Context, taskID string) ([]IssueComment, error)
	GetComment(ctx context.Context, id string) (*IssueComment, error)
	CreateComment(ctx context.Context, comment IssueComment) (*IssueComment, error)
	UpdateComment(ctx context.Context, id string, comment IssueComment) (*IssueComment, error)
	DeleteComment(ctx context.Context, id string) error
}

// ─── Labels ───

// Label represents a per-organization label with a color, used to tag tasks.
type Label struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id,omitempty"`
	Name           string `json:"name"`
	Color          string `json:"color"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// LabelStorer defines CRUD operations for labels and task-label associations.
type LabelStorer interface {
	ListLabels(ctx context.Context, orgID string) ([]Label, error)
	GetLabel(ctx context.Context, id string) (*Label, error)
	CreateLabel(ctx context.Context, label Label) (*Label, error)
	UpdateLabel(ctx context.Context, id string, label Label) (*Label, error)
	DeleteLabel(ctx context.Context, id string) error
	AddLabelToTask(ctx context.Context, taskID, labelID string) error
	RemoveLabelFromTask(ctx context.Context, taskID, labelID string) error
	ListLabelsForTask(ctx context.Context, taskID string) ([]Label, error)
	ListTasksForLabel(ctx context.Context, labelID string) ([]string, error)
}

// ─── Approvals ───

// Approval type constants.
const (
	ApprovalTypeHireAgent    = "hire_agent"
	ApprovalTypeBudgetChange = "budget_change"
	ApprovalTypeTaskEscalate = "task_escalate"
)

// Approval status constants.
const (
	ApprovalStatusPending           = "pending"
	ApprovalStatusRevisionRequested = "revision_requested"
	ApprovalStatusApproved          = "approved"
	ApprovalStatusRejected          = "rejected"
	ApprovalStatusApprovalCancelled = "cancelled"
)

// Approval represents a governance approval request.
type Approval struct {
	ID              string         `json:"id"`
	OrganizationID  string         `json:"organization_id,omitempty"`
	Type            string         `json:"type"`
	Status          string         `json:"status"`
	RequestedByType string         `json:"requested_by_type"`
	RequestedByID   string         `json:"requested_by_id"`
	RequestDetails  map[string]any `json:"request_details,omitempty"`
	DecisionNote    string         `json:"decision_note,omitempty"`
	DecidedByUserID string         `json:"decided_by_user_id,omitempty"`
	DecidedAt       string         `json:"decided_at,omitempty"`
	CreatedAt       string         `json:"created_at"`
	UpdatedAt       string         `json:"updated_at"`
}

// ApprovalStorer defines operations for the approval workflow.
type ApprovalStorer interface {
	ListApprovals(ctx context.Context, q *query.Query) (*ListResult[Approval], error)
	GetApproval(ctx context.Context, id string) (*Approval, error)
	CreateApproval(ctx context.Context, approval Approval) (*Approval, error)
	UpdateApproval(ctx context.Context, id string, approval Approval) (*Approval, error)
	ListPendingApprovals(ctx context.Context, orgID string) ([]Approval, error)
}
