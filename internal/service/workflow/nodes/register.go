// Package nodes registers all built-in workflow node types.
//
// Each file in this package defines a node type and registers it via
// an init() function that calls workflow.RegisterNodeType. Importing
// this package (even as a blank import) triggers all registrations:
//
//	import _ "github.com/rakunlabs/at/internal/service/workflow/nodes"
//
// Registered node types:
//
//   - input          — passes workflow trigger inputs downstream
//   - output         — collects final results into the registry
//   - prompt_template — Go text/template rendering with mustache conversion
//   - llm_call       — sends a prompt to an LLM provider
//   - agent_call     — agentic loop with MCP, skill, and inline tool execution
//   - skill_config   — resource node: outputs skill names for agent_call
//   - mcp_config     — resource node: outputs MCP server URLs for agent_call
//   - memory_config  — resource node: passes memory/context data to agent_call
//   - conditional    — if/branch via JavaScript expression (Goja)
//   - loop           — for-each fan-out via JavaScript expression (Goja)
//   - script         — arbitrary JavaScript execution with 3-port routing (Goja)
//   - http_request   — HTTP client node (klient, Go templates, selection routing)
//   - http_trigger   — HTTP webhook trigger (passes request body downstream)
//   - cron_trigger   — Cron schedule trigger (merges static payload with metadata)
//   - exec           — sandboxed shell command execution (/bin/sh -c)
package nodes
