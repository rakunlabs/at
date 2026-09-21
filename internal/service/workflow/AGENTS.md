# internal/service/workflow — Workflow Engine

## Purpose

DAG-based workflow execution engine. Parses workflow graphs, topologically sorts nodes, runs them concurrently with fan-out/selection routing. Inspired by worldline-go/chore.

## Key Files

- `engine.go` — `Engine.Run()`: reachableNodes → parseGraph → topoSort → execute. Handles concurrent fan-out branches, early output channel for sync API responses
- `node.go` — Core interfaces: `Noder`, `NodeResult`, `NodeResultSelection`, `NodeResultFanOut`, `Registry`, lookup function types
- `scheduler.go` — Cron scheduler: loads enabled cron triggers, builds hardloop jobs, calls `Engine.Run` per tick. Supports `Reload()` for dynamic trigger changes
- `handler.go` — `ExecuteJSHandler` and `ExecuteBashHandler` — shared helpers for nodes that run JS or shell
- `goja.go` (507 lines) — Goja JS VM setup: registers helpers (toString, jsonParse, btoa/atob, getVar, httpGet/Post), BodyWrapper for request bodies

## Execution Model

```
Engine.Run(ctx, graph, inputs)
  → reachableNodes(graph)     // prune unreachable nodes
  → parseGraph(nodes, edges)  // create Noder instances via factory, validate each
  → topoSort(nodeStates)      // dependency-ordered execution list
  → for each node in order:
      gatherInputs(node)      // collect upstream outputs
      node.Run(ctx, reg, inputs) → NodeResult
        → NodeResult           // pass data to all downstream
        → NodeResultSelection  // route to specific output ports by index
        → NodeResultFanOut     // spawn goroutine per item on port 0
```

## Registry

`Registry` holds shared state during execution:
- `ProviderLookup func(key) (LLMProvider, defaultModel, error)`
- `SkillLookup func(nameOrID) (*Skill, error)`
- `VarLookup func(key) (string, error)` / `VarLister func() (map[string]string, error)`
- `NodeConfigLookup func(id) (*NodeConfig, error)`
- `AgentLookup func(ctx, id) (*Agent, error)`
- `ConnectionLookup func(ctx, id) (*Connection, error)` — multi-instance OAuth/token credential resolution
- `RunInputs map[string]any` — original trigger inputs
- Thread-safe error collection and output aggregation

## Named Connections (multi-instance credentials)

Agents can bind named `Connection` rows (see `internal/service/types-connection.go`) by provider. At runtime, `agent-call` nodes wrap `VarLookup`/`VarLister` with `WrapVarLookupWithConnections` / `WrapVarListerWithConnections` (see `connection_resolver.go`) before executing tool handlers. Provider-scoped keys (`<provider>_client_id`, `<provider>_client_secret`, `<provider>_refresh_token`, `<provider>_api_key`) resolve through the cascade:

1. Per-skill connection override (from `SkillRef.Connections`)
2. Per-agent default binding (from `AgentConfig.Connections`)
3. Global variable (via the base `VarLookup`)

This lets two agents use two different YouTube accounts while sharing the same skill JS handler code — the handler stays `getVar('youtube_refresh_token')` and the resolver picks the right account.

## Node Contract

### Input mapping and editor snapshots

`node.Data.input_mappings` optionally maps target input port names to RFC 6901
JSON Pointers into that node's gathered inputs (for example
`{"prompt":"/prompt/customer/message"}`). `executeNode` resolves every mapping
against the original inputs before `Noder.Run`; mappings never cascade. Invalid
paths/targets fail before side effects. Existing nodes without mappings are unchanged.
See `input-mapping.go` and `input-mapping_test.go`.

Node SSE events have `execution_id` per invocation, including fan-out items.
Started events snapshot `inputs` and optionally `resolved_inputs`; completed events
snapshot output `data`. Each preview is detached JSON capped at 64 KiB, with explicit
`*_omitted` flags for large/non-JSON values. Execution data is not truncated. UI pairs
input/output by invocation ID and shows the latest-started fan-out invocation, not a
combined history. These are session-local inspector snapshots, not pins/checkpoints.

### Manual test runs and output pins

`Engine.RunTest` (`test-run.go`) accepts a target node and optional request-only
fixtures. It slices to the target's ancestor subgraph before validation; existing
conditional/fan-out routing still decides which steps execute. The target always
runs even when its own output was pinned. Pins skip only that node, not its ancestors.

Completed SSE events expose `pin_signature`, `result_kind` and `selection`. The
signature binds upstream executable configuration/wiring, entry IDs and run inputs,
not layout or downstream edits. Test preflight rejects stale/oversized pins before
side effects. Selection fixtures preserve active ports; loop nodes and their
descendants have no pin signature because a single preview is not the whole stream.
Pins are bounded to 32 nodes, 64 KiB per output and 512 KiB total. `Run` clears any
test state on its run-local engine copy; child engines do not inherit fixtures.
Only the streaming HTTP endpoint accepts explicit `test` options. Cron/webhooks,
normal API runs and saved graph JSON cannot activate pins. Regressions:
`test-run_test.go`, `nodes/test-run_test.go`, server `workflow-test-run_test.go`.

### Common retries and failure routing

Optional `node.Data.execution` is parsed centrally by `ParseNodeExecutionPolicy`
(`execution-policy.go`): `max_attempts` 1–5 (default 1), `retry_delay_ms` 0–30000
(default 1000), `backoff` fixed/exponential, `on_error` stop/continue/error_output.
Absent settings retain fail-fast single-attempt behavior. Only typed transient
HTTP/network errors retry; delays inherit cancellation, cap exponential backoff at
30s and honor Retry-After up to 5m (longer hints stop retries). Every attempt checks
node execution authority. Retries repeat the entire node, not just its last LLM call.

`continue` skips the failed branch without fabricating output; `error_output` selects
reserved `__error` only. Its payload carries node identity, message, attempts and
input. This port is separate from native HTTP `error` and must be explicitly enabled
before wiring. Native selection results keep their existing semantics. HTTP common
retry disables legacy HTTP retry, and converts retryable response statuses into
typed errors so two retry layers cannot multiply requests. Runtime authority errors,
mapping/configuration errors, parent cancellation and `ErrStopBranch` are not turned
into handled failures. The same path runs main and fan-out nodes; test pins bypass
execution/retries and handled failures never receive a pin signature.

Retry SSE events share one invocation ID; `error_handled` records the failed node
without failing the whole workflow. Attempts are bounded in the UI and reported in
logs. Regression: `execution-policy_test.go`, `nodes/execution-policy_test.go`,
`_ui/tests/workflow-data.test.mjs`.

### Data operations and merge boundaries

`edit_fields`, `filter`, `switch`, `merge` and `aggregate` are pure JSON nodes with
non-host capability registrations. No database migration or JavaScript evaluation.
The editor uses shared `DataOperationNode` / `DataOperationProps` components and
stable Switch rule IDs, with an Apply guard for deleted-but-connected cases.

`data-node-validation.go` and `parseGraph` validate the new nodes' single-producer
input contracts, reject stale Switch output IDs, and prevent Merge from claiming
to join independent fan-out contexts. Merge/Aggregate work on arrays **inside the
current invocation**: there is no cross-Loop accumulation barrier. For ordinary DAG
branches, topological execution supplies all active predecessors; inactive sides
are absent, active empty lists remain `[]`, and both inactive sides skip the Merge.
Nested Loop contexts may merge per inner invocation; independent Loop streams must
be merged as arrays before fan-out. Old node behavior is not rewritten.

The shared JSON Pointer syntax validator now checks all segments before traversal,
so malformed paths fail even if an earlier field is missing. Regression:
`nodes/data-nodes_test.go`, `_ui/tests/workflow-data-operations.test.mjs`.

### Durable Wait and checkpoints

`wait` returns `NodeResultWait`; it never sleeps. `Engine.RunDurable` in `durable.go`
checkpoints a serial/non-Loop DAG through a persistence callback. Every external
step is preceded by an acknowledged `in_flight` marker; successful results,
selection routes, skipped nodes and registry outputs are saved afterward. Resuming
installs the waiting node's stored output and skips completed nodes, including their
validation/lookups. Node IDs must be unique/nonempty to make the intent marker
unambiguous. Checkpoint version 1 has no fan-out/nested-call stack; Loop is refused
before effects, and normal child engines reject graphs containing Wait.

The server queues reachable Wait graphs through API, workflow_run, cron and webhook
entrypoints into migration 64's `workflow_executions`. The root graph is frozen;
referenced resources still resolve live. Workers rebind stored provenance and
revalidate authority/feature flags, never adopting an approver's identity. SQL lease
and revision guards serialize claims/decisions. Expired workers with a safe checkpoint
are requeued; a nonempty in-flight marker blocks replay because effects are uncertain.
This does not promise exactly-once external delivery. Approval expiry/rejection ends
the run. Saved-run data is owner-scoped with workspace/installation admin access,
plus workflow capabilities. Terminal retention is 30 days; payload/checkpoint cap
8 MiB; graph cap 240 reachable nodes; wait/approval expiry cap 30 days. PortableJSON
refuses readers, raw bytes, structs, functions, cycles and invalid UTF-8 rather than
silently encoding implementation state. Durable webhook bodies are text plus optional
body_json. Multi-replica artifact paths need shared persistent storage.

Regression: `nodes/durable_test.go`, postgres `workflow-executions_test.go`, server
`workflow-durable_test.go`. UI `ports.ts` separates Kaykay's `data_in` identity from
the persisted `data` target port for bidirectional data/Wait nodes.

```go
type Noder interface {
    Type() string
    Validate(ctx context.Context, reg *Registry) error
    Run(ctx context.Context, reg *Registry, inputs map[string]any) (NodeResult, error)
}
```

Registration: `workflow.RegisterNodeType(typeName, factory)` called from `init()` in nodes/ package.

## Key Patterns

- `ErrStopBranch` — sentinel error to gracefully terminate a branch without propagating
- Fan-out: `runFanOutBranch` spawns goroutines per item with WaitGroup
- Early output: first Output node fires sends result to `earlyOutput` channel for sync API
- JS sandboxing: Goja VM with helpers; panics map to JS type errors (`vm.NewTypeError`)
- Bash handler: `/bin/sh -c` with variable injection from `VarLister`
