# AT — LLM Gateway + Workflow Engine

## What This Is

OpenAI-compatible LLM gateway that routes requests to multiple providers (OpenAI, Anthropic, Vertex AI, Gemini) through a single `/gateway/v1/chat/completions` endpoint. Includes a DAG-based workflow engine and Svelte admin UI.

Module: `github.com/rakunlabs/at` — Go 1.27

## Architecture

```
cmd/at/main.go              → bootstrap: config → store → providers → server.Start
internal/server/            → HTTP handlers (ada framework), middleware, static UI
internal/gateway/wire/      → gateway wire DTOs + OpenAI/Anthropic translation (pure, no *Server)
internal/clientip/          → trusted-proxy aware client address resolution
internal/nativeauth/        → native sign-in: sessions, passkeys, MFA, mobile, external OAuth2, auth settings
internal/httpx/             → shared JSON response helpers
internal/service/           → domain types + store interfaces (at.go)
internal/service/workflow/  → DAG engine: parse → topoSort → run (concurrent fan-out)
internal/service/workflow/nodes/ → node types registered via init()
internal/service/llm/       → provider adapters: openai/, antropic/, gemini/, vertex/
internal/store/             → store factory → postgres (the only backend; required)
internal/crypto/            → AES-256-GCM credential encryption, key rotation
_ui/                        → Svelte 5 + Vite 8 + TailwindCSS 4 SPA
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

### Organization collaboration and task ownership

Organization task runs expose `consult_agent` for brief advice/review from active,
execution-authorized teammates (peers and managers included), independently of
the report-to tree. It creates no task: the caller remains responsible for the
deliverable. `delegate_to_*` still creates and executes a child task for a distinct
deliverable under the existing direct-report/depth rules. Task prompts distinguish
the two and discourage duplicate task creation around delegation calls.

Role-specific production pipelines retain their required specialists and stage
order; consultation is never a replacement for research, artifact writes or media
generation. Successful delegation results stay byte-for-byte intact (JSON and
exact-path handoffs); unsuccessful results retain leading `[BLOCKED]`,
`[ITERATION_LIMIT]` and similar markers while also reporting task status. This
preserves the YouTube Shorts Director's stage-failure contract. Regression:
`production-delegation_test.go` exercises three sequential specialist stages.

Consultation (`internal/server/org-consultation.go`) is one text-only model call
using the target agent's system prompt/model and explicitly supplied context.
It receives no tools, files or conversation history and cannot recursively consult
or delegate. The whole delegation tree shares eight consultation attempts per run,
including background `task_process` children. Input is bounded at 16 KiB; calls
inherit the caller's tool deadline with a 90-second additional ceiling. Normal
execution authorization, live organization membership, provider access, spending
budgets and loopgov input windowing apply. Usage is charged to the consultant on
the existing task; generation/tool observations share its trace/session. Incomplete,
empty and refused answers become tool errors for the owner to handle.

Task intake, Process and `task_process` retain the initiating request's runtime
identity when detaching from HTTP/tool cancellation; using `Server.ctx` lost that
identity and failed after accepting the job. Reservations additionally inherit
server shutdown and, for background children, the owning delegation's cancellation
(not the short-lived tool call's deadline). Retry delays are cancellation-aware.
Missing agents/providers and execution failures are blocked, not successful;
explicit cancellation stays cancelled. Failed child runs persist terminal state
through a bounded uncancelled context, and delegation results include the child's
status so a blocker cannot masquerade as a completed deliverable. Regressions:
`org-consultation_test.go`, `task-launch_test.go`, `delegations_test.go`.

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

### Shared agent-loop building blocks

Tool discovery and the policy-neutral pieces of the loops are shared rather than
copied per loop, so a tool an agent can use in Sessions is usable through org
delegation and Developer Spaces too:

- `internal/server/agent-tools.go` — `agentMCPTools` connects an agent's MCP sets
  (gateway loopback for MCP servers, set URLs, upstreams, server-side set tools)
  plus legacy `mcp_urls`, and dispatches them (`Owns` / `SetName` / `Call`).
  An optional `accept` hook lets Developer Spaces keep container tool names.
  `agentBuiltinTools` / `forkStatusBuiltinTools` build the admitted built-in
  definitions; `executionSkillLookup` (admission-checked) and
  `storeSkillLookup` (workflow registries) resolve skills by ID, then name.
- `internal/server/org-delegation.go` — `delegateToolDefinition` +
  `uniqueDelegateToolName` build `delegate_to_*` tools for org delegation and
  task-linked Sessions alike, so colliding agent names stay distinct in both.
- `internal/service/agentloop/history.go` — `SanitizeChatHistory` (stored rows),
  `SanitizeMessages` (in-memory messages), `IsToolPairingError` /
  `IsToolPairingMessage` (provider wording, also used by the Telegram bot) and
  `CallMCPTool`.

Loop lifecycle, confirmation, persistence and per-loop dispatch order remain in
each loop. Regressions: `agentloop/history_test.go`, `server/agent-tools_test.go`.

## Documentation-only skills and shell controls

Skills are Markdown instructions and resources. They never register or execute
tools, and code fences are inert. Legacy `Skill.Tools` payloads are converted to
Markdown references by `NormalizeDocumentationSkill`. Migration 82 recovers the
machine-readable payload preserved by migration 73 into a separate, paired MCP
set; new skill writes create/update the same MCP set transactionally. Agent loops
auto-attach that set from the existing skill reference, but execution still passes
MCP-set, handler and variable-resource admission. Only variables explicitly named
by a migrated handler are exposed to it; arbitrary shells never receive the whole
secret store. Ordinary external skills can describe tools supplied by any MCP set
already attached to the agent without gaining executable capability themselves.

Independently configured shell handlers (`internal/service/workflow/handler.go`)
still run under resource controls so a runaway workflow cannot peg the host:

1. **Process-group kill on cancel** — every bash handler is started in its own POSIX process group (`Setpgid: true`). When the surrounding context is cancelled (timeout, user Stop click, server shutdown), the watcher goroutine sends `SIGKILL` to the *entire group*, not just bash. This is what reaps long-running ffmpeg / python / curl children that would otherwise keep running after the agent task ended. Linux + Darwin only; Windows is a no-op stub (`handler_windows.go`).

2. **FFmpeg concurrency cap** — a process-wide `semaphore.Weighted` throttles bash handlers whose script body contains `ffmpeg` or `ffprobe`. The cap is `max(1, runtime.NumCPU()/2)`; on a 4 vCPU GCE box that's 2 concurrent encodes, with the rest queueing on `Acquire(ctx, 1)`. Auto-detected, not configurable today.

3. **Workspace janitor** — `internal/server/workspace-janitor.go` sweeps `WorkspaceRoot` once per hour and removes `<task-id>/` dirs whose owning task is in a terminal status (`done`, `completed`, `cancelled`, `blocked`) AND whose terminal timestamp is older than `loopgov.Config.WorkspaceTTL` (default 24h). Also sweeps `<WorkspaceRoot>/.at-tool-output/<run-id>/` dump dirs by mtime under the same TTL. Set `WorkspaceTTL: -1` to disable. Unknown task IDs (workspaces from a different deployment on shared FS) are *kept*, not nuked.

Arbitrary JS and Bash handlers receive only non-secret variables. Secret values
resolve only through typed references at controlled tool boundaries; initially,
HTTPS `http_request` headers with per-variable tool and host allowlists.

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
| `POST /gateway/v1/messages` | Native Anthropic Messages API, sync and streaming. Lets an unmodified Anthropic-native client (Claude Code, Cline, Roo, Kilo) point its base URL at `<base>/gateway` and get routing, fallback, budgets and tracing. Accepts `x-api-key` as well as `Authorization`. Bare model names resolve through routing profiles. Errors use the Anthropic envelope, not the OpenAI one. |
| `POST /gateway/v1/embeddings` | OpenAI-shape embeddings. Accepts string or `[]string` `input`. Backed by `service.EmbeddingProvider` (OpenAI, Cohere, Gemini). Providers can declare `embedding_models` (Provider UI: "Embedding Models" + discovery via `POST /api/v1/providers/discover-embedding-models`); these are advertised alongside chat models by `/gateway/v1/models` (advisory — unlisted models are still forwarded). |
| `POST /gateway/v1/responses` | OpenAI Responses API with streaming. Supports `input` (string or array of items), `instructions`, `tools` (function only), `tool_choice`, `reasoning.effort`, `text.format`, `parallel_tool_calls`, `metadata`. SSE event types: `response.created`, `response.output_item.added`, `response.output_text.delta`, `response.output_text.done`, `response.output_item.done`, `response.completed`, `response.failed`. Does NOT support `previous_response_id` (no server-side state). |
| `POST /gateway/v1/images/generations` | OpenAI-shape image generation. Backed by `service.ImageProvider` (OpenAI, MiniMax). |
| `POST /gateway/v1/audio/speech` | OpenAI TTS. Returns raw audio bytes. Backed by `service.AudioProvider`. |
| `POST /gateway/v1/audio/transcriptions` | Whisper-style multipart upload (`file`, `model`, `language?`, `prompt?`, `response_format?`). |
| `POST /gateway/v1/moderations` | OpenAI omni-moderation shape. Backed by `service.ModerationProvider`. |
| `POST /gateway/v1/rerank` | Cohere-shape rerank: `query`, `documents`, `top_n?`, `return_documents?`. Backed by `service.RerankProvider` (Cohere today). |
| `POST /gateway/v1/decisions` | System 1 typed decisions: `{model, state, questions}` plus any `/v1/systemone` control (`max_len`, `lang`, `min_confidence`, …), forwarded unchanged. Backed by `service.DecisionProvider` (`systemone` providers). The upstream body is returned verbatim (`answers`, `usage`, `routing`). See *System 1 decision services* below. |
| `POST /gateway/v1/scores` | Attach a quality score to a trace the same API token produced: `{trace_id, observation_id?, name, data_type?, value? \| bool_value? \| string_value?, comment?}`. See *Trace explorer*. |
| `GET /gateway/v1/media/{id}` | Download media the **same API token** produced through the gateway (e.g. `generate_image` over MCP), either with that token in `Authorization` or with the per-object `?key=` embedded in the returned `download_url` (expires after 24h, migration 93). Workspace- and token-scoped via `storage_objects.token_id` (migration 91); browser/agent media, other tokens' media and expired keys answer 404. Always `attachment` with `CSP: sandbox`. See *Image generation from any model*. |
| `GET /gateway/v1/health` | Liveness — returns `{status, providers{}, version}`. No auth required. |
| `GET /gateway/v1/health/{provider}` | Per-provider readiness check (without dialing upstream). |
| `GET /gateway/v1/models` | OpenAI-shape model list (chat + embedding models). |
| `GET /gateway/v1/model/info` | LiteLLM-shape model metadata for discovery clients. Uses the same gateway-token model filtering as `/models`; includes context/capability metadata and configured input/output/cache prices. Models without pricing remain listed with cost fields absent, while an explicit zero-price row is advertised as free. The Documentation page's opencode (V2) snippet pins `opencode-models-discovery@1.7.1` with `modelsDiscovery: {enabled, endpoint: "<deployment path>/gateway/v1/models", smartModelName: true, modelInfoFormat: "litellm", modelInfoEndpoint: "<deployment path>/gateway/v1/model/info"}`. The plugin's `omniroute` format reads only limits/capabilities from `/models` and never prices, so it left opencode without cost data; `litellm` reads limits, reasoning variants and prices from this endpoint. |
| `/gateway/v1/providers/{provider}/*` | Native provider passthrough — bypasses the OpenAI envelope. Useful for SigV4-signed Bedrock URLs, Cohere internal endpoints, etc. |
| `GET /gateway/v1/mcp/{name}/ws` | Raw WebSocket passthrough. When the named MCP server's `config.ws_upstream` is set (`{url, headers?, pass_query_params?, pass_headers?}`, `ws://`/`wss://`, header values support `{{var:key}}`), the upgrade request is reverse-proxied and frames are tunneled untouched. Same auth as the MCP endpoint (Bearer / `public` flag) plus a `?token=` query fallback for browser WS clients; AT's `Authorization`/`Cookie` never leak upstream. `pass_query_params` allowlists client query params (empty = all except AT `token`), and `pass_headers` allowlists raw client headers while preserving WebSocket handshake headers. |

### Supported provider types

| Type | Notes |
|---|---|
| `openai` | OpenAI + any OpenAI-compatible (Groq, Together, Fireworks, DeepSeek, xAI, Cerebras, Perplexity, Ollama, LM Studio…). `auth_type: copilot` for GitHub Copilot device-auth; `auth_type: chatgpt` for ChatGPT Plus/Pro OAuth through the Codex Responses backend. |
| `azure` | Azure OpenAI. `api_key` becomes `api-key` header; `base_url` must include the full deployment + `api-version`. |
| `anthropic` | Anthropic Claude with prompt caching ON by default (markers on system block + last tool + last message). Disable via `extra_headers: {at-prompt-caching: off}`. `auth_type: claude-code` for OAuth. |
| `bedrock` | AWS Bedrock Converse API. Credentials from `api_key` (`ACCESS:SECRET[:SESSION]`) or env (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`). Region from `base_url` host or `AWS_REGION`. |
| `vertex` | OpenAI-compatible Vertex AI endpoint. `credentials_json` (service-account key) or host ADC — see *Google Cloud credentials* below. |
| `vertex-gemini` | Native Gemini API on Vertex (keeps `thinkingConfig`, `safetySettings`, grounding). Needs `extra_headers.vertex_project` (or a `credentials_json` carrying its `project_id`); `extra_headers.vertex_region` defaults to `us-central1`. |
| `gemini` | Native Google Generative Language API (aistudio key). Default `safetySettings: BLOCK_NONE` on every category. Synthetic tool name `__google_search` / `web_search` activates Gemini grounding. |
| `cohere` | Native Cohere chat (v2/chat) + rerank (v2/rerank) + embeddings (v2/embed). |
| `minimax` | MiniMax via the Anthropic-compatible chat API + native MiniMax image/TTS endpoints. |
| `systemone` | External System 1 decision service speaking `POST /v1/systemone` — self-hosted Laya (`laya-serve`) or TypeSafe Jev. Decisions only; chat answers 501. `api_key` optional (Bearer). |

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
- **Reasoning effort** (`service/reasoning.go`) uses the vocabulary `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`. Adapters express different subsets: OpenAI/Azure/Vertex forward all of them, Anthropic `low`–`max`, MiniMax `low`–`high`, and Gemini `minimal`–`high`. Per-model lists come from the providers' published tables (`DetectModelReasoningEfforts`). Unrecognised models are reported as unknown, not guessed. `model_capabilities.<model>.reasoning_efforts` overrides detection, and `[]` marks a model as non-reasoning. Providers → Model capabilities → Reasoning edits this, and `provider_set_model_capability` accepts it. Claude 4.6+ receives `thinking: {type: adaptive}` plus `output_config.effort`, because 4.7+ rejects `{type: enabled}` with a 400. Earlier Claude models keep thinking budgets. Gemini `minimal` maps to `thinkingLevel: MINIMAL` on 3+ and to a 512-token budget on 2.5. The effective per-model lists are returned as `reasoning_efforts` by `/api/v1/info` and provider reads, and they drive the Chats, Agents and agent-builder pickers. The gateway advertises them as `capabilities.reasoning` / `reasoning_efforts` on `/gateway/v1/models`. On `/gateway/v1/model/info` they appear as LiteLLM `supports_reasoning`, every `supports_<level>_reasoning_effort` flag and `supported_openai_params`, which is how `opencode-models-discovery` builds variants. All flags are explicit because the client treats unset low/medium/high as supported. Only models with a known list get these fields. Regression: `internal/service/reasoning_test.go`, `TestApplyGatewayReasoningCapabilities`, `antropic/reasoning_test.go`.
- **Web search**: a synthetic tool named `web_search` (or `__google_search` / `google_search` on Gemini, `__web_search` on Anthropic) activates the provider's native internet search — Gemini/vertex-gemini `googleSearch` grounding, Anthropic server-side `web_search_20250305`. OpenAI search-preview models take `web_search_options` (also forwarded by the vertex adapter). Note the tool name is consumed by the provider: a user-defined function tool with the same name will not be called on those providers.
- **Gemini thinking** is selected per model generation, because the two field names are mutually exclusive and sending the wrong one is a 400: Gemini 3+ (`gemini-3*`, and later majors) gets `thinkingLevel` (`MINIMAL`/`LOW`/`MEDIUM`/`HIGH`), Gemini 2.5 and earlier get `thinkingBudget` in tokens. `reasoning_effort` low/medium/high maps to 2048/8192/24576 tokens or the matching level; an explicit `thinking` block wins over it, and `thinking.budget_tokens: 0` is preserved (`thinkingBudget` is a pointer, so a zero budget is no longer erased by `omitempty`). Any enabled config also sets `includeThoughts`, which is what actually makes Gemini emit `thought` parts — without it `reasoning_content` was always empty. Requests that ask for no thinking still send no `thinkingConfig` at all. See `geminiThinkingConfig` in `internal/service/llm/gemini/openai_compat.go`.
- **Gemini tool-call correlation** uses `functionCall.id` / `functionResponse.id` when the model supplies one. Matching results to calls by function name alone is ambiguous whenever a single turn calls the same function more than once, which is the normal parallel-tool-call case. Upstream IDs are preserved into `service.ToolCall.ID` and echoed symmetrically on both the call and the response. IDs this adapter minted itself (`call_<ulid>`, used only when the model sent none) are never replayed upstream, since Gemini never issued them.
- **Gemini usage** folds `toolUsePromptTokenCount` into `PromptTokens`: server-side tool input (Google Search grounding, code execution) is billed as input but reported outside `promptTokenCount`, so ignoring it under-reported cost on every grounded request. `cachedContentTokenCount` is subtracted from `promptTokenCount` because the latter is the total effective prompt size and already includes the cached prefix. Explicit context caching (the `cachedContents` resource) is not managed by AT; a pre-created handle can be passed through as `extra_body.cachedContent`. Implicit caching needs no wiring and works today — the request prefix AT builds is byte-stable across calls.
- **Finish reason vs tool calls**: adapters call `common.ReconcileToolCallFinish` after collecting tool calls, so a response carrying pending calls is always `Finished: false` / `finish_reason: "tool_calls"`. OpenAI itself reports `tool_calls`, but many OpenAI-compatible servers (Ollama, LM Studio, vLLM, several hosted gateways) return `"stop"` with a populated `tool_calls` array; the agent loops gate execution on `resp.Finished || len(resp.ToolCalls) == 0`, so taking that at face value silently dropped the calls and ended the run on whatever text came with them. Truncated/filtered responses (`length` / `content_filter`) drop their partial calls *before* reconciliation, so those stop reasons are preserved. `wire.NormalizeFinishReason` / `wire.MapStreamFinishReason` (`internal/gateway/wire`) apply the same rule at the gateway edge. Regression: `internal/service/llm/openai/compat-regression_test.go`.
- **`refusal`** is a first-class field (`service.LLMResponse.Refusal`). OpenAI returns it *instead of* content, with `finish_reason: "stop"`, on structured-output and safety refusals — so dropping it made a refusal indistinguishable from an empty response. It is forwarded in the gateway's `message.refusal` (the wire field already existed but was never populated) and reported by org delegation as a `REFUSED` result instead of an unexplained `EMPTY_RESPONSE`.
- Upstream provider errors surface as real gateway errors (429/5xx envelopes), never as HTTP-200 responses with error text in `content`.
- Provider `type` strings are validated on create/update against `service.SupportedProviderTypes` (openai, anthropic, azure, bedrock, vertex, vertex-gemini, gemini, cohere, minimax, systemone).

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

### Gateway CORS

`/gateway/v1/*` answers cross-origin browser calls from any origin
(`internal/server/gateway-cors.go`), so another AT installation can use this one
as a Chats local provider (`https://<host>/gateway/v1` + an API token) with no
configuration. Opening it is safe because admission is a header token, never
ambient state: credentials are not allowed and the gateway group strips
`Cookie`. Preflights reflect the requested header names (`*` never covers
`Authorization`), answer Chrome's Private Network Access preflight, and responses
expose `x-at-*` / `retry-after`. The gateway MCP endpoints (public servers admit
anonymous requests) and plugin downloads keep the default ada policy, as does the
rest of the application. Browsers cannot skip certificate verification, so
local providers have no insecure option; the UI points untrusted-certificate
endpoints at a server-side provider with `insecure_skip_verify`. Regression:
`TestGatewayCORS`.

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
| `at_fallbacks: ["provider/model", ...]` | `[]` (off) | When set, the gateway retries on the next entry if the primary fails with a retryable upstream error (429 / 529 / 5xx / timeout / context cancel). 4xx other than 429 does NOT trigger fallback. The model that actually served the response is reported in the `x-at-model-used` response header. Streaming requests fall back too, bounded by the commitment boundary: nothing is written to the client until the upstream stream opens, so an open failure is recoverable and a failure after the first chunk is not. |
| `extra_body: {...}` | `{}` (off) | Merged into the upstream provider request body **after** AT's own field mapping. Keys collide-overwrite our own keys. Use it for provider-native fields we don't surface yet (Anthropic `cache_control`, Gemini `safetySettings`, Bedrock `additionalModelRequestFields`, …). |
| `mock_response: "..."` | `""` (off) | When non-empty, returns a synthesized response immediately with no upstream call. Works for both sync and streaming. Streaming emits `role` → `content` → `finish=stop` chunks. Response carries `x-at-mock-response: true`. |
| `timeout_ms: <int>` | `0` (off) | Per-call deadline applied via `context.WithTimeout` across the entire fallback chain. `0` inherits the request context (no extra cap). |
| `Idempotency-Key: <string>` (header) | unset (off) | When present, the gateway caches the response (status + body + headers) for 5 minutes scoped to `(token_id, path, key)`. Subsequent requests with the same key replay the cached response and add `x-at-idempotent-replay: true`. 5xx responses are not cached. |

### Routing profiles

`at_fallbacks` is read from the request body, which the clients that most need it
cannot write: Claude Code, Cursor, Cline and Roo send a fixed body shape, so the
fallback engine was unreachable for them. A **routing profile** is a stored,
workspace-scoped name bound to an ordered list of `provider/model` targets. A
request whose `model` is that name expands to the chain and routes through the
same machinery.

Resolution lives in `chatCallChain` (`internal/server/gateway-extensions.go`) and
fires only when the request carries no `at_fallbacks` **and** the model contains
no `/`. Explicit beats implicit, and a qualified model is never looked up, so no
request that works today changes meaning. The absence of `/` is the whole
discriminator, which is why a profile name may not contain one — enforced in
`service.ValidateRoutingProfile`, the HTTP handler and a schema CHECK, because a
stored name with a slash would be permanently unreachable.

Expansion happens *before* `resolveModel`, so every target still passes the
unchanged token model-access and provider-allowlist checks: a profile grants
routing, never authorization. Targets the token cannot use are skipped; a profile
whose every target is denied reports against the profile rather than naming
models the caller may not enumerate. The serving name is reported in
`x-at-routing-profile`, and profiles are advertised by `GET /gateway/v1/models`
with `at_routing_profile: true` (as `object: "model"`, because a client with a
fixed model picker is the reason they exist). Managed at
`/api/v1/routing-profiles` under the `providers.read`/`.write` capability —
deliberately reusing it rather than introducing a kind no existing permission
bundle would carry. Migration `55`; feature key `routing_profiles`.

### Personal tokens reach their owner's providers

The gateway registry (`s.providers`) holds only Default-workspace providers by
bare key. A **personal** API token (`owner_user_id` set) additionally resolves
its owner as a live workspace principal (`gatewayTokenPrincipal`,
`internal/server/gateway-token-providers.go`): `/gateway/v1/models` (and
`/model/info`) list the owner's workspace catalog — personal providers as
`provider:<id>/<model>`, plus the token workspace's own providers — and
`resolveModel` falls back to `ResolveWorkspaceProviderRoute` for a key the
registry lacks. Previously such a token saw none of them, although Chats
offered them to the same account. Token allowlists, model grants, disabled
state and personal grants still apply; shared workspace tokens (no owner) keep
the registry-only behaviour. Regression: `TestGatewayPersonalTokenReachesOwnerProviders`.

### Provider governance: budgets and virtual providers

Migration 77. Settings page `/virtual-providers` ("Provider governance"),
feature key `provider_setup`, admission `providers.read` / `.write`.

**Budgets** (`provider_budget_policies`, one per real provider or virtual
provider) carry a period total, a default per-user allowance and the optional
`enforce_unpriced` switch ("Require model pricing"). When on, a model without
pricing is refused with `provider_pricing_required`; when off it is admitted
and settles at zero cost. Migration 86 made it opt-in (column default and UI
default `false`) because unpriced models — System 1 decision services report no
price at all — were blocked by default; policies saved earlier keep their
stored value.
Overrides per user are `custom`, `unlimited` or `blocked`; absence inherits the
default, and `unlimited` exempts only the user allowance, never the total. The
user is the authenticated principal (Chats, Sessions, execution) or the owner of
a personal API token, so an account's usage and all of its personal tokens share
one allowance — minting another token does not reset it. Shared workspace tokens
are charged to the provider total only; the request `user` / `metadata` fields
are never an identity.

Enforcement is `budgetedProvider` (`internal/server/provider-budget-runtime.go`),
applied wherever a route resolves: gateway chat/responses/embeddings/media/
passthrough, Chats, and `getExecutionProviderInfo` (Sessions, org delegation,
consultation, workflow nodes). Check order is real provider → virtual provider →
user → the existing API-token limit. Each call reserves its priced estimate
atomically under row locks (`ReserveProviderBudget`), so concurrent calls cannot
overshoot, then settles the actual cost. Streams without reported usage settle
the input estimate; passthrough settles from the adapter's proxy observation.
Reservations older than one hour are released by the next reservation, so a
crash cannot consume a budget permanently. Budget refusals are fallback-eligible
(`shouldFallback`), so an `at_fallbacks` chain or routing profile moves on;
without one the gateway answers 429 `provider_budget_exceeded` /
`provider_user_budget_exceeded`, 403 `provider_user_blocked` or 409
`provider_pricing_required`.

**Virtual providers** are *not* routing: each alias names exactly one
`provider/model` in the owning workspace and never substitutes another. They
reference only non-personal providers of their own workspace, and their key may
not collide with a physical provider key there. A grant
(`virtual_provider_grants`) exposes model aliases matching `model_patterns` to
another workspace, which uses them as `<key>/<alias>` without ever seeing the
underlying credentials. With `allow_user_overrides`, the recipient's
`providers.write` holders may set overrides for **their own active members
only**, capped by `max_user_limit_cents` (a positive ceiling also forbids
`unlimited`); the policy itself stays owner-only. Recipient override management
is enforced in the store but has no UI yet. Regression:
`internal/store/postgres/provider-governance_test.go`,
`internal/server/provider-governance_test.go`.

### Provider-declared model prices

Whoever manages a provider can price its models in the provider editor
(*Model pricing*), stored as `config.model_pricing.<model>` =
`{input, output, cache_read?, cache_write?}` in USD per 1M tokens (plain JSON
in the existing config, no migration; the keys must be the provider's chat or
embedding models). This is what lets an account price a **personal** provider:
the installation Pricing table is administrator-only and keyed by provider key.

`modelPricingFor` (`cost_helpers.go`) appends these prices after the
installation table, so the order is: installation row for the provider+model,
then the provider's own price, then an installation-wide (`""`) row. An
administrator therefore always overrides a self-declared price, which matters
because a shared personal provider's price is what other members' allowances are
charged at. All-zero means free; an empty row means unpriced. Lookup is
`ProviderModelPrices` (store, no principal — prices are not credentials):
personal providers by `provider:<id>`, bare keys in the caller's workspace with
fallback to Default, where a selected-workspace provider shadows Default's even
when it declares no prices. The workspace comes from the token, execution or
principal (`pricingWorkspaceID`).

`budgetedProvider` prices under `pricingKey` — the personal reference for a
personal provider. It previously used the bare key, so a personal provider
named like an installation provider reserved budget at that provider's price.
Cost events, Chats cost, budgets and `/gateway/v1/model/info` all use the same
lookup. Regressions: `internal/server/provider-pricing_test.go`,
`internal/store/postgres/provider-prices_test.go`,
`_ui/tests/model-pricing.test.mjs`.

### System 1 decision services

A decision model (Laya, TypeSafe Jev) answers typed questions about a state —
`choice` (a criteria key), `score` (an ordinal level), `noul` (probability the
statement holds) — in one forward pass with calibrated probabilities, and never
generates text. AT does not run one: it attaches a service that runs elsewhere
(a GPU box, a sidecar, the hosted Jev API) as a provider of type `systemone`
with a base URL and optional bearer key. Both implementations share the
`POST /v1/systemone` protocol, so one adapter (`internal/service/llm/systemone`)
covers them and any future compatible service.

The capability is `service.DecisionProvider` (`types-decision.go`), wrapped by
`budgetedProvider` and the scoped `executionProvider` like every other media
interface, so provider budgets, workspace admission and execution policy apply
unchanged. Request/answer payloads stay `map[string]any` on purpose: the
question vocabulary and per-answer fields belong to the upstream protocol, and
forwarding them means a new upstream field reaches callers without a code
change. AT validates only what every consumer relies on (non-empty state, 1–64
questions, known type, criteria on `choice`). Model `auto` (the default) omits
`model` upstream so the service routes; hook arguments are never forwarded.
Error mapping: 422/413 → 400 (`invalid_question` / `request_too_large`), 429/503
→ `RateLimitError` with Retry-After (laya-serve's busy signal), upstream 401/403
→ 502 `upstream_auth_failed` (the caller authenticated to AT fine), unreachable →
502 so fallback and cooldown treat it as an outage.

Three consumers:

- `POST /gateway/v1/decisions` — gateway auth, token model access, usage and a
  `decisions` trace observation. Decision models are **not** advertised by
  `/gateway/v1/models`, which would put them in chat pickers where they can only
  fail.
- Workflow node `decision` (`nodes/decision.go`, non-host capability): `state`
  in, `decided` / `escalate` out. Any answer the upstream flags
  `low_confidence` or whose `answer_confidence` (falling back to `confidence`)
  is below `min_confidence` selects `escalate`; an answer reporting no
  confidence is never escalated by the threshold. Both ports carry
  `{answers, model, low_confidence, state, usage, routing}`, read downstream
  with pointers such as `/answers/department/choice`.
- Built-in tool `decide` (non-host, "Other" family): lets an agent classify with
  one cheap call instead of a reasoning turn.

Shipped Laya checkpoints are weak zero-shot on domain decisions and
over-confident until temperature-fitted; the UI says so, and thresholds must be
fit on the operator's own labelled data. Regressions:
`internal/service/llm/systemone/systemone_test.go`,
`internal/server/gateway-decisions_test.go`,
`internal/server/builtin-tools-decide_test.go`,
`internal/service/workflow/nodes/decision_test.go`.

### Passthrough metering

`ProxyRequest` enforces auth, token spend limits, provider-disabled fail-closed,
token model access and the provider allowlist — but it used to record nothing.
`checkTokenLimits` gates on the accumulated `cost_events` sum that passthrough
never wrote to, so passthrough spend was invisible to budgets and to Traces, and
every adapter's `Proxy` took its rate-limiter slot with weight `0`, contributing
nothing to input-TPM.

It now records a cost event and an observation (`source: gateway_passthrough`,
its own conversation family) on success and failure alike. Usage carries an
explicit `usage_source`: `parsed`, `stream_parsed`, or `unavailable` when the
body is binary, unrecognised, or over the 256 KB inspection bound. **Token counts
are never estimated** — an estimate in the column real counts use would corrupt
budgets and the Usage dashboard — so an unreadable response records a zero-token
row rather than nothing, which is what made the gap invisible. Recording an empty
row is opt-in via `recordUsage(..., recordEmpty: true)`; the OpenAI-shape
endpoints keep the nothing-to-record shortcut, where a successful response always
reports usage. The estimate is used only for the limiter weight
(`common.ProxyInputWeight`), which is a self-imposed throttle and the one place
an approximation is the intended semantic. Observation happens through
`service.ContextWithProxyObserver`, which rides the context rather than the
`Proxy` signature that seven adapters implement and the gateway asserts
structurally; the response is never buffered.

**Upgrade note**: previously-uncounted passthrough spend now accumulates, so a
token close to its `SpendLimitCents` may begin failing. That is the defect being
fixed, but it is behaviour-visible.

### Provider cooldown

Upstream rate-limit headers were captured into `LLMResponse.Header`, forwarded to
the client, and never read, so a provider that had just reported an empty bucket
was still first in line on the next request. `internal/server/provider-availability.go`
parses the Anthropic (`anthropic-ratelimit-*-remaining`/`-reset`), OpenAI-family
(`x-ratelimit-remaining-*`/`-reset-*`) and `Retry-After` conventions into a
per-provider deadline. A typed 429/529 or a zero-remaining bucket enters
cooldown; a successful call reporting headroom clears it; a malformed header is
ignored rather than guessed at. Deadlines are clamped to `[1s, 15m]` because they
are upstream-controlled strings.

Chain resolution **stable-partitions** cooled targets to the back and never drops
them, so a single cooled target — or an all-cooled chain — is still attempted.
That is the fail-open property: a stale cooldown costs one wasted attempt,
whereas removal would turn bad cache state into a hard 503. State is in-memory
and per replica, deliberately not shared: a database write on the request path
for information that expires in seconds, and that each replica re-learns from its
own next 429, is a permanent cost for a benefit that evaporates. Health reports
`cooling` distinctly from `disabled`. `providerCooldownDisabled` is the in-code
rollback switch.

### Inbound shapes and the translation matrix

`buildProviderMessages` has always branched on provider family; there is no
OpenAI "pivot" to reuse. `[]OpenAIMessage` is an *inbound DTO*, and
`service.ContentBlock` is modelled on Anthropic's block shape (`Source` is
commented "Anthropic format"). Anthropic inbound therefore translates directly
per target rather than round-tripping through the OpenAI DTO, which would be a
lossy detour back to where the input started:

| | OpenAI-family target | Anthropic-family target |
|---|---|---|
| **OpenAI inbound** | `wire.TranslateOpenAIMessages` | `wire.TranslateOpenAIToAnthropic` |
| **Anthropic inbound** | `wire.TranslateAnthropicToOpenAI` | `wire.TranslateAnthropicMessages` (near-identity) |

The branch is evaluated per fallback attempt from the original request, so a
chain spanning provider families never derives one attempt from another's form.
This is attractive at two inbound shapes and bad at three (3×3 = 9 cells), which
is the usual argument for a pivot; Gemini and Ollama inbound are explicit
non-goals, and if a third is ever added the right pivot is `ContentBlock`, not
the OpenAI DTO.

**`ContentBlock.Content` is `any`** (string or `[]ContentBlock`), not a string.
A tool result may carry an image — a browser tool's screenshot is the ordinary
case — and the string field discarded every non-text part regardless of route.
Use `ContentText()` where a flat string is correct (token estimation,
OpenAI-shape output, Gemini `functionResponse`, Codex `function_call_output`) and
`ContentBlocks()` where the provider can carry structure (Anthropic native block
arrays, Bedrock Converse image/document tool-result blocks). The JSON tag is
unchanged and a string marshals identically, so persisted history stays readable
and no migration is needed. Regression: `internal/service/content-block_test.go`
asserts the string wire form byte-for-byte.

### Streaming fallback and the commitment boundary

Streaming used to take `chain[0]` and return, on the stated grounds that SSE
headers had already been flushed. That is not true where the decision is made:
`w.Header().Set` only populates the header map, and the upstream stream is opened
before any `Write`/`Flush`, so an open failure leaves the response completely
unwritten. Since every coding CLI streams, `at_fallbacks` was effectively dead
even for clients that could set it.

`handleStreamingChat` now returns `(committed bool, err error)`. `committed` is
set at exactly one place — immediately before the first chunk is written — so the
invariant "committed implies bytes may have reached the client" cannot drift. The
caller loops the chain on `shouldFallback(err)` while uncommitted, clearing the
headers a failed attempt staged (`clearStreamHeaders`), and renders a real status
code if every target fails. A mid-stream failure after commitment is reported on
the open stream and never advances. `handleStreamingResponses` got the same
treatment, which required emitting `response.created` lazily instead of before
the upstream open — the event carries no upstream information, so deferring it
costs nothing and is what makes the failure recoverable.


## Cron schedules: manual runs and run log

Migration 98. The settings page `/crons` is labelled **Cron schedules** and has
two tabs: *Schedules* (list with a ▶ **Run now** button per row) and *Runs*
(history per schedule, expandable log, polled every 10s while a run is live).

`POST /api/v1/triggers/{id}/run` → `Scheduler.RunNow` re-reads the trigger under
its **bound execution identity** and fires it exactly like a tick (same
authority checks, Telegram notifications, run history) in the background. No
binding answers 409 ("set Run as first"). Scheduled ticks and manual runs share
`Scheduler.fire`.

Every firing writes a `cron_runs` row (`source` schedule|manual, `triggered_by`)
plus `cron_run_logs` entries (`system`/`milestone`/`report`/`error`, ≤200 per
run, ≤200 runs per trigger; deleted with the trigger/workspace). Writes are bound
to the execution provenance's workspace (`service.CronRunStorer`); reads follow
trigger visibility (`GET /triggers/{id}/runs`, `GET /cron-runs/{id}`). Workflow
runs close when the engine returns and link `workflow_run_id`; organization runs
link the created task and stay `running` until the task finishes
(done→completed, blocked, cancelled, failed). The open run rides the context
(`workflow.ContextWithCronRun`) into the launcher.

Agents write to the log with the non-host built-in **`run_log`**
(`message`, optional final `status` completed|partial|failed, feature
`cron_triggers`). The run is resolved from the context or the task tree root,
never from arguments. A `status` stores `reported_status`/`reported_summary`,
shown next to — never instead of — the system status, because it is the
agent's claim. `telegram_notify` milestones are mirrored into the same log, so
a schedule without a Telegram chat still records them. Regression:
`internal/store/postgres/cron-runs_test.go`.

## Webhook servers (dedicated webhook ports)

Migration 92. A **webhook server** (`service.WebhookServer`, Webhooks → Servers)
is an extra listener, e.g. `:5050`, whose whole surface is `GET /healthz` plus
the webhooks bound to it — no UI, `/api`, `/auth` or `/gateway`
(`internal/server/webhook-listeners.go`). Servers are installation
administration (store checks `PlatformAdmin`); `all_workspaces` or
`webhook_server_workspaces` decides whose webhooks may be published on it.
Members list only servers open to their workspace, without TLS material, CIDRs
or other workspaces. Narrowing the audience deletes the excluded workspaces'
routes in the same transaction. TLS key and other settings live in an
encrypted `config` blob that rotates with the installation key; ports below
1024 and the main port are refused. Feature key `webhook_servers` (child of
`automation`); toggling it reloads listeners via `afterFeatureChange`.

A trigger's `webhook_routes` (`trigger_webhook_routes`) bind it to any number
of servers, each with an optional custom path (unique per server; empty means
"by alias or ID"). `hide_from_main` makes `/webhooks/{id}` answer 404 and
requires at least one route. On trigger update, nil `webhook_routes` /
`signature` preserve the stored values, so older clients cannot erase them.
`config.methods` selects accepted methods on servers (default POST).

Both entry points resolve routing metadata without a principal
(`ResolveMainWebhookRoute` / `ResolveWebhookRoute`, the gateway-MCP pattern)
and share `serveWebhook`: body bound → optional HMAC signature (`github`,
`stripe` with 5-minute tolerance, or custom `hmac_sha256`; secret encrypted in
`triggers.webhook_secret`, write-only `***`) → token check, where a token from
another workspace is now refused → the trigger's execution binding → the
trigger re-read under that identity. Note the main route previously failed
with 500 before reaching the binding because `GetTrigger` had no principal.
Triggers need an execution identity to run, now bindable from the webhook
editor (`GET/POST/DELETE /api/v1/triggers/{id}/execution-binding`).

Every request reaching a trigger is logged to `webhook_deliveries` (latest
100 per trigger, `GET /api/v1/triggers/{id}/deliveries`). Listeners are per
replica like the main port; a bind failure is reported as status `error` and
never stops the process (`POST /api/v1/webhook-servers/reload` retries). The
CIDR allowlist and per-client rate limit use the trusted-proxy client address.
Regressions: `internal/store/postgres/webhook-servers_test.go`,
`internal/server/webhook-servers_test.go` (binds a real port),
`_ui/tests/webhook-servers.test.mjs`.

## Runtime configuration

### Empty collections and independent page loading

Collection reads return 200 even when empty. `service.ListResult.MarshalJSON`
normalizes nil `data` to `[]`; `ListMeta` always includes total/offset/limit,
including zero. `httpResponseJSON` additionally normalizes nil bare slices and
slice-valued `items`/`data` map envelopes on 200 responses. It does not rewrite
optional records, nested configuration, byte payloads or custom marshalers.
Individual missing/foreign resources and unknown endpoints remain 404; store
failures remain errors. An existing agent without a configured budget returns
200/null from `/agents/{id}/budget`, after checking the parent agent exists.
Feature-disabled routes retain 404 and now include `code: feature_disabled` and
`feature`, so callers can distinguish admission from an empty result.

`_ui/src/lib/helper/page-load.svelte.ts` commits independent page resources as
they arrive, retains prior data on failure, reports per-section issues and skips
requests for known-disabled features. `LoadIssues.svelte` renders persistent
availability messages and retry. Use DataTable's `error`/`onretry` props for a
failed primary list, rather than presenting "No items" after a failed request.
Agent, Bot, Marketplace, Connection, Schedule, Webhook, Usage, Task and Token
surfaces use this pattern. The labels API intentionally returns a bare array;
`api/labels.ts` adapts it to the frontend's ListResult interface.
Regressions: `collection-contract_test.go`, `list-result_test.go`,
`_ui/tests/page-load.test.mjs`.

### Feature catalog

Settings → Features is a two-level tree of ~38 keys. The catalog
(`internal/server/features-catalog.go`) is hardcoded; `feature_settings` stores
only overrides, so a missing row means *enabled* and a fresh installation has no
rows.

The seven original coarse keys (`provider_setup`, `chat_workbench`, `agents`,
`automation`, `files`, `connections_integrations`, `organization_workflows`) were
kept as **parents** rather than renamed when the catalog was split. A child is
only reachable when its whole ancestor chain is enabled
(`featureEnabledIn`), so an installation that had disabled `chat_workbench` keeps
Playground, Sessions, Bots and transcription off without a data migration, and
turning the parent back on restores each child to its own stored switch. The
converse does not hold: disabling every child leaves the parent enabled, because
a parent also owns surfaces its children do not. The API reports both `enabled`
(own switch) and `effective` (after the chain), plus `blocked_by`.

Resolution goes through a whole-catalog snapshot cached for 10s
(`internal/server/features-gate.go`), not a per-key `SELECT`: a child walks its
ancestors, so the previous per-key read turned one admission decision into
several round-trips. Writes on the replica invalidate immediately and also reset
the longer-lived `llmAudit` cache, which a toggle previously left stale for up to
30s. A refresh that fails serves the last snapshot rather than failing every
gated request.

Route → feature matching is on **path segments**, not prefixes
(`featureKeyForRoute`): `/agents/{id}/runs` belongs to `agent_heartbeats` while
`/agents` belongs to `agents`, and a prefix rule for `/runs` would also claim a
future `/runs-export`. Each route names the most specific owner; the ancestor
walk supplies the rest. Runtime routes (`registerRuntimeRoutes`) are wrapped
individually because they sit outside `apiGroup` — the `files` routing entry
existed but nothing enforced it. `/gateway/v1/*` is never feature-gated.

Behaviour worth knowing:

- `webhook_triggers` / `cron_triggers` gate **execution**, not management. The
  Webhooks and Schedules pages stay editable (they map to `workflow_builder`)
  while their triggers do not fire; disabling `cron_triggers` stops the scheduler
  immediately and schedules resume on re-enable.
- `llm_audit` is now a child of `llm_traces` and controls **body capture only**;
  `/api/v1/llm-calls` is gated by `llm_traces`. Previously `llm_audit` gated both,
  so turning off body capture also 404'd the Traces API while the UI kept showing
  the page.
- `builtin_tools` is a real master switch: `dispatchBuiltinTool` checks it for
  every tool, including the ones no specific feature owns (`todo_*`,
  `batch_execute`, user preferences), which would otherwise stay usable.
- `builtin_other` independently gates all built-ins outside Shell, Script and
  HTTP. Resource features remain additional dependencies: Files may be enabled
  while its tools are off. Discovery and dispatch use `builtinToolFeatureKeys`;
  runtime revalidation and per-iteration tool lists also respect disabling.
  The configuration-only `include_disabled=true` option on the built-in catalog
  supplies family/group and `disabled_by` metadata for Chat/Agent pickers; those
  entries must never be sent as runnable tools. `BuiltinToolPicker.svelte` groups
  individual selections and preserves unavailable saved choices with an explicit
  explanation. Gateway + chat presets leave Other off; agent platform and full
  include it. Missing overrides preserve existing installations' behavior.
- `provider_setup` deliberately leaves `GET /api/v1/providers` open; model
  pickers across the UI need the list when management is closed.
- `guides` is presented as **Documentation** and owns the whole `/docs` surface —
  the API reference, the built-in guides, the user-authored guide library and the
  `guide_*` tools. It used to gate only `/api/v1/guides`, while the page and its
  sidebar link were exempt, so disabling it left a reachable page whose guide
  list answered 404 and raised a load-error toast. The route now sits in
  `routeFeatures`, `routes.ts` wraps it in `guarded()` and the sidebar's bottom
  nav filters the link, the same as any other page. Its key stays `guides`:
  `feature_settings` is keyed by name and a missing row means *enabled*, so
  renaming would have silently re-enabled the surface wherever it was off. Every
  preset enables it — a gateway-only installation is the one that most needs the
  API reference to configure a client.
- `workspace_management` is presented as **Workspaces** and is the only feature
  that changes *admission* rather than just hiding a surface. Disabling it gates
  creating/renaming/archiving/deleting a workspace, membership, invitations and
  `POST /auth/invitations/accept`, and additionally pins every request to
  `service.DefaultWorkspaceID`: `workspaceAuthentication` refuses an
  `X-AT-Workspace-ID` naming any other workspace, and `ListWorkspacesAPI`
  returns only the default one so the switcher cannot offer an entry that would
  then 403. Selection is **refused, not rewritten** — silently substituting the
  default would serve one workspace's data under another's identity. Nothing is
  deleted: the other workspaces' records are untouched and reappear the moment
  it is re-enabled. `GET /api/v1/workspaces`, `GET /api/v1/workspaces/{id}`,
  `/auth/workspaces/*` and the `provider-grants` / `execution-policy`
  sub-resources stay open, because the application resolves its workspace
  through them and those two sub-resources configure other features. Execution
  identities (`execution_service_bindings` for bots and gateway MCP) resolve
  outside this middleware and are deliberately unaffected, so a bot bound to a
  non-default workspace keeps running. `/settings/workspace` is **not** in
  `routeFeatures`: it is the join-a-workspace and sign-in-preference escape
  hatch, so the page stays reachable and hides its own sections instead.
  Regression: `TestWorkspaceManagementDisabledPinsDefault`.
- Nothing gates `/api/v1/features`, `/api/v1/info`, `/auth/*` or the Settings
  shell, so any combination is reversible from the Features page. The one
  exception is `/auth/invitations/*`, which belongs to `workspace_management`.
- `GET /api/v1/features` is a **shared platform route**
  (`sharedPlatformRoutes`), so every signed-in account may read the catalog. It
  had no `BusinessRoutePolicy`, which meant `requireWorkspacePlatform(true)` —
  installation administrator only. Because `isFeatureEnabled` reports *enabled*
  until the catalog arrives, a non-administrator's 403 left the store unloaded
  and every disabled feature visible and linked, for exactly the accounts that
  cannot change it. The response is installation configuration, not workspace
  data, and it is already the answer to "which pages does this deployment have"
  that the sidebar has to know. Writes (`PUT /features`, `PUT /features/{key}`,
  the presets) keep the default admission. Regressions:
  `TestFeatureCatalogReadIsShared`, plus the feature cases in
  `TestSharedPlatformRoutesAdmitNonAdministrators`.

Bulk writes exist because the catalog is fine-grained: `PUT /api/v1/features`
takes `{"features": {"<key>": bool}}` and `POST /api/v1/features/presets/{preset}`
applies a named target state (`minimal`, `gateway_traced`, `gateway_chat`,
`agent_platform`, `full`). A preset writes an explicit row for **every** key —
enabled for the listed ones plus their ancestors, disabled for the rest — so
applying one is deterministic rather than a diff against whatever was there.
Both answer with the whole refreshed catalog, because a parent toggle changes
what every descendant resolves to. Long-running subsystems (cron scheduler, bot
adapters) are resynchronised once per write from the *effective* state, not from
"which key was written", so a parent toggle and a child toggle converge.

Adding a feature: a constant in `internal/service/types-feature.go`, an entry in
`featureDefinitions` (parents before children, same group as the parent — asserted
by `TestFeatureCatalogIntegrity`), a segment arm in `featureKeyForRoute`, the TS
constant in `_ui/src/lib/api/features.ts`, and a route entry in
`_ui/src/lib/helper/feature-routes.ts`. No migration: the table is key-agnostic.

The UI keeps **one** route → feature map (`feature-routes.ts`), read by the
sidebar, the router guards and both settings indexes. They previously held
separate copies that drifted — the Traces link stayed visible while its API
answered 404, and Settings offered pages whose guard bounced straight back to
Home. `isFeatureEnabled` reads the *effective* flag and returns `true` until the
catalog loads, so a slow or failed request never hides the application.
Regression: `internal/server/features_test.go`.

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
administrator unlocks and session revocations are recorded with the resolved
client IP (see *Client address behind a reverse proxy*), bounded client
User-Agent and actor ID for administrator actions. Users displays
last sign-in and an expandable **Sign-in history** (latest 50 events, retained for
90 days by the bounded auth janitor); **Sign out all sessions** uses the existing
session-version revocation. GET `/auth/users/{id}/login-events` is installation-
admin-only. Audit storage failures are logged explicitly; last successful
sign-in metadata survives event retention. History begins at deployment.

### Installation accounts (Users)

`/settings/users` is the installation-admin account directory. Three properties
of it are load-bearing:

**New OAuth accounts take the provider's username.** Just-in-time provisioning
(`auth-external.go:CompleteAuthExternalIdentity`) seeds `auth_users.username`
from the normalized `ClaimUsername` value (`preferred_username` first). A unique
constraint conflict adds an eight-character random suffix and retries, never
adopts the conflicting account; absent names fall back to
`external-<lowercased ULID>`. Subsequent sign-ins refresh link metadata but keep
the account username stable. Existing accounts retain their names. Migration 59
adds `auth_users.externally_provisioned`, backfills the previously counted
`external-%` rows, and moves the 1000-account JIT ceiling to that durable marker,
so renaming or removing an identity link cannot bypass the ceiling.
`ListAuthUserIdentities` still
joins `auth_identity_links` for the page and the row is labelled from the link
(`authUserLabel` in `_ui/src/lib/api/auth.ts`) in decreasing order of what a
person recognises: the username the provider reports, then a verified email,
then any email, then the upstream subject. The stored username is unchanged and
still shown as secondary metadata: it is the unique key the account signs in
with, and rewriting it would collide in the one `username` column both local and
external accounts share.

Migration 56 adds `auth_identity_links.username`, filled from the token at every
sign-in by `service.ClaimUsername` — `preferred_username` first, then `nickname`
/ `name`, then ada's already-resolved `Identity.Name`. It is **display metadata,
never an identity**: the link stays keyed on provider ID plus subject, the value
is refreshed on each login (so an upstream rename follows), and a provider that
reassigns a handle therefore renames a row and grants nothing. The value is
normalized rather than refused — whitespace collapsed, control characters
dropped, truncated at 128 runes on a rune boundary, because a provider chooses
this string and an odd one must not be able to fail a sign-in or store an
invalid UTF-8 tail. Absent columns read as `''`, so nothing changes for accounts
that predate it until their next sign-in.

Migration 85 adds `auth_identity_links.display_name`, the person's full name
(`service.ClaimDisplayName`: OIDC `name`, else `given_name` + `family_name`; no
handle fallback). Same rules as `username`: normalized, refreshed at every
sign-in, searchable, never an identity. Existing links fill in on next sign-in.

The built-in `whoami` tool reports the account a run executes as (ID, username,
name, verified email, workspace, role, linked identities). It takes no
arguments and reads only the bound execution identity and the stored account,
so a conversation cannot make it describe someone else. Under a bot/MCP service
binding it reports the run-as account and marks `service`. Regression:
`TestWhoamiReportsBoundAccount`, `TestExternalPostgresDisplayName`.

The Users detail panel lays its identity block out on a
`grid-cols-[5.5rem_minmax(0,1fr)]`, not a flex row with a fixed-width term. The
term used to be the provider's raw ULID in a `w-20 shrink-0` box with no
wrapping, so it overflowed its own column and ran over the value beside it —
two opaque IDs rendered on top of each other. Grid columns cannot overlap and
`minmax(0,1fr)` is what lets the value wrap instead of widening the track. Each
link is now its own labelled sub-block titled with the provider's configured
label (from the public `/auth/login-providers` list, which reports enabled
providers only, so a link left by a disabled one falls back to its raw ID), and
every value under it is named rather than implied.

**Search is a store predicate, not a filter over the page.** `AuthUserQuery`
matches the account ID, the local username, and the identity link's email and
provider-reported username, with LIKE metacharacters escaped so a pasted address
is matched literally. The provider's username is searchable because it is often
the only name a provider that releases no email ever reports. The list
handler's query allowlist accepts `q` alongside `limit`/`after` and still 400s
on anything else. Without it an SSO installation is unnavigable: the only way to
find one of a thousand `external-…` rows is to page 50 at a time.

**Deletion is a real delete, and narrower than disable.** `DELETE
/auth/users/{id}` → `DeleteAuthUser` refuses the caller's own account and the
last active administrator, takes the same `auth_bootstrap` lock as
`InvalidateAuthUser` so two administrators cannot remove each other
concurrently, and maps the deferred `auth_settings_primary_guard` trigger's
23514 at COMMIT to a 409 — deleting the last external administrator while local
sign-in is off is refused by the database, not by the handler. Most references
cascade; `authUserDeletionTables` sweeps the ones that do not (workspace
membership and the permission/deny rows keyed on it, invitations the account
issued, execution service bindings, recovery events, mobile requests, Playground
history). Media entries in `storage_objects` are deliberately left: their rows
only point at backend blobs that the account-deletion transaction cannot delete,
so removing them would strand the blobs instead of freeing them. Disable remains
the reversible option and is what the UI still offers first.

`GET /auth/users/{id}` returns the same row plus its identities and *all* its
workspace memberships, revoked ones included — "revoked" is the answer to "why
does this account see nothing", and claim admission never restores one, so
hiding it would hide the explanation. The page renders it in an expandable row
together with recovery and sign-in history. Regressions:
`TestNativeAuthUserSearchDirectoryAndDeletion` (store),
`TestNativeAuthUserDetailAndDeletion` and the search cases in
`TestNativeAuthUserListing` (handlers).

### Navigation for an account with no workspace

A signed-in account that is a member of no workspace resolves nothing: every
workspace-scoped API answers 403, Documentation's guide API included. The shell
used to replace *every* route except `/settings/account` with the waiting
screen, so the Settings link was visible and unclickable, Documentation was
offered and dead, and Home looked like it did nothing because it rendered the
screen the user was already looking at.

`routeAllowed` (`_ui/src/lib/helper/navigation.ts`) now answers the admission
question first: with no membership and no platform role, only `/`, `/settings`,
`/settings/account` and `/settings/workspace` are allowed. That one predicate
drives the sidebar, the settings indexes and the shell, so the three cannot
disagree. `/` stays allowed deliberately — it is where the shell explains the
situation, names the account ID to hand to a workspace owner, and offers the
invitation form; a hidden Home would remove the explanation rather than the
confusion. `settingsLayout` is now just `settingsArea && routeAllowed(...)`,
which is what makes Settings open instead of being intercepted.

Two related trims: the Documentation link is filtered on `routeAllowed` as well
as its feature, and `settingsLinkVisible` hides `/settings/workspace` while
`workspace_management` is off, because single-workspace mode pins every request
to Default and leaves that page with one preference that cannot vary. The route
itself stays unguarded — it is still the join-a-workspace escape hatch when the
feature comes back. `/` renders the dashboard for everyone now; it previously
rendered Workspace settings for non-administrators, which meant Home and a
settings page were the same screen while that page was being hidden elsewhere.

### Capability routes vs administration surfaces

`navigation.ts` classifies every page twice over: `capabilityRoutes` names the
workspace capability that admits it, and `platformRoutes` marks it installation
administration. No API reports which of the two a route is, so the lists are
hardcoded — and that is exactly what drifts.

The rule is mechanical: a route belongs in `capabilityRoutes` **only** when its
API is capability-admitted, meaning it has an entry in
`workspaceBusinessPolicies()` (or in `workspaceRoutePolicies` /
`registerRuntimeRoutes`, which admit on capabilities from outside `apiGroup`).
Everything else registered on `apiGroup` falls through to
`requireWorkspacePlatform(true)` and is administrator-only. Eleven routes
claimed a capability their API never reads — Sessions, Skills,
Marketplaces, Integrations, Variables, Node configurations, Webhooks, Schedules,
Connections, MCP servers and MCP sets — so a member was shown the
link, opened the page, and every request it made answered 403. The capability
the UI named had no bearing on the decision, which made the map look like an
authorization model rather than the presentation registry it is.

The Playground, Usage and Traces moved out of that set by scoping their APIs.
The Playground rides `models.use` (rank 2): its history and media objects are
owner-scoped in the handlers, so the capability only gates entry, and the
playground chat endpoint resolves a **workspace member's** provider through
`workspaceProviderInfo` (workspace catalog, model grants, disabled state)
rather than the global gateway registry — an installation administrator keeps
the registry, which is what the endpoint always served. Usage and Traces ride
`usage.read` / `traces.read`, which the role ladder now hands out at **admin
rank** (they were installation-only surfaces, and trace bodies carry full
prompts), so a member reaches them only through a bundle. Row scoping follows:
`GetUsageSummary/Grouped/TimeSeries` and `ListAgentBudgets` bound a scoped
non-platform principal to their workspace while the installation administrator
keeps the installation-wide dashboard; `GetLLMCall`, `ListLLMCallTraces` and
`ListLLMCallConversations` bound any scoped principal to their workspace,
matching what `ListLLMCalls` already derived from `businessReadScope`, so one
Traces page cannot mix scopes. Regression: `TestWorkspacePostgresAnalyticsScope`
and `TestWorkspaceAnalyticsCapabilityRank`
(`internal/store/postgres/analytics-scope_test.go`).

Because `<img src>` cannot send `X-AT-Workspace-ID`, the workspace query
selector that `fileServeUrl` pins for `/api/v1/files/serve` also applies to
workspace-and-owner-scoped `GET /api/v1/media/{id}` (`nativeBlobReadPath`;
`mediaImageURL` takes the selected workspace as its second argument). Migration
70 originally added `media_objects.workspace_id`; migration 78 moves those rows
into the generic `storage_objects` catalog under namespace `media`, preserving
IDs and chat-share references. Historically ambiguous objects were backfilled
to Default and known chat-share workspaces were preserved. New backend
keys are `<workspace>/<owner>/<ulid>.<ext>`, so both the database lookup and
object namespace are partitioned. Durable backend configuration lives at
`/storage/settings*` and remains installation administration; media object
routes keep their separate workspace and owner scoping.

S3-compatible media storage defaults a blank SigV4 region to `us-east-1`, which
is MinIO's default region. The UI selects path-style addressing for a fresh S3
configuration because MinIO and most local gateways require it. A MinIO server
configured with a custom region still needs that exact value; an arbitrary
region is part of the signature and normally produces HTTP 400.

`/studio` and `/files` stay capability-gated because their data plane is
`/api/v1/files/*`, which `registerRuntimeRoutes` admits on `files.read`; only
Studio's one-click setup reaches administration APIs.

Files has a **Chat media** tab (`?view=media`, shown while `playground` is
enabled). Media keys (`<workspace>/<owner>/<ulid>.<ext>`) live outside the
Files tree (`workspaces/<ws>/files/`), so the tab lists the catalog instead of
browsing a folder: `GET /api/v1/media?before=&limit=` (`models.use`, ≤200,
newest first by ULID, `next_before` cursor) returns only the caller's own
objects in the selected workspace, the same scope as `GET /media/{id}`.
Regression: `TestMediaListHTTPContract`.

Sessions later moved out the same way — see *Per-user chat sessions and agent
tiers* below.

### Per-user chat sessions and agent tiers

Sessions → New can target an agent or an organization. An organization chat
stores `config.organization_chat` and `organization_id`, resolves the active head
on each turn, and uses a conversational copy of its prompt with only three scoped
tools: `organization_start_task`, `organization_task_status`, and
`organization_task_feedback`. The original production agent/config is unchanged;
requesting actual work launches its normal organization pipeline. Ordinary chat
does not create a task or expose production tools. The selected work is persisted
in `config.active_task_id`, not `task_id`, so discussion never overwrites a
production result or acquires task-linked delegation tools. Feedback records a
comment; it does not silently restart production. New work requires explicit
`new_task=true` once the previous job is terminal. The task link is shown in the
session header after a turn completes.

`CreateOrganizationChatTask` locks the session row and atomically creates the
task, execution provenance and active-task link, so concurrent requests across
replicas select the same job. Browser updates cannot retarget an organization
chat or forge its active task. Organization membership, head availability,
workspace execution authority and feature admission are checked again at use.
No migration: the mode and link use existing session JSONB. Regressions:
`internal/server/organization-chat_test.go`,
`internal/store/postgres/organization-chat_test.go`.

Chat sessions (`/sessions`, `/api/v1/chat/sessions*`) are **per-account**.
Migration 58 adds `chat_sessions.owner_user_id`; the HTTP handlers stamp it
from the authenticated principal on create (never from the request body) and
every read/write goes through `chatSessionForRequest`
(`internal/server/chat-sessions.go`): a scoped principal reaches only sessions
it owns in its selected workspace, a platform administrator additionally
reaches **ownerless** rows (bot/platform sessions and pre-existing data, whose
`owner_user_id` is `''`), and foreign sessions answer 404 exactly like unknown
ones so ownership cannot be probed (Playground precedent). `ListChatSessions`
applies the same rule as a SQL predicate. Bot adapters keep using the shared
store methods under execution identities — enforcement deliberately lives in
the handlers plus the list predicate, not in `GetChatSession`, because the
agentic loop and the Telegram/Discord paths load sessions the browser never
owns. Route admission moved from installation-administrator to
`agents.read` (look) / `agents.execute` (create, send, confirm, delete) in
`workspaceBusinessPolicies`, so `/sessions` now sits in `capabilityRoutes`.
Answering a pending tool confirmation is ownership-checked too.

Agents come in **three tiers**, decided at creation by the `scope` request
field (`workspace` | `personal` | `global`, no tier conversion afterwards):

- **workspace** (`owner_user_id = ''`) — unchanged: shared inside its
  workspace, writable by anyone with `agents.write`.
- **personal** (`owner_user_id = <account>`) — visible and resolvable only for
  its owner (platform administrators also see them); only the owner or an
  administrator may update/delete. The owner is always the authenticated
  account, never a client-supplied ID.
- **global** — a Default-workspace agent carrying
  `config.shared_with_all_workspaces` (the provider sharing pattern, so no
  extra column). Readable/usable from every workspace via the visibility
  union in `agentVisibilityScope` (`internal/store/postgres/agents.go`);
  writable only by a platform administrator in the Default workspace
  (`agentOwnershipWriteGuard`, re-checked against the *stored* row under a
  row lock in `lockAgentForWrite`). Personal ⊕ global is enforced. A global
  agent should reference a shared provider, or execution fails resolving the
  provider key in the caller's workspace.

Agent management follows the personal-resource publication model used by Skills
and MCP Sets. Anyone with `agents.read` may create, import, update and delete
their own personal agents; workspace-owned creation/updates/deletes still require
`agents.write`. `POST /api/v1/agents/{id}/publish` also requires `agents.write`
and creates an independent workspace-owned copy rather than transferring or
mutating the personal source. Publication revalidates provider, skill, MCP set,
workflow and connection references in workspace scope, so an unshared personal
dependency cannot be exposed through a workspace agent. The Agents UI defaults
new/imported agents to Personal and offers **Copy to workspace** to the owner
when their effective access includes `agents.write`. No migration is required.

`GetAgent` uses the same visibility scope, which is what lets the chat loop
run a personal or global agent; `service.DeriveAgentScope` reports the tier on
every read (`scope` field). Deleting an account sweeps its personal agents and
chat sessions (`authUserDeletionTables`; messages cascade on the session FK).
Regression: `TestAgentOwnershipTiersPostgres`,
`TestChatSessionOwnerScopePostgres`
(`internal/store/postgres/agent-ownership_test.go`),
`TestChatSessionForRequestOwnership`
(`internal/server/chat-session-ownership_test.go`).

### The Playground as an agent workbench

Playground transcripts were already per-account (`playground_conversations.owner_user_id`,
owner resolved from the authenticated subject, foreign rows answer 404). Three
things were not, and they are what made the surface unusable for anyone but an
installation administrator:

**Its tool plane was administration-only.** `/chats` is admitted at
`models.use`, but every endpoint its browser-side loop dispatches through had no
`BusinessRoutePolicy` and fell through to `requireWorkspacePlatform(true)`, so a
member got chat and no tools. They now ride the same entry capability
(`models.use` for built-ins and documentation skills, `mcp.read`/`mcp.use` for MCP sets),
with the read-only catalogs they need to offer a choice — `GET /skills`,
`GET /mcp/sets`, `GET /connections` — opened at the capability the store already
enforces on those tables. `ListConnections` blanks credentials without
`credentials.manage`, so the list carries no secret. Skills and Connections
management remains installation administration. MCP Sets are workspace
configuration: `mcp.read` opens the page/list/export, `mcp.write` admits CRUD,
import and template installation, and `mcp.use` governs Chat discovery and tool
calls. A writer receives the current full set configuration so saving cannot
erase redacted upstreams; read-only callers still need `credentials.manage` to
see credential-bearing fields. Upstream inspection is an `mcp.use` execution
action and revalidates the workspace execution policy before loading the
unredacted runtime config. Host binaries and stdio lifecycle remain installation
administration.

**Runtime calls bind an execution identity.** `CallMCPSetToolAPI` binds the
caller's runtime principal the way `dispatchBuiltinTool` does, and the set is
additionally gated by `mcp.use` against its own record. What a caller may
actually run is therefore decided by the workspace execution policy
(tool/inline_tool class, trusted-host for host tools, `platform.files` for the
legacy host-path file tools) rather than by route configuration. Admission says
"may use the workbench"; the policy says "may run this".

**Settings lived only on the conversation.** `GET/PUT /api/v1/chats/defaults`
stores a per-account preset in the existing `user_preferences` table (no
migration) and seeds a *new* conversation only — an opened conversation keeps
its own persisted config, because applying a preset over it would rewrite saved
history. The owner is the authenticated subject; the installation-wide
`/user-preferences` endpoints, which take a `user_id` from the caller, stay
administration.

**Named presets** (`GET/PUT /api/v1/chats/presets`, `models.use`) are the same
payload under a name, so the server shares one struct — `playgroundDefaults` is
an *alias* of `service.ChatWorkbenchSetup`, which `service.ChatPreset` embeds.
A preset is a default with a name; if the two wire shapes could drift, a setup
saved through one surface would come back incomplete through the other. They
live in `user_preferences` under `playground_presets` (no migration; the
historical `playground_` prefix is kept because the table is keyed by name and
renaming would orphan written rows).

Unlike the singleton default, a preset is applied **deliberately, to the
conversation already open** — switching setups mid-session is the reason for
having more than one — and it writes the conversation's own `config`, so a
reload keeps it. The transcript is never touched. PUT replaces the whole list
rather than patching one entry (the local-MCP registry shape): add, rename,
overwrite and delete are one call, at the cost of last-writer-wins between two
tabs, which for a personal picker beats a version column on a preference row.
Identity is server-assigned — a submitted `id` matching nothing stored becomes
a new entry with a minted one, so a client cannot claim an id it observed
elsewhere or backdate `created_at`. Names are unique case-insensitively: the
list is a picker, and two entries a reader cannot tell apart are a defect.

Migration 65 adds `workspace_chat_presets` for the shared half of the same
picker (`/api/v1/chats/workspace-presets`). Every member admitted to Chats may
list and apply workspace presets or publish a new one; `owner_user_id` is stamped
from the authenticated principal, and only that immutable creator may update or
delete the row. Applying somebody else's preset prepares a separately named
personal copy in the editor rather than pointing Overwrite at their row; the
reader may instead choose Workspace and publish that derived setup under a new
workspace-unique, case-insensitive name. Personal presets remain in
`user_preferences` unchanged and the toolbar groups both sources.

The toolbar switcher is controlled by a **derived** id: once
any selection diverges from the applied preset it reports "No preset" rather
than a stale name, and selecting "No preset" is a state, not an action — it
clears the claim, never the reader's selections. A preset naming an unavailable
model drops that reference and says so, because silently rewriting the model
pair would be worse than a toast. `frontend_tools` is the one selection where
nil and `[]` differ (shipped defaults vs. explicitly none), so normalization
preserves an empty non-nil list. Regressions:
`internal/server/chat-presets_test.go`, the preset cases in
`_ui/tests/playground.test.mjs`.

**The workbench is a dialog.** The setup used to be two collapsible strips
under the toolbar — system prompt and tools — each capped inside the chat
column at `max-h-80`, so together they took a third of the page while still
scrolling their own contents: the setup was cramped and the transcript was
too. They are one modal now (`showWorkbench`), because they answer one
question — what this conversation runs with. The top-aligned panel grows down
within the viewport; its body scrolls only after reaching the available height.
The system prompt leads it, followed by a dedicated Skills tab and the remaining
tool catalogues; the discovered-tool summary and **Clear my selections**
sit in a footer outside the scrolling body, since that summary is the answer to
"did that work?" and the reader who has scrolled to the bottom of the
catalogues is exactly who needs it. The toolbar keeps only the model select and
the preset switcher — the two controls worth one click — and its Workbench
button carries a dot when a system prompt is set, because the prompt is
otherwise no longer visible from the page. Escape, the backdrop and **Done**
all close it, and the panel is focused on open since Escape is handled there.

**Transcript timestamps.** `playground_messages.created_at` always existed and
the browser discarded it; worse, `persistPending()` ran only *after* the
completion, and the store stamps one `clock_timestamp()` per append, so a
question and its answer were recorded as having happened at the same instant —
the moment the answer finished. `sendMessage` now persists the user message
**before** running the completion, which costs one same-origin request per turn
and buys a server-authoritative send time plus a question that survives a turn
that never finishes. `MessageMeta.created_at` is set optimistically from the
browser clock and replaced by the stored value the moment the append returns,
so a reload shows the same stamp as the live transcript. An assistant entry is
stamped when its response *finishes* (including a turn that goes on to call
tools, and including an interrupted response that kept partial text); it stays
`''` while streaming, because showing a start time under a growing answer would
date it minutes early. `formatMessageTime` (`_ui/src/lib/helper/format.ts`)
renders the clock time alone for today and prefixes the date once the entry is
older — a bare `09:14` on a conversation resumed days later is actively
misleading — and returns `''` rather than a placeholder when there is nothing
to show.

Chats is an agent-independent workbench: its model, editable system prompt,
skills, MCP sets, built-in tools and browser tools are selected directly and
stored with the conversation/preset. Agents remain the reusable execution unit
for Sessions, bots and automation; Chats does not read or write `agent_id`.
Tool discovery waits for catalogs and discards stale results. Regression:
`_ui/tests/chat-workbench.test.mjs`.

**Slash commands and compaction.** Typing `/` on an empty composer lists
commands (↑↓ select, Enter run, Tab fill, Esc close); `/name args` on Enter
runs one, and an unknown name is sent as ordinary text so a message that only
starts with a slash is never swallowed. Built-ins (`/compact [focus]`, `/new`,
`/clear`, `/model`, `/preset`, `/effort`, `/tools`, `/share`, `/commands`) are
reserved names. Custom commands are prompt templates (`$ARGUMENTS`, `$1`..`$9`,
quotes group words; without a placeholder the arguments are appended) and may
pin a `provider/model` for that one message. They expand in the browser into an
ordinary user message, so a command grants nothing a typed message could not.
Personal commands live in `user_preferences` under `playground_commands`
(`GET`/`PUT /api/v1/chats/commands`, whole-list replace, server-assigned IDs);
workspace commands are rows in `workspace_chat_commands` (migration 94,
`/api/v1/chats/workspace-commands`), listed and run by every Chats member and
changed only by their creator — the preset model. All are `models.use`.

`/compact` asks the current model (tools off) to summarise what the model
currently sees, stores the answer as an assistant message with
`data.compaction: true`, and every later model call starts at the latest such
message (`buildRequestMessages`). Nothing is deleted: earlier rows stay on
screen, in storage, in shares and in forks. When older history is unloaded,
the server's `at_history_before` prefix expansion also starts at the latest
summary, and the browser omits the prefix entirely once a summary is loaded.
Compaction is manual; there is no automatic threshold. Regressions:
`internal/server/chat-commands_test.go`, `_ui/tests/chat-commands.test.mjs`,
the compaction cases in `_ui/tests/chat-runtime.test.mjs`.

**Conversation cost.** On the browser Chats endpoint only, the streamed usage
chunk carries `usage.at_cost_cents`, priced from installation model pricing by
the same `estimateGatewayUsageCost` that writes `cost_events`; it is absent for
an unpriced model, and gateway clients never receive the field
(`chatsStreamUsage`). The page stores each call's usage on its assistant row
(`data.at_usage: {prompt, completion, cost_cents?}`) so the session sidebar's
running cost survives reloads; calls without a price are counted as
"unpriced" rather than as zero. The total covers the loaded messages.
Regression: `TestChatsStreamUsageCarriesCost`.

**Agent-bound skills in Chats.** A selected skill with `context: fork` is not
pasted into the prompt: Chats exposes the same `load_skill` (`skill_name`,
`task`, `context`, `run_mode`) and `agent_run_status` tools as the Sessions
loop, so a skill behaves identically wherever it is selected. They call
`POST /api/v1/chats/skill-runs` and long-poll
`GET /api/v1/chats/skill-runs/{id}?wait=` (≤25s per request, so no proxy
idle limit is hit), reusing the Sessions subagent runner and its
`skills.use` / `agents.run` checks. The browser tracks each turn's background
runs; if the model answers without collecting them, the loop waits, appends
their results and asks again, so a started run always reports back.

**Run artifacts.** Each Chats skill run gets its own directory
(`<execution root>/chat-runs/<ulid>`) exposed to its tools as `AT_WORK_DIR`,
and the agent is told to save deliverables there. When the run ends
(including failed/cancelled background runs) every regular, non-hidden file
there is stored in the caller's media storage and reported as `artifacts:
[{media_id, name, content_type, size_bytes}]`; the directory is removed once
all files are delivered. Any number and size of files is accepted, bounded only
by the configured media backend's capacity. Artifacts stream from the run
directory to media storage instead of being buffered in memory. The type is sniffed from the bytes; the file name refines
only generic sniffs and can never promote a file into an inline type.
`GET /api/v1/media/{id}` serves images, PDF, audio and video `inline` and
everything else as an `attachment`, so HTML/SVG never renders on the
application origin (the existing `CSP: sandbox` and `nosniff` still apply).
The browser attaches the turn's artifacts to the final answer as `image` or
`file` parts, persisted with the transcript and carried into chat shares; a
`file` part reaches the model only as a short text reference. If storage is
disabled or rejects an upload, `artifacts_note` names what was not
delivered instead of dropping it silently. Without the run directory, skill
tools wrote to the server's `/tmp` and nothing reached the user.
Regression: `internal/server/chat-skill-runs_test.go`,
`internal/server/chat-artifacts_test.go`.

Direct MCP URLs were removed from the Playground: tools come from registered MCP
sets, which carry credentials, stdio processes and execution admission with
them. The `/mcp/list-tools` and `/mcp/call-tool` proxy endpoints are untouched
and remain installation administration. A conversation that stored `mcp_urls`
keeps the record — it is shown as a notice naming the URLs, never silently
dropped, and is only cleared when the reader dismisses it. Regression:
`internal/server/playground-defaults_test.go`, `_ui/tests/playground.test.mjs`.

Scoping one of those APIs backend-side is what moves its route back.
`TestUICapabilityRoutesAreCapabilityAdmitted` and `TestUIPlatformOnlySurfaces`
(`internal/server/ui-navigation_test.go`) read the two lists out of
`navigation.ts` and check each entry against the real admission tables, in both
directions, so neither a newly scoped API nor a newly added page can leave the
UI describing an authorization decision the server does not make. A route with
no probe fails rather than being skipped.

This is presentation only. `AccessPrincipal.Allows` plus the store's row
scoping remain the boundary; hiding a link has never been what stops a request.

### Client address behind a reverse proxy

The socket peer is the only address the process observes directly, and behind a
reverse proxy it is the proxy. Everything keyed on the caller's address —
`auth_login_events.source_ip`, `auth_users.last_login_ip`, the lockout audit log
line, the durable `AdmitAuthSecuritySource` bucket and the in-process mobile
`begin` limiter — therefore collapsed onto one address for the whole
installation, which makes the sign-in history useless and turns one abusive
client into a rate limit on everybody.

`server.trusted_proxies` (bootstrap YAML/env, default empty) lists the proxies
whose forwarded header is believed: CIDR blocks, single IPs, or the aliases
`loopback` / `private` — the aliases exist because an ingress pod's address is
assigned from a pool and cannot be enumerated. `server.trusted_proxy_header`
selects `X-Forwarded-For` (default), `X-Real-IP` or `Forwarded` (RFC 7239).
Both are validated at load, because a typo would degrade into "trust nothing",
whose symptom is identical to not configuring the feature at all.

This is deliberately **not** a runtime/workspace setting. Whoever sets it decides
whether callers may choose their own recorded IP, which is a property of the
network the process is deployed in. For `X-Forwarded-For`, only list proxies that
append their observed socket peer or replace the header with it; passing through
client input unchanged is unsafe. The right-to-left walk below makes appending
safe. A same-host Turna service proxy needs `trusted_proxies: ["loopback"]`:
`private` does not include `127.0.0.1` or `::1`. Prefer `X-Forwarded-For` over
Turna's legacy `X-Real-IP`, which can preserve a caller-supplied value.

Resolution (`clientip.Resolver`, `internal/clientip`) reads the header
only when the peer is trusted, then walks the chain **right to left** and returns
the first hop that is not itself a trusted proxy. That is what makes the result
unforgeable — a client may prepend anything to `X-Forwarded-For`, but its own
connection appends the one entry it cannot choose, and everything left of the
trusted suffix is ignored. An obfuscated or malformed hop ends the verifiable
chain and the peer is reported instead of a guess; the walk is bounded at 64
hops because the header is caller-influenced. `X-Real-IP` is single-valued, so a
comma list there is malformed rather than a chain. Addresses are normalized
(port dropped, IPv4-mapped IPv6 unmapped, zone removed) so one client is one key.

An address that cannot be parsed is recorded as `""` rather than a placeholder —
an audit column must not be able to lie. The two limiters differ on purpose:
durable admission keeps the raw form (one odd transport must not rate-limit every
other one with it), while the entry-capped in-process map collapses them into one
bucket (there the unbounded key space is the risk). With the default empty
configuration behaviour is byte-for-byte what it was before. Regression:
`internal/clientip/clientip_test.go`, `internal/config/trusted-proxies_test.go`,
plus the trusted-proxy cases in `TestPasswordLoginLockoutPostgres` and
`TestMobileBeginTrustedProxySources`.

### Sign-in screen presentation

`AuthSettings.LocalLoginCollapsed` (`local_login_collapsed`, Authentication
settings → *Hide the local sign-in form until it is requested*) makes the sign-in
screen lead with the configured identity providers and keep the username/password
form behind a **Local sign-in** disclosure in the card header. It lives in the
existing `auth_settings.config` JSONB, so absent keys read as `false` and no
migration is needed.

It is **presentation, not admission**: every local account can still sign in, and
`/auth/login` is unaffected. The access control remains `LocalLoginEnabled`, which
a database trigger (migration 37) refuses to turn off without a usable external
administrator. `GET /auth/status` therefore reports the collapse flag only while
local sign-in is actually enabled, so a stale value cannot describe a form the
browser is not allowed to show. Regression:
`TestAuthSettingsLocalLoginCollapsedPostgres`.

### Turning off passkey sign-in

`AuthSettings.PasskeyLoginDisabled` (`passkey_login_disabled`, Authentication
settings → *Allow passkey sign-in*) removes the passkey button from the sign-in
screen **and** refuses `/auth/passkeys/login/*`, next to the existing
`LocalLoginEnabled` check in `nativeAuthSettings.ServeHTTP` — hiding the button
alone would be a decorative switch. It lives in the existing `auth_settings.config`
JSONB and is stored inverted, so absent keys read as `false` and no installation
loses passkey sign-in on upgrade.

Scope is the **first factor only**: enrolment, listing, deletion and passkey
step-up (`/auth/reauth`) keep working, so an account can still manage its keys
and verify sensitive changes with one while the installation does not accept
them to sign in. `/auth/status` therefore reports two flags — `passkeys` (the
subsystem is usable at all, which Account security and `RecentAuth` depend on)
and `passkey_login_enabled` (it is accepted as a first factor). Collapsing them
into one would hide the passkey list behind a switch that does not govern it; a
UI talking to an older server treats the missing field as enabled.

No lockout guard is needed, unlike `LocalLoginEnabled` with its database
trigger: passkey sign-in is already gated on `LocalLoginEnabled`, so wherever
this switch can matter, password sign-in is available too. Regression:
`TestAuthSettingsPasskeyLoginDisabledPostgres`.

### Passkey sign-in is usernameless

`POST /auth/passkeys/login/begin` accepts an empty username and then issues a
discoverable ceremony: empty `allowCredentials`, so the authenticator offers the
accounts it holds for this RP and the asserted credential ID — `UNIQUE` in
`auth_passkeys` — identifies the account at `login/finish`
(`GetAuthPasskeyByCredential`). Typing a name the browser already knows was
pointless ceremony, and it made the button useless while the local form was
collapsed. The sign-in button therefore lives outside the password form and
forwards a username only when one happens to have been typed, which preserves
the username-first path for non-resident security keys whose credentials can
only be asserted from an explicit allow list.

Such a ceremony has no account to record, so migration 54 makes
`auth_challenges.user_id` nullable (NULL, not `''`: the column references
`auth_users`). The session version is read at finish rather than at begin, where
it was not knowable; the sign-counter CAS still refuses to commit if it changed
underneath. Per-account admission (`admitSecurityAccount`) also moves to finish —
until the credential arrives there is no account to rate-limit, so an anonymous
begin is bounded only by the global login limiter and the existing challenge
pool caps. Unlike the username-first path, it reads no account state and so
reveals nothing about which usernames exist. Regression:
`TestNativePasskeyDiscoverableLoginPostgres`.

### Google Cloud credentials (`vertex`, `vertex-gemini`)

Both Vertex types resolve credentials through `internal/service/llm/gcp`, in one
of two ways:

1. **`config.credentials_json`** — a service-account key file pasted or uploaded
   in the Providers editor ("Service account"). It is encrypted at rest with the
   rest of the provider config, so it belongs to one provider row and one
   workspace, and it can be rotated from the UI without touching the host.
2. **Application Default Credentials** when that field is empty — the previous
   and still supported behaviour. ADC is resolved from the *server process*
   (`GOOGLE_APPLICATION_CREDENTIALS`, the gcloud well-known file, or the
   GCE/Cloud Run/GKE metadata server) and is therefore installation-wide: every
   workspace authenticates as the same host identity.

The token exchange honours the provider's `proxy` and `insecure_skip_verify`.
This is not cosmetic: a network that can only reach Google through a proxy
cannot reach `oauth2.googleapis.com` either, and without it such a deployment
fails while *fetching the token* rather than while calling the model — an error
that does not mention the proxy. ADC discovery deliberately runs unproxied,
because on GCE it probes the link-local metadata server; only the file-based ADC
case (`FindDefaultCredentials` returns the key material, the metadata case
returns none) is rebuilt on the proxied context.

`ParseCredentials` accepts only `service_account` and `authorized_user`, and
this is a security boundary rather than a compatibility gap. `external_account`,
`external_account_authorized_user` and `impersonated_service_account` describe
*where to go and get* a credential — a URL the server fetches, or a command it
runs — and the file is submitted by a workspace administrator, who in AT's model
is not necessarily the platform operator. Accepting one would turn provider
configuration into server-side request forgery against the host's own metadata
server. Workload identity federation is still supported where it belongs: as the
host's ADC, which this package does not type-restrict because it came from the
host's own configuration. Construction uses
`google.CredentialsFromJSONWithType`, not the deprecated `CredentialsFromJSON`,
so the kind AT vetted is the kind oauth2 builds. The parser is also stricter
than oauth2 diagnostically — an OAuth *client secret* file (`installed`/`web`)
is the usual wrong download and is named as such.

`base_url` is optional for both types now. Left empty it is derived from the
project and region (`gcp.ChatCompletionsEndpoint` / `gcp.RegionalHost`), with the
project falling back to the `project_id` inside the stored key. The
`vertex-gemini` preset had always told operators to leave Base URL empty while
the factory rejected exactly that; an explicit `base_url` still wins.

`vertex.New` resolves ADC only when no `WithTokenSource` was supplied —
resolving it unconditionally failed provider construction on a host that has no
ADC, even for a provider carrying its own key.

Model discovery for both types (`internal/server/discover-vertex.go`) pages
Model Garden (`GET <regional host>/v1beta1/publishers/google/models`) with the
provider's own key or ADC, through its proxy, and keeps `gemini*` chat models.
Embedding discovery is `vertex-gemini` only and keeps `*embedding*` models;
the OpenAI-compatible `vertex` endpoint has no embeddings. Vertex does not
serve `batchEmbedContents`, so the gemini adapter's Vertex path (non-empty
path prefix) calls `:predict` with one instance per input, as
`gemini-embedding-001` requires. Regression:
`internal/server/discover-vertex_test.go`,
`TestCreateEmbeddingVertexUsesPredict`.

Writes: the field is redacted to `***` on read like `api_key`, and an omitted or
sentinel value preserves the stored key, because every writer (UI, the
`provider_update` MCP tool, scripts) submits the whole config and would
otherwise wipe a secret it never saw. Removal is an explicit
`clear_credentials_json: true` on the PUT body, and is deliberately not exposed
to the MCP tool. Regressions: `internal/service/llm/gcp/credentials_test.go`
(including a stub forward proxy that must see the token exchange),
`internal/service/llm/vertex/credentials_test.go`,
`internal/server/provider-credentials_test.go`.

### Provider claims, permission bundles and claim-driven admission

Workspace authorization is roles → permission bundles → provider-qualified
mappings → denies (`workspace-access.go:resolveWorkspaceAccess`). A bundle
(`service.PermissionBundle`, keyed per workspace) is the reusable "key"; a
mapping binds one immutable provider ID plus a claim kind/value to a bundle.

External login records the claims it may later match in
`auth_identity_links.asserted_permissions`, never in the local identity's roles.
Reading only top-level `roles` / `groups` / `permissions` / `scope` claims made
nested role sets invisible — Keycloak puts realm roles under
`realm_access.roles` and per-client roles under `resource_access.<client>.roles`.
A provider therefore declares `roles_claims` (Authentication settings → *Role
claim paths*): dot paths where a `*` segment matches every key of an object and
every element of an array. Their values are folded into the roles assertion that
mappings already match with claim kind `roles`, so no new claim kind and no
migration are needed, and a provider without declared paths behaves exactly as
before (opt-in: the same token asserts nothing extra). `service.HarvestClaimValues`
bounds depth (8 segments), fan-out (512 nodes), value count (64) and total bytes
(3072) because the assertion document is rejected past 8 KB and the claim shape
belongs to the provider. Values are whitespace-split, so a role containing a
space is not addressable this way. Provider-reported roles are never truncated —
they already worked without paths. Regression:
`internal/service/auth-claims_test.go`, plus the `nested_roles` case in
`TestExternalOAuth2LoginAndReplicaFlow`.

### Identity providers are explicit OAuth2 clients

An identity provider (`service.AuthIdentityProvider`, Authentication settings →
*Identity providers*) is a plain OAuth2 authorization-code client. There is no
protocol selector and no issuer URL: `auth_url`, `token_url`, `userinfo_url` and
`jwks_url` are each entered by hand. Discovery turned one stored string into
four endpoints fetched over the network — twice per sign-in, since `adapter`
runs at both `begin` and `callback` — so a provider's effective configuration
was whatever the remote document said that minute, a login could not start while
it was unreachable, and an IdP that publishes no document could not be used at
all. `no_discovery` in `TestExternalOAuth2LoginAndReplicaFlow` asserts that
neither leg fetches one.

This is also why `oauth2.Config.IssuerURL` stays empty rather than being kept
"for claim checking": in ada a non-empty issuer *is* the instruction to discover,
and `RequireIDToken` refuses to initialise without a discovered issuer and key
set. What survives is most of the value: `userinfo_url` and `jwks_url` are the
two claim sources and **at least one is required**, enforced by
`validateExternalProvider` and mirrored in the form. Userinfo is read with the
access token. JWKS instead verifies the `id_token`'s signature, audience
(ada defaults `Audience` to `ClientID`), expiry and nonce — every OIDC check
except `iss`, which nothing declares. Configuring both additionally binds them:
ada rejects a userinfo subject that disagrees with the `id_token` subject. Each
of those checks has its own subtest.

`subject_claim` is mandatory, because with no strict-OIDC mode ada would
otherwise fall back to `preferred_username` / `email` — claims an IdP may
reassign between people, which eventually hands one person another's account.

The identity namespace is `service.AuthIdentityNamespace(providerID)` —
`"oauth2:" + id`, stored in `auth_identity_links.issuer` and re-derived by the
store rather than trusted from the caller. It used to be the issuer URL for OIDC
providers, so an installation that had linked accounts through one **must not be
upgraded in place without rewriting that column**; there is no migration,
because the feature had no deployed OIDC users. The provider row's immutability
guard consequently narrows to `client_id` and `subject_claim`, the two fields
that still decide which upstream account a stored link belongs to.

### Restricting an identity provider to named addresses

`AuthIdentityProvider.AllowedEmails` (Authentication settings → *Allowed email
addresses*) is the answer to "only these people may sign in". An entry is either
one address (`ada@example.com`) or a domain (`@firma.com`); the list lives in the
existing `config` JSONB, so **no migration is needed** and an absent key reads as
*empty*, which admits everyone exactly as before. It is opt-in for a reason:
without it any successful ceremony against an enabled provider provisions an
account, which is correct for an IdP that already holds only your own people and
wrong for a public one, where the provider admits the world.

Three properties carry the design:

**It is checked on every sign-in, not at provisioning.** Enforcement sits in
`callbackResult` immediately before `CompleteAuthExternalIdentity`, and covers
`link` and `reauth` as well as `login`. Gating account creation alone would leave
every identity that has already signed in — precisely the ones an administrator
removes an entry to cut off — able to keep doing so, which makes the control
unable to revoke anything. The address checked is the one about to be written to
the link, so the decision cannot disagree with the record it admits.

**A non-empty list requires a verified address.** The strategy preserves reported
emails even without `email_verified: true`, storing the address and its
verification flag together on the identity link and refreshing both at sign-in.
`AuthEmailAdmits` requires both a non-empty address and verified status, because the
value of an allowlist is entirely in what it refuses: an address a provider hands
to whoever claims it is not an identity, and accepting one would let anybody who
can type a listed address into a public IdP's unverified profile walk in. The
practical consequence is a real limit — a provider that reports no verified email
(GitHub's user endpoint) cannot be restricted this way at all, and refuses every
sign-in while the list is non-empty. `ErrAuthEmailUnverified` is therefore a
*distinct* error from `ErrAuthEmailNotAllowed` and the UI says so, because
otherwise the administrator goes looking for a typo that is not there. Refusals
answer `403` (a decision, not the retryable `409`/`503` the rest of this path
uses) and are logged with provider, subject and reported address.

**Matching is lowercased and exact.** Both sides go through
`NormalizeAuthEmailAllowlist` (trim, lowercase, dedup) and the list is stored
canonicalized by `saveProvider`, so a difference in case or spacing is never the
reason a sign-in fails. This deliberately deviates from RFC 5321's
case-sensitive local part: no IdP treats `Ali@x.com` and `ali@x.com` as two
people, while the mismatch here is a lockout. Domain entries match that domain
only — `@firma.com` admitting `mail.firma.com` would silently widen the rule to
every name the provider's operator can create. A `*` is refused at validation
rather than stored as an address with the local part `*` that matches nobody.
Bounds: 256 entries, 320 bytes each.

`refuseSelfLockout` refuses a save whose list excludes the saving
administrator's own link on that provider — the one mistake that is not
reversible from the UI, since with local sign-in disabled the next refused
sign-in is theirs. It is a convenience guard, not a boundary, and fails open when
no browser account resolves; it cannot cover *another* administrator, whose links
this endpoint has no business enumerating. **Recovery from a self-inflicted
lockout is local sign-in, or clearing `allowed_emails` from the provider's
`config` JSONB.**

This is admission, not authorization: it decides whether an account exists and
grants nothing once it does. Workspace access still comes only from memberships
and permission mappings, which remain the right tool when the restriction should
follow a group or role claim instead of an address. Regression:
`internal/service/auth-email-allowlist_test.go`, the `allowed_*` / `denied_*`
subtests in `TestExternalOAuth2LoginAndReplicaFlow`, and the allowlist cases in
`TestExternalProviderValidation`.

Mappings are editable in place (POST `/api/v1/permission-mappings` with `id`),
which keeps the row identity that delete-and-recreate discarded; the Permissions
page offers the enabled identity providers as a dropdown and falls back to the
raw ID when a mapping names a disabled one.

Migration 53 adds `workspace_permission_mappings.admit_role`
(`'' | viewer | member | admin`, default `''`). A non-empty value makes a
matching external identity a member of that workspace when it has **no
membership row at all**; empty keeps the historical behaviour, where a mapping
only grants capabilities to somebody who is already a member. Limits are
deliberate and asserted by `TestWorkspacePostgresClaimAdmissionAndMappingEdit`:
`owner` is refused (schema CHECK plus `service.ValidWorkspaceAdmissionRole`), a
`revoked` membership is never resurrected and an existing role is never
rewritten, archived workspaces and disabled providers never admit, and
configuring an admitting mapping needs `members.manage` at or above the admitted
role — admission is membership authority, not permission authority, so
`permissions.manage` alone cannot grant it. Admission runs in
`ensureMappedMemberships` on two paths: the denied path of
`ResolveWorkspaceAccess` (so a member's request pays nothing, and the read
transaction's share lock is released before admission takes the row
exclusively), and `ListWorkspaces`, because a first-time single sign-on user has
no workspace to name yet. Each insert is logged with workspace, user, role,
provider and mapping ID.

The Permissions page presents admission as a **role select** covering all four
stored values. It was a checkbox that always wrote `viewer`, on the reasoning
that the bundle already says what a matching identity may do — but the role is
not the same question as the bundle. `WorkspaceRoleGrants` is a real grant source
and role *rank* additionally bounds `workspaceMayGrantRole`, invitation issue,
owner-membership edits and archive, none of which a bundle can reach. Admitting
an SSO group as `admin` was therefore expressible in the store and unreachable
from the UI. The select offers all three admissible roles unfiltered: the rank
ceiling is the store's (`SavePermissionMapping`), and `members.manage` already
sits at rank 3, so every actor who can reach the control can set every option a
client-side rank check would have left enabled.

Migration 57 makes the bundle **optional** for a mapping: `permission_id` is
nullable (NULL, not `''` — the composite foreign key is relaxed only by a NULL
member), a partial unique index keeps duplicate bundle-less rows off one claim
(the base UNIQUE treats NULLs as distinct), and a CHECK refuses a mapping with
neither a bundle nor an admission role, which would match claims and do
nothing. The role a mapping admits is itself a grant source, so requiring a
bundle only forced placeholder bundles into existence. A bundle-less mapping
grants nothing to existing members; `resolveWorkspaceAccess` skips it for
grants and `ensureMappedMemberships` still admits on it.

Nothing offers the read-only role presets (`GET /api/v1/permissions/presets`) in
the bundle dropdown, and the page no longer lists them: their IDs are synthetic
(`role:<name>`), so `workspacePermission` answers `ErrAccessResourceNotFound` for
one. The endpoint is kept — it is the only machine-readable projection of
`WorkspaceRoleGrants`.

### External sign-in returns through a popup bridge

`GET /auth/external/{provider}/callback` is a **top-level navigation** inside the
popup `_ui/src/lib/helper/auth-popup.ts` opened, not an XHR. It used to answer
with the login JSON, which left the browser parked on a page of JSON while the
opener waited for a `postMessage` that never came — external sign-in completed
on the server and hung in the UI at its last step.

`callback` now runs the real handler (`callbackResult`) into a recorder and
`deliverCallback` converts the finished response into the popup document. A
recorder rather than a streaming wrapper, because the decision needs the whole
response: a 3xx with `Location` is a ceremony step ada staged and is replayed
untouched; a 2xx JSON body becomes `{"type":"at-auth-result","result":…}`;
anything else — a refusal, a non-JSON body — becomes `{"error":true,"message":…}`
so the opener reports it instead of waiting out its five-minute timeout. Set-Cookie
survives (it is the entire point of the callback); `X-AT-Auth-Continue` is
consumed into the message's `continue` field rather than forwarded as a header
nobody read.

The document posts to the exact configured origin, never `*`: the payload can
carry an MFA challenge, and the wildcard would hand it to whatever page happens
to be the opener. It is `X-Frame-Options: DENY` with
`default-src 'none'; script-src 'nonce-…'`, and the payload rides a
`<script type="application/json">` block escaped with `json.HTMLEscape` so an
upstream error message cannot close the element it sits in. With no opener (the
popup reused as a tab) it navigates to the continuation or the app root, since
the session cookies are already set; a pending second factor says so instead.

ada's own flow (used by pika) answers `<script>window.close()</script>` and lets
the opener re-check `/auth/me`. That does not work here: AT's callback can return
`mfa_required` with a challenge the main window must continue with, so the result
has to travel back as data. Regression:
`internal/nativeauth/external-bridge_test.go` plus the bridge assertions
in `TestExternalOAuth2LoginAndReplicaFlow`.

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

A personal provider's **Workspace scope** only grants model use; the credential
stays account-owned and the row stays in the owner's Personal list. Choosing
Workspace while editing a personal provider instead calls POST
`/api/v1/personal-providers/{id}/move-to-workspace`
(`MovePersonalProviderToWorkspace`), which converts the row in place (same ID,
config and OAuth state; `owner_user_id` cleared, `workspace_id` set), drops its
personal grants, and rewrites that workspace's `provider:<id>` references in
agents, workflow graphs/versions and developer sessions to the bare key in one
transaction. It needs `personal_providers.manage`, `providers.write` and
`credentials.manage`; only the owner can move it, and an existing workspace
provider with the same key is a 409 with nothing moved. References in other
workspaces or in Chats conversations are not rewritten. Chats for an
installation administrator used to consult only the Default-workspace gateway
registry, so a `provider:<id>` reference or a non-default workspace's provider
answered "provider not found"; `chatProviderInfo` now falls back to the
workspace catalog. Regression: `TestMovePersonalProviderToWorkspace`,
`TestChatProviderInfoResolvesScopedProvidersForAdministrators`.

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

API Tokens creation offers **Workspace / Personal** ownership (`scope` on POST).
Migration 62 adds `tokens.owner_user_id`; empty preserves existing workspace
tokens. Personal ownership is stamped from the authenticated principal, never a
submitted owner ID, and is immutable after creation. Existing `tokens.read` /
`tokens.write` admission still applies (members can receive write via a permission
bundle). Personal tokens are visible/manageable only by their owner, workspace
admins/owners, and platform administrators within the selected workspace. The
store applies ownership before pagination/counting, on resource resolution, and
under the write transaction for edit/delete/pause/rotate; usage and reset use the
same management guard. Gateway hash lookup remains independent of management
visibility. Account deletion removes personal tokens. Regressions:
`token-ownership_test.go` in server and postgres.

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

Subagents launched from a Sessions turn (`agent_run`, or `load_skill` on a
`context: fork` skill) report back within that turn. Foreground `agent_run` is a
whole agentic loop, so it is exempt from the per-tool deadline (default 60s),
which used to cancel the child mid-run; the turn and the child's own iteration
budget still bound it. Background runs are recorded on the turn
(`turnBackgroundRuns`); when the model tries to finish while any has not been
reported, the loop waits for them and feeds their results back before the final
answer, since a result arriving after `done` had nowhere to go. A run already
read via `agent_run_status` is not reported twice. Progress streams as
`tool_progress` SSE events and the stream sends a `: ping` comment every 15s so
proxies do not drop a quiet connection. Regression:
`internal/server/chat-subagent-wait_test.go`.

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
A binding records only *who* the service runs as. `ResumeRuntimeSubject` starts
every run from the account's current membership version and the workspace's
current policy version (`liveServiceBindingVersions`), so changing the policy
or a member's role applies to the next bot message, schedule tick or MCP call
without renewing anything; a run already in progress still stops at its next
`CheckExecution` when either changes. Removing the member, revoking the binding
or rebinding (service version bump) still refuses the service. The editor
(`ExecutionBinding.svelte`, labelled **Run as**) defaults to the signed-in
account, so a new bot/schedule/MCP server is one click. **Save & start bot**
starts a bot. Regression: `TestRuntimeServiceBindingRenewalAcrossReplicas`.

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
release version. The catalog hides every model whose `minimal_client_version`
exceeds it, so a stale value silently drops the newest models from discovery;
keep it aligned with the latest `openai/codex` release. Standard OpenAI preset URLs are normalized to the Codex Responses
endpoint for ChatGPT auth; custom relay URLs remain explicit overrides. Empty
Codex catalogs are reported as errors rather than silently returning no models.
ChatGPT/Codex auth does not support embeddings: embedding discovery returns an
explicit 400 after resolving stored auth, and the Providers editor explains that
a separate API-key OpenAI provider is required instead of offering an empty Fetch.

LLM providers, gateway API tokens, and bot adapters are configured at runtime through the UI (`/api/v1/providers`, `/api/v1/api-tokens`, `/api/v1/bots`) and persisted in the database. They are NOT accepted via YAML or env. The only YAML / env knobs are bootstrap-only: log level, server bind, store backend, telemetry.

### Chats URLs and per-user usage

Chats uses `#/chats` / `#/chats/{id}` and `/api/v1/chats/{conversations,defaults,completions}`.
Navigation, feature admission and workspace capability policies use those paths.
Historical database tables, preference keys and the persisted `playground` feature
key retain their names so saved conversations and disabled-feature settings survive.
The shared `/api/v1/chat/completions` endpoint remains for other AI assistants.

Migration 60 adds `cost_events.user_id` and `source`. Browser chat accounting derives
the user from the authenticated principal (never the request's `user` or metadata),
and Sessions uses the persisted session owner for every loop iteration. Chats had
previously skipped accounting because `recordUsage` required an API token; it now
records browser calls, including streaming failures and calls without reported
tokens. Sources are `chats`, `sessions`, `assistant` and `gateway`; historical/system
rows without attribution remain explicitly unassigned. No guessed historical users.
Cost-event writes stamp the execution/browser workspace, or the gateway token's
workspace, instead of implicitly falling into Default.

Usage accepts repeated `user_id` and `source` filters and groups by `user` / `source`.
User labels resolve only for accounts represented by the workspace-scoped aggregate;
the UI does not call the installation account directory. A user selection filters
all charts and source breakdowns. Summed LLM duration is model time, not page dwell
time. Existing `usage.read` admission and platform-admin global scope remain.
Regression: `internal/server/user-usage_test.go`,
`internal/store/postgres/user-usage_test.go`.

API Tokens treats disabled optional restriction catalogs (Webhook targets / MCP
servers) as inactive features rather than page failures: their disabled notices
are omitted from the reference `LoadIssues`, while real request failures retain
their error/retry controls. The primary token-list error handling is independent.

## Unified LLM Tracing (traces / observations)

Langfuse-style trace → observation tracing covering the gateway **and** all three agentic loops. This replaced the former `audit_log` table entirely (dropped by migration 22; `/api/v1/audit*` endpoints removed). It is separate from `cost_events` (permanent per-call cost metrics feeding the Usage dashboard and budget enforcement — untouched).

- **Model**: `service.LLMCall` (`internal/service/types-llmcall.go`) + `LLMCallStorer`. Table `llm_calls` (migrations `21_llm_calls.sql` + `22_observations.sql`). Every row is one **observation** with `observation_type`: `generation` (LLM request/response pair), `tool` (tool execution, `input`/`output`, parented to its generation via `parent_observation_id`), or `event` (task lifecycle: `task_process_triggered`, `task_started`, `task_delegated`, `task_completed`/`_cancelled`/`_blocked`). Plus `name`, `level` (`default`/`warning`/`error`), `metadata` (JSON), trace/session IDs, source (`gateway` / `gateway_stream` / `responses` / `chat` / `agent` / `workflow`), token/agent/task/run/org attribution, token buckets, cost_cents, latency_ms, TTFT, status/error, finish_reason.
- **Trace identity**: org-delegation → one trace per `runOrgDelegation` run (trace ID rides the context via `contextWithOrgTraceID`; the parent pre-mints the child run's trace so the `delegate_to_*` tool observation cross-links it in `metadata.child_trace_id`), session = root task ID of the delegation tree. Chat sessions → one trace per agentic turn, session = chat session ID. Workflow `agent_call` → one trace per node run (Registry carries no workflow identity; session empty). Gateway → `x-at-trace-id` / `x-at-session-id` headers or generated; each `at_fallbacks` attempt is its own row on one trace.
- **Recorder**: `Server.recordLLMCallAsync` (`internal/server/llm-audit.go`) — fire-and-forget, returns the observation ID so callers parent tool observations under their generation. **Skeletons (tokens, cost, latency, hierarchy, 4 KB tool-IO previews) are recorded unconditionally**; the `llm_audit` feature flag (default ON, 30s cached toggle) gates **full-body capture only**. Bodies over `LLMCallBodyMaxBytes` (256 KB) truncate inline with the full payload spilled to `<WorkspaceRoot>/.at-llm-audit/<yyyy-mm-dd>/<id>-<side>.json`; oversized tool input/output spills the same way. The workflow engine reaches the recorder through the `RecordObservationFunc` seam (`workflow.Registry.RecordObservation`, wired by `Server.recordObservationFunc`), which replaced the old `RecordAuditFunc`.
- **Hooks**: gateway `ChatCompletions` / `Responses` / admin chat (as before, byte-faithful bodies, streaming reconstruction via `streamAuditResponseBody`); org-delegation loop (`org-delegation.go` — generations incl. provider errors, all six tool classes, lifecycle events); chat-session loop (`chat-sessions.go`); workflow `agent_call` node (`nodes/agent-call.go`). Loop generations store the **post-loopgov-windowing** request in AT's canonical shape (not provider wire format).
- **Hybrid OTEL export**: `emitLLMSpan` emits gen-ai spans for generations (`gen_ai.*`, `langfuse.trace.id`/`session.id`) and tool spans (`gen_ai.tool.name`, `gen_ai.operation.name=execute_tool`); events are DB-only. No-op when telemetry is off.
- **Retention (two-phase)**: `startLLMAuditJanitor` (`internal/server/llm-audit-janitor.go`) hourly — phase 1 nulls bodies (`ExpireLLMCallBodiesBefore`) after `LLMCallRetention` (7d) and sweeps spill dirs; phase 2 deletes rows after `ObservationRetention` (90d). Skeletons stay queryable between the two windows.
- **API**: `GET /api/v1/llm-calls` (observation list, generic `query.Parse` filters), `GET /api/v1/llm-calls/traces` and `/conversations` (legacy aggregates, still used by the `llm_trace_*` built-ins), `GET /api/v1/llm-calls/{id}` (full record, spill-rehydrated). The trace explorer uses `/api/v1/traces*` below.

### Trace explorer (Session → Trace → Observation)

Migration 89 turned the observation log into a Langfuse-style model. The
explorer is `_ui/src/pages/Traces.svelte` (routes `/traces`, `/traces/:id`,
`/traces/sessions/:session`; `/llm-calls` redirects) with components under
`_ui/src/lib/components/traces/` and pure logic in
`_ui/src/lib/helper/trace-view.ts` (tree, waterfall geometry, chat-message
extraction, filter ↔ URL mapping; `_ui/tests/trace-view.test.mjs`).

**Real timing.** `created_at` was second-precision text stamped when the work
*ended*, so every start was guessed from latency. Observations now carry
`started_at` / `ended_at` (`TIMESTAMPTZ`, backfilled from `created_at -
latency_ms`). Recorders pass the measured start (`llmAuditParams.startedAt`,
`GenerationObservationParams.Started`, `ToolObservationParams.Started`); a
missing start is still derived from latency, so older callers stay correct.
`metadata` became `JSONB` (read back as text, so a NULL scans as empty). Every
trace query is workspace-scoped and now has `(workspace_id, started_at)` /
`(workspace_id, trace_id, started_at)` indexes.

**Observation types** add `agent` (root span of one agent-loop run), `span`
(workflow run / node) and `embedding` to `generation` / `tool` / `event`.

**Root spans.** `agentloop.StartRun` mints the run span's ID *before* the run so
generations nest under it while it is still running; the row is written when
the run returns (`RunSpan.Finish`) with its measured duration, the request as
input and the final answer as output. Used by the Sessions turn, org delegation
(output read from the task's final result; blocked → `warning`) and workflow
`agent_call`. A run span with no parent also sets the trace's name/input/output.
Spans are written at the end, not start-then-update: one write per observation,
at the cost of a still-running trace showing its children without their root
(the tree promotes orphans to roots rather than dropping them).

**Trace context.** `service.TraceParent` (`internal/service/trace-context.go`)
rides the context. Every tool call in the three loops installs one with a
pre-minted tool observation ID, so work started *by* a tool joins the caller's
trace beneath that tool: a workflow run by `workflow_run`/`wf_*`, an
`agent_call` node, an embedding node. The recorder adopts the parent's trace,
session and parent ID when the observation names no trace (or the same one).
Org delegation children and `agent_run` subagents keep their own traces,
cross-linked through `metadata.child_trace_id` / `parent_trace_id` as before.

**Workflow spans are lazy.** `startWorkflowRunSpan` / `startNodeSpan`
(`internal/service/workflow/trace-spans.go`) record a run or node span only
when something was recorded beneath it (`TraceParent.MarkUsed` propagates up),
because cron and webhook workflows run constantly and most nodes do no
traceable work. Runs are named after the workflow through
`workflow.ContextWithWorkflowTrace`, set in `registerRun`, the workflow tool and
durable execution. Regression: `TestWorkflowTraceSpansAreLazy`.

**Trace attributes** live in `llm_traces` (`workspace_id, trace_id` key): name,
end user, tags, input/output preview. Aggregates (tokens, cost, duration,
counts, errors) are still computed from observations at read time so they
cannot drift. Writes ride an observation (`LLMCall.Trace`, upserted by
`RecordLLMCall`): first non-empty name/end-user/input win, last non-empty output
wins, tags accumulate (≤20, ≤64 chars). Input/output are prompt content: the
recorder keeps them only while `llm_audit` body capture is on, and
`ExpireLLMCallBodiesBefore` blanks them with bodies. Gateway root generations
derive input/output from the request/response (`traceIOFromBodies`: OpenAI
chat, Anthropic Messages, Responses).

**Gateway labels** (optional, client-asserted, `trace-labels.go`):
`x-at-trace-name`, `x-at-tags` (comma separated), `x-at-environment`,
`x-at-release`, `x-at-user`. They label and filter only. `x-at-user` (like the
OpenAI `user` field) is stored as the display-only **end user**; the
**user** column (`llm_calls.user_id`) is server-stamped from the execution
identity, browser principal or personal token owner (`observationUserID`) and is
never taken from a request.

**API** (`internal/server/traces.go`, `traces.read`, feature `llm_traces`):

| Route | Notes |
|---|---|
| `GET /api/v1/traces` | Typed filters: `from`/`to`, `q` (ID, session, name, input), `name`, `model`, `source`, `tag`, `environment`, `release`, `user_id`, `end_user`, `session_id`, `token_id`, `task_id`, `agent_id` (repeat or comma-separate), `status=ok|error`, `min_/max_latency_ms`, `min_/max_cost_cents`, `min_/max_tokens`, `score_name` + `min_/max_score`, `bookmarked=true`, `sort` (`started_at`, `duration`, `latency`, `cost`, `tokens`, `errors`, `observations`), `order`, `offset`, `limit` (≤500). Unknown parameters are 400, so a typo cannot silently widen a filter. Attribute filters select a trace when **any** observation matches; numeric bounds apply to the trace aggregate. |
| `GET /api/v1/traces/{id}` | Summary + all observations (bodies clipped; full payload via `/llm-calls/{id}`), bounded at 5000 with `truncated`, plus scores. |
| `GET /api/v1/traces/sessions[/{id}?token_id=]` | Sessions are keyed by session ID **and** API token, so two clients using `session-1` are never merged. Detail returns traces chronologically with full input/output for replay. Gateway session IDs come from `x-at-session-id`, `X-Session-Id`, `x-opencode-session`, `x-claude-code-session-id`, then string `metadata.session_id` / `metadata.conversation_id`; cache keys and the `user` field are not conversation identities, and requests without one stay separate traces. A request with a session but no `x-at-trace-id` gets a **turn trace** (`conversationTurnTraceID`, `trace-labels.go`): a hash of workspace + token + session + the number of user messages + the last user text. Tool results (`role: tool`, Anthropic `tool_result`, Responses `function_call_output`) are not user turns, so every model call of one agentic turn (OpenCode, Claude Code) shares one trace and the next prompt opens a new one. The session view offers a Conversation and a shared-axis Timeline view. |
| `GET /api/v1/traces/facets?from=` | Distinct values for filter pickers (≤200 each). |
| `POST /api/v1/traces/{id}/scores`, `DELETE /api/v1/traces/scores/{id}` | Manual annotations. Numeric, boolean (`bool_value` or 0/1) or categorical, optionally on one observation of the trace. Deleting another account's annotation, or an API score, needs workspace admin. |
| `PUT /api/v1/traces/{id}/bookmark` | Per-account bookmark. |
| `POST /gateway/v1/scores` | Scores from clients. The trace must contain an observation recorded with the **same** API token, so a token cannot annotate another client's traffic. |

Scores (`trace_scores`) and bookmarks (`trace_bookmarks`) are workspace records
(deleted with the workspace; bookmarks with the account) and are swept with the
trace's last observation by `DeleteLLMCallsBefore`. LLM-judge evaluators are not
implemented; scores are annotations or API values.

**UI.** The list is a dense table (configurable columns in localStorage,
sortable, chip filters with facet suggestions, time presets, live refresh every
10s while visible). All list state lives in the URL query, so filters survive
opening a trace and can be shared. The detail view is header → tree with a
shared-axis waterfall (or plain tree) → resizable inspector (Input/Output,
Metadata, Model & usage, Scores, Raw JSON). Generations render as chat messages
with tool-call cards paired to their results; unrecognized bodies fall back to a
collapsible, searchable JSON viewer. `?obs=` deep-links an observation (its
ancestors are expanded). Keyboard: ↑↓/j k select, ←→ collapse, Esc back, r
refresh. The Generations tab is the per-call view (it is also where Usage
drill-downs land: `provider` / `error_code`). TaskDetail Events links each row
to its trace.

**OTEL / trace export** use the real start/end. Per-workspace export maps
`agent` / `span` / `embedding` to Langfuse observation types and adds
`langfuse.environment` / `langfuse.release`; the global `emitLLMSpan` skips
structural rows (events, spans, agent runs), as it carries no hierarchy.

Regressions: `internal/store/postgres/traces_test.go`,
`internal/server/traces_test.go`, `TestChatSessionLoop_RecordsObservations`,
`TestAgentCall_RecordsObservations`, `TestWorkflowTraceSpansAreLazy`,
`_ui/tests/trace-view.test.mjs`.

## Stdio MCP processes & the MCP program library

### HTTP MCP upstream URLs are used verbatim

**Breaking change.** `NewHTTPMCPClient` used to run the configured URL through a
normalizer that appended `/mcp` to anything whose path did not already end in
it. A base URL was the historical storage form, so the rewrite was meant as
compatibility — but it also rewrote URLs a person had typed in full:
`https://host/mcp/api` was dialled as `/mcp/api/mcp` and `https://host/sse` as
`/sse/mcp`. A server whose endpoint path is anything other than `…/mcp` was
therefore unaddressable, and the only symptom was a 404 from a URL that appears
correct in the editor.

`mcpEndpointURL` (`internal/service/client.go`) now only trims whitespace. The
URL stored on an `MCPUpstream`, an MCP set's `urls` or an agent's legacy
`mcp_urls` is the URL that is dialled.

**Upgrade note**: an upstream stored as a bare base URL (`http://host:8787`,
no path) previously worked because `/mcp` was appended; it now hits the root
path and must be corrected to the full endpoint. AT's own gateway is unaffected
— both `POST /gateway/v1/mcp/{name}` and `…/{name}/mcp` are registered, so the
loopback URLs built in `org-delegation.go` and `chat-sessions.go` keep resolving.
Execution-policy resource IDs are unchanged: they were always keyed on the
configured URL, never the normalized one. Regression:
`TestMCPEndpointURLIsVerbatim`, `TestHTTPMCPClientDialsConfiguredPath`.

### Local MCP servers in Chats

Every other MCP path in the product dials from the server process, so
`localhost` in an upstream URL is the *server's* loopback and an MCP server
running on a person's own computer is unreachable. Chats is the one surface
where that is fixable without a new distribution artifact: its agentic loop
already runs in the browser (`executeToolCall` in `_ui/src/pages/Chat.svelte`),
and the browser sits on the user's machine.

A **local MCP server** is a per-account record (name, URL, optional headers)
stored in `user_preferences` under `local_mcp_servers` with `Secret: true`, so
the blob is encrypted by the existing path and no migration is needed. Managed
at `GET`/`PUT /api/v1/chats/local-mcp-servers` plus
`POST /api/v1/chats/local-mcp-servers/{id}/reveal`, all owner-scoped through
`playgroundAccess` and admitted on `models.use`. Feature key `chat_local_mcp`
(a sibling of `playground` under `chat_workbench`, because the catalog nests
two levels).

**The server stores these and never dials one.** The record type has no
conversion into an `MCPUpstream` and no MCP runtime reads the preference key.
That is the security position, not an implementation detail: the addresses are
loopback and private by construction, so a server-side dial would be a request
from AT into AT's own network chosen by any account — the SSRF
`internal/service/execution-mcp.go` already refuses on the scoped path and the
legacy Chats path does not check.

`service.ValidateLocalMCPURL` accepts only `http`/`https` to loopback,
`.localhost`, `.local`, RFC1918, CGNAT (100.64/10) and link-local hosts; a
public endpoint is refused and the message names MCP sets. This is not defence
against the person configuring it — they control their own browser — it is
what stops the feature becoming a workspace-policy bypass: a public MCP routed
through the page would run with no `CheckExecution`, no `mcp_tool` admission
and no server-side trace, while being exactly as usable as one in an MCP set.
The URL is dialled verbatim, like every other MCP URL.

**Another AT as a local MCP server.** The one non-local URL accepted is an
`https` AT gateway MCP endpoint (`[<base>]/gateway/v1/mcp/<name>[/mcp]`,
`service.IsGatewayMCPPath`), and only with an `Authorization: Bearer <token>`
(or `x-api-key`) header, enforced by `NormalizeLocalMCPServers` and mirrored
in the editor. This bypasses nothing: the remote installation still applies its
token admission, the MCP server's Run-as identity, workspace policy and its own
traces. On the remote side `gatewayMCPCORS` (`gateway-cors.go`) opens the
JSON-RPC POST routes cross-origin **only** for preflights announcing
`authorization`/`x-api-key` and requests carrying one; everything else
(including SSE/WebSocket routes and token-less requests) keeps the default
policy, so a website still cannot drive a public MCP server. Because a junk
header would otherwise satisfy CORS and then fall back to public admission,
`authorizeGatewayMCPServer` refuses that fallback for a cross-origin request
whose credential was rejected. Regression: `TestGatewayCORS`,
`TestGatewayMCPHandler_BrowserRejectedTokenNoPublicFallback`,
`TestNormalizeLocalMCPServers`.

Header values are redacted to `***` on ordinary reads, the sentinel preserves
the stored value on write, and real values come only from the per-record
reveal, called at the point the browser is about to dial — so the page does not
hold every credential for the whole session.

**Approval is per device** (`localStorage`, `at.local-mcp.approved`), not part
of the record. `http://127.0.0.1:3000/mcp` is a different program on a laptop
and on a desktop, so a synced approval would authorize, on the second machine,
a server inspected on the first. A record arriving on a new device lists as
*not enabled here*; the approval dialog connects first and shows the tool names
and descriptions, because a name and a URL are not enough to decide with. It is
**one approval per server, not per call** — the accepted cost is that a
prompt-injected page can make the model call a local tool with arguments the
user never sees, countered by visibility rather than interruption: the tool
list at approval time, local calls rendered in the transcript, an
always-present "local tools active" strip above the composer naming each
server, a one-click per-device disable, and tools added since approval labelled
as new.

Local tools are registered **last** in `refreshTools` and yield the name on
collision (`localMCPToolName` → `<server>__<tool>`): a program on somebody's
laptop must not be able to take over the name of a built-in or MCP-set tool the
conversation relies on. Results are bounded at 64 KiB in the browser, because
Chats is not governed by `loopgov` — that governs the three server-side loops —
and there is no workspace to spill the remainder into, so the notice asks for a
narrower result instead of naming a file.

Each call is reported to `POST /api/v1/chats/tool-observations` as a `tool`
observation with `metadata.origin: browser_local` and `client_asserted: true`.
The marking is load-bearing: every other observation is written by the process
that did the work, and a reader comparing a local tool's reported duration
against a provider's has to know which is which. Owner, workspace, source and
timestamps are stamped server-side; content is included only when the
`llm_audit` body-capture feature is on, which is stricter than server-executed
tools (whose previews are unconditional) because a local tool's input and
output never passed through this process. A failed report never fails the turn.
The Chats client now also sends a per-turn `x-at-trace-id`, which is what lets
a browser-run tool share the generation's trace — and incidentally makes Chats
turns group in Traces at all.

**Two browser constraints are inherent, not bugs.** The MCP server must send
`Access-Control-Allow-Origin` for AT's origin and
`Access-Control-Expose-Headers: Mcp-Session-Id` (without the latter the session
cannot be read, so a stateful server is used statelessly rather than failing as
a protocol error), and a page on a public origin reaching a private address
needs `Access-Control-Allow-Private-Network: true` on the preflight in current
Chrome. A browser reports a refused connection and a refused cross-origin
request identically, so the error names both causes and lists the required
headers rather than repeating "failed to fetch".

**stdio MCP servers are out of scope**: a browser cannot spawn a process. That
needs a local bridge, which is a separate change. Local tools are Chats-only —
Sessions, org delegation, workflow nodes and the gateway have no browser.

Regressions: `internal/service/types-local-mcp_test.go`,
`internal/server/chat-local-mcp_test.go`, `_ui/tests/local-mcp.test.mjs`.

### Local providers in Chats

A **local provider** (`service.LocalChatProvider`) is a per-account
OpenAI-compatible endpoint the browser calls directly from Chats: a model
server on the user's machine or a hosted API with their own key. Same position
as local MCP: the server stores it and **never sends a request to one**. Stored
in `user_preferences` under `local_chat_providers` (`Secret: true`, no
migration); managed at `GET`/`PUT /api/v1/chats/local-providers` plus
`POST .../{id}/reveal` (returns `api_key` + `headers`), owner-scoped through
`playgroundAccess`, admitted on `models.use`, feature key
`chat_local_providers` (sibling of `chat_local_mcp`). The API key and header
values are redacted to `***` on reads; the sentinel preserves on write. Plain
`http` is accepted only for local hosts (`LocalMCPHostAllowed`); public
endpoints must be `https`. Names are lowercase `[a-z0-9._-]{1,32}` because
they form the model reference `local:<name>/<model>`.

The browser lists `<base>/models` and adds the models to the picker under
`<name> · This device`; enabling is per device (`at.local-providers.enabled`
in `localStorage`). A turn on a `local:` model calls `<base>/chat/completions`
with `streamChatCompletion` and dispatches tools unchanged. Saved messages are
sent inline (the provider cannot resolve `at_message_id`); a partially loaded
conversation is refused rather than sent with a shortened context. Missing tool
call IDs are minted client-side. No reasoning effort is sent (capabilities are
unknown).

Each call is reported to `POST /api/v1/chats/generation-observations` as a
`generation` with `origin: browser_local`, `client_asserted: true` and
`noCostEstimate`, so installation pricing is never applied. Nothing is written
to `cost_events`: provider budgets, per-user allowances, API-token limits,
Usage and the loop governor do not apply. No resumable stream. Chats-only. The
user-facing explanation is the built-in guide `local-providers` (Documentation).
Regressions: `internal/server/chat-local-providers_test.go`,
`_ui/tests/local-providers.test.mjs`, the local cases in
`_ui/tests/chat-runtime.test.mjs`.

### Browser extensions in Chats

The Chat tools panel has a per-page **Web connection** switch (off on initial
load). Only while on does it create `ExtensionBridge`, discover extensions and
advertise this chat as an agent destination. On construction and in response to
`{channel:"at.extension.bridge", v:1, dir:"agent", event:"discover"}`, the bridge
posts `{channel, v, dir:"agent", event:"announce", name:"AT Chat"}`. Disposal
posts the corresponding `goodbye` and cancels pending calls. This lets extensions
list live chats before origin approval, including already-open chats after an
extension reload. Presence grants no tool access. The extension user selects a
chat and connects a source tab; AT's existing per-device tool approval still
applies. Async scans are generation-guarded so disabling or rescanning cannot
restore stale extension lists.

A local MCP server at least has an address. A **browser extension** has none:
it cannot be dialled by AT, and it cannot be dialled by this page either. The
only channel a page and an extension already share is `window.postMessage`
through the extension's content script — so that is what
`_ui/src/lib/helper/extension-bridge.ts` speaks, and why this is the one tool
source with no URL anywhere in it. What it buys is the set of things a server
categorically cannot reach: the person's other tabs, their session in them,
`chrome.tabs` / `downloads` / `debugger`.

**The discovery is generic, not a handshake with one vendor.** The page
broadcasts `describe` and every extension implementing the protocol answers
with its own `{id, name, version, capabilities, description, notice}`; tools
are then listed and called per id. A second extension needs no change in AT.
That generality is the whole reason a discovery step exists at all — a
hardcoded exchange with one extension would have been half the code.

The envelope is flat JSON on `channel: "at.extension.bridge"`, `v: 1`:

| Direction | Shape |
|---|---|
| page → extension | `{channel, v, dir:"request", id, extension?, method, params?}` |
| extension → page | `{channel, v, dir:"response", id, extension, result?, error?}` |
| extension → page | `{channel, v, dir:"event", extension, event}` |

Methods are `describe`, `tools/list` and `tools/call` (`{name, arguments}`),
with `tools` the only capability defined today. Events are `announce`,
`tools_changed` and `goodbye`. A request carrying no `extension` is a broadcast;
everything else is addressed. `tools/call` may answer with an MCP content block
array, `{isError:true, …}`, or a bare string — the protocol has to be cheap to
implement or nobody implements it — and `isError` becomes a tool error rather
than text that reads like success.

**Silence is a defined answer.** An extension is expected not to reply until
its user has connected it to this origin, so "no extension answered" covers
both "none installed" and "none connected here". The page says exactly that and
never claims to know which, because an unconditional reply would make this a
fingerprinting probe for every site the extension runs on. The same reasoning
is already visible in mcp-page-bridge, which marks `<html>` only on loopback
origins. `announce` is what makes the connect flow feel immediate: the person
connects the extension from its own popup and the open Chats tab updates
without a reload, which is why the bridge is constructed when the feature is on
rather than at the first scan.

**This is not a security boundary between extensions.** Any content script on
the page can post anything, including another extension's id. What actually
gates it is that the person installed the extension, that the extension answers
only origins its user connected it to, and the per-device approval — held in
`localStorage` under `at.extensions.approved`, deliberately a separate key
space from the local-MCP approvals so revoking one does not revoke the other.
As with local MCP the approval dialog lists the tools first, later additions
are labelled, and one click disables an extension mid-conversation.

Extension tools are registered **last**, after local MCP, and yield the name on
collision (`localMCPToolName`, reused): an extension must not be able to take
over the name of a built-in the conversation already relies on. Results are
clipped by `clipLocalToolResult` — Chats is not governed by `loopgov` and there
is no workspace to spill into — and every call is reported to
`POST /api/v1/chats/tool-observations` with the extension as `server`, so a
trace does not show the generations with an unexplained gap between them.
Descriptors and tool lists are clamped on arrival (8 extensions, 128 tools,
bounded names and descriptions): the strings are chosen by third-party code and
would otherwise be spent on the model's context.

AT stores **nothing** about extensions — no record, no endpoint, no credential;
the server is not involved in a call and never learns one happened beyond the
observation row. Feature key `chat_extensions` (a sibling of `chat_local_mcp`
under `chat_workbench`) gates the surface, and turning it off also disposes the
live channel rather than only hiding the section. Chats-only: Sessions, org
delegation, workflow nodes and the gateway have no browser.

Regression: `_ui/tests/extension-bridge.test.mjs`.

### Trace privacy

Settings → Trace privacy (`/settings/trace-privacy`) keeps chosen users, API
tokens, providers, models or sources out of traces. Migration 90 adds
`trace_privacy_rules` (NULL `workspace_id` = installation rule) and the
single-row `trace_privacy_settings`. A rule's set fields are ANDed; empty
fields match everything; `model` is a case-insensitive glob (`*`, `?`), and a
pattern with `/` matches `provider/model`. Actions: `skip` (default — nothing
is written, spilled, exported or emitted as an OTEL span) and `redact` (the
skeleton is kept; bodies, input/output, error text and trace input/output are
cleared and `metadata.redacted` is set). The strictest matching action wins.

Matching uses only server-stamped attributes (`user_id` from
`observationUserID`, the token ID, provider/model, source), never request
content. The decision runs in `recordLLMCallAsync`
(`internal/server/trace-privacy.go`) before anything touches disk, so all
recorders are covered. Rules are cached for 10s per replica and invalidated on
local writes. **The trace is the unit**: tool, agent and span rows carry no
model, so the first matching observation puts its trace in an in-memory
suppression map (30 min TTL, renewed on each matching observation, bounded at 20k), later
observations inherit the action, and rows already written are cleaned up
asynchronously (`ApplyTracePrivacyToTrace`) after a 2s delay. The map is per
replica; agent loops run on one replica, so this is sufficient in practice.
**Cost events, budgets and Usage are never affected** — suppressing traces is
not a way around spending limits.

Workspace rules need `workspace.write` in the selected workspace; installation
rules and settings need a platform administrator (enforced in the store;
foreign rules answer 404). `POST /api/v1/trace-privacy/rules/{id}/apply`
applies a rule to stored traces (`?dry_run=true` only counts): `skip` deletes
every matching trace with its observations, attributes, scores, bookmarks and
spill files in batches; `redact` clears the content of every observation of
matching traces. It is irreversible and logged with actor and counts.

Personal opt-out: with `allow_user_opt_out` on, Account security shows "Do not
record traces of my activity", stored as the non-secret preference
`trace_opt_out` (`GET/PUT /api/v1/trace-privacy/opt-out`, owner = signed-in
subject). It is ignored while the installation setting is off. Regressions:
`internal/service/types-trace-privacy_test.go`,
`internal/server/trace-privacy_test.go`,
`internal/store/postgres/trace-privacy_test.go`.

### Workspace trace export

Settings → Trace export (`/settings/trace-export`) configures a separate OTLP
destination per selected workspace. GET/PUT `/api/v1/trace-export` and POST
`/api/v1/trace-export/test` require `workspace.write` (workspace admin rank).
Migration 61 stores a versioned config blob in `trace_export_settings`; it uses
the installation encryption key when configured, participates in key rotation,
and is removed with its workspace. Reads redact header values and the Langfuse
secret to `***`; replaying that sentinel preserves the stored value, while
clearing a secret/removing a header removes it. Version CAS refuses stale saves.

HTTP uses the complete trace URL (collector `/v1/traces`, Langfuse
`/api/public/otel/v1/traces`); gRPC takes `http://host:4317` for plaintext or
`https://host:4317` for TLS. Langfuse is HTTP/protobuf-only, with Basic auth from
its keys and `x-langfuse-ingestion-version: 4` by default. Test connection sends
a real synthetic Export request using unsaved form values, including saved
masked credentials, and returns acceptance, latency and its OTEL trace ID.
HTTP redirects, HTML successes, partial rejections and RPC failures do not pass.
Acceptance proves ingestion at that endpoint, not downstream collector delivery.

`internal/service/traceexport` builds OTLP directly so global SDK/environment
exporter settings cannot redirect a workspace destination. Trace IDs are stable
hashes of workspace + token + AT trace ID; span/parent IDs hash observation IDs.
The recorder stamps workspace identity from gateway tokens or execution/browser
contexts into writes and delivery. Four workers share a bounded 256-observation
queue, collect batches of up to 32 and read current settings at delivery time
(cross-replica changes require no restart). Delivery has a 10s deadline and one
retry for transient failures; shutdown cancels delivery, and queue overflow or
final failures are logged. This is best-effort live export, not a durable outbox
or historical backfill. Local trace retention and bootstrap OTEL remain separate.
Prompt/response/tool/error content requires both `include_content` and the
installation `llm_audit` body-capture feature, with bounded inline content.
Regressions: `internal/service/traceexport/export_test.go`,
`internal/server/trace-export_test.go`, `internal/store/postgres/trace-export_test.go`.

Stdio MCP upstreams (`command`/`args`/`env` in `mcp_upstreams`) run in a
process-wide lazy pool (`service.StdioProcessManager`, keyed by resolved
command+args). Three lifecycle properties are load-bearing:

- **Crash reaping**: every `StdioMCPClient` owns a reaper goroutine calling
  `cmd.Wait()` the moment the child exits, so `Alive()` reflects a self-crashed
  process (previously it only flipped after an explicit `Close()`, and a dead
  client kept being handed out). `Close()` kills and waits on the reaper.
- **Env-change respawn**: the cache key excludes env, so `GetOrCreate` compares
  the requested (var-resolved) env against the running process's snapshot and
  replaces it on mismatch — an env edit or a rotated `{{var:...}}` secret takes
  effect on the next acquisition instead of surviving until server restart.
- **Explicit lifecycle**: `GET /api/v1/mcp/stdio-processes` (global list;
  args/env omitted because resolved args can carry secrets), and per record
  `GET .../mcp/{servers|sets}/{id}/stdio-status`, `POST .../stdio-restart`
  (optional `{"index": N}`), `POST .../stdio-stop`. Status/restart resolve
  `{{var:...}}` the same way tool execution does, so they address the process a
  tool call would reach; responses echo *stored* command/args, never resolved
  ones. Restart failures are per-upstream entries, not a 500. All are
  installation-admin (no `BusinessRoutePolicy`) and gated by `mcp_servers`.
  UI: the MCP page shows an `x/y running` chip + restart per set, and
  per-upstream status/restart/stop in the editor. Regression:
  `internal/service/stdio-manager_test.go`, `internal/server/mcp-processes_test.go`.

The **MCP program library** (`workflow.MCPDir()`, `internal/service/workflow/mcpdir.go`)
is `<server.workspace.root>/mcps` (or `./data/mcps` when root is unset) — the
durable home for binaries a stdio `command` references and config files an env
var points at. Like `assets` it is reserved in the workspace janitor and never
swept; `/api/v1/info` reports it as `mcp_root`. Managed from the MCP page's
**Binaries** tab via `GET/POST /api/v1/mcp/binaries` and
`DELETE /api/v1/mcp/binaries/{name}` (installation-admin; flat names only, no
separators or dot-prefixes). Uploads land atomically (temp + rename), default
0755 (`executable=false` → 0644 for config files). Archives
(`.tar.gz`/`.tgz`/`.tar`) are extracted server-side into
`<library>/<archive base name>/` unless `extract=false`: entry paths are
traversal-checked, symlinks/hardlinks/devices skipped, bounded at 10k files /
1 GiB decompressed, tar exec bits normalized to 0755/0644, and re-uploading the
same archive replaces the directory (the upgrade path). Delete removes a file
or a whole extracted directory. AT still installs nothing itself — `npx`/`uvx`
caches remain host concerns. Regression: `internal/server/mcp-binaries_test.go`.

## Connections & Connectors

External-service credentials are modeled in two layers:

- **Connectors** (`internal/service/types-connector.go`, `internal/server/connectors-registry.go`) — data-driven definitions of a connection *type* (the provider catalog). A connector carries an `auth_kind` (`oauth2` | `token` | `custom`), an optional OAuth2 block (`auth_url`, `token_url`, `scopes`, `use_pkce`, `userinfo_url`, `account_label_path`, …), and a `fields[]` credential schema that drives the UI form. Connectors hold **no secrets**, so the `connectors` table is unencrypted. The catalog is the merge of built-in JSON definitions (`internal/server/connectors/*.json`, embedded) and user-defined / override rows in the `connectors` table — **a DB row overrides a built-in by slug**. CRUD: `/api/v1/connectors` (+ inline "Manage providers" UI on the Connections page). This replaced the formerly hardcoded `google`/`youtube` OAuth map — new providers (GitHub, Spotify, …) are added by shipping a JSON file or creating one in the UI, no code change.
- **Connections** (`internal/service/types-connection.go`, `internal/server/connections.go`) — named, AES-256-GCM-encrypted credential *instances* bound to a connector by its slug (`Connection.Provider == Connector.Slug`). Multiple accounts per provider; executable integrations reference them independently from documentation skills. The connection create/update API accepts a dynamic `fields` map (keyed by full var name, e.g. `spotify_client_id`); `connectorCredentialsFromValues` folds well-known suffixes (`_client_id` / `_client_secret` / `_refresh_token` / `_api_key`) onto the struct and the rest into `Extra`.

The OAuth2 flow (`internal/server/oauth.go`) is fully connector-driven and **supports PKCE** (verifier cached on `Server.oauthPKCE`, keyed by state for the callback flow or `provider+connection` for the manual paste-code flow). Token exchange sends `Accept: application/json` (so GitHub-style endpoints return JSON), omits `client_secret` for PKCE public clients, and no longer hard-requires a refresh token — when a provider returns only an access token it is stored under `<slug>_access_token`. Account labels are fetched generically via the connector's `userinfo_url` + `account_label_path` (a dot-path supporting array indices, e.g. `items.0.snippet.title`).

Built-in skill templates may declare a connector, which is upserted into the
registry on install. This is setup metadata only: loading the skill does not
grant credential access or create an executable handler.

## MCP OAuth (per-account MCP credentials)

HTTP MCP upstreams (GitHub, GitLab, any spec-compliant server) can authenticate
with OAuth 2.1 per the MCP authorization spec, so different people and agents
use different external accounts with the same MCP set. An upstream opts in with
`auth` (`service.MCPUpstreamAuth`; MCP set editor → upstream → *Authentication*).

**Personal connections.** `connections.owner_user_id` (migration 97) makes a
connection personal; `scope` (`personal` | `workspace`) is derived and returned.
The owner is stamped from the authenticated principal on create and is
immutable. A personal connection is visible and usable only by its owner —
platform administrators may list (and delete) it but never read its
credentials or use it at runtime (`connectionOwnershipPredicate` for reads,
`connectionUsePredicate` for runtime/`ResolveConnectionForUse`, applied before
pagination). Members create/update/delete their own on `connections.use`;
workspace connections still need `connections.write` + `credentials.manage`.
Connectors, variable import and the legacy `/oauth/*` flow stay installation
administration. A workspace agent may bind only workspace connections; a
personal agent also its owner's personal ones. Account deletion sweeps
personal connections and pending ceremonies. Runtime revalidation of
`connections.use` now checks `connections.use` (it checked `.read`), and
`connectionLookupFunc` resolves through `ResolveConnectionForUse` when a run is
bound (the redacting DTO broke bound skills for members).

**Account selection.** `auth.accounts` is an ordered fallback list over `user`
(the run's account; for a *personal* gateway API token, the token owner,
resolved live and used only to read the owner's own connection), `agent` (the
running agent's binding for `auth.provider`) and `shared`
(`auth.shared_connection_id`, which must be a workspace connection — enforced in
the store on every set/server write). Empty means `["user"]`. A source that is
not listed is never consulted. A candidate must also have been authorized for
the same canonical MCP URL (`service.SameMCPResource`): a provider key is a
label any set writer chooses, a token is audience-bound. No usable account is a
tool error naming the provider (`ErrMCPAccountMissing`), never a fallback to
static headers; `auth` with a static `Authorization` header is refused.

**Flow.** `POST /api/v1/mcp/oauth/start` (`mcp.use`; a shared target also needs
`connections.write` + `credentials.manage`) reads the upstream through the
execution plane, runs `mcpauth.Discover`, reuses `auth.client_id` or the
connection's registered client, else dynamic registration, and stores the PKCE
verifier, client and pinned endpoints in `mcp_oauth_pending` keyed by
`sha256(state)`. `GET /api/v1/mcp/oauth/callback` is a cross-site top-level
navigation (allowed in `nativeauth.SameOrigin`); its state is
`<workspace>.<nonce>`, and the workspace prefix only selects which workspace
admits the request — the pending row is bound to that workspace, account and
session and consumed once. The redirect URI must be byte-identical to the one
stored. The popup posts `{type: at-mcp-oauth-result}` to the exact configured
origin; no tokens or provider error descriptions are shown. Renewal
(`connection_id`) keeps the row and every agent binding.

**Tokens.** Stored in `ConnectionCredentials.MCPOAuth` (encrypted with the
credential blob; API responses expose only status). `WithMCPOAuthTokens` holds
`SELECT … FOR UPDATE` across reload → refresh → save, adopts a token another
replica already rotated, and pins token endpoint/client/resource/MCP URL.
`invalid_grant` sets `needs_reauth` and fails with a reconnect error. The token
source (`mcpOAuthTokenSource`) closes over the connection ID and re-admits on
every request. All HTTP upstream constructions go through
`mcpUpstreamClientOptions`; an OAuth upstream always uses the scoped execution
client when a run is bound. Sessions and workflow `agent_call` now install agent
connection bindings *before* connecting MCP. Tool observations get an
`mcp_oauth_account` event (provider, source, connection ID — never tokens).
Stdio upstreams never take OAuth.

**Connecting from Connections.** A member who cannot edit a set gets a
redacted upstream list, so the set editor's *Connect my account* is only
reachable for writers. `GET /api/v1/mcp/oauth/accounts` (`mcp.use`,
`mcp-oauth-accounts.go`) lists the OAuth upstreams of every set the caller may
use (resolved through `ResolveMCPSetForUse` after the `mcp.use` execution
check), with the caller's own matching personal connection (same provider and
`SameMCPResource`). It returns only the server origin, provider and account
sources — no path, headers or tokens. The Connections page shows these as
*MCP servers* with Connect/Reconnect/Disconnect, which start the existing
`/mcp/oauth/start` flow by `set_id` + `upstream_index`. That page also hides
connectors without accounts behind *Show all providers*, so built-in
connector definitions (Google, Spotify, …) no longer look like installed
integrations.

Regressions: `internal/store/postgres/mcp-oauth-connections_test.go`
(isolation, owner immutability, deletion sweep, concurrent refresh),
`internal/server/mcp-oauth_test.go` (fake MCP + AS: start → callback → call →
401 → refresh → invalid_grant → reconnect), `mcp-oauth-resolver_test.go`
(fallback order, unlisted shared, gateway personal vs workspace token,
validation), `_ui/tests/mcp-oauth.test.mjs`.

## MCP upstream proxy and TLS settings

HTTP MCP upstreams have an optional `proxy` URL (MCP Sets → External MCP →
Proxy URL) and opt-in `insecure_skip_verify` (off by default), used for MCP initialization/list/call/shutdown and OAuth discovery,
dynamic registration, code exchange and refresh. HTTP/HTTPS/SOCKS5/SOCKS5H
forward proxies are supported; embedded credentials, paths, queries and
fragments are refused because MCP configuration is readable by its writers.
Blank preserves existing transport behavior. OAuth never uses ambient proxy
environment variables: `mcpauth.ClientWithTransport` accepts only this explicit
setting, retains endpoint and redirect validation and trusts the configured
proxy for destination DNS/routing. Network overrides require trusted-host execution
when bound; OAuth start enforces it too. The browser's authorization navigation
still uses the device network. The pending ceremony pins the proxy through
callback, the connection retains it for reconnect, and runtime refresh uses
the current upstream network settings. TLS opt-out applies to HTTPS proxies too,
never the browser, and the editor warns of credential interception. No migration (existing JSON config/credentials).
Regressions: `mcpauth/proxy_test.go`, `mcp-proxy-config_test.go`, the proxy
variant of `TestMCPOAuthEndToEnd`, `_ui/tests/mcp-oauth.test.mjs`.

## AT as an MCP authorization server

Migration 99. A gateway MCP server can let MCP clients (Claude Code, Cursor,
ChatGPT, the MCP SDKs) **sign in with an AT account** instead of carrying an
API token: `config.oauth` (`service.MCPServerOAuth`, MCP Servers editor →
*Sign-in*) with `enabled`, `dynamic_clients`, `redirect_patterns` and
`chain_upstreams`. It lives in the config JSON; API tokens keep working.

**Flow** (`internal/server/mcp-auth-server.go`). A token-less request to an
OAuth-enabled server answers 401 with `WWW-Authenticate: Bearer
resource_metadata=".../.well-known/oauth-protected-resource<base>/gateway/v1/mcp/<name>"`.
RFC 9728/8414 metadata sit at the origin root with the deployment path
appended (where clients derive them from the resource and the issuer =
`publicBaseURL`). `POST <base>/oauth/mcp/register` is RFC 7591 (rate-limited
per client address; the store caps dynamic clients at 5000 and prunes unused
ones first). `GET <base>/oauth/mcp/authorize` validates client and redirect
first (never redirecting on those errors), then everything else (redirected
with `error`, `state`, `iss`), and hands the browser to the SPA consent page
`#/oauth/mcp/authorize` (`McpAuthorize.svelte`, rendered outside the shell
like mobile approval) with `at_workspace` = the server's workspace. The page
calls `GET`/`POST /api/v1/mcp-auth/authorize?at_workspace=` — a cookie-
authenticated, `mcp.use`-admitted API; `at_workspace` replaces the tab's
`X-AT-Workspace-ID` for those two calls only and membership is still
resolved. `POST <base>/oauth/mcp/token` does `authorization_code` (PKCE S256
required, code single-use — a failed exchange still burns it) and rotating
`refresh_token`; `POST <base>/oauth/mcp/revoke` is RFC 7009. Codes live 10
minutes, access tokens (`atm_…`) an hour, refresh tokens (`atr_…`) 30 days;
all secrets are stored as SHA-256 only. These endpoints strip cookies and
share the gateway's token-only CORS policy.

**Identity.** An `atm_` bearer on `/gateway/v1/mcp/{name}` is resolved in
`admitMCPAuthAccess` before API-token auth: the token is audience-bound (same
server ID, workspace and resource URL, and sign-in still enabled), and the run
is bound as the **grant's account** (`bindMCPAuthGrant`,
`ExecutionProvenance.GrantID`, source `mcp-oauth`) under its live membership
and the workspace policy — the server's *Run as* binding is not used.
Revalidation re-reads the grant (5s cache, cleared on revoke) and membership,
so revoking the grant or disabling the account stops access at the next
action. Upstreams with the *Signed-in user* source then use that account's own
connections — the "everyone reaches GitLab as themselves" case.

**Clients.** Dynamic clients are installation-wide (`workspace_id` NULL) and
admitted only by servers with `dynamic_clients`, subject to
`redirect_patterns` (exact URIs or `*` prefixes; empty = any https or loopback
redirect). Pre-registered clients (`/api/v1/mcp/servers/{id}/oauth-clients`,
`mcp.write`) are bound to one server; a confidential one's secret is shown
once. Loopback redirects match on any port (RFC 8252).

**Chained upstream accounts.** With `chain_upstreams`, approval returns
`pending_accounts` — the server's MCP sets' OAuth upstreams that list the
*Signed-in user* source and that the account has not connected — and the
consent page offers *Connect* for each (existing `/mcp/oauth/start` popup)
before returning to the client.

The account's sign-ins are listed on Connections → *Apps with MCP access*
(`GET`/`DELETE /api/v1/mcp-auth/grants`). Grants are workspace records and are
deleted with the account. Regressions: `internal/server/mcp-auth-server_test.go`
(AT's own MCP OAuth client as the consumer: challenge, discovery,
registration, foreign redirect, deny, PKCE, code replay, account identity,
audience binding, refresh rotation, revocation, disabled account, dynamic
clients off; pre-registered confidential client; chaining),
`internal/service/types-mcp-auth-server_test.go`, `_ui/tests/mcp-auth.test.mjs`.

## Persistent Assets & Avatar Studio

Reusable media (character portraits, cloned-voice manifests, series state and rendered episodes) live in a **persistent asset library** (`workflow.AssetsDir()`, `internal/service/workflow/assets.go`). A nonblank explicit `server.workspace.root` selects absolute `<root>/assets`; unset or blank preserves shipped `./data/assets`, resolved against the process working directory. Unlike per-task workspaces it is NOT swept by the workspace janitor: `assets` is reserved before task lookups, and symlink entries are skipped. Approved workflow/shell handlers receive it as `AT_ASSETS_DIR` (alongside `AT_WORK_DIR`); `GET /api/v1/info` reports it as `assets_root`; `POST /api/v1/files/upload` (multipart `file` + optional `path`/`name`, 256 MB cap) writes into it (default target when `path` omitted). `EnsureAssetsDir` creates the conventional roots: `avatars/`, `voices/`, `uploads/`, and `series/`.

`Server.New` calls the thread-safe `ConfigureAssetsDir` before starting janitors/bots. `EnsureAssetsDirReady` checks creation and actual writability with temporary probe files; startup errors include the path and `server.workspace.root` guidance but leave the gateway available. No temporary fallback or automatic migration is performed. Configure `server: {workspace: {root: /mnt/at-workspace}}` and mount that root on a persistent volume writable by the service user; `/tmp` does not guarantee reboot persistence. Existing installations leaving root unset must keep their `./data` mount. Moving an old `data/assets` library is manual: back it up, stop media producers, and review manifests/records for absolute path references before resuming; setting root never moves, copies, or deletes old media.

The HeyGen-style avatar pipeline is built from three pieces:

- **Skill templates**: `fal-avatar` and `elevenlabs-voice` provide documentation and connector setup metadata. Their legacy handler definitions are preserved as inert Markdown references; production execution must use separately configured MCP or workflow tools. Avatar manifests are backward-compatible character bibles: v2 optionally adds `turnarounds`, ordered `reference_images`, bound `voice`, `sora_character_id`, `lora_url`, `style_notes`, `wardrobe`, and `persona`.
- **Integration pack** `avatar-studio` (`internal/server/integration_packs/avatar-studio/`): org "Avatar Studio" with Studio Director (head) → Avatar Designer + Video Producer. Agents ship with empty provider/model (assigned at install by the Studio UI setup, or manually).
- **Studio UI** (`_ui/src/pages/Studio.svelte`, route `/studio`, sidebar "Studio"): one-click setup installs both studio packs + all media skills and patches agent providers. Tabs: Characters (portrait gallery, v2 bible editor, turnaround/Sora actions, quick talking-head video), Series (style/cast editor, episodes, live-polled shot storyboard with still/clip/provenance), Productions (structured `episode.json.final_video` playback plus regex fallback for legacy one-offs).

## Series Studio

Episodic production is filesystem-backed so production workflows and the UI share one durable contract without new REST endpoints:

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

## Non-host built-in bookkeeping tools

`builtin-tools-registry.go` owns the validated runtime registry: each entry joins
its wire definition with an executor and an explicit execution class (non-host,
host, or host plus platform host-file admission). API/MCP/workflow discovery,
known-name checks, dispatch and service capability registration derive from that
registry. Schema payloads remain in `builtinToolSchemas` for readability; missing
schemas/executors, duplicates and unset classes invalidate the whole registry
instead of guessing authority or silently returning an empty successful result.
Registry initialization logs failures and disables built-ins. Core service-only
class fallbacks remain, but attached runtime registrations take precedence.
Regression: `internal/server/builtin-tools-registry_test.go`.

`current_time`, `whoami`, `todo_read`, `todo_write`, `get_user_preferences`,
`set_user_preference`, `guide_list` and `guide_get` support Restricted execution
without `execution.host`. Explicit workspace tool permission, feature switches,
live execution identity and existing resource admission still apply.

`current_time` reads the server clock once and returns `datetime`, `utc`
(RFC3339Nano), `timezone`, `unix_seconds` and `utc_offset_seconds`. Optional
`timezone` accepts an IANA name, default UTC; saved preferences are not applied
implicitly. `Local` is refused to avoid depending on host configuration. Embedded
`time/tzdata` keeps zones available in minimal images. Regression:
`internal/server/builtin-tools-current-time_test.go`.

Todo keys include the bound workspace/account and session; sessionless calls use
the run ID, never a shared empty/default key. Chats sends its active turn's
conversation/scratch session ID on server built-in calls via `X-Session-ID`.
Chats exposes todos only as browser Chat tools (the visible todo panel); server
todo entries are hidden there but remain available to other execution surfaces.
Legacy server todo selections in conversations/defaults/presets migrate to Chat
tools via `normalizeChatToolSelections`, including preset equality checks.
Regressions: `_ui/tests/chat-tool-selections.test.mjs`,
`internal/server/builtin-tools-todo-session_test.go`.
Preference tools use the bound run-as account
(not tool arguments or chat-channel metadata). They expose only non-secret
`timezone`, `location` and `language`; application settings and arbitrary keys
are excluded. Sessions automatic prompt injection uses the same filter in
`personal-preferences.go`, so application settings cannot hitchhike into model
requests. Regression: `internal/server/personal-preferences_test.go`, including
a real agent-loop provider request. Public writes atomically refuse to overwrite secret preferences
through `PublicUserPreferenceStorer`. Existing other preference rows are preserved
but no longer exposed or writable through these tools. Guide reads retain the
store's resource scope; guide mutations and unreviewed management tools stay host-only.
Regressions: `builtin-tools-non-host_test.go`, `builtin-tools-whoami_test.go`,
`internal/store/postgres/user-preferences_test.go`.

## Image generation from any model

ChatGPT's web app generates images by having the chat model call a hosted
image tool; a bare model reached through the API does not. AT exposes the same
capability as the built-in `generate_image` tool (non-host, "Other" family), so
an agent on any provider — Claude included — can create images through a
provider that implements `service.ImageProvider`:

- `openai` with an API key → `/v1/images/generations` (`gpt-image-*`, `dall-e-*`;
  `response_format` is sent only to DALL-E, which GPT Image models reject).
- `openai` with `auth_type: chatgpt` → `CodexProvider.GenerateImage`, which posts
  to `<codex root>/images/generations` — the endpoint the Codex CLI's
  `image_gen.imagegen` tool uses — with model `gpt-image-2` by default. Usage is
  billed to the ChatGPT subscription and unavailable on Free plans; this is an
  unofficial endpoint and may change. The same method serves
  `/gateway/v1/images/generations` for Codex providers.
- `minimax`.

Image bytes never enter the conversation. With a run work directory (Chats
skill runs, org delegation, Telegram tasks) files are written there and
delivered by the existing artifact collector; otherwise (Chats calling the tool
directly) they are stored as media owned by the caller and returned as
`artifacts`, which the Chats page attaches to the answer. Only PNG/JPEG/GIF/WebP
(sniffed from the bytes) are accepted, and URL results must be HTTPS.
Anthropic has no image-generation model; Claude uses this tool with another
provider. Regressions: `internal/server/builtin-tools-image_test.go`,
`internal/service/llm/openai/codex-images_test.go`.

**Over the gateway MCP endpoint** (OpenCode, Claude Code, any MCP client) the
tool is useful outside AT too. `gwGenMCPCallTool` installs a
`service.ToolContentCollector` and the calling API token rides the context
(`contextWithGatewayToken`). `generate_image` then (1) attaches a **preview**
of each image as MCP `image` content (`imagePreview`, stdlib only: longest side
768 px, JPEG unless the image has transparency, ≤ 8 MiB total), so the calling
model sees what it made without a ~1 MB full-resolution base64 PNG riding in
its context on every later turn, and (2) records `storage_objects.token_id`
on the stored media and returns a `download_url` for
`GET /gateway/v1/media/{id}?key=…`. The key exists because the agent's shell
never sees the API token configured in its MCP client, so the earlier
Authorization-only link was unusable to exactly the agent that asked for the
image. It is 256 random bits per object, only its SHA-256 is stored
(migration 93: `download_key_hash`, `download_expires_at`), and it expires
after 24h; the producing token may still download with `Authorization`.
Responses carry `Referrer-Policy: no-referrer`. The collector is generic:
upstream MCP servers' `image`/`audio` blocks are forwarded through it as well,
and both MCP clients now join all text blocks instead of returning only the
first one. Agent loops have no collector and keep their text-only behaviour.

**Long tool calls over the gateway MCP endpoint.** AT sets no deadline on a
gateway `tools/call`; the request context (client disconnect) is the only
bound. Because a response that writes nothing for minutes is cut by reverse
proxies (nginx 60s, Cloudflare 100s) and by clients' request timeouts,
`serveMCPCall` answers plain JSON when the call finishes within 10s and
otherwise, if the client accepts `text/event-stream`, switches to an SSE
response: a `: keep-alive` comment every 10s, `notifications/progress` when the
request carried `_meta.progressToken` (clients that reset their timeout on
progress then wait indefinitely), and the JSON-RPC result as the final event.
JSON-only clients still get JSON. Gateway HTTP tools no longer carry a 60s
client timeout. Agent loops keep their per-agent `tool_timeout`. Regression:
`TestServeMCPCallKeepsSlowCallsAlive`.

**Using the result** (modelled on fal's MCP, where the result is a durable
URL and display is the client's job). Each gateway artifact also carries
`width`/`height` (header-only decode; WebP parsed by hand to stay stdlib-only),
`expires_at` and a ready `markdown` image, and images are now served
`inline` (still `CSP: sandbox` + `nosniff`) so `![](download_url)` renders in
Claude Desktop, Cursor and similar clients. `expires_in_seconds` sets the key
lifetime per call (clamped to 1 minute .. 7 days, default 24h;
`contextWithGatewayMediaTTL`). Clients that negotiated MCP `2025-06-18`
(the `MCP-Protocol-Version` header) also get one `resource_link` per image;
older clients do not, since an unknown content type can fail their result.
The tool description (`generateImageUsage`) tells the model all of this,
including that an external user does not see the image until it shows the
markdown or saves the file. Work-directory runs keep `files` as paths and add
`images` with dimensions.

**Editing.** `reference_images` (≤5) turns the call into an edit:
`ImageGenerateRequest.ReferenceImages` → Codex `POST <codex root>/images/edits`
(JSON, data: URLs — the Codex CLI's contract) or the API's multipart
`/images/edits` (`image[]` for several). MiniMax returns
`ErrUnsupportedOperation`. References resolve in `image-references.go`: a
media_id visible to the run's account or produced by the calling token, this
installation's own `download_url` (resolved locally with the key check, no
network round trip), a `data:image/…;base64` URL, or a public `https` URL
fetched through a dialer that refuses private, loopback, link-local and CGNAT
addresses at connect time (so redirects and DNS rebinding are covered).
Bounded at 32 MiB per image and 64 MiB total. Regressions:
`internal/server/image-references_test.go`,
`internal/service/llm/openai/image-edit_test.go`.

**Placing media in Chats answers.** Calls from the Chats page (built-in and
MCP-set tool calls send `media_refs: true`; skill runs always do) give each
artifact a `markdown` snippet such as `![name](media:<media_id>)`
(`service.MediaRefMarkdown`, `contextWithChatMediaRefs`). The model copies it
where the image belongs; `MessageContent.svelte` resolves `media:` targets to
the workspace-scoped `api/v1/media/{id}` URL at render time
(`helper/media-ref.ts`) and does not render the same stored part again below
the text. Unplaced artifacts are still attached after the answer. Raw view is
text only: every non-text part is printed as a one-line descriptor
(`[image · name · media:<id> · size]`, inline data URLs never printed) instead
of being rendered. The stored part keeps the media in history, shares and
copies; share/import rewrite `media:` references in text along with
`media_id` (`service.ReplaceMediaRefs`). Shares resolve only media in their
snapshot, otherwise the label is kept. Skill runs write into a server
directory (`<execution root>/chat-runs/<ulid>`); the run is told to name files
only, and `chatArtifactCollection.rewriteText` rewrites any delivered file's
server path left in its answer (foreground result and background status) — a
Markdown target becomes its `media:` reference, a bare mention its file name.
Background summaries in the browser list each file's `markdown`. Before this, the tool description told
every model to "put its markdown in your answer" while Chats results carried
no URL, so models invented broken image links. Regressions:
`internal/service/media-ref_test.go`, `TestChatSkillRunDeliversProducedFiles`,
`_ui/tests/media-ref.test.mjs`, `_ui/tests/chat-message-content.test.mjs`.

**Choosing the provider.** `provider` is optional. When omitted, the tool uses
the only image-capable provider (`openai`/`minimax` type) in the caller's
workspace catalog; with several it fails with an error listing the valid keys
rather than leaving the model to guess one. The default model follows the
provider type (`gpt-image-2`, or `image-01` for MiniMax). A gateway MCP server
or MCP set can pin it: `config.image_generation` (`service.ImageGenerationConfig`,
the *Image generation* panel in both the MCP Sets and MCP Servers editors —
`ImageGenerationSettings.svelte` + `helper/image-generation.ts` — shown once
`generate_image` is enabled) with `provider`, `model` and default
`size`/`quality`/`background`. A set's setting applies wherever the set is
used (agents, Chats, a gateway MCP server listing it); a gateway server's own
setting overrides it for tools that server exposes.

The MCP Servers editor is set-first: the **MCP sets** panel (with a per-set
summary and an edit link to `#/mcps?edit=<id>`, which `Mcps.svelte` opens once
and drops from the URL) is where a server's tools come from. Tools configured
directly on a server (builtin tools, image generation, workflows) and the
WebSocket passthrough sit under a collapsed **Advanced** section, which opens
automatically when an existing server already uses any of them. Nothing changed
in storage: `servers` (set names) and `config` are the same fields.
Pinned provider/model are removed from the advertised schema
(`generateImageToolForConfig`) and enforced on every call
(`applyImageGenerationConfig`); the defaults fill only empty arguments. It
lives in the config JSON, so no migration. Regressions:
`internal/server/gateway-media-download_test.go`,
`internal/server/builtin-tools-image_test.go`,
`internal/service/tool-content_test.go`, `TestGatewayMediaObjectTokenScoping`,
`TestGatewayMediaObjectDownloadKey`.

## Built-in "get" tools take one identifier or a list

Every record-fetching built-in (`agent_get`, `task_get`, `org_get`,
`workflow_get`, `trigger_get`, `skill_get`, `mcp_server_get`, `mcp_set_get`,
`provider_get`, `bot_get`, `variable_get`, `connection_get`, `node_config_get`,
`guide_get`, `llm_observation_get`) goes through `multiGet`
(`internal/server/builtin-tools-multiget.go`).

The failure it fixes: the argument was read as `args["id"].(string)`, so a model
asking for several records in one call sent an array into a single-string read,
got the zero value, and was told **"id is required"** — an argument that was
present and understood by the person reading the call. The same happened for a
comma-separated list and for the plural `ids` key, both of which models produce
unprompted. `multiGetIDs` accepts all of those, trims, drops blanks and
de-duplicates with first-seen order preserved. Splitting is on commas and
newlines only, **never on interior spaces**: several of these arguments accept a
name where an identifier is expected, and names contain spaces.

`multiIDSchema` declares the array form as `anyOf: [string, array<string>]`,
which is what makes the multi-record call discoverable rather than merely
tolerated. That branch has to survive the restrictive Gemini schema subset —
`collapseGeminiAnyOf` keeps a genuine two-branch union — so the tool never
advertises a shape the adapter has stripped.

Response shape is deliberately asymmetric:

- **one identifier** → the fetcher's own JSON and its own error, byte-for-byte
  what the tool returned before. Nothing that works today changes meaning.
- **several** → `{requested, found, failed, results:[{<key>, ok, data|error}]}`
  in the requested order, with each record embedded as JSON rather than a
  re-encoded string. A missing or failing entry is reported **per item**: one
  unknown ID in a list of five must not discard the four that resolved, and a
  single error string would not say which one was at fault.

Bounded at `multiGetLimit` (25, the ceiling `batch_execute` already uses) and
cancellation-checked between entries. Regression:
`internal/server/builtin-tools-multiget_test.go`.

## Memory

AT does not ship a native long-term agent memory store. Agents that need memory should use an external memory MCP (for example a custom Postgres/vector/Engram/Mem0/Letta MCP) attached through MCP Sets or MCP server URLs. Keep memory read/write policy, retention, and embedding/search strategy inside that MCP; AT only discovers and calls the tools.

## Developer Spaces

A Developer Space is **one persistent coding environment per account and
workspace** (`/api/v1/developer-space*`, UI `#/developer-spaces`). There is no
space ID in any path: every handler resolves the caller's own space through
`EnsureDeveloperSpace`, which creates it with the default profiles on first use
(unique `(workspace_id, owner_user_id)` index, so concurrent first visits
converge). Migration 83 replaced the old space → repository → worktree model:
each account keeps its oldest space, the repository/worktree tables are dropped,
and sessions store `project_path` instead. **Upgrade note**: the dropped rows
and any extra spaces' sessions are gone; files already in the kept volume stay.

Feature key `developer_spaces` is a child of `agents`. Disabling it hides the
page and gates every `/api/v1/developer-space*` and `/api/v1/developer-sessions*`
route without deleting spaces, sessions or volumes; re-enabling restores them.

**Projects are folders**, not records. `/workspace` is a Docker-managed volume;
each top-level folder is a project (created empty or by `POST .../clone`). The
page is laid out like an editor: sessions and a lazily-loaded file tree on the
left, tabs in the middle (chat sessions, CodeMirror files with Ctrl+S and
version-checked saves, diffs), source control on the right, and one or more
terminals at the bottom. Layout and the open project persist in localStorage.

**Every path goes through one containment helper.** `internal/devfs` runs
inside the container for the browser file API and the agent's file tools alike:
paths are resolved (following symlinks) and
must remain under the project root, delete removes a link rather than its
target, and content travels on stdin (`ExecArgsInput`) — base64 in argv was
silently capped by the kernel's 128 KiB per-argument limit. Writes carry the
version returned by the read, so a save over a file the agent or terminal
changed answers 409 and the page offers reload or overwrite. Server-side,
`service.CleanDeveloperPath` refuses absolute paths, backslashes and `..`
before anything reaches the container.

The helper is a static stdlib-only binary (`cmd/at-devfs`, ~3 MB) built for
linux/amd64 and linux/arm64 by `make build-devfs` (run by `make build`,
`make run` and the goreleaser `before` hook) and embedded through
`internal/devfs/devfsbin`. On first file operation per container the Manager
copies it to `/usr/local/bin/at-devfs` (`Manager.EnsureFile` over the optional
`container.FileInstaller` driver interface; Docker uses `docker cp` of a tar
stream, so the image needs no shell or tar) after matching the image platform.
It lives outside `/workspace`, so it is neither user data nor quota. A binary
built without helpers (`go build`/`go test` alone), an unmatched platform or a
failed copy falls back to `devfs.PythonScript` through the image's python3.
Reading files from the volume's host mountpoint was rejected: it breaks with
remote/rootless daemons and would follow user-created symlinks on the host.
`TestPythonParity` runs both implementations on one tree and requires
identical JSON; `TestHelperInStockImage` runs the helper in python-less debian.

**The terminal attaches to the user's own container** (`GET
.../developer-space/terminal?cwd=`), one shell per WebSocket; closing a tab
never stops the container. WebSockets cannot send `X-AT-Workspace-ID`, so this
single endpoint accepts the `workspace_id` query selector
(`nativeWebSocketPath`), with the same admission as a header.

**Chat streams.** `run`/`confirm`/`answer` answer with SSE: `status`, `delta`
(text/reasoning), `message` (each stored transcript row), `tool_start`,
`tool_result` (with `changed` paths so open editors reload), `pending`, and
exactly one terminal `done` or `error`. Streaming reaches the provider through
`executionProvider.StreamChat` — deliberately not `ChatStream`, which would also
expose `Proxy` — and `agentloop.CollectStream` assembles the same response the
non-streaming path produced (truncated `length`/`content_filter` responses drop
their partial tool calls). Providers without streaming fall back to one Chat
call delivered as a single delta. Profiles are unchanged: Plan and Review are
read-only, Build edits (including the new `edit_file` exact-replacement tool)
and asks before `run_command`, and push is never an agent tool. Mode and model
are per session and cannot change while a turn is running or waiting.

A session can instead **run as one of the workspace's agents** (migration 84,
`developer_sessions.agent_id`; the composer's agent picker lists the built-in
profiles and every agent the caller can see). `buildDeveloperToolkit`
(`developer-toolkit.go`) then uses the agent's system prompt, skills (lazy
`load_skill`), MCP sets and built-in tools, layered on top of the container
tools, which keep their names on collision. Permissions come from the agent's
`confirmation_required_tools`: listed tools pause for approval and everything
else runs, so a read-only agent is expressed by giving it no editing tools or
listing them. The model stays per session: choosing an agent adopts its model
once, and changing the model afterwards never changes what the agent may do.
The agent's built-in tools run on the AT host, not in the container, and the
system prompt says so. Every resource passes the same execution admission as
Sessions (`agents.run`, `skills.use`, `mcp.use`, tool class). A deleted agent
fails the run with an explicit message rather than silently falling back.

**Background chat runs.** Developer `run`/`confirm`/`answer` use the bounded
replay transport (`developer-streams.go`, `chat-streams.go`): the initiating
identity is retained, HTTP disconnects do not cancel execution, and server
shutdown or the 30-minute run deadline still does. Closing a chat tab or the
page detaches only; explicit **Stop**, session deletion and space stop/reset
cancel local execution. Owner-scoped `GET .../{id}/active-stream` discovers a
live run and `GET .../{id}/streams/{stream}?offset=` replays it; neither starts
work. Streams are replica-local and restart-volatile, so multiple replicas
need sticky routing. Missing replay never triggers an automatic POST retry.
Pending approvals/questions and completed messages remain in the database.
Migration 100 adds durable Developer session run receipts (`developer-runs.go`):
server-generated IDs exclude concurrent handlers across replicas, 15s renewals
(5s call deadline, 60s database-clock stale threshold) observe cancellation flags,
and tool dispatch rechecks the receipt. History/pending/snapshot writes hold the
session row while checking run ownership. Active-stream discovery reports the
receipt separately from local replay and marks expired running jobs failed, never
clearing their receipt or replaying tools. The UI blocks new work while unresolved.
Normal exit releases only its own receipt and keeps waiting approvals. A crashed
owner's receipt stays held: verify the former process/remote tools stopped before
using a new session. This is exclusion/cancellation reporting, not remote fencing,
automatic failover or durable replay. Drain older processes before upgrading.
The page restores the last owned chat per space, polls session metadata and
reconnects to live work. The transcript uses Chats' blue-ruled user blocks,
plain assistant text, shared compact `ToolActivity` and searchable model/agent
palettes while preserving file/diff actions and the editor shell. Regressions:
`internal/server/developer-streams_test.go`, `_ui/tests/resumable-stream.test.mjs`.

Migration 101 adds durable Developer space Start/Stop/Reset control receipts and
execution suspension. Control and run acquisition share the parent row lock;
Stop/Reset cancel persisted runs and close admission, while explicit Start refuses
unresolved run receipts. Reset retains records until receipts are released and
runtime purge succeeds. Session/space deletion and reconfiguration cannot erase
receipts. Manager command/file/terminal activity registers atomically with local
provisioning; Stop/Reset cancel and drain before removal, retaining failed drains
for explicit cleanup retry. Untracked scope Stop checks the backend. These are
not distributed activity leases or remote fencing: pre-admitted remote requests,
idle cleanup, account cleanup and home reset still need distributed coordination.
Kubernetes remains single-replica; no automatic orphan-control recovery exists.
Regressions: `developer-control_test.go`, `container/activity_test.go`.

**Source control** works on the selected project: NUL-separated
`git status --porcelain=v2` (paths with spaces are safe), per-file diff (untracked
files via `--no-index`), stage/unstage, confirmed discard, commit, branch
switch/create, `pull --ff-only`, and push of the *current* branch only after the
caller confirms that exact name. Pull and push re-validate the origin remote
(no credentials in the URL, no private/loopback hosts), as clone does.

The chat composer shows an "N files changed in <project> +A -D" strip above
the input, from `git/status?numstat=true`. Counts cover staged, unstaged and
untracked changes against HEAD, computed on a throwaway `GIT_INDEX_FILE`
(untracked files added intent-to-add) so the real index is never touched.
Each entry opens a `git/diff?head=true` tab (or the untracked diff).

Every `/developer-space/*` route — including reads, because they start the
container — requires `agents.execute`, rebinds the live execution identity and
checks `execution.run`; `GET /developer-space` and the session list/history only
need `agents.read`. Containers keep their existing limits (capabilities dropped,
`no-new-privileges`, PID/CPU/RAM/disk quotas). Developer spaces re-add only
`container.PackageManagerCapabilities` (CHOWN, DAC_OVERRIDE, FOWNER, FSETID,
KILL, SETGID, SETUID — a subset of Docker's defaults). With none of them, apt
could download packages but dpkg failed in maintainer scripts. For example,
openssh-client's `groupadd _ssh` could not write `/etc/gshadow`. After 30 minutes idle, or on
Stop, the container is **stopped, not deleted** (`RetainWhenIdle`), so what
the user installed in it survives. Rootless Docker is recommended but not
required: a rootful daemon only logs a warning at container creation, because
AT often runs as a host binary talking to the system daemon. **Reset** deletes
the space, its sessions and its volume after an explicit confirmation.

The toolbar's settings button edits the space's base image, CPU, memory and
disk limit (`PUT /developer-space`; the page resends the stored `config`
because PUT replaces the record). An empty image means
`service.DefaultDeveloperImage` (`debian:13.7-slim`). **AT installs nothing
into a space and checks for no tools.** The image and anything added from the
terminal are the user's responsibility. File explorer and agent file tools
need nothing in the image (the embedded `at-devfs` helper is copied in; only a
build without helpers falls back to `python3`), while without
`git` source control fails and reports the plain command error. AT does three
things so that any image works:
- `KeepAlive` sets `--entrypoint sleep … infinity`, because stock images exit
  immediately under `docker run -d`.
- The terminal falls back from `bash -l` to `sh -l`.
- A one-line `APT::Sandbox::User "root"` file is written into
  `/etc/apt/apt.conf.d` when that directory exists. With every capability
  dropped, apt cannot switch to its `_apt` user, and downloads die with
  "Method http has died unexpectedly". apk and dnf need nothing.

An image reference is validated strictly (`service.ValidContainerImage`, plus a
flag/whitespace refusal in the driver). Containers carry an `at.config` label
hashing their configuration. A leftover or stopped container is resumed only
when that label matches; changing the image or limits replaces it. That loses
the installed packages, while `/workspace` survives.

**Persistent home (opt-in, per account).** Space settings → *Persistent home*
mounts a second Docker volume (`at-home-<hash of "developer-home:<user>">`) at a
chosen path (default `/root`) and sets `$HOME` to it, so SSH keys, `.gitconfig`
and tool settings survive container rebuilds. It is keyed by account only, so
one home is shared by the account's spaces in every workspace. The settings live
in `user_preferences` (`developer_home`), so no migration is needed. The mount
path must be absolute and outside `/workspace` and system directories
(`container.ValidHomePath`). Home settings are part of `container.Config`, so
enabling or moving the home recreates the container; they are empty when off,
so existing containers keep their `at.config` label. `POST
.../home/files` copies an upload in with `docker cp` (default mode 600), because
exec-based file tools are confined to `/workspace`. `POST .../home/reset`
removes the volume and every managed container mounting it. Deleting an account
does the same (`nativeauth.Auth.OnUserDeleted`). Storage is **not encrypted by AT**:
it relies on the host disk. Agent commands run as the same user and can read
the home, which the UI states. Regression: `internal/service/container/home_test.go`
(including `TestDockerPersistentHome` against real Docker).

Sandboxes run through a backend-neutral `container.Manager` over a
`container.Driver` (`internal/service/container/driver.go`). The Manager owns
what does not depend on the backend: live scopes, activity and idle expiry
(stop vs delete), the `/workspace` boundary, quota checks (`du` run inside the
sandbox) and terminal bookkeeping. The driver owns create/resume, running,
exec, attach, stop, remove and purge. The default driver is `docker.go`,
which uses the host's docker CLI: `DOCKER_HOST` or the docker context selects
a local, rootless or remote daemon. Experimental `internal/service/sandboxkube`
is selected through bootstrap `server.sandbox.backend: kubernetes` with
operator-owned `server.sandbox.kubernetes` settings. It requires a dedicated
namespace, a built helper image (`ci/Dockerfile.sandbox-helper`), declared CNI/PID
guarantees and **one AT replica**. Kubernetes Stop deletes the pod, preserving
PVCs but not installed system packages; runtime capabilities drive the UI
warning. Static init helpers provide exec supervision, terminals and file
operations without Python/tar. Distributed ownership/activity fencing and
durable run/replay coordination remain unimplemented; `single_replica: true`
remains required. A namespace-wide `at-sandbox-controller` Kubernetes Lease
rejects a second controller at sandbox admission, checks its UID/holder per
operation and renews every 5s. Loss cancels active streams and refuses new work;
clean shutdown drains before clearing the holder. Stale Leases are never stolen
automatically: stop the former controller and inspect orphan Pods before operator
deletion/restart. This is single-controller exclusion, not distributed
fencing/failover. After acquiring the Lease, the first operation stops previous
managed Pods before admitting new work (bounded recovery, UID checks, no PVC
deletion). Shutdown drains streams, stops managed Pods, then releases ownership;
failures keep the Lease held. AT restarts therefore recreate root filesystems
while keeping workspace/home PVCs. See
`docs/developer-spaces-kubernetes.md` and `deploy/kubernetes/sandbox-rbac.yaml`.
Driver/fake API regressions live in `internal/service/sandboxkube/driver_test.go`;
launcher/PTY tests in `internal/sandboxruntime`. Opt-in real kind tests are in
`sandboxkube/integration_test.go`, run by `bash ci/test-sandbox-kubernetes.sh`
(isolated kubeconfig, disposable cluster). They cover exec/PVC/home/TTY and both
stream transports; kindnet does not enforce policy, so this is transport/storage
smoke coverage, not an isolation certification. Fixtures use an administrator,
but sandbox calls impersonate a service account with the shipped Role; negative
checks refuse Secrets, other namespaces and unrelated Leases. Recovery/shutdown
tests verify workload replacement, PVC retention and active-command draining.
Actual cluster CNI/CSI and
multi-node guarantees still need verification. A terminal is a `Terminal` interface rather
than a PTY file, because a Kubernetes exec stream has no local PTY. Adding a
backend means writing a new Driver and choosing it at construction
(`container.NewWithDriver`). Callers do not change. Scope state is still in
process memory, so two AT replicas do not share it. Regressions:
`manager_driver_test.go` (fake driver) and `TestDockerStockImageSandbox` (real
Docker with network: no tools preinstalled, apt install works, install survives
stop, config change starts fresh but keeps `/workspace`; skipped without
Docker). Other regressions:
`developer-spaces_test.go` (store + HTTP, including the migration),
`developer-git_test.go`, `agentloop` `TestCollectStream`,
`_ui/tests/developer-space.test.mjs`.

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
- Factory: `store.New(ctx, cfg)` requires `store.postgres.datasource`; startup fails with a descriptive error when unset. Store tests run against a real postgres (`make env`) via `internal/store/postgres/postgrestest` and skip when unreachable; each test gets a private database copied from a per-process template (see *Test suite cost* below)

### Tests
- Standard `testing` package, table-driven with `t.Run`
- Pattern: `tests := []struct{name string; ...}{}` → `for _, tt := range tests { t.Run(tt.name, ...) }`
- Hand-written mock structs (no mock framework)
- HTTP tests: `httptest.NewRequest` + `httptest.NewRecorder`, call handler directly
- `t.Helper()` in test helpers
- No `go:generate` directives

#### Test suite cost

`make test` is `go test -v -race ./...`. Two structural costs used to dominate it
(212s → 33s once both were removed); keep them in mind before adding fixtures.

**Password hashing.** The `TestMain` of both `internal/server` and
`internal/nativeauth` calls `nativeauth.LowerPasswordCostForTests`, which lowers
`nativePasswordIterations` to 1000. An encoded PBKDF2 hash is verified with
*its own* iteration count, so a fixture storing `password.Dummy` — built by the
library at the 600k default — paid full production cost on every sign-in (~1.5s
under `-race`). Fixture accounts therefore store `nativeauthtest.PasswordHash`
and sign in with `nativeauthtest.Password`; the same call lowers the
unknown-user comparison target `nativePasswordDummy`. Production keeps the
library default for both, because the dummy must cost what a real hash costs or
the unknown-user path is timeable. A new test package that signs in must do the
same in its own `TestMain`.

**Database fixtures.** `internal/store/postgres/pgtemplate` migrates one
template database per process and hands each test a copy
(`CREATE DATABASE ... TEMPLATE`, ~40ms) instead of running the migration set
(~360ms) and dropping ~80 tables (~165ms). `postgrestest.New` and the store
package's `newTestStore` both use it; `TestMain` calls `Release()` to drop the
template, and a crashed run's leftovers are swept by ULID age on the next run.
Isolation is stronger than the previous per-test table prefix, which remains as
the fallback when the DSN is not URL-shaped or the role lacks `CREATEDB`.
`TestWorkspacePostgresLegacyOwnershipMigration` and `TestAuthRefreshMigrationFrom26`
deliberately keep their own prefixes: they replay historical migration sets.

Deadlines derived from `clock_timestamp()` must be asserted against that clock,
not `time.Now()` — a few milliseconds of skew between the test process and
postgres is normal and says nothing about the behaviour under test
(`TestAuthRefreshCleanupAndDeviceRevocation`).

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
- **Svelte 5** (runes mode), **Vite 8**, **TailwindCSS 4** (CSS-based config), **TypeScript**
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
- Single dark theme, no light mode and no theme switch. `.dark` is always on
  `<html>` (set in `index.html` and `store.svelte.ts`), so legacy `dark:`
  variants still resolve; write new classes without the prefix.
- Palette (`@theme` in `global.css`, modelled on the OpenCode TUI): `dark-base`
  #0f0d0d ground, `dark-surface` / `dark-elevated` / `dark-highest` for raised
  blocks, `dark-border` / `dark-border-subtle` for rules, `dark-text` /
  `-secondary` / `-muted` / `-faint` for type, `accent` #5c9cf5 (blue), plus
  `oc-peach` (running/selected), `oc-violet`, `oc-green`, `oc-red`. The gray
  scale is remapped onto the same tones; prefer the named tokens.
- Type is JetBrains Mono everywhere (`--font-sans` and `--font-mono`).
- Page backgrounds (`html`, `body`, `.grain-background`) and the app sidebar/
  navbar carry a 2px dither grain (`--grain` in `global.css`, the omp.sh
  texture). `bg-dark-base` alone stays flat; bordered boxes and settings
  sections mask the underlying grain with a flat base-layer fill, which explicit
  background utilities may override. Grain is dropped under `prefers-contrast: more`.
- Solid `bg-accent` fills carry dark ink (`text-dark-base`), never white.
- When `<style>` needs Tailwind: `@reference` the relative path to
  `src/style/global.css` (not `"tailwindcss"`, and not the `@/` alias, which CSS
  does not resolve), so the theme tokens exist inside `@apply`
- `:global()` for styling `{@html}` rendered content or third-party library elements
- Path alias: `@/` maps to `src/`

The app style is square (no `rounded*` on cards, inputs, buttons, badges or
modals), compact (`text-xs`/`text-sm`, `px-3 py-1.5`) and flat: a panel is a
`border border-dark-border` block on the ground (no raised fill) with a
`px-4 py-3 border-b` header strip and a `p-4` body. Table and field labels are
sentence case, never `uppercase tracking-wider`. Raised `bg-dark-surface` is
reserved for things that float (dialogs, menus, popovers, sticky bars) and for
row hover. The shell (sidebar, navbar, settings nav) sits on `dark-base`; the
active nav entry is `bg-dark-elevated` with a peach icon. `DataTable` pins its
last (actions) column while the table scrolls horizontally.
Providers, Agents, Secrets, Features and Tokens are the reference pages.

The `.settings-*` classes in `src/style/global.css` (`@layer components`) exist
only to reproduce that system for the settings and auth surfaces without
repeating long utility strings. They previously defined a *second* design system
— rounded, airier, `text-2xl` titles, top-rules instead of cards — so a single
sidebar click between Permissions and API tokens visibly changed styles. Keep
them in sync with the reference pages; do not let them drift again. Note that
inline utilities beat `@layer components`, so a page that hardcodes `text-2xl`
overrides `.settings-title` — use the shared classes instead.

The full-screen auth gates render outside the app shell (sign-in, first setup,
account recovery, backup codes, mobile approval, the connection splash), so they
carried their own layout — a bare centred column, `text-2xl` heading, rounded
panels — and were the first screen a user saw. They now share
`lib/components/AuthShell.svelte`: a single reference card (header strip with
title/subtitle plus the brand mark on its right, sized to the two title lines,
then a `p-4` `settings-form` body). The mark used to sit in a separate wordmark
block above the card, which only repeated what the title already says. Add a new
gate by rendering `AuthShell` with `title` / `subtitle` /
`width` (`sm` forms, `md` setup, `lg` review screens) rather than a new layout.
Primary/secondary actions in these gates are full-width with
`min-h-11 sm:min-h-0`, the established touch-target pattern, because sign-in is
the one screen that must work on a phone.

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
