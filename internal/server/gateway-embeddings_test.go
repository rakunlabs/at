package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	return &service.EmbeddingResponse{
		Embeddings: [][]float64{{1.5, -2}},
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
		"user":"user-123"
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

	var response embeddingsResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Data) != 1 {
		t.Fatalf("data length = %d, want 1", len(response.Data))
	}
	if got, ok := response.Data[0].Embedding.(string); !ok || got != encodeEmbeddingBase64([]float64{1.5, -2}) {
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
		{name: "non-zero is forwarded", dimensions: -1, wantDimensions: intPtr(-1)},
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
