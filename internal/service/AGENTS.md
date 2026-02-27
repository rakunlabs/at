# internal/service — Domain Types & Interfaces

## Purpose

Defines all domain types and store interface contracts. No implementation — pure types and interfaces consumed by server, store, and workflow packages.

## Key Files

- `at.go` (429 lines) — ALL domain types and store interfaces. Single source of truth for contracts.
- `client.go` — MCP client types: `Tool` struct (Name, Description, InputSchema, Handler, HandlerType), MCP request/response types
- `schema.go` — JSON schema sanitization helpers

## Core Interfaces (at.go)

**LLM Provider**:
- `LLMProvider` — `Chat(ctx, model string, []Message, []Tool) (*LLMResponse, error)`
- `LLMStreamProvider` — `ChatStream(ctx, model string, []Message, []Tool) (<-chan StreamChunk, error)`

**Store Contracts** (each is CRUD):
- `ProviderStorer` — List/Get/Create/Update/DeleteProvider
- `APITokenStorer` — List/GetByHash/Create/Update/Delete/UpdateLastUsed
- `WorkflowStorer` — List/Get/Create/Update/Delete workflows
- `TriggerStorer` — List/Get/GetByAlias/Create/Update/Delete + `ListEnabledCronTriggers`
- `SkillStorer` — List/Get/GetByName/Create/Update/Delete
- `VariableStorer` — List/Get/GetByKey/Create/Update/Delete
- `NodeConfigStorer` — List/ListByType/Get/Create/Update/Delete
- `KeyRotator` — `RotateEncryptionKey(ctx, newKey []byte) error`
- `EncryptionKeyUpdater` — `SetEncryptionKey(newKey []byte)`

## Core Types (at.go)

- `Message` — `{Role string, Content any}` — conversation unit
- `LLMResponse` — `{Content, InlineImages, ToolCalls, Finished, Usage}`
- `StreamChunk` — partial streaming response
- `ToolCall` — `{ID, Name, Arguments, ThoughtSignature}`
- `ProviderRecord` — persisted provider config
- `APIToken` — token with scoping (AllowedProviders/Models/Webhooks)
- `Workflow`, `Trigger`, `Skill`, `Variable`, `NodeConfig` — persistent entities

## Provider Implementations (llm/)

Four adapters, each implementing `LLMProvider` + optional `LLMStreamProvider`:

| Directory | Type | Notes |
|---|---|---|
| `llm/openai/` | `openai` | OpenAI-compatible; handles Copilot/device auth via TokenSource |
| `llm/antropic/` | `anthropic` | Anthropic Claude API adapter |
| `llm/gemini/` | `gemini` | Google AI (Gemini) via API key |
| `llm/vertex/` | `vertex` | Vertex AI via ADC auth |

Pattern: each has a main `.go` file implementing Chat/ChatStream + type-specific request translation.
