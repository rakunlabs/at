package systemone

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

var testQuestions = map[string]any{
	"department": map[string]any{
		"type": "choice", "instructions": "Which team?",
		"criteria": map[string]any{"billing": "refunds", "technical": "bugs"},
	},
}

func TestDecideForwardsProtocolRequest(t *testing.T) {
	var got map[string]any
	var auth, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"model":"english","answers":{"department":{"choice":"billing","confidence":0.94,"answer_confidence":0.97}},"usage":{"input_tokens":42,"output_tokens":0},"routing":{"model":"english","reason":"latin"}}`))
	}))
	defer srv.Close()

	// A pasted endpoint URL is tolerated.
	p, err := New("secret", "", srv.URL+"/v1/systemone", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Decide(context.Background(), service.DecisionRequest{
		Model: "multilingual", State: map[string]any{"body": "billed twice"}, Questions: testQuestions,
		Options: map[string]any{"max_len": 8192, "hooks": "x", "state": "override"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/systemone" || auth != "Bearer secret" {
		t.Fatalf("path=%q auth=%q", path, auth)
	}
	if got["model"] != "multilingual" || got["max_len"] != float64(8192) {
		t.Fatalf("body = %v", got)
	}
	if _, ok := got["hooks"]; ok {
		t.Fatal("hook arguments must not be forwarded")
	}
	if state, _ := got["state"].(map[string]any); state["body"] != "billed twice" {
		t.Fatalf("state overridden by options: %v", got["state"])
	}
	if resp.Usage.PromptTokens != 42 || resp.Model != "english" {
		t.Fatalf("resp = %+v", resp)
	}
	if _, ok := resp.Raw["routing"]; !ok {
		t.Fatal("unmodelled upstream fields must be preserved")
	}
	answer := resp.Answers["department"].(map[string]any)
	if answer["choice"] != "billing" {
		t.Fatalf("answer = %v", answer)
	}
}

func TestDecideAutoModelIsOmitted(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"answers":{}}`))
	}))
	defer srv.Close()
	p, _ := New("", "", srv.URL, "", false, nil)
	if _, err := p.Decide(context.Background(), service.DecisionRequest{State: "x", Questions: testQuestions}); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["model"]; ok {
		t.Fatalf("auto must let the upstream route, body=%v", got)
	}
}

func TestDecideErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		rateLimit  bool
		code       string
	}{
		{name: "question validation", status: 422, body: `{"detail":"question 'x' has no criteria"}`, wantStatus: 400, code: "invalid_question"},
		{name: "busy", status: 503, body: `{"detail":"server busy"}`, rateLimit: true},
		{name: "bad key", status: 401, body: `{"detail":"invalid or missing bearer token"}`, wantStatus: 502, code: "upstream_auth_failed"},
		{name: "inference failure", status: 500, body: `{"detail":"inference failed"}`, wantStatus: 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			p, _ := New("", "", srv.URL, "", false, nil)
			_, err := p.Decide(context.Background(), service.DecisionRequest{State: "x", Questions: testQuestions})
			if tt.rateLimit {
				var rle *service.RateLimitError
				if !errors.As(err, &rle) || rle.RetryAfter == 0 {
					t.Fatalf("want rate limit error with retry-after, got %v", err)
				}
				return
			}
			var up *service.UpstreamError
			if !errors.As(err, &up) || up.StatusCode != tt.wantStatus || up.Code != tt.code {
				t.Fatalf("got %#v", err)
			}
		})
	}
}

func TestDecideUnreachableIsOutage(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	p, _ := New("", "", url, "", false, nil)
	_, err := p.Decide(context.Background(), service.DecisionRequest{State: "x", Questions: testQuestions})
	var up *service.UpstreamError
	if !errors.As(err, &up) || up.StatusCode != http.StatusBadGateway {
		t.Fatalf("got %v", err)
	}
}

func TestDecideRejectsInvalidRequestLocally(t *testing.T) {
	p, _ := New("", "", "http://127.0.0.1:1", "", false, nil)
	_, err := p.Decide(context.Background(), service.DecisionRequest{State: "x", Questions: map[string]any{"q": map[string]any{"type": "essay"}}})
	var up *service.UpstreamError
	if !errors.As(err, &up) || up.StatusCode != http.StatusBadRequest {
		t.Fatalf("got %v", err)
	}
}

func TestChatIsUnsupported(t *testing.T) {
	p, _ := New("", "", "", "", false, nil)
	if _, err := p.Chat(context.Background(), "", nil, nil, nil); !errors.Is(err, service.ErrUnsupportedOperation) {
		t.Fatalf("got %v", err)
	}
}

func TestNewRejectsBadBaseURL(t *testing.T) {
	if _, err := New("", "", "laya:8000", "", false, nil); err == nil {
		t.Fatal("want error for scheme-less base URL")
	}
}
