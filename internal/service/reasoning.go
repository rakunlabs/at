package service

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// ReasoningEffortValidator is an optional adapter guard for agent reasoning effort.
// Support here describes the adapter, not model capability: upstream models may
// still reject an effort. Empty effort leaves the provider default unchanged.
type ReasoningEffortValidator interface {
	ValidateReasoningEffort(effort string) error
}

// ReasoningEffortLevels is the whole reasoning-effort vocabulary in ascending
// order. It is the union of the provider vocabularies: OpenAI uses all seven,
// Anthropic's output_config.effort uses low..max, and Gemini's thinking levels
// use minimal..high.
var ReasoningEffortLevels = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

// ValidateReasoningEffort checks the exact, case-sensitive configuration vocabulary.
func ValidateReasoningEffort(effort string) error {
	if effort == "" || slices.Contains(ReasoningEffortLevels, effort) {
		return nil
	}

	return fmt.Errorf("invalid reasoning_effort %q: expected empty or one of %s", effort, strings.Join(ReasoningEffortLevels, ", "))
}

// ProviderReasoningEfforts lists the efforts an adapter can express, not what
// a particular model accepts (see ModelReasoningEfforts). Empty means the
// adapter has no reasoning-effort mapping.
func ProviderReasoningEfforts(providerType string) []string {
	switch providerType {
	case "openai", "azure", "vertex":
		// Forwarded verbatim; the upstream model decides.
		return ReasoningEffortLevels
	case "anthropic":
		// output_config.effort on adaptive models, a thinking budget on the
		// extended-thinking generation.
		return []string{"low", "medium", "high", "xhigh", "max"}
	case "minimax":
		// Anthropic-compatible API with extended-thinking budgets only.
		return []string{"low", "medium", "high"}
	case "gemini", "vertex-gemini":
		// thinkingLevel on Gemini 3+, thinkingBudget on 2.5.
		return []string{"minimal", "low", "medium", "high"}
	}

	return nil
}

// ValidateProviderReasoningEffort checks adapter support, not model capability.
// It neither rewrites model IDs nor maps one effort to another or caps output tokens.
func ValidateProviderReasoningEffort(providerType, effort string) error {
	if err := ValidateReasoningEffort(effort); err != nil {
		return err
	}
	if effort == "" || slices.Contains(ProviderReasoningEfforts(providerType), effort) {
		return nil
	}

	return fmt.Errorf("reasoning_effort %q is not supported by provider type %q", effort, providerType)
}

// ValidateModelReasoningEffortOverride checks an operator-declared effort list
// for one model: known levels, no duplicates, all expressible by the adapter.
// An empty list is valid and declares the model non-reasoning.
func ValidateModelReasoningEffortOverride(providerType string, efforts []string) error {
	seen := make(map[string]bool, len(efforts))
	for _, effort := range efforts {
		if effort == "" {
			return fmt.Errorf("reasoning effort must not be empty")
		}
		if seen[effort] {
			return fmt.Errorf("reasoning effort %q is listed twice", effort)
		}
		seen[effort] = true
		if err := ValidateProviderReasoningEffort(providerType, effort); err != nil {
			return err
		}
	}

	return nil
}

// ModelReasoningEfforts reports the efforts a model accepts, in ascending
// order. An operator override (non-nil, possibly empty) wins over detection.
// known is false when the model is not recognised; callers then fall back to
// ProviderReasoningEfforts for pickers and advertise nothing to catalog
// clients rather than guessing.
func ModelReasoningEfforts(providerType, model string, override []string) (efforts []string, known bool) {
	if override != nil {
		return sortEfforts(slices.DeleteFunc(slices.Clone(override), func(e string) bool {
			return !slices.Contains(ProviderReasoningEfforts(providerType), e)
		})), true
	}

	return DetectModelReasoningEfforts(providerType, model)
}

// DetectModelReasoningEfforts derives efforts from the model family, following
// the providers' published per-model tables. Only named families are
// recognised: an OpenAI-compatible endpoint serving an unfamiliar model is
// reported unknown rather than assumed to reason.
func DetectModelReasoningEfforts(providerType, model string) ([]string, bool) {
	id := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	if id == "" {
		return nil, false
	}

	switch providerType {
	case "anthropic":
		return claudeReasoningEfforts(id)
	case "gemini", "vertex-gemini":
		return geminiReasoningEfforts(id)
	case "vertex":
		// The OpenAI-compatible Vertex endpoint maps reasoning_effort onto
		// Gemini thinking with the OpenAI low/medium/high vocabulary.
		if strings.HasPrefix(id, "gemini-") {
			efforts, known := geminiReasoningEfforts(id)
			return slices.DeleteFunc(efforts, func(e string) bool { return e == "minimal" }), known
		}
		return openAIReasoningEfforts(id)
	case "openai", "azure":
		return openAIReasoningEfforts(id)
	}

	return nil, false
}

func sortEfforts(efforts []string) []string {
	slices.SortFunc(efforts, func(a, b string) int {
		return slices.Index(ReasoningEffortLevels, a) - slices.Index(ReasoningEffortLevels, b)
	})

	return efforts
}

// ─── Anthropic ───

// AnthropicThinkingMode classifies how a Claude model expresses reasoning:
//
//	adaptive        thinking {type: adaptive} + output_config.effort (4.6+;
//	                4.7+ rejects {type: enabled} with a 400)
//	extended_effort extended thinking budget plus output_config.effort (Opus 4.5)
//	extended        extended thinking budget only
//	none            no thinking support
//	""              not a recognised Claude model
func AnthropicThinkingMode(model string) string {
	id := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	id = strings.ReplaceAll(id, ".", "-")

	if strings.Contains(id, "claude-3-7-sonnet") {
		return "extended"
	}
	if legacyClaudeRe.MatchString(id) {
		return "none"
	}
	m := claudeRe.FindStringSubmatch(id)
	if m == nil {
		return ""
	}
	family := m[1]
	if m[2] == "preview" {
		// Claude Mythos Preview: adaptive and extended.
		return "adaptive"
	}
	major, _ := strconv.Atoi(m[2])
	minor := 0
	if m[3] != "" {
		minor, _ = strconv.Atoi(m[3])
	}
	version := major*100 + minor

	switch family {
	case "fable", "mythos":
		return "adaptive"
	case "opus":
		switch {
		case version >= 406:
			return "adaptive"
		case version == 405:
			return "extended_effort"
		default:
			return "extended"
		}
	case "sonnet":
		if version >= 406 {
			return "adaptive"
		}
		return "extended"
	case "haiku":
		if version >= 405 {
			return "extended"
		}
		return "none"
	}

	return ""
}

var (
	// claude-<family>-<major>[-<minor>] where minor has at most two digits,
	// so a trailing 8-digit date snapshot is never read as a version.
	claudeRe       = regexp.MustCompile(`claude-(opus|sonnet|haiku|fable|mythos)-(\d+|preview)(?:-(\d{1,2}))?(?:$|[^0-9])`)
	legacyClaudeRe = regexp.MustCompile(`claude-(?:3|3-5|2|2-1|instant)(?:-|$)`)
)

func claudeReasoningEfforts(id string) ([]string, bool) {
	mode := AnthropicThinkingMode(id)
	if mode == "" {
		return nil, false
	}
	if mode == "none" {
		return []string{}, true
	}
	if mode != "adaptive" {
		return []string{"low", "medium", "high"}, true
	}

	id = strings.ReplaceAll(id, ".", "-")
	m := claudeRe.FindStringSubmatch(id)
	if m != nil && m[2] != "preview" {
		major, _ := strconv.Atoi(m[2])
		minor := 0
		if m[3] != "" {
			minor, _ = strconv.Atoi(m[3])
		}
		// Opus 4.6 and Sonnet 4.6 have max but no xhigh.
		if major == 4 && minor == 6 {
			return []string{"low", "medium", "high", "max"}, true
		}
	}
	if m != nil && m[2] == "preview" {
		return []string{"low", "medium", "high", "max"}, true
	}

	return []string{"low", "medium", "high", "xhigh", "max"}, true
}

// ─── Gemini ───

var geminiLevels = []struct {
	prefix  string
	efforts []string
}{
	// Most specific prefixes first.
	{"gemini-3.1-flash-lite-image", []string{"minimal", "high"}},
	{"gemini-3.1-pro", []string{"low", "medium", "high"}},
	{"gemini-3-pro", []string{"low", "high"}},
	{"gemini-3-flash", []string{"minimal", "low", "medium", "high"}},
	{"gemini-3.5-flash", []string{"minimal", "low", "medium", "high"}},
	{"gemini-3.6-flash", []string{"minimal", "low", "medium", "high"}},
	{"gemini-3.7-flash", []string{"low", "medium", "high"}},
	{"gemini-3.8-flash", []string{"low", "medium", "high"}},
	{"gemini-robotics-er", []string{"minimal", "low", "medium", "high"}},
	{"gemini-2.5-", []string{"low", "medium", "high"}},
}

func geminiReasoningEfforts(id string) ([]string, bool) {
	for _, entry := range geminiLevels {
		if strings.HasPrefix(id, entry.prefix) {
			return slices.Clone(entry.efforts), true
		}
	}
	if strings.HasPrefix(id, "gemini-2.0") || strings.HasPrefix(id, "gemini-1.") {
		return []string{}, true
	}

	return nil, false
}

// ─── OpenAI ───

var (
	gptRe = regexp.MustCompile(`^gpt-(\d+)(?:\.(\d+))?(?:-([a-z]+))?`)
	oRe   = regexp.MustCompile(`^o(\d)(?:-(mini|pro))?(?:$|-)`)
)

func openAIReasoningEfforts(id string) ([]string, bool) {
	// GPT-OSS on OpenAI-compatible hosts (including Groq and Cerebras).
	if id == "gpt-oss-120b" || id == "gpt-oss-20b" {
		return []string{"low", "medium", "high"}, true
	}
	// Published DeepSeek /models effort tables; do not infer legacy aliases.
	if id == "deepseek-flash" || id == "deepseek-v4-pro" {
		return []string{"low", "high", "max"}, true
	}
	if m := oRe.FindStringSubmatch(id); m != nil {
		if m[1] == "1" && (strings.HasPrefix(id, "o1-mini") || strings.HasPrefix(id, "o1-preview")) {
			return []string{}, true
		}
		if m[2] == "pro" {
			return nil, false
		}
		return []string{"low", "medium", "high"}, true
	}

	m := gptRe.FindStringSubmatch(id)
	if m == nil {
		return nil, false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	variant := m[3]
	if major < 5 {
		return []string{}, true
	}
	if strings.Contains(id, "-chat") {
		return []string{}, true
	}
	// Pro and Codex variants publish their own, differing tables; do not guess.
	if strings.Contains(id, "-pro") || strings.Contains(id, "-codex") {
		return nil, false
	}

	// Per-model "Reasoning.effort supports" lines on developers.openai.com.
	switch {
	case major == 5 && minor == 0:
		return []string{"minimal", "low", "medium", "high"}, true
	case major == 5 && minor == 1:
		return []string{"none", "low", "medium", "high"}, true
	case major == 5 && (minor == 2 || minor == 4 || minor == 5):
		return []string{"none", "low", "medium", "high", "xhigh"}, true
	case major == 5 && minor == 6:
		return []string{"none", "low", "medium", "high", "xhigh", "max"}, true
	case major == 6 && minor == 0 && variant == "astra":
		return []string{"low", "medium", "high", "xhigh", "max"}, true
	case major == 6 && minor == 0 && (variant == "sol" || variant == "luna"):
		return []string{"none", "low", "medium", "high", "xhigh", "max"}, true
	case major == 6 && minor == 1 && variant == "sol":
		return []string{"low", "medium", "high", "xhigh", "max"}, true
	}

	return nil, false
}
