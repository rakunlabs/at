---
phase: 3
slug: depth-concurrency
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-08
---

# Phase 3 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (stdlib) |
| **Config file** | None (standard `go test`) |
| **Quick run command** | `go test -v -race ./internal/server/...` |
| **Full suite command** | `make test` |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test -v -race ./internal/server/... ./internal/store/...`
- **After every plan wave:** Run `make test`
- **Before `/gsd-verify-work`:** Full suite must be green
- **Max feedback latency:** 15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 03-01-01 | 01 | 1 | STAT-01, STAT-02, STAT-03, STAT-04 | unit | `go test -v -race -run "TestStatusPropagation\|TestAutoCompletion\|TestFailurePropagation\|TestGetTaskWithSubtasks" ./internal/server/... ./internal/store/...` | ❌ W0 | ⬜ pending |
| 03-02-01 | 02 | 2 | CONC-01, CONC-02 | unit | `go test -v -race -run "TestConcurrentDelegation" ./internal/server/` | ❌ W0 | ⬜ pending |
| 03-02-02 | 02 | 2 | DELG-04 | unit | `go test -v -race -run "TestDeepDelegation" ./internal/server/` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/server/org-delegation_test.go` — extend with concurrent delegation tests (CONC-01, CONC-02)
- [ ] `internal/server/org-delegation_test.go` — extend with deep delegation tests (DELG-04)
- [ ] `internal/server/org-delegation_test.go` — extend with status propagation tests (STAT-01, STAT-02, STAT-03)
- [ ] `internal/server/tasks_test.go` — new file for sub-task tree retrieval tests (STAT-04)
- [ ] Mock stores for `ListChildTasks` and `UpdateTaskStatus` — extend existing mock stores

*No new framework install needed — existing `go test` infrastructure is sufficient.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| End-to-end 3+ level delegation with real LLM | DELG-04 | Requires real LLM provider | Submit task to org with 3+ level hierarchy, verify child tasks created at each level |
| Concurrent delegation wall-clock time | CONC-01 | Timing-dependent with real LLM | Submit task that triggers 3 parallel delegations, verify completion time < 3x sequential |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
