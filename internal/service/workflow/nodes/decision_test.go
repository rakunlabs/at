package nodes_test

import (
	"context"
	"slices"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

type mockDecisionProvider struct {
	mockProvider
	req     service.DecisionRequest
	answers map[string]any
}

func (m *mockDecisionProvider) Decide(_ context.Context, req service.DecisionRequest) (*service.DecisionResponse, error) {
	m.req = req
	return &service.DecisionResponse{Model: "english", Answers: m.answers, Raw: map[string]any{"answers": m.answers, "routing": map[string]any{"model": "english"}}}, nil
}

func decisionTestNode(t *testing.T, data map[string]any) workflow.Noder {
	t.Helper()
	factory := workflow.GetNodeFactory("decision")
	if factory == nil {
		t.Fatal("decision node not registered")
	}
	n, err := factory(service.WorkflowNode{ID: "d", Type: "decision", Data: data})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDecisionNodeRoutesOnConfidence(t *testing.T) {
	questions := map[string]any{"dept": map[string]any{"type": "choice", "criteria": map[string]any{"billing": "refunds", "tech": "bugs"}}}
	tests := []struct {
		name       string
		confidence float64
		wantPort   string
	}{
		{name: "confident", confidence: 0.95, wantPort: "decided"},
		{name: "uncertain", confidence: 0.4, wantPort: "escalate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &mockDecisionProvider{answers: map[string]any{"dept": map[string]any{"choice": "billing", "answer_confidence": tt.confidence}}}
			registry := newTestRegistryWithLookup(func(key string) (service.LLMProvider, string, error) { return provider, "auto", nil })
			n := decisionTestNode(t, map[string]any{"provider": "laya", "questions": questions, "min_confidence": 0.7, "max_len": float64(2048)})
			if err := n.Validate(context.Background(), registry); err != nil {
				t.Fatal(err)
			}
			result, err := n.Run(context.Background(), registry, map[string]any{"state": "billed twice"})
			if err != nil {
				t.Fatal(err)
			}
			sel, ok := result.(workflow.NodeResultSelection)
			if !ok || !slices.Equal(sel.Selection(), []string{tt.wantPort}) {
				t.Fatalf("selection = %v", sel)
			}
			out := result.Data()[tt.wantPort].(map[string]any)
			if out["answers"] == nil || out["state"] != "billed twice" || out["routing"] == nil {
				t.Fatalf("output = %v", out)
			}
			if provider.req.Model != "auto" || provider.req.Options["max_len"] != 2048 {
				t.Fatalf("request = %+v", provider.req)
			}
		})
	}
}

func TestDecisionNodeValidation(t *testing.T) {
	registry := newTestRegistryWithLookup(func(string) (service.LLMProvider, string, error) { return nil, "", nil })
	tests := []struct {
		name string
		data map[string]any
	}{
		{name: "no provider", data: map[string]any{"questions": map[string]any{"q": map[string]any{"type": "noul"}}}},
		{name: "no questions", data: map[string]any{"provider": "laya"}},
		{name: "bad threshold", data: map[string]any{"provider": "laya", "min_confidence": 1.5, "questions": map[string]any{"q": map[string]any{"type": "noul"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := decisionTestNode(t, tt.data).Validate(context.Background(), registry); err == nil {
				t.Fatal("want validation error")
			}
		})
	}
}

func TestDecisionNodeAcceptsQuestionJSONText(t *testing.T) {
	n := decisionTestNode(t, map[string]any{"provider": "laya", "questions": `{"q":{"type":"noul","instructions":"?"}}`})
	registry := newTestRegistryWithLookup(func(string) (service.LLMProvider, string, error) { return nil, "", nil })
	if err := n.Validate(context.Background(), registry); err != nil {
		t.Fatal(err)
	}
}

func TestDecisionNodeRejectsNonDecisionProvider(t *testing.T) {
	registry := newTestRegistryWithLookup(func(string) (service.LLMProvider, string, error) { return &mockProvider{}, "", nil })
	n := decisionTestNode(t, map[string]any{"provider": "openai", "questions": map[string]any{"q": map[string]any{"type": "noul"}}})
	if _, err := n.Run(context.Background(), registry, map[string]any{"state": "x"}); err == nil {
		t.Fatal("want unsupported provider error")
	}
}

func newTestRegistryWithLookup(lookup workflow.ProviderLookup) *workflow.Registry {
	return workflow.NewRegistry(lookup, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
}
