package server

import (
	"net/http"
	"testing"
)

func TestForwardProviderResponseHeadersDropsUpstreamBodyAndTransportHeaders(t *testing.T) {
	src := http.Header{
		"Content-Length":                       []string{"12"},
		"Content-Encoding":                     []string{"gzip"},
		"Content-Type":                         []string{"application/json"},
		"Transfer-Encoding":                    []string{"chunked"},
		"Connection":                           []string{"keep-alive, X-Upstream-Hop"},
		"Keep-Alive":                           []string{"timeout=5"},
		"X-Upstream-Hop":                       []string{"remove-me"},
		"X-Request-Id":                         []string{"request-1"},
		"Anthropic-Ratelimit-Tokens-Remaining": []string{"42"},
	}
	dst := http.Header{}

	forwardProviderResponseHeaders(dst, src)

	for _, key := range []string{
		"Content-Length", "Content-Encoding", "Content-Type", "Transfer-Encoding",
		"Connection", "Keep-Alive", "X-Upstream-Hop",
	} {
		if got := dst.Get(key); got != "" {
			t.Errorf("%s = %q, want omitted", key, got)
		}
	}
	if got := dst.Get("X-Request-Id"); got != "request-1" {
		t.Errorf("X-Request-Id = %q, want request-1", got)
	}
	if got := dst.Get("Anthropic-Ratelimit-Tokens-Remaining"); got != "42" {
		t.Errorf("rate-limit header = %q, want 42", got)
	}
}

func TestHTTPResponseJSONDropsStaleUpstreamBodyHeaders(t *testing.T) {
	w := newHeaderRecorder()
	w.Header().Set("Content-Length", "999")
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Transfer-Encoding", "chunked")

	httpResponseJSON(w, map[string]any{"ok": true}, http.StatusOK)

	for _, key := range []string{"Content-Length", "Content-Encoding", "Transfer-Encoding"} {
		if got := w.Header().Get(key); got != "" {
			t.Errorf("%s = %q, want omitted", key, got)
		}
	}
}

// headerRecorder keeps the response header map inspectable without net/http
// adding an automatic Content-Length for the small test body.
type headerRecorder struct {
	header http.Header
}

func newHeaderRecorder() *headerRecorder { return &headerRecorder{header: make(http.Header)} }

func (w *headerRecorder) Header() http.Header         { return w.header }
func (w *headerRecorder) WriteHeader(_ int)           {}
func (w *headerRecorder) Write(p []byte) (int, error) { return len(p), nil }
