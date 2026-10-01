package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/gateway/wire"
	"github.com/rakunlabs/at/internal/nativeauth/nativeauthtest"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

// gatewayRoutingToken stores a gateway token with a known plaintext, mirroring
// the hashing CreateAPITokenAPI performs.
func gatewayRoutingToken(t *testing.T, ctx context.Context, p *postgres.Postgres, plain string, token service.APIToken) string {
	t.Helper()
	sum := sha256.Sum256([]byte(plain))
	token.Name = plain
	token.TokenPrefix = plain[:8]
	if _, err := p.CreateAPIToken(service.WithLegacyWorkspaceAccess(ctx), token, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	return plain
}

// TestGatewayRouting covers the two ways a misconfigured OpenAI client used to
// be told "this gateway has no models" when the real answer was different:
//
//   - an empty model list serialized as `null`, which clients iterate without a
//     nil check, and
//   - any unmatched path under /gateway falling through to the SPA catch-all,
//     which answers HTTP 200 with index.html.
//
// Both are indistinguishable from an empty registry at the client, so both are
// asserted on the production mux rather than on the handlers in isolation.
func TestGatewayRouting(t *testing.T) {
	p := postgrestest.New(t, nil)
	cfg := nativeauthtest.Config()
	cfg.Workspace = &config.Workspace{Root: t.TempDir(), TTLHours: -1}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	// Deliberately unsorted keys and models: the response must not depend on
	// Go's map iteration order.
	s, err := New(ctx, cfg, map[string]ProviderInfo{
		"anthropic": {providerType: "anthropic", defaultModel: "claude-opus-5-5"},
		"zeta":      {defaultModel: "fallback", models: []string{"delta", "beta"}, modelLimits: map[string]config.ModelLimit{"beta": {Context: 1_000_000, Output: 128_000}}},
		"alpha":     {defaultModel: "only"},
		"empty":     {},
	}, p, "postgres", nil, nil, "test", "", "")
	if err != nil {
		t.Fatal(err)
	}

	full := gatewayRoutingToken(t, ctx, p, "at_routing_full_token", service.APIToken{})
	denied := gatewayRoutingToken(t, ctx, p, "at_routing_denied_tokn", service.APIToken{
		AllowedProvidersMode: service.AccessModeNone,
		AllowedModelsMode:    service.AccessModeNone,
	})
	if err := p.SetModelPricing(ctx, service.ModelPricing{
		ProviderKey: "zeta", Model: "beta",
		PromptPricePer1M: 1.25, CompletionPricePer1M: 10,
		CacheReadPricePer1M: 0.25, CacheWritePricePer1M: 2,
	}); err != nil {
		t.Fatal(err)
	}
	// A present all-zero row means deliberately free; it must remain distinct
	// from zeta/delta, which has no pricing row at all.
	if err := p.SetModelPricing(ctx, service.ModelPricing{ProviderKey: "alpha", Model: "only"}); err != nil {
		t.Fatal(err)
	}

	do := func(method, path, bearer string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		s.server.ServeHTTP(w, r)
		return w
	}

	t.Run("models are a sorted array", func(t *testing.T) {
		w := do(http.MethodGet, "/at/gateway/v1/models", full)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		var got wire.ModelsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(got.Data))
		for _, m := range got.Data {
			ids = append(ids, m.ID)
		}
		// "empty" declares neither a model list nor a default, so it
		// contributes nothing; the rest are sorted by full ID.
		want := []string{"alpha/only", "anthropic/claude-opus-5-5", "zeta/beta", "zeta/delta"}
		if strings.Join(ids, ",") != strings.Join(want, ",") {
			t.Fatalf("ids %v want %v", ids, want)
		}
		if got.Data[2].ContextLength != 1_000_000 || got.Data[2].MaxOutputTokens != 128_000 {
			t.Fatalf("beta limits = context %d output %d", got.Data[2].ContextLength, got.Data[2].MaxOutputTokens)
		}
		claude := got.Data[1]
		if strings.Join(claude.InputModalities, ",") != "text,image,pdf" || strings.Join(claude.OutputModalities, ",") != "text" {
			t.Fatalf("claude modalities = input %v output %v", claude.InputModalities, claude.OutputModalities)
		}
		if claude.Capabilities == nil || claude.Capabilities.Vision == nil || !*claude.Capabilities.Vision || claude.Capabilities.Attachment == nil || !*claude.Capabilities.Attachment || claude.Capabilities.ToolCalling == nil || !*claude.Capabilities.ToolCalling {
			t.Fatalf("claude capabilities = %+v", claude.Capabilities)
		}
	})

	t.Run("an empty list is [] and never null", func(t *testing.T) {
		w := do(http.MethodGet, "/at/gateway/v1/models", denied)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		// Asserted on the raw body: unmarshalling into a slice cannot tell
		// `[]` from `null`, and the client-visible difference is the point.
		if body := strings.ReplaceAll(w.Body.String(), " ", ""); !strings.Contains(body, `"data":[]`) {
			t.Fatalf("empty list must serialize as []: %s", w.Body)
		}
	})

	t.Run("LiteLLM model info includes prices and preserves unknown pricing", func(t *testing.T) {
		w := do(http.MethodGet, "/at/gateway/v1/model/info", full)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		var got liteLLMModelInfoResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		byID := make(map[string]liteLLMModelMetadata, len(got.Data))
		for _, entry := range got.Data {
			byID[entry.ModelName] = entry.ModelInfo
		}

		beta := byID["zeta/beta"]
		if beta.InputCostPerToken == nil || *beta.InputCostPerToken != 0.00000125 || beta.OutputCostPerToken == nil || *beta.OutputCostPerToken != 0.00001 {
			t.Fatalf("beta base prices = input %v output %v", beta.InputCostPerToken, beta.OutputCostPerToken)
		}
		if beta.CacheReadInputTokenCost == nil || *beta.CacheReadInputTokenCost != 0.00000025 || beta.CacheCreationInputTokenCost == nil || *beta.CacheCreationInputTokenCost != 0.000002 {
			t.Fatalf("beta cache prices = read %v write %v", beta.CacheReadInputTokenCost, beta.CacheCreationInputTokenCost)
		}
		if beta.MaxTokens != 1_000_000 || beta.MaxOutputTokens != 128_000 || beta.Mode != "chat" {
			t.Fatalf("beta metadata = %+v", beta)
		}

		free := byID["alpha/only"]
		if free.InputCostPerToken == nil || *free.InputCostPerToken != 0 || free.OutputCostPerToken == nil || *free.OutputCostPerToken != 0 {
			t.Fatalf("free model prices = input %v output %v", free.InputCostPerToken, free.OutputCostPerToken)
		}
		unknown := byID["zeta/delta"]
		if unknown.InputCostPerToken != nil || unknown.OutputCostPerToken != nil || unknown.CacheReadInputTokenCost != nil || unknown.CacheCreationInputTokenCost != nil {
			t.Fatalf("unknown model unexpectedly has prices: %+v", unknown)
		}

		w = do(http.MethodGet, "/at/gateway/v1/model/info", denied)
		if w.Code != http.StatusOK || !strings.Contains(strings.ReplaceAll(w.Body.String(), " ", ""), `"data":[]`) {
			t.Fatalf("restricted catalog: %d %s", w.Code, w.Body)
		}
	})

	t.Run("unmatched gateway paths answer JSON, not the SPA", func(t *testing.T) {
		for _, tt := range []struct{ method, path string }{
			{http.MethodGet, "/at/gateway/v1/models/"},   // trailing slash
			{http.MethodGet, "/at/gateway/v1/modelz"},    // typo
			{http.MethodPost, "/at/gateway/v1/models"},   // wrong method
			{http.MethodGet, "/at/gateway/v1"},           // base URL itself
			{http.MethodGet, "/at/gateway/v1/"},          // base URL with slash
			{http.MethodGet, "/at/gateway"},              // prefix only
			{http.MethodGet, "/at/gateway/v2/models"},    // wrong version
			{http.MethodPost, "/at/gateway/v1/chat"},     // truncated path
			{http.MethodGet, "/at/gateway/v1/providers"}, // passthrough without a provider
		} {
			w := do(tt.method, tt.path, full)
			if w.Code != http.StatusNotFound {
				t.Errorf("%s %s: status %d want 404: %s", tt.method, tt.path, w.Code, w.Body)
			}
			if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
				t.Errorf("%s %s: content-type %q", tt.method, tt.path, ct)
			}
			var envelope struct {
				Error struct{ Message, Type, Code string } `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
				t.Errorf("%s %s: %v: %s", tt.method, tt.path, err, w.Body)
				continue
			}
			if envelope.Error.Code != "unknown_endpoint" || !strings.Contains(envelope.Error.Message, "/at/gateway/v1") {
				t.Errorf("%s %s: unhelpful error %+v", tt.method, tt.path, envelope.Error)
			}
		}
	})

	t.Run("registered routes and the SPA are unaffected", func(t *testing.T) {
		// Health needs no credentials and reads the same registry.
		if w := do(http.MethodGet, "/at/gateway/v1/health", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"providers"`) {
			t.Fatalf("health: %d %s", w.Code, w.Body)
		}
		if w := do(http.MethodGet, "/at/gateway/v1/health/zeta", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"model_count":2`) {
			t.Fatalf("provider health: %d %s", w.Code, w.Body)
		}
		// Authentication still runs before the catch-all can see the path.
		if w := do(http.MethodGet, "/at/gateway/v1/models", ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated models: %d %s", w.Code, w.Body)
		}
		// The SPA keeps every path outside the gateway prefix.
		w := do(http.MethodGet, "/at/", "")
		if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("spa: %d %s", w.Code, w.Header().Get("Content-Type"))
		}
	})

	// The most common misconfiguration is a base URL that drops the /gateway
	// prefix, which lands outside the group above. The SPA answers HTTP 200
	// there, so that one path is redirected to the same explanation.
	t.Run("the versioned root without the gateway prefix explains itself", func(t *testing.T) {
		w := do(http.MethodGet, "/at/v1/models", full)
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "unknown_endpoint") {
			t.Fatalf("/v1/models: %d %s", w.Code, w.Body)
		}
	})

	// A trailing wildcard does not match its own slashless base, so the bare
	// prefix used to 404. Serving the SPA there instead of redirecting would
	// resolve every relative asset, API and service-worker URL one level too
	// high, so the only correct answer is a redirect onto the trailing slash.
	t.Run("the bare base path redirects onto its trailing slash", func(t *testing.T) {
		w := do(http.MethodGet, "/at", "")
		if w.Code != http.StatusMovedPermanently {
			t.Fatalf("/at: %d %s", w.Code, w.Body)
		}
		if got := w.Header().Get("Location"); got != "/at/" {
			t.Fatalf("Location = %q, want /at/", got)
		}
	})

	t.Run("the redirect preserves the query string", func(t *testing.T) {
		w := do(http.MethodGet, "/at?next=%2Ftasks", "")
		if got := w.Header().Get("Location"); got != "/at/?next=%2Ftasks" {
			t.Fatalf("Location = %q", got)
		}
	})
}

func TestApplyGatewayModelCapabilities(t *testing.T) {
	enabled, disabled := true, false
	tests := []struct {
		name         string
		providerType string
		model        string
		capability   *config.ModelCapability
		wantInputs   string // "" = no modality metadata advertised
	}{
		{name: "anthropic opus", providerType: "anthropic", model: "claude-opus-5-5", wantInputs: "text,image,pdf"},
		{name: "anthropic dated sonnet", providerType: "anthropic", model: "claude-sonnet-4-5-20250929", wantInputs: "text,image,pdf"},
		{name: "gemini all media", providerType: "gemini", model: "gemini-3-flash-preview", wantInputs: "text,image,pdf,audio,video"},
		{name: "gemini 2.0 has no pdf", providerType: "gemini", model: "gemini-2.0-flash", wantInputs: "text,image,audio,video"},
		{name: "openai gpt-5", providerType: "openai", model: "gpt-5", wantInputs: "text,image,pdf"},
		{name: "openai audio model", providerType: "openai", model: "gpt-audio", wantInputs: "text,audio"},
		{name: "legacy image enable on unknown", providerType: "openai", model: "custom-vision", capability: &config.ModelCapability{ImageInput: &enabled}, wantInputs: "text,image"},
		{name: "legacy image disable keeps pdf", providerType: "anthropic", model: "claude-opus-5-5", capability: &config.ModelCapability{ImageInput: &disabled}, wantInputs: "text,pdf"},
		{name: "explicit modalities", providerType: "openai", model: "llama-omni", capability: &config.ModelCapability{InputModalities: []string{"text", "audio", "video"}}, wantInputs: "text,audio,video"},
		{name: "legacy claude", providerType: "anthropic", model: "claude-2.1", wantInputs: "text"},
		{name: "unknown compatible model", providerType: "anthropic", model: "custom-text-model"},
		{name: "same model on unknown protocol", providerType: "openai", model: "claude-opus-5-5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := wire.ModelData{}
			info := ProviderInfo{providerType: tt.providerType}
			if tt.capability != nil {
				info.modelCapabilities = map[string]config.ModelCapability{tt.model: *tt.capability}
			}
			applyGatewayModelCapabilities(&model, info, tt.model)

			if got := strings.Join(model.InputModalities, ","); got != tt.wantInputs {
				t.Fatalf("inputs = %q, want %q", got, tt.wantInputs)
			}
			if tt.wantInputs == "" {
				if model.Capabilities != nil && model.Capabilities.Vision != nil {
					t.Fatalf("unknown model advertised vision: %+v", model.Capabilities)
				}
				return
			}
			c := model.Capabilities
			for name, pair := range map[string]struct {
				flag     *bool
				modality string
			}{"vision": {c.Vision, "image"}, "pdf": {c.PDFInput, "pdf"}, "audio": {c.AudioInput, "audio"}, "video": {c.VideoInput, "video"}} {
				want := strings.Contains(","+tt.wantInputs+",", ","+pair.modality+",")
				if pair.flag == nil || *pair.flag != want {
					t.Fatalf("%s flag = %v, want %v", name, pair.flag, want)
				}
			}
			if c.Attachment == nil || *c.Attachment != (tt.wantInputs != "text") {
				t.Fatalf("attachment = %v", c.Attachment)
			}
		})
	}
}

func TestApplyGatewayReasoningCapabilities(t *testing.T) {
	tests := []struct {
		name         string
		providerType string
		model        string
		override     []string
		wantKnown    bool
		wantEfforts  []string
	}{
		{name: "opus 4.7", providerType: "anthropic", model: "claude-opus-4-7", wantKnown: true, wantEfforts: []string{"low", "medium", "high", "xhigh", "max"}},
		{name: "opus 4.6 has no xhigh", providerType: "anthropic", model: "claude-opus-4-6", wantKnown: true, wantEfforts: []string{"low", "medium", "high", "max"}},
		{name: "non-reasoning", providerType: "openai", model: "gpt-4o", wantKnown: true},
		{name: "unknown advertises nothing", providerType: "openai", model: "llama-3.3-70b"},
		{name: "manual override", providerType: "openai", model: "llama-3.3-70b", override: []string{"low", "high"}, wantKnown: true, wantEfforts: []string{"low", "high"}},
		{name: "override disables", providerType: "anthropic", model: "claude-opus-5", override: []string{}, wantKnown: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := ProviderInfo{providerType: tt.providerType}
			if tt.override != nil {
				info.modelCapabilities = map[string]config.ModelCapability{tt.model: {ReasoningEfforts: tt.override}}
			}
			model := wire.ModelData{ID: tt.providerType + "/" + tt.model}
			applyGatewayModelCapabilities(&model, info, tt.model)
			if !tt.wantKnown {
				if model.Capabilities != nil && model.Capabilities.Reasoning != nil {
					t.Fatalf("unknown model advertised reasoning: %+v", model.Capabilities)
				}
				return
			}
			if model.Capabilities == nil || model.Capabilities.Reasoning == nil || *model.Capabilities.Reasoning != (len(tt.wantEfforts) > 0) {
				t.Fatalf("reasoning = %+v", model.Capabilities)
			}
			if !slices.Equal(model.Capabilities.ReasoningEfforts, tt.wantEfforts) {
				t.Fatalf("efforts = %v, want %v", model.Capabilities.ReasoningEfforts, tt.wantEfforts)
			}

			// LiteLLM projection: every tier explicit, so discovery clients do
			// not default unlisted low/medium/high to supported.
			var meta liteLLMModelMetadata
			applyLiteLLMReasoning(&meta, model)
			applyLiteLLMParams(&meta, model)
			if meta.SupportsReasoning == nil || *meta.SupportsReasoning != (len(tt.wantEfforts) > 0) {
				t.Fatalf("supports_reasoning = %v", meta.SupportsReasoning)
			}
			if len(tt.wantEfforts) == 0 {
				if slices.Contains(meta.SupportedOpenAIParams, "reasoning_effort") || meta.SupportsLowEffort != nil {
					t.Fatalf("non-reasoning model advertised effort flags: %+v", meta)
				}
				return
			}
			flags := map[string]*bool{"none": meta.SupportsNoneEffort, "minimal": meta.SupportsMinimalEffort, "low": meta.SupportsLowEffort, "medium": meta.SupportsMediumEffort, "high": meta.SupportsHighEffort, "xhigh": meta.SupportsXHighEffort, "max": meta.SupportsMaxEffort}
			for effort, flag := range flags {
				if flag == nil || *flag != slices.Contains(tt.wantEfforts, effort) {
					t.Fatalf("supports_%s_reasoning_effort = %v", effort, flag)
				}
			}
			if !slices.Contains(meta.SupportedOpenAIParams, "reasoning_effort") {
				t.Fatalf("supported_openai_params = %v", meta.SupportedOpenAIParams)
			}
			if temp := model.Capabilities.Temperature; temp != nil && !*temp && slices.Contains(meta.SupportedOpenAIParams, "temperature") {
				t.Fatalf("temperature advertised for a model that rejects it: %v", meta.SupportedOpenAIParams)
			}
		})
	}
}
