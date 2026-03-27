package minimax

import (
	"encoding/base64"
	"net/http"
)

// encodeBase64 encodes raw bytes to a standard base64 string.
func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// pathPrefixTransport wraps an http.RoundTripper and prepends a path prefix
// to every outgoing request URL. This is used to route requests through
// MiniMax's Anthropic-compatible endpoint (/anthropic/v1/messages) when the
// Anthropic provider constructs absolute paths (/v1/messages) that would
// otherwise replace the base URL path during Go's URL resolution.
type pathPrefixTransport struct {
	base   http.RoundTripper
	prefix string // e.g. "/anthropic"
}

func (t *pathPrefixTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request to avoid mutating the original.
	r2 := req.Clone(req.Context())
	r2.URL.Path = t.prefix + r2.URL.Path
	if r2.URL.RawPath != "" {
		r2.URL.RawPath = t.prefix + r2.URL.RawPath
	}
	return t.base.RoundTrip(r2)
}
