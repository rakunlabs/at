---
phase: 01-foundation
plan: 02
subsystem: api
tags: [go, http, hierarchy, cycle-detection, task-intake, async]

# Dependency graph
requires:
  - phase: 01-foundation plan 01
    provides: "Organization struct with HeadAgentID, MaxDelegationDepth, IssuePrefix, IssueCounter fields"
provides:
  - "validateHierarchy method with cycle detection and parent-in-org validation"
  - "IntakeTaskAPI handler for POST /api/v1/organizations/{id}/tasks"
  - "Head agent validation on organization update"
  - "15 new tests covering hierarchy validation and task intake"
affects: [02-core-delegation, 04-ui-integration]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Cycle detection via parent-map walk: build map, apply proposed change, walk ancestors checking visited set"
    - "Task intake returns 202 Accepted with minimal {id, identifier, status} response"
    - "Org-scoped identifier via atomic IncrementIssueCounter"
    - "Store nil checks at handler entry for graceful degradation"

key-files:
  created:
    - "internal/server/task-intake.go"
    - "internal/server/organization-agents_test.go"
    - "internal/server/task-intake_test.go"
  modified:
    - "internal/server/organization-agents.go"
    - "internal/server/server.go"
    - "internal/server/organizations.go"

key-decisions:
  - "Cycle detection walks parent chain from proposed agent upward — O(depth) per validation, sufficient for typical org hierarchies"
  - "Task intake returns 202 (not 201) to signal async processing intent for Phase 2 delegation"
  - "Head agent validation on org update checks both membership and active status"

patterns-established:
  - "HTTP handler tests use mock stores implementing full interface with stubs for unused methods"
  - "TDD RED-GREEN with separate test files per handler group"
  - "Hierarchy validation as reusable method called from both create and update paths"

requirements-completed: [HIER-04, INTK-01, INTK-02, INTK-03, INTK-04]

# Metrics
duration: 5min
completed: 2026-03-08
---

# Phase 1 Plan 02: Hierarchy Validation & Task Intake Summary

**Cycle-detecting hierarchy validation on org-agent mutations plus async task intake endpoint returning 202 with org-scoped identifiers via IncrementIssueCounter**

## Performance

- **Duration:** 5 min
- **Started:** 2026-03-08T20:54:34Z
- **Completed:** 2026-03-08T20:59:42Z
- **Tasks:** 2 (each with TDD RED→GREEN)
- **Files modified:** 6

## Accomplishments
- Added `validateHierarchy` method to Server with cycle detection (self-reference, direct A→B→A, deeper A→C→B→A) and parent-in-org validation
- Hooked hierarchy validation into both `AddAgentToOrganizationAPI` and `UpdateOrganizationAgentAPI`
- Created `IntakeTaskAPI` handler with full validation chain: org exists → head agent set → head agent active
- Registered `POST /api/v1/organizations/{id}/tasks` route returning 202 Accepted
- Added head_agent_id validation to `UpdateOrganizationAPI` (checks membership + active status)
- Created 15 new tests across 2 test files with mock stores

## Task Commits

Each task was committed atomically:

1. **Task 1 (RED): Add failing tests for hierarchy validation** - `d1212e6` (test)
2. **Task 1 (GREEN): Add hierarchy validation to org-agent handlers** - `2e9e2c3` (feat)
3. **Task 2 (RED): Add failing tests for task intake endpoint** - `8953229` (test)
4. **Task 2 (GREEN): Create intake endpoint, register route, add head_agent_id validation** - `5417d1b` (feat)

_TDD tasks had RED → GREEN commits. REFACTOR skipped (code was clean)._

## Files Created/Modified
- `internal/server/task-intake.go` — New IntakeTaskAPI handler with org/head-agent validation and identifier generation
- `internal/server/organization-agents_test.go` — 9 tests for hierarchy validation (unit + handler integration)
- `internal/server/task-intake_test.go` — 6 tests for intake endpoint (202, 404, 422 cases, identifier format)
- `internal/server/organization-agents.go` — Added validateHierarchy method + hooks in create/update handlers
- `internal/server/server.go` — Registered POST /v1/organizations/{id}/tasks route
- `internal/server/organizations.go` — Added head_agent_id validation + partial update preservation

## Decisions Made
- Cycle detection walks parent chain from proposed agent upward — O(depth) per validation, sufficient for typical org hierarchies
- Task intake returns 202 (not 201) to signal async processing intent for Phase 2 delegation
- Head agent validation on org update checks both membership and active status
- Fixed deeper cycle test to correctly model A→C→B→A (set A's parent to C, not C's parent to A)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed deeper cycle test case**
- **Found during:** Task 1 (Hierarchy validation GREEN phase)
- **Issue:** Plan's test for "A→B→C→A cycle" described setting C's parent to A, but that doesn't create a cycle (A is root). The actual cycle requires setting A's parent to C.
- **Fix:** Changed test to set A's parent to C (creating A→C→B→A cycle)
- **Files modified:** internal/server/organization-agents_test.go
- **Verification:** Test correctly detects cycle after fix
- **Committed in:** 2e9e2c3 (Task 1 GREEN commit)

---

**Total deviations:** 1 auto-fixed (1 bug in test specification)
**Impact on plan:** Minor test correction, no architectural change. All behaviors work as specified.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness
- Phase 1 complete: organization data model, hierarchy validation, and task intake all working
- Ready for Phase 2 (Core Delegation): IntakeTaskAPI placeholder comment marks where `delegateTask` goroutine will be added
- Foundation is solid: 15 new tests + existing tests all pass, `go build ./...` and `make test` clean

---
*Phase: 01-foundation*
*Completed: 2026-03-08*

## Self-Check: PASSED

All 7 key files verified present on disk. All 4 task commits verified in git history.
