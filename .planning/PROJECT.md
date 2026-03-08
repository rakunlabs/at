# AT Organization Task Routing

## What This Is

Enhancement to the AT LLM gateway's organization system. Currently, organizations have agents with hierarchy (parent_agent_id) and a visual canvas, but the hierarchy is structural only -- it doesn't drive task routing. This work makes the org chart functional: tasks enter through a designated head agent who uses LLM judgment to delegate down an arbitrarily deep management chain, with each delegation tracked as a real Task record.

## Core Value

Tasks submitted to an organization are intelligently routed through the agent hierarchy -- the head agent receives every task, uses LLM to decide who handles it, and managers can delegate further down the tree, creating a persistent chain of tracked sub-tasks.

## Requirements

### Validated

- ✓ Organization CRUD with name, description, issue prefix, budget -- existing
- ✓ Organization-Agent membership with role, title, parent_agent_id, status -- existing
- ✓ Visual canvas on org detail page showing agent hierarchy tree -- existing
- ✓ Agent CRUD with provider, model, system prompt, skills, MCP, builtin tools -- existing
- ✓ Task system with parent_task_id hierarchy, status tracking, assignment -- existing
- ✓ agent_call workflow node with agentic loop, tool calling, sub-agent delegation -- existing
- ✓ agent_config resource node for wiring agents as delegates -- existing
- ✓ Agent budget tracking, usage recording, audit logging -- existing
- ✓ Agent runtime state and task session tracking -- existing
- ✓ Approval system for hire_agent, budget_change, task_escalate -- existing

### Active

- [ ] Head agent designation on organization (explicit field, selectable in UI)
- [ ] Org-level task intake API (POST /api/v1/organizations/{id}/tasks)
- [ ] Head agent receives incoming org tasks and uses LLM to delegate
- [ ] Managers delegate further down the hierarchy using LLM judgment
- [ ] Unlimited depth delegation chain (head -> VP -> director -> manager -> worker)
- [ ] Each delegation creates a real Task record linked to parent task
- [ ] Async delegation -- manager can hand off multiple tasks to different sub-agents simultaneously
- [ ] Canvas defines the real reporting structure (drag-to-reorder = change hierarchy)
- [ ] Hierarchy enforcement -- agents can only delegate to their direct reports

### Out of Scope

- Auto-routing by rules (no rule engine, head agent always decides) -- simplicity first
- Budget rollup across hierarchy -- existing per-agent budgets are sufficient for now
- Escalation rules -- managers handle what they can, no automatic escalation chains
- Full enterprise dashboard with progress tracking across org -- core mechanics first
- Workflow node for org_call -- API-only entry point for v1

## Context

This is a brownfield enhancement to an existing Go monolith. The organization and agent systems are fully implemented with store backends (postgres, sqlite3, memory), HTTP handlers, database migrations, and a Svelte admin UI. The key gap is that the hierarchy is visual/structural only -- there's no runtime behavior where the org chart drives task delegation.

The `agent_call` workflow node already supports sub-agent delegation via `delegate_to_{agent_name}` tool calls and recursive agentic loops. The challenge is bridging the organization hierarchy model with this delegation mechanism so that org-level task submission triggers a chain of LLM-driven delegation decisions down the management tree.

Existing infrastructure to build on:
- `OrganizationAgent.ParentAgentID` -- hierarchy already in data model
- `agent_call` node's delegate pattern -- recursive agent invocation exists
- `Task` with `ParentTaskID` -- sub-task hierarchy exists
- Canvas layout with tree positioning based on parent_agent_id

## Constraints

- **Tech stack**: Go 1.26 backend, Svelte 5 frontend, existing store pattern (postgres/sqlite3/memory)
- **Store pattern**: Must implement all three backends for any new tables/fields
- **Migration convention**: Sequential numbered SQL migrations in both postgres and sqlite3
- **API convention**: REST endpoints under /api/v1/ using ada framework
- **Node convention**: Reuse existing agent_call delegation pattern where possible

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Head agent selected manually in UI | Simple, explicit, avoids complex auto-detection | -- Pending |
| LLM-driven delegation (not rules) | Head agent uses judgment, more flexible than static routing | -- Pending |
| Unlimited hierarchy depth | Real orgs have deep chains; no artificial limit | -- Pending |
| Async delegation | Manager may delegate to multiple sub-agents simultaneously | -- Pending |
| Sub-tasks persisted as Task records | Full traceability, fits existing Task model with parent_task_id | -- Pending |
| Canvas defines hierarchy | Single source of truth, visual editing = structural editing | -- Pending |
| API-only task intake (no workflow node) | Scope control for v1, workflow node can come later | -- Pending |

---
*Last updated: 2026-03-08 after initialization*
