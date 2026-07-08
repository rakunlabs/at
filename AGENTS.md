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
| `WorkspaceTTL` | 24h | How long terminal-task workspaces and tool-output dumps are kept; the janitor (see below) sweeps anything older. Set `< 0` to disable. |

**No output-token cap.** Providers and agent configs already define per-model `max_tokens`. An earlier revision shipped a 4096-token platform cap; it broke structured outputs (e.g. multi-scene Script Writer JSON for video shorts) and was removed. `Governor.ChatOptions()` now always returns `nil`, the documented "no cap" sentinel for every provider adapter.

**No per-tool / per-class byte caps.** Earlier revisions classified tools (`executable`, `structured`, `freeform`) and applied per-class caps with overrides for `task_get` / `task_list`. Those over-truncated structured tool outputs (notably the video-generation suite — FAL Veo, Sora, Runway — and the `delegate_to_*` channel that carries full script JSON between agents). We now use a single generous global cap and rely on the workspace dump file to preserve the original payload, which the agent can read via `file_read` or `bash_execute cat`.

To override, edit the constants in `internal/service/loopgov/config.go` or add UI-driven configuration in a follow-up change. The `Disabled` field exists in `loopgov.Config` as an in-code rollback switch but is not exposed via YAML.

**Breaking change**: workflow `agent_call` nodes no longer accept `max_iterations: 0` (legacy "unlimited" mode). Existing graphs are migrated to the platform ceiling on server startup.

## Bash skill handler controls

Skill bash handlers (`internal/service/workflow/handler.go`) run under three resource controls so a runaway video pipeline can't peg the host:

1. **Process-group kill on cancel** — every bash handler is started in its own POSIX process group (`Setpgid: true`). When the surrounding context is cancelled (timeout, user Stop click, server shutdown), the watcher goroutine sends `SIGKILL` to the *entire group*, not just bash. This is what reaps long-running ffmpeg / python / curl children that would otherwise keep running after the agent task ended. Linux + Darwin only; Windows is a no-op stub (`handler_windows.go`).

2. **FFmpeg concurrency cap** — a process-wide `semaphore.Weighted` throttles bash handlers whose script body contains `ffmpeg` or `ffprobe`. The cap is `max(1, runtime.NumCPU()/2)`; on a 4 vCPU GCE box that's 2 concurrent encodes, with the rest queueing on `Acquire(ctx, 1)`. Auto-detected, not configurable today. Substring matching is intentionally broad — user-installed skills get the same protection as the built-in video templates.

3. **Workspace janitor** — `internal/server/workspace-janitor.go` sweeps `WorkspaceRoot` once per hour and removes `<task-id>/` dirs whose owning task is in a terminal status (`done`, `completed`, `cancelled`, `blocked`) AND whose terminal timestamp is older than `loopgov.Config.WorkspaceTTL` (default 24h). Also sweeps `<WorkspaceRoot>/.at-tool-output/<run-id>/` dump dirs by mtime under the same TTL. Set `WorkspaceTTL: -1` to disable. Unknown task IDs (workspaces from a different deployment on shared FS) are *kept*, not nuked.

The built-in video skill templates (`internal/server/skill_templates/{fal-video,video-composer,ffmpeg-guide}.json`) standardize on `-c:v libx264 -preset veryfast -crf 23 -threads 2` and cap `compose_short_v2`'s Phase 1 worker pool to `max(1, min(3, NumCPU/2))` so per-encode CPU stays bounded too.

## Gateway OpenAI compatibility

The `/gateway/v1/...` endpoints aim to be a drop-in replacement for the
OpenAI HTTP API. Endpoints exposed today:

| Endpoint | Notes |
|---|---|
| `POST /gateway/v1/chat/completions` | Full OpenAI shape. Supports `tool_choice`, `parallel_tool_calls`, `n`, `presence_penalty`, `frequency_penalty`, `logit_bias`, `user`, `logprobs`, `top_logprobs`, `store`, `metadata`, `service_tier`, `seed`, `response_format`, streaming with `stream_options.include_usage`, `system_fingerprint`, full `finish_reason` vocabulary (`stop` / `length` / `content_filter` / `tool_calls` / `function_call`), `usage.completion_tokens_details.reasoning_tokens`. AT extensions: `at_fallbacks`, `extra_body`, `mock_response`, `timeout_ms`, and `Idempotency-Key` header. |
| `POST /gateway/v1/embeddings` | OpenAI-shape embeddings. Accepts string or `[]string` `input`. Backed by `service.EmbeddingProvider` (OpenAI, Cohere, Gemini). |
| `POST /gateway/v1/responses` | OpenAI Responses API with streaming. Supports `input` (string or array of items), `instructions`, `tools` (function only), `tool_choice`, `reasoning.effort`, `text.format`, `parallel_tool_calls`, `metadata`. SSE event types: `response.created`, `response.output_item.added`, `response.output_text.delta`, `response.output_text.done`, `response.output_item.done`, `response.completed`, `response.failed`. Does NOT support `previous_response_id` (no server-side state). |
| `POST /gateway/v1/images/generations` | OpenAI-shape image generation. Backed by `service.ImageProvider` (OpenAI, MiniMax). |
| `POST /gateway/v1/audio/speech` | OpenAI TTS. Returns raw audio bytes. Backed by `service.AudioProvider`. |
| `POST /gateway/v1/audio/transcriptions` | Whisper-style multipart upload (`file`, `model`, `language?`, `prompt?`, `response_format?`). |
| `POST /gateway/v1/moderations` | OpenAI omni-moderation shape. Backed by `service.ModerationProvider`. |
| `POST /gateway/v1/rerank` | Cohere-shape rerank: `query`, `documents`, `top_n?`, `return_documents?`. Backed by `service.RerankProvider` (Cohere today). |
| `GET /gateway/v1/health` | Liveness — returns `{status, providers{}, version}`. No auth required. |
| `GET /gateway/v1/health/{provider}` | Per-provider readiness check (without dialing upstream). |
| `GET /gateway/v1/models` | OpenAI-shape model list. |
| `/gateway/v1/providers/{provider}/*` | Native provider passthrough — bypasses the OpenAI envelope. Useful for SigV4-signed Bedrock URLs, Cohere internal endpoints, etc. |
| `GET /gateway/v1/mcp/{name}/ws` | Raw WebSocket passthrough. When the named MCP server's `config.ws_upstream` is set (`{url, headers?, pass_query_params?, pass_headers?}`, `ws://`/`wss://`, header values support `{{var:key}}`), the upgrade request is reverse-proxied and frames are tunneled untouched. Same auth as the MCP endpoint (Bearer / `public` flag) plus a `?token=` query fallback for browser WS clients; AT's `Authorization`/`Cookie` never leak upstream. `pass_query_params` allowlists client query params (empty = all except AT `token`), and `pass_headers` allowlists raw client headers while preserving WebSocket handshake headers. |

### Supported provider types

| Type | Notes |
|---|---|
| `openai` | OpenAI + any OpenAI-compatible (Groq, Together, Fireworks, DeepSeek, xAI, Cerebras, Perplexity, Ollama, LM Studio…). `auth_type: copilot` for GitHub Copilot device-auth. |
| `azure` | Azure OpenAI. `api_key` becomes `api-key` header; `base_url` must include the full deployment + `api-version`. |
| `anthropic` | Anthropic Claude with prompt caching ON by default (markers on system block + last tool + last message). Disable via `extra_headers: {at-prompt-caching: off}`. `auth_type: claude-code` for OAuth. |
| `bedrock` | AWS Bedrock Converse API. Credentials from `api_key` (`ACCESS:SECRET[:SESSION]`) or env (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`). Region from `base_url` host or `AWS_REGION`. |
| `vertex` | OpenAI-compatible Vertex AI endpoint with Google ADC. |
| `vertex-gemini` | Native Gemini API on Vertex (keeps `thinkingConfig`, `safetySettings`, grounding). Requires `extra_headers.vertex_project` + `extra_headers.vertex_region`. |
| `gemini` | Native Google Generative Language API (aistudio key). Default `safetySettings: BLOCK_NONE` on every category. Synthetic tool name `__google_search` / `web_search` activates Gemini grounding. |
| `cohere` | Native Cohere chat (v2/chat) + rerank (v2/rerank) + embeddings (v2/embed). |
| `minimax` | MiniMax via the Anthropic-compatible chat API + native MiniMax image/TTS endpoints. |

Provider-specific compatibility notes:

- **`tool_choice`** is plumbed to every provider:
  - OpenAI / Vertex: forwarded verbatim
  - Anthropic: translated (`auto` → `{type:"auto"}`, `required` → `{type:"any"}`, `{type:"function",function:{name:"X"}}` → `{type:"tool",name:"X"}`)
  - Gemini: translated to `toolConfig.functionCallingConfig` (`AUTO` / `ANY` / `NONE` + `allowedFunctionNames`)
  - Bedrock: translated to Converse `toolChoice` (`auto` / `any` / `tool`); `"none"` is emulated by omitting `toolConfig` entirely
  - Cohere: translated to v2 `tool_choice` (`REQUIRED` / `NONE`); forcing a specific tool maps to `REQUIRED` (closest native behaviour)
- **`parallel_tool_calls`** maps to Anthropic's `disable_parallel_tool_use` (inverted)
- **`response_format`** is best-effort on non-OpenAI providers:
  - Gemini: `json_object` → `responseMimeType: application/json`; `json_schema` → adds `responseSchema`
  - Cohere: `json_object` / `json_schema` → v2 `response_format` (`{"type":"json_object","schema":{...}}`)
  - Anthropic: no native equivalent — we append a system-prompt instruction asking for JSON output (and embed the schema for `json_schema`). Strict structured-output guarantees require the tool-call grammar pattern instead.
- **`logprobs`/`top_logprobs`** are OpenAI/Vertex only; non-OpenAI providers ignore them.
- **`n` > 1** is honoured by OpenAI/Vertex (and Gemini via `candidateCount`); Anthropic returns one choice.
- **`seed`** is honoured by OpenAI/Vertex/Gemini/Cohere; Anthropic ignores it.
- **Web search**: a synthetic tool named `web_search` (or `__google_search` / `google_search` on Gemini, `__web_search` on Anthropic) activates the provider's native internet search — Gemini/vertex-gemini `googleSearch` grounding, Anthropic server-side `web_search_20250305`. OpenAI search-preview models take `web_search_options` (also forwarded by the vertex adapter). Note the tool name is consumed by the provider: a user-defined function tool with the same name will not be called on those providers.
- Upstream provider errors surface as real gateway errors (429/5xx envelopes), never as HTTP-200 responses with error text in `content`.
- Provider `type` strings are validated on create/update against `service.SupportedProviderTypes` (openai, anthropic, azure, bedrock, vertex, vertex-gemini, gemini, cohere, minimax).

Error envelope conforms to OpenAI's shape including `param` where applicable:

```json
{"error":{"message":"...","type":"invalid_request_error","param":"model","code":"model_not_found"}}
```

### AT extensions to `/chat/completions` and `/responses`

These are non-standard fields the gateway accepts in addition to the
OpenAI shape. **All are opt-in** — the gateway behaves exactly like
upstream OpenAI when none of them are present.

| Field | Default | Behaviour |
|---|---|---|
| `at_fallbacks: ["provider/model", ...]` | `[]` (off) | When set, the gateway retries on the next entry if the primary fails with a retryable upstream error (429 / 529 / 5xx / timeout / context cancel). 4xx other than 429 does NOT trigger fallback. The model that actually served the response is reported in the `x-at-model-used` response header. Streaming requests ignore this field — once SSE headers are flushed we can't restart. |
| `extra_body: {...}` | `{}` (off) | Merged into the upstream provider request body **after** AT's own field mapping. Keys collide-overwrite our own keys. Use it for provider-native fields we don't surface yet (Anthropic `cache_control`, Gemini `safetySettings`, Bedrock `additionalModelRequestFields`, …). |
| `mock_response: "..."` | `""` (off) | When non-empty, returns a synthesized response immediately with no upstream call. Works for both sync and streaming. Streaming emits `role` → `content` → `finish=stop` chunks. Response carries `x-at-mock-response: true`. |
| `timeout_ms: <int>` | `0` (off) | Per-call deadline applied via `context.WithTimeout` across the entire fallback chain. `0` inherits the request context (no extra cap). |
| `Idempotency-Key: <string>` (header) | unset (off) | When present, the gateway caches the response (status + body + headers) for 5 minutes scoped to `(token_id, path, key)`. Subsequent requests with the same key replay the cached response and add `x-at-idempotent-replay: true`. 5xx responses are not cached. |

## Runtime configuration

LLM providers, gateway API tokens, and bot adapters are configured at runtime through the UI (`/api/v1/providers`, `/api/v1/api-tokens`, `/api/v1/bots`) and persisted in the database. They are NOT accepted via YAML or env. The only YAML / env knobs are bootstrap-only: log level, server bind, store backend, telemetry.

## LLM Call Audit (tracing)

Langfuse-style request/response tracing for gateway LLM traffic, gated by the `llm_audit` feature flag (default ON, toggle on the Features page). This is separate from `cost_events` (per-call metrics) and `audit_log` (agent-action log): it stores the **full request and response bodies** of every upstream provider call.

- **Model**: `service.LLMCall` (`internal/service/types-llmcall.go`) + `LLMCallStorer`. Table `llm_calls` (migration `21_llm_calls.sql`, both backends). One row per upstream call — each `at_fallbacks` attempt is its own row, correlated by `trace_id`. Fields: trace/session IDs, source (`gateway` / `gateway_stream` / `responses` / `chat`), endpoint, token/agent/task/run/org attribution, provider+model+requested_model, full request/response bodies, token buckets (incl. reasoning), cost_cents, latency_ms, time_to_first_token_ms (streaming), status/error, finish_reason, user_field.
- **Recorder**: `Server.recordLLMCallAsync` (`internal/server/llm-audit.go`) — fire-and-forget, feature-gated (30s cached toggle in `Server.llmAudit`). Bodies over `LLMCallBodyMaxBytes` (256 KB) are truncated inline and the full payload is spilled to `<WorkspaceRoot>/.at-llm-audit/<yyyy-mm-dd>/<id>-<request|response>.json` (path in `request_ref`/`response_ref`; `GetLLMCall` rehydrates from disk). List queries clip bodies to `LLMCallPreviewBytes` (2 KB) via a `substr()` projection; the detail endpoint returns full bodies.
- **Hooks**: gateway `ChatCompletions` (sync success/error, true-streaming success/mid-stream-error/open-error with reconstructed response, fake-streaming success/error) and `Responses` (non-streaming). The admin `AdminChatCompletions` streaming path records with source `chat`. Streaming reconstructs a single OpenAI-shape response from accumulated deltas (`streamAuditResponseBody`). Raw request bytes are captured pre-decode so the stored request is byte-faithful. Clients can set `x-at-trace-id` / `x-at-session-id` to stitch multi-turn conversations.
- **Hybrid OTEL export**: `emitLLMSpan` emits a completed OTEL span per call using gen-ai semantic conventions (`gen_ai.system`, `gen_ai.request.model`, `gen_ai.usage.*`, `gen_ai.prompt`/`gen_ai.completion`) plus Langfuse dimensions (`langfuse.trace.id`, `langfuse.session.id`, `langfuse.observation.type=generation`). Goes to whatever OTLP collector `tell` wires as the global tracer provider (e.g. a self-hosted Langfuse); no-op when telemetry is off.
- **Retention**: `startLLMAuditJanitor` (`internal/server/llm-audit-janitor.go`) sweeps rows + spill dirs older than `LLMCallRetention` (7d) hourly.
- **API/UI**: `GET /api/v1/llm-calls` (list, newest-first, filters via `rakunlabs/query`) + `GET /api/v1/llm-calls/{id}` (full record). UI: `_ui/src/pages/LLMCalls.svelte` (route `/llm-calls`, "LLM Traces" sidebar link) — filterable table + slide-over drawer with pretty-printed request/response JSON and copy buttons.

## Connections & Connectors

External-service credentials are modeled in two layers:

- **Connectors** (`internal/service/types-connector.go`, `internal/server/connectors-registry.go`) — data-driven definitions of a connection *type* (the provider catalog). A connector carries an `auth_kind` (`oauth2` | `token` | `custom`), an optional OAuth2 block (`auth_url`, `token_url`, `scopes`, `use_pkce`, `userinfo_url`, `account_label_path`, …), and a `fields[]` credential schema that drives the UI form. Connectors hold **no secrets**, so the `connectors` table is unencrypted. The catalog is the merge of built-in JSON definitions (`internal/server/connectors/*.json`, embedded) and user-defined / override rows in the `connectors` table — **a DB row overrides a built-in by slug**. CRUD: `/api/v1/connectors` (+ inline "Manage providers" UI on the Connections page). This replaced the formerly hardcoded `google`/`youtube` OAuth map — new providers (GitHub, Spotify, …) are added by shipping a JSON file or creating one in the UI, no code change.
- **Connections** (`internal/service/types-connection.go`, `internal/server/connections.go`) — named, AES-256-GCM-encrypted credential *instances* bound to a connector by its slug (`Connection.Provider == Connector.Slug`). Multiple accounts per provider; agents/skills reference them by ID. The connection create/update API accepts a dynamic `fields` map (keyed by full var name, e.g. `spotify_client_id`); `connectorCredentialsFromValues` folds well-known suffixes (`_client_id` / `_client_secret` / `_refresh_token` / `_api_key`) onto the struct and the rest into `Extra`.

The OAuth2 flow (`internal/server/oauth.go`) is fully connector-driven and **supports PKCE** (verifier cached on `Server.oauthPKCE`, keyed by state for the callback flow or `provider+connection` for the manual paste-code flow). Token exchange sends `Accept: application/json` (so GitHub-style endpoints return JSON), omits `client_secret` for PKCE public clients, and no longer hard-requires a refresh token — when a provider returns only an access token it is stored under `<slug>_access_token`. Account labels are fetched generically via the connector's `userinfo_url` + `account_label_path` (a dot-path supporting array indices, e.g. `items.0.snippet.title`).

Skills can ship their own connector: `SkillTemplate.connector` (`internal/server/skill-templates.go`) is upserted into the registry on install when no connector with that slug exists, so a user-added skill brings its own connection type. Runtime credential resolution for skill handlers is unchanged (`internal/service/workflow/connection_resolver.go`): `getVar("<provider>_<suffix>")` resolves through per-skill → per-agent connection bindings → global variable.

## Memory

AT does not ship a native long-term agent memory store. Agents that need memory should use an external memory MCP (for example a custom Postgres/vector/Engram/Mem0/Letta MCP) attached through MCP Sets or MCP server URLs. Keep memory read/write policy, retention, and embedding/search strategy inside that MCP; AT only discovers and calls the tools.

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
