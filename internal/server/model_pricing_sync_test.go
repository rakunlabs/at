package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

func TestParseLLMPricesModelPricing(t *testing.T) {
	json := `{"updated_at":"2026-05-19","prices":[
{"id":"gpt-4o","vendor":"openai","name":"GPT-4o","input":2.5,"output":10,"input_cached":1.25},
{"id":"claude-sonnet-4.5","vendor":"anthropic","name":"Claude Sonnet 4.5","input":3,"output":15,"input_cached":null}
]}`

	items, err := parseLLMPricesModelPricing(strings.NewReader(json))
	if err != nil {
		t.Fatalf("parseLLMPricesModelPricing: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	got := items[0]
	if got.Provider != "openai" || got.Model != "gpt-4o" || got.URL != llmPricesCurrentURL {
		t.Fatalf("item = %+v", got)
	}
	if got.PromptPricePer1M != 2.5 || got.CompletionPricePer1M != 10 || got.CacheReadPricePer1M != 1.25 || got.CacheWritePricePer1M != 0 {
		t.Fatalf("prices = %+v", got)
	}
	if items[1].CacheReadPricePer1M != 0 {
		t.Fatalf("null cached price = %v, want 0", items[1].CacheReadPricePer1M)
	}
}

func TestModelPricingSyncSourceRegistry(t *testing.T) {
	source, ok := modelPricingSyncSourceByName(" LLM-PRICES ")
	if !ok {
		t.Fatal("expected llm-prices source")
	}
	if source.Source != llmPricesPricingSource || source.URL != llmPricesCurrentURL || source.fetchCatalog == nil {
		t.Fatalf("source = %+v", source)
	}

	infos := listModelPricingSyncSourceInfos()
	if len(infos) != 2 || infos[0].Source != atPricingSource {
		t.Fatalf("expected AT Pricing first and legacy source retained: %+v", infos)
	}
	if _, ok := modelPricingSyncSourceByName("pi.dev"); ok {
		t.Fatal("removed pricing source is still registered")
	}
}

func TestApplyCachePricingDefaultsAnthropic(t *testing.T) {
	got := applyCachePricingDefaults("anthropic", modelPricingSourceMatch{PromptPricePer1M: 5})
	if got.CacheReadPricePer1M != 0.5 {
		t.Fatalf("cache read price = %v, want 0.5", got.CacheReadPricePer1M)
	}
	if got.CacheWritePricePer1M != 6.25 {
		t.Fatalf("cache write price = %v, want 6.25", got.CacheWritePricePer1M)
	}
}

func TestMatchModelPricingSourceProviderAlias(t *testing.T) {
	catalog := []modelPricingSourceItem{
		{Provider: "google", Model: "gemini-2.5-pro"},
		{Provider: "anthropic", Model: "claude-sonnet-4-5"},
	}

	got, matchType, confidence, ok := matchModelPricingSource(catalog, "gemini", "gemini-2.5-pro")
	if !ok {
		t.Fatal("expected match")
	}
	if got.Provider != "google" || matchType != "provider_model" || confidence != 1 {
		t.Fatalf("match = %+v %s %f", got, matchType, confidence)
	}
}

func TestMatchModelPricingSourceVertexGoogleAlias(t *testing.T) {
	catalog := []modelPricingSourceItem{
		{Provider: "google", Model: "gemini-2.5-pro", PromptPricePer1M: 1.25, CompletionPricePer1M: 10},
	}

	got, matchType, confidence, ok := matchModelPricingSource(catalog, "vertex-gemini", "gemini-2.5-pro")
	if !ok {
		t.Fatal("expected match")
	}
	if got.Provider != "google" || matchType != "provider_model" || confidence != 1 {
		t.Fatalf("match = %+v %s %f", got, matchType, confidence)
	}
}

func TestPricingPreviewStatusOverride(t *testing.T) {
	status := pricingPreviewStatus(modelPricingSyncPreviewItem{
		HasCurrent:                  true,
		ManualOverride:              true,
		CurrentPromptPricePer1M:     2,
		SourcePromptPricePer1M:      3,
		CurrentCompletionPricePer1M: 8,
		SourceCompletionPricePer1M:  8,
		CurrentCacheReadPricePer1M:  0.2,
		SourceCacheReadPricePer1M:   0.2,
		CurrentCacheWritePricePer1M: 0,
		SourceCacheWritePricePer1M:  0,
	})
	if status != "override" {
		t.Fatalf("status = %q, want override", status)
	}
}

func TestParsePricingAgentCatalogResponse(t *testing.T) {
	content := "```json\n{\"items\":[{\"source_provider\":\"openai\",\"source_model\":\"gpt-4o\",\"input_price_per_1m\":2.5,\"output_price_per_1m\":10}]}\n```"

	items, err := parsePricingAgentCatalogResponse(content, "https://example.com/pricing")
	if err != nil {
		t.Fatalf("parsePricingAgentCatalogResponse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	got := items[0]
	if got.Provider != "openai" || got.Model != "gpt-4o" || got.URL != "https://example.com/pricing" {
		t.Fatalf("normalized item = %+v", got)
	}
	if got.PromptPricePer1M != 2.5 || got.CompletionPricePer1M != 10 {
		t.Fatalf("prices = %+v", got)
	}
}

func TestNormalizePricingSourceURLGitHubBlob(t *testing.T) {
	parsed, err := url.Parse("https://github.com/acme/pricing/blob/main/catalog.json?plain=1")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	got := normalizePricingSourceURL(parsed).String()
	want := "https://raw.githubusercontent.com/acme/pricing/main/catalog.json"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

func TestPreviewModelPricingAgentAPI(t *testing.T) {
	llm := &pricingTestProvider{content: `{"items":[{"provider":"openai","model":"gpt-4o","prompt_price_per_1m":2.5,"completion_price_per_1m":10,"cache_read_price_per_1m":1.25,"cache_write_price_per_1m":0}]}`}
	s := &Server{
		agentBudgetStore: &pricingTestBudgetStore{},
		providers: map[string]ProviderInfo{
			"pricing-agent": {provider: llm, providerType: "openai", defaultModel: "gpt-agent"},
			"openai-prod":   {providerType: "openai", defaultModel: "gpt-4o"},
		},
	}
	body := `{"provider_key":"pricing-agent","source_text":"OpenAI GPT-4o is $2.50 input and $10 output per 1M tokens."}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/model-pricing/agent/preview", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.PreviewModelPricingAgentAPI(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var res modelPricingSyncPreviewResponse
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var matched *modelPricingSyncPreviewItem
	for i := range res.Items {
		if res.Items[i].ProviderKey == "openai-prod" && res.Items[i].Model == "gpt-4o" {
			matched = &res.Items[i]
			break
		}
	}
	if matched == nil || !matched.Matched {
		t.Fatalf("openai-prod/gpt-4o not matched in %+v", res.Items)
	}
	if matched.Source != "agent" || matched.SourceProvider != "openai" || matched.SourcePromptPricePer1M != 2.5 {
		t.Fatalf("matched item = %+v", matched)
	}
}

func TestApplyModelPricingSyncUsesPreviewSource(t *testing.T) {
	store := &pricingTestBudgetStore{}
	s := &Server{agentBudgetStore: store}
	body := `{"source":"agent","items":[{"provider_key":"openai-prod","model":"gpt-4o"}],"preview_items":[{"provider_key":"openai-prod","provider_type":"openai","model":"gpt-4o","matched":true,"status":"missing","source":"agent","source_provider":"openai","source_model":"gpt-4o","source_url":"https://example.com/pricing","source_prompt_price_per_1m":2.5,"source_completion_price_per_1m":10}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/model-pricing/sync/apply", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.ApplyModelPricingSyncAPI(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if len(store.set) != 1 {
		t.Fatalf("len(set) = %d, want 1", len(store.set))
	}
	got := store.set[0]
	if got.Source != "agent" || got.SourceURL != "https://example.com/pricing" || got.PromptPricePer1M != 2.5 {
		t.Fatalf("set pricing = %+v", got)
	}
}

func TestModelPricingManualMappingFlow(t *testing.T) {
	catalog := []modelPricingSourceItem{{Provider: "anthropic", Model: "claude-sonnet-4.5", PromptPricePer1M: 3, CompletionPricePer1M: 15}}
	original := modelPricingSyncSources
	modelPricingSyncSources = []modelPricingSyncSource{{
		modelPricingSyncSourceInfo: modelPricingSyncSourceInfo{Source: llmPricesPricingSource},
		fetchCatalog:               func(context.Context) ([]modelPricingSourceItem, error) { return catalog, nil },
	}}
	t.Cleanup(func() { modelPricingSyncSources = original })

	for _, tt := range []struct {
		name        string
		override    bool
		overwrite   bool
		wantApplied int
	}{
		{name: "deployment alias", wantApplied: 1},
		{name: "protected override", override: true},
		{name: "overwrite enabled", override: true, overwrite: true, wantApplied: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := &pricingTestBudgetStore{}
			if tt.override {
				store.pricing = []service.ModelPricing{{ProviderKey: "prod", Model: "my-deployment", ManualOverride: true, PromptPricePer1M: 99}}
			}
			s := &Server{agentBudgetStore: store, providers: map[string]ProviderInfo{
				"prod": {providerType: "anthropic", defaultModel: "my-deployment"},
			}}
			w := httptest.NewRecorder()
			s.PreviewModelPricingSyncAPI(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"source":"llm-prices"}`)))
			var preview modelPricingSyncPreviewResponse
			if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil || w.Code != http.StatusOK {
				t.Fatalf("preview: %d %s, %v", w.Code, w.Body.String(), err)
			}
			if preview.Source != llmPricesPricingSource || len(preview.Catalog) != 1 || len(preview.Items) != 1 || preview.Items[0].Matched {
				t.Fatalf("unexpected initial preview: %+v", preview)
			}
			req := modelPricingSyncApplyRequest{
				Source:             llmPricesPricingSource,
				OverwriteOverrides: tt.overwrite,
				Items:              []modelPricingSyncKeyItem{{ProviderKey: "prod", Model: "my-deployment", SourceProvider: "anthropic", SourceModel: "claude-sonnet-4.5"}},
				// A registered source must ignore browser-supplied prices.
				PreviewItems: []modelPricingSyncPreviewItem{{ProviderKey: "prod", Model: "my-deployment", Matched: true, SourcePromptPricePer1M: 999}},
			}
			body, _ := json.Marshal(req)
			w = httptest.NewRecorder()
			s.ApplyModelPricingSyncAPI(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))))
			if w.Code != http.StatusOK || len(store.set) != tt.wantApplied {
				t.Fatalf("apply: %d %s, writes=%d", w.Code, w.Body.String(), len(store.set))
			}
			if tt.wantApplied == 0 {
				return
			}
			got := store.set[0]
			if got.Source != llmPricesPricingSource || got.SourceModel != "claude-sonnet-4.5" || got.PromptPricePer1M != 3 || !samePrice(got.CacheReadPricePer1M, 0.3) || got.CacheWritePricePer1M != 3.75 || got.ManualOverride {
				t.Fatalf("saved pricing: %+v", got)
			}
			store.pricing = store.set
			refreshed := append([]modelPricingSourceItem(nil), catalog...)
			refreshed[0].PromptPricePer1M = 4
			items, err := s.buildCatalogPricingPreview(context.Background(), llmPricesPricingSource, refreshed, nil)
			if err != nil || len(items) != 1 || items[0].MatchType != "saved_mapping" || items[0].SourcePromptPricePer1M != 4 || items[0].Status != "update" {
				t.Fatalf("saved match refresh: %+v, %v", items, err)
			}
		})
	}
}

func TestModelPricingMappingMissingCatalogEntry(t *testing.T) {
	s := &Server{agentBudgetStore: &pricingTestBudgetStore{pricing: []service.ModelPricing{{
		ProviderKey: "prod", Model: "gpt-4o", Source: llmPricesPricingSource, SourceProvider: "openai", SourceModel: "removed-model",
	}}}, providers: map[string]ProviderInfo{"prod": {providerType: "openai", defaultModel: "gpt-4o"}}}
	catalog := []modelPricingSourceItem{{Provider: "openai", Model: "gpt-4o", PromptPricePer1M: 2.5}}
	items, err := s.buildCatalogPricingPreview(context.Background(), llmPricesPricingSource, catalog, nil)
	if err != nil || len(items) != 1 || items[0].Matched || items[0].Status != "no_match" {
		t.Fatalf("missing saved mapping must not silently auto-match: %+v, %v", items, err)
	}
	_, err = s.buildCatalogPricingPreview(context.Background(), llmPricesPricingSource, catalog, []modelPricingSyncKeyItem{{
		ProviderKey: "prod", Model: "gpt-4o", SourceProvider: "openai", SourceModel: "invalid",
	}})
	if err == nil {
		t.Fatal("expected invalid explicit catalog selection to fail")
	}
}

func TestImportModelPricingCatalogSkipsManualOverride(t *testing.T) {
	store := &pricingTestBudgetStore{pricing: []service.ModelPricing{{ProviderKey: "openai-prod", Model: "gpt-4o", ManualOverride: true}}}
	s := &Server{agentBudgetStore: store}
	body := `{"items":[{"provider_key":"openai-prod","model":"gpt-4o","prompt_price_per_1m":2.5,"completion_price_per_1m":10}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/model-pricing/catalog/import", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.ImportModelPricingCatalogAPI(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if len(store.set) != 0 {
		t.Fatalf("len(set) = %d, want 0", len(store.set))
	}
	var res modelPricingSyncApplyResponse
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if res.Applied != 0 || res.Skipped != 1 {
		t.Fatalf("response = %+v", res)
	}
}

type pricingTestProvider struct {
	content string
}

func (p *pricingTestProvider) Chat(context.Context, string, []service.Message, []service.Tool, *service.ChatOptions) (*service.LLMResponse, error) {
	return &service.LLMResponse{Content: p.content}, nil
}

type pricingTestBudgetStore struct {
	pricing []service.ModelPricing
	set     []service.ModelPricing
}

func (s *pricingTestBudgetStore) GetAgentBudget(context.Context, string) (*service.AgentBudget, error) {
	return nil, nil
}

func (s *pricingTestBudgetStore) SetAgentBudget(context.Context, service.AgentBudget) error {
	return nil
}

func (s *pricingTestBudgetStore) ListAgentBudgets(context.Context) ([]service.AgentBudget, error) {
	return nil, nil
}

func (s *pricingTestBudgetStore) RecordAgentUsage(context.Context, service.AgentUsageRecord) error {
	return nil
}

func (s *pricingTestBudgetStore) GetAgentUsage(context.Context, string, *query.Query) (*service.ListResult[service.AgentUsageRecord], error) {
	return nil, nil
}

func (s *pricingTestBudgetStore) GetAgentTotalSpend(context.Context, string) (float64, error) {
	return 0, nil
}

func (s *pricingTestBudgetStore) ListModelPricing(context.Context) ([]service.ModelPricing, error) {
	return s.pricing, nil
}

func (s *pricingTestBudgetStore) SetModelPricing(_ context.Context, pricing service.ModelPricing) error {
	s.set = append(s.set, pricing)
	return nil
}

func (s *pricingTestBudgetStore) DeleteModelPricing(context.Context, string) error {
	return nil
}

func (s *pricingTestBudgetStore) ResetModelPricingOverride(context.Context, string) error {
	return nil
}
