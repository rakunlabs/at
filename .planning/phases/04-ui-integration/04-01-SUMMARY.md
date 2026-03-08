---
phase: 04-ui-integration
plan: 01
subsystem: ui
tags: [svelte, organization, head-agent, task-intake, dropdown, form]

# Dependency graph
requires:
  - phase: 01-foundation
    provides: Organization head_agent_id field, task intake API
  - phase: 02-core-delegation
    provides: Delegation engine triggered by task submission
provides:
  - Head agent dropdown on OrganizationDetail toolbar
  - Submit Task side panel calling intake API
  - Organization TS type synced with Go backend struct
  - submitOrgTask API function
affects: [04-02-PLAN]

# Tech tracking
tech-stack:
  added: []
  patterns: [collapsible-side-panel, toolbar-dropdown-selector, mutual-panel-exclusion]

key-files:
  created: []
  modified:
    - _ui/src/lib/api/organizations.ts
    - _ui/src/pages/OrganizationDetail.svelte

key-decisions:
  - "Head agent dropdown placed in toolbar right section, visible only when not editing org name and org has memberships"
  - "Submit Task button disabled with tooltip when no head agent is set"
  - "Mutual exclusion between Add Agent and Submit Task side panels to avoid layout collision"

patterns-established:
  - "Toolbar dropdown for entity selection: Crown icon + label + select element"
  - "Side panel toggle with mutual exclusion: opening one closes the other"

requirements-completed: [HIER-02, UI-01, UI-02, UI-03]

# Metrics
duration: 2min
completed: 2026-03-08
---

# Phase 4 Plan 1: Head Agent Selector + Task Submission Form Summary

**Head agent dropdown and task submission side panel on OrganizationDetail page, with Organization TS type synced to Go backend**

## Performance

- **Duration:** 2 min
- **Started:** 2026-03-08T22:19:17Z
- **Completed:** 2026-03-08T22:21:55Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments
- Organization TypeScript interface now mirrors all Go backend fields (head_agent_id, max_delegation_depth, issue_prefix, budget fields, etc.)
- Head agent dropdown in toolbar populated from org memberships, persists selection via updateOrganization API
- Submit Task side panel with title/description fields calls POST /organizations/{id}/tasks and displays returned identifier
- Submit Task button disabled with tooltip when no head agent is configured

## Task Commits

Each task was committed atomically:

1. **Task 1: Update Organization TS type and add submitOrgTask API function** - `71e27d4` (feat)
2. **Task 2: Add head agent dropdown and Submit Task form to OrganizationDetail** - `11caebe` (feat)

## Files Created/Modified
- `_ui/src/lib/api/organizations.ts` - Added missing Organization fields, IntakeTaskRequest/Response types, submitOrgTask function
- `_ui/src/pages/OrganizationDetail.svelte` - Added Crown/Send icons, head agent dropdown, Submit Task button and side panel with form

## Decisions Made
- Head agent dropdown is placed in toolbar's right side, between agent count and Refresh button, with a divider
- Submit Task button is placed next to Add Agent button, disabled when no head agent set (tooltip explains why)
- Panels are mutually exclusive: opening Submit Task closes Add Agent panel and vice versa
- Success message shows the returned task identifier (e.g., "PAP-42 created — delegation in progress")

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
None

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Ready for 04-02-PLAN.md: Delegation chain tree visualization on TaskDetail page
- Organization detail page is complete with head agent management and task submission

---
*Phase: 04-ui-integration*
*Completed: 2026-03-08*

## Self-Check: PASSED
