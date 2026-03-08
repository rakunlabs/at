# Architecture Patterns

**Domain:** Hierarchical agent task routing within an existing LLM gateway
**Researched:** 2026-03-08
**Confidence:** HIGH — based on deep analysis of existing codebase, not external sources

## Executive Summary

This is a brownfield integration problem, not a greenfield architecture. The AT codebase already has every primitive needed for hierarchical task routing — organizations with agent memberships and `ParentAgentID` hierarchy, tasks with `ParentID` sub-task chains, and an `agent_call` workflow node with recursive `delegate_to_{agent}` sub-agent execution. The architecture challenge is bridging these three existing subsystems with a thin orchestration layer, not building new infrastructure.

The recommended architecture introduces a single new component — an **Org Task Router** (a Go service-layer function, not a new node type) — that receives tasks submitted to an organization, resolves the hierarchy, and recursively delegates using the existing `agent_call` machinery. This keeps the blast radius small and reuses battle-tested code paths.

## Recommended Architecture

### High-Level Data Flow

```
External Request
    │
    ▼
POST /api/v1/organizations/{id}/tasks
    │
    ▼
┌─────────────────────────────┐
│  Org Task Intake Handler    │  ← new HTTP handler in server/
│  (validates org, creates    │
│   root Task, finds head     │
│   agent, kicks off routing) │
└─────────────┬───────────────┘
              │
              ▼
┌─────────────────────────────┐
│  Org Task Router            │  ← new service-layer orchestrator
│  (resolves hierarchy tree,  │
│   builds agent_call config, │
│   manages delegation chain) │
└─────────────┬───────────────┘
              │
              ▼
┌─────────────────────────────┐
│  Head Agent (agent_call)    │  ← existing agent_call machinery
│  LLM decides: handle self   │
│  or delegate via tool call  │
│  "delegate_to_{report}"     │
└───────┬───────────┬─────────┘
        │           │
   (handles)    (delegates)
        │           │
        ▼           ▼
   Task Result   ┌──────────────────┐
                 │  Sub-Task Created │  ← new Task record (parent_id = root)
                 │  Manager Agent    │  ← next agent_call (recursive)
                 │  LLM decides...   │
                 └───────┬───────────┘
                         │
                    (recurses N levels deep)
```

### Component Boundaries

| Component | Responsibility | Communicates With | New/Existing |
|-----------|---------------|-------------------|--------------|
| **Org Task Intake Handler** | HTTP endpoint, validation, root task creation, async kickoff | OrganizationStore, TaskStore, OrgAgentStore, Org Task Router | **New** (server/org-task-routing.go) |
| **Org Task Router** | Resolves head agent, builds hierarchy context, manages delegation lifecycle | OrgAgentStore, AgentStore, TaskStore, agent_call node (reused) | **New** (service layer, ~200 lines) |
| **Organization** | Stores `head_agent_id` field | OrgAgentStore for agent membership | **Modified** (add 1 field) |
| **OrganizationAgent** | Stores hierarchy via `ParentAgentID` | Already exists, read-only in this flow | **Existing** |
| **Task** | Stores delegation chain via `ParentID`, results via `Result` | TaskStore CRUD | **Existing** |
| **agent_call node** | Agentic loop with LLM, tool execution, sub-agent delegation | LLM providers, MCP, skills, sub-agents | **Existing** (reused programmatically) |
| **Hierarchy Resolver** | Walks `OrganizationAgent` tree to find direct reports for any agent | OrgAgentStore | **New** (pure function, ~50 lines) |
| **Delegation Tool Builder** | Creates `delegate_to_{name}` tools scoped to an agent's direct reports | Hierarchy Resolver, AgentStore | **New** (pure function, ~30 lines) |
| **Canvas (Svelte UI)** | Head agent selector in org settings, task submission UI | Organization API, Org Task Intake API | **Modified** (add head agent picker + submit button) |

### Boundary Rules

1. **The Org Task Router does NOT touch LLM providers directly.** It delegates to `agent_call` node machinery which handles all LLM interaction, tool calling, and iteration limits.
2. **The agent_call node does NOT know about organizations.** It receives a prompt, tools (including delegation tools), and an agent config. Organization awareness lives in the Router.
3. **Task creation happens in the Router, not in agent_call.** When a delegation tool fires, the Router intercepts the result, creates a sub-task record, then recursively invokes the next agent.
4. **Hierarchy enforcement is structural.** An agent's delegation tools are generated from its direct reports in the org — it literally cannot delegate to anyone else because no tool exists.

## Data Flow: Detailed Walk-Through

### 1. Task Intake (Synchronous)

```
Client → POST /api/v1/organizations/{org_id}/tasks
  Body: { "title": "Build auth module", "description": "...", "priority_level": "high" }

Handler:
  1. Load org → check org.HeadAgentID is set (400 if not)
  2. Create root Task record:
     - OrganizationID = org_id
     - Status = "in_progress"
     - Identifier = org.IssuePrefix + "-" + IncrementIssueCounter()
     - AssignedAgentID = org.HeadAgentID
  3. Return 202 Accepted + Task record (async processing begins)
  4. Spawn goroutine → orgTaskRouter.Route(ctx, org, task)
```

### 2. Head Agent Routing (Async)

```
orgTaskRouter.Route(ctx, org, rootTask):
  1. Load org agent memberships → build hierarchy tree
  2. Find head agent's direct reports
  3. Build delegation prompt:
     "You are {agent.Name}, {orgAgent.Role} at {org.Name}.
      Your direct reports are: {list with roles/titles}.
      Task: {rootTask.Title} - {rootTask.Description}
      Decide: handle this yourself or delegate to a direct report."
  4. Build delegation tools:
     For each direct report:
       delegate_to_{sanitized_name}(task: string) → "Delegate to {name} ({role})"
     Plus:
       complete_task(result: string) → "Complete the task with this result"
  5. Create temporary agent_call config from head agent's Agent record
  6. Execute agent_call.Run(ctx, registry, {prompt, tools})
```

### 3. Delegation Decision (LLM-Driven)

```
LLM Response options:
  A. Completes task directly → tool call: complete_task(result: "...")
     → Router updates rootTask.Status = "completed", rootTask.Result = result
     → Done

  B. Delegates → tool call: delegate_to_vp_engineering(task: "...")
     → Router intercepts:
       1. Create sub-task:
          - ParentID = rootTask.ID
          - AssignedAgentID = vp_engineering's agent ID
          - Title = delegated task description
          - Status = "in_progress"
       2. Recursively: orgTaskRouter.RouteToAgent(ctx, org, subTask, vpAgent)
       3. Return sub-agent's result as tool call response to head agent
       4. Head agent may delegate more tasks or complete

  C. Delegates to multiple → multiple tool calls in same turn
     → Router processes each sequentially (or concurrently via goroutines)
     → Each creates a sub-task, each runs recursively
     → All results returned to the delegating agent
```

### 4. Recursive Depth

```
Head Agent (CEO)
  └── delegates "Build auth module" →
      VP Engineering
        └── delegates "Design auth schema" →
            Senior Engineer
              └── completes: "Schema: users table with..."
        └── delegates "Implement OAuth flow" →
            Auth Specialist
              └── completes: "OAuth implementation plan..."
      VP Engineering completes: "Auth module plan: {combined results}"
  Head Agent completes: "Auth module delegated and planned: {VP result}"
```

No artificial depth limit. Recursion terminates naturally when agents with no direct reports handle tasks directly, or when any agent chooses `complete_task` instead of delegating.

## Key Integration Points with Existing Code

### Reusing agent_call's Agentic Loop

The `agent_call` node (768 lines) already implements:
- Agent preset loading (`AgentLookup`)
- Provider resolution with model override
- MCP tool collection and deduplication
- Skill loading with system prompt fragments
- Builtin tool dispatch
- **Sub-agent delegation via `delegate_to_{name}` tools** (lines 387-470)
- Budget checking per LLM call
- Usage recording per LLM call
- Audit logging per tool call
- Iteration limits

**The Router should NOT duplicate this.** Instead, it programmatically constructs a `WorkflowNode` config and calls `newAgentCallNode` + `node.Run()` — exactly how the existing sub-agent delegation works (lines 648-675 of agent-call.go).

### What Changes in agent_call

**Nothing.** The existing sub-agent handler type (`"agent"`, lines 641-676) already:
1. Creates a temporary node from agent ID
2. Calls `newAgentCallNode` recursively
3. Runs the sub-agent with an isolated prompt
4. Returns the response as the tool result

The Router wraps this pattern but adds:
- Organization-aware hierarchy resolution (which sub-agents to offer)
- Task record creation at each delegation step
- An enriched system prompt with org context

### Modified Store: Organization

Add one field:

```go
type Organization struct {
    // ... existing fields ...
    HeadAgentID string `json:"head_agent_id,omitempty"` // designated head agent for task routing
}
```

One migration per store backend (postgres + sqlite3). Memory store updated in-place.

### New Store Queries Needed

**None.** All required queries already exist:
- `ListOrganizationAgents(ctx, orgID)` — get all agents in org
- `GetOrganizationAgentByPair(ctx, orgID, agentID)` — get specific membership
- `GetAgent(ctx, id)` — load agent config
- `CreateTask(ctx, task)` — create sub-tasks
- `UpdateTask(ctx, id, task)` — update status/result
- `IncrementIssueCounter(ctx, orgID)` — auto-identifiers

### Hierarchy Resolution (Pure Function)

```go
// resolveDirectReports returns OrganizationAgents whose ParentAgentID == agentID.
func resolveDirectReports(agents []OrganizationAgent, agentID string) []OrganizationAgent {
    var reports []OrganizationAgent
    for _, oa := range agents {
        if oa.ParentAgentID == agentID && oa.Status == "active" {
            reports = append(reports, oa)
        }
    }
    return reports
}
```

Load all org agents once at Route() entry, then filter in-memory. No new DB queries needed.

## Patterns to Follow

### Pattern 1: Programmatic agent_call Reuse

**What:** Create `agentCallNode` instances programmatically and call `Run()` directly, bypassing the workflow DAG engine.

**When:** The Router needs to invoke agents but isn't running inside a workflow graph.

**Why:** The agent_call node is self-contained — it needs only a `Registry` and inputs. The Router builds a Registry from the server's existing stores (identical to how `Engine.Run` does it in server.go).

**Example:**
```go
func (r *OrgTaskRouter) runAgent(ctx context.Context, agent *service.Agent, prompt string, delegateTools []service.Tool) (string, error) {
    nodeConfig := service.WorkflowNode{
        Data: map[string]any{
            "agent_id": agent.ID,
        },
    }
    node, err := nodes.NewAgentCallNode(nodeConfig)
    if err != nil {
        return "", fmt.Errorf("init agent %s: %w", agent.Name, err)
    }

    inputs := map[string]any{
        "prompt": prompt,
    }
    // delegation tools injected through the agent's skill or inline tools
    result, err := node.Run(ctx, r.registry, inputs)
    if err != nil {
        return "", fmt.Errorf("agent %s failed: %w", agent.Name, err)
    }

    return result.Data()["response"].(string), nil
}
```

**Note:** The `newAgentCallNode` function is currently unexported. The Router will need either:
(a) Export it as `NewAgentCallNode` — minimal change, follows Go convention
(b) Add a helper function in the workflow package — slightly more indirection

Option (a) is cleaner. This is one small modification to agent-call.go.

### Pattern 2: Custom Delegation Tools with Router Interception

**What:** Instead of using agent_call's built-in `"agent"` handler type (which recursively creates a new agent_call), the Router registers delegation tools with a custom handler that creates Task records before recursing.

**When:** Every delegation step in org task routing.

**Why:** The built-in sub-agent delegation in agent_call skips Task creation — it just runs the sub-agent inline. For org routing, we need a persistent Task record at each delegation step for tracking and auditing.

**Implementation approach:** The Router doesn't use agent_call's sub-agent feature at all. Instead:
1. Router builds delegation tools as inline tools with handler type `"js"` or a new handler type `"org_delegate"`
2. Each tool's handler: creates sub-task → recursively calls Router → returns result
3. This keeps Task creation in the Router's control

**Alternative (simpler):** The Router handles the agentic loop itself — calling the LLM provider directly, processing tool calls, and managing delegation. This avoids modifying agent_call but duplicates the iteration logic. Given agent_call is 768 lines, this is NOT recommended.

**Recommended approach:** Add a callback mechanism. The Router passes a custom tool handler function to the agent execution that intercepts `delegate_to_*` tool calls, creates a Task, recurses, and returns the result. This plugs into the existing `toolHandlers` map pattern.

### Pattern 3: Async Execution with Task Tracking

**What:** Task routing runs asynchronously. The intake handler returns 202 immediately. Progress tracked via Task status updates.

**When:** Always — LLM calls are slow, delegation chains can be deep.

**Why:** A 5-level delegation chain could take 30+ seconds of LLM calls. HTTP timeout would kill the request. Async with polling is the standard pattern.

**Tracking:**
- Root task: `in_progress` → `completed`/`failed`
- Sub-tasks: created as `in_progress`, updated on completion
- Client polls `GET /api/v1/tasks/{id}` or lists sub-tasks via `GET /api/v1/tasks?parent_id={id}`

### Pattern 4: System Prompt Enrichment with Org Context

**What:** When routing through the org hierarchy, each agent's system prompt is enriched with organizational context.

**When:** Every agent invocation in the routing chain.

**Example prompt assembly:**
```
[Agent's base system_prompt from Agent record]

[Organization Context]
You are {orgAgent.Title} ({orgAgent.Role}) at {org.Name}.
Organization: {org.Description}

[Hierarchy Context]
Your direct reports:
- {report1.Name}: {report1.Title} ({report1.Role}) - {report1Agent.Config.Description}
- {report2.Name}: {report2.Title} ({report2.Role}) - {report2Agent.Config.Description}

[Task Context]
Task ID: {task.Identifier}
Task: {task.Title}
Description: {task.Description}
Priority: {task.PriorityLevel}

You must either:
1. Handle this task yourself and call complete_task(result: "your answer")
2. Delegate to one or more of your direct reports using their delegation tools
```

## Anti-Patterns to Avoid

### Anti-Pattern 1: Building a New Workflow Node Type

**What:** Creating an `org_task_route` workflow node for the DAG engine.

**Why bad:** The routing is inherently recursive and dynamic — the "graph" shape depends on LLM decisions at runtime. Workflow DAGs are static and defined at design time. Forcing dynamic recursion into a DAG creates impedance mismatch.

**Instead:** Keep routing as a service-layer function invoked by an HTTP handler. A workflow node can wrap it later (out of scope per PROJECT.md).

### Anti-Pattern 2: Duplicating the Agentic Loop

**What:** Reimplementing LLM chat → tool call → execute → repeat in the Router.

**Why bad:** agent_call.go handles edge cases (budget checks, usage recording, audit logging, MCP deduplication, thought signatures, iteration limits, bash/JS/builtin handlers). Duplicating this creates maintenance burden and misses edge cases.

**Instead:** Reuse `agentCallNode.Run()` with injected delegation handlers.

### Anti-Pattern 3: Modifying OrganizationAgent for Routing State

**What:** Adding `current_task_id`, `routing_status`, or similar fields to OrganizationAgent.

**Why bad:** OrganizationAgent is a structural join table — it describes who reports to whom. Runtime state belongs in Task records and AgentRuntimeState.

**Instead:** Track routing state via Task.Status, Task.AssignedAgentID, and Task.ParentID chain.

### Anti-Pattern 4: Synchronous Routing

**What:** Blocking the HTTP request until the entire delegation chain completes.

**Why bad:** Deep chains with multiple LLM calls can take minutes. HTTP timeouts, client disconnects, and resource exhaustion.

**Instead:** 202 Accepted + async goroutine + poll via task API.

### Anti-Pattern 5: Global Hierarchy Cache

**What:** Caching the org hierarchy in memory and invalidating on changes.

**Why bad:** Hierarchy changes are rare (admin action). The cost of loading all org agents on each routing request is negligible (single DB query, typically <100 rows). Cache invalidation adds complexity for no measurable gain.

**Instead:** Load fresh from DB on each routing request.

## Component Dependency Graph (Build Order)

```
Phase 1: Foundation (no dependencies)
├── Organization.HeadAgentID field + migration
├── Hierarchy resolver (pure function)
└── Delegation tool builder (pure function)

Phase 2: Core Routing (depends on Phase 1)
├── Export newAgentCallNode in agent-call.go
├── Org Task Router service layer
└── Custom delegation handler (intercepts delegate_to_* → creates sub-task → recurses)

Phase 3: HTTP Interface (depends on Phase 2)
├── POST /api/v1/organizations/{id}/tasks handler
├── Task status polling (already exists: GET /api/v1/tasks/{id})
└── List sub-tasks (already exists: GET /api/v1/tasks with query filter)

Phase 4: UI Integration (depends on Phase 3)
├── Head agent selector in org settings
├── Task submission form in org detail view
└── Delegation tree visualization (optional, deferred)
```

### Why This Order

1. **Phase 1 has zero risk** — adding a nullable field to Organization and writing pure functions can't break anything.
2. **Phase 2 is the core logic** — it depends on the hierarchy resolver and uses the exported node factory. Build and test in isolation with unit tests before wiring HTTP.
3. **Phase 3 is thin** — the handler just validates, creates a task, and calls the router. Most task API endpoints already exist.
4. **Phase 4 is additive** — UI changes don't affect backend correctness. A head agent picker is a dropdown. Task submission is a form.

## Scalability Considerations

| Concern | At 10 agents | At 100 agents | At 1000 agents |
|---------|-------------|---------------|----------------|
| Hierarchy resolution | In-memory filter on list | Same — single query returns all | Consider DB-level filtering by parent_agent_id |
| Concurrent delegations | Sequential is fine | Goroutine per delegation | Need rate limiting per org to prevent LLM cost explosion |
| Task volume | Direct lookup | Index on parent_id, org_id | Already indexed in existing migrations |
| LLM cost | Budget per agent | Budget per agent (existing) | Need org-level budget rollup (out of scope) |
| Recursion depth | ~3 levels typical | ~5 levels typical | Cap at configurable max (default 10) to prevent runaway |

### Key Scalability Decision: Recursion Depth Limit

Add a configurable max delegation depth (default 10) to prevent infinite recursion from circular hierarchies or LLM confusion. This is NOT in the current OrganizationAgent model (no cycle detection). The Router should track depth as a parameter passed through recursive calls.

```go
func (r *OrgTaskRouter) routeToAgent(ctx context.Context, org *Organization, task *Task, agentID string, depth int) error {
    if depth > r.maxDepth {
        return fmt.Errorf("delegation depth %d exceeds max %d", depth, r.maxDepth)
    }
    // ... routing logic ...
    // recursive call: depth + 1
}
```

## File Placement in Existing Codebase

| New File | Purpose | Lines (est.) |
|----------|---------|-------------|
| `internal/server/org-task-routing.go` | HTTP handler for `POST /api/v1/organizations/{id}/tasks` | ~100 |
| `internal/service/org-router.go` | `OrgTaskRouter` struct, `Route()`, `routeToAgent()`, hierarchy helpers | ~250 |
| `internal/store/postgres/migrations/XX_add_org_head_agent.sql` | Add `head_agent_id` to organizations table | ~5 |
| `internal/store/sqlite3/migrations/XX_add_org_head_agent.sql` | Same for sqlite3 | ~5 |

| Modified File | Change | Lines Changed (est.) |
|--------------|--------|---------------------|
| `internal/service/at.go` | Add `HeadAgentID` to `Organization` struct | 1 |
| `internal/server/server.go` | Register new route, pass stores to Router | ~5 |
| `internal/server/organizations.go` | Preserve `HeadAgentID` in partial updates | ~3 |
| `internal/store/postgres/organizations.go` | Include `head_agent_id` in queries | ~10 |
| `internal/store/sqlite3/organizations.go` | Same for sqlite3 | ~10 |
| `internal/store/memory/organizations.go` | Same for memory | ~5 |
| `internal/service/workflow/nodes/agent-call.go` | Export `NewAgentCallNode` (rename `newAgentCallNode`) | 1 |

**Total estimated new code:** ~400 lines
**Total estimated modified code:** ~35 lines

This is intentionally small. The architecture leverages what exists rather than building new infrastructure.

## Sources

- Existing codebase analysis: `internal/service/at.go` (1443 lines — all domain types)
- Existing agent_call implementation: `internal/service/workflow/nodes/agent-call.go` (768 lines)
- Existing workflow engine: `internal/service/workflow/engine.go` (634 lines)
- Existing organization model: `internal/server/organizations.go`, `internal/server/organization-agents.go`
- Existing task model: `internal/server/tasks.go`
- Project requirements: `.planning/PROJECT.md`
