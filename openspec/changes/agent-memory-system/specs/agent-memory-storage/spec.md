## ADDED Requirements

### Requirement: AgentMemory domain type

The system SHALL define an `AgentMemory` struct in `internal/service/at.go` with the following fields: `ID` (ULID), `AgentID`, `OrganizationID`, `TaskID`, `TaskIdentifier`, `SummaryL0` (one-sentence summary), `SummaryL1` (structured decisions and approach), `Tags` (string slice), and `CreatedAt`. All fields SHALL have `json:"snake_case"` tags.

#### Scenario: AgentMemory struct is usable for JSON serialization
- **WHEN** an `AgentMemory` value is marshaled to JSON
- **THEN** all fields use snake_case keys matching the established codebase convention

### Requirement: AgentMemoryMessages type for L2 storage

The system SHALL define an `AgentMemoryMessages` struct with fields: `MemoryID` (FK to AgentMemory) and `Messages` (slice of `service.Message`). This type stores the full L2 conversation separately from the lightweight L0/L1 data.

#### Scenario: L2 messages are stored separately from summaries
- **WHEN** a memory is created with full conversation history
- **THEN** the L0/L1 data is stored in the `agent_memory` table and the L2 messages are stored in the `agent_memory_messages` table

### Requirement: AgentMemoryStorer interface

The system SHALL define an `AgentMemoryStorer` interface with these methods:
- `CreateAgentMemory(ctx, mem AgentMemory) (*AgentMemory, error)` — creates a memory entry
- `GetAgentMemory(ctx, id string) (*AgentMemory, error)` — returns nil, nil if not found
- `ListAgentMemories(ctx, agentID, orgID string) ([]AgentMemory, error)` — list by agent within org
- `ListOrgMemories(ctx, orgID string) ([]AgentMemory, error)` — list all memories in an org (cross-agent)
- `SearchAgentMemories(ctx, agentID, orgID, query string) ([]AgentMemory, error)` — keyword search on L0, L1, and tags
- `DeleteAgentMemory(ctx, id string) error` — deletes memory and associated messages
- `GetAgentMemoryMessages(ctx, memoryID string) (*AgentMemoryMessages, error)` — load L2 messages on demand
- `CreateAgentMemoryMessages(ctx, msgs AgentMemoryMessages) error` — persist L2 messages

#### Scenario: Interface is part of the Storer mega-interface
- **WHEN** the store factory creates a store
- **THEN** the returned `service.Storer` satisfies `AgentMemoryStorer`

### Requirement: Memory store backend — memory

The system SHALL implement `AgentMemoryStorer` in `internal/store/memory/` using in-memory maps protected by `sync.RWMutex`, following the existing memory backend patterns.

#### Scenario: In-memory CRUD operations
- **WHEN** a memory is created, then retrieved by ID
- **THEN** the retrieved memory matches the created one
- **WHEN** a memory is retrieved that does not exist
- **THEN** the method returns `nil, nil`

### Requirement: Memory store backend — sqlite3

The system SHALL implement `AgentMemoryStorer` in `internal/store/sqlite3/` using goqu query builder, private row structs with `db:"..."` tags, `sql.NullString` for nullable fields, and `errors.Is(err, sql.ErrNoRows)` returning `nil, nil`. The DDL SHALL create `agent_memory` and `agent_memory_messages` tables with appropriate indexes.

#### Scenario: SQLite tables are created on init
- **WHEN** the sqlite3 store initializes
- **THEN** `agent_memory` and `agent_memory_messages` tables exist with indexes on `(agent_id, organization_id)`, `(organization_id)`, and `(task_id)`

### Requirement: Memory store backend — postgres

The system SHALL implement `AgentMemoryStorer` in `internal/store/postgres/` using goqu query builder, `types.RawJSON` for JSON columns, `time.Time` for timestamps, `nullString()` helper for nullable strings, and positional `$N` parameters.

#### Scenario: Postgres tables are created on init
- **WHEN** the postgres store initializes
- **THEN** `agent_memory` and `agent_memory_messages` tables exist with the same indexes as sqlite3

### Requirement: OrganizationAgent memory config fields

The system SHALL add three fields to `OrganizationAgent`: `MemoryModel` (string, optional), `MemoryProvider` (string, optional), and `MemoryEnabled` (*bool, optional, default true). These fields SHALL be persisted in all three store backends.

#### Scenario: Agent added to org with memory config
- **WHEN** an agent is added to an organization with `memory_model: "gpt-4o-mini"` and `memory_provider: "openai"`
- **THEN** the stored `OrganizationAgent` record has those values

#### Scenario: Default memory_enabled is true
- **WHEN** an agent is added to an organization without specifying `memory_enabled`
- **THEN** memory extraction and recall are active for that agent (treated as enabled)

### Requirement: Database schema for agent_memory

The `agent_memory` table SHALL have columns: `id` (TEXT PK), `agent_id` (TEXT NOT NULL), `organization_id` (TEXT NOT NULL), `task_id` (TEXT NOT NULL), `task_identifier` (TEXT), `summary_l0` (TEXT NOT NULL), `summary_l1` (TEXT NOT NULL), `tags` (TEXT, JSON array), `created_at` (TEXT NOT NULL). Indexes SHALL exist on `(agent_id, organization_id)`, `(organization_id)`, and `(task_id)`.

#### Scenario: Query performance for recall
- **WHEN** listing memories for a specific agent in a specific org
- **THEN** the query uses the `(agent_id, organization_id)` index

### Requirement: Database schema for agent_memory_messages

The `agent_memory_messages` table SHALL have columns: `memory_id` (TEXT PK, FK to agent_memory), `messages` (TEXT NOT NULL, JSON blob of full conversation). Deleting from `agent_memory` SHALL cascade to delete the associated messages row.

#### Scenario: Cascade delete
- **WHEN** an agent memory is deleted
- **THEN** the associated messages record in `agent_memory_messages` is also deleted
