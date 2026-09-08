package nodes

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPClientRetryBehavior(t *testing.T) {
	tests := []struct {
		name     string
		retry    bool
		status   int
		attempts int32
	}{
		{name: "disabled", status: http.StatusTooManyRequests, attempts: 1},
		{name: "exhausted", retry: true, status: http.StatusTooManyRequests, attempts: 5},
		{name: "not implemented is not retried", retry: true, status: http.StatusNotImplemented, attempts: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != "payload" || r.Header.Get("X-Test") != "value" {
					t.Errorf("request body = %q, header = %q, error = %v", body, r.Header.Get("X-Test"), err)
				}
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, "upstream body")
			}))
			defer server.Close()
			client, err := (&httpRequestNode{retry: tt.retry}).buildClient()
			if err != nil {
				t.Fatal(err)
			}
			// Retrying with exponential delays instead of Retry-After would exceed this deadline.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader("payload"))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("X-Test", "value")
			resp, err := client.HTTP.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil || string(body) != "upstream body" || resp.StatusCode != tt.status {
				t.Fatalf("response status = %d, body = %q, error = %v", resp.StatusCode, body, err)
			}
			if got := attempts.Load(); got != tt.attempts {
				t.Errorf("attempts = %d, want %d", got, tt.attempts)
			}
		})
	}
}
