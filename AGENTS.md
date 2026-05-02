# AT — LLM Gateway + Workflow Engine

## What This Is

OpenAI-compatible LLM gateway that routes requests to multiple providers (OpenAI, Anthropic, Vertex AI, Gemini) through a single `/gateway/v1/chat/completions` endpoint. Includes a DAG-based workflow engine and Svelte admin UI.

Module: `github.com/rakunlabs/at` — Go 1.26

## Architecture

```
cmd/at/main.go              → bootstrap: config → store → providers → server.Start
internal/server/            → HTTP handlers (ada framework), middleware, static UI
internal/service/           → domain types + store interfaces (at.go)
internal/service/workflow/  → DAG engine: parse → topoSort → run (concurrent fan-out)
internal/service/workflow/nodes/ → node types registered via init()
internal/service/llm/       → provider adapters: openai/, antropic/, gemini/, vertex/
internal/store/             → store factory → postgres | sqlite3 (default: sqlite at ./data/at.db)
internal/crypto/            → AES-256-GCM credential encryption, key rotation
_ui/                        → Svelte 5 + Vite 6 + TailwindCSS 4 SPA
```

## Build, Test, Lint

```sh
# Go
make run                # go run cmd/at/main.go
make test               # go test -v -race ./...
make lint               # golangci-lint run ./... (default config, no .golangci.yml)
make build              # goreleaser build --snapshot (builds UI first)

# Single test
go test -v -race -run TestName ./path/to/package
# Example: go test -v -race -run TestGenerateHash ./internal/crypto

# Package tests
go test -v -race ./internal/service/...

# UI
make install-ui         # pnpm install in _ui/
make run-ui             # vite dev server (localhost:3000, proxies api to :8080)
cd _ui && pnpm run check   # svelte-check (TypeScript + Svelte type checking)
cd _ui && pnpm run build   # production build

# Infrastructure
make env                # docker compose up (postgres for local dev)
make env-down           # docker compose down --volumes
```

## Loop Governor

The agentic loops (`internal/server/org-delegation.go`, `internal/server/chat-sessions.go`, `internal/service/workflow/nodes/agent-call.go`) are governed by `internal/service/loopgov`, which enforces:

- A sliding-window message budget on every `provider.Chat` call (optional rolling-summary fallback; default is "drop oldest")
- A platform ceiling on iteration counts (clamps per-agent / per-task `max_iterations`)
- A single global byte cap on tool results before they enter the LLM message history; the full payload is dumped to the workspace as `.at-tool-output/<run-id>/<tool>-<seq>.txt` so the agent can read it on demand
- A `LIMIT` on `ListChatMessages` reads in the chat-session loop

Defaults are baked into `loopgov.fillDefaults` (no YAML / env knobs):

| Default | Value | Purpose |
|---|---|---|
| `WindowTokens` | 32768 | Input-token budget per Chat call |
| `SummaryTokens` | 2000 | Cap on rolling-summary message (when summarizer is wired) |
| `SummaryTimeout` | 10s | Bound on summarisation call |
| `MaxIterCeiling` | 60 | Platform iteration ceiling |
| `ToolResultMaxBytes` | 65536 | Inline cap on every tool result; full payload spilled to dump file |
| `ChatHistoryLimit` | 200 | Messages reloaded per chat turn |
| `WorkspaceRoot` | `/tmp/at-tasks` | Where `.at-tool-output/<run-id>/...` dumps land |

**No output-token cap.** Providers and agent configs already define per-model `max_tokens`. An earlier revision shipped a 4096-token platform cap; it broke structured outputs (e.g. multi-scene Script Writer JSON for video shorts) and was removed. `Governor.ChatOptions()` now always returns `nil`, the documented "no cap" sentinel for every provider adapter.

**No per-tool / per-class byte caps.** Earlier revisions classified tools (`executable`, `structured`, `freeform`) and applied per-class caps with overrides for `task_get` / `task_list`. Those over-truncated structured tool outputs (notably the video-generation suite — FAL Veo, Sora, Runway — and the `delegate_to_*` channel that carries full script JSON between agents). We now use a single generous global cap and rely on the workspace dump file to preserve the original payload, which the agent can read via `file_read` or `bash_execute cat`.

To override, edit the constants in `internal/service/loopgov/config.go` or add UI-driven configuration in a follow-up change. The `Disabled` field exists in `loopgov.Config` as an in-code rollback switch but is not exposed via YAML.

**Breaking change**: workflow `agent_call` nodes no longer accept `max_iterations: 0` (legacy "unlimited" mode). Existing graphs are migrated to the platform ceiling on server startup.

## Runtime configuration

LLM providers, gateway API tokens, and bot adapters are configured at runtime through the UI (`/api/v1/providers`, `/api/v1/api-tokens`, `/api/v1/bots`) and persisted in the database. They are NOT accepted via YAML or env. The only YAML / env knobs are bootstrap-only: log level, server bind, store backend, telemetry.

## Go Code Style

### Formatting & Imports
- Run `gofmt` on all files. No extra formatter config.
- Group imports in order, separated by blank lines:
  1. Standard library (`"fmt"`, `"context"`, `"net/http"`)
  2. Third-party (`"github.com/worldline-go/types"`, `"github.com/doug-martin/goqu/v9"`)
  3. Internal (`"github.com/rakunlabs/at/internal/..."`)

### Naming Conventions
- **Files**: lowercase with hyphens (`api-tokens.go`, `http-request.go`)
- **Interfaces**: `-er` suffix (`ProviderStorer`, `KeyRotator`, `EncryptionKeyUpdater`)
- **Structs/Functions**: PascalCase exported, camelCase unexported
- **Variables**: short and descriptive (`ctx`, `err`, `cfg`, `req`, `w`, `r`)
- **JSON tags**: snake_case (`json:"api_key"`, `json:"created_at"`)
- **DB tags**: snake_case on private row structs (`db:"organization_id"`)
- **Config tags**: `cfg:"name"` with flags like `no_prefix`, `default:"value"`

### Types & Data Structures
- **Nullables**: `types.Null[T]` from `github.com/worldline-go/types` for optional DB fields
- **Slices**: `types.Slice[T]` when custom JSON marshaling is needed, otherwise `[]T`
- **IDs**: generated with `ulid.Make().String()`
- **Timestamps**: `time.Now().UTC().Format(time.RFC3339)`
- **Context**: always first param for I/O functions: `func Foo(ctx context.Context, ...) error`

### Error Handling
- **Always wrap** with context: `fmt.Errorf("failed to create provider: %w", err)`
- **Not-found**: `errors.Is(err, sql.ErrNoRows)` → return `nil, nil` (not an error)
- **No panics** except in Goja JS VM layer (`vm.NewTypeError`)

### Logging
- `slog.Info`, `slog.Error`, etc. with structured fields
- Use `logi.Ctx(ctx)` for contextual logger
- Error values use key `"error"`: `slog.String("error", err.Error())`

### HTTP Handlers (ada framework)
- Signature: methods on `*Server` — `func (s *Server) ListFooAPI(w http.ResponseWriter, r *http.Request)`
- Path params: `r.PathValue("id")` (Go 1.22+ stdlib routing)
- Query parsing: `query.Parse(r.URL.RawQuery)` from `rakunlabs/query` — generic filtering
- Request body: `json.NewDecoder(r.Body).Decode(&req)` with inline struct
- Responses: `httpResponse(w, "message", statusCode)` or `httpResponseJSON(w, data, statusCode)`
- Nil store guard: `if s.store == nil { httpResponse(w, "store not configured", 503); return }`

### Store Pattern
- Three backends (`postgres/`, `sqlite3/`, `memory/`) implement interfaces from `service/at.go`
- Private `fooRow` struct with `db:"..."` tags, converted via `fooRowToRecord(row)`
- SQL built with `goqu` query builder
- Updates re-fetch after write; `RowsAffected() == 0` → return `nil, nil`
- Factory: `store.New(ctx, cfg)` tries postgres → sqlite3 (default: on-disk sqlite at `./data/at.db` when no backend is configured; the parent directory is auto-created so Docker users can bind-mount a volume to `/data`)

### Tests
- Standard `testing` package, table-driven with `t.Run`
- Pattern: `tests := []struct{name string; ...}{}` → `for _, tt := range tests { t.Run(tt.name, ...) }`
- Hand-written mock structs (no mock framework)
- HTTP tests: `httptest.NewRequest` + `httptest.NewRecorder`, call handler directly
- `t.Helper()` in test helpers
- No `go:generate` directives

### Middleware
- Chain: recover → server → CORS → requestid → log → telemetry → [forward-auth] → [admin-token]
- Middleware imports aliased with `m` prefix: `mcors`, `mlog`, `mrecover`, `mrequestid`

## UI Code Style (_ui/)

### Stack
- **Svelte 5** (runes mode), **Vite 6**, **TailwindCSS 4** (CSS-based config), **TypeScript**
- **Router**: `svelte-spa-router` (hash-based, `#/path`), eager imports
- **HTTP**: `axios` per-domain files, each with `axios.create({ baseURL: 'api/v1' })` (relative, same-origin)
- **Icons**: `lucide-svelte`
- **Package manager**: pnpm

### Component Patterns
- Always `<script lang="ts">` — TypeScript everywhere
- Runes: `$state()`, `$derived()`, `$props()`, `$effect()`, `$bindable()`
- Props: `interface Props { ... }` then `let { items, loading = false }: Props = $props()`
- Events: `onclick={handler}` (Svelte 5 style, not `on:click`)
- Snippets: `{#snippet name(args)}...{/snippet}` and `{@render name(args)}`
- Generic components: `<script lang="ts" generics="T">`
- Conditional classes: `class={["base", condition ? "active" : ""]}`

### API Layer (`src/lib/api/`)
- One file per domain (providers.ts, agents.ts, tasks.ts, etc.)
- Each file: `const api = axios.create({ baseURL: 'api/v1' })`, interface defs, async CRUD functions
- Types: `interface` (not `type`), `snake_case` fields matching backend JSON
- Shared: `ListResult<T>`, `ListParams`, `ListMeta` in `types.ts`
- Error handling in callers: `try/catch` → `addToast(e?.response?.data?.message || 'fallback', 'alert')`
- Streaming: native `fetch` with `ReadableStream` (not axios)

### Styling
- TailwindCSS 4 utility classes inline — almost no `<style>` blocks
- Dark mode: class-based (`.dark`), custom `dark-*` tokens in `@theme` block in `global.css`
- When `<style>` needs Tailwind: `@reference "tailwindcss"` at top of style block
- `:global()` for styling `{@html}` rendered content or third-party library elements
- Path alias: `@/` maps to `src/`

### File Naming
- Components: PascalCase (`TaskDetail.svelte`, `KanbanBoard.svelte`)
- TypeScript files: kebab-case (`api-tokens.ts`, `heartbeat-runs.ts`)
- Store files: `*.svelte.ts` with module-level `$state()` exports
- Routes defined in `src/routes.ts` as plain object mapping paths to components

### Checking & Linting
- `pnpm run check` — runs `svelte-check` for type errors
- `oxlint` available as devDep (Rust-based linter, works without config)
- `stylelint` configured for CSS/SCSS with standard + sass-guidelines
- No ESLint or Prettier config
