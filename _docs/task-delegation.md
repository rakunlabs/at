# Task Delegation & Agent-to-Agent Communication

AT supports multi-agent task delegation where a parent agent can assign work to sub-agents, collect results, and delegate further (e.g., to a reviewer). There are two approaches: **organization-based delegation** (dynamic, LLM-driven) and **workflow DAG delegation** (static, graph-driven).

## Organization-Based Delegation

### Organization Hierarchy

Agents are organized into an org chart via the Organizations page. Each agent in an organization has:

| Field | Description |
|-------|-------------|
| **Role** | Functional role (e.g., "CTO", "Engineer") |
| **Title** | Display title (e.g., "Senior Backend Engineer") |
| **Parent Agent** | Reporting line within the org |
| **Status** | `active`, `paused`, or `terminated` |

The organization has a **Head Agent** — the top-level agent that receives all incoming tasks.

### How It Works

```
Human creates task
  → Head Agent receives it
  → LLM decides to delegate (via delegate_to_* tools)
  → Child tasks created and assigned to sub-agents
  → Sub-agents process (may delegate further)
  → Results return to parent as tool responses
  → Parent decides next step (delegate to reviewer, finish, etc.)
```

### Step-by-Step Lifecycle

#### 1. Task Creation

Submit a task to an organization via API or UI:

```bash
curl -X POST /api/v1/organizations/{org_id}/tasks \
  -H 'Content-Type: application/json' \
  -d '{"title": "Implement user auth", "description": "Add JWT-based authentication"}'
```

The API returns **202 Accepted** immediately. The task is assigned to the org's head agent and processing starts in the background.

#### 2. Head Agent Thinks

The system:
1. Loads the head agent's config (provider, model, system prompt)
2. Queries for the agent's **direct reports** (active agents where `parent_agent_id` matches)
3. Creates a `delegate_to_{agent_name}` tool for each report
4. Enriches the system prompt with a **"Your Team"** section listing each report's name, role, title, and description
5. Calls the LLM with the task description and delegation tools

#### 3. Agent Delegates

When the LLM decides to delegate, the system:
1. Creates a **child task** linked to the parent via `parent_id`
2. Assigns it to the chosen sub-agent
3. Increments `request_depth` (max depth of 10 prevents infinite recursion)
4. **Recursively** runs the same delegation process for the sub-agent

Multiple delegations run **concurrently** — one goroutine per delegation.

#### 4. Results Flow Back

When a sub-agent finishes (LLM produces a final answer), its task is marked `completed` with the result. The result returns to the parent agent's LLM as a tool response. The parent can then:

- Delegate to another agent (e.g., send code to a reviewer)
- Delegate to the same agent with feedback
- Produce a final answer

#### 5. Task Completes

When the parent agent produces a final answer with no more tool calls, the task is marked `completed` with the LLM's response as the `result`.

### Example: Code + Review Pattern

Consider an org with three agents:

```
CTO (Head Agent)
├── CodeWriter (role: "Engineer")
└── CodeReviewer (role: "Reviewer")
```

When a task "Implement feature X" is submitted:

1. **CTO** receives the task and sees two delegation tools: `delegate_to_code_writer` and `delegate_to_code_reviewer`
2. **CTO** calls `delegate_to_code_writer(task: "Implement feature X with these requirements...")`
3. **CodeWriter** runs its agentic loop (may use skills, MCP tools, etc.) and returns code
4. **CTO** receives the code as a tool response
5. **CTO** calls `delegate_to_code_reviewer(task: "Review this code for correctness and style: <code>")`
6. **CodeReviewer** analyzes the code and returns feedback
7. **CTO** receives the review. If issues found, it can delegate back to **CodeWriter** with fix instructions
8. This loop continues until the **CTO** is satisfied and produces a final answer

The parent LLM orchestrates the entire loop — it decides when to delegate, who to delegate to, and when the work is done.

### Process an Existing Task

You can also trigger delegation on a task that already exists:

```bash
curl -X POST /api/v1/tasks/{task_id}/process
```

This assigns the task to the org's head agent (if not already assigned) and starts the same delegation flow.

## Workflow DAG Delegation

The workflow engine provides a visual, graph-based approach to agent chaining.

### Explicit Chaining via Edges

Wire one `agent_call` node's output into another's input through the visual editor:

```
input ──prompt──> agent_call_1 ──response──> template ──text──> agent_call_2 ──response──> output
                       ↑                                              ↑
                  skill_config                                   skill_config
                  (coding skills)                                (review skills)
```

- Agent 1's response flows through a template node that formats it as a prompt for Agent 2
- Each agent can have its own provider, model, system prompt, and skills
- Resource config nodes (`skill_config`, `mcp_config`, `memory_config`) connect via bottom-handle ports

### Sub-Agent Delegation via Tools

The `agent_call` node also supports dynamic delegation. Connect `agent_config` nodes to the `agents` input port:

```
agent_config ──agent──> agent_call[agents]
```

This creates `delegate_to_{agent_name}` tools that the LLM can call. When invoked, a new `agent_call` node is created and run recursively — identical to the organization pattern.

### Sub-Workflow Composition

The `workflow_call` node executes an entire separate workflow synchronously:

```
input ──> workflow_call (runs "review-pipeline" workflow) ──> output
```

The child workflow runs with its own engine instance, and its output node results become the parent node's output.

## Conditional Routing

Both approaches support conditional logic:

- **Org delegation**: The parent LLM decides dynamically based on results
- **Workflow**: Use `conditional` nodes (JS expression → `true`/`false` ports) or `script` nodes (3-port routing: `true`/`false`/`always`) to route data based on conditions

## Key Properties

| Property | Description |
|----------|-------------|
| **Concurrency** | Child delegations run in parallel goroutines |
| **Depth limit** | `MaxDelegationDepth` (default 10) prevents infinite recursion |
| **Budget enforcement** | Checked before every LLM call; exceeding stops execution |
| **Exclusive checkout** | `CheckoutTask` prevents two agents from working the same task |
| **Audit trail** | Every LLM call and tool invocation is recorded |
| **Cost tracking** | Per-call cost events linked to agent, task, project, and billing code |

## Task Statuses

Tasks move through these statuses during their lifecycle:

| Status | Meaning |
|--------|---------|
| `open` | Created, assigned to agent, not yet started |
| `in_progress` | Agent is actively working |
| `in_review` | Submitted for review |
| `completed` | Finished with a result |
| `cancelled` | Cancelled before completion |
| `blocked` | Waiting on external dependency |

## API Reference

### Task Endpoints

```bash
# List all tasks
GET /api/v1/tasks

# Create a task (standalone)
POST /api/v1/tasks

# Create and auto-delegate via organization
POST /api/v1/organizations/{org_id}/tasks

# Get task (use ?include=subtasks for tree view)
GET /api/v1/tasks/{id}

# Update task
PUT /api/v1/tasks/{id}

# Trigger delegation on existing task
POST /api/v1/tasks/{id}/process

# Checkout/release (exclusive lock)
POST /api/v1/tasks/{id}/checkout
POST /api/v1/tasks/{id}/release

# Task comments
GET /api/v1/tasks/{id}/comments
POST /api/v1/tasks/{id}/comments
```

### Agent Endpoints

```bash
# CRUD
GET    /api/v1/agents
POST   /api/v1/agents
GET    /api/v1/agents/{id}
PUT    /api/v1/agents/{id}
DELETE /api/v1/agents/{id}

# Agent tasks and budget
GET /api/v1/agents/{id}/tasks
GET /api/v1/agents/{id}/budget
PUT /api/v1/agents/{id}/budget
GET /api/v1/agents/{id}/usage
GET /api/v1/agents/{id}/spend
```

## Choosing an Approach

| | Org Delegation | Workflow DAG |
|---|---|---|
| **Control** | LLM decides dynamically | Pipeline defined explicitly |
| **Flexibility** | High — agent chooses tools and sub-agents at runtime | Moderate — fixed graph with conditional branches |
| **Visibility** | Task tree with parent/child relationships | Visual node editor with execution logs |
| **Use case** | Complex, open-ended tasks requiring judgment | Repeatable automation pipelines |
| **Review pattern** | Parent agent loops: delegate → review → iterate | Chain agent nodes: writer → reviewer → output |
