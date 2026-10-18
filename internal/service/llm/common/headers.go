package common

import (
	"context"
	"net/http"
	"strings"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/ok"

	"github.com/rakunlabs/at/internal/service"
)

// OpenCodeUserAgent identifies AT to OpenCode Go/Zen, which rejects
// generic SDK/HTTP-library user agents.
const OpenCodeUserAgent = "at-gateway/1.0"

// WithDefaultHeaders preserves explicit request headers, including empty values.
// ok.WithHeader fills empty values too, which can reintroduce suppressed credentials.
//
// It also adds the conversation headers OpenCode Go requires: requests to
// opencode.ai without x-opencode-session are refused ("cannot be routed
// efficiently"). ok has a single inject slot, so this lives here.
func WithDefaultHeaders(headers http.Header) ok.OptionClientFn {
	headers = headers.Clone()
	return ok.WithInject(func(ctx context.Context, req *http.Request) {
		for key, values := range headers {
			if _, exists := req.Header[key]; !exists {
				req.Header[key] = append([]string(nil), values...)
			}
		}
		applyOpenCodeHeaders(ctx, req)
	})
}

func isOpenCodeHost(host string) bool {
	host = strings.ToLower(host)
	return host == "opencode.ai" || strings.HasSuffix(host, ".opencode.ai")
}

func applyOpenCodeHeaders(ctx context.Context, req *http.Request) {
	if req.URL == nil || !isOpenCodeHost(req.URL.Hostname()) {
		return
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", OpenCodeUserAgent)
	}
	if req.Header.Get("x-opencode-session") == "" {
		id := service.UpstreamSessionFromContext(ctx)
		if id == "" {
			// Unknown conversation: a fresh ID still gets the request served,
			// only without cross-request cache affinity.
			id = ulid.Make().String()
		}
		req.Header.Set("x-opencode-session", id)
	}
}
