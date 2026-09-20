// Package traceexport sends workspace-owned observations over OTLP. It uses the
// OTLP wire contract directly so environment-wide SDK exporter settings cannot
// redirect a workspace's data or inject another workspace's credentials.
package traceexport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	collector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	resource "go.opentelemetry.io/proto/otlp/resource/v1"
	trace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/rakunlabs/at/internal/service"
)

const ContentLimit = 16384

func Clip(s string) string {
	if len(s) <= ContentLimit {
		return s
	}
	n := ContentLimit
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "...[truncated]"
}

func stringAttr(k, v string) *common.KeyValue {
	return &common.KeyValue{Key: k, Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: v}}}
}
func intAttr(k string, v int64) *common.KeyValue {
	return &common.KeyValue{Key: k, Value: &common.AnyValue{Value: &common.AnyValue_IntValue{IntValue: v}}}
}

// Stable, workspace-namespaced IDs preserve parenting even when observations
// arrive out of order, on different replicas, or after a restart.
func TraceID(workspace string, call service.LLMCall) []byte {
	h := sha256.Sum256([]byte(workspace + "\x00" + call.TokenID + "\x00" + call.TraceID))
	return h[:16]
}
func spanID(id string) []byte { h := sha256.Sum256([]byte(id)); return h[:8] }

func Request(workspace string, calls []service.LLMCall, content bool) *collector.ExportTraceServiceRequest {
	spans := make([]*trace.Span, 0, len(calls))
	for _, c := range calls {
		end, err := time.Parse(time.RFC3339Nano, c.CreatedAt)
		if err != nil {
			end = time.Now()
		}
		start := end.Add(-time.Duration(max(c.LatencyMs, 0)) * time.Millisecond)
		name := c.Name
		if name == "" {
			name = "chat " + c.Model
		}
		s := &trace.Span{TraceId: TraceID(workspace, c), SpanId: spanID(c.ID), Name: name, Kind: trace.Span_SPAN_KIND_INTERNAL, StartTimeUnixNano: uint64(start.UnixNano()), EndTimeUnixNano: uint64(end.UnixNano())}
		if c.ParentObservationID != "" {
			s.ParentSpanId = spanID(c.ParentObservationID)
		}
		s.Attributes = []*common.KeyValue{
			stringAttr("at.trace_id", c.TraceID), stringAttr("at.observation_id", c.ID), stringAttr("at.workspace_id", workspace), stringAttr("at.source", c.Source),
			stringAttr("langfuse.session.id", c.SessionID), stringAttr("langfuse.user.id", c.UserField),
			stringAttr("at.token_id", c.TokenID), stringAttr("at.agent_id", c.AgentID), stringAttr("at.task_id", c.TaskID), stringAttr("at.run_id", c.RunID), stringAttr("at.organization_id", c.OrganizationID),
		}
		kind := "span"
		input, output := c.Input, c.Output
		switch c.ObservationType {
		case service.ObservationTool:
			kind = "tool"
			s.Attributes = append(s.Attributes, stringAttr("gen_ai.operation.name", "execute_tool"), stringAttr("gen_ai.tool.name", c.Name))
		case service.ObservationEvent:
			kind = "event"
		default:
			kind = "generation"
			s.Kind = trace.Span_SPAN_KIND_CLIENT
			input, output = c.RequestBody, c.ResponseBody
			s.Attributes = append(s.Attributes, stringAttr("gen_ai.operation.name", "chat"), stringAttr("gen_ai.provider.name", c.Provider), stringAttr("gen_ai.request.model", c.RequestedModel), stringAttr("gen_ai.response.model", c.Model), stringAttr("langfuse.observation.model.name", c.Model),
				intAttr("gen_ai.usage.input_tokens", c.InputTokens), intAttr("gen_ai.usage.output_tokens", c.OutputTokens), intAttr("gen_ai.usage.cache_read.input_tokens", c.CacheReadTokens), intAttr("gen_ai.usage.cache_creation.input_tokens", c.CacheWriteTokens), intAttr("gen_ai.usage.reasoning.output_tokens", c.ReasoningTokens), intAttr("at.ttft_ms", c.TimeToFirstTokenMs), stringAttr("at.finish_reason", c.FinishReason))
			usage, _ := json.Marshal(map[string]int64{"input": c.InputTokens, "output": c.OutputTokens, "cache_read_input_tokens": c.CacheReadTokens, "cache_creation_input_tokens": c.CacheWriteTokens})
			cost, _ := json.Marshal(map[string]float64{"total": c.CostCents / 100})
			s.Attributes = append(s.Attributes, stringAttr("langfuse.observation.usage_details", string(usage)), stringAttr("langfuse.observation.cost_details", string(cost)))
		}
		s.Attributes = append(s.Attributes, stringAttr("langfuse.observation.type", kind))
		if content {
			s.Attributes = append(s.Attributes, stringAttr("langfuse.observation.input", Clip(input)), stringAttr("langfuse.observation.output", Clip(output)))
		}
		if c.Status == "error" || c.Level == service.ObservationLevelError {
			s.Status = &trace.Status{Code: trace.Status_STATUS_CODE_ERROR}
			// Upstream error text can contain prompt fragments; it follows the
			// same opt-in as other content rather than bypassing that switch.
			if content {
				s.Status.Message = Clip(c.ErrorMessage)
			}
			s.Attributes = append(s.Attributes, stringAttr("langfuse.observation.level", "ERROR"), stringAttr("error.type", c.ErrorCode))
		}
		spans = append(spans, s)
	}
	return &collector.ExportTraceServiceRequest{ResourceSpans: []*trace.ResourceSpans{{Resource: &resource.Resource{Attributes: []*common.KeyValue{stringAttr("service.name", "at"), stringAttr("at.workspace_id", workspace)}}, ScopeSpans: []*trace.ScopeSpans{{Scope: &common.InstrumentationScope{Name: "github.com/rakunlabs/at/workspace-traces"}, Spans: spans}}}}}
}

// Send confirms an actual Export RPC, including partial rejections. All errors
// are deliberately local descriptions: receiver bodies may echo secret headers.
func Send(ctx context.Context, cfg service.TraceExportSettings, request *collector.ExportTraceServiceRequest) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if cfg.Endpoint == "" {
		return fmt.Errorf("enter an endpoint before testing")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	headers := make(map[string]string, len(cfg.Headers)+1)
	for k, v := range cfg.Headers {
		headers[strings.ToLower(k)] = v
	}
	if cfg.Target == "langfuse" {
		headers["authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(cfg.PublicKey+":"+cfg.SecretKey))
		if headers["x-langfuse-ingestion-version"] == "" {
			headers["x-langfuse-ingestion-version"] = "4"
		}
	}
	var response *collector.ExportTraceServiceResponse
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		var retry bool
		if cfg.Protocol == "grpc" {
			response, retry, err = sendGRPC(ctx, cfg.Endpoint, headers, request)
		} else {
			response, retry, err = sendHTTP(ctx, cfg.Endpoint, headers, request)
		}
		if err == nil || !retry || attempt == 1 {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("OTLP request timed out or was cancelled")
		case <-time.After(250 * time.Millisecond):
		}
	}
	if err != nil {
		return err
	}
	if p := response.GetPartialSuccess(); p != nil && (p.RejectedSpans > 0 || p.ErrorMessage != "") {
		return fmt.Errorf("receiver reported partial success (%d rejected spans); check receiver logs", p.RejectedSpans)
	}
	return nil
}

var httpClient = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func sendHTTP(ctx context.Context, endpoint string, headers map[string]string, data *collector.ExportTraceServiceRequest) (*collector.ExportTraceServiceResponse, bool, error) {
	b, err := proto.Marshal(data)
	if err != nil {
		return nil, false, fmt.Errorf("encode OTLP request: %w", err)
	}
	r, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(b))
	if err != nil {
		return nil, false, fmt.Errorf("invalid OTLP endpoint")
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	r.Header.Set("Content-Type", "application/x-protobuf")
	r.Header.Set("Accept", "application/x-protobuf")
	resp, err := httpClient.Do(r)
	if err != nil {
		return nil, true, fmt.Errorf("OTLP HTTP connection failed; check DNS, network, TLS certificate and endpoint")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode == 429 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504, fmt.Errorf("OTLP HTTP returned %d; check endpoint and authentication", resp.StatusCode)
	}
	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if ct != "application/x-protobuf" {
		return nil, false, fmt.Errorf("endpoint did not return an OTLP protobuf response; check the full trace endpoint path")
	}
	b, err = io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(b) > 65536 {
		return nil, false, fmt.Errorf("invalid or oversized OTLP response")
	}
	response := &collector.ExportTraceServiceResponse{}
	if proto.Unmarshal(b, response) != nil {
		return nil, false, fmt.Errorf("invalid OTLP protobuf response")
	}
	return response, false, nil
}

func sendGRPC(ctx context.Context, endpoint string, headers map[string]string, data *collector.ExportTraceServiceRequest) (*collector.ExportTraceServiceResponse, bool, error) {
	u, _ := url.Parse(endpoint)
	var creds credentials.TransportCredentials = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	if u.Scheme == "http" {
		creds = insecure.NewCredentials()
	}
	conn, err := grpc.NewClient("dns:///"+u.Host, grpc.WithTransportCredentials(creds), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(65536)))
	if err != nil {
		return nil, false, fmt.Errorf("cannot initialize OTLP gRPC connection")
	}
	defer conn.Close()
	ctx = metadata.NewOutgoingContext(ctx, metadata.New(headers))
	response, err := collector.NewTraceServiceClient(conn).Export(ctx, data)
	if err != nil {
		code := status.Code(err)
		return nil, code == codes.Unavailable || code == codes.ResourceExhausted, fmt.Errorf("OTLP gRPC returned %s; check endpoint, TLS and authentication", code)
	}
	return response, false, nil
}

func TraceHex(workspace string, call service.LLMCall) string {
	return hex.EncodeToString(TraceID(workspace, call))
}
