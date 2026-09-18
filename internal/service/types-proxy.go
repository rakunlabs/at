package service

import (
	"context"
	"net/http"
)

// Usage provenance for a native provider passthrough request. Recorded
// verbatim so a zero-token row is distinguishable from a request whose upstream
// genuinely reported zero, and so the coverage gap is queryable.
const (
	// UsageSourceParsed means real counts were read from a non-streaming JSON
	// response body.
	UsageSourceParsed = "parsed"
	// UsageSourceStreamParsed means real counts were read from the terminal
	// event of a streamed response.
	UsageSourceStreamParsed = "stream_parsed"
	// UsageSourceUnavailable means the response carried no usage AT could read
	// — a binary body, an unrecognised shape, or a body over the inspection
	// bound. Token counts are zero and were NOT estimated: writing an estimate
	// into the same column real counts use would corrupt billing.
	UsageSourceUnavailable = "unavailable"
)

// ProxyObservation is what a passthrough request reports back to the gateway
// once its upstream response has been fully relayed to the client.
type ProxyObservation struct {
	StatusCode  int
	Header      http.Header
	Usage       Usage
	UsageSource string
}

type proxyObserverKey struct{}

// ContextWithProxyObserver attaches a callback that the provider's Proxy
// implementation invokes when the upstream response finishes streaming.
//
// It rides the context rather than the Proxy(w, r, path) signature because that
// signature is asserted structurally at the gateway call site and implemented by
// seven adapters; threading an extra parameter through all of them to carry a
// per-request value is what context is for. A Proxy that ignores it keeps
// working — the gateway then records the request with no usage rather than
// nothing at all.
func ContextWithProxyObserver(ctx context.Context, fn func(ProxyObservation)) context.Context {
	if fn == nil {
		return ctx
	}

	return context.WithValue(ctx, proxyObserverKey{}, fn)
}

// ProxyObserverFromContext returns the observer attached by the gateway, if any.
func ProxyObserverFromContext(ctx context.Context) (func(ProxyObservation), bool) {
	fn, ok := ctx.Value(proxyObserverKey{}).(func(ProxyObservation))

	return fn, ok
}
