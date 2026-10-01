package server

import (
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/rakunlabs/at/internal/gateway/wire"
	"github.com/rakunlabs/at/internal/service"
)

// These types cover the LiteLLM /v1/model/info subset consumed by
// opencode-models-discovery. LiteLLM prices are USD per token; AT stores USD
// per million tokens.
type liteLLMModelInfoResponse struct {
	Data []liteLLMModelInfoEntry `json:"data"`
}

type liteLLMModelInfoEntry struct {
	ModelName     string               `json:"model_name"`
	LiteLLMParams liteLLMModelParams   `json:"litellm_params"`
	ModelInfo     liteLLMModelMetadata `json:"model_info"`
}

type liteLLMModelParams struct {
	Model string `json:"model"`
}

type liteLLMModelMetadata struct {
	Key                         string                  `json:"key"`
	Mode                        string                  `json:"mode"`
	MaxTokens                   int                     `json:"max_tokens,omitempty"`
	MaxOutputTokens             int                     `json:"max_output_tokens,omitempty"`
	Modalities                  *liteLLMModelModalities `json:"modalities,omitempty"`
	SupportsVision              *bool                   `json:"supports_vision,omitempty"`
	SupportsFunctionCalling     *bool                   `json:"supports_function_calling,omitempty"`
	SupportsPDFInput            *bool                   `json:"supports_pdf_input,omitempty"`
	SupportsAudioInput          *bool                   `json:"supports_audio_input,omitempty"`
	SupportsVideoInput          *bool                   `json:"supports_video_input,omitempty"`
	SupportsAudioOutput         *bool                   `json:"supports_audio_output,omitempty"`
	SupportsParallelFunctions   *bool                   `json:"supports_parallel_function_calling,omitempty"`
	SupportsResponseSchema      *bool                   `json:"supports_response_schema,omitempty"`
	SupportsWebSearch           *bool                   `json:"supports_web_search,omitempty"`
	SupportsPromptCaching       *bool                   `json:"supports_prompt_caching,omitempty"`
	SupportsReasoning           *bool                   `json:"supports_reasoning,omitempty"`
	SupportsNoneEffort          *bool                   `json:"supports_none_reasoning_effort,omitempty"`
	SupportsMinimalEffort       *bool                   `json:"supports_minimal_reasoning_effort,omitempty"`
	SupportsLowEffort           *bool                   `json:"supports_low_reasoning_effort,omitempty"`
	SupportsMediumEffort        *bool                   `json:"supports_medium_reasoning_effort,omitempty"`
	SupportsHighEffort          *bool                   `json:"supports_high_reasoning_effort,omitempty"`
	SupportsXHighEffort         *bool                   `json:"supports_xhigh_reasoning_effort,omitempty"`
	SupportsMaxEffort           *bool                   `json:"supports_max_reasoning_effort,omitempty"`
	SupportedOpenAIParams       []string                `json:"supported_openai_params,omitempty"`
	InputCostPerToken           *float64                `json:"input_cost_per_token,omitempty"`
	OutputCostPerToken          *float64                `json:"output_cost_per_token,omitempty"`
	CacheReadInputTokenCost     *float64                `json:"cache_read_input_token_cost,omitempty"`
	CacheCreationInputTokenCost *float64                `json:"cache_creation_input_token_cost,omitempty"`
}

type liteLLMModelModalities struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

// ListLiteLLMModelInfo serves token-scoped model metadata in LiteLLM's shape so
// discovery clients can use AT's centrally managed prices. Missing pricing is
// represented by absent fields, never zero; an explicit all-zero row therefore
// continues to mean a deliberately free model.
func (s *Server) ListLiteLLMModelInfo(w http.ResponseWriter, r *http.Request) {
	auth, authErr := s.authenticateRequest(r)
	if authErr != "" {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": authErr,
				"type":    "invalid_request_error",
				"code":    "invalid_api_key",
			},
		}, http.StatusUnauthorized)
		return
	}

	pricing := []service.ModelPricing{}
	if s.agentBudgetStore != nil {
		var err error
		pricing, err = s.agentBudgetStore.ListModelPricing(r.Context())
		if err != nil {
			slog.Error("list gateway model pricing failed", "error", err)
			httpResponseJSON(w, map[string]any{
				"error": map[string]any{
					"message": "failed to load model pricing",
					"type":    "server_error",
					"code":    "model_pricing_unavailable",
				},
			}, http.StatusInternalServerError)
			return
		}
	}

	models := s.gatewayModels(r.Context(), auth)
	entries := make([]liteLLMModelInfoEntry, 0, len(models))
	for _, model := range models {
		metadata := liteLLMModelMetadata{
			Key:             model.ID,
			Mode:            model.Mode,
			MaxTokens:       model.ContextLength,
			MaxOutputTokens: model.MaxOutputTokens,
		}
		if len(model.InputModalities) > 0 || len(model.OutputModalities) > 0 {
			metadata.Modalities = &liteLLMModelModalities{
				Input:  model.InputModalities,
				Output: model.OutputModalities,
			}
		}
		if c := model.Capabilities; c != nil {
			metadata.SupportsVision = c.Vision
			metadata.SupportsPDFInput = c.PDFInput
			metadata.SupportsAudioInput = c.AudioInput
			metadata.SupportsVideoInput = c.VideoInput
			metadata.SupportsFunctionCalling = c.ToolCalling
			metadata.SupportsParallelFunctions = c.ParallelToolCalls
			metadata.SupportsResponseSchema = c.StructuredOutput
			metadata.SupportsWebSearch = c.WebSearch
			metadata.SupportsPromptCaching = c.PromptCaching
			if model.OutputModalities != nil {
				metadata.SupportsAudioOutput = boolPtr(slices.Contains(model.OutputModalities, service.ModalityAudio))
			}
			applyLiteLLMReasoning(&metadata, model)
			applyLiteLLMParams(&metadata, model)
		}

		providerKey := model.OwnedBy
		actualModel := strings.TrimPrefix(model.ID, providerKey+"/")
		if p, ok := findModelPricing(pricing, providerKey, actualModel, model.ID); ok {
			metadata.InputCostPerToken = perTokenPrice(p.PromptPricePer1M)
			metadata.OutputCostPerToken = perTokenPrice(p.CompletionPricePer1M)
			if p.CacheReadPricePer1M > 0 {
				metadata.CacheReadInputTokenCost = perTokenPrice(p.CacheReadPricePer1M)
			}
			if p.CacheWritePricePer1M > 0 {
				metadata.CacheCreationInputTokenCost = perTokenPrice(p.CacheWritePricePer1M)
			}
		}

		entries = append(entries, liteLLMModelInfoEntry{
			ModelName:     model.ID,
			LiteLLMParams: liteLLMModelParams{Model: model.ID},
			ModelInfo:     metadata,
		})
	}

	httpResponseJSON(w, liteLLMModelInfoResponse{Data: entries}, http.StatusOK)
}

func perTokenPrice(perMillion float64) *float64 {
	price := perMillion / 1_000_000
	return &price
}

// applyLiteLLMReasoning maps AT's per-model efforts onto LiteLLM's flags.
// Every tier flag is set explicitly, because discovery clients default the
// unset low/medium/high tiers to supported and xhigh/max/none/minimal to not.
func applyLiteLLMReasoning(metadata *liteLLMModelMetadata, model wire.ModelData) {
	reasoning := model.Capabilities.Reasoning
	if reasoning == nil {
		return
	}
	metadata.SupportsReasoning = reasoning
	if !*reasoning {
		return
	}
	efforts := model.Capabilities.ReasoningEfforts
	flag := func(effort string) *bool {
		supported := slices.Contains(efforts, effort)
		return &supported
	}
	metadata.SupportsNoneEffort = flag("none")
	metadata.SupportsMinimalEffort = flag("minimal")
	metadata.SupportsLowEffort = flag("low")
	metadata.SupportsMediumEffort = flag("medium")
	metadata.SupportsHighEffort = flag("high")
	metadata.SupportsXHighEffort = flag("xhigh")
	metadata.SupportsMaxEffort = flag("max")
}

// applyLiteLLMParams lists supported_openai_params only when AT knows the
// model well enough to say something true. Clients read a non-empty list as
// the complete set: temperature missing means "never send it", which is how
// reasoning models (which reject it) are protected.
func applyLiteLLMParams(metadata *liteLLMModelMetadata, model wire.ModelData) {
	c := model.Capabilities
	if c.Temperature == nil && c.Reasoning == nil {
		return
	}
	params := []string{"max_tokens", "stop", "stream", "stream_options", "response_format", "seed", "user"}
	if c.Temperature == nil || *c.Temperature {
		params = append(params, "temperature", "top_p")
	}
	if c.ToolCalling == nil || *c.ToolCalling {
		params = append(params, "tools", "tool_choice")
	}
	if c.ParallelToolCalls != nil && *c.ParallelToolCalls {
		params = append(params, "parallel_tool_calls")
	}
	if c.Reasoning != nil && *c.Reasoning {
		params = append(params, "reasoning_effort")
	}
	metadata.SupportedOpenAIParams = params
}
