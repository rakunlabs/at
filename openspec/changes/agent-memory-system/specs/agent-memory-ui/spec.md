## ADDED Requirements

### Requirement: Agent memory API client

The system SHALL provide a TypeScript API client at `_ui/src/lib/api/agent-memory.ts` with functions for listing org memories, getting memory detail, getting memory messages (L2), searching memories, and deleting memories. The client SHALL follow the existing pattern: `axios.create({ baseURL: 'api/v1' })`, interface definitions matching backend JSON, and async functions.

#### Scenario: API client exports all CRUD functions
- **WHEN** the API client module is imported
- **THEN** it exports `listOrgMemories`, `getAgentMemory`, `getAgentMemoryMessages`, `searchOrgMemories`, and `deleteAgentMemory` functions

### Requirement: Organization memories list page

The system SHALL provide a Svelte page component for browsing agent memories within an organization. The page SHALL display a table with columns: Agent name, Task identifier, L0 summary, Tags, and Date. The page SHALL support filtering by agent (dropdown) and searching by keyword.

#### Scenario: List view shows all org memories
- **WHEN** a user navigates to the org memories page
- **THEN** all memories in the organization are displayed in a table, ordered by recency

#### Scenario: Filter by agent
- **WHEN** a user selects an agent from the filter dropdown
- **THEN** only memories from that agent are shown

#### Scenario: Search memories
- **WHEN** a user types a search query
- **THEN** the list filters to show memories matching the query

### Requirement: Memory detail page

The system SHALL provide a Svelte page component for viewing a single memory's details. The page SHALL display the L0 summary as a header, tags as badges, and a tabbed view with: "Summary" tab (L1 content rendered as markdown), "Full Conversation" tab (L2 messages in a chat-like view), and a link to the associated task.

#### Scenario: Detail view shows L1 summary
- **WHEN** a user clicks a memory in the list
- **THEN** the detail page shows the L0 as header and L1 content rendered as markdown

#### Scenario: L2 conversation tab loads on demand
- **WHEN** a user clicks the "Full Conversation" tab
- **THEN** the L2 messages are fetched via the `/messages` endpoint and displayed
- **AND** the messages are not loaded until the tab is clicked

#### Scenario: Delete memory from detail page
- **WHEN** a user clicks "Delete Memory" and confirms
- **THEN** the memory is deleted and the user is navigated back to the list

### Requirement: Memory config in org agent detail panel

The organization detail page's agent detail side panel SHALL include a "Memory Settings" section with: a checkbox for "Memory Enabled" (default checked), a provider dropdown for "Memory Provider" (optional), and a text input for "Memory Model" (optional). Changes SHALL be saved via the existing `updateOrgAgent` API call.

#### Scenario: Configure memory model for an agent
- **WHEN** a user opens the agent detail panel in the org chart
- **AND** sets Memory Provider to "openai" and Memory Model to "gpt-4o-mini"
- **AND** saves
- **THEN** the `OrganizationAgent` record is updated with `memory_provider: "openai"` and `memory_model: "gpt-4o-mini"`

#### Scenario: Disable memory for an agent
- **WHEN** a user unchecks "Memory Enabled" in the agent detail panel
- **THEN** the `OrganizationAgent` record is updated with `memory_enabled: false`
- **AND** the agent no longer generates or recalls memories during task delegation

### Requirement: Navigation to memories page

The system SHALL add a route for the memories page and provide navigation from the organization context (sidebar, toolbar, or org detail page).

#### Scenario: Route is accessible
- **WHEN** a user navigates to `#/organizations/{id}/memories`
- **THEN** the memories list page loads for that organization

### Requirement: View Memories link in agent detail panel

The agent detail side panel in the organization chart SHALL include a "View Memories" link that navigates to the memories list page pre-filtered to that agent.

#### Scenario: Navigate to agent's memories
- **WHEN** a user clicks "View Memories" in an agent's detail panel
- **THEN** the memories list page opens with the agent filter pre-selected
