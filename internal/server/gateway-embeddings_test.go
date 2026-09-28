package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

type embeddingCaptureProvider struct {
	req service.EmbeddingRequest
}

func (p *embeddingCaptureProvider) Chat(context.Context, string, []service.Message, []service.Tool, *service.ChatOptions) (*service.LLMResponse, error) {
	return nil, nil
}

func (p *embeddingCaptureProvider) CreateEmbedding(_ context.Context, req service.EmbeddingRequest) (*service.EmbeddingResponse, error) {
	p.req = req
	dimensions := 2
	if req.Dimensions != nil {
		dimensions = *req.Dimensions
	}
	embedding := make([]float64, dimensions)
	if req.EncodingFormat == "base64" {
		return &service.EmbeddingResponse{
			Base64Embeddings: []string{encodeEmbeddingBase64(embedding)},
			Model:            req.Model,
		}, nil
	}
	return &service.EmbeddingResponse{
		Embeddings: [][]float64{embedding},
		Model:      req.Model,
	}, nil
}

func TestEmbeddingsForwardsOptionalFields(t *testing.T) {
	provider := &embeddingCaptureProvider{}
	s := &Server{
		providers: map[string]ProviderInfo{
			"openai": {
				provider:     provider,
				providerType: "openai",
			},
		},
		tokenStore: gatewayTestToken("test-token", service.APIToken{
			AllowedProvidersMode: service.AccessModeAll,
			AllowedModelsMode:    service.AccessModeAll,
		}),
	}

	req := httptest.NewRequest(http.MethodPost, "/gateway/v1/embeddings", strings.NewReader(`{
		"model":"openai/text-embedding-3-small",
		"input":["hello"],
		"encoding_format":"base64",
		"dimensions":256,
		"user":"user-123",
		"input_type":"search_query"
	}`))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	s.Embeddings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if provider.req.Model != "text-embedding-3-small" {
		t.Errorf("model = %q", provider.req.Model)
	}
	if provider.req.EncodingFormat != "base64" {
		t.Errorf("encoding_format = %q", provider.req.EncodingFormat)
	}
	if provider.req.Dimensions == nil || *provider.req.Dimensions != 256 {
		t.Errorf("dimensions = %v", provider.req.Dimensions)
	}
	if provider.req.User != "user-123" {
		t.Errorf("user = %q", provider.req.User)
	}
	if provider.req.InputType != "search_query" {
		t.Errorf("input_type = %q", provider.req.InputType)
	}

	var response embeddingsResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Data) != 1 {
		t.Fatalf("data length = %d, want 1", len(response.Data))
	}
	if got, ok := response.Data[0].Embedding.(string); !ok || got != encodeEmbeddingBase64(make([]float64, 256)) {
		t.Errorf("embedding = %#v", response.Data[0].Embedding)
	}
}

func TestEmbeddingsDimensionPassthrough(t *testing.T) {
	tests := []struct {
		name           string
		dimensions     int
		wantDimensions *int
	}{
		{name: "zero uses model default", dimensions: 0},
		{name: "non-zero is forwarded", dimensions: 2, wantDimensions: intPtr(2)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &embeddingCaptureProvider{}
			s := &Server{
				providers: map[string]ProviderInfo{
					"openai": {provider: provider, providerType: "openai"},
				},
				tokenStore: gatewayTestToken("test-token", service.APIToken{
					AllowedProvidersMode: service.AccessModeAll,
					AllowedModelsMode:    service.AccessModeAll,
				}),
			}
			body := fmt.Sprintf(`{"model":"openai/model","input":"hello","dimensions":%d}`, tt.dimensions)
			req := httptest.NewRequest(http.MethodPost, "/gateway/v1/embeddings", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer test-token")
			rec := httptest.NewRecorder()

			s.Embeddings(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
			}
			if tt.wantDimensions == nil {
				if provider.req.Dimensions != nil {
					t.Fatalf("dimensions = %v, want nil", *provider.req.Dimensions)
				}
			} else if provider.req.Dimensions == nil || *provider.req.Dimensions != *tt.wantDimensions {
				t.Fatalf("dimensions = %v, want %d", provider.req.Dimensions, *tt.wantDimensions)
			}
		})
	}
}

func intPtr(value int) *int { return &value }

func TestEmbeddingsRejectsInvalidEncodingFormat(t *testing.T) {
	provider := &embeddingCaptureProvider{}
	s := &Server{
		providers: map[string]ProviderInfo{
			"openai": {provider: provider, providerType: "openai"},
		},
		tokenStore: gatewayTestToken("test-token", service.APIToken{
			AllowedProvidersMode: service.AccessModeAll,
			AllowedModelsMode:    service.AccessModeAll,
		}),
	}
	req := httptest.NewRequest(http.MethodPost, "/gateway/v1/embeddings", strings.NewReader(`{"model":"openai/model","input":"hello","encoding_format":"hex"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	s.Embeddings(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if provider.req.Input != nil {
		t.Fatal("provider was called for an invalid request")
	}
}

func TestEmbeddingsValidatesBatchRequest(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "empty item", body: `{"model":"openai/model","input":["ok",""]}`},
		{name: "invalid input type", body: `{"model":"openai/model","input":["ok"],"input_type":"image"}`},
		{name: "negative dimensions", body: `{"model":"openai/model","input":["ok"],"dimensions":-1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &embeddingCaptureProvider{}
			s := &Server{
				providers:  map[string]ProviderInfo{"openai": {provider: provider, providerType: "openai"}},
				tokenStore: gatewayTestToken("test-token", service.APIToken{AllowedProvidersMode: service.AccessModeAll, AllowedModelsMode: service.AccessModeAll}),
			}
			req := httptest.NewRequest(http.MethodPost, "/gateway/v1/embeddings", strings.NewReader(tt.body))
			req.Header.Set("Authorization", "Bearer test-token")
			rec := httptest.NewRecorder()
			s.Embeddings(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
			if provider.req.Input != nil {
				t.Fatal("provider was called")
			}
		})
	}
}

func TestEmbeddingsConfiguredBatchLimit(t *testing.T) {
	provider := &embeddingCaptureProvider{}
	s := &Server{
		providers: map[string]ProviderInfo{"openai": {
			provider: provider, providerType: "openai", embeddingMaxInputs: 1,
		}},
		tokenStore: gatewayTestToken("test-token", service.APIToken{AllowedProvidersMode: service.AccessModeAll, AllowedModelsMode: service.AccessModeAll}),
	}
	req := httptest.NewRequest(http.MethodPost, "/gateway/v1/embeddings", strings.NewReader(`{"model":"openai/model","input":["a","b"]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	s.Embeddings(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "batch_too_large") {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestValidateEmbeddingConfig(t *testing.T) {
	if got := validateEmbeddingConfig(config.LLMConfig{}); got != "" {
		t.Fatalf("empty config: %q", got)
	}
	if got := validateEmbeddingConfig(config.LLMConfig{EmbeddingMaxInputs: 25}); got != "" {
		t.Fatalf("positive limit: %q", got)
	}
	if got := validateEmbeddingConfig(config.LLMConfig{EmbeddingMaxInputs: -1}); !strings.Contains(got, "must be >= 0") {
		t.Fatalf("negative limit: %q", got)
	}
}

type malformedEmbeddingProvider struct{ embeddingCaptureProvider }

func (p *malformedEmbeddingProvider) CreateEmbedding(_ context.Context, req service.EmbeddingRequest) (*service.EmbeddingResponse, error) {
	p.req = req
	return &service.EmbeddingResponse{Embeddings: [][]float64{{1}}, Model: req.Model}, nil
}

func TestEmbeddingsRejectsPartialUpstreamBatch(t *testing.T) {
	provider := &malformedEmbeddingProvider{}
	s := &Server{
		providers:  map[string]ProviderInfo{"openai": {provider: provider, providerType: "openai"}},
		tokenStore: gatewayTestToken("test-token", service.APIToken{AllowedProvidersMode: service.AccessModeAll, AllowedModelsMode: service.AccessModeAll}),
	}
	req := httptest.NewRequest(http.MethodPost, "/gateway/v1/embeddings", strings.NewReader(`{"model":"openai/model","input":["a","b"]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	s.Embeddings(rec, req)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "invalid_upstream_response") {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}
