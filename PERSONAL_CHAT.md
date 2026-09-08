# Personal Chat V1

Persistent, text-only provider/model conversations for web and Flutter/mobile
clients. Both clients use the same authoritative PostgreSQL transcript.

## Clients

- Native administrators use persistent web chat at `#/chat` and
  `#/chat/<conversation-id>`, with history, model/system settings, rename/delete,
  safe Markdown, cancellation and durable-status recovery.
- The tool/image playground remains at `#/playground`. Legacy-mode `#/chat`
  retains that playground; private persistent conversations require native auth.
- Flutter in `_mobile/` uses the same conversation/message IDs after PKCE login.
  It supports text chat, model selection, history, streaming and cancellation.
  Mobile Markdown, attachments/tools and editing existing model/system settings
  remain deferred.
- EOF does not mean completion. Both clients recover authoritative snapshots;
  uncertain sends may be explicitly replayed only with the original request ID
  and content. There is no durable offline outbox.

Backend race tests use PostgreSQL and adapter HTTP fixtures. Browser checks and
Flutter widget/client tests use mocked backends; deployed web-to-phone
interoperability and physical device acceptance remain required.

## Access And Compatibility

- **Native administrators only.** These routes are additions under the existing
  `/api` middleware; this is not a new authorization surface for regular users.
- Browser clients use the existing native session cookie. Mutating cookie
  requests require the exact configured native-auth `Origin` and the existing
  same-origin protections. Mobile clients use their current native mobile
  `Authorization: Bearer <access_token>` without cookies. Gateway API tokens are
  not native mobile credentials and do not grant access.
- Every conversation stores `owner_user_id = identity.Subject` from verified
  middleware identity. Clients cannot supply or change ownership. No empty-owner
  or legacy shared-history fallback exists. A second administrator gets `404`
  for another owner's conversation or nested message, including cancellation.
- With native auth disabled, the handler returns `503` with
  `{"message":"personal chat requires native authentication"}`. Existing outer
  middleware may instead reject the request with `403` before it reaches the
  handler. Native mode returns `401` for absent/invalid sessions and `403` for
  authenticated non-admins. No auth configuration is disclosed.
- Existing `/api/v1/chat/completions`, `/api/v1/chat/sessions/*`, agent/task
  sessions, bot behavior, and gateway endpoints are unchanged. Personal chat does
  not create an agent, task, bot session, or workflow.
- The old `/chat` playground's in-browser transcript cannot be migrated by the
  server: it was never persisted. There is no import or automatic migration.

All paths below are relative to the deployment base path. For example a server
mounted at `/at` uses `/at/api/v1/conversations`. JSON responses and SSE use
`Cache-Control: no-store`.

## Routes

| Method | Path | Success |
| --- | --- | --- |
| GET | `/api/v1/conversations/models` | `200`, safe model array |
| POST | `/api/v1/conversations` | `201`, conversation |
| GET | `/api/v1/conversations?limit=50&before=<id>` | `200`, conversation page |
| GET | `/api/v1/conversations/{id}` | `200`, conversation |
| PATCH | `/api/v1/conversations/{id}` | `200`, updated conversation |
| DELETE | `/api/v1/conversations/{id}` | `204`, empty body |
| GET | `/api/v1/conversations/{id}/messages?limit=50&before=<id>` | `200`, message page |
| GET | `/api/v1/conversations/{id}/messages/{message_id}` | `200`, message snapshot |
| POST | `/api/v1/conversations/{id}/messages` | `200`, SSE turn or replay |
| POST | `/api/v1/conversations/{id}/messages/{message_id}/cancel` | `200`, assistant snapshot |

There is no separate status resource: GET of an assistant message is its durable
status and current checkpoint. Cancellation takes no body. A user message ID at
the cancel route returns `404`.

## Model Catalog

The exact response shape is an array, not an OpenAI `data` envelope:

```json
[
  {"provider_key":"anthropic","model":"claude-sonnet-4-5"},
  {"provider_key":"openai","model":"gpt-4.1"}
]
```

Each entry has **only** `provider_key` and `model`. Entries are sorted by provider
key then model and deduplicated. They come from registered runtime providers with
the `service.LLMProvider.Chat` capability and their configured chat model list.
An empty list uses that provider's nonempty default model. Embedding model lists
are not included. Catalog configuration is advisory about upstream capability:
AT does not probe upstream or guarantee a configured model is actually text-capable.

No credentials, base URLs, extra headers, OAuth tokens, provider config, or
embedding metadata are returned. A conversation's `(provider_key, model)` must
exactly match a returned pair; arbitrary unlisted models are rejected. The
catalog is checked on create/config change and again before a new upstream call.
If a selected provider/model disappears, the admitted assistant fails durably
with `model_unavailable`. Historical snapshots remain readable and retries of an
existing request never require the old model to still exist.

## Conversations

Create request:

```json
{
  "title":"Trip planning",
  "provider_key":"openai",
  "model":"gpt-4.1",
  "system_prompt":"Be concise."
}
```

`provider_key` and `model` are required. `title` defaults to `New conversation`;
`system_prompt` defaults to an empty string. PATCH accepts any subset of these
same four fields, but `provider_key` and `model` must be supplied together.
Omitted or JSON-null PATCH fields leave existing values unchanged; empty
`system_prompt` clears it. An empty PATCH is allowed. Configuration changes
affect future turns, not existing message snapshots. PATCH and DELETE return
`409` while a turn is active; cancel it first.

The complete conversation response has these fields, always present:

```json
{
  "id":"01K00000000000000000000001",
  "owner_user_id":"01K00000000000000000000000",
  "title":"Trip planning",
  "provider_key":"openai",
  "model":"gpt-4.1",
  "system_prompt":"Be concise.",
  "created_at":"2026-09-07T12:00:00Z",
  "updated_at":"2026-09-07T12:00:00Z"
}
```

IDs are opaque server-generated ULIDs. Timestamps are UTC RFC3339 strings and may
contain fractional seconds. `updated_at` changes on settings updates, turn
admission, terminal generation/cancellation, and stale-turn recovery; intermediate
text checkpoints do not change it.

## Pagination And History

Both list endpoints return:

```json
{"items":[],"next_before":""}
```

`items` contains complete conversation or message objects. `limit` is an integer
from 1 through 100, default 50. Conversations remain ordered by **descending ID**,
not `updated_at`. Messages are ordered by **descending `sequence`**, most recently
admitted first. `before` remains an exclusive **message ID** cursor (maximum 128
bytes): the server resolves it to that conversation's stored sequence before
filtering. A nonexistent or foreign-conversation message cursor returns `404`.
Owner filtering happens before ordering/limiting. An empty result is `[]`, not
`null`. `next_before` is the last returned ID when the page is full; otherwise it
is `""`. A full last page can therefore yield one additional empty page.

**Web/mobile integration requirement:** every message now has a mandatory JSON
integer `sequence` (Go `int64`, PostgreSQL `BIGINT`), starting at 1 and unique
within its conversation. Sort messages ascending by `sequence` for chronological
display, including messages merged from SSE and paginated GETs. Do not sort by
ULID, `created_at`, or arrival order. Both accepted messages, all snapshots,
replays, terminal/cancel responses, and message GET/list responses include it.
Delta/heartbeat frames are unchanged: resolve their message ID against the
accepted snapshot. Keep passing IDs, not sequence numbers, in `before`.

Allocation occurs under the conversation's database parent-row lock. Each turn
receives consecutive user then assistant numbers; replay, checkpoint and
cancellation never allocate or change them. Message `created_at` is database
time, but timestamps are display metadata and may tie. Replica clock skew in a
ULID cannot reorder history or displace the current user question from the prompt.
Do not treat the current page as the complete transcript.
No automatic retention, pruning, or user-visible context truncation is applied
to these tables. DELETE removes the conversation and all its messages atomically.

## Messages And Snapshots

Send request, and the **only** allowed fields:

```json
{"content":"Suggest a weekend itinerary.","request_id":"a-client-generated-uuid"}
```

`content` is a nonblank string of at most 32,768 UTF-8 bytes. `request_id` is a
nonempty string of at most 128 bytes with no surrounding whitespace. Use a fresh
UUID per intentional turn and retain it until the outcome is known. NUL text is
not supported. Message arrays, client roles/transcripts, tools, images, files,
model overrides, and generation options are rejected as unknown fields or wrong
types. The server loads the transcript and stored configuration itself.

Complete message snapshot, with **all fields always present**:

```json
{
  "id":"01K00000000000000000000003",
  "sequence":2,
  "conversation_id":"01K00000000000000000000001",
  "request_id":"a-client-generated-uuid",
  "role":"assistant",
  "content":"Visit the old town on Saturday.",
  "status":"completed",
  "provider_key":"openai",
  "model":"gpt-4.1",
  "finish_reason":"stop",
  "error":"",
  "usage":{
    "prompt_tokens":18,
    "completion_tokens":9,
    "cache_read_tokens":0,
    "cache_write_tokens":0,
    "reasoning_tokens":0,
    "total_tokens":27
  },
  "created_at":"2026-09-07T12:00:01Z"
}
```

| Field | Meaning |
| --- | --- |
| `sequence` | Mandatory positive integer; immutable database-owned order within the conversation |
| `role` | `user` or `assistant`; no system/tool transcript rows |
| `status` | `pending`, `streaming`, `completed`, `failed`, or `cancelled` |
| `provider_key`, `model` | Immutable configuration snapshot at turn admission |
| `content` | User text or assistant text accumulated so far |
| `finish_reason` | Empty until known; `stop`, `length`, or `content_filter` |
| `error` | Empty normally, otherwise a stable safe code below; never raw upstream text |
| `usage` | Latest reported usage; all six integer fields start at zero |

User messages are created `completed`; their finish reason and error are empty
and usage is zero. Assistant messages start `pending`, become `streaming` when
text arrives, and reach exactly one immutable terminal status. A response with
`length` or `content_filter` is `completed` with that finish reason, not an
automatic retry. Partial content and any reported usage survive failures.
Reasoning tokens are a subset of completion tokens. Prompt tokens follow AT's
normalized non-cache bucket; total tokens use the provider's total if supplied,
otherwise prompt + cache reads + cache writes + completion tokens.

## SSE Contract

New sends and idempotent replays return `Content-Type: text/event-stream` and
`X-Accel-Buffering: no`. Use streaming `fetch` on web or an authenticated streamed
POST on mobile, not EventSource. Event frames are UTF-8:

```text
event: <event-name>
data: <one JSON value on one line>

```

No SSE `id`, `retry`, `Last-Event-ID` cursor, or `[DONE]` sentinel is used. Parse
frames across arbitrary network chunks. Ignore unknown event names for forward
compatibility.

### Accepted

The first event is emitted **after** the user/assistant pair has committed:

```text
event: accepted
data: {"user":{"id":"01K00000000000000000000002","sequence":1,"conversation_id":"01K00000000000000000000001","request_id":"a-client-generated-uuid","role":"user","content":"Suggest a weekend itinerary.","status":"completed","provider_key":"openai","model":"gpt-4.1","finish_reason":"","error":"","usage":{"prompt_tokens":0,"completion_tokens":0,"cache_read_tokens":0,"cache_write_tokens":0,"reasoning_tokens":0,"total_tokens":0},"created_at":"2026-09-07T12:00:01Z"},"assistant":{"id":"01K00000000000000000000003","sequence":2,"conversation_id":"01K00000000000000000000001","request_id":"a-client-generated-uuid","role":"assistant","content":"","status":"pending","provider_key":"openai","model":"gpt-4.1","finish_reason":"","error":"","usage":{"prompt_tokens":0,"completion_tokens":0,"cache_read_tokens":0,"cache_write_tokens":0,"reasoning_tokens":0,"total_tokens":0},"created_at":"2026-09-07T12:00:01Z"},"replay":false}

```

`accepted` contains exactly `user`, `assistant`, and `replay`. Both messages use
the complete snapshot shape. No lease token or worker identity is exposed.
Store these durable IDs immediately. On replay the two snapshots can already be
partially or fully populated and `replay` is true.

### Delta And Heartbeat

```text
event: delta
data: {"assistant_message_id":"01K00000000000000000000003","offset":0,"content":"Visit the old town on Saturday."}

event: heartbeat
data: {"assistant_message_id":"01K00000000000000000000003"}

```

`offset` is the UTF-8 **byte** length of assistant content before this delta, not
a JavaScript UTF-16 or Dart string index. Delta content is provisional until a
checkpoint commits. Append only when the ID and offset match local state; GET
the message and replace local content if they do not. Heartbeats are sent about
once per second after a successful database checkpoint, including during upstream
connection setup. They carry no text. Heartbeat cadence is not a delivery SLA.

### Snapshot And Terminal Events

`snapshot` is used on replay and carries the complete assistant message object.
`done` carries that same complete shape with `status: "completed"`.
`error` carries that same shape with `status: "failed"` or `"cancelled"`.
Terminal event payloads are read from the committed store operation, not an
optimistic in-memory result. Example successful terminal frame:

```text
event: done
data: {"id":"01K00000000000000000000003","sequence":2,"conversation_id":"01K00000000000000000000001","request_id":"a-client-generated-uuid","role":"assistant","content":"Visit the old town on Saturday.","status":"completed","provider_key":"openai","model":"gpt-4.1","finish_reason":"stop","error":"","usage":{"prompt_tokens":18,"completion_tokens":9,"cache_read_tokens":0,"cache_write_tokens":0,"reasoning_tokens":0,"total_tokens":27},"created_at":"2026-09-07T12:00:01Z"}

```

Example failed terminal frame:

```text
event: error
data: {"id":"01K00000000000000000000003","sequence":2,"conversation_id":"01K00000000000000000000001","request_id":"a-client-generated-uuid","role":"assistant","content":"Visit the old town","status":"failed","provider_key":"openai","model":"gpt-4.1","finish_reason":"","error":"incomplete_stream","usage":{"prompt_tokens":0,"completion_tokens":0,"cache_read_tokens":0,"cache_write_tokens":0,"reasoning_tokens":0,"total_tokens":0},"created_at":"2026-09-07T12:00:01Z"}

```

Always replace local assistant state with a snapshot/terminal payload. EOF alone
does **not** mean completion. If terminal persistence fails, the connection closes
without a terminal event; recover via GET, including after the lease expires.

## Idempotency And Recovery

Admission takes a PostgreSQL parent-row lock and atomically inserts one completed
user message and one pending assistant. A partial unique index permits only one
active assistant per conversation. A unique `(conversation_id, request_id, role)`
constraint keeps the pair unique. There is no provider retry/fallback in v1.

The request hash is SHA-256 of the exact decoded `content` UTF-8 bytes, without
whitespace normalization. It is scoped to conversation + request ID. Settings
are not part of this hash because send accepts no settings; changing conversation
configuration does not change the meaning of an old request ID.

| Repeated request | Response |
| --- | --- |
| Same ID and same content, terminal | `200` SSE: `accepted(replay=true)`, `snapshot`, `done` or `error`; EOF; no upstream call |
| Same ID and same content, active | `200` SSE: `accepted(replay=true)`, `snapshot`; EOF; no subscription and no upstream call |
| Same ID, different content | `409` JSON; no additional messages |
| New ID while another turn is active | `409` JSON; no additional messages |

Clients **must not automatically create a new request ID** after a timeout or
lost response. Replay the same ID/content if acceptance is unknown. Once an
assistant ID is known, GET it to recover status. For active replay, poll GET until
terminal or explicitly cancel; the replay stream deliberately does not attach
to the original stream. A failed/cancelled request can only be intentionally
re-attempted as a new turn with a new ID, leaving the old pair intact.

## Cancellation, Leases, And Bounds

- Disconnect cancels the request-bound upstream context. The handler attempts a
  final partial save using `context.WithoutCancel` with its own five-second DB
  deadline. It does not continue generation as a detached job.
  Request-context cancellation takes precedence over concurrently ready EOF,
  provider errors, or finish chunks until the final persistence decision:
  cancellation records `cancelled/generation_cancelled`; a context deadline
  records `failed/generation_timeout`. Even a received finish reason is not
  completion before trailing usage and EOF have drained. Already committed
  terminal states remain immutable, including shared-DB cancellation/recovery.
- Explicit cancellation is a database terminal transition to `cancelled`, not
  a process-local cancel map. It preserves the last committed partial snapshot
  and immediately fences further writes. A worker on any replica observes this
  at its next checkpoint (normally within one second) and cancels upstream.
  Uncheckpointed deltas can be discarded by explicit cancellation.
- Repeated cancellation is idempotent and returns the existing terminal snapshot,
  including a completed/failed snapshot if generation won the race. A new turn or
  DELETE may proceed after cancellation commits; the old upstream connection can
  take one checkpoint interval to shut down but cannot write into a new turn.
- Workers have a random, private lease token, a 15-second lease renewed at
  one-second checkpoints, and a nonrenewable five-minute database deadline.
  Worker SQL requires matching token, active status, and unexpired database-time
  lease/deadline. The request also has a five-minute context timeout.
- Owner conversation/message reads and subsequent mutations reconcile expired
  active rows to `failed/generation_interrupted`, preserving checkpointed content.
  Conversation lists reconcile only their bounded returned page. No process
  restart can leave a permanent busy conversation; read/retry after the stale
  lease expires. Recovery invalidates the old token. The old worker cannot revive
  or overwrite a terminal message, even after another turn starts.
- Checkpoint DB calls have three-second timeouts. SSE writes have five-second
  deadlines so a stalled consumer does not hold generation open indefinitely.
  After cancellation, a five-second bounded drain lets adapters with blocking
  buffered-channel sends unwind without retaining abandoned stream workers.
- JSON request bodies are bounded at 256 KiB (to accommodate JSON escaping).
  Titles/provider keys/model names are nonblank and at most 256 UTF-8 bytes;
  system prompts and user content are at most 32 KiB each. NUL is rejected.
  Assistant inline text is capped at 1 MiB; exceeding it fails the turn while
  preserving the accepted prefix, rather than silently truncating completion.
- Before each provider call, AT reads a bounded recent suffix (at most about
  128 KiB/200 completed messages, with a boundary message included), prepends the
  stored system prompt, and applies the existing `loopgov` 32,768-token estimate
  window. Only completed messages enter the prompt; failed/cancelled assistant
  text is not replayed. This is an approximate token bound, not model-specific
  tokenization. Persisted history is never pruned by windowing.
- Runtime adapters are called directly with nil tools and default generation
  options. Streaming adapters provide actual deltas; Chat-only adapters return a
  single content delta. Closure without a recognized finish reason fails as
  `incomplete_stream`, not success. Upstream tool/image output fails as unsupported;
  reasoning text is not exposed or persisted. Usage-only trailing chunks are read
  before committing successful completion.

Native finish reasons are matched case-insensitively after whitespace trimming:

| Stored reason | Accepted native reasons |
| --- | --- |
| `stop` | `stop`, `end_turn`, `stop_sequence`, `endofturn`, `COMPLETE` |
| `length` | `length`, `max_tokens`, `max_output_tokens` |
| `content_filter` | `content_filter`, `content_filtered`, `guardrail_intervened`, `safety`, `blocklist`, `prohibited_content`, `spii`, `recitation` |

Unknown reasons fail with `unsupported_output`; missing reasons at EOF fail with
`incomplete_stream`. This strict policy also applies to Chat-only providers:
`Finished: true` without an explicit recognized finish reason is not success.
Text in a finish-bearing chunk is retained (unless the chunk contains unsupported
tools/images or violates the text bounds). A recognized finish does not end
consumption: trailing usage is consumed until channel EOF under the same bounded
context. Anthropic/MiniMax output usage is read from the top-level `message_delta`
usage object and published by the adapter after finish on `message_stop`.

## Errors And Accounting

Before SSE begins, errors are JSON with exactly this envelope:

```json
{"message":"conversation busy or request_id content mismatch"}
```

| HTTP status | Meaning |
| --- | --- |
| `400` | Malformed JSON/unknown fields, invalid text/settings/cursor/limit, or catalog selection invalid |
| `401` | Native session absent, invalid, expired, or revoked; middleware-owned |
| `403` | Non-admin, origin protection, or legacy outer-middleware denial |
| `404` | Conversation/message absent or not owned; wrong nested parent; cancelling user message |
| `409` | Active conversation blocks send/PATCH/DELETE, or request ID content mismatch |
| `413` | Encoded JSON body exceeds 256 KiB |
| `415` | JSON body endpoint lacks `Content-Type: application/json` |
| `503` | Native auth disabled/unavailable or personal-chat store unavailable |

After acceptance, failures use HTTP `200` with the SSE terminal `error` event.
`error` snapshot codes are `provider_error`, `model_unavailable`,
`history_unavailable`, `storage_unavailable`, `incomplete_stream`,
`unsupported_output`, `output_limit`, `generation_timeout`,
`generation_interrupted`, or `generation_cancelled`. A DB deadline observed by
reconciliation may appear as `generation_interrupted`; an active handler observing
its context deadline records `generation_timeout` when it can still save.
No raw upstream error, response header, credential, or internal DB error is sent
to the client or logged by these handlers.

Each actual generation uses the existing `recordLLMCallAsync` hook with source
`chat`, name `personal_chat`, trace ID = assistant message ID, session ID =
conversation ID, and user field = verified subject. **Only accounting skeletons
are supplied**, even when full audit-body capture is enabled: private transcript
bodies must not leak through the shared administrator trace API. Model, IDs,
usage, latency, finish reason, and safe error codes are operational metadata
visible to administrators. Cost events use `AgentID: personal:<subject>` and
existing pricing with reported usage. These hooks are best-effort, not an
exactly-once billing ledger across process crashes. Usage absent on interruption
stays zero/last-known rather than being fabricated.

Like the existing admin playground, personal chat has no gateway API-token
quota or agent budget: it has neither a gateway token nor an agent. Existing
gateway/agent budget enforcement is unchanged. V1 adds no personal spending
quota; the one-active-turn, text/output, lease, and time bounds above apply.

## Implementation And Verification

- `internal/service/types-personal-chat.go`: separate types and narrow
  `PersonalChatStorer`, not added to the composite `Storer`.
- `internal/store/postgres/migrations/29_personal_chat.sql`: next migration after
  native mobile auth 28; separate `personal_conversations`/`personal_messages`
  tables with configured table prefix, ownership, idempotency, and active index.
- `internal/store/postgres/migrations/30_personal_chat_sequence.sql`: additive
  authoritative sequence column and unique per-conversation index. Existing v29
  messages are backfilled in their previously exposed ID order, including their
  JSON snapshots; original cross-replica admission order cannot be reconstructed
  retroactively. Subsequent admissions use locked database sequence allocation.
  Stop admissions/drain old replicas during rollout: v29 writers cannot insert
  rows without the now-required sequence. Migration 29 is left unchanged.
- `internal/store/postgres/personal-chat.go`: owner-scoped transactions,
  pagination, atomic admission, leases, cancellation, and recovery.
- `internal/server/personal-chat.go`: native admin guard, catalog, CRUD, SSE,
  authoritative prompt construction, provider calls, safe accounting.
- `internal/server/server.go`: additive route registration only.

Focused tests against **real PostgreSQL 17**, including race detection:

```sh
AT_TEST_POSTGRES_DSN='postgres://postgres@localhost:55432/postgres?sslmode=disable' \
  go test -race ./internal/server ./internal/store/postgres -run '^TestPersonal' -count=1
```

The repository's PostgreSQL helpers skip DB tests if PostgreSQL is unreachable;
use an available PostgreSQL 17 instance to verify rather than relying on skips.
Coverage includes two-admin CRUD/nested ownership, native admin/mobile/browser
auth, safe catalog, text-only validation, atomic/concurrent admission, same-ID
replay and content conflicts, provider setup/midstream failures, missing finish,
unsupported output, output cap, durable terminal payload equality, disconnect,
cross-replica cancellation, cancellation/checkpoint races, expired leases and
deadlines, stale-worker writes, prompt windowing without pruning, and terminal
write failure without a false SSE completion.
The real OpenAI adapter is also exercised with a finish chunk but no `[DONE]`,
and with `[DONE]` but no finish chunk. Chat-only fallback, private trace-body
omission, cost recording, and draining blocked adapter sends are covered.
Regression fixtures additionally exercise actual Anthropic, MiniMax, Bedrock and
Cohere HTTP adapters, including native stop/length reasons, unknown/missing
reasons, trailing usage and nonzero priced cost. Deterministic cancellation/EOF,
error, finish and deadline cases retain partial text with the correct terminal
status. Sequence tests seed future-skewed legacy ULIDs, verify the current question
is last, traverse all message pages, reject foreign cursors, preserve sequence on
worker writes/replay/cancel, and migrate populated v29 tables to v30.

Broader backend regression command:

```sh
AT_TEST_POSTGRES_DSN='postgres://postgres@localhost:55432/postgres?sslmode=disable' \
  go test -race ./internal/server ./internal/store/postgres ./internal/service/...
```

## Deferred

Web/Flutter UI work; non-admin access and finer-grained authorization; sharing;
imports; transcript edits/branches/regeneration; attachments, images, audio and
tools; temperature and advanced generation settings; discovery of unconfigured
models; resumeable SSE/event cursors and reconnect subscriptions; background
generation after disconnect; automatic retries/fallbacks; model-specific
tokenization; personal spending quotas; automatic history retention; exactly-once
crash-safe usage settlement. None is silently emulated by v1.
