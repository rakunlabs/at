package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/config"
)

// discoverRequest is the JSON body for POST /api/v1/providers/discover-models.
type discoverRequest struct {
	Config config.LLMConfig `json:"config"`
}

// discoverResponse is returned by the discover-models endpoint.
type discoverResponse struct {
	Models []string `json:"models"`
}

// DiscoverModelsAPI handles POST /api/v1/providers/discover-models.
// It uses the provided config (type, api_key, base_url, extra_headers) to
// call the upstream provider's model listing API and returns available model IDs.
func (s *Server) DiscoverModelsAPI(w http.ResponseWriter, r *http.Request) {
	var req discoverRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Config.Type == "" {
		httpResponse(w, "config.type is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	var models []string
	var err error

	switch req.Config.Type {
	case "openai":
		models, err = discoverOpenAIModels(ctx, req.Config)
	case "anthropic":
		models, err = discoverAnthropicModels(ctx, req.Config)
	default:
		httpResponse(w, fmt.Sprintf("model discovery is not supported for provider type %q", req.Config.Type), http.StatusBadRequest)
		return
	}

	if err != nil {
		slog.Error("discover models failed", "type", req.Config.Type, "error", err)
		httpResponse(w, fmt.Sprintf("failed to discover models: %v", err), http.StatusBadGateway)
		return
	}

	httpResponseJSON(w, discoverResponse{Models: models}, http.StatusOK)
}

// discoverOpenAIModels calls GET /v1/models on an OpenAI-compatible endpoint.
// It derives the models URL from the configured base_url by stripping /chat/completions.
func discoverOpenAIModels(ctx context.Context, cfg config.LLMConfig) ([]string, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1/chat/completions"
	}

	// Derive the models endpoint from the chat completions URL.
	// e.g., "https://api.openai.com/v1/chat/completions" -> "https://api.openai.com/v1/models"
	// e.g., "https://models.github.ai/inference/chat/completions" -> "https://models.github.ai/inference/models"
	modelsURL := baseURL
	if idx := strings.Index(modelsURL, "/chat/completions"); idx != -1 {
		modelsURL = modelsURL[:idx] + "/models"
	} else {
		// Fallback: append /models to the base URL
		modelsURL = strings.TrimSuffix(modelsURL, "/") + "/models"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}

	// Apply extra headers (important for GitHub).
	for k, v := range cfg.ExtraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream returned %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	// Parse OpenAI-compatible /v1/models response.
	var modelsResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &modelsResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	models := make([]string, 0, len(modelsResp.Data))
	for _, m := range modelsResp.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}

	return models, nil
}

// discoverAnthropicModels calls GET /v1/models on the Anthropic API.
func discoverAnthropicModels(ctx context.Context, cfg config.LLMConfig) ([]string, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}

	modelsURL := strings.TrimSuffix(baseURL, "/") + "/v1/models"

	var allModels []string
	afterID := ""

	for {
		url := modelsURL
		if afterID != "" {
			url += "?after_id=" + afterID
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}

		if cfg.APIKey != "" {
			req.Header.Set("x-api-key", cfg.APIKey)
		}
		req.Header.Set("anthropic-version", "2023-06-01")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("request failed: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("upstream returned %d: %s", resp.StatusCode, truncate(string(body), 200))
		}

		var page struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}

		for _, m := range page.Data {
			if m.ID != "" {
				allModels = append(allModels, m.ID)
			}
		}

		if !page.HasMore || page.LastID == "" {
			break
		}
		afterID = page.LastID
	}

	return allModels, nil
}

// truncate shortens a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
