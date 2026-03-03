# internal/service/workflow/nodes — Node Type Registry

## Purpose

21 built-in node types. Each file defines one node type and registers it via `init()` → `workflow.RegisterNodeType(typeName, factory)`.

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
| `memory-config.go` | `memory_config` | Resource node: passes memory/context data to agent_call |
| `git-fetch.go` | `git_fetch` | Clone/pull git repo, output repo path + HEAD SHA |
| `git-diff.go` | `git_diff` | Detect changed files since last sync, read contents |
| `rag-ingest.go` | `rag_ingest` | Ingest files into RAG collection, update sync state |
| `rag-search.go` | `rag_search` | Query RAG collection for relevant documents |

## Patterns

- JS nodes (conditional, loop, script) use `ExecuteJSHandler` from `handler.go` with Goja VM
- Nodes access providers via `reg.ProviderLookup`, skills via `reg.SkillLookup`
- External configs (email SMTP) resolved via `reg.NodeConfigLookup`
- Error prefix convention: `"node_type: detail"` (e.g. `"http_request: failed to execute"`)
- `register.go` is package doc only — no code, just the blank import trigger point
