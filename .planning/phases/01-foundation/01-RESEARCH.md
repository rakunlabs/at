# Phase 1: Foundation - Research

**Researched:** 2026-03-08
**Domain:** Go backend — data model extension, hierarchy validation, async HTTP endpoint
**Confidence:** HIGH

## Summary

Phase 1 delivers the foundational data model and async task intake API for organization task routing. The codebase already has most of the pieces: `Organization` struct with issue_prefix/counter fields, `OrganizationAgent` with `ParentAgentID` for hierarchy, `Task` with all needed fields (Identifier, ParentID, RequestDepth, AssignedAgentID). The critical gap is that the organization store CRUD never reads/writes the enhanced fields added in migration 36, and there's zero hierarchy validation on agent mutations.

The implementation requires: (1) a new migration adding `head_agent_id` to organizations, (2) fixing org store CRUD across all 3 backends to read/write enhanced fields, (3) hierarchy validation logic in the server layer for org-agent create/update, (4) a new async task intake endpoint at `POST /api/v1/organizations/{id}/tasks`, and (5) max delegation depth as a constant or config field.

**Primary recommendation:** Follow the established store CRUD pattern exactly — interface in `at.go`, implementation in postgres/sqlite3/memory, handler in server/, route in server.go. No new packages needed. Hierarchy validation belongs in the server handler layer (not the store layer), following the pattern of `AddAgentToOrganizationAPI` which already does membership duplicate-checking before calling the store.

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- **New dedicated endpoint**: `POST /api/v1/organizations/{id}/tasks` — org-scoped, returns 202 Accepted. The existing `POST /api/v1/tasks` remains untouched (201, synchronous).
- **Minimal 202 response**: `{ "id": "01JQ...", "identifier": "PAP-42", "status": "pending" }` — caller polls `GET /api/v1/tasks/{id}` for updates.
- **Fire-and-forget goroutine** for async processing: `go func() { ... }()` after returning 202. No channel, no queue, no worker pool. Lost-on-restart is a Phase 3 concern (CONC-04).
- **Identifier generation in intake handler only**: Calls `IncrementIssueCounter` atomically with task creation to produce `{prefix}-{counter}` identifiers. Existing task creation endpoints do not auto-generate identifiers.

### Claude's Discretion
- **Head agent field design**: How `head_agent_id` is stored (nullable string with `types.Null[string]`), referential integrity across 3 backends, behavior when head agent is removed/deactivated from org.
- **Hierarchy validation rules**: Cycle detection algorithm, orphan branch handling, must-parent-be-in-same-org enforcement, relationship between head agent and tree roots.
- **Max depth configuration**: Where it lives (Organization struct vs config.go vs both), default value, behavior when exceeded.
- **Store CRUD gap**: Migration 36 added `issue_prefix`, `issue_counter`, `budget_monthly_cents`, `spent_monthly_cents`, `budget_reset_at`, `require_board_approval` columns but Go store code never reads/writes them. Whether to fix this broader gap now or only add `head_agent_id` is Claude's call — recommended: fix what's needed for Phase 1 to work (at minimum `issue_prefix`, `issue_counter`, `head_agent_id`).

### Deferred Ideas (OUT OF SCOPE)
None — discussion stayed within phase scope.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| HIER-01 | Organization has a designated head agent field (nullable, one agent per org) | New `HeadAgentID` field on Organization struct + migration 48 adds `head_agent_id` column + store CRUD update across all 3 backends |
| HIER-04 | Hierarchy validation rejects cycles and orphan branches on save | Server-layer validation in `AddAgentToOrganizationAPI` and `UpdateOrganizationAgentAPI` — load all org agents, build adjacency map, DFS for cycles, verify all non-root nodes reachable from roots |
| INTK-01 | POST /api/v1/organizations/{id}/tasks creates a Task assigned to the head agent | New `IntakeTaskAPI` handler on Server, registered at existing org route block in server.go ~line 615 |
| INTK-02 | Task intake returns 202 Accepted immediately with task ID (async processing) | Handler creates task synchronously, returns 202 with minimal JSON, then launches `go func()` for future async work (Phase 2+) |
| INTK-03 | Intake validates org exists, has a head agent, and head agent is active | Handler calls `GetOrganization` → check `HeadAgentID` → `GetOrganizationAgentByPair` → check status == "active" |
| INTK-04 | Created task gets org-scoped identifier (e.g., PAP-42) via existing issue counter | Handler calls `IncrementIssueCounter` → formats `{issue_prefix}-{counter}` → sets `Task.Identifier` before `CreateTask` |
| DELG-06 | Delegation enforces max depth limit (configurable, default 10) to prevent runaway recursion | Add `MaxDelegationDepth` field to Organization struct (default 10) + migration column + store CRUD. Intake handler sets `RequestDepth: 0` on root tasks. Depth check is a Phase 2 concern during actual delegation, but the field/config must exist now. |
</phase_requirements>

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Go stdlib | 1.26 | Language runtime, `net/http`, `encoding/json`, `database/sql` | Project requirement |
| goqu | v9 | SQL query builder | Already used in all postgres + sqlite3 store code |
| oklog/ulid | v2 | ID generation | Already used in all store create methods |
| worldline-go/types | latest | `types.Null[T]` for nullable DB fields | Already used throughout codebase for nullable fields |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| ada framework | latest | HTTP routing, middleware | Already the HTTP framework — register new route |
| rakunlabs/query | latest | Query string parsing for list endpoints | Already used in ListTasks/ListOrganizations |
| log/slog | stdlib | Structured logging | Already the logging standard |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Server-layer validation | Store-layer validation (triggers/constraints) | Server-layer is consistent with existing patterns — membership duplicate check is in `AddAgentToOrganizationAPI`, not in the store. DB constraints are unreliable across memory backend. |
| `head_agent_id` on Organization | Separate table/config | Overkill — single nullable field is simpler and matches existing pattern of direct columns on organizations table |
| `MaxDelegationDepth` on Organization | Global config constant | Per-org config is more flexible and future-proof; adding a column now is cheap |

**No new dependencies needed.** Everything is already in go.mod.

## Architecture Patterns

### Files to Create/Modify

```
internal/service/at.go                              # Add HeadAgentID, MaxDelegationDepth to Organization struct
internal/store/postgres/migrations/48_*.sql          # Add head_agent_id, max_delegation_depth columns
internal/store/sqlite3/migrations/48_*.sql           # Same migration for sqlite
internal/store/postgres/organizations.go             # Fix orgRow, fix CRUD to read/write all enhanced fields
internal/store/sqlite3/organizations.go              # Same fixes for sqlite3
internal/store/memory/organizations.go               # Fix CreateOrganization/UpdateOrganization to copy new fields
internal/server/organization-agents.go               # Add hierarchy validation to create/update handlers
internal/server/task-intake.go                       # New file: IntakeTaskAPI handler
internal/server/server.go                            # Register new route
```

### Pattern 1: Store CRUD Extension (Fix Existing Gap + Add New Fields)

**What:** Extend `orgRow` struct and all CRUD methods to include `issue_prefix`, `issue_counter`, `budget_monthly_cents`, `spent_monthly_cents`, `budget_reset_at`, `require_board_approval_for_new_agents`, `head_agent_id`, and `max_delegation_depth`.

**When to use:** When the Go struct has fields that DB columns support but the store never reads/writes them.

**Postgres orgRow pattern:**
```go
type orgRow struct {
    ID                       string         `db:"id"`
    Name                     string         `db:"name"`
    Description              string         `db:"description"`
    IssuePrefix              string         `db:"issue_prefix"`
    IssueCounter             int64          `db:"issue_counter"`
    BudgetMonthlyCents       int64          `db:"budget_monthly_cents"`
    SpentMonthlyCents        int64          `db:"spent_monthly_cents"`
    BudgetResetAt            sql.NullTime   `db:"budget_reset_at"`
    RequireBoardApproval     bool           `db:"require_board_approval_for_new_agents"`
    HeadAgentID              sql.NullString `db:"head_agent_id"`
    MaxDelegationDepth       int            `db:"max_delegation_depth"`
    CanvasLayout             string         `db:"canvas_layout"`
    CreatedAt                time.Time      `db:"created_at"`
    UpdatedAt                time.Time      `db:"updated_at"`
    CreatedBy                string         `db:"created_by"`
    UpdatedBy                string         `db:"updated_by"`
}
```

**SQLite3 orgRow pattern** (uses `sql.NullString` more liberally):
```go
type orgRow struct {
    ID                       string         `db:"id"`
    Name                     string         `db:"name"`
    Description              sql.NullString `db:"description"`
    IssuePrefix              sql.NullString `db:"issue_prefix"`
    IssueCounter             int64          `db:"issue_counter"`
    BudgetMonthlyCents       int64          `db:"budget_monthly_cents"`
    SpentMonthlyCents        int64          `db:"spent_monthly_cents"`
    BudgetResetAt            sql.NullString `db:"budget_reset_at"`
    RequireBoardApproval     bool           `db:"require_board_approval_for_new_agents"`
    HeadAgentID              sql.NullString `db:"head_agent_id"`
    MaxDelegationDepth       int            `db:"max_delegation_depth"`
    CanvasLayout             sql.NullString `db:"canvas_layout"`
    CreatedAt                string         `db:"created_at"`
    UpdatedAt                string         `db:"updated_at"`
    CreatedBy                sql.NullString `db:"created_by"`
    UpdatedBy                sql.NullString `db:"updated_by"`
}
```

### Pattern 2: Hierarchy Validation (Server-Layer)

**What:** Before creating/updating an org-agent membership with a `parent_agent_id`, validate: (a) parent exists in same org, (b) no cycle would be created, (c) no orphan branches.

**When to use:** In `AddAgentToOrganizationAPI` and `UpdateOrganizationAgentAPI` handlers, BEFORE calling the store.

**Algorithm:**
```go
// validateHierarchy checks that setting parentAgentID for agentID in orgID
// would not create a cycle or orphan branch.
func (s *Server) validateHierarchy(ctx context.Context, orgID, agentID, parentAgentID string) error {
    if parentAgentID == "" {
        return nil // root node, always valid
    }

    // 1. Load all org agents
    agents, err := s.orgAgentStore.ListOrganizationAgents(ctx, orgID)
    if err != nil {
        return fmt.Errorf("load org agents: %w", err)
    }

    // 2. Build adjacency map: agentID -> parentAgentID
    //    Apply the proposed change (agentID -> parentAgentID)
    parentMap := make(map[string]string)
    for _, a := range agents {
        parentMap[a.AgentID] = a.ParentAgentID
    }
    parentMap[agentID] = parentAgentID

    // 3. Check parent exists in org
    found := false
    for _, a := range agents {
        if a.AgentID == parentAgentID {
            found = true
            break
        }
    }
    if !found {
        return fmt.Errorf("parent agent %q not in organization", parentAgentID)
    }

    // 4. Cycle detection: walk from agentID up through parents
    visited := map[string]bool{agentID: true}
    current := parentAgentID
    for current != "" {
        if visited[current] {
            return fmt.Errorf("cycle detected: agent %q", current)
        }
        visited[current] = true
        current = parentMap[current]
    }

    return nil
}
```

### Pattern 3: Async Task Intake Handler

**What:** New handler that creates a task assigned to org's head agent, returns 202 immediately.

**When to use:** `POST /api/v1/organizations/{id}/tasks`

**Example:**
```go
// IntakeTaskAPI handles POST /api/v1/organizations/{id}/tasks.
// Creates a task assigned to the head agent and returns 202 Accepted.
func (s *Server) IntakeTaskAPI(w http.ResponseWriter, r *http.Request) {
    // 1. Validate org exists
    orgID := r.PathValue("id")
    org, err := s.organizationStore.GetOrganization(r.Context(), orgID)
    // ... nil check, error check

    // 2. Validate head agent exists and is active
    if org.HeadAgentID == "" {
        httpResponse(w, "organization has no head agent", http.StatusUnprocessableEntity)
        return
    }
    headMembership, err := s.orgAgentStore.GetOrganizationAgentByPair(r.Context(), orgID, org.HeadAgentID)
    // ... nil check, status == "active" check

    // 3. Generate identifier: IncrementIssueCounter + format
    counter, err := s.organizationStore.IncrementIssueCounter(r.Context(), orgID)
    identifier := fmt.Sprintf("%s-%d", org.IssuePrefix, counter)

    // 4. Create task
    task := service.Task{
        OrganizationID:  orgID,
        AssignedAgentID: org.HeadAgentID,
        Title:           req.Title,
        Description:     req.Description,
        Status:          service.TaskStatusOpen,
        Identifier:      identifier,
        RequestDepth:    0,
        // ...
    }
    record, err := s.taskStore.CreateTask(r.Context(), task)

    // 5. Return 202 with minimal response
    httpResponseJSON(w, map[string]any{
        "id":         record.ID,
        "identifier": record.Identifier,
        "status":     record.Status,
    }, http.StatusAccepted)

    // 6. Fire-and-forget (Phase 2 will add delegation logic here)
    // go func() { /* future: delegation chain */ }()
}
```

### Anti-Patterns to Avoid
- **Store-layer validation:** Don't put hierarchy cycle detection in the store — the memory backend would diverge from SQL backends, and the existing pattern does business logic checks in handlers.
- **Modifying existing CreateTaskAPI:** The locked decision says existing `POST /api/v1/tasks` stays untouched. Don't add identifier generation there.
- **Channel/queue for async:** Locked decision says fire-and-forget goroutine. No channels, no worker pools.
- **New store interfaces:** Don't add a new storer interface when `OrganizationStorer` + `OrganizationAgentStorer` + `TaskStorer` already cover all needed operations.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| ID generation | Custom UUID/snowflake | `ulid.Make().String()` | Already standard in codebase, sortable, collision-resistant |
| SQL query building | Raw SQL strings | `goqu` query builder | Already used everywhere, prevents injection, handles dialect differences |
| Nullable DB fields | Custom nil-checking | `sql.NullString`, `sql.NullTime`, `sql.NullInt64` | Standard Go pattern, already used in orgAgentRow |
| Atomic counter increment | Application-level lock | Existing `IncrementIssueCounter` with DB-level atomicity | Already implemented and tested in postgres (RETURNING) and sqlite3 (UPDATE + SELECT) |
| HTTP response formatting | Custom JSON encoding | `httpResponseJSON(w, obj, code)` | Already the standard helper |

**Key insight:** This phase is almost entirely about wiring together existing patterns and filling a data model gap. The only genuinely new logic is hierarchy cycle detection (a simple DFS walk) and the intake handler (a standard CRUD handler with 202 instead of 201).

## Common Pitfalls

### Pitfall 1: Forgetting to Update All 3 Store Backends
**What goes wrong:** Adding `head_agent_id` to postgres but forgetting sqlite3 or memory. Tests pass locally (memory) but fail in production (postgres).
**Why it happens:** Three backends is unusual — most Go projects have one store.
**How to avoid:** Always modify all three: `postgres/organizations.go`, `sqlite3/organizations.go`, `memory/organizations.go`. Migration SQL in both postgres and sqlite3.
**Warning signs:** Compile errors on one backend, nil fields in API responses.

### Pitfall 2: orgRow Scan Column Count Mismatch
**What goes wrong:** Adding new columns to SELECT but forgetting to add corresponding `Scan` targets, or vice versa. Results in `sql: expected X destination arguments in Scan, not Y`.
**Why it happens:** The SELECT column list and Scan() call are manually synchronized.
**How to avoid:** Count columns in SELECT list, count arguments in Scan(), ensure they match exactly. Use the same variable naming convention as existing code.
**Warning signs:** Runtime panic on any read operation.

### Pitfall 3: SQLite3 Lacks RETURNING Clause
**What goes wrong:** Writing postgres-style `INSERT ... RETURNING` for sqlite3.
**Why it happens:** Postgres and sqlite have different SQL capabilities.
**How to avoid:** Follow existing pattern — sqlite3's `IncrementIssueCounter` does UPDATE then separate SELECT. For new migrations, use `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` pattern (works in both).
**Warning signs:** SQL syntax error on sqlite3 backend.

### Pitfall 4: Empty String vs NULL for Optional Fields
**What goes wrong:** Storing empty string `""` when NULL is expected, or vice versa.
**Why it happens:** Go's zero value for string is `""`, but DB columns default to NULL.
**How to avoid:** Use `nullString()` helper (already exists in postgres) for INSERT/UPDATE of optional fields. Use `sql.NullString` in row structs for SELECT.
**Warning signs:** Inconsistent API responses (`""` vs `null` vs absent).

### Pitfall 5: Race Condition in Counter + Task Creation
**What goes wrong:** Two concurrent intake requests get the same counter value.
**Why it happens:** `IncrementIssueCounter` and `CreateTask` are separate DB calls.
**How to avoid:** `IncrementIssueCounter` is already atomic (UPDATE ... SET counter = counter + 1). Each call gets a unique value. The only risk is if the task creation fails after counter increment — the counter "leaks" a number. This is acceptable (same as GitHub issue numbers).
**Warning signs:** Duplicate identifiers in tasks table.

### Pitfall 6: Hierarchy Validation Loading All Agents
**What goes wrong:** For very large orgs, loading all agents to validate hierarchy is slow.
**Why it happens:** The simple approach loads all org members to build the parent map.
**How to avoid:** For Phase 1, this is acceptable — org sizes are typically < 100 agents. If needed later, can optimize with a single recursive CTE query. Don't prematurely optimize.
**Warning signs:** Slow response times on org-agent create/update for large orgs.

## Code Examples

### Migration 48: Add head_agent_id and max_delegation_depth

**Postgres (`48_add_head_agent_and_delegation_depth.sql`):**
```sql
ALTER TABLE ${TABLE_PREFIX}organizations ADD COLUMN IF NOT EXISTS head_agent_id TEXT DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}organizations ADD COLUMN IF NOT EXISTS max_delegation_depth INTEGER DEFAULT 10;
```

**SQLite3 (`48_add_head_agent_and_delegation_depth.sql`):**
```sql
ALTER TABLE ${TABLE_PREFIX}organizations ADD COLUMN head_agent_id TEXT DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}organizations ADD COLUMN max_delegation_depth INTEGER DEFAULT 10;
```

Note: SQLite3 doesn't support `IF NOT EXISTS` on `ALTER TABLE ADD COLUMN`. The migration runner handles idempotency.

### Organization Struct Update (at.go)

```go
type Organization struct {
    ID                   string          `json:"id"`
    Name                 string          `json:"name"`
    Description          string          `json:"description"`
    IssuePrefix          string          `json:"issue_prefix,omitempty"`
    IssueCounter         int64           `json:"issue_counter,omitempty"`
    BudgetMonthlyCents   int64           `json:"budget_monthly_cents,omitempty"`
    SpentMonthlyCents    int64           `json:"spent_monthly_cents,omitempty"`
    BudgetResetAt        string          `json:"budget_reset_at,omitempty"`
    RequireBoardApproval bool            `json:"require_board_approval_for_new_agents"`
    HeadAgentID          string          `json:"head_agent_id,omitempty"`
    MaxDelegationDepth   int             `json:"max_delegation_depth,omitempty"`
    CanvasLayout         json.RawMessage `json:"canvas_layout,omitempty"`
    CreatedAt            string          `json:"created_at"`
    UpdatedAt            string          `json:"updated_at"`
    CreatedBy            string          `json:"created_by"`
    UpdatedBy            string          `json:"updated_by"`
}
```

### Route Registration (server.go)

```go
// After existing organization routes (~line 615):
apiGroup.POST("/v1/organizations/{id}/tasks", s.IntakeTaskAPI)
```

### Intake Task Request Body

```go
type intakeTaskRequest struct {
    Title       string `json:"title"`
    Description string `json:"description"`
    GoalID      string `json:"goal_id,omitempty"`
    Priority    string `json:"priority_level,omitempty"`
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| orgRow has 8 fields | orgRow needs 14+ fields | Migration 36 (budget/issue fields) was never reflected in Go code | Must fix now — read/write gap |
| No hierarchy validation | Phase 1 adds validation | New in this phase | Prevents invalid org charts |
| Sync task creation only | Async intake (202) added | New in this phase | Enables future delegation chain |

**Existing but unused:**
- `IncrementIssueCounter`: Fully implemented in all 3 backends, never called from any handler. Phase 1 wires it up.
- `ParentAgentID` on OrganizationAgent: Column and field exist, zero validation. Phase 1 adds validation.
- `RequestDepth` on Task: Field exists, never used. Phase 1 sets it to 0 on intake tasks.

## Open Questions

1. **Should `head_agent_id` be validated on org update?**
   - What we know: When updating an org to set head_agent_id, should we verify the agent is a member?
   - What's unclear: The existing UpdateOrganizationAPI doesn't validate any references
   - Recommendation: Yes, validate — check agent is in org and active. Add to UpdateOrganizationAPI handler, not store.

2. **What happens when head agent is removed from org?**
   - What we know: `DeleteOrganizationAgentByPair` has no cascade logic
   - What's unclear: Should removing the head agent clear the org's `head_agent_id`?
   - Recommendation: Don't auto-clear for now. Intake validation (INTK-03) will catch the broken reference. A future phase can add cascade cleanup.

3. **Migration numbering**
   - What we know: Latest is 47 (`add_organization_canvas_layout`). Next is 48.
   - What's unclear: Whether other pending work might claim 48
   - Recommendation: Use 48 — we're the next in line.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go standard `testing` package |
| Config file | None (standard `go test`) |
| Quick run command | `go test -v -race ./internal/server/... ./internal/store/...` |
| Full suite command | `make test` (`go test -v -race ./...`) |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| HIER-01 | Organization has head_agent_id field | unit | `go test -v -race -run TestOrgHeadAgent ./internal/store/memory/...` | ❌ Wave 0 |
| HIER-04 | Hierarchy rejects cycles/orphans | unit | `go test -v -race -run TestHierarchyValidation ./internal/server/...` | ❌ Wave 0 |
| INTK-01 | Intake creates task assigned to head agent | unit | `go test -v -race -run TestIntakeTask ./internal/server/...` | ❌ Wave 0 |
| INTK-02 | Intake returns 202 Accepted | unit | `go test -v -race -run TestIntakeReturns202 ./internal/server/...` | ❌ Wave 0 |
| INTK-03 | Intake validates org/head-agent/active | unit | `go test -v -race -run TestIntakeValidation ./internal/server/...` | ❌ Wave 0 |
| INTK-04 | Task gets org-scoped identifier | unit | `go test -v -race -run TestIntakeIdentifier ./internal/server/...` | ❌ Wave 0 |
| DELG-06 | Max depth field exists with default 10 | unit | `go test -v -race -run TestOrgMaxDepth ./internal/store/memory/...` | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `go test -v -race ./internal/server/... ./internal/store/memory/...`
- **Per wave merge:** `make test`
- **Phase gate:** Full suite green before `/gsd-verify-work`

### Wave 0 Gaps
- [ ] `internal/server/task_intake_test.go` — covers INTK-01, INTK-02, INTK-03, INTK-04
- [ ] `internal/server/hierarchy_validation_test.go` — covers HIER-04
- [ ] `internal/store/memory/organizations_test.go` — covers HIER-01, DELG-06
- [ ] Test helpers: memory store setup function for creating org + agents + tasks in tests

Note: The existing codebase has only 6 test files total. The test infrastructure is minimal — standard `testing` package, no test helpers or fixtures. Tests will need to construct memory stores directly since there's no test harness.

## Sources

### Primary (HIGH confidence)
- `internal/service/at.go` — Organization struct (line 505-520), OrganizationAgent struct (line 540-550), Task struct (line 642-668), all store interfaces
- `internal/store/postgres/organizations.go` — orgRow gap confirmed (8 fields vs 14+ needed)
- `internal/store/sqlite3/organizations.go` — same gap confirmed
- `internal/store/memory/organizations.go` — IncrementIssueCounter works on in-memory struct
- `internal/store/postgres/organization-agents.go` — orgAgentRow includes ParentAgentID, no validation
- `internal/server/organization-agents.go` — handlers with duplicate-check pattern but no hierarchy validation
- `internal/server/tasks.go` — CreateTaskAPI pattern (decode → validate → store → respond)
- `internal/server/server.go` — route registration at lines 604-633, store fields at lines 119-168
- `internal/store/postgres/migrations/36_enhance_organizations.sql` — columns that store code never reads
- `internal/store/postgres/migrations/47_add_organization_canvas_layout.sql` — latest migration number

### Secondary (MEDIUM confidence)
- `AGENTS.md` (project root) — architecture overview, conventions, build commands
- `internal/server/AGENTS.md` — route map, auth model, patterns
- `internal/store/AGENTS.md` — store factory, backend descriptions, encryption

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - all libraries already in use, no new dependencies needed
- Architecture: HIGH - patterns directly observed in existing code, all 3 backends inspected
- Pitfalls: HIGH - identified from actual code gaps (orgRow mismatch, scan count, sqlite limitations)
- Validation: HIGH - test framework confirmed (standard `testing`), gaps clearly identified

**Research date:** 2026-03-08
**Valid until:** 2026-04-08 (stable Go codebase, no fast-moving dependencies)
