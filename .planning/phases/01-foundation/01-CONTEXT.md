# Phase 1: Foundation - Context

**Gathered:** 2026-03-08
**Status:** Ready for planning

<domain>
## Phase Boundary

Phase 1 delivers the data model foundation and async task intake API for organization task routing. Specifically: a `head_agent_id` field on organizations, hierarchy validation (cycle/orphan detection) on agent mutations, an async task intake endpoint that returns 202 Accepted with an org-scoped identifier, and a configurable max delegation depth limit.

Requirements: HIER-01, HIER-04, INTK-01, INTK-02, INTK-03, INTK-04, DELG-06

</domain>

<decisions>
## Implementation Decisions

### Async Task Intake Contract
- **New dedicated endpoint**: `POST /api/v1/organizations/{id}/tasks` — org-scoped, returns 202 Accepted. The existing `POST /api/v1/tasks` remains untouched (201, synchronous).
- **Minimal 202 response**: `{ "id": "01JQ...", "identifier": "PAP-42", "status": "pending" }` — caller polls `GET /api/v1/tasks/{id}` for updates.
- **Fire-and-forget goroutine** for async processing: `go func() { ... }()` after returning 202. No channel, no queue, no worker pool. Lost-on-restart is a Phase 3 concern (CONC-04).
- **Identifier generation in intake handler only**: Calls `IncrementIssueCounter` atomically with task creation to produce `{prefix}-{counter}` identifiers. Existing task creation endpoints do not auto-generate identifiers.

### Claude's Discretion
The following areas were not discussed — Claude has flexibility to choose the best approach based on codebase patterns:

- **Head agent field design**: How `head_agent_id` is stored (nullable string with `types.Null[string]`), referential integrity across 3 backends, behavior when head agent is removed/deactivated from org.
- **Hierarchy validation rules**: Cycle detection algorithm, orphan branch handling, must-parent-be-in-same-org enforcement, relationship between head agent and tree roots.
- **Max depth configuration**: Where it lives (Organization struct vs config.go vs both), default value, behavior when exceeded.
- **Store CRUD gap**: Migration 36 added `issue_prefix`, `issue_counter`, `budget_monthly_cents`, `spent_monthly_cents`, `budget_reset_at`, `require_board_approval` columns but Go store code never reads/writes them. Whether to fix this broader gap now or only add `head_agent_id` is Claude's call — recommended: fix what's needed for Phase 1 to work (at minimum `issue_prefix`, `issue_counter`, `head_agent_id`).

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- **Organization struct** (`internal/service/at.go`): Exists with name, description, issue_prefix, budget fields but NO `HeadAgentID` field yet — needs migration + struct update
- **OrganizationAgent struct** (`internal/service/at.go`): Has `ParentAgentID` (nullable string) for hierarchy — zero validation currently
- **IncrementIssueCounter** (`internal/store/postgres/organizations.go`, `sqlite3/organizations.go`): Raw SQL that atomically increments counter and returns new value — works, just never called from Go handlers
- **Task struct** (`internal/service/at.go`): Has `ParentTaskID`, `Identifier`, `RequestDepth`, `Status`, `OrganizationID` — all fields needed for intake exist
- **CreateTask store methods**: Exist across all 3 backends — can be called from the new intake handler
- **httpResponse/httpResponseJSON helpers** (`internal/server/response.go`): Standard HTTP response helpers

### Established Patterns
- **Store CRUD**: Each resource has a storer interface in `at.go`, implementations in postgres/sqlite3/memory, handler in server/, routes in server.go
- **Migration**: Sequential numbered SQL files in both postgres and sqlite3 `migrations/` directories (currently at 47)
- **Handler pattern**: Method on `*Server`, reads request body, validates, calls store, returns JSON response
- **Nullable fields**: `types.Null[T]` from `worldline-go/types` for optional DB columns
- **ID generation**: ULID via `oklog/ulid/v2`

### Integration Points
- **Route registration**: Organization routes at `internal/server/server.go` ~lines 604-615 — new endpoint registers here
- **Server constructor**: New storer injected via `NewServer` parameters (if needed, though existing `organizationStore` + `taskStore` may suffice)
- **Store factory**: `internal/store/store.go` — no changes needed (interfaces already composed)
- **Organization-agents mutations**: `internal/server/organization-agents.go` — hierarchy validation hooks into create/update handlers here

</code_context>

<specifics>
## Specific Ideas

No specific requirements — open to standard approaches. The user chose all recommended options for task intake and deferred the remaining gray areas to Claude's discretion.

</specifics>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope.

</deferred>

---

*Phase: 01-foundation*
*Context gathered: 2026-03-08*
