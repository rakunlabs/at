# Technology Stack

**Project:** AT Organization Task Routing
**Researched:** 2026-03-08

## Recommended Stack

This is a brownfield enhancement. The entire stack already exists. The research question is: **what patterns and minimal additions are needed to wire hierarchical LLM-driven task delegation into the existing Go monolith?**

### Core Framework (Already In Place — No Changes)

| Technology | Version | Purpose | Why |
|------------|---------|---------|-----|
| Go | 1.26 | Backend language | Existing codebase, no migration needed |
| ada | v0.2.11 | HTTP framework | All existing routes use ada; adding new endpoints is trivial |
| goqu/v9 | v9.19.0 | SQL query builder | All store backends (postgres, sqlite3) use goqu consistently |
| pgx/v5 | v5.8.0 | PostgreSQL driver | Already the postgres backend driver |
| modernc.org/sqlite | v1.46.1 | SQLite driver | Already the sqlite3 backend driver |
| ulid/v2 | v2.1.1 | ID generation | All entities use ULID for primary keys |
| Svelte 5 | (in _ui/) | Frontend framework | Existing admin UI; canvas already renders org hierarchy |

**Confidence: HIGH** — These are direct observations from go.mod and codebase, not research.

### New Backend Components (Build, Don't Buy)

No new Go libraries are needed. Every required capability already exists in the codebase:

| Component | How to Build | Existing Pattern to Follow |
|-----------|-------------|---------------------------|
| Head agent designation | Add `head_agent_id` field to `Organization` struct + migration | `OrganizationAgent.ParentAgentID` — existing field pattern |
| Org task intake API | New handler `POST /api/v1/organizations/{id}/tasks` | `organizations.go` handler pattern, `tasks.go` for task creation |
| Hierarchy walker | Query `OrganizationAgent.ParentAgentID` tree in Go | `GetGoalAncestry` — existing recursive ancestor walking |
| Delegation orchestrator | New service function that wires agent_call recursion to org hierarchy | `agent_call` node's sub-agent delegation (lines 387-470 of agent-call.go) |
| Task creation during delegation | Call `TaskStorer.CreateTask` with `ParentID` set | Existing `Task.ParentID` + `Task.AssignedAgentID` fields |
| Async fan-out delegation | goroutine-per-delegate with WaitGroup | `runFanOutBranch` in engine.go (lines 438-480) |

**Confidence: HIGH** — Every pattern is verified by reading the actual source code.

### Database Changes (Migrations 48+)

| Change | Type | Backend Impact | Rationale |
|--------|------|----------------|-----------|
| `head_agent_id TEXT` on organizations table | ALTER TABLE | postgres + sqlite3 + memory struct | Designates which agent receives all incoming org tasks |
| Index on `(organization_id, parent_agent_id)` on organization_agents | CREATE INDEX | postgres + sqlite3 | Fast hierarchy traversal when delegating; avoids full table scan |
| Index on `(parent_id, organization_id)` on tasks | CREATE INDEX | postgres + sqlite3 | Fast sub-task lookup per parent, scoped to org |

**No new tables required.** The existing `tasks` table with `parent_id`, `assigned_agent_id`, and `organization_id` is sufficient for tracking delegation chains. The existing `organization_agents` table with `parent_agent_id` defines the hierarchy.

**Confidence: HIGH** — Verified existing schema supports the data model. Only a head_agent_id field and performance indexes are needed.

### Frontend Changes (Svelte 5)

| Component | Approach | Rationale |
|-----------|----------|-----------|
| Head agent selector | Dropdown on org detail page, writes `head_agent_id` to org record | Simple CRUD update; similar to `LeadAgentID` on Project |
| Task submission form | New panel on org detail page, POST to intake API | Standard form pattern used throughout the admin UI |
| Delegation tree view | Recursive task list showing parent→child chain | Can reuse canvas tree layout logic that already renders `parent_agent_id` hierarchy |

**Confidence: MEDIUM** — Frontend patterns observed from AGENTS.md descriptions but not deeply audited.

## What NOT to Use (And Why)

### Don't Use: External Orchestration Frameworks (LangGraph, CrewAI, AutoGen)

**Why not:** AT already has a custom workflow engine with DAG execution, topological sorting, concurrent fan-out, and an `agent_call` node with recursive sub-agent delegation. Adding an external multi-agent framework would:
1. Create a parallel execution model that conflicts with AT's engine
2. Introduce Python dependencies into a Go monolith (LangGraph, CrewAI) or complex Go bindings
3. Duplicate existing capabilities — the `agent_call` node already does agentic loops with tool calling and sub-agent delegation

**Instead:** Extend the existing `agent_call` delegation pattern. The `delegate_to_{agent_name}` tool call mechanism (agent-call.go lines 437-470) is exactly the right abstraction — it just needs to be wired to the org hierarchy instead of statically configured agent_config nodes.

**Confidence: HIGH** — Direct code analysis confirms the existing delegation mechanism is sufficient.

### Don't Use: Message Queue / Event Bus (NATS, RabbitMQ, Kafka)

**Why not:** The delegation is LLM-driven and synchronous-from-the-agent's-perspective — the head agent calls the LLM, gets back a delegation decision, creates a sub-task, and runs the sub-agent. This is the exact pattern already in `agent_call.Run()`. Adding a message queue would:
1. Introduce eventual consistency where none is needed
2. Add operational complexity (broker deployment, dead letter queues)
3. Break the existing request/response model that the `agent_call` node uses

**Instead:** Use Go goroutines with sync.WaitGroup for async fan-out, exactly as `runFanOutBranch` does in the workflow engine. For the org delegation case, the orchestrator function spawns goroutines per delegate agent, each creating a Task record and running `agent_call`.

**Confidence: HIGH** — The existing concurrent execution model handles this without external infrastructure.

### Don't Use: Separate Task Queue (Temporal, Celery)

**Why not:** AT's task model is a database-backed ticket system with `Task` records, not a distributed job queue. The delegation creates persistent `Task` records with status tracking, which already exists. Temporal-style workflows would duplicate the existing `WorkflowStorer` + `HeartbeatRun` execution tracking.

**Instead:** Leverage existing `Task` + `HeartbeatRun` for execution tracking and the `WakeupRequest` system for async agent invocation.

**Confidence: HIGH** — Existing infrastructure covers all needed execution tracking.

### Don't Use: Graph Database (Neo4j, Dgraph)

**Why not:** The org hierarchy is a simple tree (each agent has one parent via `parent_agent_id`). SQL with recursive CTEs (available in both PostgreSQL and SQLite) handles tree queries efficiently. The hierarchy is shallow in practice (5-10 levels max), and the tree is queried infrequently (only on task delegation).

**Instead:** `WITH RECURSIVE` CTE for hierarchy traversal, or simple iterative Go code that walks parent_agent_id links.

**Confidence: HIGH** — Standard relational pattern, well-supported by both database backends.

## Architecture Decision: Delegation Orchestrator

The key new component is a **delegation orchestrator** function. This is NOT a new service, library, or framework — it's a function in the existing Go codebase that:

1. **Receives** an org task (from the intake API handler)
2. **Resolves** the head agent from `Organization.HeadAgentID`
3. **Runs** the head agent via `agent_call` with:
   - The task as prompt
   - Direct reports as available delegate tools (queried from `OrganizationAgent` where `parent_agent_id = head_agent_id`)
   - Org hierarchy context in system prompt
4. **When head agent delegates:** Creates a child `Task` record, recursively invokes the delegate agent with their own direct reports as tools
5. **When any agent completes:** Updates Task status, rolls result back to parent

This follows the EXACT pattern already in `agent_call.go` lines 640-675 where sub-agents are recursively invoked. The only difference is that instead of statically wired `agent_config` nodes, the sub-agents are dynamically resolved from the org hierarchy.

### Implementation Sketch

```go
// internal/service/org-delegation.go

// DelegateToOrg submits a task to an organization for hierarchical delegation.
// It resolves the head agent and recursively delegates through the org hierarchy.
func (s *Server) DelegateToOrg(ctx context.Context, orgID string, task service.Task) (*service.Task, error) {
    // 1. Get org + head agent
    org, err := s.organizationStore.GetOrganization(ctx, orgID)
    // ...
    headAgentID := org.HeadAgentID
    
    // 2. Create root task
    task.OrganizationID = orgID
    task.AssignedAgentID = headAgentID
    task.Status = service.TaskStatusInProgress
    created, err := s.taskStore.CreateTask(ctx, task)
    
    // 3. Run delegation chain
    result, err := s.runDelegation(ctx, orgID, headAgentID, created)
    // ...
}

// runDelegation runs an agent, providing its direct reports as delegation tools.
func (s *Server) runDelegation(ctx context.Context, orgID, agentID string, task *service.Task) (string, error) {
    // Get direct reports
    allMembers, _ := s.orgAgentStore.ListOrganizationAgents(ctx, orgID)
    var directReports []service.OrganizationAgent
    for _, m := range allMembers {
        if m.ParentAgentID == agentID {
            directReports = append(directReports, m)
        }
    }
    
    // Build delegate tools (same as agent_call.go lines 437-470)
    // Run agent via existing agent_call pattern
    // On delegation: create child task, recurse
}
```

**Confidence: HIGH** — This is a thin orchestration layer over existing patterns, not new technology.

## Supporting Libraries (Already in go.mod — No New Dependencies)

| Library | Version | Used For | Status |
|---------|---------|----------|--------|
| `golang.org/x/sync` | v0.19.0 | errgroup for concurrent delegation branches | Already in go.mod |
| `github.com/oklog/ulid/v2` | v2.1.1 | Task ID generation | Already used everywhere |
| `github.com/doug-martin/goqu/v9` | v9.19.0 | SQL query building for new store methods | Already used in all store backends |
| `github.com/rakunlabs/query` | v0.4.0 | Pagination for task list endpoints | Already used in all list handlers |

## Installation

```bash
# No new dependencies needed. Zero go get commands.
# The entire stack is already in place.
```

## Alternatives Considered

| Category | Recommended | Alternative | Why Not Alternative |
|----------|-------------|-------------|---------------------|
| Delegation mechanism | Extend agent_call pattern | LangGraph / CrewAI multi-agent | Python dependency, duplicates existing engine |
| Async execution | goroutines + WaitGroup | Message queue (NATS/RabbitMQ) | Over-engineering for in-process fan-out |
| Hierarchy storage | SQL with parent_agent_id FK | Graph database | Tree depth is shallow, SQL CTEs sufficient |
| Task tracking | Existing Task table | New delegation_runs table | Task already has parent_id, assigned_agent_id, status |
| Execution tracking | Existing HeartbeatRun | Temporal workflows | Already have run tracking infrastructure |
| Head agent config | Field on Organization | Separate config table | Single field is simpler, one head agent per org |

## Key Insight

**This milestone requires NO new technology.** The entire stack — LLM provider routing, agentic loops, sub-agent delegation, task hierarchy, org hierarchy, concurrent execution, cost tracking, audit logging — already exists. The work is pure **wiring**: connecting the org hierarchy data model to the agent_call delegation mechanism, with a thin orchestration layer and a new API endpoint.

The biggest risk is not technology but **prompt engineering**: crafting the right system prompt so that manager agents make good delegation decisions based on their direct reports' roles and capabilities.

## Sources

- `go.mod` — direct dependency inspection (HIGH confidence)
- `internal/service/at.go` — domain model verification (HIGH confidence)
- `internal/service/workflow/nodes/agent-call.go` — delegation pattern verification (HIGH confidence)
- `internal/service/workflow/engine.go` — concurrent execution pattern verification (HIGH confidence)
- `internal/store/postgres/organizations.go` — store pattern verification (HIGH confidence)
- `internal/server/organizations.go` — handler pattern verification (HIGH confidence)
