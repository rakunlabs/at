package common

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// ProxyUsageMaxBytes bounds how much of a non-streaming passthrough response is
// held in memory to read its usage object. It matches the audit body bound, so a
// response small enough to be recorded is also small enough to be metered.
// Larger bodies relay untouched and report UsageSourceUnavailable.
const ProxyUsageMaxBytes = 256 * 1024

// ProxyResponseObserver returns a ReverseProxy ModifyResponse hook that meters a
// passthrough response, or nil when the gateway attached no observer.
//
// The response is never buffered on the way to the client: the body is wrapped
// in a reader that relays every byte immediately and only accumulates what it
// needs to find a usage object. The callback fires once, when the body reaches
// EOF or is closed, so a client that disconnects mid-response still produces a
// record.
func ProxyResponseObserver(ctx context.Context) func(*http.Response) error {
	observe, ok := service.ProxyObserverFromContext(ctx)
	if !ok {
		return nil
	}

	return func(resp *http.Response) error {
		obs := service.ProxyObservation{
			StatusCode:  resp.StatusCode,
			Header:      resp.Header.Clone(),
			UsageSource: service.UsageSourceUnavailable,
		}
		if resp.Body == nil {
			observe(obs)
			return nil
		}

		contentType := resp.Header.Get("Content-Type")
		switch {
		case strings.Contains(contentType, "text/event-stream"):
			resp.Body = &proxyStreamSniffer{inner: resp.Body, obs: obs, report: observe}
		case isProxyJSONContentType(contentType):
			resp.Body = &proxyJSONSniffer{inner: resp.Body, obs: obs, report: observe}
		default:
			// Binary downloads, file bodies, anything unrecognised: relay
			// untouched and still record the request.
			resp.Body = &proxyPlainBody{inner: resp.Body, obs: obs, report: observe}
		}

		return nil
	}
}

func isProxyJSONContentType(v string) bool {
	v = strings.ToLower(v)

	return strings.Contains(v, "application/json") || strings.Contains(v, "+json")
}

// ─── Body wrappers ───

// proxyPlainBody records the request without inspecting the payload.
type proxyPlainBody struct {
	inner    io.ReadCloser
	obs      service.ProxyObservation
	report   func(service.ProxyObservation)
	reported bool
}

func (b *proxyPlainBody) Read(p []byte) (int, error) {
	n, err := b.inner.Read(p)
	if err != nil {
		b.finish()
	}

	return n, err
}

func (b *proxyPlainBody) Close() error {
	b.finish()

	return b.inner.Close()
}

func (b *proxyPlainBody) finish() {
	if b.reported {
		return
	}
	b.reported = true
	b.report(b.obs)
}

// proxyJSONSniffer accumulates up to ProxyUsageMaxBytes so the terminal usage
// object can be decoded. Every byte is relayed as it arrives; the buffer is a
// copy, not a gate.
type proxyJSONSniffer struct {
	inner     io.ReadCloser
	buf       bytes.Buffer
	truncated bool
	obs       service.ProxyObservation
	report    func(service.ProxyObservation)
	reported  bool
}

func (b *proxyJSONSniffer) Read(p []byte) (int, error) {
	n, err := b.inner.Read(p)
	if n > 0 {
		if b.buf.Len()+n > ProxyUsageMaxBytes {
			b.truncated = true
		} else {
			b.buf.Write(p[:n])
		}
	}
	if err != nil {
		b.finish()
	}

	return n, err
}

func (b *proxyJSONSniffer) Close() error {
	b.finish()

	return b.inner.Close()
}

func (b *proxyJSONSniffer) finish() {
	if b.reported {
		return
	}
	b.reported = true

	if !b.truncated {
		if usage, found := parseProxyUsage(b.buf.Bytes()); found {
			b.obs.Usage = usage
			b.obs.UsageSource = service.UsageSourceParsed
		}
	}
	b.report(b.obs)
}

// proxyStreamSniffer scans SSE data lines incrementally. It keeps only the
// current line and the extracted counts, so an unbounded stream costs bounded
// memory.
type proxyStreamSniffer struct {
	inner    io.ReadCloser
	line     bytes.Buffer
	usage    service.Usage
	found    bool
	obs      service.ProxyObservation
	report   func(service.ProxyObservation)
	reported bool
}

func (b *proxyStreamSniffer) Read(p []byte) (int, error) {
	n, err := b.inner.Read(p)
	if n > 0 {
		b.scan(p[:n])
	}
	if err != nil {
		b.finish()
	}

	return n, err
}

func (b *proxyStreamSniffer) Close() error {
	b.finish()

	return b.inner.Close()
}

// proxyStreamMaxLine caps one accumulated SSE line. A data line carrying usage
// is small; anything larger is content we do not need to hold.
const proxyStreamMaxLine = 64 * 1024

func (b *proxyStreamSniffer) scan(chunk []byte) {
	for _, c := range chunk {
		if c == '\n' {
			b.consumeLine()
			b.line.Reset()

			continue
		}
		if b.line.Len() < proxyStreamMaxLine {
			b.line.WriteByte(c)
		}
	}
}

func (b *proxyStreamSniffer) consumeLine() {
	line := bytes.TrimSpace(b.line.Bytes())
	if !bytes.HasPrefix(line, []byte("data:")) {
		return
	}
	payload := bytes.TrimSpace(line[len("data:"):])
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return
	}

	usage, found := parseProxyUsage(payload)
	if !found {
		return
	}
	b.found = true
	// Anthropic splits usage across message_start (input) and message_delta
	// (output), so later events must not erase earlier counts. Each field keeps
	// its largest reported value.
	b.usage = mergeProxyUsage(b.usage, usage)
}

func (b *proxyStreamSniffer) finish() {
	if b.reported {
		return
	}
	b.reported = true

	// A stream carries no trailing newline guarantee; flush whatever is left.
	b.consumeLine()
	b.line.Reset()

	if b.found {
		b.obs.Usage = b.usage
		b.obs.UsageSource = service.UsageSourceStreamParsed
	}
	b.report(b.obs)
}

// ─── Usage extraction ───

// proxyUsageEnvelope covers the two wire shapes a passthrough response can
// carry. Anthropic nests usage under the message object on message_start, which
// is why that level is decoded too.
type proxyUsageEnvelope struct {
	Usage   *proxyUsageBody `json:"usage"`
	Message *struct {
		Usage *proxyUsageBody `json:"usage"`
	} `json:"message"`
}

type proxyUsageBody struct {
	// Anthropic
	InputTokens              *int `json:"input_tokens"`
	OutputTokens             *int `json:"output_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`

	// OpenAI
	PromptTokens        *int `json:"prompt_tokens"`
	CompletionTokens    *int `json:"completion_tokens"`
	TotalTokens         *int `json:"total_tokens"`
	PromptTokensDetails *struct {
		CachedTokens *int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens *int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func parseProxyUsage(body []byte) (service.Usage, bool) {
	if len(bytes.TrimSpace(body)) == 0 {
		return service.Usage{}, false
	}

	var env proxyUsageEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return service.Usage{}, false
	}

	src := env.Usage
	if src == nil && env.Message != nil {
		src = env.Message.Usage
	}
	if src == nil {
		return service.Usage{}, false
	}

	var usage service.Usage
	populated := false
	set := func(dst *int, v *int) {
		if v != nil {
			*dst = *v
			populated = true
		}
	}

	set(&usage.PromptTokens, src.InputTokens)
	set(&usage.PromptTokens, src.PromptTokens)
	set(&usage.CompletionTokens, src.OutputTokens)
	set(&usage.CompletionTokens, src.CompletionTokens)
	set(&usage.CacheReadTokens, src.CacheReadInputTokens)
	set(&usage.CacheWriteTokens, src.CacheCreationInputTokens)
	set(&usage.TotalTokens, src.TotalTokens)

	if src.PromptTokensDetails != nil {
		set(&usage.CacheReadTokens, src.PromptTokensDetails.CachedTokens)
	}
	if src.CompletionTokensDetails != nil {
		set(&usage.ReasoningTokens, src.CompletionTokensDetails.ReasoningTokens)
	}

	if !populated {
		return service.Usage{}, false
	}

	return usage, true
}

// mergeProxyUsage keeps the larger of each field. Streamed usage arrives across
// several events and a later event reporting only output tokens must not zero
// the input count an earlier one reported.
func mergeProxyUsage(a, b service.Usage) service.Usage {
	max := func(x, y int) int {
		if y > x {
			return y
		}

		return x
	}

	return service.Usage{
		PromptTokens:          max(a.PromptTokens, b.PromptTokens),
		CompletionTokens:      max(a.CompletionTokens, b.CompletionTokens),
		CacheReadTokens:       max(a.CacheReadTokens, b.CacheReadTokens),
		CacheWriteTokens:      max(a.CacheWriteTokens, b.CacheWriteTokens),
		TotalTokens:           max(a.TotalTokens, b.TotalTokens),
		ReasoningTokens:       max(a.ReasoningTokens, b.ReasoningTokens),
		AudioPromptTokens:     max(a.AudioPromptTokens, b.AudioPromptTokens),
		AudioCompletionTokens: max(a.AudioCompletionTokens, b.AudioCompletionTokens),
	}
}

// ProxyInputWeight estimates the input-token weight of a passthrough request for
// the provider rate limiter.
//
// An estimate is correct here and wrong for cost_events: the limiter is a
// self-imposed throttle whose job is to keep us under an upstream ceiling, so an
// approximate weight is the intended semantic. Adapters previously passed 0,
// which spent an RPM and a concurrency slot but contributed nothing to the
// input-TPM budget.
func ProxyInputWeight(r *http.Request) int {
	if r == nil || r.ContentLength <= 0 {
		return 0
	}

	// Same 4-chars-per-token heuristic EstimateInputTokens uses.
	return int(r.ContentLength / 4)
}
