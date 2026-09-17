package gemini

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/rakunlabs/at/internal/service"
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

// Token budgets used when an OpenAI-style reasoning_effort has to be
// expressed as a Gemini 2.5 thinkingBudget.
const (
	thinkingBudgetLow    = 2048
	thinkingBudgetMedium = 8192
	thinkingBudgetHigh   = 24576
	// thinkingBudgetDynamic (-1) tells Gemini 2.5 to pick its own budget.
	thinkingBudgetDynamic = -1
)

// usesThinkingLevel reports whether the model takes Google's coarse
// `thinkingLevel` enum instead of a `thinkingBudget` token count.
//
// Gemini 3 replaced thinkingBudget with thinkingLevel and rejects the old
// field with 400 INVALID_ARGUMENT, while Gemini 2.5 only understands
// thinkingBudget. The field therefore has to be selected from the model
// generation rather than sent speculatively or sent as both.
func usesThinkingLevel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	// Accept fully qualified names such as "models/gemini-3-pro-preview" and
	// Vertex publisher paths.
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	rest, ok := strings.CutPrefix(m, "gemini-")
	if !ok {
		return false
	}
	digits := rest
	if i := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		digits = rest[:i]
	}
	major, err := strconv.Atoi(digits)
	return err == nil && major >= 3
}

// thinkingLevelForBudget expresses an explicit token budget as the nearest
// Gemini 3 thinking level. Gemini 3 exposes no token-level control, so an
// explicit budget can only be honoured approximately; 0 means "think as
// little as possible".
func thinkingLevelForBudget(budget int) string {
	switch {
	case budget == 0:
		return "MINIMAL"
	case budget <= thinkingBudgetLow:
		return "LOW"
	case budget <= thinkingBudgetMedium:
		return "MEDIUM"
	default:
		return "HIGH"
	}
}

// geminiThinkingConfig derives generationConfig.thinkingConfig from the
// request options for the given model.
//
// An explicit Thinking block wins over reasoning_effort (matching the
// Anthropic adapter). Returning nil leaves the model's own default depth
// untouched, which is also the behaviour when no thinking option is set at
// all — so this never changes requests that did not ask for thinking.
func geminiThinkingConfig(model string, opts *service.ChatOptions) *thinkingConfig {
	if opts == nil {
		return nil
	}

	budget := 0
	switch {
	case opts.Thinking != nil && opts.Thinking.Type == "disabled":
		budget = 0
	case opts.Thinking != nil && opts.Thinking.Type == "enabled":
		budget = opts.Thinking.BudgetTokens
		if budget <= 0 {
			// 0 here means "provider default" in service.ThinkingConfig, which
			// for Gemini is the dynamic budget rather than thinking disabled.
			budget = thinkingBudgetDynamic
		}
	case opts.ReasoningEffort != "":
		switch opts.ReasoningEffort {
		case "low":
			budget = thinkingBudgetLow
		case "medium":
			budget = thinkingBudgetMedium
		case "high":
			budget = thinkingBudgetHigh
		default:
			// Unsupported effort for this provider; leave the model default.
			return nil
		}
	default:
		return nil
	}

	// includeThoughts is what actually makes Gemini emit `thought` parts. It
	// is pointless when thinking is switched off.
	cfg := &thinkingConfig{IncludeThoughts: budget != 0}
	if usesThinkingLevel(model) {
		cfg.ThinkingLevel = thinkingLevelForBudget(budget)
		if budget == thinkingBudgetDynamic {
			// Dynamic has no thinkingLevel equivalent; omit the field so the
			// model keeps its own default depth.
			cfg.ThinkingLevel = ""
		}
		return cfg
	}
	cfg.ThinkingBudget = &budget
	return cfg
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
		// The responseSchema goes through the same strict Gemini validator as
		// tool declarations, so it must be sanitized identically — otherwise
		// $ref/additionalProperties/exclusiveMinimum/etc. trigger a 400.
		if sanitized := service.SanitizeSchemaForGemini(schema); sanitized != nil {
			return "application/json", sanitized
		}
		return "application/json", nil
	}
	return "", nil
}
