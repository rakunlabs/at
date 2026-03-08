---
phase: 1
slug: foundation
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-08
---

# Phase 1 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go standard `testing` package |
| **Config file** | None (standard `go test`) |
| **Quick run command** | `go test -v -race ./internal/server/... ./internal/store/...` |
| **Full suite command** | `make test` (`go test -v -race ./...`) |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test -v -race ./internal/server/... ./internal/store/memory/...`
- **After every plan wave:** Run `make test`
- **Before `/gsd-verify-work`:** Full suite must be green
- **Max feedback latency:** 15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 01-01-01 | 01 | 1 | HIER-01 | unit | `go test -v -race -run TestOrgHeadAgent ./internal/store/memory/...` | ❌ W0 | ⬜ pending |
| 01-01-02 | 01 | 1 | HIER-04 | unit | `go test -v -race -run TestHierarchyValidation ./internal/server/...` | ❌ W0 | ⬜ pending |
| 01-02-01 | 02 | 1 | INTK-01 | unit | `go test -v -race -run TestIntakeTask ./internal/server/...` | ❌ W0 | ⬜ pending |
| 01-02-02 | 02 | 1 | INTK-02 | unit | `go test -v -race -run TestIntakeReturns202 ./internal/server/...` | ❌ W0 | ⬜ pending |
| 01-02-03 | 02 | 1 | INTK-03 | unit | `go test -v -race -run TestIntakeValidation ./internal/server/...` | ❌ W0 | ⬜ pending |
| 01-02-04 | 02 | 1 | INTK-04 | unit | `go test -v -race -run TestIntakeIdentifier ./internal/server/...` | ❌ W0 | ⬜ pending |
| 01-01-03 | 01 | 1 | DELG-06 | unit | `go test -v -race -run TestOrgMaxDepth ./internal/store/memory/...` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/server/task_intake_test.go` — stubs for INTK-01, INTK-02, INTK-03, INTK-04
- [ ] `internal/server/hierarchy_validation_test.go` — stubs for HIER-04
- [ ] `internal/store/memory/organizations_test.go` — stubs for HIER-01, DELG-06
- [ ] Test helpers: memory store setup function for creating org + agents + tasks in tests

---

## Manual-Only Verifications

*All phase behaviors have automated verification.*

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
