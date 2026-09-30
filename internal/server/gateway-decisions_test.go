package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

type decisionCaptureProvider struct {
	req service.DecisionRequest
	err error
}

func (p *decisionCaptureProvider) Chat(context.Context, string, []service.Message, []service.Tool, *service.ChatOptions) (*service.LLMResponse, error) {
	return nil, service.ErrUnsupportedOperation
}

func (p *decisionCaptureProvider) Decide(_ context.Context, req service.DecisionRequest) (*service.DecisionResponse, error) {
	p.req = req
	if p.err != nil {
		return nil, p.err
	}
	answers := map[string]any{"dept": map[string]any{"choice": "billing", "confidence": 0.9}}
	return &service.DecisionResponse{
		Model: "english", Answers: answers,
		Raw: map[string]any{"model": "english", "answers": answers, "routing": map[string]any{"model": "english"}},
	}, nil
}

func decisionTestServer(provider service.LLMProvider, providerType string) *Server {
	return &Server{
		providers: map[string]ProviderInfo{
			"laya": {provider: provider, providerType: providerType, defaultModel: "auto", models: []string{"auto", "english"}},
		},
		tokenStore: gatewayTestToken("test-token", service.APIToken{
			AllowedProvidersMode: service.AccessModeAll,
			AllowedModelsMode:    service.AccessModeAll,
		}),
	}
}

func postDecision(s *Server, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/gateway/v1/decisions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Decisions(rec, req)
	return rec
}

func TestDecisionsForwardsRequestAndPreservesResponse(t *testing.T) {
	provider := &decisionCaptureProvider{}
	s := decisionTestServer(provider, "systemone")

	rec := postDecision(s, `{"model":"laya/english","state":{"body":"billed twice"},
		"questions":{"dept":{"type":"choice","instructions":"team?","criteria":{"billing":"refunds","tech":"bugs"}}},
		"max_len":2048,"min_confidence":0.8}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if provider.req.Model != "english" || provider.req.Options["max_len"] != float64(2048) || provider.req.Options["min_confidence"] != 0.8 {
		t.Fatalf("forwarded request = %+v", provider.req)
	}
	if _, ok := provider.req.Options["model"]; ok {
		t.Fatal("model must not travel as an option")
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out["routing"]; !ok {
		t.Fatalf("upstream fields dropped: %v", out)
	}
	if rec.Header().Get("x-at-model-used") != "laya/english" {
		t.Fatalf("x-at-model-used = %q", rec.Header().Get("x-at-model-used"))
	}
}

func TestDecisionsValidation(t *testing.T) {
	s := decisionTestServer(&decisionCaptureProvider{}, "systemone")
	tests := []struct {
		name, body string
		status     int
	}{
		{name: "missing state", body: `{"model":"laya/auto","questions":{"q":{"type":"noul"}}}`, status: 400},
		{name: "bad question type", body: `{"model":"laya/auto","state":"x","questions":{"q":{"type":"essay"}}}`, status: 400},
		{name: "missing model", body: `{"state":"x","questions":{"q":{"type":"noul"}}}`, status: 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rec := postDecision(s, tt.body); rec.Code != tt.status {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDecisionsUnsupportedProvider(t *testing.T) {
	s := decisionTestServer(&embeddingCaptureProvider{}, "openai")
	rec := postDecision(s, `{"model":"laya/auto","state":"x","questions":{"q":{"type":"noul"}}}`)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDecisionsUpstreamErrorsSurface(t *testing.T) {
	s := decisionTestServer(&decisionCaptureProvider{err: &service.UpstreamError{Provider: "systemone", StatusCode: 400, Code: "invalid_question", Message: "question 'q' is bad"}}, "systemone")
	rec := postDecision(s, `{"model":"laya/auto","state":"x","questions":{"q":{"type":"noul"}}}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_question") {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDecisionModelsAreNotAdvertisedForChat(t *testing.T) {
	s := decisionTestServer(&decisionCaptureProvider{}, "systemone")
	auth, _ := s.authenticateRequest(func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/gateway/v1/models", nil)
		r.Header.Set("Authorization", "Bearer test-token")
		return r
	}())
	for _, m := range s.gatewayModels(context.Background(), auth) {
		if strings.HasPrefix(m.ID, "laya/") {
			t.Fatalf("decision model advertised as chat model: %s", m.ID)
		}
	}
}
