package bedrock

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestExplicitCredentialsAndEndpointDoNotMixWithEnvironment(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "env-access")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "env-secret")
	t.Setenv("AWS_SESSION_TOKEN", "env-session")
	t.Setenv("AWS_REGION", "us-east-1")
	p, err := New("access:secret", "model", "https://bedrock-runtime.eu-west-1.amazonaws.com", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if p.sessionToken != "" || p.region != "eu-west-1" {
		t.Fatalf("mixed credentials/region: session=%q region=%q", p.sessionToken, p.region)
	}
	if _, err := New("malformed", "model", "", "", false); err == nil {
		t.Fatal("invalid explicit credentials fell back to a different account")
	}
}

func TestExtraBodyUsesNativeTopLevelShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["requestMetadata"] == nil {
			t.Error("native extra field was nested incorrectly")
		}
		additional, _ := body["additionalModelRequestFields"].(map[string]any)
		if additional["top_k"] != float64(10) {
			t.Errorf("additional fields = %#v", additional)
		}
		fmt.Fprint(w, `{"output":{"message":{"role":"assistant","content":[{"text":"ok"}]}},"stopReason":"end_turn"}`)
	}))
	defer srv.Close()
	p, err := New("access:secret", "model", srv.URL, "", false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Chat(context.Background(), "", []service.Message{{Role: "user", Content: "hi"}}, nil, &service.ChatOptions{ExtraBody: map[string]any{
		"requestMetadata": map[string]any{"task": "test"}, "additionalModelRequestFields": map[string]any{"top_k": 10},
	}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDocumentAndEmptyToolResultWireShape(t *testing.T) {
	blocks := convertContentToConverse([]service.ContentBlock{
		{Type: "document", Source: &service.MediaSource{Type: "base64", MediaType: "application/pdf", Data: "cGRm"}},
		{Type: "tool_result", ToolUseID: "call_1", Content: ""},
	})
	if len(blocks) != 2 || blocks[0].Document == nil || blocks[0].Document.Format != "pdf" {
		t.Fatalf("document dropped: %+v", blocks)
	}
	data, err := json.Marshal(blocks[1])
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	result := wire["toolResult"].(map[string]any)["content"].([]any)[0].(map[string]any)
	if text, exists := result["text"]; !exists || text != "" {
		t.Fatalf("empty output became invalid empty union: %s", data)
	}
}
