package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func testATCatalog(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../pricing/index.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestATPricingFetch(t *testing.T) {
	valid := testATCatalog(t)
	for _, tt := range []struct {
		name      string
		status    int
		body      string
		cancel    bool
		wantError bool
	}{
		{name: "published catalog", status: 200, body: valid},
		{name: "not published", status: 404, body: "Not Found", wantError: true},
		{name: "upstream failure", status: 503, wantError: true},
		{name: "malformed", status: 200, body: "{}", wantError: true},
		{name: "trailing data", status: 200, body: valid + "{}", wantError: true},
		{name: "oversized", status: 200, body: valid + strings.Repeat(" ", atPricingMaxBytes), wantError: true},
		{name: "cancelled", status: 200, body: valid, cancel: true, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer srv.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancel {
				cancel()
			}
			items, err := fetchATModelPricingURL(ctx, srv.Client(), srv.URL)
			if (err != nil) != tt.wantError {
				t.Fatalf("error = %v", err)
			}
			if tt.wantError && len(items) != 0 {
				t.Fatal("failed fetch returned partial catalog")
			}
			if !tt.wantError && len(items) == 0 {
				t.Fatal("missing catalog")
			}
		})
	}
}

func TestATPricingMatching(t *testing.T) {
	catalog, err := parseATModelPricing(strings.NewReader(testATCatalog(t)))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		provider, model, want string
	}{
		{"openai", "gpt-4o", "gpt-4o"},
		{"anthropic", "claude-sonnet-4-5-20250929", "claude-sonnet-4-5"},
		{"openai", "MiniMax-M2.7", "MiniMax-M2.7"},
		{"minimax", "MiniMax-M2.7", "MiniMax-M2.7"},
		{"azure", "gpt-4o", ""},
		{"bedrock", "claude-sonnet-4-5", ""},
		{"vertex-gemini", "claude-sonnet-4-5", ""},
		{"anthropic", "gpt-4o", ""},
		{"openai", "gpt-4o-2024-05-13", ""},
		{"openai", "grok-4.6", ""},
		{"openai", "grok-4.6@short", ""},
		{"openai", "deepseek-flash", ""},
	} {
		t.Run(tt.provider+"/"+tt.model, func(t *testing.T) {
			got, _, _, ok := matchModelPricingSource(catalog, tt.provider, tt.model)
			if ok != (tt.want != "") || got.Model != tt.want {
				t.Fatalf("match = %+v, %v; want %q", got, ok, tt.want)
			}
		})
	}
}

func TestATPricingNullZeroAndCacheDefaults(t *testing.T) {
	body := `{"version":1,"currency":"USD","unit":"per_1m_tokens","providers":[{"provider":"anthropic","models":[
	{"model":"unknown","source_url":"https://example.com/pricing","verified_at":"2026-09-13","notes":"Unknown cache rate","input":3,"output":15,"cache_read":null,"cache_write":0},
	{"model":"free","source_url":"https://example.com/pricing","verified_at":"2026-09-13","notes":"Free","input":0,"output":0,"cache_read":0,"cache_write":0},
	{"model":"paid","source_url":"https://example.com/pricing","verified_at":"2026-09-13","notes":"Free cache","input":3,"output":15,"cache_read":0,"cache_write":0}]}]}`
	items, err := parseATModelPricing(strings.NewReader(body))
	if err != nil || len(items) != 2 || items[0].Model != "free" {
		t.Fatalf("unknown/free handling: %+v, %v", items, err)
	}
	got := applyCachePricingDefaults("anthropic", sourceItemMatch(items[1]))
	if got.CacheReadPricePer1M != 0 || got.CacheWritePricePer1M != 0 {
		t.Fatalf("explicit free cache was overwritten: %+v", got)
	}
}

func TestATPricingDefaultPreviewApply(t *testing.T) {
	catalog, err := parseATModelPricing(strings.NewReader(testATCatalog(t)))
	if err != nil {
		t.Fatal(err)
	}
	original := modelPricingSyncSources
	modelPricingSyncSources = []modelPricingSyncSource{{
		modelPricingSyncSourceInfo: modelPricingSyncSourceInfo{Source: atPricingSource},
		fetchCatalog:               func(context.Context) ([]modelPricingSourceItem, error) { return catalog, nil },
	}}
	t.Cleanup(func() { modelPricingSyncSources = original })
	for _, override := range []bool{false, true} {
		t.Run(fmt.Sprintf("override=%v", override), func(t *testing.T) {
			store := &pricingTestBudgetStore{pricing: []service.ModelPricing{{ProviderKey: "prod", Model: "custom", ManualOverride: override, PromptPricePer1M: 99}}}
			s := &Server{agentBudgetStore: store, providers: map[string]ProviderInfo{"prod": {providerType: "anthropic", defaultModel: "custom"}}}
			w := httptest.NewRecorder()
			s.PreviewModelPricingSyncAPI(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)))
			var preview modelPricingSyncPreviewResponse
			if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil || w.Code != 200 || preview.Source != atPricingSource {
				t.Fatalf("default preview: %d %s, %v", w.Code, w.Body.String(), err)
			}
			// Explicit mapping can select a conditional profile, and its zero cache
			// write must survive even when mapped through the Anthropic adapter.
			w = httptest.NewRecorder()
			s.ApplyModelPricingSyncAPI(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"items":[{"provider_key":"prod","model":"custom","source_provider":"xai","source_model":"grok-4.6@short"}],"preview_items":[{"provider_key":"prod","model":"custom","matched":true,"source_prompt_price_per_1m":999}]}`)))
			if w.Code != 200 {
				t.Fatalf("apply: %d %s", w.Code, w.Body.String())
			}
			if override {
				if len(store.set) != 0 {
					t.Fatal("manual override was overwritten")
				}
				return
			}
			if len(store.set) != 1 || store.set[0].Source != atPricingSource || store.set[0].PromptPricePer1M != 2 || store.set[0].CacheWritePricePer1M != 0 {
				t.Fatalf("authoritative apply: %+v", store.set)
			}
			store.pricing = store.set
			items, err := s.buildCatalogPricingPreview(context.Background(), atPricingSource, catalog, nil)
			if err != nil || len(items) != 1 || items[0].MatchType != "saved_mapping" || items[0].SourceCacheWritePricePer1M != 0 {
				t.Fatalf("saved profile: %+v, %v", items, err)
			}
		})
	}
}
