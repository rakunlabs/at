package common

import (
	"context"
	"net/http"

	"github.com/rakunlabs/ok"
)

// WithDefaultHeaders preserves explicit request headers, including empty values.
// ok.WithHeader fills empty values too, which can reintroduce suppressed credentials.
func WithDefaultHeaders(headers http.Header) ok.OptionClientFn {
	headers = headers.Clone()
	return ok.WithInject(func(_ context.Context, req *http.Request) {
		for key, values := range headers {
			if _, exists := req.Header[key]; !exists {
				req.Header[key] = append([]string(nil), values...)
			}
		}
	})
}
