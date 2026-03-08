# Project Research Summary

**Project:** AT Organization Task Routing — Hierarchical Agent Delegation
**Domain:** Multi-agent orchestration within an existing LLM gateway (brownfield Go monolith)
**Researched:** 2026-03-08
**Confidence:** HIGH

## Executive Summary

AT already possesses every infrastructure primitive needed for hierarchical agent task routing: organizations with agent memberships and `ParentAgentID` hierarchy, tasks with `ParentID` sub-task chains, an `agent_call` workflow node with recursive `delegate_to_{agent}` sub-agent execution, concurrent fan-out via goroutines, per-agent budget enforcement, and audit logging. **This is not a build-from-scratch project — it is a wiring project.** The work connects three existing subsystems (org hierarchy, task management, agent execution) through a thin orchestration layer (~400 lines new, ~35 lines modified) with no new dependencies.

The recommended approach introduces a single new component — an **Org Task Router** as a Go service-layer function (not a workflow node) — that receives tasks via a new `POST /api/v1/organizations/{id}/tasks` endpoint, resolves the head agent, builds delegation tools scoped to direct reports, and recursively invokes agents using the existing `agent_call` machinery. Task records persist at every delegation level, providing full audit trail. The intake API returns 202 immediately; delegation runs asynchronously. The architecture explicitly avoids building a new workflow node, duplicating the agentic loop, or adding external orchestration frameworks.

The primary risks are: (1) **unbounded recursive delegation depth** causing goroutine/cost explosion — mitigated by a configurable depth limit and async task-based delegation rather than synchronous recursion; (2) **conflating workflow-engine delegation with org-level delegation** — mitigated by keeping org routing as a separate code path that doesn't modify `agent_call`; (3) **insufficient LLM context for delegation decisions** — mitigated by injecting direct reports' roles, capabilities, and workload into delegation prompts. The three-backend store multiplier (postgres + sqlite3 + memory) means every schema change costs 3x in implementation effort, so all schema changes should be batched in the first phase.

## Key Findings

### Recommended Stack

**Zero new dependencies.** The entire stack is already in place — Go 1.26, ada HTTP framework, goqu SQL builder, pgx/postgres, modernc/sqlite, ULID IDs, and Svelte 5 UI. No external orchestration frameworks (LangGraph, CrewAI), no message queues (NATS, Kafka), no graph databases (Neo4j), and no task queues (Temporal) are needed. The existing `agent_call` node's recursive delegation mechanism is the right abstraction — it just needs to be wired to the org hierarchy instead of statically configured workflow edges.

**Core technologies (all existing):**
- **Go + ada framework**: HTTP handlers, routing, middleware — all existing patterns apply directly
- **goqu + pgx/modernc-sqlite**: SQL queries across postgres/sqlite3 backends — existing query patterns cover all needed operations
- **agent_call node**: Agentic loop with LLM, tool execution, sub-agent delegation — reused programmatically, not modified
- **Svelte 5 UI**: Canvas already renders org hierarchy — needs head agent picker and task submission form

**Only changes needed:**
- Add `head_agent_id` TEXT field to organizations table (1 migration per backend)
- Add performance indexes on `(organization_id, parent_agent_id)` and `(parent_id, organization_id)`
- Export `newAgentCallNode` → `NewAgentCallNode` (1 line change in agent-call.go)

### Expected Features

**Must have (table stakes — v1):**
- Head agent designation on Organization (field + UI selector)
- Org-level task intake API (`POST /api/v1/organizations/{id}/tasks`)
- LLM-driven delegation by head agent with direct reports as tool calls
- Sub-task creation on delegation (child Task linked via ParentID)
- Hierarchy enforcement (structural — agents can ONLY delegate to direct reports)
- Two-level delegation chain (head → worker, proves the pattern)
- Basic result propagation (worker result flows back to head for synthesis)
- Org hierarchy context in system prompt (direct reports' roles/titles/skills)

**Should have (differentiators — v1.x):**
- Multi-level delegation (depth > 2) with recursive chain
- Async parallel delegation (fan-out to multiple direct reports simultaneously)
- Task status propagation (automatic rollup when sub-tasks complete)
- Canvas drag-to-reparent (visual = structural hierarchy editing)
- Delegation depth visibility in UI (full chain visualization)
- Mixed provider hierarchy (Claude for CEO, GPT-4 for VP, Gemini for worker — already inherent)

**Defer (v2+):**
- Workflow `org_call` node (trigger org routing from DAGs)
- Delegation analytics (success rates, bottlenecks per hierarchy level)
- Agent capability matching (structured skill taxonomy)
- Delegation retry/fallback strategies

**Anti-features (explicitly avoid):**
- Rule-based auto-routing (defeats LLM-driven delegation purpose)
- Cross-org delegation (breaks org boundaries)
- Dynamic agent creation during delegation (security/resource control)
- Dynamic org chart modification during delegation (race conditions)

### Architecture Approach

The architecture adds a service-layer **Org Task Router** that bridges the existing org hierarchy, task system, and agent execution engine. It does NOT create a new workflow node or modify `agent_call`. The Router receives tasks from a new HTTP handler, resolves the head agent's direct reports, constructs scoped `delegate_to_{name}` tools, and runs agents via programmatic `agent_call` invocation. Each delegation step creates a persistent Task record. Hierarchy enforcement is structural: agents literally cannot delegate outside their direct reports because no tool exists for non-reports.

**Major components (4 new, ~400 lines total):**
1. **Org Task Intake Handler** (`server/org-task-routing.go`, ~100 lines) — HTTP endpoint, validation, root task creation, 202 async kickoff
2. **Org Task Router** (`service/org-router.go`, ~250 lines) — resolves hierarchy, builds delegation context, manages recursive delegation lifecycle
3. **Hierarchy Resolver** (pure function, ~50 lines) — filters org agents by parent to find direct reports
4. **Delegation Tool Builder** (pure function, ~30 lines) — constructs `delegate_to_{name}` tools scoped to an agent's direct reports

**Modified components (minimal, ~35 lines):**
- `Organization` struct: add `HeadAgentID` field
- `agent-call.go`: export `NewAgentCallNode`
- Store backends: include `head_agent_id` in queries (3 backends × ~10 lines)
- `server.go`: register new route

### Critical Pitfalls

1. **Unbounded recursive delegation depth** — Each delegation level holds goroutines, MCP connections, and LLM message history. Fan-out across 5+ levels is combinatorial. **Avoid:** Configurable depth limit (default 5-10), async task-based delegation (not synchronous recursion), enforce `RequestDepth` as circuit breaker.

2. **Conflating workflow delegation with org delegation** — `agent_call`'s existing delegation is for static workflow graphs; org delegation is dynamic, LLM-decided, and needs persistent Task records. **Avoid:** Build org delegation as a separate code path. No org-specific code inside `internal/service/workflow/nodes/`.

3. **Synchronous task intake blocking on LLM chains** — A 3-level delegation at 5s per LLM call = 15+ second HTTP request. **Avoid:** Return 202 immediately. Process delegation asynchronously. Poll via `GET /tasks/{id}`.

4. **Invalid hierarchy states** — Head agent removed from org, leaf node designated as head, circular parent references. **Avoid:** `head_agent_id` on Organization (not OrgAgent). Validate hierarchy on every mutation. `ValidateHierarchy()` function with cycle detection.

5. **Three-backend store multiplication (3x tax)** — Every schema change/query hits postgres + sqlite3 + memory. **Avoid:** Batch all schema changes into one migration. Start postgres → sqlite3 → memory. Minimize new tables.

6. **Insufficient delegation context** — LLM sees only agent names and roles, makes superficial routing decisions. **Avoid:** Inject direct reports' roles, titles, skills, system prompt summaries, and current workload into delegation prompt.

## Implications for Roadmap

Based on combined research, the dependency graph, and pitfall-to-phase mapping, the work naturally divides into 4 phases:

### Phase 1: Foundation — Data Model, Hierarchy Validation & API Shell
**Rationale:** All other phases depend on the `head_agent_id` field, the task intake endpoint returning 202, and hierarchy resolution working. Pitfalls #1 (unbounded depth), #4 (invalid hierarchy), #3 (sync intake), and #5 (store 3x tax) must be addressed here — they are architectural decisions that can't be bolted on later.
**Delivers:** Head agent designation (field + migration + all 3 store backends), hierarchy resolver pure function, delegation tool builder pure function, task intake endpoint returning 202 with async goroutine, basic hierarchy validation on mutations, depth limit enforcement.
**Features:** Head agent designation, org-level task intake API (shell), hierarchy enforcement (structural validation)
**Avoids:** Synchronous delegation (async from day 1), invalid hierarchy states (validation on mutations), store parity drift (all 3 backends updated together)

### Phase 2: Core Delegation — LLM-Driven Routing Engine
**Rationale:** This is the heart of the feature. Depends on Phase 1's head agent field and intake endpoint. Pitfall #2 (conflating workflow/org delegation) and #6 (insufficient context) are addressed here. Requires careful implementation of the delegation tool interception mechanism — the trickiest integration point.
**Delivers:** Org Task Router service layer, delegation tool handler (intercepts `delegate_to_*` to create Task records before recursing), system prompt enrichment with org context, two-level delegation working end-to-end (head → worker), result propagation from worker back to head, `NewAgentCallNode` export.
**Features:** LLM-driven delegation, sub-task creation on delegation, org hierarchy context in prompt, basic result propagation, two-level delegation chain
**Avoids:** Duplicating the agentic loop (reuses agent_call), cross-branch delegation (structural enforcement via tool scoping), putting org-specific code in agent_call

### Phase 3: Depth & Concurrency — Multi-Level & Parallel Delegation
**Rationale:** Depends on Phase 2's proven two-level delegation. Extends to N-level recursive chains and parallel fan-out. This is where the delegation pattern proves it scales beyond trivial cases.
**Delivers:** Multi-level delegation (depth > 2), async parallel delegation (manager fans out to multiple reports concurrently), task status propagation (automatic rollup), delegation chain tracking and result aggregation.
**Features:** Multi-level delegation chain, async parallel delegation, task status propagation, delegation result aggregation
**Avoids:** Goroutine explosion (depth limit from Phase 1), unbounded message history (fresh conversation per delegation level)

### Phase 4: UI Integration — Visual Management & UX Polish
**Rationale:** Backend must be solid before investing in UI. Depends on all backend phases. Low risk — mostly CRUD forms and data display using existing Svelte patterns.
**Delivers:** Head agent selector dropdown in org settings, task submission form on org detail page, delegation tree visualization showing full chain with status at each level, canvas drag-to-reparent with hierarchy confirmation dialog.
**Features:** Canvas-driven hierarchy, delegation depth visibility, head agent UI selector, task submission form
**Avoids:** Canvas/hierarchy drift (canvas reads from hierarchy data, drag-drop updates ParentAgentID via API), no-feedback head agent selection (validate and warn at selection time)

### Phase Ordering Rationale

- **Phase 1 before everything:** The async 202 pattern and `head_agent_id` field are load-bearing architectural decisions. Getting them wrong means rewriting later (recovery cost: HIGH per PITFALLS.md). All schema changes batched here to pay the 3x store tax once.
- **Phase 2 is the core value:** Once foundation is in place, delegation logic is the entire product. Two-level depth is enough to validate the pattern and prompt engineering before scaling depth.
- **Phase 3 extends proven patterns:** Multi-level and parallel delegation are extensions of Phase 2's two-level chain. Don't attempt N-level before 2-level works — each additional level amplifies edge cases.
- **Phase 4 last:** UI is additive. The API is fully usable without it. Backend bugs are cheaper to fix before UI is built on top.
- **All phases include store parity:** Every phase that touches the database implements all 3 backends in the same phase. No "skip memory store for now."

### Research Flags

Phases likely needing deeper research during planning:
- **Phase 2:** The delegation tool interception mechanism (how the Router intercepts `delegate_to_*` tool calls to create Task records before recursing) has 3 implementation approaches identified in ARCHITECTURE.md: callback handler injection, Router-managed loop, or new handler type. Needs a spike to determine the right one. Also, prompt engineering for delegation quality needs empirical iteration with real LLMs.
- **Phase 3:** Async parallel delegation with result aggregation. The workflow engine's `NodeResultFanOut` pattern exists but hasn't been used for org-level delegation. Needs validation that goroutine fan-out + WaitGroup + result collection works cleanly with Task record updates.

Phases with standard patterns (skip research-phase):
- **Phase 1:** Standard schema migration + CRUD patterns. Existing codebase has 47 migrations as templates. Hierarchy resolver is a pure function with no external dependencies.
- **Phase 4:** Standard Svelte component patterns. Existing UI already has org detail pages, dropdowns, and canvas rendering. Follow existing patterns.

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH | All technologies directly observed in go.mod and codebase. Zero new dependencies needed. |
| Features | HIGH | Feature set derived from PROJECT.md requirements cross-referenced with competitor analysis (CrewAI, AutoGen, Swarm). Clear MVP definition with dependency graph. |
| Architecture | HIGH | Based on deep analysis of existing code (agent-call.go 768 lines, engine.go 634 lines, at.go 1443 lines). All integration points verified against actual implementations. |
| Pitfalls | HIGH | Grounded in actual codebase constraints (three-backend pattern, synchronous Run(), recursive agent_call). Supported by multi-agent framework literature. |

**Overall confidence: HIGH** — This is a brownfield project where the existing codebase is the primary research source. All findings are verified against actual code, not external documentation or community opinions.

### Gaps to Address

- **Delegation tool interception mechanism:** ARCHITECTURE.md proposes 3 approaches (callback handler, Router-managed loop, new handler type) but doesn't conclusively recommend one. Phase 2 planning should spike this before committing to an implementation.
- **Prompt engineering for delegation quality:** The system prompt template for delegation decisions needs iterative testing with real LLMs. No amount of architecture research can predict whether Claude/GPT-4 will make good delegation decisions with the proposed prompt structure. Plan for prompt iteration in Phase 2.
- **Frontend depth:** STACK.md rates frontend confidence as MEDIUM. The Svelte UI patterns were observed from AGENTS.md descriptions but not deeply audited. Phase 4 planning should review actual component patterns in `_ui/`.
- **Server restart recovery:** PITFALLS.md flags that in-flight delegations are lost on restart. The existing `WakeupRequestStorer` mechanism may handle this, but it hasn't been validated for the org delegation use case. Phase 1 should design for recoverability even if implementation is deferred.
- **Org-level budget enforcement during delegation chains:** Per-agent budget checking exists but delegation chains can exceed org budgets through per-agent checks passing individually. The `BudgetMonthlyCents` field on Organization exists but isn't used as a gate for task creation. Needs design attention in Phase 1.

## Sources

### Primary (HIGH confidence)
- `go.mod` — dependency verification, version confirmation
- `internal/service/at.go` (1443 lines) — all domain types: Organization, OrganizationAgent, Task, Agent structs
- `internal/service/workflow/nodes/agent-call.go` (768 lines) — delegation mechanism, recursive sub-agent invocation, tool handler patterns
- `internal/service/workflow/engine.go` (634 lines) — workflow execution, concurrent fan-out, Registry pattern
- `internal/store/postgres/organizations.go` — store patterns, query patterns, migration conventions
- `internal/server/organizations.go` — HTTP handler patterns, route registration
- `.planning/PROJECT.md` — requirements, constraints, key decisions

### Secondary (MEDIUM confidence)
- CrewAI documentation — hierarchical process, crews, task delegation patterns (competitor analysis)
- AutoGen documentation — multi-agent conversations, group chat, nested chat patterns
- OpenAI Cookbook — Swarm pattern, routines + handoffs, orchestrating agents
- Svelte 5 / `_ui/` — frontend patterns (described in AGENTS.md, not deeply audited)

---
*Research completed: 2026-03-08*
*Ready for roadmap: yes*
