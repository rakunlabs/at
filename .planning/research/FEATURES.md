# Feature Research

**Domain:** Hierarchical agent task routing for LLM gateway
**Researched:** 2026-03-08
**Confidence:** HIGH — most features build on existing, verified infrastructure

## Feature Landscape

### Table Stakes (Users Expect These)

Features that anyone deploying a hierarchical agent organization expects to work. Missing any of these makes the routing system feel broken or incomplete.

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| Head agent designation | Without an explicit entry point, tasks have nowhere to go. Every org needs a root. | LOW | Add `head_agent_id` field to Organization model, dropdown in UI. Trivial schema change across 3 store backends. |
| Org-level task intake API | The whole point — tasks must enter the org as a unit. Without this, callers must know internal agent structure. | MEDIUM | `POST /api/v1/organizations/{id}/tasks` creates Task, looks up head agent, triggers delegation. Needs to wire org context into agent_call execution. |
| LLM-driven delegation by head agent | Head agent must use its system prompt + knowledge of direct reports to decide who handles a task. This is the core value prop. | HIGH | Build delegation prompt that includes direct reports' roles/titles/skills. Head agent returns `delegate_to_{name}` tool call. Leverage existing agent_call machinery. |
| Sub-task creation on delegation | Each delegation must create a Task record linked via `parent_task_id`. Without this, there's no audit trail or status tracking. | MEDIUM | Existing Task model has `ParentTaskID`. Delegation handler creates child task, assigns to target agent. Wire into agent_call's delegate flow. |
| Hierarchy enforcement | Agents must only delegate to their direct reports (children in `parent_agent_id` tree). Without this, the org chart is meaningless. | MEDIUM | Before executing delegation, validate target agent is a direct child of delegating agent in OrganizationAgent table. Reject otherwise. |
| Multi-level delegation chain | Head -> VP -> Director -> IC must work. Delegation must be recursive with no artificial depth limit. | HIGH | Each level runs its own agent_call with its own direct reports as available delegates. Recursive but bounded by tree depth. Must handle context propagation and task lineage across levels. |
| Task status propagation | When a leaf agent completes a task, parent tasks should reflect progress. Without this, submitters can't track anything. | MEDIUM | Leaf task completion -> check siblings -> if all done, mark parent done (or surface to parent agent for review). Needs careful state machine. |
| Delegation result aggregation | When a manager delegates to multiple sub-agents, their results must be collected and synthesized back to the delegator. | HIGH | Existing agent_call has sub-agent result handling. Extend for async fan-out: collect results from parallel delegations, feed back into manager's context. |

### Differentiators (Competitive Advantage)

Features that set AT's org routing apart from CrewAI/AutoGen/Swarm patterns.

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| Canvas-driven hierarchy (visual = structural) | Drag agents on canvas to restructure org chart. No code, no config files. CrewAI/AutoGen require code changes for hierarchy changes. | LOW | Canvas already renders based on `parent_agent_id`. Need: drag-to-reparent saves to API, which already exists. Mostly UI wiring. |
| Persistent task lineage across delegation depth | Full Task records at every level with parent-child links, not just ephemeral message passing. CrewAI tasks are transient; AutoGen conversations evaporate. | MEDIUM | Already have Task model with `ParentTaskID`. Differentiation is that every delegation creates a durable, queryable record — not just an in-memory handoff. |
| Async parallel delegation | Manager delegates to 3 sub-agents simultaneously, doesn't block on sequential completion. CrewAI's hierarchical process is sequential by default. | HIGH | agent_call already supports fan-out via `NodeResultFanOut`. Need to expose this through delegation: manager issues multiple `delegate_to_X` calls, engine runs them concurrently. |
| Hot-swappable agents | Change an agent's provider/model/prompt without redeploying. Agent registry is DB-backed with hot reload. CrewAI/AutoGen require code redeploy. | LOW | Already exists. Differentiator is inherent to AT's architecture. Just needs to work correctly when mid-delegation (don't swap while agent is running). |
| Org-scoped task identifiers | Tasks get org-prefixed IDs (e.g., "PAP-42") for human readability. Professional project management feel. | LOW | Already implemented — `Identifier` field on Task with org's `IssuePrefix`. Just ensure intake API assigns them correctly. |
| Mixed provider hierarchy | Head agent can be Claude, VP can be GPT-4, worker can be Gemini. Each level picks the best model for its role. CrewAI locks you to one manager LLM. | LOW | Already possible — each Agent has its own `provider` + `model`. The delegation chain naturally uses each agent's configured provider. Zero extra work. |
| Org hierarchy context in system prompt | Manager agents see their direct reports' roles, titles, and specializations injected into system prompt for informed delegation decisions. | LOW | Build template that lists direct reports from OrganizationAgent table. Inject at delegation time. |
| Budget checking per delegation step | Each agent checked against budget before LLM call. Prevents runaway spend in deep delegation chains. | LOW | Already exists — `reg.CheckBudget` called before each `Chat()`. Just needs to be wired into org delegation path. |

### Anti-Features (Commonly Requested, Often Problematic)

| Feature | Why Requested | Why Problematic | Alternative |
|---------|---------------|-----------------|-------------|
| Rule-based auto-routing | "I want task type X to always go to Agent Y" | Defeats the purpose of LLM-driven delegation. Creates brittle mappings that break when org changes. Dual routing logic (rules + LLM) is confusing. | Give head agent a detailed system prompt describing each report's specialization. The LLM IS the routing engine. |
| Automatic escalation chains | "If worker can't handle it, auto-escalate to manager" | Creates infinite loops, unclear ownership, noisy escalation storms. Who decides "can't handle"? | Agent reports failure in task result. Manager agent reviews and re-delegates or handles directly. LLM judgment, not automation. |
| Budget rollup across hierarchy | "Show total spend for a branch of the org tree" | Complex aggregation across async delegation chains. Race conditions. Misleading numbers during in-flight work. | Per-agent budgets (already exist). Org-level budget cap (already exists). Dashboard aggregation can come later as read-only view. |
| Real-time streaming of delegation progress | "Show live updates as tasks flow through the org" | WebSocket complexity, partial state rendering, confusing UX when multiple delegation branches are active. | Poll task status. Show delegation tree with statuses. Good enough for v1, streaming can layer on later. |
| Workflow node for org_call | "I want to trigger org routing from a workflow" | Scope explosion — workflows need org context injection, error handling for multi-level async delegation, timeout management across hierarchy. | API-only intake for v1. `POST /organizations/{id}/tasks` is the entry point. Workflow node in v2 after patterns stabilize. |
| Dynamic agent creation during delegation | "Let manager spin up new agents on the fly" | Uncontrolled resource creation, no human approval, hard to track/clean up, security implications. | Use existing approval system. Manager can request hire_agent approval. Human approves, then agent is available for future delegation. |
| Cross-org delegation | "Agent in Org A delegates to agent in Org B" | Breaks org boundaries, confused ownership, permission nightmares, split task lineage. | Keep orgs isolated. If you need shared capability, add agent to both orgs with different roles. |
| Dynamic org chart modification during delegation | "Let agents restructure the org during a task run" | Race conditions, incoherent delegation chains, impossible to reason about mid-flight hierarchy changes. | Org chart is static during a delegation run. Restructure between runs. |

## Feature Dependencies

```
[Head Agent Designation]
    └──requires──> [Org-Level Task Intake API]
                       └──requires──> [LLM-Driven Delegation]
                                          ├──requires──> [Sub-Task Creation on Delegation]
                                          ├──requires──> [Hierarchy Enforcement]
                                          └──requires──> [Multi-Level Delegation Chain]
                                                             └──requires──> [Delegation Result Aggregation]

[Task Status Propagation] ──enhances──> [Sub-Task Creation on Delegation]

[Canvas-Driven Hierarchy] ──enhances──> [Head Agent Designation]
[Canvas-Driven Hierarchy] ──enhances──> [Hierarchy Enforcement]

[Async Parallel Delegation] ──enhances──> [Multi-Level Delegation Chain]
[Async Parallel Delegation] ──requires──> [Delegation Result Aggregation]

[Persistent Task Lineage] ──enhances──> [Sub-Task Creation on Delegation]
[Persistent Task Lineage] ──enhances──> [Task Status Propagation]

[Org Hierarchy Context in Prompt] ──enhances──> [LLM-Driven Delegation]

[Budget Checking] ──enhances──> [All Delegation Steps] (already exists, wire in)
```

### Dependency Notes

- **Task Intake API requires Head Agent Designation:** The intake endpoint needs to know which agent receives the task. Head agent field must exist first.
- **LLM-Driven Delegation requires Task Intake:** Delegation logic triggers after a task reaches an agent. The intake pipeline must exist to deliver tasks.
- **Multi-Level Chain requires LLM Delegation:** Each level in the chain performs the same delegation logic recursively. Core delegation must work before depth.
- **Async Parallel Delegation requires Result Aggregation:** If you fan out to multiple agents, you must have a way to collect and synthesize their results. Fan-out without collection is useless.
- **Canvas-Driven Hierarchy enhances Head Agent Designation:** Selecting head agent in the canvas UI is the ideal UX, but the field can exist without canvas integration.
- **Task Status Propagation enhances Sub-Task Creation:** Status rollup only matters once sub-tasks exist and are linked.
- **Org Hierarchy Context enhances LLM Delegation:** Injecting org structure into system prompt makes delegation smarter, but delegation works (poorly) without it.

## MVP Definition

### Launch With (v1)

Minimum to validate that org-level task routing works end-to-end.

- [ ] Head agent designation on Organization — field + UI selector
- [ ] Org-level task intake API — `POST /api/v1/organizations/{id}/tasks` creates Task and triggers head agent
- [ ] LLM-driven delegation by head agent — system prompt injection with direct reports, `delegate_to_{name}` tool execution
- [ ] Sub-task creation on delegation — each `delegate_to_X` creates a child Task linked to parent
- [ ] Hierarchy enforcement — validate delegation target is direct report of delegating agent
- [ ] Two-level delegation — head agent -> worker (prove the pattern at depth=2 before going deeper)
- [ ] Basic result propagation — worker completes task, result flows back to head agent for synthesis

### Add After Validation (v1.x)

Features to add once core two-level delegation is proven.

- [ ] Multi-level delegation (depth > 2) — extend once two-level is solid and context propagation is tested
- [ ] Async parallel delegation — manager delegates to multiple agents simultaneously with fan-out/collect
- [ ] Task status propagation — automatic status rollup when sub-tasks complete
- [ ] Canvas drag-to-reparent — visual hierarchy editing updates `parent_agent_id` in real-time
- [ ] Delegation depth visibility — show full delegation chain in UI (CEO -> VP -> Director -> Engineer)

### Future Consideration (v2+)

Features to defer until the routing pattern is production-proven.

- [ ] Workflow `org_call` node — trigger org routing from workflow DAGs (once API patterns stabilize)
- [ ] Delegation analytics — success rates, time-to-complete by hierarchy level, bottleneck identification
- [ ] Agent capability matching — structured skill taxonomy for smarter delegation (beyond free-text system prompts)
- [ ] Delegation retry/fallback — if delegated agent fails, manager auto-retries with different agent

## Feature Prioritization Matrix

| Feature | User Value | Implementation Cost | Priority |
|---------|------------|---------------------|----------|
| Head agent designation | HIGH | LOW | P1 |
| Org-level task intake API | HIGH | MEDIUM | P1 |
| LLM-driven delegation | HIGH | HIGH | P1 |
| Sub-task creation on delegation | HIGH | MEDIUM | P1 |
| Hierarchy enforcement | HIGH | MEDIUM | P1 |
| Org hierarchy context in prompt | HIGH | LOW | P1 |
| Multi-level delegation chain | HIGH | HIGH | P1 |
| Delegation result aggregation | HIGH | HIGH | P1 |
| Task status propagation | MEDIUM | MEDIUM | P2 |
| Async parallel delegation | MEDIUM | HIGH | P2 |
| Canvas drag-to-reparent | MEDIUM | LOW | P2 |
| Delegation depth visibility (UI) | MEDIUM | LOW | P2 |
| Persistent task lineage (query UI) | MEDIUM | MEDIUM | P2 |
| Delegation analytics | LOW | HIGH | P3 |
| Workflow org_call node | MEDIUM | HIGH | P3 |
| Agent capability matching | LOW | HIGH | P3 |

**Priority key:**
- P1: Must have for launch — core routing loop doesn't work without these
- P2: Should have, add when possible — improves usability and reliability
- P3: Nice to have, future consideration — valuable but routing works without them

## Competitor Feature Analysis

| Feature | CrewAI | AutoGen | OpenAI Swarm | AT (Our Approach) |
|---------|--------|---------|--------------|-------------------|
| Hierarchy definition | Code-defined crew with `manager_agent` | Code-defined group chat with speaker selection | Code-defined routines with `transfer_to_X()` | DB-backed org chart, visual canvas editor |
| Delegation mechanism | Manager LLM allocates tasks to crew | Auto-reply + function-call handoffs | Agent returns another agent object | `delegate_to_{name}` tool calls, recursive agent_call |
| Task persistence | In-memory task objects per run | Conversation history (ephemeral) | None — stateless by design | Full Task records in DB with parent-child lineage |
| Delegation depth | Flat (manager -> workers, 2 levels) | Nested chat allows depth, but complex to configure | Flat handoff chains (no true hierarchy) | Unlimited depth via recursive delegation |
| Async delegation | Sequential by default, async_execution flag exists | Concurrent via group chat patterns | Single-threaded | Fan-out via workflow engine's NodeResultFanOut |
| Runtime reconfiguration | Requires code change + redeploy | Requires code change + redeploy | Requires code change + redeploy | Hot-swap via DB + API, no redeploy |
| Multi-provider support | One manager LLM per crew | Per-agent LLM config possible | Per-agent model possible | Per-agent provider/model, mixed providers natural |
| Visual hierarchy editor | None | None | None | Canvas with drag-to-position, tree visualization |
| Task tracking | Callback-based, no persistence | Conversation logs | None | Org-prefixed identifiers, status tracking, audit log |
| Budget controls | Token usage tracking only | No built-in budget | No built-in budget | Per-agent budgets with pre-call checking |

## Sources

- CrewAI official documentation — crews, hierarchical process, tasks, planning (context7 + docs.crewai.com) — HIGH confidence
- AutoGen documentation — multi-agent conversations, group chat, nested chat (microsoft.github.io/autogen) — HIGH confidence
- OpenAI Cookbook — Orchestrating Agents, Swarm pattern, routines + handoffs (cookbook.openai.com) — HIGH confidence
- AT codebase — `internal/service/at.go`, `agent-call.go`, organization stores, canvas UI (direct code analysis) — HIGH confidence
- AT `.planning/PROJECT.md` — requirements, constraints, key decisions (direct file) — HIGH confidence

---
*Feature research for: Hierarchical agent task routing for LLM gateway*
*Researched: 2026-03-08*
