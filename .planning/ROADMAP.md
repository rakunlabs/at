# Roadmap: AT Organization Task Routing

## Overview

This roadmap delivers hierarchical agent task routing for the AT LLM gateway — connecting the existing org hierarchy, task system, and agent execution engine through a thin orchestration layer. The work progresses from data model foundation through core LLM-driven delegation, then scales to multi-level parallel execution, and finally delivers the UI for visual management. Each phase builds on the previous, with backend capability proven before UI investment.

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3, 4): Planned milestone work
- Decimal phases (e.g., 2.1): Urgent insertions (marked with INSERTED)

- [x] **Phase 1: Foundation** - Data model, hierarchy validation, and async task intake API
- [x] **Phase 2: Core Delegation** - LLM-driven routing from head agent to direct reports with task tracking (completed 2026-03-08)
- [ ] **Phase 3: Depth & Concurrency** - Multi-level delegation chains, parallel fan-out, and status propagation
- [ ] **Phase 4: UI Integration** - Head agent selector, task submission form, canvas reparenting, and delegation tree view

## Phase Details

### Phase 1: Foundation
**Goal**: Organization has a head agent field, hierarchy is validated, and tasks can be submitted via API with async processing
**Depends on**: Nothing (first phase)
**Requirements**: HIER-01, HIER-04, INTK-01, INTK-02, INTK-03, INTK-04, DELG-06
**Success Criteria** (what must be TRUE):
  1. Organization record stores a head_agent_id field, persisted across all three store backends (postgres, sqlite3, memory)
  2. POST /api/v1/organizations/{id}/tasks returns 202 Accepted with a task ID when org has an active head agent
  3. Task intake rejects requests when org has no head agent or head agent is inactive, returning appropriate errors
  4. Created task receives an org-scoped identifier (e.g., PAP-42) using the existing issue counter
  5. Hierarchy validation rejects cycles and orphan branches when organization agents are mutated
**Plans:** 2 plans

Plans:
- [x] 01-01-PLAN.md — Data model extension: HeadAgentID + MaxDelegationDepth on Organization, migration 48, fix store CRUD gap across all 3 backends
- [x] 01-02-PLAN.md — Hierarchy validation (cycle/orphan detection) + async task intake endpoint (POST /api/v1/organizations/{id}/tasks → 202 Accepted)

### Phase 2: Core Delegation
**Goal**: Head agent receives a submitted task, uses LLM judgment to delegate to a direct report, and the delegation creates a tracked child task — proving the two-level delegation pattern end-to-end
**Depends on**: Phase 1
**Requirements**: HIER-03, HIER-05, DELG-01, DELG-02, DELG-03, DELG-05, CONC-03
**Success Criteria** (what must be TRUE):
  1. Head agent's LLM call receives system prompt enriched with direct reports' names, roles, titles, and descriptions
  2. Head agent delegates to a direct report via delegate_to_{name} tool call, creating a child Task record linked via parent_task_id
  3. Delegated agent runs its own agentic loop (reusing agent_call pattern) and produces a result
  4. Delegation tools presented to any agent are restricted to only that agent's direct reports — no cross-branch delegation possible
  5. Budget is checked before each agent's LLM call in the delegation chain
**Plans:** 2/2 plans complete

Plans:
- [x] 02-01-PLAN.md — Core org delegation engine: runOrgDelegation with agentic loop, delegate tool generation from direct reports, system prompt enrichment, child task creation, recursive delegation, budget checking
- [x] 02-02-PLAN.md — Wire task intake → async delegation goroutine + unit tests for getDirectReports, createDelegationTask, tool name sanitization

### Phase 3: Depth & Concurrency
**Goal**: Delegation chains extend to unlimited depth with parallel fan-out, and task status propagates automatically through the hierarchy
**Depends on**: Phase 2
**Requirements**: DELG-04, CONC-01, CONC-02, CONC-04, STAT-01, STAT-02, STAT-03, STAT-04
**Success Criteria** (what must be TRUE):
  1. A manager agent can delegate to multiple sub-agents simultaneously, with all delegations running as concurrent goroutines
  2. Delegation chain works at 3+ levels deep (head → VP → director → worker), creating Task records at each level
  3. When all child tasks of a parent complete, the parent task is automatically marked complete
  4. Task failure at any level is recorded and the parent agent receives the failure information
  5. GET /api/v1/tasks/{id} returns the task with its full sub-task tree showing status at every level
**Plans:** 1/2 plans executed

Plans:
- [x] 03-01-PLAN.md — Store infrastructure (ListChildTasks + UpdateTaskStatus across 3 backends), status propagation (parent auto-complete/fail), sub-task tree API (GET ?include=subtasks)
- [ ] 03-02-PLAN.md — Concurrent fan-out (WaitGroup + Mutex replacing sequential tool call loop), deep delegation chain verification + tests

### Phase 4: UI Integration
**Goal**: Users can manage head agents, submit tasks, visualize delegation chains, and edit hierarchy through the Svelte admin UI
**Depends on**: Phase 1, Phase 2, Phase 3
**Requirements**: HIER-02, UI-01, UI-02, UI-03, UI-04
**Success Criteria** (what must be TRUE):
  1. Organization edit/create form includes a head agent dropdown populated from the org's agents
  2. Organization detail page has a "Submit Task" form that calls the intake API and shows the returned task ID
  3. Canvas drag-to-reparent changes an agent's parent_agent_id via the existing API, updating the hierarchy
  4. Task detail page shows the full delegation chain as a parent → child tree with status at each node
**Plans**: TBD

Plans:
- [ ] 04-01: TBD
- [ ] 04-02: TBD

## Progress

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Foundation | 2/2 | Complete | 2026-03-08 |
| 2. Core Delegation | 2/2 | Complete   | 2026-03-08 |
| 3. Depth & Concurrency | 1/2 | In Progress|  |
| 4. UI Integration | 0/? | Not started | - |
