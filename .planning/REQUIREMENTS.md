# Requirements: AT Organization Task Routing

**Defined:** 2026-03-08
**Core Value:** Tasks submitted to an organization are intelligently routed through the agent hierarchy via LLM-driven delegation, with full task tracking at every level.

## v1 Requirements

Requirements for initial release. Each maps to roadmap phases.

### Hierarchy

- [x] **HIER-01**: Organization has a designated head agent field (nullable, one agent per org)
- [ ] **HIER-02**: User can select head agent from org's existing agents via UI dropdown
- [ ] **HIER-03**: Agents can only delegate to their direct reports (children in parent_agent_id tree)
- [ ] **HIER-04**: Hierarchy validation rejects cycles and orphan branches on save
- [ ] **HIER-05**: Delegating agent's system prompt is enriched with direct reports' roles, titles, and descriptions

### Task Intake

- [ ] **INTK-01**: POST /api/v1/organizations/{id}/tasks creates a Task assigned to the head agent
- [ ] **INTK-02**: Task intake returns 202 Accepted immediately with task ID (async processing)
- [ ] **INTK-03**: Intake validates org exists, has a head agent, and head agent is active
- [ ] **INTK-04**: Created task gets org-scoped identifier (e.g., PAP-42) via existing issue counter

### Delegation

- [ ] **DELG-01**: Head agent receives task and uses LLM to decide which direct report handles it
- [ ] **DELG-02**: Each delegation creates a child Task record linked via parent_task_id
- [ ] **DELG-03**: Delegated agent runs its own agentic loop (agent_call pattern) to handle the sub-task
- [ ] **DELG-04**: Delegation chain supports unlimited depth (head -> VP -> director -> manager -> worker)
- [ ] **DELG-05**: Each level's delegation tools are restricted to that agent's direct reports only
- [x] **DELG-06**: Delegation enforces max depth limit (configurable, default 10) to prevent runaway recursion

### Concurrency

- [ ] **CONC-01**: Manager can delegate to multiple sub-agents simultaneously (async fan-out)
- [ ] **CONC-02**: Results from parallel sub-agents are collected and returned to the delegating agent
- [ ] **CONC-03**: Budget is checked before each agent's LLM call in the delegation chain
- [ ] **CONC-04**: Delegation runs in background goroutines, not blocking the HTTP request

### Status

- [ ] **STAT-01**: When a leaf task completes, parent task status is updated to reflect progress
- [ ] **STAT-02**: When all child tasks of a parent complete, parent task is marked complete
- [ ] **STAT-03**: Task failure at any level is recorded and propagated to the parent agent
- [ ] **STAT-04**: GET /api/v1/tasks/{id} returns the task with its full sub-task tree

### UI

- [ ] **UI-01**: Organization edit/create form has head agent dropdown selector
- [ ] **UI-02**: Organization detail page has a "Submit Task" form that calls the intake API
- [ ] **UI-03**: Canvas drag-to-reparent updates parent_agent_id via existing API
- [ ] **UI-04**: Task detail shows delegation chain (parent -> child tree visualization)

## v2 Requirements

Deferred to future release. Tracked but not in current roadmap.

### Workflow Integration

- **WKFL-01**: Workflow node type (org_call) that sends a task to an org's head agent
- **WKFL-02**: org_call node waits for task completion or returns task ID for polling

### Analytics & Visibility

- **ANLY-01**: Delegation success rate per agent (how often their delegations complete vs fail)
- **ANLY-02**: Time-to-complete by hierarchy level (identify bottleneck levels)
- **ANLY-03**: Full delegation tree visualization with timing and cost per node

### Advanced Delegation

- **ADVD-01**: Delegation retry/fallback -- if delegated agent fails, manager re-delegates to alternate
- **ADVD-02**: Agent capability matching -- structured skill taxonomy for smarter delegation
- **ADVD-03**: Delegation templates -- pre-configured delegation patterns for common task types

## Out of Scope

| Feature | Reason |
|---------|--------|
| Rule-based auto-routing | Defeats LLM-driven delegation purpose; creates brittle mappings |
| Automatic escalation chains | Creates infinite loops, unclear ownership; LLM judgment preferred |
| Budget rollup across hierarchy | Complex aggregation with race conditions; per-agent budgets sufficient |
| Real-time streaming of delegation progress | WebSocket complexity; polling task status sufficient for v1 |
| Dynamic agent creation during delegation | Uncontrolled resource creation; use existing approval system |
| Cross-org delegation | Breaks org boundaries and permission model |
| Dynamic org chart modification during delegation | Race conditions with in-flight delegation chains |

## Traceability

Which phases cover which requirements. Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| HIER-01 | Phase 1 | Complete |
| HIER-02 | Phase 4 | Pending |
| HIER-03 | Phase 2 | Pending |
| HIER-04 | Phase 1 | Pending |
| HIER-05 | Phase 2 | Pending |
| INTK-01 | Phase 1 | Pending |
| INTK-02 | Phase 1 | Pending |
| INTK-03 | Phase 1 | Pending |
| INTK-04 | Phase 1 | Pending |
| DELG-01 | Phase 2 | Pending |
| DELG-02 | Phase 2 | Pending |
| DELG-03 | Phase 2 | Pending |
| DELG-04 | Phase 3 | Pending |
| DELG-05 | Phase 2 | Pending |
| DELG-06 | Phase 1 | Complete |
| CONC-01 | Phase 3 | Pending |
| CONC-02 | Phase 3 | Pending |
| CONC-03 | Phase 2 | Pending |
| CONC-04 | Phase 3 | Pending |
| STAT-01 | Phase 3 | Pending |
| STAT-02 | Phase 3 | Pending |
| STAT-03 | Phase 3 | Pending |
| STAT-04 | Phase 3 | Pending |
| UI-01 | Phase 4 | Pending |
| UI-02 | Phase 4 | Pending |
| UI-03 | Phase 4 | Pending |
| UI-04 | Phase 4 | Pending |

**Coverage:**
- v1 requirements: 27 total
- Mapped to phases: 27
- Unmapped: 0

---
*Requirements defined: 2026-03-08*
*Last updated: 2026-03-08 after initial definition*
