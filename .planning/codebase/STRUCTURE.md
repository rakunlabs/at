# Codebase Structure

**Analysis Date:** 2026-03-08

## Directory Layout

```
at/
├── cmd/at/                      # Application entry point
│   └── main.go                  # Bootstrap: config → store → providers → server
├── internal/                    # All Go packages (not importable externally)
│   ├── cluster/                 # Distributed coordination (alan library)
│   ├── config/                  # Application configuration structs
│   ├── crypto/                  # AES-256-GCM encryption, key derivation
│   ├── render/                  # Go template rendering (thin wrapper)
│   ├── server/                  # HTTP handlers, routing, middleware, embedded UI
│   │   ├── dist/                # Embedded SPA build output (//go:embed)
│   │   ├── mcp_templates/       # JSON templates for MCP server presets
│   │   └── skill_templates/     # JSON templates for skill presets
│   ├── service/                 # Domain types, interfaces (pure contracts)
│   │   ├── llm/                 # LLM provider adapters
│   │   │   ├── antropic/        # Anthropic Claude adapter
│   │   │   ├── common/          # Shared provider utilities
│   │   │   ├── gemini/          # Google AI (Gemini) adapter
│   │   │   ├── openai/          # OpenAI-compatible adapter
│   │   │   └── vertex/          # Google Vertex AI adapter
│   │   ├── rag/                 # RAG: embedder, loader, vectorstore
│   │   └── workflow/            # DAG workflow engine
│   │       └── nodes/           # 21+ pluggable node type implementations
│   ├── skillmd/                 # Markdown skill parser
│   └── store/                   # Persistence layer
│       ├── memory/              # In-memory store (volatile, no encryption)
│       ├── postgres/            # PostgreSQL backend
│       │   └── migrations/      # 47 sequential SQL migration files
│       └── sqlite3/             # SQLite backend
│           └── migrations/      # Sequential SQL migration files
├── _ui/                         # Svelte 5 SPA frontend
│   └── src/
│       ├── lib/
│       │   ├── api/             # TypeScript API client modules (36 files)
│       │   ├── components/      # Reusable Svelte components
│       │   │   └── workflow/    # Workflow editor node components (50 files)
│       │   ├── helper/          # Utility functions (format, markdown, sort)
│       │   └── store/           # Svelte stores (global state, toast)
│       ├── pages/               # Route-level page components (25 files)
│       └── style/               # Global CSS
├── _docs/                       # VitePress documentation site
├── .github/workflows/           # CI: test.yml, tag.yml
├── assets/                      # Static assets (logo)
├── ci/                          # Dockerfile for container builds
├── env/                         # compose.yaml for local dev (Postgres)
├── .goreleaser.yaml             # GoReleaser build configuration
├── at.yaml                      # Runtime configuration file
├── go.mod                       # Go module definition
├── go.sum                       # Go dependency checksums
└── Makefile                     # Build, run, test commands
```

## Directory Purposes

**`cmd/at/`:**
- Purpose: Application bootstrap — the only `main` package
- Contains: Single `main.go` (218 lines) that wires config → store → providers → cluster → server
- Key files: `cmd/at/main.go`

**`internal/server/`:**
- Purpose: HTTP layer — all API endpoints, middleware, gateway, bot adapters, embedded UI
- Contains: 64 Go files, each handling a specific resource or concern
- Key files:
  - `internal/server/server.go` — Server struct, constructor, route registration (1207 lines)
  - `internal/server/gateway.go` — OpenAI-compatible gateway endpoint (977 lines)
  - `internal/server/translate.go` — Format translation between OpenAI and internal types (603 lines)
  - `internal/server/chat.go` — Admin chat completions
  - `internal/server/chat-sessions.go` — Agentic chat session management
  - `internal/server/workflows.go` — Workflow CRUD and execution
  - `internal/server/provider.go` — Provider CRUD with hot-reload
  - `internal/server/builtin-tools.go` — Server-side tool implementations (http, bash, js, url_fetch)
- Naming: Each file maps to a domain resource (e.g., `agents.go`, `tasks.go`, `goals.go`)

**`internal/service/`:**
- Purpose: Domain types and interface contracts — the central dependency for all packages
- Contains: Core `at.go` (1443 lines) defining all types and ~40 storer interfaces, plus MCP client code
- Key files:
  - `internal/service/at.go` — ALL domain types and store interfaces (single source of truth)
  - `internal/service/client.go` — MCP client interface and HTTP implementation
  - `internal/service/client-stdio.go` — Stdio MCP client
  - `internal/service/stdio-manager.go` — MCP subprocess lifecycle manager
  - `internal/service/schema.go` — JSON schema sanitization helpers

**`internal/service/llm/`:**
- Purpose: LLM provider adapters implementing `LLMProvider` / `LLMStreamProvider`
- Contains: Four provider directories + shared common utilities
- Key files:
  - `internal/service/llm/openai/openai.go` — OpenAI-compatible (also Copilot, Groq, Ollama)
  - `internal/service/llm/antropic/antropic.go` — Anthropic Claude
  - `internal/service/llm/gemini/gemini.go` — Google AI (Gemini)
  - `internal/service/llm/vertex/vertex.go` — Google Vertex AI (ADC auth)
  - `internal/service/llm/common/content.go` — Shared content processing

**`internal/service/workflow/`:**
- Purpose: DAG-based workflow execution engine
- Contains: Engine, scheduler, JS VM, node interface, and 21+ node type implementations
- Key files:
  - `internal/service/workflow/engine.go` — `Engine.Run()`: parse → topoSort → execute (634 lines)
  - `internal/service/workflow/node.go` — `Noder` interface, `Registry`, result types (438 lines)
  - `internal/service/workflow/scheduler.go` — Cron scheduler with distributed locking (438 lines)
  - `internal/service/workflow/goja.go` — Goja JavaScript VM setup (507 lines)
  - `internal/service/workflow/handler.go` — Shared JS/Bash execution helpers
  - `internal/service/workflow/nodes/register.go` — Node type factory registration

**`internal/service/workflow/nodes/`:**
- Purpose: Individual workflow node type implementations
- Contains: 28 files — each implements the `Noder` interface via factory pattern
- Key files (representative):
  - `internal/service/workflow/nodes/llm-call.go` — LLM provider invocation node
  - `internal/service/workflow/nodes/agent-call.go` — Agent invocation with tool loop
  - `internal/service/workflow/nodes/conditional.go` — Conditional branching
  - `internal/service/workflow/nodes/loop.go` — Loop iteration with fan-out
  - `internal/service/workflow/nodes/script.go` — JavaScript execution via Goja
  - `internal/service/workflow/nodes/http-request.go` — HTTP request node
  - `internal/service/workflow/nodes/register.go` — Central registration of all node factories

**`internal/service/rag/`:**
- Purpose: Retrieval-Augmented Generation — embedding, ingestion, vector search
- Contains: 4 files covering the RAG pipeline
- Key files:
  - `internal/service/rag/rag.go` — RAG service: `Ingest()`, `Search()`, `IngestChunks()`
  - `internal/service/rag/embedder.go` — Embedding client creation
  - `internal/service/rag/loader.go` — Document loading and chunking
  - `internal/service/rag/vectorstore.go` — Vector store backend creation

**`internal/store/`:**
- Purpose: Database persistence — factory + three backends
- Contains: Factory file + three parallel backend directories
- Key files:
  - `internal/store/store.go` — Factory: selects Postgres > SQLite > Memory based on config
  - `internal/store/postgres/postgres.go` — PostgreSQL backend constructor
  - `internal/store/sqlite3/sqlite3.go` — SQLite backend constructor
  - `internal/store/memory/memory.go` — In-memory backend constructor

**`internal/store/postgres/` and `internal/store/sqlite3/`:**
- Purpose: Relational DB backends — each file implements one storer interface
- Contains: 39 Go files each (1:1 mapping to storer interfaces) + `migrations/` directory
- Naming: Files match resource names (e.g., `agents.go`, `tasks.go`, `workflows.go`)
- Key files:
  - `postgres/migrate.go` / `sqlite3/migrate.go` — Migration runner
  - `postgres/utils.go` / `sqlite3/utils.go` — Shared query helpers, encryption wrappers
  - `postgres/migrations/` — 47 numbered SQL files (e.g., `1_create_providers.sql` through `47_add_organization_canvas_layout.sql`)

**`internal/store/memory/`:**
- Purpose: In-memory volatile store — used as fallback when no DB configured
- Contains: 29 Go files (subset of features — no encryption, no migrations)

**`internal/crypto/`:**
- Purpose: AES-256-GCM encryption for data at rest
- Contains: 3 files
- Key files:
  - `internal/crypto/crypto.go` — `Encrypt()`, `Decrypt()`, `GenerateHash()`
  - `internal/crypto/config.go` — `DeriveKey()` (SHA-256 from passphrase)
  - `internal/crypto/crypto_test.go` — Unit tests

**`internal/cluster/`:**
- Purpose: Distributed coordination for multi-instance deployments
- Contains: Single file `cluster.go` (188 lines)

**`internal/config/`:**
- Purpose: Application configuration structs
- Contains: Single file `config.go` (337 lines) — nested structs for all config areas

**`internal/render/`:**
- Purpose: Go template rendering
- Contains: Single file `render.go` (8 lines — thin wrapper around `mugo/render`)

**`internal/skillmd/`:**
- Purpose: Parse skill definitions from Markdown format
- Contains: `parse.go` and `parse_test.go`

**`_ui/`:**
- Purpose: Svelte 5 SPA frontend — built to `internal/server/dist/` for embedding
- Contains: Full Svelte/Vite/TypeScript project
- Key files:
  - `_ui/src/App.svelte` — Root component with router
  - `_ui/src/routes.ts` — Route definitions
  - `_ui/src/main.ts` — Application entry point

**`_ui/src/pages/`:**
- Purpose: Top-level page components (one per route)
- Contains: 25 Svelte files (e.g., `Agents.svelte`, `Workflows.svelte`, `Chat.svelte`, `Tasks.svelte`)

**`_ui/src/lib/api/`:**
- Purpose: TypeScript API client modules — one per backend resource
- Contains: 36 TypeScript files (e.g., `agents.ts`, `workflows.ts`, `providers.ts`)

**`_ui/src/lib/components/`:**
- Purpose: Reusable UI components
- Contains: 12 general components + `workflow/` subdirectory with 50 workflow node components
- Key files: `DataTable.svelte`, `Navbar.svelte`, `Sidebar.svelte`, `KanbanBoard.svelte`, `Pagination.svelte`

**`_ui/src/lib/components/workflow/`:**
- Purpose: Visual workflow editor node components
- Contains: 50 Svelte files — pairs of `{NodeType}Node.svelte` (visual) + `{NodeType}Props.svelte` (properties panel)
- Pattern: Each workflow node type has a corresponding pair (e.g., `LLMCallNode.svelte` + `LLMCallProps.svelte`)

**`_docs/`:**
- Purpose: VitePress documentation site
- Contains: Markdown docs (`getting-started.md`, `skills.md`, `bots.md`) + VitePress config

## Key File Locations

**Entry Points:**
- `cmd/at/main.go`: Application bootstrap (Go)
- `_ui/src/main.ts`: Frontend entry point (Svelte/TS)
- `internal/server/server.go`: HTTP server start

**Configuration:**
- `at.yaml`: Runtime config (providers, gateway, store, bots)
- `internal/config/config.go`: Config struct definitions
- `.goreleaser.yaml`: Release build config
- `_ui/vite.config.js`: Vite build config
- `_ui/tsconfig.json`: TypeScript config
- `_ui/svelte.config.js`: Svelte config

**Core Logic:**
- `internal/service/at.go`: All domain types and interfaces
- `internal/server/gateway.go`: OpenAI-compatible gateway
- `internal/server/chat-sessions.go`: Agentic chat loop
- `internal/service/workflow/engine.go`: Workflow execution engine
- `internal/service/workflow/nodes/register.go`: Node type registry

**Testing:**
- `internal/crypto/crypto_test.go`: Crypto unit tests
- `internal/service/schema_test.go`: Schema sanitization tests
- `internal/service/workflow/engine_test.go`: Workflow engine tests
- `internal/service/workflow/nodes/nodes_test.go`: Node type tests
- `internal/server/gateway-rag-mcp_test.go`: RAG MCP gateway tests

**Build & CI:**
- `Makefile`: All build/run/test targets
- `.github/workflows/test.yml`: CI test pipeline
- `.github/workflows/tag.yml`: Release pipeline
- `ci/Dockerfile`: Container build

**Database Migrations:**
- `internal/store/postgres/migrations/`: 47 numbered SQL files
- `internal/store/sqlite3/migrations/`: Matching migration files

## Naming Conventions

**Go Files:**
- Pattern: Lowercase, hyphen-separated (e.g., `api-tokens.go`, `http-request.go`, `chat-sessions.go`)
- Exception: Some use underscores for compound domain names (e.g., `rag_states.go`, `workflow_versions.go`)
- Test files: `*_test.go` suffix (standard Go convention)

**Go File-to-Resource Mapping:**
- Server handler files match the resource they manage: `agents.go` → Agent CRUD handlers
- Store files match the storer interface they implement: `agents.go` → `AgentStorer` methods
- This pattern is consistent across `internal/server/`, `internal/store/postgres/`, `internal/store/sqlite3/`, `internal/store/memory/`

**Svelte Files:**
- Pages: PascalCase (e.g., `Agents.svelte`, `WorkflowEditor.svelte`, `ChatSessions.svelte`)
- Components: PascalCase (e.g., `DataTable.svelte`, `KanbanBoard.svelte`)
- Workflow nodes: `{NodeType}Node.svelte` + `{NodeType}Props.svelte` pairs

**TypeScript Files:**
- API modules: Lowercase, hyphen-separated matching backend resources (e.g., `agents.ts`, `chat-sessions.ts`)
- Helpers: Lowercase, hyphen-separated (e.g., `config-snippet.ts`, `format.ts`)
- Stores: `*.svelte.ts` suffix (e.g., `store.svelte.ts`, `toast.svelte.ts`)

**SQL Migrations:**
- Pattern: `{number}_{description}.sql` (e.g., `1_create_providers.sql`, `47_add_organization_canvas_layout.sql`)
- Sequential numbering, no gaps expected

**Directories:**
- Go packages: Lowercase, single-word or hyphenated (e.g., `workflow`, `skillmd`)
- LLM providers: Provider name as directory (e.g., `openai/`, `antropic/`, `gemini/`, `vertex/`)
- Frontend: Standard conventions (`lib/`, `pages/`, `components/`, `api/`)

## Where to Add New Code

**New API Endpoint (Go):**
1. Add types/interfaces to `internal/service/at.go` (storer interface + domain types)
2. Add store implementation in all three backends:
   - `internal/store/postgres/{resource}.go`
   - `internal/store/sqlite3/{resource}.go`
   - `internal/store/memory/{resource}.go`
3. Add migration file in `internal/store/postgres/migrations/` and `internal/store/sqlite3/migrations/` (next sequential number)
4. Add HTTP handler file in `internal/server/{resource}.go`
5. Register routes in `internal/server/server.go` (inside the route registration section)
6. Inject storer into Server constructor (update `NewServer` parameters)

**New LLM Provider:**
1. Create directory `internal/service/llm/{provider}/`
2. Add `{provider}.go` implementing `service.LLMProvider` (and optionally `service.LLMStreamProvider`)
3. Add optional `auth.go` for custom auth logic
4. Register provider type in `cmd/at/main.go` provider factory switch

**New Workflow Node Type:**
1. Create `internal/service/workflow/nodes/{node-type}.go`
2. Implement `Noder` interface (Validate + Run methods)
3. Add factory registration via `init()` calling `workflow.RegisterNodeType()`
4. Add UI components in `_ui/src/lib/components/workflow/`:
   - `{NodeType}Node.svelte` — visual representation
   - `{NodeType}Props.svelte` — properties panel

**New Frontend Page:**
1. Create `_ui/src/pages/{PageName}.svelte`
2. Add route in `_ui/src/routes.ts`
3. Add API module in `_ui/src/lib/api/{resource}.ts`
4. Add navigation entry in `_ui/src/lib/components/Sidebar.svelte`

**New Reusable Component:**
- General components: `_ui/src/lib/components/{ComponentName}.svelte`
- Workflow-specific: `_ui/src/lib/components/workflow/{Name}.svelte`

**Utility Functions:**
- Go: Add to existing package or create new `internal/{package}/` directory
- TypeScript: Add to `_ui/src/lib/helper/{name}.ts`

**New Database Migration:**
- Postgres: `internal/store/postgres/migrations/{next_number}_{description}.sql`
- SQLite: `internal/store/sqlite3/migrations/{next_number}_{description}.sql`
- Current highest: 47 (Postgres), keep in sync across both backends

## Special Directories

**`internal/server/dist/`:**
- Purpose: Embedded SPA build output served by Go binary
- Generated: Yes — by `make build-ui` (copies from `_ui/dist/`)
- Committed: Only `.gitkeep` is committed; actual build artifacts are in `.gitignore` of `internal/server/`
- Note: Contains `//go:embed dist/*` directive in `internal/server/server.go`

**`internal/store/*/migrations/`:**
- Purpose: Sequential SQL migration files executed on startup
- Generated: No — manually authored
- Committed: Yes — part of the source code

**`internal/server/mcp_templates/`:**
- Purpose: Preset MCP server configuration templates (JSON)
- Generated: No — manually authored
- Committed: Yes
- Contains: 6 JSON files (context7, grep-app, playwright, rag-knowledge-base, rag-search-only, sentry)

**`internal/server/skill_templates/`:**
- Purpose: Preset skill configuration templates (JSON)
- Generated: No — manually authored
- Committed: Yes
- Contains: 8 JSON files (current-datetime, github-issues, gmail-reader, google-calendar, jira-tasks, json-api, slack-messages, web-scraper)

**`_ui/node_modules/` and `_docs/node_modules/`:**
- Purpose: NPM dependencies
- Generated: Yes — by `pnpm install`
- Committed: No

**`dist/`:**
- Purpose: GoReleaser build output
- Generated: Yes — by `make build`
- Committed: No (in `.gitignore`)

**`.planning/`:**
- Purpose: GSD planning and codebase analysis documents
- Generated: By analysis tooling
- Committed: May be committed for reference

---

*Structure analysis: 2026-03-08*
