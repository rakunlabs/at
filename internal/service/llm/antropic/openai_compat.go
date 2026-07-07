package antropic

import (
	"encoding/json"
	"fmt"
	"strings"
)

// translateAnthropicToolChoice converts an OpenAI-style tool_choice value
// (string or object) to Anthropic's tool_choice shape.
//
// OpenAI vocabulary:
//
//	"none"     → tools disabled for this call
//	"auto"     → model decides (default)
//	"required" → must call at least one tool
//	{type:"function", function:{name:"X"}} → must call tool X
//
// Anthropic vocabulary:
//
//	{type:"none"}            → no tools
//	{type:"auto"}            → model decides
//	{type:"any"}             → must call any tool
//	{type:"tool", name:"X"}  → must call tool X
//
// Returns nil when the input doesn't map to a recognised choice (callers
// should leave tool_choice unset in that case).
func translateAnthropicToolChoice(v any) map[string]any {
	switch x := v.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "none":
			return map[string]any{"type": "none"}
		case "auto":
			return map[string]any{"type": "auto"}
		case "required", "any":
			return map[string]any{"type": "any"}
		}
	case map[string]any:
		t, _ := x["type"].(string)
		switch strings.ToLower(t) {
		case "function":
			fn, _ := x["function"].(map[string]any)
			if fn == nil {
				return nil
			}
			name, _ := fn["name"].(string)
			if name == "" {
				return nil
			}
			return map[string]any{"type": "tool", "name": name}
		case "none", "auto", "any":
			out := map[string]any{"type": strings.ToLower(t)}
			return out
		case "tool":
			// Already Anthropic shape — pass through.
			name, _ := x["name"].(string)
			if name == "" {
				return nil
			}
			return map[string]any{"type": "tool", "name": name}
		}
	}
	return nil
}

// isAnthropicBuiltinSearchName reports whether a tool name is the synthetic
// marker that activates Anthropic's server-side web_search tool. Mirrors the
// gemini adapter's isGeminiBuiltinSearchName so one `web_search` tool
// declaration enables native internet search on both providers.
func isAnthropicBuiltinSearchName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "web_search", "__web_search", "websearch":
		return true
	}
	return false
}

// anthropicResponseFormatInstruction returns a system-prompt suffix that
// instructs the model to emit JSON when the caller passed an OpenAI
// response_format value. Returns "" when no response_format was requested
// or the type isn't a known JSON variant.
//
// Anthropic has no native equivalent to OpenAI's response_format, so this
// is a best-effort translation. Production callers wanting strict
// structured output on Anthropic should use the dedicated tool-call
// grammar pattern documented in the Anthropic SDK.
func anthropicResponseFormatInstruction(rf map[string]any) string {
	if len(rf) == 0 {
		return ""
	}
	t, _ := rf["type"].(string)
	switch t {
	case "json_object":
		return "Respond with a single JSON object and nothing else. Do not wrap the JSON in markdown fences or prose."
	case "json_schema":
		schemaWrap, _ := rf["json_schema"].(map[string]any)
		if schemaWrap == nil {
			return "Respond with a single JSON object and nothing else."
		}
		schema, _ := schemaWrap["schema"].(map[string]any)
		if schema == nil {
			return "Respond with a single JSON object and nothing else."
		}
		buf, err := json.Marshal(schema)
		if err != nil {
			return "Respond with a single JSON object and nothing else."
		}
		name, _ := schemaWrap["name"].(string)
		if name == "" {
			name = "Response"
		}
		return fmt.Sprintf(
			"Respond with a single JSON object named %q that conforms exactly to this JSON schema. Do not wrap the JSON in markdown fences or prose.\n\nSchema:\n%s",
			name, string(buf),
		)
	}
	return ""
}
