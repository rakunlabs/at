package openai

import (
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestReasoningEffortRequest(t *testing.T) {
	p := &Provider{}
	body := p.buildRequestBody("model", nil, nil, &service.ChatOptions{ReasoningEffort: "xhigh"})
	if body["reasoning_effort"] != "xhigh" {
		t.Fatalf("body = %+v", body)
	}
	if _, ok := body["max_tokens"]; ok {
		t.Fatalf("unexpected token cap: %+v", body)
	}
	if _, ok := body["max_completion_tokens"]; ok {
		t.Fatalf("unexpected token cap: %+v", body)
	}
}
