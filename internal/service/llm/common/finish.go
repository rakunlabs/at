package common

import "github.com/rakunlabs/at/internal/service"

// ReconcileToolCallFinish makes a response's completion flags agree with its
// tool calls.
//
// The agentic loops gate tool execution on `resp.Finished ||
// len(resp.ToolCalls) == 0`, so a response that carries pending tool calls
// while still claiming to be finished has its tools silently dropped and the
// run ends on whatever text came with them (often nothing). Upstream cannot be
// trusted to prevent that: OpenAI itself reports `tool_calls`, but many of the
// OpenAI-compatible servers AT targets — Ollama, LM Studio, vLLM, and several
// hosted gateways — return `finish_reason: "stop"` alongside a populated
// `tool_calls` array.
//
// Adapters must call this after they have finished collecting tool calls, and
// only for calls that are safe to execute. A truncated or filtered response
// (`length` / `content_filter`) must have dropped its partial tool calls
// before reaching here, so that stop reason is preserved as-is.
func ReconcileToolCallFinish(resp *service.LLMResponse) {
	if resp == nil || len(resp.ToolCalls) == 0 {
		return
	}
	resp.Finished = false
	resp.FinishReason = "tool_calls"
}
