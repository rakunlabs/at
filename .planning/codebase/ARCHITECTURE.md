# Architecture

**Analysis Date:** 2026-03-08

## Pattern Overview

**Overall:** Monolithic Go application with clean layered architecture and dependency injection via constructor parameters.

**Key Characteristics:**
- Interface-driven design: all store and provider contracts defined as interfaces in a central `service` package
- Constructor injection: ~40 store interfaces injected individually into the `Server` constructor (no DI framework)
- Factory pattern for store backends (Postgres > SQLite > Memory fallback)
- Hot-reload of LLM providers without server restart
- Embedded SPA frontend served from the same binary
- DAG-based workflow engine with concurrent execution

## Layers

**Config Layer:**
- Purpose: Load and validate application configuration from YAML files, environment variables, Consul, and Vault
- Location: `internal/config/config.go`
- Contains: `Config` struct with nested `Server`, `Gateway`, `Store`, `Bots`, `Telemetry` configs; `LLMConfig` for provider definitions
- Depends on: `chu` config loader, `loaderenv` (AT_ prefix), `loaderconsul`, `loadervault`
- Used by: `cmd/at/main.go` for bootstrap

**Domain/Service Layer:**
- Purpose: Define all domain types, store interface contracts, and LLM provider interfaces. Pure types — no implementation.
- Location: `internal/service/at.go` (1443 lines — single source of truth)
- Contains: ~40 storer interfaces, domain types (Workflow, Agent, Task, Goal, Organization, etc.), LLM types (Message, LLMResponse, StreamChunk, ToolCall)
- Depends on: `config` (for LLMConfig in ProviderRecord), `query` (for ListResult pagination), `types` (for Null[T])
- Used by: Every other package — server, store, workflow, LLM providers
- Additional files:
  - `internal/service/client.go` — MCP client interface (`MCPClient`), HTTP/stdio MCP client implementations, `Tool` struct
  - `internal/service/client-stdio.go` — Stdio-based MCP client (subprocess communication)
  - `internal/service/stdio-manager.go` — `StdioProcessManager` for MCP subprocess lifecycles
  - `internal/service/schema.go` — JSON schema sanitization helpers

**LLM Provider Layer:**
- Purpose: Adapt multiple LLM APIs to a common `LLMProvider`/`LLMStreamProvider` interface
- Location: `internal/service/llm/`
- Contains: Four provider adapters + shared utilities
  - `internal/service/llm/openai/` — OpenAI-compatible API (also Copilot, Groq, Ollama, etc.)
  - `internal/service/llm/antropic/` — Anthropic Claude API (note: directory name is `antropic`, not `anthropic`)
  - `internal/service/llm/gemini/` — Google AI (Gemini) via API key
  - `internal/service/llm/vertex/` — Google Vertex AI via Application Default Credentials (ADC)
  - `internal/service/llm/common/` — Shared utilities across providers
- Depends on: `service` (interfaces), `config` (LLMConfig)
- Used by: `cmd/at/main.go` (provider factory), `server` (via provider registry)

**HTTP/Server Layer:**
- Purpose: HTTP routing, middleware chain, API handlers, gateway, embedded UI serving
- Location: `internal/server/` (64 files)
- Contains: Server struct with ~40 store references, route registration (~300+ endpoints), middleware chain, streaming SSE, bot adapters
- Key files:
  - `internal/server/server.go` (1207 lines) — Server struct, constructor, route registration, middleware
  - `internal/server/gateway.go` (977 lines) — OpenAI-compatible gateway (`/gateway/v1/chat/completions`, `/v1/models`)
  - `internal/server/translate.go` (603 lines) — OpenAI format ↔ internal type translation
  - `internal/server/chat.go` — Admin chat completions endpoint
  - `internal/server/chat-sessions.go` — Chat session management with agentic tool-calling loop
  - `internal/server/workflows.go` — Workflow CRUD + `RunWorkflowAPI`
  - `internal/server/triggers.go` — Trigger CRUD + `WebhookAPI`
  - `internal/server/provider.go` — Provider CRUD + hot-reload
  - `internal/server/builtin-tools.go` — Server-side builtin tools (http, bash, js, url_fetch)
  - `internal/server/bot-discord.go`, `internal/server/bot-telegram.go` — Bot adapters
- Depends on: `service`, `config`, `workflow`, `rag`, `cluster`, `ada` framework
- Used by: `cmd/at/main.go` (creates and starts server)

**Store Layer:**
- Purpose: Persist all domain entities to database
- Location: `internal/store/`
- Contains: Factory + three backend implementations, all implementing `StorerClose` (composition of ~40 storer interfaces + `Close()`)
  - `internal/store/store.go` — Factory: Postgres > SQLite > Memory fallback
  - `internal/store/postgres/` (39 files) — PostgreSQL backend with embedded migrations, `goqu` query builder
  - `internal/store/sqlite3/` (39 files) — SQLite backend with embedded migrations, `goqu` query builder
  - `internal/store/memory/` (29 files) — In-memory volatile store (no encryption, no persistence)
- Depends on: `service` (interfaces), `config` (store config), `crypto` (encryption)
- Used by: `cmd/at/main.go` (creates store), `server` (injected as individual storer interfaces)

**Workflow Engine Layer:**
- Purpose: Execute DAG-based workflows with concurrent node execution
- Location: `internal/service/workflow/`
- Contains:
  - `internal/service/workflow/engine.go` (634 lines) — `Engine.Run()`: reachableNodes → parseGraph → topoSort → execute
  - `internal/service/workflow/node.go` (438 lines) — `Noder` interface, `NodeResult` variants, `Registry`, lookup function types
  - `internal/service/workflow/scheduler.go` (438 lines) — Cron scheduler using `hardloop` library
  - `internal/service/workflow/handler.go` — `ExecuteJSHandler` and `ExecuteBashHandler` shared helpers
  - `internal/service/workflow/goja.go` (507 lines) — Goja JS VM setup with helper functions
  - `internal/service/workflow/nodes/` (28 files) — 21+ node type implementations registered via `init()`
- Depends on: `service` (types, provider/store interfaces), `render` (Go templates)
- Used by: `server` (RunWorkflowAPI, scheduler, webhook triggers)

**RAG Layer:**
- Purpose: Retrieval-Augmented Generation — embedding, ingestion, and vector search
- Location: `internal/service/rag/`
- Contains:
  - `internal/service/rag/rag.go` — `Service` struct, `Ingest()`, `Search()`, `IngestChunks()`
  - `internal/service/rag/embedder.go` — Embedding client creation
  - `internal/service/rag/loader.go` — Document loading and chunking
  - `internal/service/rag/vectorstore.go` — Vector store backend creation (pgvector, chroma, qdrant, etc.)
- Depends on: `service` (RAG collection types), `langchaingo` (embeddings, vectorstores, schema)
- Used by: `server` (RAG API endpoints, MCP RAG tools, workflow RAG nodes)

**Crypto Layer:**
- Purpose: AES-256-GCM encryption for sensitive data at rest
- Location: `internal/crypto/`
- Contains:
  - `internal/crypto/crypto.go` — `Encrypt()`, `Decrypt()`, `GenerateHash()` (AES-256-GCM)
  - `internal/crypto/config.go` — `DeriveKey()` (SHA-256 key derivation from passphrase)
- Depends on: Standard library crypto packages
- Used by: `store` (encrypts/decrypts provider credentials, secrets)

**Cluster Layer:**
- Purpose: Distributed coordination across multiple AT instances
- Location: `internal/cluster/cluster.go` (188 lines)
- Contains: `Cluster` struct wrapping `alan` library for UDP peer discovery, distributed locking, key rotation broadcast
- Depends on: `alan` library
- Used by: `server` (key rotation), `scheduler` (distributed lock for cron)

**Render Layer:**
- Purpose: Go text/template rendering with mustache support
- Location: `internal/render/render.go` (8 lines — thin wrapper)
- Depends on: `mugo/render` library
- Used by: Workflow template nodes, HTTP request nodes

**SkillMD Layer:**
- Purpose: Parse skill definitions from Markdown format
- Location: `internal/skillmd/parse.go`
- Used by: Server skill import endpoints

## Data Flow

**Gateway Request (LLM Chat):**

1. Client sends `POST /gateway/v1/chat/completions` with OpenAI-format request
2. `authenticateRequest()` checks Bearer token: config tokens first → DB tokens via `tokenStore.GetAPITokenByHash(sha256(token))`
3. Token scoping validated: allowed providers, models, expiry, usage limits
4. `parseModelID()` splits `"provider_key/actual_model"` from the `model` field
5. Provider looked up from `Server.providers` map (RWMutex-protected)
6. Request translated from OpenAI format to internal `[]service.Message` + `[]service.Tool` via `translate.go`
7. If `stream: true` and provider implements `LLMStreamProvider` → `ChatStream()` → SSE response
8. Otherwise → `Chat()` → single JSON response (or fake-streamed if `stream: true`)
9. Response translated back to OpenAI format
10. Usage tracked: per-token counters updated atomically in `tokenUsageStore`

**Workflow Execution (HTTP Trigger):**

1. Client sends `POST /webhooks/{id}` with JSON payload
2. `WebhookAPI` looks up trigger by ID or alias, validates auth (public vs Bearer token)
3. Workflow loaded from `workflowStore`, active version resolved from `workflowVersionStore`
4. `Engine.Run(ctx, graph, triggerNodeID, inputs)` called
5. Phase 1: `reachableNodes()` prunes unreachable subgraph → `parseGraph()` creates `Noder` instances via registered factories → `topoSort()` orders execution
6. Phase 2: Nodes execute in topological order. Each node gathers inputs from upstream, runs, returns `NodeResult` variant:
   - `NodeResult` → data flows to all downstream connections
   - `NodeResultSelection` → data routes only to selected output ports
   - `NodeResultFanOut` → spawns goroutine per item for parallel branches
7. First `output` node fires → sends result to `earlyOutput` channel for sync API response
8. Full execution continues in background for remaining branches

**Cron Workflow Trigger:**

1. `Scheduler.Start()` loads all enabled cron triggers from `triggerStore`
2. Each trigger registered as a `hardloop` cron job
3. On tick: workflow loaded, `Engine.Run()` called with `cron_trigger` as entry node
4. In clustered mode: distributed lock (`lockScheduler`) ensures only one instance runs cron

**Chat Session (Agentic Loop):**

1. `POST /api/v1/chat/sessions/{id}/messages` with user message
2. Agent loaded by session's `agent_id`, system prompt assembled (agent prompt + skills + memory + user preferences)
3. MCP tools collected from agent's MCP sets + skills + builtin tools
4. Agentic loop: send messages to LLM → if tool calls returned → execute tools → append results → loop until no more tool calls or max iterations
5. Budget checked before each LLM call via `checkBudget()`
6. Messages persisted to `chatSessionStore`
7. Response streamed via SSE to client

**State Management:**
- Server state: `Server.providers` map (RWMutex), `Server.activeRuns` (sync.Map), `Server.thoughtSigCache` (sync.Map)
- Workflow state: `Registry` per execution (thread-safe output aggregation, error collection)
- Store state: Database transactions for atomicity (task checkout, issue counter increment, key rotation)
- Cluster state: `alan` UDP peer discovery, distributed locks

## Key Abstractions

**LLMProvider / LLMStreamProvider:**
- Purpose: Uniform interface for all LLM API providers
- Examples: `internal/service/llm/openai/openai.go`, `internal/service/llm/antropic/antropic.go`, `internal/service/llm/gemini/gemini.go`, `internal/service/llm/vertex/vertex.go`
- Pattern: Each adapter translates between provider-native API format and internal `Message`/`LLMResponse`/`StreamChunk` types

**Noder (Workflow Node):**
- Purpose: Pluggable workflow node type with validate/run lifecycle
- Examples: `internal/service/workflow/nodes/llm-call.go`, `internal/service/workflow/nodes/agent-call.go`, `internal/service/workflow/nodes/conditional.go`
- Pattern: Factory registration via `init()` → `workflow.RegisterNodeType(typeName, factory)`. Return-type routing via `NodeResult`, `NodeResultSelection`, `NodeResultFanOut`

**StorerClose (Composite Store):**
- Purpose: Single interface composing all ~40 storer interfaces + `Close()`
- Examples: `internal/store/postgres/postgres.go`, `internal/store/sqlite3/sqlite3.go`, `internal/store/memory/memory.go`
- Pattern: Factory in `internal/store/store.go` selects backend based on config priority

**MCPClient:**
- Purpose: Communication with MCP (Model Context Protocol) tool servers
- Examples: `internal/service/client.go` (HTTP client), `internal/service/client-stdio.go` (stdio client)
- Pattern: JSON-RPC 2.0 protocol over HTTP or subprocess stdio

**Registry (Workflow):**
- Purpose: Shared state and lookup functions during workflow execution
- Examples: `internal/service/workflow/node.go` (definition), used by all node `Run()` methods
- Pattern: Carries provider/skill/variable lookups, collects outputs, tracks errors thread-safely

## Entry Points

**Application Bootstrap:**
- Location: `cmd/at/main.go`
- Triggers: `go run cmd/at/main.go` or compiled binary
- Responsibilities: Load config → create providers from YAML → create store → load DB providers (override YAML) → create cluster → create server → start HTTP

**HTTP Server:**
- Location: `internal/server/server.go` → `Server.Start()`
- Triggers: Bootstrap complete
- Responsibilities: Listen on `host:port`, serve all API routes + embedded SPA

**Gateway Endpoint:**
- Location: `internal/server/gateway.go` → `ChatCompletions()`
- Triggers: `POST /gateway/v1/chat/completions`
- Responsibilities: Authenticate, route to provider, translate formats, stream/respond

**Webhook Endpoint:**
- Location: `internal/server/triggers.go` → `WebhookAPI()`
- Triggers: `POST /webhooks/{id}`
- Responsibilities: Validate trigger, execute workflow

**Cron Scheduler:**
- Location: `internal/service/workflow/scheduler.go` → `Scheduler.Start()`
- Triggers: Server startup (if triggerStore configured)
- Responsibilities: Load cron triggers, execute workflows on schedule

## Error Handling

**Strategy:** Error wrapping with context using `fmt.Errorf("context: %w", err)`. Sentinel errors for specific conditions.

**Patterns:**
- Always wrap errors: `fmt.Errorf("failed to parse config: %w", err)`
- Not-found returns nil: `if errors.Is(err, sql.ErrNoRows) { return nil, nil }`
- Workflow branch termination: `ErrStopBranch` sentinel (graceful, non-propagating)
- JS VM panics: `vm.NewTypeError()` in Goja layer only — never panics elsewhere
- HTTP error responses: `httpResponse(w, message, statusCode)` helper
- Non-fatal errors logged and skipped: provider creation failures, scheduler start failures, bot start failures

## Cross-Cutting Concerns

**Logging:** `log/slog` structured logging with `logi.Ctx(ctx)` for contextual fields. Key convention: `"error"` for error values.

**Validation:** Request validation in HTTP handlers (check required fields, parse IDs). Node validation in `Validate()` method during workflow phase 1.

**Authentication:**
- Gateway: Bearer tokens (config tokens → DB tokens via SHA-256 hash lookup)
- Admin endpoints: `adminAuthMiddleware` checks `config.AdminToken`
- Base API group: Optional forward auth middleware (`mforwardauth`)
- Webhooks: Per-trigger `public` flag; non-public requires Bearer token matching gateway auth

**Middleware Chain:** recover → server → CORS → requestid → log → telemetry → [forward-auth on base group] → [admin-token on settings]

**Encryption:** AES-256-GCM for sensitive fields at rest. `enc:` prefix on stored strings signals encrypted value. Key derived via SHA-256 from config passphrase.

**Distributed Coordination:** Optional `alan`-based clustering for key rotation broadcast and cron scheduler locking.

---

*Architecture analysis: 2026-03-08*
