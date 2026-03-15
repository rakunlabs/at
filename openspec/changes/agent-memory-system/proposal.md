## Why

When the organization delegation engine (`runOrgDelegation`) completes a task, all conversation history is discarded — `messages` is a local variable. If a manager later asks an agent to fix or extend prior work, the agent starts from scratch with zero context about what it did, what decisions it made, or what its sibling agents produced. This makes iterative workflows (delegate → review → fix) unreliable and wasteful.

## What Changes

- Introduce a persistent **AgentMemory** domain type with tiered storage (L0 one-liner summary, L1 structured decisions, L2 full conversation), stored in the database across all three backends (postgres, sqlite3, memory).
- Add **memory extraction** at the end of `runOrgDelegation`: after each task completes, an LLM call generates L0/L1 summaries and persists them alongside the full L2 conversation.
- Add **memory recall** at the start of `runOrgDelegation`: before the agentic loop, relevant past memories are loaded, scored, and injected into the system prompt as context.
- Support **cross-agent memory**: agents can recall memories from other agents in the same organization, enabling org-wide knowledge sharing.
- Add **per-agent memory configuration** on the `OrganizationAgent` join table: `memory_model`, `memory_provider`, and `memory_enabled` fields, so each agent can use a cheap/fast model for summarization.
- Upgrade the **`memory_config` workflow node** from a pass-through pipe to an optional memory retriever (`mode: "recall"`), making agent memory pluggable in the workflow DAG engine.
- Add a **`MemoryRecall` function** to the workflow `Registry`, wiring the memory store into the workflow engine.
- Add **HTTP API endpoints** for listing, viewing, searching, and deleting agent memories.
- Add a **Memory Viewer UI** in the Svelte admin: org-scoped memory list, per-memory detail view with L0/L1/L2 tabs, and memory config fields in the org agent detail panel.

## Capabilities

### New Capabilities

- `agent-memory-storage`: Domain type, store interface, and database implementations for persisting agent memories with L0/L1/L2 tiered structure.
- `agent-memory-lifecycle`: Memory extraction (post-task summarization) and recall (pre-task context injection) integrated into the org delegation engine.
- `agent-memory-workflow`: Upgraded `memory_config` node with recall mode and `MemoryRecall` function in the workflow Registry.
- `agent-memory-api`: HTTP endpoints for listing, viewing, searching, and deleting agent memories.
- `agent-memory-ui`: Svelte admin UI for browsing org memories, viewing memory details with L0/L1/L2 tabs, and configuring per-agent memory settings.

### Modified Capabilities

- None (no existing specs to modify).

## Impact

- **Domain model** (`internal/service/at.go`): New `AgentMemory` type, `AgentMemoryStorer` interface, added to `Storer` mega-interface. Extended `OrganizationAgent` with memory config fields.
- **Store backends** (`internal/store/memory/`, `sqlite3/`, `postgres/`): New store implementations for `agent_memory` and `agent_memory_messages` tables. Updated `organization_agents` table schema for new columns.
- **Delegation engine** (`internal/server/org-delegation.go`): New recall and extract steps wrapping the existing agentic loop.
- **Workflow engine** (`internal/service/workflow/node.go`, `nodes/memory-config.go`): New `MemoryRecallFunc` in Registry, upgraded `memory_config` node with recall mode.
- **Server wiring** (`internal/server/server.go`): New store field, route registration, Registry function wiring.
- **HTTP handlers** (`internal/server/agent-memory.go`): New file with CRUD + search endpoints.
- **UI** (`_ui/src/`): New API client, list page, detail page, updated org detail panel.
- **Database schema**: New `agent_memory` and `agent_memory_messages` tables with indexes. New columns on `organization_agents` table.
- **LLM cost**: Each task completion incurs one additional LLM call for summarization (configurable model, can use cheap/fast model).
