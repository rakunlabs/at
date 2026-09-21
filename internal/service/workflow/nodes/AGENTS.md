# internal/service/workflow/nodes — Node Type Registry

## Purpose

Built-in node types. Each file defines one node type and registers it via `init()` → `workflow.RegisterNodeType(typeName, factory)`.

## Adding a New Node Type

1. Create `my-node.go` in this directory
2. Define struct implementing `workflow.Noder` (Type, Validate, Run)
3. Add `init()` calling `workflow.RegisterNodeType("my_node", factory)`
4. The blank import in `register.go` ensures auto-registration

## Node Registry

| File | Type Name | Purpose |
|---|---|---|
| `input.go` | `input` | Passes workflow trigger inputs downstream |
| `output.go` | `output` | Collects final results into Registry outputs |
| `llm-call.go` | `llm_call` | Sends prompt to LLM provider via ProviderLookup |
| `agent-call.go` | `agent_call` | Agentic loop with MCP servers, skills, inline tools |
| `conditional.go` | `conditional` | JS expression → NodeResultSelection (port routing) |
| `loop.go` | `loop` | JS expression → NodeResultFanOut (parallel branches) |
| `script.go` | `script` | Arbitrary JS execution, 3-port output routing |
| `http-request.go` | `http_request` | HTTP client node with Go templates, selection routing |
| `http-trigger.go` | `http_trigger` | HTTP webhook trigger, passes request body downstream |
| `cron-trigger.go` | `cron_trigger` | Cron schedule trigger, merges static payload + metadata |
| `exec.go` | `exec` | Sandboxed shell execution (`/bin/sh -c`) |
| `email.go` | `email` | SMTP email via NodeConfig-based server settings |
| `template.go` | `template` | Go text/template rendering with mustache conversion |
| `log.go` | `log` | Log data at configurable level, pass through unchanged |
| `skill-config.go` | `skill_config` | Resource node: outputs skill names for agent_call |
| `mcp-config.go` | `mcp_config` | Resource node: outputs MCP server URLs for agent_call |
| `edit-fields.go` | `edit_fields` | Project/set top-level fields on one object or each object in an array |
| `filter.go` | `filter` | Typed AND/OR predicates over items; always emits an array |
| `switch.go` | `switch` | First/all matching rules; stable `case_<id>` handles + fallback |
| `merge.go` | `merge` | Append, zip or scalar-key join over two inputs in one invocation |
| `aggregate.go` | `aggregate` | Collect/count/sum/average/min/max over an array |
| `wait.go` | `wait` | Durable timer/approval boundary; returns NodeResultWait, never sleeps |

The five data-operation nodes are registered as non-host execution capabilities in
their own `init()` functions. They use no JS VM, network, secrets or shell. Explicit
workspace node grants still apply. `data-values.go` shares typed literals, predicates
and collection helpers; paths use `workflow.ResolveJSONPointer` / `ValidateJSONPointer`.
Missing, null, false and zero are distinct. Configuration literals store
`value_type` plus a string `value`; only JSON/number/boolean types parse that string.

Input/output is `data` except Merge's `left`/`right` inputs and Switch's named outputs.
Edit Fields preserves object/array shape and reads assignments from the original
item independently. Filter always emits `[]` when nothing matches (it does not stop
the branch). Switch routes the unchanged payload. Merge emits an array; zip/key-join
rows are `{left,right}` pairs so fields cannot collide. Join keys are non-null scalars,
typed and exact; duplicate keys produce all matching pairs in stable input order.
Aggregate emits `data: {value,count}`; empty collect/count/sum are `[]`/0/0 and empty
average/min/max are null. Missing/non-numeric aggregate values fail, never disappear
from a total. Inputs/results are bounded at 10,000 collection items; assignments at
64, conditions at 32, Switch rules at 16 and literals at 32 KiB. Long loops check ctx.

These are per-invocation array operations, **not global fan-out collectors**.
`validateDataNodeInputs` enforces one producer per declared input and rejects a Merge
whose upstream Loop contexts are independent. Nested Loop contexts and static inputs
can merge within each invocation. Existing arbitrary-node OR-join behavior is unchanged.
Regression: `data-nodes_test.go`.

## Patterns

- JS nodes (conditional, loop, script) use `ExecuteJSHandler` from `handler.go` with Goja VM
- Nodes access providers via `reg.ProviderLookup`, skills via `reg.SkillLookup`
- External configs (email SMTP) resolved via `reg.NodeConfigLookup`
- Error prefix convention: `"node_type: detail"` (e.g. `"http_request: failed to execute"`)
- `register.go` is package doc only — no code, just the blank import trigger point
