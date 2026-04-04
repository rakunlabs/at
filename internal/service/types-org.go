package service

import (
	"context"
	"encoding/json"

	"github.com/rakunlabs/query"
)

// ─── Organizations (Multi-Tenant Isolation) ───

// Organization represents a tenant scope for grouping agents, goals, and tasks.
type Organization struct {
	ID                   string           `json:"id"`
	Name                 string           `json:"name"`
	Description          string           `json:"description"`
	IssuePrefix          string           `json:"issue_prefix,omitempty"`
	IssueCounter         int64            `json:"issue_counter,omitempty"`
	BudgetMonthlyCents   int64            `json:"budget_monthly_cents,omitempty"`
	SpentMonthlyCents    int64            `json:"spent_monthly_cents,omitempty"`
	BudgetResetAt        string           `json:"budget_reset_at,omitempty"`
	RequireBoardApproval bool             `json:"require_board_approval_for_new_agents"`
	HeadAgentID          string           `json:"head_agent_id,omitempty"`
	MaxDelegationDepth   int              `json:"max_delegation_depth,omitempty"`
	CanvasLayout         json.RawMessage  `json:"canvas_layout,omitempty"`
	ContainerConfig      *ContainerConfig `json:"container_config,omitempty"`
	CreatedAt            string           `json:"created_at"`
	UpdatedAt            string           `json:"updated_at"`
	CreatedBy            string           `json:"created_by"`
	UpdatedBy            string           `json:"updated_by"`
}

// ContainerConfig holds optional Docker container configuration for isolated agent execution.
type ContainerConfig struct {
	Enabled bool   `json:"enabled"`
	Image   string `json:"image,omitempty"`
	CPU     string `json:"cpu,omitempty"`
	Memory  string `json:"memory,omitempty"`
	Network bool   `json:"network"`
}

// OrganizationStorer defines CRUD operations for organizations.
type OrganizationStorer interface {
	ListOrganizations(ctx context.Context, q *query.Query) (*ListResult[Organization], error)
	GetOrganization(ctx context.Context, id string) (*Organization, error)
	CreateOrganization(ctx context.Context, org Organization) (*Organization, error)
	UpdateOrganization(ctx context.Context, id string, org Organization) (*Organization, error)
	DeleteOrganization(ctx context.Context, id string) error
	IncrementIssueCounter(ctx context.Context, orgID string) (int64, error)
}

// ─── Organization–Agent Membership (Join Table) ───

// OrganizationAgent represents the many-to-many relationship between an
// organization and an agent.
type OrganizationAgent struct {
	ID                string `json:"id"`
	OrganizationID    string `json:"organization_id"`
	AgentID           string `json:"agent_id"`
	Role              string `json:"role,omitempty"`
	Title             string `json:"title,omitempty"`
	ParentAgentID     string `json:"parent_agent_id,omitempty"`
	Status            string `json:"status,omitempty"`
	HeartbeatSchedule string `json:"heartbeat_schedule,omitempty"`
	MemoryModel       string `json:"memory_model,omitempty"`
	MemoryProvider    string `json:"memory_provider,omitempty"`
	MemoryMethod      string `json:"memory_method,omitempty"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

// OrganizationAgentStorer defines CRUD operations for the organization–agent join table.
type OrganizationAgentStorer interface {
	ListOrganizationAgents(ctx context.Context, orgID string) ([]OrganizationAgent, error)
	ListAgentOrganizations(ctx context.Context, agentID string) ([]OrganizationAgent, error)
	GetOrganizationAgent(ctx context.Context, id string) (*OrganizationAgent, error)
	GetOrganizationAgentByPair(ctx context.Context, orgID, agentID string) (*OrganizationAgent, error)
	CreateOrganizationAgent(ctx context.Context, oa OrganizationAgent) (*OrganizationAgent, error)
	UpdateOrganizationAgent(ctx context.Context, id string, oa OrganizationAgent) (*OrganizationAgent, error)
	DeleteOrganizationAgent(ctx context.Context, id string) error
	DeleteOrganizationAgentByPair(ctx context.Context, orgID, agentID string) error
}

// ─── Goals (Mission Alignment) ───

// Goal level constants.
const (
	GoalLevelCompany = "company"
	GoalLevelTeam    = "team"
	GoalLevelAgent   = "agent"
	GoalLevelTask    = "task"
)

// Goal represents a hierarchical objective in an organization.
type Goal struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id,omitempty"`
	ParentGoalID   string `json:"parent_goal_id,omitempty"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	Level          string `json:"level,omitempty"`
	Status         string `json:"status"`
	Priority       int    `json:"priority"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	CreatedBy      string `json:"created_by"`
	UpdatedBy      string `json:"updated_by"`
}

// GoalStorer defines CRUD operations for goals.
type GoalStorer interface {
	ListGoals(ctx context.Context, q *query.Query) (*ListResult[Goal], error)
	GetGoal(ctx context.Context, id string) (*Goal, error)
	CreateGoal(ctx context.Context, goal Goal) (*Goal, error)
	UpdateGoal(ctx context.Context, id string, goal Goal) (*Goal, error)
	DeleteGoal(ctx context.Context, id string) error
	ListGoalsByParent(ctx context.Context, parentID string) ([]Goal, error)
	GetGoalAncestry(ctx context.Context, id string) ([]Goal, error)
}

// ─── Projects ───

// Project links goals to actual work, tracking progress and ownership.
type Project struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id,omitempty"`
	GoalID         string `json:"goal_id,omitempty"`
	LeadAgentID    string `json:"lead_agent_id,omitempty"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	Status         string `json:"status"`
	Color          string `json:"color,omitempty"`
	TargetDate     string `json:"target_date,omitempty"`
	ArchivedAt     string `json:"archived_at,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	CreatedBy      string `json:"created_by"`
	UpdatedBy      string `json:"updated_by"`
}

// ProjectStorer defines CRUD operations for projects.
type ProjectStorer interface {
	ListProjects(ctx context.Context, q *query.Query) (*ListResult[Project], error)
	GetProject(ctx context.Context, id string) (*Project, error)
	CreateProject(ctx context.Context, project Project) (*Project, error)
	UpdateProject(ctx context.Context, id string, project Project) (*Project, error)
	DeleteProject(ctx context.Context, id string) error
	ListProjectsByGoal(ctx context.Context, goalID string) ([]Project, error)
	ListProjectsByOrganization(ctx context.Context, orgID string) ([]Project, error)
}
