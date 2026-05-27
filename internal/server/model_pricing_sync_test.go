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

func TestParsePiDevModelPricing(t *testing.T) {
	html := `<table><tbody>
<tr data-model-row="true" data-model-provider="anthropic" data-model-id="claude-sonnet-4-5" data-model-name="claude sonnet" data-model-path="/models/anthropic/claude-sonnet-4-5">
<td><a>Claude Sonnet</a><code>claude-sonnet-4-5</code></td><td>200,000</td><td>$3</td><td>$15</td><td>$0.3</td><td>$3.75</td>
</tr>
</tbody></table>`

	items, err := parsePiDevModelPricing(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parsePiDevModelPricing: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	got := items[0]
	if got.Provider != "anthropic" || got.Model != "claude-sonnet-4-5" {
		t.Fatalf("model = %s/%s", got.Provider, got.Model)
	}
	if got.PromptPricePer1M != 3 || got.CompletionPricePer1M != 15 || got.CacheReadPricePer1M != 0.3 || got.CacheWritePricePer1M != 3.75 {
		t.Fatalf("prices = %+v", got)
	}
}

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
	if source.Source != llmPricesPricingSource || source.URL != llmPricesCurrentURL || source.buildPreview == nil {
		t.Fatalf("source = %+v", source)
	}

	infos := listModelPricingSyncSourceInfos()
	if len(infos) < 2 {
		t.Fatalf("len(infos) = %d, want at least 2", len(infos))
	}
}

func TestMatchPiDevPricingProviderAlias(t *testing.T) {
	catalog := []piDevModelPricing{
		{Provider: "google", Model: "gemini-2.5-pro"},
		{Provider: "anthropic", Model: "claude-sonnet-4-5"},
	}

	got, matchType, confidence, ok := matchPiDevPricing(catalog, "gemini", "gemini-2.5-pro")
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
