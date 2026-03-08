---
phase: 03-depth-concurrency
plan: 01
subsystem: api, database
tags: [goqu, sqlite3, postgres, memory, task-store, status-propagation, sub-task-tree]

# Dependency graph
requires:
  - phase: 02-core-delegation
    provides: "TaskStorer interface, task creation/update, runOrgDelegation engine"
provides:
  - "ListChildTasks store method across 3 backends"
  - "UpdateTaskStatus store method (race-safe, no field clobbering)"
  - "propagateStatusToParent helper for automatic parent status updates"
  - "completeTaskWithStatus helper reducing repetitive status+propagate calls"
  - "TaskWithSubtasks type + buildTaskTree recursive tree builder"
  - "GET /api/v1/tasks/{id}?include=subtasks returns recursive sub-task tree"
affects: [03-depth-concurrency, 04-ui-integration]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "UpdateTaskStatus for atomic status+result updates without field clobbering"
    - "propagateStatusToParent pattern: list children → check all done → update parent"
    - "buildTaskTree recursive tree builder with maxDepth safety bound"
    - "?include=subtasks query param for optional tree expansion on GET"

key-files:
  created: []
  modified:
    - internal/service/at.go
    - internal/store/memory/tasks.go
    - internal/store/postgres/tasks.go
    - internal/store/sqlite3/tasks.go
    - internal/server/org-delegation.go
    - internal/server/tasks.go
    - internal/server/org-delegation_test.go
    - internal/server/task-intake_test.go

key-decisions:
  - "UpdateTaskStatus only touches status, result, and updated_at — prevents field clobbering in concurrent scenarios"
  - "propagateStatusToParent is fire-and-forget with slog.Warn on errors — non-blocking for the completing task"
  - "buildTaskTree maxDepth defaults to 20 — safe upper bound preventing runaway recursion on malformed data"
  - "Refactored all runOrgDelegation UpdateTask calls to UpdateTaskStatus for consistency"

patterns-established:
  - "UpdateTaskStatus pattern: atomic status mutation without reading/overwriting other fields"
  - "completeTaskWithStatus helper: status update + parent propagation in one call"
  - "Query param expansion: ?include=subtasks for optional rich response on existing endpoint"

requirements-completed: [STAT-01, STAT-02, STAT-03, STAT-04]

# Metrics
duration: 4min
completed: 2026-03-08
---

# Phase 3 Plan 01: Store Infrastructure & Status Propagation Summary

**ListChildTasks + UpdateTaskStatus across 3 backends, automatic parent status propagation on child completion/failure, and recursive sub-task tree API via ?include=subtasks**

## Performance

- **Duration:** 4 min
- **Started:** 2026-03-08T21:51:02Z
- **Completed:** 2026-03-08T21:55:50Z
- **Tasks:** 2
- **Files modified:** 8

## Accomplishments
- Extended TaskStorer interface with ListChildTasks and UpdateTaskStatus, implemented across memory, postgres, and sqlite3 backends
- Added propagateStatusToParent helper that auto-completes parent when all children done, auto-cancels when any child fails
- Refactored all runOrgDelegation status updates to use race-safe UpdateTaskStatus (no field clobbering)
- Added TaskWithSubtasks type and buildTaskTree for recursive sub-task tree, exposed via GET /api/v1/tasks/{id}?include=subtasks
- 4 new tests: TestStatusPropagation, TestAutoCompletion, TestFailurePropagation, TestGetTaskWithSubtasks — all pass with -race

## Task Commits

Each task was committed atomically:

1. **Task 1: Add ListChildTasks + UpdateTaskStatus to interface and all 3 backends** - `d37379c` (feat)
2. **Task 2: Status propagation helper + sub-task tree API + tests** - `baa17e0` (feat)

**Plan metadata:** `8bd88b3` (docs: complete plan)

## Files Created/Modified
- `internal/service/at.go` - Extended TaskStorer interface with ListChildTasks + UpdateTaskStatus
- `internal/store/memory/tasks.go` - Memory backend implementations for both new methods
- `internal/store/postgres/tasks.go` - Postgres backend implementations using goqu builder
- `internal/store/sqlite3/tasks.go` - SQLite3 backend implementations (RFC3339 string timestamps)
- `internal/server/org-delegation.go` - propagateStatusToParent, completeTaskWithStatus helpers; refactored all UpdateTask → UpdateTaskStatus
- `internal/server/tasks.go` - TaskWithSubtasks type, buildTaskTree recursive method, enhanced GetTaskAPI with ?include=subtasks
- `internal/server/org-delegation_test.go` - Updated mock + 4 new tests (propagation, auto-completion, failure, tree)
- `internal/server/task-intake_test.go` - Updated mockTaskStore with new interface methods

## Decisions Made
- UpdateTaskStatus only touches status, result, and updated_at — prevents field clobbering in concurrent scenarios where multiple goroutines complete child tasks simultaneously
- propagateStatusToParent is fire-and-forget with slog.Warn on errors — a failing propagation should not block the child task from completing
- buildTaskTree maxDepth defaults to 20 — safe upper bound preventing runaway recursion on malformed circular data
- Refactored ALL existing UpdateTask calls in runOrgDelegation to UpdateTaskStatus for consistency and race-safety

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Updated mockTaskStore in task-intake_test.go**
- **Found during:** Task 2 (status propagation + tests)
- **Issue:** The separate `mockTaskStore` in `task-intake_test.go` also implements `TaskStorer` and needed the 2 new interface methods to compile
- **Fix:** Added `ListChildTasks` and `UpdateTaskStatus` stub implementations to `mockTaskStore`
- **Files modified:** `internal/server/task-intake_test.go`
- **Verification:** `go build ./...` and `go test -race ./internal/server/...` pass
- **Committed in:** `baa17e0` (part of Task 2 commit)

---

**Total deviations:** 1 auto-fixed (1 blocking)
**Impact on plan:** Minimal — trivial mock update required for interface compliance. No scope creep.

## Issues Encountered
None — both tasks executed cleanly.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Store infrastructure (ListChildTasks, UpdateTaskStatus) ready for concurrent fan-out in Plan 02
- propagateStatusToParent pattern ready to handle parallel child completions
- Sub-task tree API available for UI phase (Phase 4) to visualize delegation chains
- Plan 02 (concurrent fan-out) can proceed immediately — no blockers

## Self-Check: PASSED

All 9 files verified present. Both task commits (d37379c, baa17e0) found in git log.

---
*Phase: 03-depth-concurrency*
*Completed: 2026-03-08*
