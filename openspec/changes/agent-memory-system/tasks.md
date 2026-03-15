## 1. Domain Types and Interfaces

- [x] 1.1 Add `AgentMemory` struct to `internal/service/at.go` with fields: ID, AgentID, OrganizationID, TaskID, TaskIdentifier, SummaryL0, SummaryL1, Tags ([]string), CreatedAt
- [x] 1.2 Add `AgentMemoryMessages` struct to `internal/service/at.go` with fields: MemoryID, Messages ([]Message as JSON)
- [x] 1.3 Add `AgentMemoryStorer` interface to `internal/service/at.go` with methods: CreateAgentMemory, GetAgentMemory, ListAgentMemories, ListOrgMemories, SearchAgentMemories, DeleteAgentMemory, GetAgentMemoryMessages, CreateAgentMemoryMessages
- [x] 1.4 Add `AgentMemoryStorer` to the `Storer` mega-interface
- [x] 1.5 Add memory config fields to `OrganizationAgent` struct: MemoryModel, MemoryProvider, MemoryEnabled (*bool)

## 2. Store Backend — Memory (in-memory)

- [x] 2.1 Create `internal/store/memory/agent-memory.go` implementing `AgentMemoryStorer` with map-based storage and RWMutex
- [x] 2.2 Add `agentMemory` and `agentMemoryMessages` map fields to the Memory struct and initialize in `New()`
- [x] 2.3 Update `internal/store/memory/organization-agents.go` to handle the new memory config fields on OrganizationAgent

## 3. Store Backend — SQLite3

- [x] 3.1 Create `internal/store/sqlite3/agent-memory.go` with `agentMemoryRow` struct, scan helpers, goqu queries, and `rowToRecord` converter
- [x] 3.2 Add DDL for `agent_memory` table with indexes on (agent_id, organization_id), (organization_id), (task_id)
- [x] 3.3 Add DDL for `agent_memory_messages` table with FK to agent_memory and cascade delete
- [x] 3.4 Add table references (`tableAgentMemory`, `tableAgentMemoryMessages`) to SQLite struct
- [x] 3.5 Update `organization_agents` table DDL and row handling for new memory config columns

## 4. Store Backend — Postgres

- [x] 4.1 Create `internal/store/postgres/agent-memory.go` with `agentMemoryRow` struct using types.RawJSON for JSON columns and time.Time for timestamps
- [x] 4.2 Add DDL for `agent_memory` and `agent_memory_messages` tables with same indexes as sqlite3
- [x] 4.3 Add table references to Postgres struct
- [x] 4.4 Update `organization_agents` table DDL and row handling for new memory config columns

## 5. Server Wiring

- [x] 5.1 Add `agentMemoryStore service.AgentMemoryStorer` field to Server struct in `internal/server/server.go`
- [x] 5.2 Wire `agentMemoryStore: store` in Server `New()` constructor
- [x] 5.3 Register agent memory HTTP routes in `New()`

## 6. HTTP API Handlers

- [x] 6.1 Create `internal/server/agent-memory.go` with `ListOrgMemoriesAPI` handler (GET /api/v1/organizations/{id}/memories, supports ?agent_id filter)
- [x] 6.2 Add `SearchOrgMemoriesAPI` handler (POST /api/v1/organizations/{id}/memories/search)
- [x] 6.3 Add `GetAgentMemoryAPI` handler (GET /api/v1/agent-memories/{id})
- [x] 6.4 Add `GetAgentMemoryMessagesAPI` handler (GET /api/v1/agent-memories/{id}/messages)
- [x] 6.5 Add `DeleteAgentMemoryAPI` handler (DELETE /api/v1/agent-memories/{id})

## 7. Memory Lifecycle — Extraction

- [x] 7.1 Create `internal/server/org-memory.go` with `generateMemorySummary` method that builds the summarization prompt and calls the LLM
- [x] 7.2 Implement summarization prompt structure: input = task title + description + conversation messages, output = SUMMARY, DECISIONS, APPROACH, TAGS
- [x] 7.3 Implement response parser that extracts L0 (SUMMARY), L1 (DECISIONS + APPROACH), and tags from the LLM response
- [x] 7.4 Implement `extractAndPersistMemory` method that resolves the memory provider/model from OrganizationAgent config (with fallback to agent's own), calls generateMemorySummary, and saves AgentMemory + AgentMemoryMessages to the store
- [x] 7.5 Integrate extraction into `runOrgDelegation`: call `extractAndPersistMemory` after the agentic loop completes and before `completeTaskWithStatus`, gated by `memory_enabled` check

## 8. Memory Lifecycle — Recall

- [x] 8.1 Implement `recallAgentMemories` method that loads agent's own memories + org-wide memories, scores them, and returns top matches within token budget
- [x] 8.2 Implement the scoring algorithm: recency (0-30), tag overlap (20 per match, max 100), L0 keyword match (0-50), own-memory bonus (+25), parent-task bonus (+50)
- [x] 8.3 Implement `formatMemoriesForPrompt` that formats scored memories as a "## Relevant Past Work" section with agent name, task identifier, and L1 content
- [x] 8.4 Integrate recall into `runOrgDelegation`: call `recallAgentMemories` after loading the agent but before building messages, append formatted memories to the system prompt, gated by `memory_enabled` check

## 9. Workflow Engine Integration

- [x] 9.1 Add `MemoryRecall MemoryRecallFunc` field and `MemoryRecallFunc` type to `Registry` in `internal/service/workflow/node.go`
- [x] 9.2 Wire `MemoryRecall` closure in `server.go` that calls the agentMemoryStore
- [x] 9.3 Upgrade `memory_config` node in `internal/service/workflow/nodes/memory-config.go`: add `mode` config field parsing, implement recall mode that calls `Registry.MemoryRecall`, fallback to static mode when MemoryRecall is nil
- [x] 9.4 Add config fields to memory_config node: agent_id, organization_id, cross_agent (bool), max_tokens (int)

## 10. Frontend — API Client

- [x] 10.1 Create `_ui/src/lib/api/agent-memory.ts` with interfaces (AgentMemory, AgentMemoryMessages) and async functions (listOrgMemories, getAgentMemory, getAgentMemoryMessages, searchOrgMemories, deleteAgentMemory)

## 11. Frontend — Memory List Page

- [x] 11.1 Create `_ui/src/pages/AgentMemories.svelte` with DataTable showing agent name, task identifier, L0 summary, tags, and date columns
- [x] 11.2 Add agent filter dropdown populated from org agents list
- [x] 11.3 Add search input that calls the search API endpoint
- [x] 11.4 Add click-through navigation to memory detail page

## 12. Frontend — Memory Detail Page

- [x] 12.1 Create `_ui/src/pages/AgentMemoryDetail.svelte` with L0 header, tags as badges, and metadata section
- [x] 12.2 Add tabbed view with "Summary" tab rendering L1 as markdown and "Full Conversation" tab loading L2 messages on demand
- [x] 12.3 Add link to associated task (navigates to TaskDetail page)
- [x] 12.4 Add delete button with confirmation

## 13. Frontend — Org Agent Memory Config

- [x] 13.1 Update `OrganizationDetail.svelte` agent detail side panel: add "Memory Settings" section with Memory Enabled checkbox, Memory Provider dropdown, and Memory Model text input
- [x] 13.2 Wire save to existing `updateOrgAgent` API call with new fields
- [x] 13.3 Add "View Memories" link in agent detail panel that navigates to memories list pre-filtered by agent

## 14. Frontend — Routing

- [x] 14.1 Add route `#/organizations/:id/memories` to `_ui/src/routes.ts` pointing to AgentMemories component
- [x] 14.2 Add route `#/agent-memories/:id` to `_ui/src/routes.ts` pointing to AgentMemoryDetail component
- [x] 14.3 Add navigation entry for memories in the organization context (sidebar or org detail page)

## 15. Testing

- [x] 15.1 Add unit tests for memory scoring algorithm (table-driven tests with various scenarios)
- [x] 15.2 Add unit tests for summarization prompt builder and response parser
- [x] 15.3 Add HTTP handler tests using httptest for memory CRUD endpoints
- [x] 15.4 Run `make test` to verify no regressions
- [x] 15.5 Run `cd _ui && pnpm run check` to verify no TypeScript/Svelte errors
