package server

import (
	"log/slog"
	"net/http"
	"strings"

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
		if model.Capabilities != nil {
			metadata.SupportsVision = model.Capabilities.Vision
			metadata.SupportsFunctionCalling = model.Capabilities.ToolCalling
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
