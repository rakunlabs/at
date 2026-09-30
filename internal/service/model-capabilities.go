package service

import (
	"fmt"
	"slices"
	"strings"

	"github.com/rakunlabs/at/internal/config"
)

// Model capabilities describe what one chat model accepts and can do, so the
// gateway can advertise it to catalog clients (OpenCode, LiteLLM-shaped
// discovery), the UI can offer only what works, and a request carrying input
// the model cannot read is refused before it costs anything upstream.
//
// Every value has three states: detected from the model family (the
// providers' published per-model tables), set by an operator override on the
// provider, or unknown. Unknown is never guessed: an unrecognised model on an
// OpenAI-compatible endpoint advertises nothing rather than a capability it
// may not have.

// Input modalities, in the vocabulary models.dev / OpenCode / LiteLLM use.
const (
	ModalityText  = "text"
	ModalityImage = "image"
	ModalityPDF   = "pdf"
	ModalityAudio = "audio"
	ModalityVideo = "video"
)

// InputModalities is the whole input vocabulary in display order.
var InputModalities = []string{ModalityText, ModalityImage, ModalityPDF, ModalityAudio, ModalityVideo}

// OutputModalities is the whole output vocabulary in display order.
var OutputModalities = []string{ModalityText, ModalityImage, ModalityAudio, ModalityVideo}

// Feature keys for boolean capabilities.
const (
	FeatureToolCalling      = "tool_calling"
	FeatureParallelTools    = "parallel_tool_calls"
	FeatureStructuredOutput = "structured_output"
	FeatureTemperature      = "temperature"
	FeatureWebSearch        = "web_search"
	FeaturePromptCaching    = "prompt_caching"
)

// ModelFeatures is the boolean vocabulary in display order.
var ModelFeatures = []string{FeatureToolCalling, FeatureParallelTools, FeatureStructuredOutput, FeatureTemperature, FeatureWebSearch, FeaturePromptCaching}

// ModelCapabilityOverride is the operator-declared form stored on a provider
// (config.ModelCapability mirrors it). nil fields keep detection.
type ModelCapabilityOverride struct {
	InputModalities  []string
	OutputModalities []string
	ReasoningEfforts []string
	Features         map[string]bool
}

// ModelCapabilities is the resolved view. A nil slice or absent map key means
// unknown; an empty non-nil slice means "known: none".
type ModelCapabilities struct {
	InputModalities  []string        `json:"input_modalities,omitempty"`
	OutputModalities []string        `json:"output_modalities,omitempty"`
	ReasoningEfforts []string        `json:"reasoning_efforts,omitzero"`
	Features         map[string]bool `json:"features,omitempty"`
}

// Known reports whether anything at all is known about the model.
func (c ModelCapabilities) Known() bool {
	return c.InputModalities != nil || c.OutputModalities != nil || c.ReasoningEfforts != nil || len(c.Features) > 0
}

// Accepts reports whether the model is known to accept a modality. Unknown
// modalities are accepted: refusing on missing metadata would break every
// unrecognised model.
func (c ModelCapabilities) Accepts(modality string) bool {
	return c.InputModalities == nil || slices.Contains(c.InputModalities, modality)
}

// Feature returns a feature flag and whether it is known.
func (c ModelCapabilities) Feature(name string) (value, known bool) {
	value, known = c.Features[name]
	return value, known
}

// ResolveModelCapabilities merges an override over detection, field by field.
func ResolveModelCapabilities(providerType, model string, override ModelCapabilityOverride) ModelCapabilities {
	caps := DetectModelCapabilities(providerType, model)
	if override.InputModalities != nil {
		caps.InputModalities = orderedModalities(override.InputModalities, InputModalities)
	}
	if override.OutputModalities != nil {
		caps.OutputModalities = orderedModalities(override.OutputModalities, OutputModalities)
	}
	if efforts, known := ModelReasoningEfforts(providerType, model, override.ReasoningEfforts); known {
		caps.ReasoningEfforts = efforts
	}
	if len(override.Features) > 0 {
		merged := make(map[string]bool, len(caps.Features)+len(override.Features))
		for k, v := range caps.Features {
			merged[k] = v
		}
		for k, v := range override.Features {
			merged[k] = v
		}
		caps.Features = merged
	}

	return caps
}

// ValidateModelCapabilityOverride checks vocabulary; it is the provider
// save-time guard. An override that says nothing is ambiguous and refused.
func ValidateModelCapabilityOverride(providerType string, override ModelCapabilityOverride) error {
	if override.InputModalities == nil && override.OutputModalities == nil && override.ReasoningEfforts == nil && len(override.Features) == 0 {
		return fmt.Errorf("set at least one of input_modalities, output_modalities, reasoning_efforts or features")
	}
	if err := validateModalities("input_modalities", override.InputModalities, InputModalities); err != nil {
		return err
	}
	if override.InputModalities != nil && !slices.Contains(override.InputModalities, ModalityText) {
		return fmt.Errorf("input_modalities must include text")
	}
	if err := validateModalities("output_modalities", override.OutputModalities, OutputModalities); err != nil {
		return err
	}
	if err := ValidateModelReasoningEffortOverride(providerType, override.ReasoningEfforts); err != nil {
		return fmt.Errorf("reasoning_efforts: %w", err)
	}
	for name := range override.Features {
		if !slices.Contains(ModelFeatures, name) {
			return fmt.Errorf("unknown feature %q: expected one of %s", name, strings.Join(ModelFeatures, ", "))
		}
	}

	return nil
}

func validateModalities(field string, values, vocabulary []string) error {
	seen := map[string]bool{}
	for _, v := range values {
		if !slices.Contains(vocabulary, v) {
			return fmt.Errorf("%s: unknown modality %q: expected %s", field, v, strings.Join(vocabulary, ", "))
		}
		if seen[v] {
			return fmt.Errorf("%s: %q is listed twice", field, v)
		}
		seen[v] = true
	}

	return nil
}

func orderedModalities(values, vocabulary []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range vocabulary {
		if slices.Contains(values, v) {
			out = append(out, v)
		}
	}

	return out
}

// DetectModelCapabilities derives capabilities from the model family,
// following the providers' published model pages (platform.claude.com model
// overview, vision, PDF support and thinking pages; ai.google.dev per-model
// "Supported data types" and "Capabilities" tables; developers.openai.com
// per-model modalities and features). Reasoning efforts come from
// DetectModelReasoningEfforts.
func DetectModelCapabilities(providerType, model string) ModelCapabilities {
	id := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	var caps ModelCapabilities
	if efforts, known := DetectModelReasoningEfforts(providerType, id); known {
		caps.ReasoningEfforts = efforts
	}
	if id == "" {
		return caps
	}

	switch providerType {
	case "anthropic", "bedrock":
		detectClaudeCapabilities(&caps, id, providerType)
	case "gemini", "vertex-gemini":
		detectGeminiCapabilities(&caps, id)
	case "vertex":
		if strings.HasPrefix(id, "gemini-") {
			detectGeminiCapabilities(&caps, id)
			if caps.Features != nil {
				// The OpenAI-compatible endpoint has no native web-search tool.
				caps.Features[FeatureWebSearch] = false
			}
		} else {
			detectOpenAICapabilities(&caps, id)
		}
	case "openai", "azure":
		detectOpenAICapabilities(&caps, id)
	}

	return caps
}

// ─── Anthropic ───

func detectClaudeCapabilities(caps *ModelCapabilities, id, providerType string) {
	if providerType == "bedrock" {
		// Bedrock IDs look like anthropic.claude-opus-5-5 or
		// us.anthropic.claude-...-v1:0; the family rules are the same.
		if i := strings.Index(id, "claude-"); i >= 0 {
			id = id[i:]
		}
		// Reasoning mapping differs on Converse; do not advertise efforts.
		caps.ReasoningEfforts = nil
	}
	mode := AnthropicThinkingMode(id)
	if mode == "" {
		return
	}
	// Every active Claude model takes text + images, and PDF processing
	// ("All active models support PDF processing"). Claude has no audio or
	// video input and only text output.
	caps.InputModalities = []string{ModalityText, ModalityImage, ModalityPDF}
	caps.OutputModalities = []string{ModalityText}
	if strings.Contains(id, "claude-2") || strings.Contains(id, "claude-instant") {
		caps.InputModalities = []string{ModalityText}
	}
	caps.Features = map[string]bool{
		FeatureToolCalling:      !strings.Contains(id, "claude-instant") && !strings.Contains(id, "claude-2"),
		FeatureParallelTools:    true,
		FeatureStructuredOutput: false, // no response_format; AT emulates JSON mode by prompt
		FeaturePromptCaching:    true,
		FeatureWebSearch:        providerType == "anthropic",
		// "Models released after Claude Opus 4.6 do not support setting
		// temperature" — non-default values are a 400 on every request.
		FeatureTemperature: !claudeRejectsSampling(id),
	}
}

// claudeRejectsSampling is the set named in the thinking docs' "Sampling
// parameters" section: Opus 4.7+, Sonnet 5+, Fable, Mythos.
func claudeRejectsSampling(id string) bool {
	id = strings.ReplaceAll(id, ".", "-")
	m := claudeRe.FindStringSubmatch(id)
	if m == nil {
		return false
	}
	family := m[1]
	if family == "fable" || family == "mythos" {
		return true
	}
	if m[2] == "preview" {
		return true
	}
	major, minor := 0, 0
	fmt.Sscanf(m[2], "%d", &major)
	if m[3] != "" {
		fmt.Sscanf(m[3], "%d", &minor)
	}
	switch family {
	case "opus":
		return major > 4 || (major == 4 && minor >= 7)
	case "sonnet":
		return major >= 5
	}

	return false
}

// ─── Gemini ───

var geminiModalityTable = []struct {
	prefix string
	input  []string
	output []string
	tools  bool
}{
	// Image-generation models ("Nano Banana"): no function calling.
	{"gemini-3-pro-image", []string{ModalityText, ModalityImage}, []string{ModalityText, ModalityImage}, false},
	{"gemini-3.1-flash-image", []string{ModalityText, ModalityImage, ModalityPDF, ModalityVideo}, []string{ModalityText, ModalityImage}, false},
	{"gemini-3.1-flash-lite-image", []string{ModalityText, ModalityImage, ModalityPDF, ModalityVideo}, []string{ModalityText, ModalityImage}, false},
	{"gemini-2.5-flash-image", []string{ModalityText, ModalityImage}, []string{ModalityText, ModalityImage}, false},
	// TTS models produce audio from text.
	{"-tts", []string{ModalityText}, []string{ModalityAudio}, false},
	// Everything else in the text family: full multimodal input, text out.
	{"gemini-2.0", []string{ModalityText, ModalityImage, ModalityAudio, ModalityVideo}, []string{ModalityText}, true},
	{"gemini-", []string{ModalityText, ModalityImage, ModalityPDF, ModalityAudio, ModalityVideo}, []string{ModalityText}, true},
}

func detectGeminiCapabilities(caps *ModelCapabilities, id string) {
	if !strings.HasPrefix(id, "gemini-") || strings.Contains(id, "embedding") || strings.Contains(id, "-live") || strings.HasPrefix(id, "gemini-omni") {
		return
	}
	for _, row := range geminiModalityTable {
		match := strings.HasPrefix(id, row.prefix)
		if strings.HasPrefix(row.prefix, "-") {
			match = strings.Contains(id, row.prefix)
		}
		if !match {
			continue
		}
		caps.InputModalities = slices.Clone(row.input)
		caps.OutputModalities = slices.Clone(row.output)
		caps.Features = map[string]bool{
			FeatureToolCalling:      row.tools,
			FeatureParallelTools:    row.tools,
			FeatureStructuredOutput: row.tools,
			FeatureTemperature:      true,
			FeatureWebSearch:        true, // Search grounding: supported on every listed chat model
			FeaturePromptCaching:    row.tools,
		}
		return
	}
}

// ─── OpenAI ───

func detectOpenAICapabilities(caps *ModelCapabilities, id string) {
	switch {
	case strings.HasPrefix(id, "gpt-realtime") || strings.Contains(id, "-realtime"):
		// Realtime models need the Realtime API; they are not chat models.
		return
	case strings.HasPrefix(id, "gpt-audio") || strings.Contains(id, "-audio-preview"):
		caps.InputModalities = []string{ModalityText, ModalityAudio}
		caps.OutputModalities = []string{ModalityText, ModalityAudio}
		caps.Features = map[string]bool{FeatureToolCalling: true, FeatureParallelTools: true, FeatureStructuredOutput: false, FeatureTemperature: true, FeatureWebSearch: false, FeaturePromptCaching: false}
		return
	}

	reasoning := caps.ReasoningEfforts != nil && len(caps.ReasoningEfforts) > 0
	_, family := openAIReasoningEfforts(id)
	isO := oRe.MatchString(id)
	m := gptRe.FindStringSubmatch(id)
	if !family && !isO && m == nil {
		return
	}
	if m != nil {
		major := 0
		fmt.Sscanf(m[1], "%d", &major)
		if major < 4 || (major == 4 && !strings.HasPrefix(id, "gpt-4o") && !strings.HasPrefix(id, "gpt-4.1")) {
			// gpt-3.5 / original gpt-4: text only, not worth modelling.
			caps.InputModalities = []string{ModalityText}
			caps.OutputModalities = []string{ModalityText}
			return
		}
	}

	// Every current GPT-4o/4.1/5/6 and o-series model: text + image in, text
	// out ("Input modalities: text, image"). Chat Completions additionally
	// accepts PDF as a `file` part on vision models.
	caps.InputModalities = []string{ModalityText, ModalityImage, ModalityPDF}
	caps.OutputModalities = []string{ModalityText}
	caps.Features = map[string]bool{
		FeatureToolCalling:      true,
		FeatureParallelTools:    true,
		FeatureStructuredOutput: true,
		// Reasoning models reject temperature; plain chat models accept it.
		FeatureTemperature: !reasoning && !isO,
		// AT calls Chat Completions, where only the search-preview models
		// search natively; the web_search tool is a Responses API feature.
		FeatureWebSearch:     false,
		FeaturePromptCaching: true,
	}
	if strings.Contains(id, "search") {
		// Search-preview models: no tools, web search built in.
		caps.Features[FeatureToolCalling] = false
		caps.Features[FeatureParallelTools] = false
		caps.Features[FeatureWebSearch] = true
	}
}

// CapabilityOverrideFromConfig converts the stored provider form, folding the
// legacy image_input switch into input modalities relative to detection.
func CapabilityOverrideFromConfig(providerType, model string, stored config.ModelCapability) ModelCapabilityOverride {
	override := ModelCapabilityOverride{
		InputModalities:  stored.InputModalities,
		OutputModalities: stored.OutputModalities,
		ReasoningEfforts: stored.ReasoningEfforts,
		Features:         stored.Features,
	}
	if override.InputModalities == nil && stored.ImageInput != nil {
		base := DetectModelCapabilities(providerType, model).InputModalities
		if base == nil {
			base = []string{ModalityText}
		}
		inputs := slices.DeleteFunc(slices.Clone(base), func(m string) bool { return m == ModalityImage })
		if *stored.ImageInput {
			inputs = append(inputs, ModalityImage)
		}
		override.InputModalities = orderedModalities(inputs, InputModalities)
	}

	return override
}

// ProviderModelCapabilities resolves capabilities for one model of a stored
// provider configuration.
func ProviderModelCapabilities(cfg config.LLMConfig, model string) ModelCapabilities {
	return ResolveModelCapabilities(cfg.Type, model, CapabilityOverrideFromConfig(cfg.Type, model, cfg.ModelCapabilities[model]))
}
