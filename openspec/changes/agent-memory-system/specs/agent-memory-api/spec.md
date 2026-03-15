## ADDED Requirements

### Requirement: List organization memories endpoint

The system SHALL provide `GET /api/v1/organizations/{id}/memories` that returns all agent memories within an organization. The endpoint SHALL support an optional `agent_id` query parameter to filter by a specific agent. Results SHALL be ordered by `created_at` descending (most recent first). The response SHALL return L0 and L1 data but NOT L2 messages.

#### Scenario: List all memories in an org
- **WHEN** a GET request is made to `/api/v1/organizations/{org_id}/memories`
- **THEN** all memories in that organization are returned with L0 and L1 data, ordered by recency

#### Scenario: Filter by agent
- **WHEN** a GET request is made to `/api/v1/organizations/{org_id}/memories?agent_id=X`
- **THEN** only memories belonging to agent X in that organization are returned

#### Scenario: Store not configured
- **WHEN** the agent memory store is nil
- **THEN** the endpoint returns HTTP 503 with message "store not configured"

### Requirement: Search organization memories endpoint

The system SHALL provide `POST /api/v1/organizations/{id}/memories/search` that accepts a JSON body with a `query` field and returns memories matching the query. The search SHALL match against L0 summaries, L1 content, and tags. Results SHALL be scored and ordered by relevance.

#### Scenario: Search returns relevant memories
- **WHEN** a POST request with `{"query": "JWT authentication"}` is made
- **THEN** memories with matching L0, L1, or tag content are returned ordered by relevance score

#### Scenario: Empty query returns recent memories
- **WHEN** a POST request with `{"query": ""}` is made
- **THEN** the most recent memories are returned (recency-ordered fallback)

### Requirement: Get memory detail endpoint

The system SHALL provide `GET /api/v1/agent-memories/{id}` that returns a single memory by ID including L0, L1, and tags but NOT L2 messages.

#### Scenario: Memory found
- **WHEN** a GET request is made with a valid memory ID
- **THEN** the memory's L0, L1, tags, agent_id, task_id, task_identifier, and timestamps are returned

#### Scenario: Memory not found
- **WHEN** a GET request is made with a non-existent ID
- **THEN** the endpoint returns HTTP 404

### Requirement: Get memory messages endpoint

The system SHALL provide `GET /api/v1/agent-memories/{id}/messages` that returns the L2 full conversation messages for a memory.

#### Scenario: Messages found
- **WHEN** a GET request is made for a memory that has L2 messages stored
- **THEN** the full conversation messages array is returned as JSON

#### Scenario: No messages stored
- **WHEN** a GET request is made for a memory that has no L2 messages
- **THEN** the endpoint returns HTTP 404 or an empty messages array

### Requirement: Delete memory endpoint

The system SHALL provide `DELETE /api/v1/agent-memories/{id}` that deletes a memory and its associated L2 messages.

#### Scenario: Successful deletion
- **WHEN** a DELETE request is made with a valid memory ID
- **THEN** the memory and its associated messages are deleted
- **AND** the endpoint returns HTTP 200 with a success message

#### Scenario: Delete non-existent memory
- **WHEN** a DELETE request is made with a non-existent ID
- **THEN** the endpoint returns HTTP 404

### Requirement: Server route registration

All agent memory API endpoints SHALL be registered in `server.go` following the existing route registration pattern. Routes SHALL be guarded by nil store checks.

#### Scenario: Routes are registered
- **WHEN** the server starts with a configured store
- **THEN** all agent memory routes are accessible
