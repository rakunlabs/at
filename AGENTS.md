# AT — LLM Gateway + Workflow Engine

## What This Is

OpenAI-compatible LLM gateway that routes requests to multiple providers (OpenAI, Anthropic, Vertex AI, Gemini) through a single `/gateway/v1/chat/completions` endpoint. Includes a DAG-based workflow engine and Svelte admin UI.

Module: `github.com/rakunlabs/at` — Go 1.27

## Architecture

```
cmd/at/main.go              → bootstrap: config → store → providers → server.Start
internal/server/            → HTTP handlers (ada framework), middleware, static UI
internal/service/           → domain types + store interfaces (at.go)
internal/service/workflow/  → DAG engine: parse → topoSort → run (concurrent fan-out)
internal/service/workflow/nodes/ → node types registered via init()
internal/service/llm/       → provider adapters: openai/, antropic/, gemini/, vertex/
internal/store/             → store factory → postgres (the only backend; required)
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
| `MaxIterCeiling` | 240 | Finite platform ceiling; individual agent/task budgets remain opt-in |
| `ToolResultMaxBytes` | 65536 | Inline cap on every tool result; full payload spilled to dump file |
| `ChatHistoryLimit` | 200 | Messages reloaded per chat turn |
| `WorkspaceRoot` | `/tmp/at-tasks` | Where `.at-tool-output/<run-id>/...` dumps land |
| `WorkspaceTTL` | 24h | How long terminal-task workspaces and tool-output dumps are kept; the janitor (see below) sweeps anything older. Set `< 0` to disable. |

**No output-token cap.** Providers and agent configs already define per-model `max_tokens`. An earlier revision shipped a 4096-token platform cap; it broke structured outputs (e.g. multi-scene Script Writer JSON for video shorts) and was removed. `Governor.ChatOptions()` now always returns `nil`, the documented "no cap" sentinel for every provider adapter.

Anthropic requires a `max_tokens` field even when the governor supplies no cap.
Its adapter defaults to 32000 per request (matching OpenCode's default output
budget and Claude Code's unknown-model fallback; formerly 4096, which truncated
Shorts script tool calls). Recognized legacy Claude 3.5 models use 8192; Claude 3/2/Instant
use 4096. Explicit provider/request limits still override these defaults. This is
an output allowance per response, separate from gateway API-token usage quotas.

Long-form agents may opt into budgets up to 240; existing smaller agent defaults remain unchanged. Positive task/Telegram-command `max_iterations` overrides the agent, so use `0` there to inherit a revised agent budget. Workflow nodes using the legacy zero/unlimited migration adopt the current ceiling. Org delegation stops after three consecutive output-limit responses rather than burning the entire budget on incomplete tool arguments; no partial tool call is executed, and recovery guidance asks for small chapter/section artifact writes.

Built-in tool calls in all three agent loops inherit the configured per-tool deadline. `bash_execute` uses that remaining deadline when `timeout` is omitted (otherwise 60 seconds outside a bounded context); explicit `timeout` is in seconds, capped at 3600, and never extends an outer deadline. `timeout_seconds` is not a bash argument. Cancellation still kills the process group. Use bounded foreground chapter renders and validated artifacts rather than detached jobs with repeated sleep/poll calls.

**No per-tool / per-class byte caps.** Earlier revisions classified tools (`executable`, `structured`, `freeform`) and applied per-class caps with overrides for `task_get` / `task_list`. Those over-truncated structured tool outputs (notably the video-generation suite — FAL Veo, Sora, Runway — and the `delegate_to_*` channel that carries full script JSON between agents). We now use a single generous global cap and rely on the workspace dump file to preserve the original payload, which the agent can read via `file_read` or `bash_execute cat`.

When the shared input window overflows, the governor evicts oldest tool exchanges
before conversational text, replacing results with an explicit omission notice
and removing their paired calls while retaining assistant text. This prevents a
large result batch from displacing the user's request and then disappearing as
orphan results, leaving a system-only request. Full stored history is unchanged;
the model is told to retrieve smaller targeted results when necessary. Summary
space is reserved only when a summarizer is actually configured. Regression:
`internal/service/loopgov/conversation_test.go`.

To override, edit the constants in `internal/service/loopgov/config.go` or add UI-driven configuration in a follow-up change. The `Disabled` field exists in `loopgov.Config` as an in-code rollback switch but is not exposed via YAML.

**Breaking change**: workflow `agent_call` nodes no longer accept `max_iterations: 0` (legacy "unlimited" mode). Existing graphs are migrated to the platform ceiling on server startup.

## Bash skill handler controls

Skill bash handlers (`internal/service/workflow/handler.go`) run under three resource controls so a runaway video pipeline can't peg the host:

1. **Process-group kill on cancel** — every bash handler is started in its own POSIX process group (`Setpgid: true`). When the surrounding context is cancelled (timeout, user Stop click, server shutdown), the watcher goroutine sends `SIGKILL` to the *entire group*, not just bash. This is what reaps long-running ffmpeg / python / curl children that would otherwise keep running after the agent task ended. Linux + Darwin only; Windows is a no-op stub (`handler_windows.go`).

2. **FFmpeg concurrency cap** — a process-wide `semaphore.Weighted` throttles bash handlers whose script body contains `ffmpeg` or `ffprobe`. The cap is `max(1, runtime.NumCPU()/2)`; on a 4 vCPU GCE box that's 2 concurrent encodes, with the rest queueing on `Acquire(ctx, 1)`. Auto-detected, not configurable today. Substring matching is intentionally broad — user-installed skills get the same protection as the built-in video templates.

3. **Workspace janitor** — `internal/server/workspace-janitor.go` sweeps `WorkspaceRoot` once per hour and removes `<task-id>/` dirs whose owning task is in a terminal status (`done`, `completed`, `cancelled`, `blocked`) AND whose terminal timestamp is older than `loopgov.Config.WorkspaceTTL` (default 24h). Also sweeps `<WorkspaceRoot>/.at-tool-output/<run-id>/` dump dirs by mtime under the same TTL. Set `WorkspaceTTL: -1` to disable. Unknown task IDs (workspaces from a different deployment on shared FS) are *kept*, not nuked.

The built-in video skill templates (`internal/server/skill_templates/{fal-video,video-composer,ffmpeg-guide}.json`) standardize on `-c:v libx264 -preset veryfast -crf 23 -threads 2` and cap `compose_short_v2`'s Phase 1 worker pool to `max(1, min(3, NumCPU/2))` so per-encode CPU stays bounded too.

## Host terminals: watching and control

A host terminal is a tmux session in its own transient systemd unit
(`internal/service/terminal/host.go`); the browser WebSocket is only an
attachment, never the shell's owner. Each connection spawns its own `tmux
attach` client, so tmux already fans output out to every attached screen — AT
does not buffer or broadcast anything itself.

Several connections may attach at once. Exactly one holds **control** and is the
only one whose bytes are written into the PTY; the rest watch. The registry
(`terminalSeat` in `internal/server/terminals.go`) lives on the host that owns
the tmux socket, never on the node a browser happened to reach: two browsers can
arrive through two replicas, and a per-replica flag would hand out two writers.
Attaching never takes control from whoever is typing; `control` is an explicit
request. When the holder disconnects, the longest-waiting watcher is promoted, so
a shell is never left attached but unusable.

`attach-session` deliberately does **not** pass `-d` (which used to disconnect
everyone else) and does **not** pass `-r` for watchers. tmux cannot toggle a live
client between read-only and read-write, so `-r` would make every handover a
kill-and-reattach under a running output pump. It is unnecessary: the PTY master
fd is private to the AT process, the session has no prefix and no key bindings
(`tmuxStartArgs`), and input is gated at the single point where AT writes to the
PTY. Regression: `TestTerminalSeatControl`.

Sizing uses `window-size manual` plus `resize-window` from the holder only, so a
phone watching a desktop shell cannot reflow it; watchers resize only their own
viewport and are letterboxed. Both are best effort — tmux before 3.1 lacks them
and keeps its own sizing. The browser is told its role on attach, on handover and
on its next call after losing control (`{"type":"role"}`), because a screen that
silently discards keystrokes is indistinguishable from a frozen shell. Local
connections are woken immediately; remote ones learn within one 10s heartbeat,
while enforcement is immediate either way.

Terminals are per administrator account: records are owner-scoped, so one admin
cannot see or attach to another's terminal. Cross-account sharing would need an
explicit, revocable, audited permission model — these are usually root shells and
a watcher sees every keystroke, including secrets.

## Gateway OpenAI compatibility

The `/gateway/v1/...` endpoints aim to be a drop-in replacement for the
OpenAI HTTP API. Endpoints exposed today:

| Endpoint | Notes |
|---|---|
| `POST /gateway/v1/chat/completions` | Full OpenAI shape. Supports `tool_choice`, `parallel_tool_calls`, `n`, `presence_penalty`, `frequency_penalty`, `logit_bias`, `user`, `logprobs`, `top_logprobs`, `store`, `metadata`, `service_tier`, `seed`, `response_format`, streaming with `stream_options.include_usage`, `system_fingerprint`, full `finish_reason` vocabulary (`stop` / `length` / `content_filter` / `tool_calls` / `function_call`), `usage.completion_tokens_details.reasoning_tokens`. AT extensions: `at_fallbacks`, `extra_body`, `mock_response`, `timeout_ms`, and `Idempotency-Key` header. |
| `POST /gateway/v1/embeddings` | OpenAI-shape embeddings. Accepts string or `[]string` `input`. Backed by `service.EmbeddingProvider` (OpenAI, Cohere, Gemini). Providers can declare `embedding_models` (Provider UI: "Embedding Models" + discovery via `POST /api/v1/providers/discover-embedding-models`); these are advertised alongside chat models by `/gateway/v1/models` (advisory — unlisted models are still forwarded). |
| `POST /gateway/v1/responses` | OpenAI Responses API with streaming. Supports `input` (string or array of items), `instructions`, `tools` (function only), `tool_choice`, `reasoning.effort`, `text.format`, `parallel_tool_calls`, `metadata`. SSE event types: `response.created`, `response.output_item.added`, `response.output_text.delta`, `response.output_text.done`, `response.output_item.done`, `response.completed`, `response.failed`. Does NOT support `previous_response_id` (no server-side state). |
| `POST /gateway/v1/images/generations` | OpenAI-shape image generation. Backed by `service.ImageProvider` (OpenAI, MiniMax). |
| `POST /gateway/v1/audio/speech` | OpenAI TTS. Returns raw audio bytes. Backed by `service.AudioProvider`. |
| `POST /gateway/v1/audio/transcriptions` | Whisper-style multipart upload (`file`, `model`, `language?`, `prompt?`, `response_format?`). |
| `POST /gateway/v1/moderations` | OpenAI omni-moderation shape. Backed by `service.ModerationProvider`. |
| `POST /gateway/v1/rerank` | Cohere-shape rerank: `query`, `documents`, `top_n?`, `return_documents?`. Backed by `service.RerankProvider` (Cohere today). |
| `GET /gateway/v1/health` | Liveness — returns `{status, providers{}, version}`. No auth required. |
| `GET /gateway/v1/health/{provider}` | Per-provider readiness check (without dialing upstream). |
| `GET /gateway/v1/models` | OpenAI-shape model list (chat + embedding models). |
| `/gateway/v1/providers/{provider}/*` | Native provider passthrough — bypasses the OpenAI envelope. Useful for SigV4-signed Bedrock URLs, Cohere internal endpoints, etc. |
| `GET /gateway/v1/mcp/{name}/ws` | Raw WebSocket passthrough. When the named MCP server's `config.ws_upstream` is set (`{url, headers?, pass_query_params?, pass_headers?}`, `ws://`/`wss://`, header values support `{{var:key}}`), the upgrade request is reverse-proxied and frames are tunneled untouched. Same auth as the MCP endpoint (Bearer / `public` flag) plus a `?token=` query fallback for browser WS clients; AT's `Authorization`/`Cookie` never leak upstream. `pass_query_params` allowlists client query params (empty = all except AT `token`), and `pass_headers` allowlists raw client headers while preserving WebSocket handshake headers. |

### Supported provider types

| Type | Notes |
|---|---|
| `openai` | OpenAI + any OpenAI-compatible (Groq, Together, Fireworks, DeepSeek, xAI, Cerebras, Perplexity, Ollama, LM Studio…). `auth_type: copilot` for GitHub Copilot device-auth; `auth_type: chatgpt` for ChatGPT Plus/Pro OAuth through the Codex Responses backend. |
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
- **`n`** currently supports only `1`. Other values return HTTP 400 before inference across all providers. The internal response/stream contract represents one choice; forwarding `n > 1` previously paid for extra candidates and silently discarded them. Multi-choice response support requires extending that contract end-to-end.
- **`seed`** is honoured by OpenAI/Vertex/Gemini/Cohere; Anthropic ignores it.
- **Web search**: a synthetic tool named `web_search` (or `__google_search` / `google_search` on Gemini, `__web_search` on Anthropic) activates the provider's native internet search — Gemini/vertex-gemini `googleSearch` grounding, Anthropic server-side `web_search_20250305`. OpenAI search-preview models take `web_search_options` (also forwarded by the vertex adapter). Note the tool name is consumed by the provider: a user-defined function tool with the same name will not be called on those providers.
- **Gemini thinking** is selected per model generation, because the two field names are mutually exclusive and sending the wrong one is a 400: Gemini 3+ (`gemini-3*`, and later majors) gets `thinkingLevel` (`MINIMAL`/`LOW`/`MEDIUM`/`HIGH`), Gemini 2.5 and earlier get `thinkingBudget` in tokens. `reasoning_effort` low/medium/high maps to 2048/8192/24576 tokens or the matching level; an explicit `thinking` block wins over it, and `thinking.budget_tokens: 0` is preserved (`thinkingBudget` is a pointer, so a zero budget is no longer erased by `omitempty`). Any enabled config also sets `includeThoughts`, which is what actually makes Gemini emit `thought` parts — without it `reasoning_content` was always empty. Requests that ask for no thinking still send no `thinkingConfig` at all. See `geminiThinkingConfig` in `internal/service/llm/gemini/openai_compat.go`.
- **Gemini tool-call correlation** uses `functionCall.id` / `functionResponse.id` when the model supplies one. Matching results to calls by function name alone is ambiguous whenever a single turn calls the same function more than once, which is the normal parallel-tool-call case. Upstream IDs are preserved into `service.ToolCall.ID` and echoed symmetrically on both the call and the response. IDs this adapter minted itself (`call_<ulid>`, used only when the model sent none) are never replayed upstream, since Gemini never issued them.
- **Gemini usage** folds `toolUsePromptTokenCount` into `PromptTokens`: server-side tool input (Google Search grounding, code execution) is billed as input but reported outside `promptTokenCount`, so ignoring it under-reported cost on every grounded request. `cachedContentTokenCount` is subtracted from `promptTokenCount` because the latter is the total effective prompt size and already includes the cached prefix. Explicit context caching (the `cachedContents` resource) is not managed by AT; a pre-created handle can be passed through as `extra_body.cachedContent`. Implicit caching needs no wiring and works today — the request prefix AT builds is byte-stable across calls.
- **Finish reason vs tool calls**: adapters call `common.ReconcileToolCallFinish` after collecting tool calls, so a response carrying pending calls is always `Finished: false` / `finish_reason: "tool_calls"`. OpenAI itself reports `tool_calls`, but many OpenAI-compatible servers (Ollama, LM Studio, vLLM, several hosted gateways) return `"stop"` with a populated `tool_calls` array; the agent loops gate execution on `resp.Finished || len(resp.ToolCalls) == 0`, so taking that at face value silently dropped the calls and ended the run on whatever text came with them. Truncated/filtered responses (`length` / `content_filter`) drop their partial calls *before* reconciliation, so those stop reasons are preserved. `normalizeFinishReason` / `mapStreamFinishReason` apply the same rule at the gateway edge. Regression: `internal/service/llm/openai/compat-regression_test.go`.
- **`refusal`** is a first-class field (`service.LLMResponse.Refusal`). OpenAI returns it *instead of* content, with `finish_reason: "stop"`, on structured-output and safety refusals — so dropping it made a refusal indistinguishable from an empty response. It is forwarded in the gateway's `message.refusal` (the wire field already existed but was never populated) and reported by org delegation as a `REFUSED` result instead of an unexplained `EMPTY_RESPONSE`.
- Upstream provider errors surface as real gateway errors (429/5xx envelopes), never as HTTP-200 responses with error text in `content`.
- Provider `type` strings are validated on create/update against `service.SupportedProviderTypes` (openai, anthropic, azure, bedrock, vertex, vertex-gemini, gemini, cohere, minimax).

Provider contract regressions are covered by `internal/service/llm/contracts_test.go`, per-adapter `contracts_test.go` files, and the gateway translation tests. Native JSON Schema providers use `service.CopyJSONSchema`; do not apply Gemini's restrictive filter to their tools (it removes referenced arguments and constraints). Gemini inlines acyclic local references before applying its schema subset. OpenAI/Vertex/Codex preserve explicit tool `strict` settings; Responses tools use their native flat shape and are normalized before adapter dispatch.

Streaming adapters relay through `common.StreamWithContext` so cancellation closes the upstream body and drains blocked parser sends, allowing limiter slots to be released. Missing completion signals are errors rather than successful EOFs. Shared `common.ParseToolArguments` rejects malformed/non-object arguments. OAuth proxy credentials are resolved before forwarding; caller bearer tokens/cookies are not fallback upstream credentials. Gemini remote-image requests explicitly suppress provider credential injection.

Error envelope conforms to OpenAI's shape including `param` where applicable:

```json
{"error":{"message":"...","type":"invalid_request_error","param":"model","code":"model_not_found"}}
```

A wrong base URL must not look like an empty gateway. Every unmatched path under
`/gateway`, the prefix itself, and `<base>/v1/*` (the `/gateway` segment dropped)
answer `404` with `code: unknown_endpoint` naming the correct base URL. Without
those routes the SPA catch-all (`baseGroup.Handle("/*", …)`) served `index.html`
with HTTP 200 and `text/html` for a trailing slash, a typo, a wrong method or a
truncated base URL, and an OpenAI client reported "no models" rather than a
configuration error. A trailing wildcard does not match its own slashless base in
ada, so `/gateway` is registered separately. `/gateway/v1/models` returns `data`
as `[]`, never `null` — clients iterate it without a nil check — sorted by model
ID, since Go map iteration order previously reshuffled pickers between restarts.
Regression: `internal/server/gateway-routing_test.go`.

### Deployment sub-path (`server.base_path`)

`config.NormalizeBasePath` canonicalizes the value once at load: either `""`
(root) or a cleaned path with a leading slash and no trailing slash. This has to
be central because routes reach the table two ways — ada groups, which run
`path.Join` and tolerate `at` or `/at/`, and raw prefix concatenation for the
`/auth`, workspace and runtime route tables, which does not. Under `/at/` the
latter produced patterns like `/at//auth/*` whose empty segment no request can
match, so authentication and workspace endpoints silently disappeared while
`/gateway` and `/api` kept working. `..` segments are rejected rather than
resolved, because `path.Clean` would turn a typo into a prefix that serves
somewhere else.

The bare prefix (`GET /at`) redirects `301` to `/at/` instead of 404ing: a
trailing wildcard does not match its own slashless base, and the trailing slash
is load-bearing — asset URLs, the axios `baseURL`, the service-worker scope and
the session cookie path are all resolved relative to it, so serving the SPA at
`/at` would resolve every one of them a level too high.

UI code that shows an absolute URL for a server route (webhooks, MCP endpoints,
Claude Code marketplace links) must build it with `lib/helper/deployment-url.ts`
(`deploymentUrl` / `deploymentWsUrl` / `deploymentOrigin`), which resolves
against `document.baseURI`. `location.origin` and `location.host` drop the
prefix, and `location.pathname` yields the current SPA route rather than the
deployment root. Regression: `internal/config/base-path_test.go`,
`internal/server/gateway-routing_test.go`.

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

### Password login lockout

Native password login locks an account after five consecutive incorrect passwords
for 15 minutes. Successful password verification resets the counter; expiry starts
a fresh budget. Blocked attempts never extend the deadline. Counters and deadlines
live in the existing `auth_security.data` record and verification runs under the
account row lock, so restarts and concurrent replicas cannot reset or overrun it.
Existing source/account admission limits and process-wide hashing slots still apply.
The lock covers password sign-in; existing sessions and passkey/SSO flows retain
their own admission controls.

The installation-admin Users list exposes only active `password_locked_until`
metadata. **Unlock sign-in** calls POST `/auth/users/{id}/unlock-login`, clears the
password counter/deadline and logs the actor and target. It does not enable disabled
accounts or revoke sessions. No migration is required: absent JSON fields default
to zero. Regression coverage: `TestPasswordLoginLockoutPostgres`.

Migration 51 adds `auth_login_events` and persistent `auth_users.last_login_at` /
`last_login_ip`. Password failures, lock/blocked attempts, completed sign-ins,
administrator unlocks and session revocations are recorded with socket-peer IP,
bounded client User-Agent and actor ID for administrator actions. Users displays
last sign-in and an expandable **Sign-in history** (latest 50 events, retained for
90 days by the bounded auth janitor); **Sign out all sessions** uses the existing
session-version revocation. GET `/auth/users/{id}/login-events` is installation-
admin-only. Forwarded IP headers are deliberately not trusted: proxy deployments
show the proxy peer. Audit storage failures are logged explicitly; last successful
sign-in metadata survives event retention. History begins at deployment.

### Selected-workspace provider catalog and workflows

`GET /api/v1/info` is workspace-admitted. Its provider/model list comes from
`ListWorkspaceProviderCatalog`, not the process-wide gateway registry; it includes
local providers and explicitly shared providers, without decrypting credentials.
Default-workspace providers may set `config.shared_with_all_workspaces` through
the Providers editor's **Make available to all workspaces** checkbox. Only a
platform administrator may create or modify globally shared providers. Sharing
applies to existing and future workspaces; local keys take precedence, explicit
per-workspace model grants remain restrictive, and runtime model-use checks still
apply. The management CRUD list remains the selected workspace's owned providers.

Providers can be parked instead of deleted. The Providers list exposes a power
toggle behind PUT `/api/v1/providers/{key}/disable` (`{disabled: bool}`,
selected-workspace `providers.write`, plus the shared-provider platform-admin
guard). `config.disabled` lives in the existing config JSONB, so no migration is
needed and absent keys read as enabled; `SetProviderDisabled` patches it with
`jsonb_set` without decrypting or rewriting credentials, and ordinary config
saves preserve it (`preserveProviderAvailability`) so an edit cannot silently
resume a provider somebody stopped. A disabled provider disappears from
`ListWorkspaceProviderCatalog` (so `/api/v1/info` model pickers do not offer it)
and from `/gateway/v1/models`, and is refused by `ResolveWorkspaceProviderForUse`
/ `ExecutionProviderDefaultModel` with `service.ErrProviderDisabled` (which wraps
`ErrAccessDenied`), covering agents, chat sessions and workflow nodes in one
place. On the gateway, `getProviderInfo` reports it as absent so chat,
responses, embeddings, media, passthrough and admin chat fail closed by default
rather than through per-call-site checks; the error message still names the real
reason and chat answers 404 (administratively unavailable is deterministic, not a
retryable outage) via the typed `providerDisabledError`. Health reports
`disabled` rather than hiding the provider, and model discovery returns 409
because it spends the same credentials a request would. Credentials, model lists
and OAuth state survive disabling. Regression:
`internal/server/provider-disable_test.go`.

Workflow CRUD, versions, activation, nested triggers, run/run-stream, and active
run listing/cancellation use selected-workspace admission. Active runs capture
their workspace at registration; one workspace cannot list or cancel another's
run. Existing workflows remain in their persisted workspace (legacy data in
Default); switching workspace changes visibility rather than moving records.

Bots CRUD/start/stop/status and API token CRUD/usage/reset also use selected-
workspace admission. Bot lifecycle helpers check the persisted workspace before
touching the global adapter map, including MCP builtin calls. Token management
usage access has its own ownership guard, separate from gateway accounting.
Workspace switching preserves the current hash route and reloads it to clear
the previous workspace's cached data.

API Tokens exposes **Pause / Resume**, persisted as `tokens.paused` (migration
50, default false). PUT `/api/v1/api-tokens/{id}/pause` accepts `{paused: bool}`
under selected-workspace `tokens.write` admission and changes only availability
and the update actor. Ordinary token edits preserve pause. Authentication rejects
paused tokens before usage tracking with HTTP 401 and an actionable message;
MCP requests presenting a paused token cannot fall back to public admission.
Pause blocks new authenticated requests; already admitted requests/streams continue.

API Tokens also exposes **Rotate**, a confirmed row action behind POST
`/api/v1/api-tokens/{id}/rotate` (selected-workspace `tokens.write`). It replaces
the secret in place: the row id, name, restrictions, limits, pause state and
accumulated usage rows survive, and only `token_hash` / `token_prefix` change, so
references to the token and its budget accounting stay intact — the alternative
was delete-and-recreate, which discards all of it. No migration is needed.
Generation is shared with creation through `generateAPITokenSecret`
(`internal/server/api-tokens.go`), which owns the `at_` + hex(32 crypto/rand
bytes) format and sha256-only storage for both the HTTP handlers and the
`apitoken_create` MCP tool. The plaintext is returned exactly once, as on create.
The superseded secret dies on commit — gateway auth resolves the bearer by hash
per request and keeps no hash cache, so rotation is immediate and cluster-wide —
while already admitted requests finish. `last_used_at` is cleared and the
in-memory `tokenLastUsed` throttle entry dropped, because both described the old
secret and a stale throttle would hide the new secret's first use for up to five
minutes. A paused token stays paused: pause is a separate availability decision
and rotating must not silently reopen a closed token. Regression:
`internal/server/token-rotate_test.go`.

Workspace startup selection is account-configurable under **Settings → Workspace
→ Workspace on sign-in**: Default (the shipped default), last used, or a specific
accessible workspace. Migration 49 stores `workspace_preferences`; GET/PUT
`/auth/workspaces/preferences` and PUT `/auth/workspaces/selection` are self-scoped.
Tab selection is keyed by user and nonsecret session-family ID so another login
does not inherit it. Default is explicitly preferred over newly created ULIDs.
Deleted/revoked selections fall back to Default or the oldest accessible workspace.

POST `/api/v1/workspaces/{workspace}/delete` permanently deletes a non-default
workspace after an exact-name confirmation. The store revalidates owner/admin
authority and `workspace.archive`, locks the workspace, and deletes all owned
records in one transaction (the inventory is schema-tested). The handler cancels
local workflows/chat turns/delegations/bots and removes that workspace's execution
directory. Cleanup failures are reported separately after record deletion.
`legacy-default` is protected. The older DELETE endpoint remains archive-only.
Owner-scoped Playground conversations/media and installation-wide assets are not
workspace records and are not deleted by this operation.

Sessions use the wide Playground-style message layout, a 256–288px desktop sidebar with compact search and session rows
and a 40px header. Tool activity is expandable rather than replacing the transcript.
The chat agent loop reserves its last available iteration (after a tool step) for
a text-only final response, repairs one empty response within its existing budget,
and persists final/interruption text before sending `done`. A persistence failure
emits an error so the UI retains the received answer. Task-chat imports expose the
task result as an assistant message, including task_complete-only delegation runs.

The Sessions composer has aligned 40px controls, agent grouping by `config.group`,
and file picker / drag-and-drop / paste attachments. All file types are accepted
within 4 files, 5 MiB each and 8 MiB total; the message JSON is capped at 12 MiB.
Attachments are base64 payloads in the workspace-owned message JSON (not personal
Playground media). Standard-library MIME sniffing normalizes types without image
processing dependencies. Small UTF-8 text is sent as text, common images as image
blocks, and other files as native document blocks with their original filenames
where the adapter supports them. Model/provider format limitations still apply
and surface as errors. History replay and Retry preserve the files; the transcript
offers image previews and download actions. No attachment migration is required.

Files uses the shared `fileServeUrl` helper, which pins a nonsecret `workspace_id`
query selector to native media/download URLs. Only GET/HEAD `/api/v1/files/serve`
accepts this alternative to `X-AT-Workspace-ID`; ambiguous query/header selections
are rejected and session, membership, execution policy and rooted path admission
still run on every request (including Range). The media service worker leaves
explicitly scoped URLs alone, so previews/downloads work without its intervention.
Files has neutral theme tokens, wrapping controls, always-visible touch actions
and a full-width mobile preview. Navbar account menus display the verified role /
username; the opaque account ID is copyable under Settings → Account security.

Model discovery endpoints retain the selected workspace when resolving stored
credentials. Anthropic discovery uses `/v1/models`, refreshes/persists Claude
OAuth credentials when needed, normalizes `/v1` and `/v1/messages` base URLs,
and reports upstream failures instead of returning `models: null`.
GitHub Copilot discovery is supported: `GET <base>/models` is a real endpoint,
so the earlier "Copilot does not support model discovery" refusal was removed.
It authenticates with the short-lived Copilot JWT (the same exchange the provider
uses — the stored GitHub OAuth token is rejected there) and sends the editor
headers from `openai.CopilotDefaultHeaders`; `extra_headers` still override them.
The catalog labels each entry's capability, so chat and embedding discovery filter
on `capabilities.type` instead of a name heuristic, duplicate IDs (multi-version
models) collapse, and the chat endpoint's `api-version` query is dropped because
the catalog is unversioned. Models whose org policy is unaccepted are listed
rather than hidden — they fail at request time until enabled in GitHub's settings,
and hiding them would make an enableable model look unavailable. Discovery is
routed by `auth_type: copilot` or a `githubcopilot.com` base URL.
Pricing previews and AI pricing targets use the workspace provider catalog;
the price-reference dropdown is the external pricing catalog, not the user's
provider-model list. Price records themselves remain installation-wide.

Claude, Copilot and ChatGPT first-authorization routes use the same workspace
admission as provider CRUD. PKCE/device-flow state is keyed by workspace, provider
ID and initiating user/session. Token-save helpers retain the initiating context;
workspace-local authorizations never overwrite the global gateway registry.
`provider-auth-workspace_test.go` exercises the Providers page's create → OAuth
start → callback/token-paste flow through real native HTTP auth and PostgreSQL.

Bots' optional Long Video picker calls the installation-admin-only
`GET /api/v1/bots/video-templates` catalog. It returns validated template IDs/names
from the fixed Studio library used by Telegram dispatch, rather than requiring
arbitrary host-file browse permission to configure ordinary custom commands.

### Bot and gateway MCP execution identities

Bot dispatch and gateway MCP execution use revocable `execution_service_bindings`
(`bot` / `mcp` / `trigger`), independent of browser sessions. Bots and MCP Servers
editors expose **Execution identity → Run as**: select an active workspace member
or a platform administrator (administrator identities may only be bound by a
platform administrator). The current administrator is listed even without an
explicit workspace membership row. Configure the workspace's execution policy
first: **Allow all tools and node types** selects trusted-host mode and persists
`allow_all_tools` / `allow_all_nodes`, including future registered implementations
and dynamic skill/MCP/delegation tools. Individual permissions use registry-backed
checkbox lists. These options retain live resource, membership and policy checks.
For existing records, configure the identity once;
then bind/renew. A policy or membership version change requires renewal. Bot
binding renewal stops the old adapter; **Bind & start bot** starts the new one.

Gateway MCP admission resolves routing metadata using the authenticated token's
persisted `workspace_id`; anonymous requests can resolve only a uniquely named
public server. It then resumes the server's `mcp` service binding and retains that
context through initialize/list/call. Private token MCP allowlists still apply.
Missing/stale bindings return actionable 403s; ambiguous public names return 409.
Machine credential accessors in `postgres/execution-machine.go` check the exact
service subject/version before returning executable configuration or bot tokens,
so human secret DTO redaction does not break bot startup. The scoped gateway
runtime supports builtins, skills, workflows and referenced MCP Sets; legacy
custom HTTP templates, pooled stdio and unowned URL routing remain outside that
scoped runtime.

Regression coverage: `internal/server/machine-admission_test.go` uses real
PostgreSQL for gateway initialize/list/tool-call, token workspace scoping,
browser logout, identity revocation, bot message persistence/reply, binding UI
APIs and credential redaction. Set `AT_TEST_POSTGRES_DSN` or run `make env` so
these tests execute rather than skip. Migration 48 adds the `mcp` binding kind.

Claude Code OAuth refreshes use `ClaudeOAuthTokenStorer.WithClaudeOAuthTokens`: a
workspace-owned provider row lock covers credential reload, the single-use OAuth
exchange and encrypted save. Anthropic invalidates the previous refresh token on
every exchange, so a source refreshing from a purely in-memory copy can replay a
credential another source already consumed and get `400 invalid_grant`. Every
token source for a provider — boot/gateway registry, the workspace execution
cache, model discovery and other replicas — goes through the same coordinator and
adopts a stored credential that is still fresh instead of exchanging again.
Rotation is encrypted, transactional and rejects stale credentials; failed saves
retain the exchanged credentials for an idempotent, previous-token checked retry
rather than rotating again. `wireClaudeOAuthCallback` takes an explicit workspace:
a silent default persisted rotations against the wrong row, which consumed the
stored credential without saving the replacement. An already-invalid refresh token
still requires reauthorization; a restart cannot recover a rotated token that an
earlier version never persisted. Regression coverage:
`internal/service/llm/antropic/auth-refresh_test.go`.

ChatGPT/Codex refresh uses `CodexOAuthTokenStorer`: a workspace-owned provider row
lock covers credential reload, the single-use OAuth exchange and encrypted save,
so inference and model discovery across instances do not race the same refresh
token. Failed saves retain the new credentials for an idempotent, previous-token
checked retry. Both gateway and scoped runtime providers wire the coordinator;
discovery uses the workspace+provider-ID cache, never a key-only global lookup.
The Codex model catalog uses `openai.CodexClientVersion`, independent of AT's
release version. Standard OpenAI preset URLs are normalized to the Codex Responses
endpoint for ChatGPT auth; custom relay URLs remain explicit overrides. Empty
Codex catalogs are reported as errors rather than silently returning no models.
ChatGPT/Codex auth does not support embeddings: embedding discovery returns an
explicit 400 after resolving stored auth, and the Providers editor explains that
a separate API-key OpenAI provider is required instead of offering an empty Fetch.

LLM providers, gateway API tokens, and bot adapters are configured at runtime through the UI (`/api/v1/providers`, `/api/v1/api-tokens`, `/api/v1/bots`) and persisted in the database. They are NOT accepted via YAML or env. The only YAML / env knobs are bootstrap-only: log level, server bind, store backend, telemetry.

## Unified LLM Tracing (traces / observations)

Langfuse-style trace → observation tracing covering the gateway **and** all three agentic loops. This replaced the former `audit_log` table entirely (dropped by migration 22; `/api/v1/audit*` endpoints removed). It is separate from `cost_events` (permanent per-call cost metrics feeding the Usage dashboard and budget enforcement — untouched).

- **Model**: `service.LLMCall` (`internal/service/types-llmcall.go`) + `LLMCallStorer`. Table `llm_calls` (migrations `21_llm_calls.sql` + `22_observations.sql`). Every row is one **observation** with `observation_type`: `generation` (LLM request/response pair), `tool` (tool execution, `input`/`output`, parented to its generation via `parent_observation_id`), or `event` (task lifecycle: `task_process_triggered`, `task_started`, `task_delegated`, `task_completed`/`_cancelled`/`_blocked`). Plus `name`, `level` (`default`/`warning`/`error`), `metadata` (JSON), trace/session IDs, source (`gateway` / `gateway_stream` / `responses` / `chat` / `agent` / `workflow`), token/agent/task/run/org attribution, token buckets, cost_cents, latency_ms, TTFT, status/error, finish_reason.
- **Trace identity**: org-delegation → one trace per `runOrgDelegation` run (trace ID rides the context via `contextWithOrgTraceID`; the parent pre-mints the child run's trace so the `delegate_to_*` tool observation cross-links it in `metadata.child_trace_id`), session = root task ID of the delegation tree. Chat sessions → one trace per agentic turn, session = chat session ID. Workflow `agent_call` → one trace per node run (Registry carries no workflow identity; session empty). Gateway → `x-at-trace-id` / `x-at-session-id` headers or generated; each `at_fallbacks` attempt is its own row on one trace.
- **Recorder**: `Server.recordLLMCallAsync` (`internal/server/llm-audit.go`) — fire-and-forget, returns the observation ID so callers parent tool observations under their generation. **Skeletons (tokens, cost, latency, hierarchy, 4 KB tool-IO previews) are recorded unconditionally**; the `llm_audit` feature flag (default ON, 30s cached toggle) gates **full-body capture only**. Bodies over `LLMCallBodyMaxBytes` (256 KB) truncate inline with the full payload spilled to `<WorkspaceRoot>/.at-llm-audit/<yyyy-mm-dd>/<id>-<side>.json`; oversized tool input/output spills the same way. The workflow engine reaches the recorder through the `RecordObservationFunc` seam (`workflow.Registry.RecordObservation`, wired by `Server.recordObservationFunc`), which replaced the old `RecordAuditFunc`.
- **Hooks**: gateway `ChatCompletions` / `Responses` / admin chat (as before, byte-faithful bodies, streaming reconstruction via `streamAuditResponseBody`); org-delegation loop (`org-delegation.go` — generations incl. provider errors, all six tool classes, lifecycle events); chat-session loop (`chat-sessions.go`); workflow `agent_call` node (`nodes/agent-call.go`). Loop generations store the **post-loopgov-windowing** request in AT's canonical shape (not provider wire format).
- **Hybrid OTEL export**: `emitLLMSpan` emits gen-ai spans for generations (`gen_ai.*`, `langfuse.trace.id`/`session.id`) and tool spans (`gen_ai.tool.name`, `gen_ai.operation.name=execute_tool`); events are DB-only. No-op when telemetry is off.
- **Retention (two-phase)**: `startLLMAuditJanitor` (`internal/server/llm-audit-janitor.go`) hourly — phase 1 nulls bodies (`ExpireLLMCallBodiesBefore`) after `LLMCallRetention` (7d) and sweeps spill dirs; phase 2 deletes rows after `ObservationRetention` (90d). Skeletons stay queryable between the two windows.
- **API/UI**: `GET /api/v1/llm-calls` (list, newest-first, filters incl. `observation_type`/`trace_id`/`task_id`/`session_id`), `GET /api/v1/llm-calls/traces` (GROUP BY trace aggregate: counts, token/cost sums, duration, error count), `GET /api/v1/llm-calls/{id}` (full record, spill-rehydrated). UI: `_ui/src/pages/LLMCalls.svelte` (route `/llm-calls`, "Traces" sidebar link) — trace list → nested observation tree with child-trace cross-links + detail drawer; the TaskDetail "Events" tab is the same data filtered by the task tree's `task_id`s (live-polled during delegation).

The Traces page defaults to **Conversations** (`GET /api/v1/llm-calls/conversations`):
server-paginated grouping by explicit session ID, token ID and source family.
`gateway`, `gateway_stream` and `responses` share a source family; requests without
a session ID remain separate traces, and different API tokens never share a group.
Totals include model calls, input/output tokens, cache read/write, errors and cost;
drilldown retains the conversation's token/session/source filters. Gateway correlation
recognizes `x-at-session-id`, `X-Session-Id`, `x-opencode-session`, then string
`metadata.session_id` / `metadata.conversation_id`. Cache keys and the `user` field
are not conversation identities. Existing rows with session IDs group immediately;
missing historical IDs are not guessed or rewritten.

## Connections & Connectors

External-service credentials are modeled in two layers:

- **Connectors** (`internal/service/types-connector.go`, `internal/server/connectors-registry.go`) — data-driven definitions of a connection *type* (the provider catalog). A connector carries an `auth_kind` (`oauth2` | `token` | `custom`), an optional OAuth2 block (`auth_url`, `token_url`, `scopes`, `use_pkce`, `userinfo_url`, `account_label_path`, …), and a `fields[]` credential schema that drives the UI form. Connectors hold **no secrets**, so the `connectors` table is unencrypted. The catalog is the merge of built-in JSON definitions (`internal/server/connectors/*.json`, embedded) and user-defined / override rows in the `connectors` table — **a DB row overrides a built-in by slug**. CRUD: `/api/v1/connectors` (+ inline "Manage providers" UI on the Connections page). This replaced the formerly hardcoded `google`/`youtube` OAuth map — new providers (GitHub, Spotify, …) are added by shipping a JSON file or creating one in the UI, no code change.
- **Connections** (`internal/service/types-connection.go`, `internal/server/connections.go`) — named, AES-256-GCM-encrypted credential *instances* bound to a connector by its slug (`Connection.Provider == Connector.Slug`). Multiple accounts per provider; agents/skills reference them by ID. The connection create/update API accepts a dynamic `fields` map (keyed by full var name, e.g. `spotify_client_id`); `connectorCredentialsFromValues` folds well-known suffixes (`_client_id` / `_client_secret` / `_refresh_token` / `_api_key`) onto the struct and the rest into `Extra`.

The OAuth2 flow (`internal/server/oauth.go`) is fully connector-driven and **supports PKCE** (verifier cached on `Server.oauthPKCE`, keyed by state for the callback flow or `provider+connection` for the manual paste-code flow). Token exchange sends `Accept: application/json` (so GitHub-style endpoints return JSON), omits `client_secret` for PKCE public clients, and no longer hard-requires a refresh token — when a provider returns only an access token it is stored under `<slug>_access_token`. Account labels are fetched generically via the connector's `userinfo_url` + `account_label_path` (a dot-path supporting array indices, e.g. `items.0.snippet.title`).

Skills can ship their own connector: `SkillTemplate.connector` (`internal/server/skill-templates.go`) is upserted into the registry on install when no connector with that slug exists, so a user-added skill brings its own connection type. Runtime credential resolution for skill handlers is unchanged (`internal/service/workflow/connection_resolver.go`): `getVar("<provider>_<suffix>")` resolves through per-skill → per-agent connection bindings → global variable.

## Persistent Assets & Avatar Studio

Reusable media (character portraits, cloned-voice manifests, series state and rendered episodes) live in a **persistent asset library** (`workflow.AssetsDir()`, `internal/service/workflow/assets.go`). A nonblank explicit `server.workspace.root` selects absolute `<root>/assets`; unset or blank preserves shipped `./data/assets`, resolved against the process working directory. Unlike per-task workspaces it is NOT swept by the workspace janitor: `assets` is reserved before task lookups, and symlink entries are skipped. Bash skill handlers receive it as `AT_ASSETS_DIR` (alongside `AT_WORK_DIR`); `GET /api/v1/info` reports it as `assets_root`; `POST /api/v1/files/upload` (multipart `file` + optional `path`/`name`, 256 MB cap) writes into it (default target when `path` omitted). `EnsureAssetsDir` creates the conventional roots: `avatars/`, `voices/`, `uploads/`, and `series/`.

`Server.New` calls the thread-safe `ConfigureAssetsDir` before starting janitors/bots. `EnsureAssetsDirReady` checks creation and actual writability with temporary probe files; startup errors include the path and `server.workspace.root` guidance but leave the gateway available. No temporary fallback or automatic migration is performed. Configure `server: {workspace: {root: /mnt/at-workspace}}` and mount that root on a persistent volume writable by the service user; `/tmp` does not guarantee reboot persistence. Existing installations leaving root unset must keep their `./data` mount. Moving an old `data/assets` library is manual: back it up, stop media producers, and review manifests/records for absolute path references before resuming; setting root never moves, copies, or deletes old media.

The HeyGen-style avatar pipeline is built from three pieces:

- **Skill templates**: `fal-avatar` (`create_avatar` Nano Banana 2 portrait/edit, `character_sheet` front/profile/back identity references, `update_character` bible metadata + voice binding, `talking_video` ByteDance OmniHuman v1.5 lip-sync — 60s@720p / 30s@1080p, `talking_video_budget` InfiniTalk, `lipsync_video` Sync Lipsync 2.0, library ops) and `elevenlabs-voice` (`clone_voice` IVC, `list_voices`, `generate_speech`). Both ship embedded `connector` blocks (`fal`, `elevenlabs`) so credentials are manageable as Connections; handlers are bash-wrapped python using the queue.fal.run submit→poll→download pattern with FAL CDN upload (data-URI fallback) for local inputs. Avatar manifests are backward-compatible character bibles: v2 optionally adds `turnarounds`, ordered `reference_images`, bound `voice`, `sora_character_id`, `lora_url`, `style_notes`, `wardrobe`, and `persona`.
- **Integration pack** `avatar-studio` (`internal/server/integration_packs/avatar-studio/`): org "Avatar Studio" with Studio Director (head) → Avatar Designer + Video Producer. Agents ship with empty provider/model (assigned at install by the Studio UI setup, or manually).
- **Studio UI** (`_ui/src/pages/Studio.svelte`, route `/studio`, sidebar "Studio"): one-click setup installs both studio packs + all media skills and patches agent providers. Tabs: Characters (portrait gallery, v2 bible editor, turnaround/Sora actions, quick talking-head video), Series (style/cast editor, episodes, live-polled shot storyboard with still/clip/provenance), Productions (structured `episode.json.final_video` playback plus regex fallback for legacy one-offs).

## Series Studio

Episodic production is filesystem-backed so bash skill handlers and the UI share one durable contract without new REST endpoints:

```
<assets-root>/series/<series-slug>/
  series.json                       # cast + style lock + model policy
  episodes/<NN>/
    episode.json                    # script + shots[] + per-shot provenance
    stills/<shot-id>.png
    shots/<shot-id>.mp4
    final.mp4
```

- **`series-library` skill** (`internal/server/skill_templates/series-library.json`): pure-local tools `series_create/get/list/update`, `episode_create/get/update`, `shots_set`, `shot_update`. Writes are atomic; workspace still/clip/final paths are copied into the persistent episode tree. Episode statuses: `draft|scripted|generating|assembled|published`; shot statuses: `planned|still_ready|generated|approved|failed`. The UI reads `final_video` from this manifest — free-text task regex is only a legacy fallback.
- **`fal-cinema` skill** (`internal/server/skill_templates/fal-cinema.json`): identity-preserving scene tools — Kling v3 Pro elements (`scene_video`), Veo 3.1 references (`scene_video_veo`), Seedance 2.5 long takes (`scene_video_long`), Vidu Q2 drafts (`scene_video_budget`), Veo/MiniMax first-last continuity, PixVerse transitions, local `extract_last_frame`, and Sora 2 character registry/generation. Doctrine: stills before clips, always pass the series style lock, draft cheap/final premium, chain shot N's last frame into shot N+1.
- **`ltx-video` skill** (`internal/server/skill_templates/ltx-video.json`): direct Lightricks API integration (not FAL), authenticated through the `ltx` connector's `ltx_api_key`. `scene_video_ltx25` routes prompt-only / `start_image` / `audio` to `api.ltx.io/v2/{text,image,audio}-to-video`, uploads local media through `/v1/upload`, polls the async job, and downloads the result. LTX-2.5 Fast supports up to 20s/4K; Pro up to 10s/1080p; both support native audio, camera motion, automatic duration, and first/last frames. For recurring identity, compose the multi-reference still first, because the managed LTX API accepts one start image rather than character-reference arrays.
- **Adjacent media additions**: `fal-image.edit_image_nano_banana` composes character/location/prop reference images into shot keyframes; `video-composer.generate_subtitles` + `burn_subtitles` provide Whisper→SRT→ffmpeg captioning.
- **Integration pack `video-series`** (`internal/server/integration_packs/video-series/`): org "Series Studio" with Showrunner (head) → Script Writer + Character Designer + Scene Director + Episode Editor. Agents ship with empty provider/model; the Studio setup assigns them. Every stage reads/writes `series_library` state instead of carrying the production only in LLM history.

Note: `internal/server/workflow_seeds/` is legacy/unreferenced — Integration Packs are the supported install mechanism.

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
- Single backend (`postgres/`) implements interfaces from `service/at.go`
- Private `fooRow` struct with `db:"..."` tags, converted via `fooRowToRecord(row)`
- SQL built with `goqu` query builder
- Updates re-fetch after write; `RowsAffected() == 0` → return `nil, nil`
- Factory: `store.New(ctx, cfg)` requires `store.postgres.datasource`; startup fails with a descriptive error when unset. Store tests run against a real postgres (`make env`) via `internal/store/postgres/postgrestest` and skip when unreachable; per-test isolation uses a unique table prefix

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

### PWA

The mobile client is the `_ui/` PWA; the separate Flutter project has been removed.
`public/manifest.webmanifest` uses relative URLs for prefix deployments. The existing
`public/workspace-media.js` owns both workspace media and network-first app navigation
with a public offline fallback. Only `offline.html` is cached; never cache API/auth,
chat or media responses. Bump its offline cache version when updating that page.
Worker activation does not reload pages. `pwa.svelte.ts` captures installation and
online events; Settings exposes installation guidance. The phone navigation uses a
modal dialog; the shell uses dynamic viewport height and safe-area padding.
See `_ui/README.md` for install, deployment and device verification instructions.

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

The app style is square (no `rounded*` on cards, inputs, buttons, badges or
modals), compact (`text-xs`/`text-sm`, `px-3 py-1.5`) and card-based: a bordered
`border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface` panel
with a `px-4 py-3 … bg-gray-50 dark:bg-dark-base` header strip and a `p-4` body.
Providers, Agents, Secrets, Features and Tokens are the reference pages.

The `.settings-*` classes in `src/style/global.css` (`@layer components`) exist
only to reproduce that system for the settings and auth surfaces without
repeating long utility strings. They previously defined a *second* design system
— rounded, airier, `text-2xl` titles, top-rules instead of cards — so a single
sidebar click between Permissions and API tokens visibly changed styles. Keep
them in sync with the reference pages; do not let them drift again. Note that
inline utilities beat `@layer components`, so a page that hardcodes `text-2xl`
overrides `.settings-title` — use the shared classes instead.

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
