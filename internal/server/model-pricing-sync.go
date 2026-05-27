package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/rakunlabs/at/internal/service"
)

const (
	piDevPricingSource         = "pi.dev"
	piDevModelsURL             = "https://pi.dev/models"
	llmPricesPricingSource     = "llm-prices"
	llmPricesCurrentURL        = "https://www.llm-prices.com/current-v1.json"
	pricingAgentSourceMaxBytes = 512 << 10
	pricingAgentPromptMaxChars = 120_000
)

type modelPricingSyncSourceInfo struct {
	Source      string `json:"source"`
	Label       string `json:"label"`
	URL         string `json:"url,omitempty"`
	Description string `json:"description,omitempty"`
}

type modelPricingSyncSource struct {
	modelPricingSyncSourceInfo
	buildPreview func(*Server, context.Context) ([]modelPricingSyncPreviewItem, error)
}

var modelPricingSyncSources = []modelPricingSyncSource{
	{
		modelPricingSyncSourceInfo: modelPricingSyncSourceInfo{
			Source:      piDevPricingSource,
			Label:       "pi.dev",
			URL:         piDevModelsURL,
			Description: "pi.dev model catalog scraped from the public models page.",
		},
		buildPreview: func(s *Server, ctx context.Context) ([]modelPricingSyncPreviewItem, error) {
			return s.buildPiDevPricingPreview(ctx)
		},
	},
	{
		modelPricingSyncSourceInfo: modelPricingSyncSourceInfo{
			Source:      llmPricesPricingSource,
			Label:       "llm-prices",
			URL:         llmPricesCurrentURL,
			Description: "Simon Willison's llm-prices current JSON catalog.",
		},
		buildPreview: func(s *Server, ctx context.Context) ([]modelPricingSyncPreviewItem, error) {
			return s.buildLLMPricesPricingPreview(ctx)
		},
	},
}

type modelPricingSyncPreviewRequest struct {
	Source string `json:"source,omitempty"`
}

type modelPricingSyncApplyRequest struct {
	Source             string                        `json:"source,omitempty"`
	OverwriteOverrides bool                          `json:"overwrite_overrides,omitempty"`
	Items              []modelPricingSyncKeyItem     `json:"items,omitempty"`
	PreviewItems       []modelPricingSyncPreviewItem `json:"preview_items,omitempty"`
}

type modelPricingSyncKeyItem struct {
	ProviderKey string `json:"provider_key"`
	Model       string `json:"model"`
}

type modelPricingSyncPreviewResponse struct {
	Source string                        `json:"source"`
	Items  []modelPricingSyncPreviewItem `json:"items"`
}

type modelPricingSyncPreviewItem struct {
	ProviderKey  string `json:"provider_key"`
	ProviderType string `json:"provider_type"`
	Model        string `json:"model"`

	Matched        bool    `json:"matched"`
	MatchType      string  `json:"match_type,omitempty"`
	Confidence     float64 `json:"confidence,omitempty"`
	Status         string  `json:"status"`
	HasCurrent     bool    `json:"has_current"`
	ManualOverride bool    `json:"manual_override"`

	Source         string `json:"source,omitempty"`
	SourceProvider string `json:"source_provider,omitempty"`
	SourceModel    string `json:"source_model,omitempty"`
	SourceURL      string `json:"source_url,omitempty"`

	CurrentPromptPricePer1M     float64 `json:"current_prompt_price_per_1m"`
	CurrentCompletionPricePer1M float64 `json:"current_completion_price_per_1m"`
	CurrentCacheReadPricePer1M  float64 `json:"current_cache_read_price_per_1m"`
	CurrentCacheWritePricePer1M float64 `json:"current_cache_write_price_per_1m"`

	SourcePromptPricePer1M     float64 `json:"source_prompt_price_per_1m"`
	SourceCompletionPricePer1M float64 `json:"source_completion_price_per_1m"`
	SourceCacheReadPricePer1M  float64 `json:"source_cache_read_price_per_1m"`
	SourceCacheWritePricePer1M float64 `json:"source_cache_write_price_per_1m"`
}

type modelPricingSyncApplyResponse struct {
	Applied int      `json:"applied"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors,omitempty"`
}

type modelPricingCatalogExport struct {
	Version    int                    `json:"version"`
	ExportedAt string                 `json:"exported_at"`
	Items      []service.ModelPricing `json:"items"`
}

type modelPricingCatalogImportRequest struct {
	Items              []service.ModelPricing `json:"items"`
	OverwriteOverrides bool                   `json:"overwrite_overrides,omitempty"`
}

type modelPricingAgentPreviewRequest struct {
	ProviderKey string `json:"provider_key"`
	Model       string `json:"model,omitempty"`
	Instruction string `json:"instruction,omitempty"`
	SourceURL   string `json:"source_url,omitempty"`
	SourceText  string `json:"source_text,omitempty"`
	WebSearch   bool   `json:"web_search,omitempty"`
}

type modelPricingSourceMatch struct {
	Provider             string
	Model                string
	URL                  string
	PromptPricePer1M     float64
	CompletionPricePer1M float64
	CacheReadPricePer1M  float64
	CacheWritePricePer1M float64
}

type modelPricingSourceItem struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Name     string `json:"name,omitempty"`
	URL      string `json:"url,omitempty"`

	SourceProvider string `json:"source_provider,omitempty"`
	SourceModel    string `json:"source_model,omitempty"`
	SourceURL      string `json:"source_url,omitempty"`

	PromptPricePer1M     float64 `json:"prompt_price_per_1m"`
	CompletionPricePer1M float64 `json:"completion_price_per_1m"`
	CacheReadPricePer1M  float64 `json:"cache_read_price_per_1m"`
	CacheWritePricePer1M float64 `json:"cache_write_price_per_1m"`

	InputPricePer1M  float64 `json:"input_price_per_1m,omitempty"`
	OutputPricePer1M float64 `json:"output_price_per_1m,omitempty"`
}

type configuredPricingModel struct {
	ProviderKey  string
	ProviderType string
	Model        string
}

type piDevModelPricing struct {
	Provider             string
	Model                string
	Name                 string
	Path                 string
	PromptPricePer1M     float64
	CompletionPricePer1M float64
	CacheReadPricePer1M  float64
	CacheWritePricePer1M float64
}

// ListModelPricingSyncSourcesAPI handles GET /api/v1/model-pricing/sync/sources.
func (s *Server) ListModelPricingSyncSourcesAPI(w http.ResponseWriter, r *http.Request) {
	httpResponseJSON(w, listModelPricingSyncSourceInfos(), http.StatusOK)
}

// PreviewModelPricingSyncAPI handles POST /api/v1/model-pricing/sync/preview.
func (s *Server) PreviewModelPricingSyncAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentBudgetStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	req := modelPricingSyncPreviewRequest{Source: piDevPricingSource}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	req.Source = strings.TrimSpace(req.Source)
	if req.Source == "" {
		req.Source = piDevPricingSource
	}
	source, ok := modelPricingSyncSourceByName(req.Source)
	if !ok {
		httpResponse(w, fmt.Sprintf("unsupported pricing source %q", req.Source), http.StatusBadRequest)
		return
	}

	items, err := source.buildPreview(s, r.Context())
	if err != nil {
		slog.Error("build model pricing preview failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to build pricing preview: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, modelPricingSyncPreviewResponse{Source: source.Source, Items: items}, http.StatusOK)
}

// ApplyModelPricingSyncAPI handles POST /api/v1/model-pricing/sync/apply.
func (s *Server) ApplyModelPricingSyncAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentBudgetStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req modelPricingSyncApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	req.Source = strings.TrimSpace(req.Source)
	if req.Source == "" {
		req.Source = piDevPricingSource
	}
	if _, ok := modelPricingSyncSourceByName(req.Source); !ok && len(req.PreviewItems) == 0 {
		httpResponse(w, "preview_items are required for unregistered pricing sync sources", http.StatusBadRequest)
		return
	}

	preview, err := s.buildPricingSyncApplyPreview(r.Context(), req)
	if err != nil {
		slog.Error("build model pricing apply preview failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to refresh pricing preview: %v", err), http.StatusInternalServerError)
		return
	}
	selected := make(map[string]struct{}, len(req.Items))
	for _, item := range req.Items {
		if item.ProviderKey == "" || item.Model == "" {
			continue
		}
		selected[item.ProviderKey+"\x00"+item.Model] = struct{}{}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	var out modelPricingSyncApplyResponse
	for _, item := range preview {
		if _, ok := selected[item.ProviderKey+"\x00"+item.Model]; !ok {
			continue
		}
		if !item.Matched {
			out.Skipped++
			continue
		}
		if item.ManualOverride && !req.OverwriteOverrides {
			out.Skipped++
			continue
		}
		source := item.Source
		if source == "" {
			source = req.Source
		}

		pricing := service.ModelPricing{
			ProviderKey:                item.ProviderKey,
			Model:                      item.Model,
			PromptPricePer1M:           item.SourcePromptPricePer1M,
			CompletionPricePer1M:       item.SourceCompletionPricePer1M,
			CacheReadPricePer1M:        item.SourceCacheReadPricePer1M,
			CacheWritePricePer1M:       item.SourceCacheWritePricePer1M,
			Source:                     source,
			SourceProvider:             item.SourceProvider,
			SourceModel:                item.SourceModel,
			SourceURL:                  item.SourceURL,
			SourcePromptPricePer1M:     item.SourcePromptPricePer1M,
			SourceCompletionPricePer1M: item.SourceCompletionPricePer1M,
			SourceCacheReadPricePer1M:  item.SourceCacheReadPricePer1M,
			SourceCacheWritePricePer1M: item.SourceCacheWritePricePer1M,
			ManualOverride:             false,
			LastSyncedAt:               now,
		}
		if err := s.agentBudgetStore.SetModelPricing(r.Context(), pricing); err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("%s/%s: %v", item.ProviderKey, item.Model, err))
			out.Skipped++
			continue
		}
		out.Applied++
	}

	status := http.StatusOK
	if len(out.Errors) > 0 && out.Applied == 0 {
		status = http.StatusInternalServerError
	}
	httpResponseJSON(w, out, status)
}

// ExportModelPricingCatalogAPI handles GET /api/v1/model-pricing/catalog.
func (s *Server) ExportModelPricingCatalogAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentBudgetStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	items, err := s.agentBudgetStore.ListModelPricing(r.Context())
	if err != nil {
		slog.Error("export model pricing catalog failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to export pricing catalog: %v", err), http.StatusInternalServerError)
		return
	}
	if items == nil {
		items = []service.ModelPricing{}
	}

	w.Header().Set("Content-Disposition", `attachment; filename="at-model-pricing-catalog.json"`)
	httpResponseJSON(w, modelPricingCatalogExport{
		Version:    1,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Items:      items,
	}, http.StatusOK)
}

// ImportModelPricingCatalogAPI handles POST /api/v1/model-pricing/catalog/import.
func (s *Server) ImportModelPricingCatalogAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentBudgetStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req modelPricingCatalogImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if len(req.Items) == 0 {
		httpResponse(w, "pricing catalog contains no items", http.StatusBadRequest)
		return
	}

	currentList, err := s.agentBudgetStore.ListModelPricing(r.Context())
	if err != nil {
		slog.Error("list model pricing for catalog import failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list current pricing: %v", err), http.StatusInternalServerError)
		return
	}
	current := make(map[string]service.ModelPricing, len(currentList))
	for _, item := range currentList {
		current[item.ProviderKey+"\x00"+item.Model] = item
	}

	now := time.Now().UTC().Format(time.RFC3339)
	var out modelPricingSyncApplyResponse
	for _, item := range req.Items {
		item.ProviderKey = strings.TrimSpace(item.ProviderKey)
		item.Model = strings.TrimSpace(item.Model)
		if item.ProviderKey == "" || item.Model == "" {
			out.Skipped++
			continue
		}
		if cur, ok := current[item.ProviderKey+"\x00"+item.Model]; ok && cur.ManualOverride && !req.OverwriteOverrides {
			out.Skipped++
			continue
		}
		if item.Source != "" {
			if item.SourcePromptPricePer1M == 0 {
				item.SourcePromptPricePer1M = item.PromptPricePer1M
			}
			if item.SourceCompletionPricePer1M == 0 {
				item.SourceCompletionPricePer1M = item.CompletionPricePer1M
			}
			if item.SourceCacheReadPricePer1M == 0 {
				item.SourceCacheReadPricePer1M = item.CacheReadPricePer1M
			}
			if item.SourceCacheWritePricePer1M == 0 {
				item.SourceCacheWritePricePer1M = item.CacheWritePricePer1M
			}
			if item.LastSyncedAt == "" {
				item.LastSyncedAt = now
			}
		}
		if err := s.agentBudgetStore.SetModelPricing(r.Context(), item); err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("%s/%s: %v", item.ProviderKey, item.Model, err))
			out.Skipped++
			continue
		}
		out.Applied++
	}

	status := http.StatusOK
	if len(out.Errors) > 0 && out.Applied == 0 {
		status = http.StatusInternalServerError
	}
	httpResponseJSON(w, out, status)
}

// PreviewModelPricingAgentAPI handles POST /api/v1/model-pricing/agent/preview.
func (s *Server) PreviewModelPricingAgentAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentBudgetStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req modelPricingAgentPreviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	items, err := s.buildAgentPricingPreview(r.Context(), req)
	if err != nil {
		slog.Error("build agent model pricing preview failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to build agent pricing preview: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, modelPricingSyncPreviewResponse{Source: "agent", Items: items}, http.StatusOK)
}

func (s *Server) buildPricingSyncApplyPreview(ctx context.Context, req modelPricingSyncApplyRequest) ([]modelPricingSyncPreviewItem, error) {
	if source, ok := modelPricingSyncSourceByName(req.Source); ok {
		return source.buildPreview(s, ctx)
	}
	if len(req.PreviewItems) == 0 {
		return nil, fmt.Errorf("preview_items are required for source %q", req.Source)
	}
	return req.PreviewItems, nil
}

func (s *Server) buildLLMPricesPricingPreview(ctx context.Context) ([]modelPricingSyncPreviewItem, error) {
	catalog, err := fetchLLMPricesModelPricing(ctx)
	if err != nil {
		return nil, err
	}
	return s.buildPricingPreview(ctx, llmPricesPricingSource, func(providerType, model string) (modelPricingSourceMatch, string, float64, bool) {
		return matchModelPricingSource(catalog, providerType, model)
	})
}

func (s *Server) buildPiDevPricingPreview(ctx context.Context) ([]modelPricingSyncPreviewItem, error) {
	catalog, err := fetchPiDevModelPricing(ctx)
	if err != nil {
		return nil, err
	}
	return s.buildPricingPreview(ctx, piDevPricingSource, func(providerType, model string) (modelPricingSourceMatch, string, float64, bool) {
		src, matchType, confidence, ok := matchPiDevPricing(catalog, providerType, model)
		if !ok {
			return modelPricingSourceMatch{}, "", 0, false
		}
		return modelPricingSourceMatch{
			Provider:             src.Provider,
			Model:                src.Model,
			URL:                  "https://pi.dev" + src.Path,
			PromptPricePer1M:     src.PromptPricePer1M,
			CompletionPricePer1M: src.CompletionPricePer1M,
			CacheReadPricePer1M:  src.CacheReadPricePer1M,
			CacheWritePricePer1M: src.CacheWritePricePer1M,
		}, matchType, confidence, true
	})
}

func (s *Server) buildPricingPreview(ctx context.Context, source string, match func(providerType, model string) (modelPricingSourceMatch, string, float64, bool)) ([]modelPricingSyncPreviewItem, error) {
	currentList, err := s.agentBudgetStore.ListModelPricing(ctx)
	if err != nil {
		return nil, fmt.Errorf("list current pricing: %w", err)
	}
	current := make(map[string]service.ModelPricing, len(currentList))
	for _, item := range currentList {
		current[item.ProviderKey+"\x00"+item.Model] = item
	}

	models := s.configuredPricingModels()
	items := make([]modelPricingSyncPreviewItem, 0, len(models))
	for _, model := range models {
		item := modelPricingSyncPreviewItem{
			ProviderKey:  model.ProviderKey,
			ProviderType: model.ProviderType,
			Model:        model.Model,
			Status:       "no_match",
		}
		if cur, ok := current[model.ProviderKey+"\x00"+model.Model]; ok {
			item.HasCurrent = true
			item.ManualOverride = cur.ManualOverride
			item.CurrentPromptPricePer1M = cur.PromptPricePer1M
			item.CurrentCompletionPricePer1M = cur.CompletionPricePer1M
			item.CurrentCacheReadPricePer1M = cur.CacheReadPricePer1M
			item.CurrentCacheWritePricePer1M = cur.CacheWritePricePer1M
		}

		if src, matchType, confidence, ok := match(model.ProviderType, model.Model); ok {
			item.Matched = true
			item.MatchType = matchType
			item.Confidence = confidence
			item.Source = source
			item.SourceProvider = src.Provider
			item.SourceModel = src.Model
			item.SourceURL = src.URL
			item.SourcePromptPricePer1M = src.PromptPricePer1M
			item.SourceCompletionPricePer1M = src.CompletionPricePer1M
			item.SourceCacheReadPricePer1M = src.CacheReadPricePer1M
			item.SourceCacheWritePricePer1M = src.CacheWritePricePer1M
			item.Status = pricingPreviewStatus(item)
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Server) buildAgentPricingPreview(ctx context.Context, req modelPricingAgentPreviewRequest) ([]modelPricingSyncPreviewItem, error) {
	providerKey := strings.TrimSpace(req.ProviderKey)
	if providerKey == "" {
		return nil, fmt.Errorf("provider_key is required")
	}

	s.providerMu.RLock()
	info, ok := s.providers[providerKey]
	s.providerMu.RUnlock()
	if !ok || info.provider == nil {
		return nil, fmt.Errorf("provider %q not found", providerKey)
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = info.defaultModel
	}
	if model == "" {
		return nil, fmt.Errorf("model is required when provider %q has no default model", providerKey)
	}

	sourceText := strings.TrimSpace(req.SourceText)
	fallbackURL := strings.TrimSpace(req.SourceURL)
	if fallbackURL != "" {
		fetched, finalURL, err := fetchPricingAgentSource(ctx, fallbackURL)
		if err != nil {
			return nil, err
		}
		fallbackURL = finalURL
		if sourceText != "" {
			sourceText += "\n\n"
		}
		sourceText += fetched
	}

	instruction := strings.TrimSpace(req.Instruction)
	if instruction == "" {
		instruction = "Find current model pricing for the configured AT provider models. Return input, output, cache read, and cache write prices in USD per 1M tokens."
	}

	targets := s.configuredPricingModels()
	prompt := buildPricingAgentPrompt(instruction, fallbackURL, sourceText, targets, req.WebSearch)
	temperature := 0.1
	opts := &service.ChatOptions{
		Temperature:    &temperature,
		ResponseFormat: map[string]any{"type": "json_object"},
	}
	if req.WebSearch {
		opts.WebSearchOptions = map[string]any{}
	}

	resp, err := info.provider.Chat(ctx, model, []service.Message{
		{Role: "system", Content: "You extract LLM model pricing into strict JSON for AT. Return only JSON."},
		{Role: "user", Content: prompt},
	}, nil, opts)
	if err != nil {
		return nil, fmt.Errorf("pricing agent chat: %w", err)
	}

	catalog, err := parsePricingAgentCatalogResponse(resp.Content, fallbackURL)
	if err != nil {
		return nil, err
	}
	return s.buildPricingPreview(ctx, "agent", func(providerType, model string) (modelPricingSourceMatch, string, float64, bool) {
		return matchModelPricingSource(catalog, providerType, model)
	})
}

func buildPricingAgentPrompt(instruction, sourceURL, sourceText string, targets []configuredPricingModel, webSearch bool) string {
	targetJSON, _ := json.MarshalIndent(targets, "", "  ")
	if len(sourceText) > pricingAgentPromptMaxChars {
		sourceText = sourceText[:pricingAgentPromptMaxChars] + "\n...[truncated]"
	}

	var b strings.Builder
	b.WriteString("Instruction:\n")
	b.WriteString(instruction)
	b.WriteString("\n\nConfigured AT provider models to price:\n")
	b.Write(targetJSON)
	b.WriteString("\n\nReturn exactly this JSON shape and no markdown:\n")
	b.WriteString(`{"items":[{"provider":"openai","model":"gpt-4o","name":"optional display name","url":"https://source.example/model","prompt_price_per_1m":2.5,"completion_price_per_1m":10,"cache_read_price_per_1m":1.25,"cache_write_price_per_1m":0}]}`)
	b.WriteString("\n\nRules:\n- Prices must be USD per 1M tokens. Convert from per-token, per-1K, or per-M rates if needed.\n- Use 0 only when a cache read/write price is unavailable or not charged.\n- Prefer source provider/model names from the pricing source.\n- Include one item per source model you can confidently price.\n")
	if sourceURL != "" {
		b.WriteString("\nSource URL:\n")
		b.WriteString(sourceURL)
		b.WriteString("\n")
	}
	if sourceText != "" {
		b.WriteString("\nSource content:\n")
		b.WriteString(sourceText)
		b.WriteString("\n")
	} else if webSearch {
		b.WriteString("\nUse web search if the selected model/provider supports it.\n")
	}
	return b.String()
}

func fetchPricingAgentSource(ctx context.Context, rawURL string) (string, string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", "", fmt.Errorf("invalid source_url %q", rawURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", fmt.Errorf("source_url must use http or https")
	}
	parsed = normalizePricingSourceURL(parsed)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", "", err
	}
	client := http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("fetch pricing source: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("fetch pricing source: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, pricingAgentSourceMaxBytes+1))
	if err != nil {
		return "", "", fmt.Errorf("read pricing source: %w", err)
	}
	if len(body) > pricingAgentSourceMaxBytes {
		body = body[:pricingAgentSourceMaxBytes]
	}
	text := string(body)
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "html") || strings.Contains(strings.ToLower(text[:min(len(text), 512)]), "<html") {
		text = htmlVisibleText(bytes.NewReader(body))
	}
	return text, resp.Request.URL.String(), nil
}

func normalizePricingSourceURL(u *url.URL) *url.URL {
	if !strings.EqualFold(u.Host, "github.com") {
		return u
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 5 || parts[2] != "blob" {
		return u
	}
	raw := *u
	raw.Host = "raw.githubusercontent.com"
	raw.Path = "/" + strings.Join(append([]string{parts[0], parts[1], parts[3]}, parts[4:]...), "/")
	raw.RawQuery = ""
	raw.Fragment = ""
	return &raw
}

func htmlVisibleText(r io.Reader) string {
	z := html.NewTokenizer(r)
	var b strings.Builder
	skipDepth := 0
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return strings.Join(strings.Fields(b.String()), " ")
		case html.StartTagToken:
			t := z.Token()
			if t.Data == "script" || t.Data == "style" || t.Data == "noscript" {
				skipDepth++
			}
		case html.EndTagToken:
			t := z.Token()
			if skipDepth > 0 && (t.Data == "script" || t.Data == "style" || t.Data == "noscript") {
				skipDepth--
			}
		case html.TextToken:
			if skipDepth > 0 {
				continue
			}
			part := strings.TrimSpace(string(z.Text()))
			if part != "" {
				b.WriteByte(' ')
				b.WriteString(part)
			}
		}
	}
}

func parsePricingAgentCatalogResponse(content, fallbackURL string) ([]modelPricingSourceItem, error) {
	payload := pricingAgentJSONPayload(content)
	var obj struct {
		Items   []modelPricingSourceItem `json:"items"`
		Models  []modelPricingSourceItem `json:"models"`
		Catalog []modelPricingSourceItem `json:"catalog"`
	}
	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		var arr []modelPricingSourceItem
		if arrErr := json.Unmarshal([]byte(payload), &arr); arrErr != nil {
			return nil, fmt.Errorf("parse pricing agent JSON: %w", err)
		}
		return normalizePricingSourceItems(arr, fallbackURL), nil
	}

	items := append([]modelPricingSourceItem{}, obj.Items...)
	items = append(items, obj.Models...)
	items = append(items, obj.Catalog...)
	items = normalizePricingSourceItems(items, fallbackURL)
	if len(items) == 0 {
		return nil, fmt.Errorf("pricing agent returned no usable pricing items")
	}
	return items, nil
}

func pricingAgentJSONPayload(content string) string {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		content = strings.TrimPrefix(content, "```json")
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSuffix(strings.TrimSpace(content), "```")
		content = strings.TrimSpace(content)
	}
	if strings.HasPrefix(content, "{") || strings.HasPrefix(content, "[") {
		return content
	}
	if start := strings.Index(content, "{"); start >= 0 {
		if end := strings.LastIndex(content, "}"); end > start {
			return content[start : end+1]
		}
	}
	if start := strings.Index(content, "["); start >= 0 {
		if end := strings.LastIndex(content, "]"); end > start {
			return content[start : end+1]
		}
	}
	return content
}

func normalizePricingSourceItems(items []modelPricingSourceItem, fallbackURL string) []modelPricingSourceItem {
	out := make([]modelPricingSourceItem, 0, len(items))
	for _, item := range items {
		item.Provider = strings.TrimSpace(firstNonEmpty(item.Provider, item.SourceProvider))
		item.Model = strings.TrimSpace(firstNonEmpty(item.Model, item.SourceModel, item.Name))
		item.URL = strings.TrimSpace(firstNonEmpty(item.URL, item.SourceURL, fallbackURL))
		if item.PromptPricePer1M == 0 {
			item.PromptPricePer1M = item.InputPricePer1M
		}
		if item.CompletionPricePer1M == 0 {
			item.CompletionPricePer1M = item.OutputPricePer1M
		}
		item.PromptPricePer1M = nonNegativePrice(item.PromptPricePer1M)
		item.CompletionPricePer1M = nonNegativePrice(item.CompletionPricePer1M)
		item.CacheReadPricePer1M = nonNegativePrice(item.CacheReadPricePer1M)
		item.CacheWritePricePer1M = nonNegativePrice(item.CacheWritePricePer1M)
		if item.Provider == "" || item.Model == "" {
			continue
		}
		if item.PromptPricePer1M == 0 && item.CompletionPricePer1M == 0 && item.CacheReadPricePer1M == 0 && item.CacheWritePricePer1M == 0 {
			continue
		}
		out = append(out, item)
	}
	return out
}

func listModelPricingSyncSourceInfos() []modelPricingSyncSourceInfo {
	out := make([]modelPricingSyncSourceInfo, 0, len(modelPricingSyncSources))
	for _, source := range modelPricingSyncSources {
		out = append(out, source.modelPricingSyncSourceInfo)
	}
	return out
}

func modelPricingSyncSourceByName(name string) (modelPricingSyncSource, bool) {
	name = strings.TrimSpace(name)
	for _, source := range modelPricingSyncSources {
		if strings.EqualFold(source.Source, name) {
			return source, true
		}
	}
	return modelPricingSyncSource{}, false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func nonNegativePrice(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}

func matchModelPricingSource(catalog []modelPricingSourceItem, providerType, model string) (modelPricingSourceMatch, string, float64, bool) {
	providers := pricingProviderAliases(providerType)
	for _, provider := range providers {
		for _, item := range catalog {
			if strings.EqualFold(item.Provider, provider) && item.Model == model {
				return sourceItemMatch(item), "provider_model", 1, true
			}
		}
	}

	var matches []modelPricingSourceItem
	for _, item := range catalog {
		if item.Model == model {
			matches = append(matches, item)
		}
	}
	if len(matches) == 1 {
		return sourceItemMatch(matches[0]), "model", 0.8, true
	}

	normModel := normalizePricingModelName(model)
	for _, provider := range providers {
		for _, item := range catalog {
			if strings.EqualFold(item.Provider, provider) && normalizePricingModelName(item.Model) == normModel {
				return sourceItemMatch(item), "normalized_provider_model", 0.9, true
			}
		}
	}

	matches = matches[:0]
	for _, item := range catalog {
		if normalizePricingModelName(item.Model) == normModel {
			matches = append(matches, item)
		}
	}
	if len(matches) == 1 {
		return sourceItemMatch(matches[0]), "normalized_model", 0.7, true
	}

	return modelPricingSourceMatch{}, "", 0, false
}

func sourceItemMatch(item modelPricingSourceItem) modelPricingSourceMatch {
	return modelPricingSourceMatch{
		Provider:             item.Provider,
		Model:                item.Model,
		URL:                  item.URL,
		PromptPricePer1M:     item.PromptPricePer1M,
		CompletionPricePer1M: item.CompletionPricePer1M,
		CacheReadPricePer1M:  item.CacheReadPricePer1M,
		CacheWritePricePer1M: item.CacheWritePricePer1M,
	}
}

func (s *Server) configuredPricingModels() []configuredPricingModel {
	s.providerMu.RLock()
	defer s.providerMu.RUnlock()
	models := make([]configuredPricingModel, 0, len(s.providers))
	for key, info := range s.providers {
		if len(info.models) == 0 {
			if info.defaultModel != "" {
				models = append(models, configuredPricingModel{ProviderKey: key, ProviderType: info.providerType, Model: info.defaultModel})
			}
			continue
		}
		for _, model := range info.models {
			models = append(models, configuredPricingModel{ProviderKey: key, ProviderType: info.providerType, Model: model})
		}
	}
	return models
}

func pricingPreviewStatus(item modelPricingSyncPreviewItem) string {
	if !item.HasCurrent {
		return "missing"
	}
	changed := !samePrice(item.CurrentPromptPricePer1M, item.SourcePromptPricePer1M) ||
		!samePrice(item.CurrentCompletionPricePer1M, item.SourceCompletionPricePer1M) ||
		!samePrice(item.CurrentCacheReadPricePer1M, item.SourceCacheReadPricePer1M) ||
		!samePrice(item.CurrentCacheWritePricePer1M, item.SourceCacheWritePricePer1M)
	if item.ManualOverride && changed {
		return "override"
	}
	if changed {
		return "update"
	}
	return "current"
}

func samePrice(a, b float64) bool {
	return math.Abs(a-b) < 0.0000001
}

func matchPiDevPricing(catalog []piDevModelPricing, providerType, model string) (piDevModelPricing, string, float64, bool) {
	providers := piDevProviderAliases(providerType)
	for _, provider := range providers {
		for _, item := range catalog {
			if item.Provider == provider && item.Model == model {
				return item, "provider_model", 1, true
			}
		}
	}

	var matches []piDevModelPricing
	for _, item := range catalog {
		if item.Model == model {
			matches = append(matches, item)
		}
	}
	if len(matches) == 1 {
		return matches[0], "model", 0.8, true
	}

	normModel := normalizePricingModelName(model)
	for _, provider := range providers {
		for _, item := range catalog {
			if item.Provider == provider && normalizePricingModelName(item.Model) == normModel {
				return item, "normalized_provider_model", 0.9, true
			}
		}
	}

	return piDevModelPricing{}, "", 0, false
}

func piDevProviderAliases(providerType string) []string {
	switch strings.ToLower(providerType) {
	case "anthropic", "antropic":
		return []string{"anthropic"}
	case "gemini", "google":
		return []string{"google"}
	case "vertex", "google-vertex":
		return []string{"google-vertex"}
	case "minimax":
		return []string{"minimax", "minimax-cn"}
	case "openai":
		return []string{"openai"}
	default:
		if providerType == "" {
			return nil
		}
		return []string{providerType}
	}
}

func pricingProviderAliases(providerType string) []string {
	switch strings.ToLower(providerType) {
	case "anthropic", "antropic":
		return []string{"anthropic"}
	case "gemini", "google", "vertex", "vertex-gemini", "google-vertex":
		return []string{"google", "google-vertex"}
	case "bedrock", "amazon", "aws":
		return []string{"amazon", "aws", "bedrock"}
	case "minimax":
		return []string{"minimax", "minimax-cn"}
	case "openai":
		return []string{"openai"}
	case "xai", "x.ai":
		return []string{"xai", "x.ai"}
	default:
		if providerType == "" {
			return nil
		}
		return []string{providerType}
	}
}

func normalizePricingModelName(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer(".", "-", "_", "-", ":", "-", "/", "-").Replace(s)
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

func fetchPiDevModelPricing(ctx context.Context) ([]piDevModelPricing, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, piDevModelsURL, nil)
	if err != nil {
		return nil, err
	}
	client := http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch pi.dev models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch pi.dev models: status %d", resp.StatusCode)
	}
	body := io.LimitReader(resp.Body, 8<<20)
	items, err := parsePiDevModelPricing(body)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("pi.dev models page contained no pricing rows")
	}
	return items, nil
}

func fetchLLMPricesModelPricing(ctx context.Context) ([]modelPricingSourceItem, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, llmPricesCurrentURL, nil)
	if err != nil {
		return nil, err
	}
	client := http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch llm-prices current catalog: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch llm-prices current catalog: status %d", resp.StatusCode)
	}
	body := io.LimitReader(resp.Body, 8<<20)
	items, err := parseLLMPricesModelPricing(body)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("llm-prices catalog contained no pricing rows")
	}
	return items, nil
}

func parseLLMPricesModelPricing(r io.Reader) ([]modelPricingSourceItem, error) {
	var catalog struct {
		Prices []struct {
			ID          string   `json:"id"`
			Vendor      string   `json:"vendor"`
			Name        string   `json:"name"`
			Input       float64  `json:"input"`
			Output      float64  `json:"output"`
			InputCached *float64 `json:"input_cached"`
		} `json:"prices"`
	}
	if err := json.NewDecoder(r).Decode(&catalog); err != nil {
		return nil, fmt.Errorf("parse llm-prices JSON: %w", err)
	}

	items := make([]modelPricingSourceItem, 0, len(catalog.Prices))
	for _, price := range catalog.Prices {
		cacheRead := 0.0
		if price.InputCached != nil {
			cacheRead = *price.InputCached
		}
		items = append(items, modelPricingSourceItem{
			Provider:             price.Vendor,
			Model:                price.ID,
			Name:                 price.Name,
			URL:                  llmPricesCurrentURL,
			PromptPricePer1M:     price.Input,
			CompletionPricePer1M: price.Output,
			CacheReadPricePer1M:  cacheRead,
			CacheWritePricePer1M: 0,
			InputPricePer1M:      price.Input,
			OutputPricePer1M:     price.Output,
		})
	}
	return normalizePricingSourceItems(items, llmPricesCurrentURL), nil
}

func parsePiDevModelPricing(r io.Reader) ([]piDevModelPricing, error) {
	z := html.NewTokenizer(r)
	var items []piDevModelPricing
	var row piDevModelPricing
	var cells []string
	var text strings.Builder
	inRow := false
	inCell := false
	cellDepth := 0

	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if err := z.Err(); err != nil && err != io.EOF {
				return nil, fmt.Errorf("parse pi.dev html: %w", err)
			}
			return items, nil

		case html.StartTagToken:
			t := z.Token()
			if t.Data == "tr" && attr(t, "data-model-row") == "true" {
				inRow = true
				row = piDevModelPricing{
					Provider: attr(t, "data-model-provider"),
					Model:    attr(t, "data-model-id"),
					Name:     attr(t, "data-model-name"),
					Path:     attr(t, "data-model-path"),
				}
				cells = nil
				continue
			}
			if inRow && t.Data == "td" && !inCell {
				inCell = true
				cellDepth = 1
				text.Reset()
				continue
			}
			if inCell {
				cellDepth++
			}

		case html.TextToken:
			if inCell {
				part := strings.TrimSpace(string(z.Text()))
				if part != "" {
					if text.Len() > 0 {
						text.WriteByte(' ')
					}
					text.WriteString(part)
				}
			}

		case html.EndTagToken:
			t := z.Token()
			if inCell {
				cellDepth--
				if cellDepth == 0 {
					inCell = false
					cells = append(cells, strings.Join(strings.Fields(text.String()), " "))
				}
				continue
			}
			if inRow && t.Data == "tr" {
				inRow = false
				if row.Provider != "" && row.Model != "" && len(cells) >= 6 {
					row.PromptPricePer1M = parsePiDevPrice(cells[2])
					row.CompletionPricePer1M = parsePiDevPrice(cells[3])
					row.CacheReadPricePer1M = parsePiDevPrice(cells[4])
					row.CacheWritePricePer1M = parsePiDevPrice(cells[5])
					items = append(items, row)
				}
			}
		}
	}
}

func attr(t html.Token, key string) string {
	for _, a := range t.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func parsePiDevPrice(s string) float64 {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "$")
	s = strings.ReplaceAll(s, ",", "")
	if s == "" || s == "-" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}
