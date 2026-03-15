## Context

AT's organization delegation engine (`runOrgDelegation` in `internal/server/org-delegation.go`) runs an agentic LLM loop where a manager agent can delegate tasks to sub-agents. The conversation history (`var messages []service.Message`) is local to the function. When a task completes, only a flat `result` string is persisted in the `tasks` table — all decisions, reasoning, and sub-agent interactions are lost.

The codebase has unused memory infrastructure: `AgentRuntimeState.StateJSON` (free-form map, API-only, nothing reads it), `AgentTaskSession` (per-task session params, API-only), and a `memory_config` workflow node that is a pure pass-through pipe. The workflow engine's `agent_call` node already accepts a `"memory"` input port and appends it to the prompt as `"\n\nMemory:\n"`, but nothing populates it with persistent data.

## Goals / Non-Goals

**Goals:**
- Every agent in an organization automatically remembers what it did on past tasks
- Memories are tiered (L0/L1/L2) to minimize token consumption during recall
- Cross-agent memory allows agents to learn from the entire org's experience
- Memory summarization model is configurable per agent per org (cheap/fast model)
- Memory is pluggable in the workflow DAG engine via the upgraded `memory_config` node
- Memory is browsable and manageable through the admin UI
- Works across all three store backends (postgres, sqlite3, memory)

**Non-Goals:**
- Semantic/vector search (use simple keyword + recency scoring for now; can upgrade later if AT's RAG system is available)
- Memory decay or automatic pruning (keep all memories; address scale later)
- Memory sharing across organizations (scoped to single org)
- Embedding-based retrieval (no new ML model dependencies)
- Modifying the existing `AgentRuntimeState` or `AgentTaskSession` — those remain as-is

## Decisions

### 1. Separate tables for L0/L1 vs L2 messages

**Decision**: Store L0 + L1 in `agent_memory` table, full L2 conversation in `agent_memory_messages` table (1:1 relationship).

**Alternatives considered:**
- Single table with all fields: simpler schema, but recall queries load 50KB+ message blobs just to read 100-byte summaries. With 50+ memories per agent, this is 2.5MB vs ~100KB per recall.
- Separate L0/L1/L2 each in own table: over-normalized for the access patterns.

**Rationale**: The recall path (hot path) only needs L0 + L1. L2 is loaded on demand for UI drill-down or deep context. Keeping the hot table lean benefits both Postgres (avoids TOAST decompression) and SQLite (no large blobs in scanned rows).

### 2. Per-agent memory config on OrganizationAgent join table

**Decision**: Add `memory_model`, `memory_provider`, and `memory_enabled` fields to `OrganizationAgent`.

**Alternatives considered:**
- Global summarization model config on Organization: less flexible, can't optimize per-agent.
- Config on the Agent itself: agent may belong to multiple orgs with different needs.
- Separate config table: over-engineered for three fields.

**Rationale**: The join table already holds per-agent-per-org config (role, title, heartbeat_schedule). Memory config is the same kind of thing — how this agent operates in this specific org. Falls back to agent's own provider/model when not set.

### 3. Synchronous summarization (not async)

**Decision**: Generate L0/L1 summaries synchronously at task completion, before returning the result.

**Alternatives considered:**
- Async background worker: avoids latency but memory isn't available for immediate follow-up tasks ("fix this thing you just did").
- Fire-and-forget goroutine: risk of lost summaries on crash.

**Rationale**: The summarization LLM call is small (~1-2K input tokens for the conversation, ~200 output tokens for the summary). Using a cheap model (gpt-4o-mini, claude-haiku), this adds ~1-2 seconds. The primary use case is rapid iteration (delegate → review → fix), where immediate memory availability is critical.

### 4. Simple scoring algorithm for recall (no embeddings)

**Decision**: Score memories using recency + keyword overlap + tag matching + relationship bonuses. No vector/embedding search.

**Scoring formula:**
- Recency: 0-30 points (linear decay over time)
- Tag overlap with task words: 0-100 points (20 per matching tag)
- L0 keyword match: 0-50 points (simple term frequency)
- Own-memory bonus: +25 points (prefer agent's own work)
- Parent-task bonus: +50 points (same task tree = very relevant)

**Alternatives considered:**
- RAG/embedding search: AT has a RAG system, but adding embedding dependencies to every task completion is heavyweight. Can upgrade later.
- Full-text search (Postgres `tsvector`, SQLite FTS5): better than keyword matching but adds schema complexity. Good future enhancement.

**Rationale**: Start simple, measure effectiveness, upgrade if needed. The scoring is fast (in-memory after a single DB query) and handles the two primary cases well: "fix what you just did" (parent-task bonus) and "related past work" (tag/keyword overlap).

### 5. Upgrade memory_config node with recall mode

**Decision**: Add a `mode` config field to `memory_config` node. `mode: "static"` preserves current pass-through behavior. `mode: "recall"` queries the `AgentMemory` store via a new `MemoryRecall` function on the workflow `Registry`.

**Alternatives considered:**
- New node type `memory_recall`: cleaner separation but duplicates the node registration and edge wiring.
- Always recall (remove static mode): breaks backward compatibility for existing workflows.

**Rationale**: Extending the existing node preserves backward compatibility and keeps the workflow editor simple. Users can drag a memory_config node, set mode=recall, and connect it to agent_call's memory port.

### 6. API structure

**Decision**: Org-scoped endpoints for listing and searching, direct ID-based endpoints for detail and delete.

```
GET    /api/v1/organizations/{id}/memories           — list (supports ?agent_id= filter)
POST   /api/v1/organizations/{id}/memories/search    — search (body: {query: "..."})
GET    /api/v1/agent-memories/{id}                   — get memory detail (L0+L1)
GET    /api/v1/agent-memories/{id}/messages           — get L2 messages
DELETE /api/v1/agent-memories/{id}                   — delete memory
```

**Rationale**: Org-scoped listing enables the cross-agent memory view. Separate `/messages` endpoint for L2 keeps the detail response lightweight and mirrors the separate-table design.

## Risks / Trade-offs

- **Extra LLM cost per task**: Each task completion adds one summarization LLM call. Mitigated by using cheap/fast models (configurable) and `memory_enabled` opt-out flag.

- **Memory grows unbounded**: No decay/pruning mechanism. With high task volume, the memory table could grow large. Mitigated by keeping the hot table lean (L0+L1 only, ~2KB per entry) and deferring pruning to a future change.

- **Keyword scoring is unsophisticated**: May miss semantically related memories or surface irrelevant ones. Mitigated by heavily weighting recency and task-tree relationships, which cover the primary use cases. Can upgrade to FTS5/tsvector or RAG-based retrieval later.

- **Summarization quality depends on model**: Cheap models may produce poor summaries. Mitigated by using a structured prompt with clear output format (L0/L1/tags), which even small models handle well.

- **Schema migration required**: New tables and columns on existing table. Mitigated by using the same DDL patterns as existing tables. No data migration needed (additive change).

- **Token budget for recall context**: Injecting too many memories could consume too much of the context window. Mitigated by a token budget (default 2000 tokens) that limits how many L1 summaries are included.
