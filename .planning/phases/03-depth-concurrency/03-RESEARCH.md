# Phase 3: Depth & Concurrency - Research

**Researched:** 2026-03-08
**Domain:** Go concurrency patterns for recursive delegation with fan-out, task status propagation, and sub-task tree APIs
**Confidence:** HIGH

## Summary

Phase 3 transforms the existing sequential delegation engine (`org-delegation.go`, 453 lines) into a concurrent, multi-level system. The current code already supports recursive delegation at arbitrary depth — the `runOrgDelegation` function calls itself recursively via `createDelegationTask` + recursive call. However, the Phase 2 code processes multiple tool calls **sequentially** in a `for _, tc := range resp.ToolCalls` loop (line 275). Phase 3 must (a) parallelize that loop with goroutines when multiple delegate tool calls arrive in a single LLM response, (b) add status propagation so parent tasks auto-complete/fail based on children, and (c) add a `GetTaskWithSubtasks` API for tree retrieval.

The codebase already has a proven concurrency pattern in the workflow engine (`engine.go`) that uses `sync.WaitGroup` + `sync.Mutex` for fan-out branches, and `golang.org/x/sync` is already in the dependency tree (indirect). The main technical challenge is the `UpdateTask` store methods — all three backends overwrite ALL fields on update. This means concurrent status updates from parallel sub-tasks could clobber each other. A targeted `UpdateTaskStatus` store method is needed.

**Primary recommendation:** Use `sync.WaitGroup` + `sync.Mutex` (matching the workflow engine pattern) for concurrent tool call execution in `runOrgDelegation`, add `ListChildTasks` and `UpdateTaskStatus` store methods across all 3 backends, and implement status propagation as a post-completion check in the delegation function itself.

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| DELG-04 | Delegation chain supports unlimited depth (head → VP → director → manager → worker) | Already works via recursive `runOrgDelegation` — Phase 2 code is already recursive. Need to verify/test at 3+ levels. |
| CONC-01 | Manager can delegate to multiple sub-agents simultaneously (async fan-out) | Convert sequential `for _, tc := range resp.ToolCalls` loop (org-delegation.go:275) to concurrent goroutines with WaitGroup |
| CONC-02 | Results from parallel sub-agents are collected and returned to the delegating agent | Each goroutine writes its tool result to a shared slice protected by mutex; results fed back to LLM in next iteration |
| CONC-04 | Delegation runs in background goroutines, not blocking the HTTP request | Already implemented in task-intake.go:131 — `go func() { ... s.runOrgDelegation(...) ... }()` with `context.Background()` |
| STAT-01 | When a leaf task completes, parent task status is updated to reflect progress | Add status propagation check after each child delegation completes |
| STAT-02 | When all child tasks of a parent complete, parent task is marked complete | Check all siblings' status after each child completion; if all done, mark parent complete |
| STAT-03 | Task failure at any level is recorded and propagated to the parent agent | Set failed task status + result; parent receives error in tool result message |
| STAT-04 | GET /api/v1/tasks/{id} returns the task with its full sub-task tree | Add `ListChildTasks` store method + recursive tree builder in API handler |
</phase_requirements>

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `sync` (stdlib) | Go 1.26 | WaitGroup + Mutex for goroutine coordination | Project already uses this pattern in workflow engine |
| `golang.org/x/sync` | v0.19.0 | `errgroup` available if needed | Already in dependency tree (indirect) |
| `log/slog` (stdlib) | Go 1.26 | Structured logging with context | Project convention per AGENTS.md |
| `github.com/doug-martin/goqu/v9` | existing | SQL query builder for store methods | Already used in all postgres/sqlite3 store methods |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/oklog/ulid/v2` | existing | ULID generation for new records | Already used in all store CreateTask methods |
| `github.com/rakunlabs/query` | existing | Query parameter parsing for list APIs | Used in existing ListTasks |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| sync.WaitGroup + Mutex | errgroup.Group | errgroup adds context cancellation on first error but the project's workflow engine uses WaitGroup; either works, errgroup slightly cleaner for error collection |
| In-process status propagation | Database triggers/events | Overkill — delegation already has the parent context; in-process check after child completion is simpler and matches existing patterns |

**Installation:** No new dependencies needed. All required libraries are already in go.mod.

## Architecture Patterns

### Key Code Locations

```
internal/server/org-delegation.go       # Core delegation engine — main changes here
internal/server/tasks.go                # GetTaskAPI needs subtree enhancement
internal/service/at.go                  # TaskStorer interface — add 2 methods
internal/store/postgres/tasks.go        # Postgres implementation
internal/store/sqlite3/tasks.go         # SQLite implementation
internal/store/memory/tasks.go          # Memory implementation
internal/server/org-delegation_test.go  # Existing tests — extend
```

### Pattern 1: Concurrent Tool Call Execution (Fan-Out)

**What:** When an LLM returns multiple `delegate_to_*` tool calls in a single response, execute them concurrently.
**When to use:** In `runOrgDelegation`, replacing the sequential tool call loop (lines 274-352).
**How it works:** Same pattern as `engine.go` lines 285-369 — WaitGroup + Mutex.

```go
// Source: Based on internal/service/workflow/engine.go:285-369
var wg sync.WaitGroup
var resultMu sync.Mutex
toolResults := make([]service.ContentBlock, len(resp.ToolCalls))

for i, tc := range resp.ToolCalls {
    if reportAgentID, ok := delegateToolMap[tc.Name]; ok {
        wg.Add(1)
        go func(idx int, toolCall service.ToolCall) {
            defer wg.Done()
            
            taskText, _ := toolCall.Arguments["task"].(string)
            if taskText == "" {
                taskText = task.Title
            }
            
            childTask, err := s.createDelegationTask(ctx, org, task, reportAgentID, taskText, depth)
            if err != nil {
                resultMu.Lock()
                toolResults[idx] = service.ContentBlock{
                    Type:      "tool_result",
                    ToolUseID: toolCall.ID,
                    Content:   fmt.Sprintf("Error: failed to create delegation task: %v", err),
                }
                resultMu.Unlock()
                return
            }
            
            var result string
            if delegErr := s.runOrgDelegation(ctx, org, childTask, reportAgentID, depth+1); delegErr != nil {
                result = fmt.Sprintf("Error: delegation failed: %v", delegErr)
            } else {
                updated, _ := s.taskStore.GetTask(ctx, childTask.ID)
                if updated != nil && updated.Result != "" {
                    result = updated.Result
                } else {
                    result = "Delegation completed (no result returned)."
                }
            }
            
            resultMu.Lock()
            toolResults[idx] = service.ContentBlock{
                Type:      "tool_result",
                ToolUseID: toolCall.ID,
                Content:   result,
            }
            resultMu.Unlock()
        }(i, tc)
    } else {
        // Unknown tool — handle synchronously
        toolResults[i] = service.ContentBlock{
            Type:      "tool_result",
            ToolUseID: tc.ID,
            Content:   fmt.Sprintf("Error: unknown tool %q", tc.Name),
        }
    }
}

wg.Wait()
```

### Pattern 2: Status Propagation (Bottom-Up)

**What:** After a child delegation completes (success or failure), check if all siblings are done; if so, update the parent's status.
**When to use:** At the end of `runOrgDelegation`, after updating the task to completed/failed.
**Design:** The propagation happens naturally because the parent's `runOrgDelegation` waits for all children (via WaitGroup.Wait) before continuing its own agentic loop. The LLM receives all child results and decides the parent's final state. The parent task is then marked complete at the end of its own `runOrgDelegation` call.

For the auto-completion requirement (STAT-02), we add a helper that checks: "Are all child tasks of my parent done?" If yes, update the parent to completed.

```go
// propagateStatusToParent checks if all sibling tasks are complete
// and if so, marks the parent task as complete.
func (s *Server) propagateStatusToParent(ctx context.Context, task *service.Task) {
    if task.ParentID == "" {
        return // root task, nothing to propagate
    }
    
    children, err := s.taskStore.ListChildTasks(ctx, task.ParentID)
    if err != nil {
        slog.Warn("org-delegation: failed to list child tasks for propagation",
            "parent_id", task.ParentID, "error", err)
        return
    }
    
    allDone := true
    anyFailed := false
    for _, child := range children {
        switch child.Status {
        case service.TaskStatusCompleted, service.TaskStatusDone:
            // ok
        case service.TaskStatusCancelled:
            anyFailed = true
        default:
            allDone = false
        }
    }
    
    if !allDone {
        return
    }
    
    status := service.TaskStatusCompleted
    if anyFailed {
        status = service.TaskStatusCancelled
    }
    
    s.taskStore.UpdateTaskStatus(ctx, task.ParentID, status)
}
```

### Pattern 3: Sub-Task Tree Retrieval

**What:** `GET /api/v1/tasks/{id}` returns the task with all its descendants.
**When to use:** STAT-04 requirement.
**Design:** Add a `SubTasks` field to the Task response type (or a wrapper struct), fetch children recursively.

```go
// TaskWithSubtasks is the response type for GET /api/v1/tasks/{id} with subtree.
type TaskWithSubtasks struct {
    service.Task
    SubTasks []TaskWithSubtasks `json:"sub_tasks,omitempty"`
}

func (s *Server) buildTaskTree(ctx context.Context, taskID string) (*TaskWithSubtasks, error) {
    task, err := s.taskStore.GetTask(ctx, taskID)
    if err != nil || task == nil {
        return nil, err
    }
    
    result := &TaskWithSubtasks{Task: *task}
    
    children, err := s.taskStore.ListChildTasks(ctx, taskID)
    if err != nil {
        return result, nil // return task without children on error
    }
    
    for _, child := range children {
        childTree, err := s.buildTaskTree(ctx, child.ID)
        if err != nil {
            continue
        }
        if childTree != nil {
            result.SubTasks = append(result.SubTasks, *childTree)
        }
    }
    
    return result, nil
}
```

### Anti-Patterns to Avoid
- **Overwriting all fields on status update:** The current `UpdateTask` sets ALL fields. When two goroutines concurrently update children and then try to propagate status to the parent, they could clobber each other's fields. **Use a dedicated `UpdateTaskStatus` method that only writes `status` + `updated_at`.**
- **Using `errgroup.Group` and canceling all siblings on first failure:** The LLM expects results from ALL delegations, even failed ones. Don't cancel in-flight delegations when one fails — let them all complete and report back.
- **Shared mutable state between goroutines without protection:** The `messages` slice, `delegateToolMap`, and `delegateTools` are read-only during execution. Only `toolResults` needs mutex protection, and using a pre-allocated indexed slice (not append) avoids most contention.
- **Recursive tree queries without depth limit:** The `buildTaskTree` function should have a max depth guard (e.g., same `MaxDelegationDepth` from org) to prevent excessive recursion on malformed data.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Goroutine coordination | Custom channel-based fan-out/fan-in | `sync.WaitGroup` + `sync.Mutex` | Project convention from workflow engine; well-tested pattern |
| Partial field updates | Custom SQL string building | goqu query builder (already used) | Consistent with all existing store methods |
| Task tree flattening | Recursive SQL (CTEs) | In-memory recursion from `ListChildTasks` | Simpler, works across all 3 backends including memory store |
| Error aggregation across goroutines | Custom error collector | Collect errors into tool result strings | The LLM needs to see error messages as tool results anyway |

**Key insight:** The existing delegation engine already handles depth and recursion correctly. The main work is making the fan-out concurrent (straightforward goroutine pattern), adding 2 new store methods across 3 backends, and wiring up status propagation.

## Common Pitfalls

### Pitfall 1: UpdateTask Field Clobbering
**What goes wrong:** Current `UpdateTask` in all 3 backends overwrites every field. If goroutine A updates child task 1's status to "completed" while goroutine B updates child task 2's status, and then both call `UpdateTaskStatus` on the parent, the second call might overwrite the first's result.
**Why it happens:** The `UpdateTask` method doesn't support partial updates — it always writes all columns.
**How to avoid:** Add a dedicated `UpdateTaskStatus(ctx, id, status string) error` method that ONLY updates `status`, `result`, and `updated_at`. Use this for all status transitions in org-delegation.go.
**Warning signs:** Tasks showing stale data, results being empty after concurrent delegations.

### Pitfall 2: Context Cancellation Cascading
**What goes wrong:** If one delegation goroutine's context gets cancelled, all sibling goroutines sharing the same context also get cancelled.
**Why it happens:** The delegation already uses `context.Background()` from task-intake.go, so the HTTP context doesn't affect it. But if you derive contexts with `WithCancel` for individual goroutines, cancelling one would cancel siblings.
**How to avoid:** All concurrent delegation goroutines should share the same `context.Background()`-derived context. Don't use `errgroup.WithContext` if you want sibling delegations to continue after one fails.
**Warning signs:** Multiple child tasks showing "cancelled" when only one should have failed.

### Pitfall 3: Three-Backend Store Tax
**What goes wrong:** Every new store method must be implemented in postgres, sqlite3, AND memory backends. Forgetting one causes compile errors (interface satisfaction).
**Why it happens:** The project uses three separate store implementations with a shared interface.
**How to avoid:** Always implement new methods in all 3 backends in the same plan. Test with `make test` to catch interface compliance issues.
**Warning signs:** Compilation failures on one backend.

### Pitfall 4: Race Conditions in Status Propagation
**What goes wrong:** Two child tasks complete simultaneously, both check "are all siblings done?", both see the other as not-yet-done, so neither triggers parent completion.
**Why it happens:** The check-then-update is not atomic.
**How to avoid:** Use the "last writer wins" approach: each child checks siblings AFTER persisting its own status. Even with a small race window, the last child to complete will always see all siblings as done and trigger the parent update. This is safe because status only moves forward (open → in_progress → completed).
**Warning signs:** Parent tasks stuck in "in_progress" when all children are "completed".

### Pitfall 5: Deep Recursion Exhausting goroutine Stack
**What goes wrong:** Very deep delegation chains (e.g., 10 levels × multiple fan-outs) create large numbers of goroutines.
**Why it happens:** Each delegation level can spawn N goroutines (one per direct report tool call), and each of those can spawn more.
**How to avoid:** The existing `MaxDelegationDepth` (default 10) limits depth. For breadth, the number of goroutines is bounded by the total number of agents in the org (each can only be delegated to once per level). In practice, orgs with 100+ agents at a single level are rare. No additional limiting needed for v1.
**Warning signs:** Memory pressure under load — monitor with `runtime.NumGoroutine()` in logs if concerned.

## Code Examples

### New Store Interface Methods

```go
// Source: Add to internal/service/at.go TaskStorer interface
type TaskStorer interface {
    // ... existing methods ...
    
    // ListChildTasks returns all tasks with the given parent_id.
    ListChildTasks(ctx context.Context, parentID string) ([]Task, error)
    
    // UpdateTaskStatus updates only the status and result fields of a task.
    // This is safe for concurrent use (no field clobbering).
    UpdateTaskStatus(ctx context.Context, id string, status string, result string) error
}
```

### Memory Store: ListChildTasks

```go
// Source: Based on existing ListTasksByAgent pattern in internal/store/memory/tasks.go
func (m *Memory) ListChildTasks(_ context.Context, parentID string) ([]service.Task, error) {
    m.mu.RLock()
    defer m.mu.RUnlock()

    var result []service.Task
    for _, t := range m.tasks {
        if t.ParentID == parentID {
            result = append(result, t)
        }
    }

    return result, nil
}
```

### Memory Store: UpdateTaskStatus

```go
func (m *Memory) UpdateTaskStatus(_ context.Context, id string, status string, result string) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    existing, ok := m.tasks[id]
    if !ok {
        return nil
    }

    existing.Status = status
    if result != "" {
        existing.Result = result
    }
    existing.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
    m.tasks[id] = existing

    return nil
}
```

### SQL Store: ListChildTasks (goqu pattern)

```go
// Source: Based on existing ListTasksByAgent pattern in internal/store/postgres/tasks.go
func (p *Postgres) ListChildTasks(ctx context.Context, parentID string) ([]service.Task, error) {
    query, _, err := p.goqu.From(p.tableTasks).
        Select(taskColumns...).
        Where(goqu.I("parent_id").Eq(parentID)).
        ToSQL()
    if err != nil {
        return nil, fmt.Errorf("build list child tasks query: %w", err)
    }
    // ... rows scan pattern same as ListTasksByAgent ...
}
```

### SQL Store: UpdateTaskStatus (goqu pattern)

```go
func (p *Postgres) UpdateTaskStatus(ctx context.Context, id string, status string, result string) error {
    now := time.Now().UTC()
    
    record := goqu.Record{
        "status":     status,
        "updated_at": now,
    }
    if result != "" {
        record["result"] = result
    }
    
    query, _, err := p.goqu.Update(p.tableTasks).
        Set(record).
        Where(goqu.I("id").Eq(id)).
        ToSQL()
    if err != nil {
        return fmt.Errorf("build update task status query: %w", err)
    }

    _, err = p.db.ExecContext(ctx, query)
    if err != nil {
        return fmt.Errorf("update task status %q: %w", id, err)
    }

    return nil
}
```

## State of the Art

| Old Approach (Phase 2) | New Approach (Phase 3) | Impact |
|------------------------|----------------------|--------|
| Sequential tool call execution | Concurrent goroutine-per-delegation with WaitGroup | Multiple delegations run simultaneously, reducing total latency |
| Manual task status updates only | Automatic status propagation on child completion | Parent tasks auto-complete when all children are done |
| Flat task retrieval | Recursive sub-task tree retrieval | API consumers can see full delegation chain in one call |
| No "failed" task status | Cancelled status with error result | Failures are tracked and propagated to parent agent |

**Note:** There is no `TaskStatusFailed` constant in the codebase. The existing pattern uses `TaskStatusCancelled` with a descriptive `Result` string (see `task-intake.go:142`). Phase 3 should follow this same convention rather than introducing a new status.

## Open Questions

1. **Should concurrent delegation have a per-level goroutine limit?**
   - What we know: MaxDelegationDepth (default 10) limits depth. No breadth limit exists.
   - What's unclear: Whether a single agent could have 50+ direct reports, all delegated simultaneously.
   - Recommendation: For v1, don't add a limit. The number of goroutines is bounded by org size. Add a `slog.Info` log with goroutine count for monitoring. Can add semaphore-based throttling in v2 if needed.

2. **Should the sub-task tree endpoint be a separate route or enhance existing GetTask?**
   - What we know: Current `GET /api/v1/tasks/{id}` returns a flat task.
   - What's unclear: Whether enhancing the existing endpoint with a `?include=subtasks` query param is better than a new route.
   - Recommendation: Enhance `GetTaskAPI` to optionally include sub-tasks via `?include=subtasks` query param. When absent, behavior is unchanged (backward compatible). This avoids a new route and follows REST conventions.

3. **Status propagation timing: check in delegation or asynchronous?**
   - What we know: The current `runOrgDelegation` already has the context to check parent status.
   - What's unclear: Whether checking synchronously after each child completion adds latency.
   - Recommendation: Check synchronously. The check is a single `ListChildTasks` query + conditional `UpdateTaskStatus` — fast enough. Async would add complexity for no real benefit.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib) |
| Config file | None (standard `go test`) |
| Quick run command | `go test -v -race -run TestPhase3 ./internal/server/...` |
| Full suite command | `make test` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| DELG-04 | Delegation works at 3+ levels deep | unit | `go test -v -race -run TestDeepDelegation ./internal/server/` | ❌ Wave 0 |
| CONC-01 | Multiple delegations run concurrently | unit | `go test -v -race -run TestConcurrentDelegation ./internal/server/` | ❌ Wave 0 |
| CONC-02 | Results from parallel delegations collected | unit | `go test -v -race -run TestConcurrentDelegationResults ./internal/server/` | ❌ Wave 0 |
| CONC-04 | Delegation runs in background goroutine | unit | `go test -v -race -run TestIntakeTask_ValidOrgAndHeadAgent ./internal/server/` | ✅ Exists (task-intake_test.go) |
| STAT-01 | Child completion updates parent progress | unit | `go test -v -race -run TestStatusPropagation ./internal/server/` | ❌ Wave 0 |
| STAT-02 | All children done → parent marked complete | unit | `go test -v -race -run TestAutoCompletion ./internal/server/` | ❌ Wave 0 |
| STAT-03 | Failure recorded and propagated to parent | unit | `go test -v -race -run TestFailurePropagation ./internal/server/` | ❌ Wave 0 |
| STAT-04 | GET returns task with sub-task tree | unit | `go test -v -race -run TestGetTaskWithSubtasks ./internal/server/` | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `go test -v -race ./internal/server/... ./internal/store/...`
- **Per wave merge:** `make test`
- **Phase gate:** Full suite green before `/gsd-verify-work`

### Wave 0 Gaps
- [ ] `internal/server/org-delegation_test.go` — extend with concurrent delegation tests (CONC-01, CONC-02)
- [ ] `internal/server/org-delegation_test.go` — extend with deep delegation tests (DELG-04)
- [ ] `internal/server/org-delegation_test.go` — extend with status propagation tests (STAT-01, STAT-02, STAT-03)
- [ ] `internal/server/tasks_test.go` — new file for sub-task tree retrieval tests (STAT-04)
- [ ] Mock stores for `ListChildTasks` and `UpdateTaskStatus` — extend existing mock stores in test files

*(No new framework install needed — existing `go test` infrastructure is sufficient)*

## Sources

### Primary (HIGH confidence)
- `internal/server/org-delegation.go` — current delegation engine (453 lines), sequential tool call loop at line 275
- `internal/service/workflow/engine.go` — existing fan-out concurrency pattern (WaitGroup + Mutex, lines 285-369)
- `internal/service/at.go` — TaskStorer interface (lines 673-688), Task struct (lines 644-670), status constants (lines 619-631)
- `internal/store/*/tasks.go` — all 3 backend implementations (memory: 209 lines, postgres: 434 lines, sqlite3: 408 lines)
- `internal/server/task-intake.go` — async delegation goroutine pattern (line 131)

### Secondary (MEDIUM confidence)
- `internal/server/org-delegation_test.go` — existing test patterns, mock stores (363 lines)
- `internal/server/task-intake_test.go` — existing intake tests with mock stores (267 lines)

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries already in use, patterns proven in codebase
- Architecture: HIGH — directly extending existing code with well-understood Go patterns
- Pitfalls: HIGH — identified from reading actual code (UpdateTask clobbering, context sharing)
- Store patterns: HIGH — 3 existing task store implementations provide exact templates

**Research date:** 2026-03-08
**Valid until:** 2026-04-08 (stable codebase patterns, no external dependency churn)
