# Workflow editor: n8n comparison and development priorities

Reviewed: 2026-09-21. Evidence: AT source and n8n's official documentation.
This is a product/implementation comparison, not a usability study or a claim
that the two execution engines have equivalent semantics.

## Main finding

Bigger nodes help recognition, but n8n's advantage is the complete build–inspect–test
cycle: search for an operation, add it from the current step, map a real input
field, execute just that step, inspect the result, and reuse its test data.
AT already has substantial AI execution capabilities; improving that cycle has
more value than starting with a large catalog of additional integrations.

## Implemented in this change

- Kaykay 0.2.1 in the package manifest and lockfile; use its public
  `clientToCanvas` API for native drag/drop.
- A collapsible 288px node library with search over labels, descriptions, types
  and categories, readable descriptions and keyboard-focusable add actions.
- Shared 256px workflow cards, larger headings/type badges and 12px detail text,
  themed light/dark surfaces; annotations retain their own size.
- Empty-canvas starting action; click-added nodes use the current viewport and
  avoid existing cards rather than following a global counter offscreen.
- Output-specific “add next step” actions in the inspector. Connect automatically
  only when exactly one compatible input exists; ambiguous inputs require the
  user's choice by wiring the ports. No arbitrary prompt/context selection.
- Wider inspector, visible undo/redo/fit controls, Ctrl/Cmd+S and Escape for the library.
- History graphs load atomically through `fromJSON`, preserving edges without
  requiring the newly rendered handles to have mounted. Historical versions are
  locked, saving is disabled and running them does not overwrite the current graph.
- Pending inspector edits are applied before Save, Run or adding the next step.
- New connections reject cycles, matching the DAG engine.

## Comparison

| Area | n8n | AT evidence and remaining gap |
|---|---|---|
| Node discovery | Search/browse, trigger/action distinction, add from connector [1] | Central node definitions already exist. Library and next-step improvements ship here. Trigger management remains separate; HTTP/cron legacy renderers are intentionally not offered in the palette. |
| Readable canvas | Node controls, notes, grouping [1] | Kaykay already supplies zoom, selection, clipboard and history; AT already supplies groups/Markdown notes. The former cards were 144–240px with 9–11px detail text. This change consolidates their presentation. |
| Data mapping | Drag an INPUT field into a parameter or use an expression editor [2] | Typed edges and Go-template/JavaScript editing existed. The first follow-up now adds a connected-input field picker and typed-value JSON Pointer mappings; direct drag into expression fields remains a gap. |
| Step testing | Execute a node and the ancestors required for its inputs [3] | The second follow-up adds explicit test runs up to a selected node and its ancestors; unrelated branches and later nodes are excluded. |
| Test fixtures | Pin/edit output for manual runs; production ignores pinned data [4] | The second follow-up adds session-local Pin/Unpin with server-side freshness checks and preserved selection routing. Fixture editing, persistence and full fan-out replay remain later extensions. |
| Error recovery | Per-node retry, stop/continue/error output; shared error workflows [1][5] | The third follow-up adds common transient-failure retry and stop/skip-branch/failure-output policies. Shared error workflows and durable resume remain separate work. |
| Flow logic | If/Switch, Merge, loops, Wait [6][7] | Typed Switch, append/zip/key Merge and array Aggregate are implemented. The fifth follow-up adds durable duration/approval Wait for non-Loop DAGs. Cross-fan-out collection and waiting inside nested workflow calls remain separate capabilities. |
| AI and tools | Agents, tools and integrations | AT already has LLM/agent calls, skill/MCP/agent resource nodes, images, vision, speech, transcription and embeddings. Better connection/configuration guidance is more urgent than duplicating these nodes. |
| Inspection | Execution history and debug/replay [3][5] | Live SSE outputs/errors/duration, run views and LLM observations exist. The first follow-up now pairs input/output snapshots per invocation; persisted replay and full fan-out history remain separate work. |
| Credentials/integrations | Operation-oriented integration catalog | AT has encrypted Connections, connectors, skills, generic HTTP and MCP. Credentials inside agent/skill execution do not automatically make every integration a deterministic workflow action. |

## Recommended implementation order

Delivery status:

1. **Implemented:** Input/output inspector and connected-input field mapping.
2. **Implemented:** Partial execution and development-only pinned data.
3. **Implemented:** Common retry and error routing.
4. **Implemented:** Edit Fields, Filter, Switch, Merge/Aggregate.
5. **Implemented:** Durable duration waits, approval and restart-safe continuation for non-Loop DAGs.
6. **Later:** Expanded operation catalog and starter workflows.

### Shipped mapping contract

The inspector now has Parameters / Input / Output sections. Input offers a bounded
field browser and manual RFC 6901 JSON Pointer entry; Output offers field and JSON
views. Mapping configuration lives in the existing node data JSON:

```json
{"input_mappings":{"prompt":"/prompt/customer/message"}}
```

The pointer starts at **this node's gathered inputs**, keyed by target port. It is
not a reference to arbitrary nodes, `$json`, a Go template or a JavaScript expression.
All mappings resolve against the original inputs independently, then override the
named target ports before `Noder.Run`. Missing paths, malformed pointers and unknown
target ports fail before that node's external call. Absent mappings retain existing
behavior. No database migration is needed.

Streaming node events carry a unique `execution_id`, gathered `inputs`, optional
`resolved_inputs`, and completed output `data`. Input and output snapshots are detached
from live node values. Each preview is bounded to 64 KiB; oversized/non-JSON values
are explicitly omitted, not silently clipped. This cap is an inspector transport
bound, not an execution data limit or the agent-loop tool-result cap.

Fan-out invocations share a node ID but have distinct execution IDs. The inspector
shows the latest-started invocation and pairs its input with only its own completion;
it also displays the invocation count. It does not claim to be a full fan-out history.
Changing graph versions clears the snapshots. Editing parameters leaves the clearly
labelled **last run** data available for mapping. Snapshots are session-local, not
persisted execution history or pins.

Verified with workflow engine/node race tests, targeted server workflow tests, UI
field-path/event-correlation tests and Chrome interactions using mocked API/events
(mapping selection, Save payload, field/JSON views, light/dark/mobile).

### 1. Input/output inspector and data mapping

Initial implementation complete as described above. Rich parameter expressions and
drag-and-drop mapping directly into template fields remain later extensions.

**Value:** users can see what they are wiring, rather than remembering object paths.

- Give the inspector Parameters / Input / Output views.
- Extend bounded node events with input snapshots and execution identity; currently
  `NodeEvent.Data` is the completed node's output.
- Add tree/JSON views with copyable paths and example values.
- Define expression semantics per field explicitly. AT currently mixes Go templates
  and JavaScript contexts; do not paste n8n's `$json` syntax into fields that cannot
  evaluate it.
- Acceptance: map a nested HTTP response field into an LLM prompt without writing
  a Script node; invalid paths explain the problem before a paid call.

### 2. Partial execution and pinned development data

Initial implementation complete. `Execute step` opens the run panel with a target;
it runs the target's ancestor subgraph using existing execution ordering, mapping,
conditional routing, cancellation and runtime admission. Downstream and unrelated
nodes are excluded before node validation. A target on an inactive branch remains
skipped. The selected target always executes even if its own output was pinned.

`Output → Pin output for tests` freezes a complete successful output and its result
kind/selection routes in the current editor session. `Use pinned outputs (test mode)`
is explicit in the run panel, with individual Unpin and Clear all actions. Pins are
not saved into workflow node data or browser storage; changing viewed versions clears
them. Pin replay skips only the mocked node: its ancestors still execute unless also
pinned. UI status says **Pinned**, rather than claiming a provider call completed.

The streaming endpoint accepts an optional request-only contract:

```json
{
  "inputs": {"text": "hello"},
  "test": {
    "target_node_id": "format",
    "pins": {
      "model": {
        "signature": "<pin_signature from completed event>",
        "result_kind": "result",
        "data": {"response": "previous model output"}
      }
    }
  }
}
```

Selection results additionally carry `selection` (for example HTTP's
`["always","error"]`). Signatures cover the source node, its transitive upstream
node configuration/wiring, entry selection and run inputs; presentation changes and
downstream edits do not invalidate them. This is a freshness check, not an access
credential. Stale pins fail before SSE commitment/provider calls. External service,
provider or referenced-workflow state is deliberately frozen by a fixture, not
included in its structural signature; unpin to fetch a new result.

Production `Engine.Run`, cron/webhooks and nested child engines never receive test
pins. The nonstreaming `/workflows/run/{id}` endpoint rejects `test` options instead
of silently misrepresenting a production run. Real nodes retain live execution and
provider authorization. Mocked nodes still require node execution admission but do
not resolve provider credentials or consume their budget. Active runs are labelled
`stream-test`; `run_started` includes `test_mode`.

Bounds: 32 pins, 64 KiB output each, 512 KiB combined fixtures; streaming request body
8 MiB. Loop outputs and descendants cannot be pinned from a one-invocation preview;
resource-config and Output nodes are not replayed as ordinary results. No migration.

Verification: engine race tests prove no repeated LLM/HTTP calls, selected-target
execution, inactive-branch routing, stale rejection and production isolation. UI
tests cover immutable snapshots and opt-in request serialization. Chrome mock-API
checks cover pin/unpin, step and full test requests, opting out of pins, desktop and
mobile. The real PostgreSQL workspace HTTP regression was added but skipped locally
because PostgreSQL was unavailable; its pure request-preflight tests passed.

**Value:** avoid repeating paid LLM/media calls while testing downstream logic.

- Add an explicit manual/test execution mode with a target node and ancestor closure.
- Bind fixtures to workspace + graph/version + node configuration; invalidate stale
  data when upstream dependencies change.
- Pins must be ignored by cron, webhooks and production calls, not merely hidden in UI.
- Enforce the same execution/provider/budget permissions for partial runs.
- Acceptance: rerun a formatting step against pinned model output without another
  provider call; production still executes the real model.

### 3. Common retry and error routing

Initial implementation complete. The inspector's **Settings** section stores
optional common settings in the existing node data JSON:

```json
{
  "execution": {
    "max_attempts": 3,
    "retry_delay_ms": 1000,
    "backoff": "exponential",
    "on_error": "error_output"
  }
}
```

Without this block, the engine keeps one attempt and stops on a returned error.
`max_attempts` includes the initial call (1–5). Base delay is 0–30000 ms; fixed or
exponential backoff caps at 30 seconds. Typed upstream 408/429/5xx errors (except
501), transient network errors and local request timeouts are retryable; arbitrary
application/configuration errors are not. Upstream `Retry-After` is honored up to
5 minutes. A longer hint ends retries rather than retrying earlier than requested.
Every attempt rechecks execution authority. Parent cancellation/deadline interrupts
the delay and is never handled as a recoverable workflow failure.

Failure policies apply to errors **returned by Run**, after eligible retries:

- `stop`: propagate the failure (historical default).
- `continue`: emit a handled-failure event, produce no output, and continue independent
  branches. Dependent nodes follow ordinary inactive-input rules. No success payload
  is fabricated and no arbitrary conditional branch is chosen.
- `error_output`: activate only reserved `__error`, shown as **failure** on the card.
  Its payload includes `node_id`, `node_type`, `message`, `attempts`, `input`, and an
  upstream `status_code` when available. Normal outputs stay inactive on failure;
  successful nodes never activate this port. A failure edge requires this policy;
  the editor refuses to disable it while its connections remain.

Configuration/input-mapping errors and execution authorization failures remain hard
errors. `ErrStopBranch` remains a normal branch stop. Native selection results are
still data: Script's `false` and HTTP's normal status branches do not become thrown
errors automatically. For HTTP specifically, enabling common retries turns transient
HTTP statuses into typed failures and bypasses its legacy transport retry to avoid
multiplying attempts. Other HTTP responses retain success/error/always routing.

A retry repeats the **whole node** (including tools/sends already performed); it is
opt-in and is not an exactly-once delivery mechanism. Existing node-specific
credential, budget and observation paths retain their runtime context on each call.
Attempt failures and waits are exposed as bounded SSE history and structured logs;
this is not a new persisted workflow execution ledger.

With retry enabled, `attempt_started`, `attempt_failed` and `retrying` share the
invocation's `execution_id`. Final events include the attempt count; `error_handled`
keeps the failed node visibly failed while allowing the run to complete through
its selected recovery policy. Such a result cannot be pinned as a successful output.
**Stop run** aborts the stream, and generation guards prevent a late Save response
or an old stream callback from starting or overwriting a newer run.

Verified: transient success/exhaustion, single-attempt default, permanent errors,
authorization failures, cancellation during backoff, Retry-After bounds, branch
skipping/routing, inactive failure ports on success, and exactly three HTTP attempts
when both the old retry flag and a three-attempt common policy are present. UI tests
cover attempt correlation and handled failures; Chrome mock checks cover Settings
persistence, the live failure handle, attempt history, light/dark/mobile and stopping
before Save finishes. No migration is needed.

**Value:** recover from transient API failures without rerunning the whole workflow.

- Define per-node attempt limit, backoff, deadline and stop/continue/error-output behavior.
- Preserve cancellation, workspace identity and usage/trace attribution per attempt.
- Do not retry SMTP, payments or other side effects blindly; distinguish retryable
  failures and document idempotency behavior.
- Acceptance: a controlled 429 retries within its deadline, while an exhausted error
  reaches an explicit error branch and the run records each attempt.

### 4. Data-operation nodes

Implemented as five registered nodes, with native forms, readable cards, the common
Input/Output/Settings inspector, partial execution, retry/failure routing and pinning
(outside Loop contexts). Edit Fields, Filter and Aggregate appear under **Data**;
Switch and Merge under **Flow Control**. They are pure JSON transformations registered
as non-host capabilities; adding a node does not grant an explicit workspace policy
permission automatically. No migration.

| Node | Input/configuration | Output |
|---|---|---|
| Edit Fields | `data`: object or array of objects. `keep_input`; ordered `fields` with `name`, `source: value/path`, JSON Pointer `path` or typed literal. Assignments read the original item independently. | Same object/array shape, with top-level fields assigned. `keep_input=false` projects only the configured fields. |
| Filter | `data`, optional `items_path`; 1–32 conditions with `match: all/any`. A scalar/object is treated as one item. | `data`: matching items, always an array, including `[]`. |
| Switch | Whole `data` payload; `match_mode: first/all`; 1–16 rules with stable `case_<id>` names and typed conditions. | Original payload under each matching case port, or `fallback` when none match. Reordering changes priority, not IDs. |
| Merge | One producer per `left`/`right` input; arrays or singleton items. `mode: append/zip/join`; join uses item-relative `left_key`/`right_key` and `join_type: inner/left/outer`. | Array. Append preserves left-then-right order. Zip/key joins emit `{left,right}` pairs, with null for unmatched sides. |
| Aggregate | Array selected by `items_path`; optional per-item `field_path`; `operation: collect/count/sum/average/min/max`. | `data: {value,count}`. Empty collect is `[]`; count/sum are 0; average/min/max are null. |

Paths are RFC 6901 JSON Pointers. Empty path selects the current whole item/payload;
`~1` and `~0` escape slash and tilde in field names. Conditions support equals/not
equals, numeric comparisons, contains, exists/not exists, and empty/null. Types do
not coerce: number `0`, text `"0"`, false and null are distinct; missing paths only
match **Does not exist**. Exists includes explicit null. Numeric operations reject
missing/non-numeric values and overflow rather than silently dropping them.

Merge joins only non-null scalar keys; absent/null keys do not match. Duplicate keys
produce every matching pair, in stable left/right input order. Pair envelopes avoid
silent field overwrites and can be projected with Edit Fields afterward. Collection
inputs/results are bounded at 10,000 items, including join expansion; Edit Fields
supports 64 assignments and literals up to 32 KiB.

**Execution boundary:** these nodes operate inside one invocation. Merge is not a
barrier collecting results from concurrent Loop executions; Aggregate summarizes
an array it receives, not a hidden history of prior calls. The engine rejects
independent upstream Loop contexts at a Merge rather than returning misleading
partial joins. A single/nested Loop context may merge per item with static data.
For ordinary DAG branches, all active predecessors are available in topological
order. An inactive branch contributes an empty side; both inactive skips the node.
An active empty array is real data, so Filter → Aggregate can return zero.

Switch labels and order can change without reconnecting wires. Removing a connected
case requires removing its edge first; the editor blocks Apply/Save and the engine
rejects stale output IDs. The new data nodes also reject multiple producers on the
same input port instead of allowing last-write-wins overwrites.

Verification: typed/falsy/null/missing conditions; original-input assignment and
literal isolation; first/all/fallback Switch routing; append/zip/inner/left/outer
Merge including duplicate/unmatched keys; empty aggregation; limits/cancellation;
metadata/capability registration; Filter → Aggregate empty results; Switch branches
rejoining once; stale/duplicate input edges; independent and nested Loop scopes.
Chrome mock-API checks added, configured and saved all five types, connected a
stable Switch case after reordering, and verified the connected-case deletion guard
plus light/dark/mobile layouts. Persistent full fan-out collection is not implemented.

These nodes establish the data-transformation foundation before expanding the app
integration catalog. The verified acceptance case is a branched workflow rejoining
deterministically, including an inactive branch or an active empty array. A global
fan-out collection barrier remains an explicit future engine feature.

### 5. Durable Wait / approval / resume

Implemented with migration **64**, `workflow_executions`, and an explicit checkpoint
format (version 1). A workflow containing a reachable `wait` node is queued durably
by manual API/stream runs, `workflow_run`, cron and webhook entrypoints. The selected
top-level graph, inputs and entry IDs are snapshotted at launch. Editing that workflow
later does not replace the stored graph. Referenced providers, agents, skills and
child workflows still resolve live with their existing permission checks; this is
not an immutable archive of every external dependency.

**Wait / Approval** has two modes:

- `duration`: release the worker and resume after `seconds` (1 second–30 days).
- `approval`: await the initiator or an authorized workspace/installation administrator;
  `expires_seconds` defaults to 7 days, with the same 30-day maximum. Reject/cancel
  ends the run, and an elapsed approval becomes `expired`. There is no public bearer
  resume URL or automatic approval tool.

The editor's **Saved runs** panel lists up to 50 records (pending first), polls while
open, shows approval instructions and captured wait data, and offers approve/reject/
cancel. Closing the editor does not cancel a durable run. Other workspace members
cannot inspect another account's execution data; administrators additionally need
the existing workflow capabilities. Decisions reload authority inside a transaction
and compare the current revision, so duplicate or stale approval requests cannot
approve another wait or run the next segment twice.

### Checkpoint and recovery contract

Before a node executes, the engine durably saves an `in_flight` marker. After a node
finishes it saves its output/routing result and clears the marker. Skipped nodes and
registry outputs are retained too. Waiting saves the unchanged payload and returns;
no sleeping goroutine or open browser stream owns the wait.

Two workers per server claim rows with `FOR UPDATE SKIP LOCKED`; a random worker ID,
revision CAS and a 120-second lease fence all checkpoint writes. Leases renew every
15 seconds. Polling is every 5 seconds, with local launch/decision wakeups. Due timers
queue automatically. Disabled launch-source features leave queued work unclaimed;
live identity, membership, policy, resource and feature admission run again when a
segment resumes. The original initiator is retained; an approver never lends their
authority. Machine service bindings remain independent of browser logout.

On an expired lease, a safe checkpoint with no in-flight step is requeued. An
interrupted in-flight step becomes **blocked** rather than automatically repeating
an external side effect that may already have happened. This is restart-safe
checkpointing, **not exactly-once delivery to arbitrary external services**. Native
node retry settings still repeat a node only when explicitly configured.

### APIs and supported boundary

- `GET /api/v1/workflows/{id}/executions`: scoped saved-run summaries.
- `GET /api/v1/workflows/{id}/executions/{execution}`: outputs/wait data and in-flight ID.
- `POST /api/v1/workflows/{id}/executions/{execution}/{approve|reject|cancel}` with
  `{"revision": N}`: guarded transition. A stale decision returns 409.
- Nonstreaming launches return 202/`queued` when Wait requires durable execution,
  even if sync was requested. Streaming launches emit `durable_started` with the run
  ID and close; the client follows the persistent record.

The initial checkpoint format supports up to 240 reachable nodes in a **non-Loop
DAG**. Wait inside a nested workflow call is refused by the ordinary child engine;
partial/pinned tests may stop before Wait but cannot cross it. These restrictions
are explicit instead of pretending a single-frame checkpoint can resume a fan-out
stack. Data must be portable JSON, with 8 MiB bounds on the launch payload and full
checkpoint. Use artifact references for larger data; those artifacts must be on
storage reachable by whichever replica resumes the job. Durable webhook payloads
use a text `body` and, for valid JSON, `body_json`, rather than a live body reader.

Completed/blocked/cancelled/expired records are swept after 30 days. Waiting records
are retained until a decision, timer or expiry. Workspace/account deletion cleans
their owned execution records. A cancelled record fences further checkpoint writes;
an already-admitted external request may need time to observe cancellation.

Verification used a real temporary PostgreSQL instance: concurrent worker claims,
duplicate approvals, expired approvals, timer scheduling, cancellation fencing,
safe versus uncertain crash recovery, private owner scope and stale/forged role
metadata, workspace/account cleanup, a fresh server resuming the original graph
without repeating earlier HTTP calls, original-session revocation, and a machine
timer surviving browser logout. Workflow, postgres and server package suites passed
with `-race`. Chrome mock-API checks exercised the saved-run review/approval flow and
light/dark/mobile layouts.

The connected-chain browser regression also found and fixed a Kaykay identity
collision in data nodes: it keys handles by node+ID, not direction. Edit Fields,
Filter, Aggregate and Wait now use internal `data_in` input handles while the graph
wire format remains `target_handle: "data"`. Editor load/save, Input mapper and AI
edge creation use the same adapter, preserving already-saved graphs.

### 6. Operation catalog and starter workflows

Use existing connector/skill metadata where possible to expose deterministic action
nodes with a connection picker and typed input/output definitions. Start with a few
real workflows (HTTP → transform → LLM → output; scheduled summary; document analysis).
Treat a tool execution node as distinct from an agent that may choose a tool.

## AT code references

- `_ui/src/pages/WorkflowEditor.svelte`: graph editing, inspector, versions and runs.
- `_ui/src/lib/workflow/node-definitions.ts`: defaults, palette and dimensions.
- `_ui/src/lib/components/workflow/NodePalette.svelte`: searchable step library.
- `_ui/src/lib/components/workflow/TemplateProps.svelte`: current template/port UX.
- `_ui/src/lib/store/workflow-run.svelte.ts`: live node output state.
- `internal/service/workflow/engine.go`: entry reachability, DAG execution and node events.
- `internal/service/workflow/nodes/http-request.go`: existing HTTP-specific retry.
- `internal/service/workflow/nodes/`: implemented node types.

## Sources

1. [n8n: Work with nodes](https://docs.n8n.io/build/understand-workflows/workflow-components/work-with-nodes.md)
2. [n8n: UI mapper](https://docs.n8n.io/build/work-with-data/reference-data/use-the-ui-mapper.md)
3. [n8n: Types of executions](https://docs.n8n.io/build/understand-workflows/understand-executions/types-of-executions.md)
4. [n8n: Pin and mock data](https://docs.n8n.io/build/work-with-data/pin-and-mock-data.md)
5. [n8n: Handle errors gracefully](https://docs.n8n.io/build/flow-logic/handle-errors-gracefully.md)
6. [n8n: Merge data](https://docs.n8n.io/build/flow-logic/merge-data.md)
7. [n8n: Wait](https://docs.n8n.io/build/flow-logic/wait.md)
8. [Kaykay 0.2.1 package](https://www.npmjs.com/package/kaykay/v/0.2.1) — verified against the installed package types and source, including Canvas coordinate helpers and FlowState history/connection APIs.
