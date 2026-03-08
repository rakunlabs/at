# Pitfalls Research

**Domain:** Hierarchical agent task routing in existing LLM gateway (Go monolith)
**Researched:** 2026-03-08
**Confidence:** HIGH (grounded in codebase analysis of AT's actual implementation)

## Critical Pitfalls

### Pitfall 1: Unbounded Recursive Delegation Depth Causing Goroutine/Cost Explosion

**What goes wrong:**
The existing `agent_call` node runs sub-agents by recursively creating a new `agentCallNode` and calling `.Run()` within the same goroutine (line 655-666 of agent-call.go). Each level of delegation creates a full agentic loop — LLM call, tool gathering, MCP client initialization, etc. With "unlimited depth delegation" (PROJECT.md requirement), a head agent delegates to a VP who delegates to a director who delegates to a manager who delegates to a worker. Each level holds its goroutine, all MCP client connections, and the full message history in memory. At 5+ levels deep with multiple sub-agents per level, this fans out combinatorially — a head agent delegating to 3 VPs, each delegating to 3 directors = 9 concurrent recursive chains, each consuming LLM API calls, goroutines, and MCP connections.

**Why it happens:**
The current `agent_call` sub-agent delegation is synchronous and recursive — the parent blocks waiting for the child, which blocks waiting for its child. This works for 1 level of delegation in a workflow. It breaks when you have N levels of delegation where N is unbounded and each level can fan out to multiple delegates simultaneously.

**How to avoid:**
1. Implement a hard configurable depth limit (e.g., `max_delegation_depth` on the Organization, default 5) even though PROJECT.md says "unlimited." Unlimited should mean "no artificial single-digit limit" not "literally unbounded."
2. The org-level task intake should NOT use synchronous recursive `Run()` calls. Instead, create Task records at each level and process them asynchronously — the head agent creates subtask records and returns, those subtasks are picked up by delegate agents independently.
3. Track `RequestDepth` (already exists on Task struct, line 657 in at.go) and enforce it as a circuit breaker.

**Warning signs:**
- Context deadline exceeded errors during delegation chains
- Memory growth proportional to org hierarchy depth
- Single task intake request consuming many seconds/minutes to return
- MCP client connection exhaustion (each recursive call opens new connections)

**Phase to address:**
Phase 1 (API/data model design). The async delegation model must be baked in from the start. Bolting async onto a synchronous recursive design is a rewrite.

---

### Pitfall 2: Conflating Workflow-Engine Delegation with Organization-Level Delegation

**What goes wrong:**
The existing `agent_call` node's delegation mechanism is designed for workflow graphs — sub-agents are wired statically via `agent_config` resource nodes connected by edges. Organization hierarchy delegation is fundamentally different: the set of delegates is dynamic (determined by `OrganizationAgent.ParentAgentID` relationships), the routing is LLM-decided at runtime, and tasks must be persisted as `Task` records with full lifecycle tracking. Developers try to make org-level delegation work by building a workflow graph on-the-fly or by deeply extending `agent_call`, creating a Frankenstein node that does two very different things.

**Why it happens:**
The `agent_call` node already "does delegation" via `delegate_to_*` tool calls (line 445). It's tempting to think: "we just need to inject the org hierarchy's direct reports as sub-agents into an agent_call run." But workflow delegation returns a result inline; org delegation should create Task records, track status, allow async completion, and support the existing checkout/release pattern.

**How to avoid:**
Build the org task intake as a **new API handler** (`POST /api/v1/organizations/{id}/tasks`) that:
1. Creates a Task record assigned to the head agent
2. Triggers the head agent's execution (not through the workflow engine DAG, but via a dedicated "org delegation" runtime)
3. The head agent's delegation creates new Task records for sub-agents, not recursive `agent_call` invocations
4. Keep `agent_call`'s existing workflow delegation untouched — it serves a different purpose

**Warning signs:**
- Adding organization-specific fields to the `agentCallNode` struct
- The `agent_call` Run method growing beyond 800 lines with org-specific branching
- Workflow-engine `Registry` needing org-level lookups (OrgAgentLookup, TaskCreator)
- Tests that require setting up a full org hierarchy to test basic agent_call behavior

**Phase to address:**
Phase 1 (Architecture decision). This is the most consequential fork in the road. Get it wrong and you're rebuilding.

---

### Pitfall 3: Head Agent Designation Without Hierarchy Validation Creates Invalid States

**What goes wrong:**
Organization sets a head agent, but:
- The head agent is later removed from the org (deleted from `organization_agents`)
- The head agent has no direct reports (leaf node designated as head)
- The head agent's parent_agent_id is non-empty (it reports to someone else, contradicting "head" semantics)
- Two agents claim to be head (if the field isn't exclusive)
- Agent is added to org after head designation, creating an orphan branch not connected to the head's subtree

The current `OrganizationAgent` table has no constraints preventing these states. `ParentAgentID` is a freeform string with no foreign key to the same table or the agents table.

**Why it happens:**
The hierarchy is structural metadata only today — no runtime behavior depends on it being consistent. Once delegation runs through it, every inconsistency becomes a runtime failure: tasks route to non-existent agents, delegation loops form, or parts of the org are unreachable.

**How to avoid:**
1. Add `head_agent_id` as a field on Organization (not on OrganizationAgent). Only one head per org.
2. Validate hierarchy on every mutation: `UpdateOrganizationAgent`, `DeleteOrganizationAgent`, canvas layout save. Specifically:
   - No cycles in parent_agent_id chain
   - Head agent must exist in org and have `parent_agent_id` empty
   - Deleting an agent with children is an error (or requires re-parenting)
   - Every non-head agent must be reachable from head via parent_agent_id chain
3. Build a `ValidateHierarchy(orgID)` function that does a full tree walk and returns errors — call it from API handlers.

**Warning signs:**
- Tasks stuck in "open" with no delegation because the target agent isn't in the org
- Canvas showing disconnected subtrees
- Head agent trying to delegate to agents that aren't its direct reports
- Null pointer panics when looking up agent by ParentAgentID that references a deleted agent

**Phase to address:**
Phase 1 (data model) for the `head_agent_id` field and basic validation. Phase 2 (hierarchy enforcement) for the full validation suite.

---

### Pitfall 4: Synchronous Task Intake Blocking on LLM Delegation Chains

**What goes wrong:**
The `POST /api/v1/organizations/{id}/tasks` endpoint creates a task and immediately has the head agent process it. The head agent makes an LLM call to decide delegation, which takes 2-10 seconds. Then the delegate agent processes its subtask (another LLM call). The HTTP request blocks for the entire chain. At 3 levels deep with 5-second LLM calls, the API request takes 15+ seconds. Add tool calls and it's minutes. API clients timeout. If the server restarts mid-delegation, all progress is lost.

**Why it happens:**
The existing workflow engine's `Run()` method (engine.go line 234) is synchronous — it returns when all nodes complete. The `EarlyOutput` channel pattern provides partial async for sync API responses, but the execution still runs in-process. Developers cargo-cult this pattern for org delegation.

**How to avoid:**
Task intake must be fire-and-forget:
1. `POST .../tasks` creates the Task record, assigns it to the head agent, returns `202 Accepted` with the task ID immediately
2. Head agent processing happens asynchronously — either via the existing heartbeat/wakeup mechanism (`WakeupRequestStorer` already exists) or a dedicated goroutine pool
3. Each delegation step is a new async unit: parent creates subtask → subtask agent is woken up → processes independently
4. Status polling via `GET /api/v1/tasks/{id}` (already exists) or webhook callback

**Warning signs:**
- Task intake API response times > 5 seconds
- Gateway timeouts on task submission
- Lost delegation progress after server restart
- Client-side "loading spinner" for 30+ seconds on task submit

**Phase to address:**
Phase 1 (API design). The 202-async pattern must be the design from day one. Adding async later means rewriting all the delegation orchestration.

---

### Pitfall 5: Three-Backend Store Multiplication Tax

**What goes wrong:**
Every new store method or schema change must be implemented three times: postgres, sqlite3, and memory. The existing codebase has 47 migrations for each SQL backend. Adding org-level task routing needs: new `head_agent_id` column on organizations, `ListTasksByParent` query, `ListDirectReports(orgID, agentID)` query, possibly a `DelegationRecord` table for tracking delegation decisions. Each addition = migration for postgres + migration for sqlite3 + Go code for postgres + Go code for sqlite3 + Go code for memory. A seemingly simple "add 3 queries" is actually 9+ files changed across 3 packages.

**Why it happens:**
This is the inherent cost of the multi-backend store pattern. Developers estimate work based on "one query" and forget the 3x multiplier plus migration files.

**How to avoid:**
1. **Batch schema changes.** Design all new fields and queries upfront, implement them in a single migration per backend, not one migration per field.
2. **Start with postgres, then sqlite3, then memory.** Postgres has the richest SQL — it's easiest to get right first. SQLite has subtle differences (no `ON CONFLICT ... DO UPDATE` with all same syntax, no `RETURNING *` in older versions). Memory is simplest but most tedious (manual index management).
3. **Minimize new tables.** Extend existing `Organization` and `Task` tables rather than creating new tables when possible. The `head_agent_id` can go on `organizations`. Delegation tracking can use the existing `AuditEntry` system.
4. Reuse existing query patterns — look at how `ListTasksByAgent` and `ListTasksByGoal` are implemented and follow the exact same pattern.

**Warning signs:**
- PR with 20+ files for "one small feature"
- SQLite tests passing but Postgres tests failing (or vice versa) due to SQL dialect differences
- Memory store getting out of sync with SQL stores (missing a new method)
- Compile errors in one backend that don't appear in another

**Phase to address:**
Every phase. Budget 3x for every store change. Front-load all schema changes to Phase 1.

---

### Pitfall 6: LLM Delegation Decisions Without Sufficient Context

**What goes wrong:**
The head agent receives a task and must decide which direct report to delegate to. But the LLM only sees the task title/description and a list of agent names with roles. It doesn't know: what each agent is currently working on (capacity), their skill set beyond the role title, their success rate on similar tasks, or the goal hierarchy context. The LLM makes random or superficial delegation choices — assigning backend tasks to the "Marketing Lead" because the task mentioned "user-facing."

**Why it happens:**
It's easy to build the mechanic (LLM picks an agent) without building the context injection (LLM has enough information to pick well). The system prompt for the delegating agent needs substantial enrichment.

**How to avoid:**
When constructing the delegation prompt for a manager agent, inject:
1. **Direct reports roster** with role, title, description, and current task count (from `ListTasksByAgent`)
2. **Agent capabilities** — the agent's system prompt summary, skills, and tools give a signal about what they're suited for
3. **Goal ancestry** — `GetGoalAncestry` already exists; inject the goal chain so the manager understands the "why"
4. **Current workload** — count of in-progress tasks per report agent
5. **Task context** — parent task chain if this is a sub-delegation

Don't try to make delegation "smart" in Phase 1. Make it functional with basic context, then improve context richness later.

**Warning signs:**
- Head agent always delegates to the first agent in the list
- Delegation choices that don't match agent roles
- Users manually re-assigning tasks after delegation
- Delegation system prompt is < 200 characters

**Phase to address:**
Phase 2 (delegation logic). Phase 1 should get the mechanic working. Phase 2 enriches the delegation prompt with context.

---

### Pitfall 7: Hierarchy Enforcement Gaps Allow Cross-Branch Delegation

**What goes wrong:**
PROJECT.md states "agents can only delegate to their direct reports." But the `delegate_to_*` tool mechanism (line 445 of agent-call.go) constructs tool names from agent names and doesn't validate hierarchy. A manager agent could, in theory, be given tools for any agent — not just their direct reports. If the delegation runtime accidentally exposes all org agents as delegation targets, a mid-level manager could delegate to the CEO's direct reports, bypassing the chain of command.

**Why it happens:**
The existing `agent_call` sub-agent delegation has no concept of hierarchy — it delegates to whatever agents are wired via edges. When repurposing this pattern for org delegation, developers wire up "all agents in the org" as potential delegates and rely on the LLM to "know" the hierarchy. LLMs don't reliably respect hierarchical constraints in their tool choices.

**How to avoid:**
Hierarchy enforcement must be **structural, not prompt-based**:
1. When constructing delegation tools for an agent, query `ListOrganizationAgents` filtered by `parent_agent_id = current_agent_id` — only direct reports become tools
2. Never include agents from other branches in the tool set
3. Validate at the API level: when a delegation (Task creation with ParentID) happens, verify that the assigned agent is a direct report of the delegating agent in the org hierarchy
4. Log hierarchy violations as audit entries even if you soft-fail

**Warning signs:**
- Agent's delegation tool list includes agents from different org branches
- Task delegation records where `assigned_agent_id` is not a direct report of the task creator
- LLM "hallucinating" delegation to agents that don't exist in the org
- System prompt containing "only delegate to your direct reports" as the sole enforcement

**Phase to address:**
Phase 2 (delegation runtime). This is the core business logic of the feature.

---

## Technical Debt Patterns

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|----------------|-----------------|
| Synchronous delegation for v1 (head agent blocks until subtask completes) | Simpler implementation, easier debugging | Blocks HTTP requests, can't scale beyond 2-3 depth levels, lost progress on restart | Never — even v1 must be async. The Task record IS the async boundary. |
| Skip memory store implementation for new methods | Save ~30% of store implementation time | Compile errors, broken tests for anyone using default config | Acceptable for initial PR if followed up immediately. Memory store is rarely used in prod. |
| Put org-delegation logic inside `agent_call` node | Reuses existing code, one node does everything | `agent_call` becomes unmaintainable, testing requires org hierarchy setup | Never — org delegation is a different runtime, not a workflow node. |
| Skip hierarchy validation on canvas drag-drop | Canvas feels snappy, less API roundtrips | Allows invalid hierarchies that break delegation at runtime | Only in Phase 1 if runtime delegation validates before executing |
| Hardcode delegation prompt template | Ships faster | Can't tune delegation quality without code changes | Acceptable for Phase 1, must become configurable (system prompt on head agent) by Phase 2 |
| Store delegation decisions only in audit log | No new tables needed | Can't query "who delegated what to whom" efficiently, no re-delegation support | Acceptable for MVP if audit log is structured (action="delegate", details has from/to agent IDs) |

## Integration Gotchas

| Integration | Common Mistake | Correct Approach |
|-------------|----------------|------------------|
| LLM Provider for delegation decisions | Using the same model/provider as the agent's work model. Delegation decisions need fast, cheap models; work tasks may need capable expensive models. | Allow separate `delegation_model` or use the org head agent's own provider/model config specifically for routing decisions. |
| Existing Task `CheckoutTask` mechanism | Assuming delegation creates a checkout. Checkout is for exclusive work locks; delegation is assignment. An agent can be assigned multiple tasks. | Use `assigned_agent_id` for delegation. Reserve `checked_out_by` for when the agent actively starts working on the task. |
| `agent_call` recursive sub-agent | Assuming recursive Run() will work with org context (task IDs, budget tracking, goal ancestry). The recursive call creates a bare sub-node with no task context. | Org delegation must create proper Task records. The sub-agent execution should run as a separate agent_call with full task context injected via prompt. |
| Canvas layout `CanvasLayout json.RawMessage` | Treating canvas as source of truth for hierarchy. Canvas stores visual positions; `OrganizationAgent.ParentAgentID` stores the actual hierarchy. They can drift. | Canvas should READ from the hierarchy data. Drag-drop on canvas should UPDATE `ParentAgentID` via API, then re-render. Canvas layout is visual-only metadata. |
| Budget checking during delegation chains | Each delegation level makes LLM calls, each consuming budget. Head agent's budget check passes, but by the time the 5th-level agent runs, the cumulative org spend exceeds limits. | Check org-level budget (not just agent budget) before accepting new tasks. The `BudgetMonthlyCents` field on Organization exists for this purpose. |

## Performance Traps

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|----------------|
| Loading entire org hierarchy for every delegation decision | API latency > 1s on delegation, N+1 query pattern (load agent, then load their reports, then each report's details) | Add `ListDirectReports(orgID, agentID)` as a single query returning agents with their roles. Cache the org hierarchy in memory with TTL during a delegation chain. | Orgs with > 50 agents |
| Unbounded message history in recursive delegation | Memory growth proportional to delegation depth * conversation turns * message size | Each delegation level should be a fresh conversation — don't pass parent's full message history to child. Pass only the delegated task description. | Delegation chains > 3 levels deep with tool-using agents |
| Full org agent list query on every task submission | DB round-trips per task intake | Cache org hierarchy on first load, invalidate on org-agent mutations. Use `updated_at` comparison for cache freshness. | > 10 tasks/minute submitted to same org |
| Audit log writes on every delegation step | Blocking I/O in the delegation hot path, especially with postgres over network | Buffer audit entries and flush asynchronously. The existing `RecordAudit` is synchronous. | Delegation chains > 3 levels with > 5 tool calls each |

## Security Mistakes

| Mistake | Risk | Prevention |
|---------|------|------------|
| Task intake API without org membership validation | Any API token holder can submit tasks to any org, potentially consuming budget | Validate that the API token's scope includes the target org. Add org-level access control on task intake endpoint. |
| Delegation allowing agents to access other orgs' agents | Agent belongs to multiple orgs (OrganizationAgent is a join table). Delegation in Org A could accidentally reference an agent's config from Org B. | Always filter agent lookups by `organization_id`. Delegation tool construction must scope to the current org's agents only. |
| Head agent system prompt injection via task description | Malicious task description contains prompt injection that makes head agent delegate to unauthorized paths or bypass hierarchy | Separate system prompt (trusted) from user task content (untrusted). Use delimiters/framing in the delegation prompt. Never concatenate task description directly into system prompt. |
| Missing budget enforcement on delegated subtasks | Head agent passes budget check, creates 10 subtasks, each subtask's agent also passes individual budget check, but total spend far exceeds org budget | Enforce org-level budget gate at task creation time, not just at LLM call time. Decrement org budget reservation on task creation. |

## UX Pitfalls

| Pitfall | User Impact | Better Approach |
|---------|-------------|-----------------|
| No visibility into delegation chain status | User submits task, sees "in progress" forever. No way to know it's been delegated 3 levels deep and is stuck at level 2. | Show delegation tree on task detail page — visual chain of who delegated to whom, with status at each level. Use ParentID chain to reconstruct. |
| Canvas drag-drop silently changes delegation behavior | User rearranges canvas for visual organization, unknowingly changes `ParentAgentID`, breaking existing delegation chains | Show a confirmation dialog when drag-drop would change the reporting structure. Distinguish visual positioning from hierarchy changes. |
| Head agent selection with no feedback on consequences | User picks head agent from dropdown, doesn't realize this agent has no direct reports, or is a leaf node in the hierarchy | Validate and warn at selection time: "This agent has no direct reports. Tasks will not be delegated further." Show the subtree that would handle tasks. |
| Task result only shows final worker's output | Intermediate delegation decisions (why head chose VP-Engineering, why VP chose Team Lead) are invisible | Store delegation reasoning as task comments or in a dedicated field. Each delegation step should record the LLM's rationale. |

## "Looks Done But Isn't" Checklist

- [ ] **Task intake API:** Often missing org-level budget check — verify task creation fails when org budget is exhausted
- [ ] **Head agent designation:** Often missing validation that the agent is actually in the org — verify setting a non-member as head returns error
- [ ] **Delegation chain:** Often missing circular delegation detection — verify that A→B→C→A is caught and fails gracefully
- [ ] **Hierarchy enforcement:** Often missing the "leaf node" case — verify that when a worker agent (no reports) receives a delegated task, it processes it directly instead of trying to delegate further
- [ ] **Three-backend parity:** Often missing memory store implementation — verify `make test` passes with default (memory) config, not just postgres
- [ ] **Async delegation:** Often missing the "what if the server restarts" case — verify that pending delegated tasks are resumed on startup (use existing heartbeat/wakeup mechanism)
- [ ] **Canvas sync:** Often missing bidirectional sync — verify that API changes to `ParentAgentID` update canvas visual, and canvas drag-drop updates `ParentAgentID`
- [ ] **Error propagation:** Often missing error surfacing in task result — verify that when a delegate agent fails, the parent task's status reflects the failure, not stuck "in_progress" forever

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|---------------|----------------|
| Synchronous delegation causing timeouts | HIGH | Requires architectural change to async. Add task queue, modify all delegation code to create Task records instead of blocking. 1-2 week rewrite. |
| Hierarchy inconsistencies in production | MEDIUM | Write a one-time migration script to validate all org hierarchies, fix orphaned agents (set parent to head or remove from org), add validation constraints. |
| Recursive goroutine explosion | LOW | Add depth limit check (read `RequestDepth` from Task, reject if > max). Deploy as hotfix. Then redesign async. |
| Cross-branch delegation data corruption | MEDIUM | Audit log query to find all delegations where assigned_agent is not a direct report. Re-assign affected tasks. Add hierarchy validation to delegation API. |
| Budget overruns from delegation chains | LOW | Check org-level spend, pause all org agents if over budget. Add budget reservation on task creation as preventive fix. |
| Memory store out of sync with SQL stores | LOW | Compile-time detection — if a new method is added to the store interface but not implemented by memory store, it won't compile. The Go compiler enforces this. |

## Pitfall-to-Phase Mapping

| Pitfall | Prevention Phase | Verification |
|---------|------------------|--------------|
| Unbounded recursive delegation depth | Phase 1: Data model | `RequestDepth` enforced in task creation. Integration test: create 6-deep chain, verify 6th level is rejected or becomes terminal. |
| Conflating workflow/org delegation | Phase 1: Architecture | Org delegation is a separate code path from `agent_call`. No org-specific code in `internal/service/workflow/nodes/`. |
| Invalid hierarchy states | Phase 1: Data model + Phase 2: Validation | `head_agent_id` on Organization. `ValidateHierarchy()` called on org-agent mutations. Test: delete agent with children → error. |
| Synchronous task intake | Phase 1: API design | `POST .../tasks` returns 202 immediately. Task processing verifiable via `GET .../tasks/{id}` polling. Load test: 10 concurrent submissions < 1s each. |
| Three-backend store tax | Phase 1: Schema + every phase | `make test` passes. Every new store method implemented in postgres, sqlite3, and memory. CI enforces. |
| Insufficient delegation context | Phase 2: Delegation logic | Delegation prompt includes direct reports with roles and current task counts. Manual review of 5 delegation decisions shows reasonable routing. |
| Cross-branch delegation | Phase 2: Hierarchy enforcement | Delegation tool list for agent X contains only X's direct reports. Test: attempt delegation to non-report → error. |

## Sources

- Codebase analysis: `internal/service/at.go` (domain types, Task/Organization/OrganizationAgent structs)
- Codebase analysis: `internal/service/workflow/nodes/agent-call.go` (recursive delegation mechanism, lines 387-470, 641-676)
- Codebase analysis: `internal/service/workflow/engine.go` (workflow execution model, goroutine fan-out)
- Codebase analysis: `internal/store/store.go` (three-backend pattern, StorerClose interface)
- Codebase analysis: `internal/service/workflow/node.go` (Registry pattern, lookup types)
- PROJECT.md requirements: unlimited depth, async delegation, hierarchy enforcement, canvas defines hierarchy
- Multi-agent orchestration patterns: common failure modes in LLM-based agent delegation systems (HIGH confidence — well-documented in multi-agent framework literature: AutoGen, CrewAI, LangGraph)

---
*Pitfalls research for: AT Organization Task Routing — Hierarchical Agent Delegation*
*Researched: 2026-03-08*
