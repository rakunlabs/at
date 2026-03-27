package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/worldline-go/klient"

	"github.com/rakunlabs/at/internal/config"
)

// discoverRequest is the JSON body for POST /api/v1/providers/discover-models.
type discoverRequest struct {
	Config config.LLMConfig `json:"config"`
	Key    string           `json:"key,omitempty"` // optional: existing provider key to fall back to stored api_key
}

// discoverResponse is returned by the discover-models endpoint.
type discoverResponse struct {
	Models []string `json:"models"`
}

// DiscoverModelsAPI handles POST /api/v1/providers/discover-models.
// It uses the provided config (type, api_key, base_url, extra_headers, proxy) to
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

	// When editing an existing provider the UI redacts the API key. If the
	// request omits the key but includes a provider key, look up the stored
	// config and use its API key so discovery still works.
	if req.Key != "" {
		existing, err := s.store.GetProvider(r.Context(), req.Key)
		if err == nil && existing != nil {
			if req.Config.APIKey == "" {
				req.Config.APIKey = existing.Config.APIKey
			}
			if req.Config.AuthType == "" {
				req.Config.AuthType = existing.Config.AuthType
			}
		}
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
		if err != nil {
			// Some Anthropic-compatible providers (e.g. MiniMax) don't support
			// the /v1/models endpoint. Return empty list instead of failing.
			slog.Warn("anthropic model discovery failed, returning empty list", "error", err)
			models = nil
			err = nil
		}
	case "gemini":
		models, err = discoverGeminiModels(ctx, req.Config)
	case "minimax":
		// MiniMax does not have a /v1/models endpoint. Return known models.
		models = []string{
			"MiniMax-M2.7",
			"MiniMax-M2.7-highspeed",
			"MiniMax-M2.5",
			"MiniMax-M2.5-highspeed",
			"MiniMax-M2.1",
			"MiniMax-M2.1-highspeed",
			"MiniMax-M2",
		}
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

	// Parse the base URL properly to preserve query parameters.
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base_url: %w", err)
	}

	// Check if this is a GitHub Copilot endpoint which does not support model listing.
	if strings.Contains(parsedURL.Host, "githubcopilot.com") {
		return nil, fmt.Errorf("GitHub Copilot API does not support model discovery; please enter models manually or use the preset list")
	}

	// Derive the models endpoint from the chat completions URL path.
	// e.g., "/v1/chat/completions" -> "/v1/models"
	// e.g., "/inference/chat/completions" -> "/inference/models"
	path := parsedURL.Path
	if idx := strings.Index(path, "/chat/completions"); idx != -1 {
		parsedURL.Path = path[:idx] + "/models"
	} else {
		// Fallback: append /models to the path
		parsedURL.Path = strings.TrimSuffix(path, "/") + "/models"
	}

	modelsURL := parsedURL.String()

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

	client, err := klientForConfig(cfg)
	if err != nil {
		return nil, err
	}

	resp, err := client.HTTP.Do(req)
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

	// Try OpenAI-compatible /v1/models response first: { "data": [{ "id": "..." }] }
	var modelsResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &modelsResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	// If "data" field is present and non-empty, use it (standard OpenAI format).
	if len(modelsResp.Data) > 0 {
		models := make([]string, 0, len(modelsResp.Data))
		for _, m := range modelsResp.Data {
			if m.ID != "" {
				models = append(models, m.ID)
			}
		}
		return models, nil
	}

	// Fallback: try flat array format [{ "id": "..." }] (e.g., GitHub Models catalog).
	var flatModels []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &flatModels); err != nil {
		return nil, fmt.Errorf("parse response: unexpected format")
	}

	models := make([]string, 0, len(flatModels))
	for _, m := range flatModels {
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

	client, err := klientForConfig(cfg)
	if err != nil {
		return nil, err
	}

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

		if cfg.AuthType == "claude-code" && cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
			req.Header.Set("anthropic-beta", "oauth-2025-04-20")
		} else if cfg.APIKey != "" {
			req.Header.Set("x-api-key", cfg.APIKey)
		}
		req.Header.Set("anthropic-version", "2023-06-01")

		resp, err := client.HTTP.Do(req)
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

// klientForConfig returns a *klient.Client that routes through cfg.Proxy
// when configured, with WithDisableBaseURLCheck so full URLs can be used directly.
func klientForConfig(cfg config.LLMConfig) (*klient.Client, error) {
	klientOpts := []klient.OptionClientFn{
		klient.WithDisableBaseURLCheck(true),
		klient.WithLogger(slog.Default()),
		klient.WithDisableRetry(true),
		klient.WithDisableEnvValues(true),
	}
	if cfg.Proxy != "" {
		klientOpts = append(klientOpts, klient.WithProxy(cfg.Proxy))
	}
	if cfg.InsecureSkipVerify {
		klientOpts = append(klientOpts, klient.WithInsecureSkipVerify(true))
	}
	return klient.New(klientOpts...)
}

// geminiModel represents a model returned by the Gemini /v1beta/models endpoint.
type geminiModel struct {
	Name                       string   `json:"name"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
}

// listGeminiModels calls GET /v1beta/models on the Google Generative Language API
// and returns all models without filtering. When bearerAuth is true the API key
// is sent as an Authorization Bearer token instead of the native x-goog-api-key
// header (useful when the request goes through a gateway proxy).
func listGeminiModels(ctx context.Context, cfg config.LLMConfig, bearerAuth bool) ([]geminiModel, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	modelsURL := baseURL + "/v1beta/models"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	if cfg.APIKey != "" {
		if bearerAuth {
			req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		} else {
			req.Header.Set("x-goog-api-key", cfg.APIKey)
		}
	}

	client, err := klientForConfig(cfg)
	if err != nil {
		return nil, err
	}

	resp, err := client.HTTP.Do(req)
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

	// Google's /v1beta/models returns: { "models": [{ "name": "models/gemini-2.5-flash", ... }] }
	var modelsResp struct {
		Models []geminiModel `json:"models"`
	}
	if err := json.Unmarshal(body, &modelsResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return modelsResp.Models, nil
}

// filterGeminiModelsByMethod returns model IDs that support the given generation method.
func filterGeminiModelsByMethod(models []geminiModel, method string) []string {
	var result []string
	for _, m := range models {
		for _, supported := range m.SupportedGenerationMethods {
			if supported == method {
				id := strings.TrimPrefix(m.Name, "models/")
				if id != "" {
					result = append(result, id)
				}

				break
			}
		}
	}

	return result
}

// discoverGeminiModels calls GET /v1beta/models on the Google Generative Language API
// and returns models that support generateContent (chat).
func discoverGeminiModels(ctx context.Context, cfg config.LLMConfig) ([]string, error) {
	models, err := listGeminiModels(ctx, cfg, false)
	if err != nil {
		return nil, err
	}

	return filterGeminiModelsByMethod(models, "generateContent"), nil
}

// discoverGeminiEmbeddingModels calls GET /v1beta/models on the Google Generative Language API
// and returns models that support embedContent (embedding).
func discoverGeminiEmbeddingModels(ctx context.Context, cfg config.LLMConfig, bearerAuth bool) ([]string, error) {
	models, err := listGeminiModels(ctx, cfg, bearerAuth)
	if err != nil {
		return nil, err
	}

	return filterGeminiModelsByMethod(models, "embedContent"), nil
}
