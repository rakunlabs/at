package common

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/ok"

	"github.com/rakunlabs/at/internal/service"
)

func TestWithDefaultHeaders(t *testing.T) {
	for _, value := range []string{"absent", "", "Bearer fresh"} {
		t.Run(value, func(t *testing.T) {
			want := value
			if value == "absent" {
				want = "Bearer default"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != want {
					t.Errorf("Authorization = %q, want %q", got, want)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			client, err := ok.New(ok.WithDisableRetry(true), WithDefaultHeaders(http.Header{
				"Authorization": {"Bearer default"},
			}))
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			if value != "absent" {
				req.Header.Set("Authorization", value)
			}
			if err := client.Do(req, func(*http.Response) error { return nil }); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOpenCodeSessionHeaders(t *testing.T) {
	tests := []struct {
		name        string
		host        string
		ctx         context.Context
		wantSession string // "*" = any non-empty
		wantUA      string
	}{
		{"explicit session", "opencode.ai", service.ContextWithUpstreamSession(context.Background(), "conv-1"), "conv-1", OpenCodeUserAgent},
		{"trace parent session", "opencode.ai", func() context.Context {
			ctx, _ := service.WithTraceParent(context.Background(), "trace-1", "obs", "sess-1")
			return ctx
		}(), "sess-1", OpenCodeUserAgent},
		{"trace parent without session", "opencode.ai", func() context.Context {
			ctx, _ := service.WithTraceParent(context.Background(), "trace-1", "obs", "")
			return ctx
		}(), "trace-1", OpenCodeUserAgent},
		{"no identity", "opencode.ai", context.Background(), "*", OpenCodeUserAgent},
		{"other host", "api.openai.com", service.ContextWithUpstreamSession(context.Background(), "conv-1"), "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got http.Header
			client, err := ok.New(ok.WithDisableRetry(true), WithDefaultHeaders(nil), ok.WithBaseTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
				got = r.Header.Clone()
				return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: r}, nil
			})))
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequestWithContext(tt.ctx, http.MethodPost, "https://"+tt.host+"/zen/go/v1/chat/completions", nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := client.Do(req, func(*http.Response) error { return nil }); err != nil {
				t.Fatal(err)
			}
			session := got.Get("x-opencode-session")
			if tt.wantSession == "*" {
				if session == "" {
					t.Fatal("x-opencode-session missing")
				}
			} else if session != tt.wantSession {
				t.Fatalf("x-opencode-session = %q, want %q", session, tt.wantSession)
			}
			if tt.wantUA != "" && got.Get("User-Agent") != tt.wantUA {
				t.Fatalf("User-Agent = %q, want %q", got.Get("User-Agent"), tt.wantUA)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
