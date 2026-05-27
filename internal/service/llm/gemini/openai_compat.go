package gemini

import (
	"encoding/json"
	"strings"
)

// marshalRequestWithExtraBody marshals the typed Gemini request, then
// merges extra_body into the resulting JSON object so callers can inject
// fields like `safetySettings`, `cachedContent`, `groundingConfig` that
// aren't surfaced as first-class struct fields.
//
// Keys in extra_body overwrite the typed fields when they collide. Returns
// the same bytes as json.Marshal when extra_body is empty.
func marshalRequestWithExtraBody(req *generateContentRequest, extra map[string]any) ([]byte, error) {
	if len(extra) == 0 {
		return json.Marshal(req)
	}
	base, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var merged map[string]any
	if err := json.Unmarshal(base, &merged); err != nil {
		return nil, err
	}
	for k, v := range extra {
		merged[k] = v
	}
	return json.Marshal(merged)
}

// isGeminiBuiltinSearchName reports whether the given tool name should be
// rewritten as the Gemini built-in googleSearch grounding tool instead of
// a function declaration.
func isGeminiBuiltinSearchName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "__google_search", "google_search", "googlesearch", "web_search":
		return true
	}
	return false
}

// translateGeminiToolChoice converts an OpenAI-style tool_choice value
// (string or object) to Gemini's functionCallingConfig shape.
//
// OpenAI:
//
//	"none" | "auto" | "required"
//	{type:"function", function:{name:"X"}}
//
// Gemini:
//
//	mode: AUTO | ANY | NONE
//	allowedFunctionNames: [...]  (only honoured when mode == ANY)
func translateGeminiToolChoice(v any) *functionCallingConfig {
	switch x := v.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "none":
			return &functionCallingConfig{Mode: "NONE"}
		case "auto":
			return &functionCallingConfig{Mode: "AUTO"}
		case "required", "any":
			return &functionCallingConfig{Mode: "ANY"}
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
			return &functionCallingConfig{Mode: "ANY", AllowedFunctionNames: []string{name}}
		case "none":
			return &functionCallingConfig{Mode: "NONE"}
		case "auto":
			return &functionCallingConfig{Mode: "AUTO"}
		case "any", "required":
			return &functionCallingConfig{Mode: "ANY"}
		}
	}
	return nil
}

// geminiResponseFormat maps OpenAI's response_format value to Gemini's
// (responseMimeType, responseSchema) pair.
//
// json_object → application/json, no schema
// json_schema → application/json + schema
//
// Returns ("", nil) when the format is unknown or unset.
func geminiResponseFormat(rf map[string]any) (string, any) {
	if len(rf) == 0 {
		return "", nil
	}
	t, _ := rf["type"].(string)
	switch t {
	case "json_object":
		return "application/json", nil
	case "json_schema":
		wrap, _ := rf["json_schema"].(map[string]any)
		if wrap == nil {
			return "application/json", nil
		}
		schema, _ := wrap["schema"].(map[string]any)
		return "application/json", schema
	}
	return "", nil
}
