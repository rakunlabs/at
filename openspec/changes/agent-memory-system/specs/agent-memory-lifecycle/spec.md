## ADDED Requirements

### Requirement: Memory extraction after task completion

The system SHALL generate and persist an `AgentMemory` entry after each task completes in `runOrgDelegation`, before calling `completeTaskWithStatus`. The extraction SHALL use an LLM call to produce L0 (one-sentence summary), L1 (structured decisions, approach, and notes), and tags (3-5 topic keywords) from the conversation history.

#### Scenario: Successful memory extraction
- **WHEN** a task completes in `runOrgDelegation` with conversation messages
- **THEN** an `AgentMemory` record is created with L0, L1, tags, and L2 messages
- **AND** the memory is linked to the agent, organization, and task

#### Scenario: Memory extraction with configured summarization model
- **WHEN** the agent's `OrganizationAgent` record has `memory_model` and `memory_provider` set
- **THEN** the summarization LLM call uses those provider and model values

#### Scenario: Memory extraction with default model
- **WHEN** the agent's `OrganizationAgent` record has no `memory_model` set
- **THEN** the summarization LLM call falls back to the agent's own provider and model

#### Scenario: Memory disabled for agent
- **WHEN** the agent's `OrganizationAgent` record has `memory_enabled` set to false
- **THEN** no memory extraction occurs and no `AgentMemory` record is created

#### Scenario: Memory extraction failure is non-fatal
- **WHEN** the summarization LLM call fails or the memory store write fails
- **THEN** the error is logged but the task still completes successfully
- **AND** the task result is not affected

### Requirement: Memory recall before agentic loop

The system SHALL load and inject relevant past memories into the agent's system prompt at the start of `runOrgDelegation`, before building the initial messages array. The recall SHALL search the agent's own memories and optionally cross-agent memories within the same organization.

#### Scenario: Agent recalls own past work
- **WHEN** an agent starts a new task in `runOrgDelegation`
- **AND** the agent has prior memories in this organization
- **THEN** relevant memories are scored, ranked, and the top matches' L1 summaries are appended to the system prompt under a "## Relevant Past Work" section

#### Scenario: Cross-agent memory recall
- **WHEN** an agent starts a new task
- **THEN** memories from other agents in the same organization are also considered
- **AND** cross-agent memories are labeled with the originating agent's name

#### Scenario: Token budget limits recall context
- **WHEN** multiple relevant memories exist
- **THEN** L1 summaries are included in order of relevance score until the token budget (default 2000 tokens) is exhausted
- **AND** remaining memories are excluded

#### Scenario: No memories exist
- **WHEN** an agent starts a task with no prior memories in the organization
- **THEN** the system prompt is unchanged (no "Relevant Past Work" section)

#### Scenario: Memory disabled for agent skips recall
- **WHEN** the agent's `OrganizationAgent` record has `memory_enabled` set to false
- **THEN** no memory recall occurs and no past work context is injected

### Requirement: Memory scoring algorithm

The system SHALL score candidate memories for relevance using: recency (0-30 points, linear decay), tag overlap with task words (20 points per matching tag, max 100), L0 keyword match against task title and description (0-50 points), own-memory bonus (+25 for agent's own memories), and parent-task bonus (+50 if memory's task is the current task's parent).

#### Scenario: Recent own memory about same topic scores highest
- **WHEN** an agent has a recent memory with matching tags and it is the agent's own memory
- **THEN** that memory scores higher than an older memory from another agent with fewer matching tags

#### Scenario: Parent task memory gets priority
- **WHEN** a task is a child of a previously completed task
- **AND** the parent task has an associated memory
- **THEN** that memory receives the parent-task bonus and ranks highly

### Requirement: Summarization prompt structure

The memory extraction LLM call SHALL use a structured prompt requesting: (1) SUMMARY — one sentence describing what was accomplished, (2) DECISIONS — key technical decisions made and their rationale, (3) APPROACH — how the work was done including files, tools, and patterns used, (4) TAGS — 3-5 topic keywords. The LLM response SHALL be parsed into L0 (SUMMARY), L1 (DECISIONS + APPROACH), and tags.

#### Scenario: Well-structured summarization output
- **WHEN** the summarization LLM is called with a completed task's conversation
- **THEN** the response contains distinct SUMMARY, DECISIONS, APPROACH, and TAGS sections
- **AND** these are parsed into the `AgentMemory` L0, L1, and tags fields

### Requirement: L2 conversation persistence

The full conversation messages array from the agentic loop SHALL be persisted as the L2 data in the `agent_memory_messages` table. This is stored as a JSON blob alongside the L0/L1 summary.

#### Scenario: Full conversation is recoverable
- **WHEN** a memory's L2 messages are loaded via `GetAgentMemoryMessages`
- **THEN** the returned messages match the original conversation from the agentic loop
