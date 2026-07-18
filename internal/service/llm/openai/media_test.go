package openai

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func TestAPIURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		path    string
		want    string
	}{
		{
			"openai standard",
			"https://api.openai.com/v1/chat/completions",
			"/embeddings",
			"https://api.openai.com/v1/embeddings",
		},
		{
			"trailing slash",
			"https://api.openai.com/v1/chat/completions/",
			"/moderations",
			"https://api.openai.com/v1/moderations",
		},
		{
			"azure with api-version query",
			"https://res.openai.azure.com/openai/deployments/gpt4o/chat/completions?api-version=2024-06-01",
			"/embeddings",
			"https://res.openai.azure.com/openai/deployments/gpt4o/embeddings?api-version=2024-06-01",
		},
		{
			"generic base without chat suffix",
			"https://example.com/v1",
			"/images/generations",
			"https://example.com/v1/images/generations",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Provider{BaseURL: tt.baseURL}
			if got := p.apiURL(tt.path); got != tt.want {
				t.Errorf("apiURL(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestTranscribeAudio(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Errorf("path = %q, want /v1/audio/transcriptions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want bearer token", got)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		if got := r.FormValue("model"); got != "whisper-1" {
			t.Errorf("model = %q, want whisper-1", got)
		}
		if got := r.FormValue("language"); got != "tr" {
			t.Errorf("language = %q, want tr", got)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("FormFile: %v", err)
		}
		defer file.Close()
		if header.Filename != "speech.mp3" {
			t.Errorf("filename = %q, want speech.mp3", header.Filename)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"merhaba","language":"tr","duration":1.5}`))
	}))
	defer server.Close()

	provider, err := New("test-key", "unused", server.URL+"/v1/chat/completions", "", false, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := provider.TranscribeAudio(context.Background(), service.AudioTranscribeRequest{
		AudioBase64: base64.StdEncoding.EncodeToString([]byte("fake wav")),
		ContentType: "audio/wav",
		Filename:    "speech.mp3",
		Model:       "whisper-1",
		Language:    "tr",
	})
	if err != nil {
		t.Fatalf("TranscribeAudio: %v", err)
	}
	if resp.Text != "merhaba" || resp.Language != "tr" || resp.Duration != 1.5 {
		t.Fatalf("response = %#v", resp)
	}
}

func TestTranscribeAudioRateLimit(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"quota exhausted","type":"insufficient_quota"}}`))
	}))
	defer server.Close()

	provider, err := New("test-key", "unused", server.URL+"/v1/chat/completions", "", false, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = provider.TranscribeAudio(context.Background(), service.AudioTranscribeRequest{
		AudioBase64: base64.StdEncoding.EncodeToString([]byte("fake wav")),
		ContentType: "audio/wav",
		Model:       "whisper-1",
	})
	var rateLimitErr *service.RateLimitError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("error = %T %v, want *service.RateLimitError", err, err)
	}
	if rateLimitErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, want 429", rateLimitErr.StatusCode)
	}
	if rateLimitErr.RetryAfter != 7*time.Second {
		t.Errorf("RetryAfter = %s, want 7s", rateLimitErr.RetryAfter)
	}
	if rateLimitErr.Message != "quota exhausted" {
		t.Errorf("Message = %q, want quota exhausted", rateLimitErr.Message)
	}
}
