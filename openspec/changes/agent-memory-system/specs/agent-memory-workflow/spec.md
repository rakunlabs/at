## ADDED Requirements

### Requirement: memory_config node recall mode

The `memory_config` workflow node SHALL support a `mode` configuration field with two values: `"static"` (default, current pass-through behavior) and `"recall"` (queries the AgentMemory store for relevant memories).

#### Scenario: Static mode preserves backward compatibility
- **WHEN** a `memory_config` node has `mode: "static"` or no mode configured
- **THEN** it passes through input data to the output port unchanged (current behavior)

#### Scenario: Recall mode queries memory store
- **WHEN** a `memory_config` node has `mode: "recall"` configured
- **AND** it has `agent_id` and `organization_id` in its config
- **THEN** it uses the input data as a query string and calls `MemoryRecall` from the Registry
- **AND** outputs the formatted memory results on the `"memory"` port

### Requirement: memory_config recall mode configuration

In recall mode, the `memory_config` node SHALL accept these config fields: `agent_id` (required), `organization_id` (required), `cross_agent` (bool, default true), and `max_tokens` (int, default 2000). The `agent_id` and `organization_id` MAY reference workflow variables using the existing variable resolution pattern.

#### Scenario: Recall with cross-agent enabled
- **WHEN** `cross_agent` is true
- **THEN** the recall includes memories from all agents in the organization

#### Scenario: Recall with cross-agent disabled
- **WHEN** `cross_agent` is false
- **THEN** the recall only includes memories from the specified agent

#### Scenario: Token budget limits output
- **WHEN** `max_tokens` is set to 1000
- **THEN** the combined L1 summaries in the output do not exceed approximately 1000 tokens

### Requirement: MemoryRecall function in Registry

The workflow `Registry` SHALL include a `MemoryRecall` field of type `MemoryRecallFunc`. This function takes `ctx`, `agentID`, `orgID`, `query` string, `crossAgent` bool, and `maxTokens` int, and returns formatted memory context as a string. It SHALL be nil when the agent memory store is not configured.

#### Scenario: Registry MemoryRecall is wired
- **WHEN** the server creates a workflow Registry
- **AND** the agent memory store is configured
- **THEN** `Registry.MemoryRecall` is a non-nil function that queries the memory store

#### Scenario: Registry MemoryRecall handles nil gracefully
- **WHEN** `Registry.MemoryRecall` is nil (store not configured)
- **AND** a `memory_config` node with `mode: "recall"` executes
- **THEN** the node logs a warning and falls back to static mode (pass-through)

### Requirement: memory_config node output format for recall mode

In recall mode, the `memory_config` node SHALL output a formatted string containing relevant memories. Each memory SHALL include the agent name, task identifier, L0 summary, and L1 details. Cross-agent memories SHALL be labeled with the originating agent's name.

#### Scenario: Formatted recall output
- **WHEN** recall mode finds 3 relevant memories
- **THEN** the output string contains each memory with agent name, task identifier, and L1 content
- **AND** the output is suitable for direct injection into an LLM prompt via the agent_call memory port
