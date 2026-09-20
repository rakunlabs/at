package traceexport

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	collector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/rakunlabs/at/internal/service"
)

func TestTraceHierarchyAndContent(t *testing.T) {
	calls := []service.LLMCall{
		{ID: "gen", TraceID: "run", ObservationType: "generation", RequestBody: "private prompt", ResponseBody: "answer", LatencyMs: 20, InputTokens: 42},
		{ID: "tool", TraceID: "run", ParentObservationID: "gen", ObservationType: "tool", Input: "secret args", ErrorMessage: "private error", Status: "error"},
	}
	for _, content := range []bool{false, true} {
		r := Request("one", calls, content)
		spans := r.ResourceSpans[0].ScopeSpans[0].Spans
		if !bytes.Equal(spans[0].TraceId, spans[1].TraceId) || !bytes.Equal(spans[0].SpanId, spans[1].ParentSpanId) {
			t.Fatal("hierarchy lost")
		}
		if spans[0].EndTimeUnixNano-spans[0].StartTimeUnixNano != uint64(20*time.Millisecond) {
			t.Fatal("wrong duration")
		}
		wire, _ := proto.Marshal(r)
		if bytes.Contains(wire, []byte("private prompt")) != content || bytes.Contains(wire, []byte("secret args")) != content || bytes.Contains(wire, []byte("private error")) != content {
			t.Fatal("content switch ignored")
		}
	}
	if bytes.Equal(TraceID("one", calls[0]), TraceID("two", calls[0])) {
		t.Fatal("workspace namespace lost")
	}
	other := calls[0]
	other.TokenID = "other-token"
	if bytes.Equal(TraceID("one", calls[0]), TraceID("one", other)) {
		t.Fatal("token namespace lost")
	}
}

func TestHTTPExport(t *testing.T) {
	var received atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/custom/v1/traces" || r.Header.Get("Authorization") != "Basic cGs6c2s=" || r.Header.Get("X-Langfuse-Ingestion-Version") != "4" {
			t.Error("wrong endpoint or auth")
		}
		b, _ := io.ReadAll(r.Body)
		var req collector.ExportTraceServiceRequest
		if err := proto.Unmarshal(b, &req); err != nil || len(req.ResourceSpans) != 1 {
			t.Error("invalid wire request")
		}
		received.Add(1)
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer receiver.Close()
	c := service.DefaultTraceExportSettings()
	c.Target = "langfuse"
	c.PublicKey = "pk"
	c.SecretKey = "sk"
	c.Endpoint = receiver.URL + "/custom/v1/traces"
	if err := Send(t.Context(), c, Request("one", []service.LLMCall{{ID: "test", TraceID: "test"}}, false)); err != nil {
		t.Fatal(err)
	}
	if received.Load() != 1 {
		t.Fatal("test never exported")
	}
}

func TestHTTPFailuresAndRetry(t *testing.T) {
	for _, name := range []string{"unauthorized", "html", "partial", "retry", "redirect"} {
		t.Run(name, func(t *testing.T) {
			var attempts atomic.Int32
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := attempts.Add(1)
				w.Header().Set("Content-Type", "application/x-protobuf")
				switch name {
				case "unauthorized":
					w.WriteHeader(401)
					_, _ = w.Write([]byte("echo-secret"))
				case "html":
					w.Header().Set("Content-Type", "text/html")
					_, _ = w.Write([]byte("<html>"))
				case "partial":
					b, _ := proto.Marshal(&collector.ExportTraceServiceResponse{PartialSuccess: &collector.ExportTracePartialSuccess{RejectedSpans: 1, ErrorMessage: "echo-secret"}})
					_, _ = w.Write(b)
				case "retry":
					if n == 1 {
						w.WriteHeader(503)
					}
				case "redirect":
					w.Header().Set("Location", "/other")
					w.WriteHeader(302)
				}
			}))
			defer receiver.Close()
			c := service.DefaultTraceExportSettings()
			c.Endpoint = receiver.URL
			err := Send(t.Context(), c, Request("one", nil, false))
			if name == "retry" {
				if err != nil || attempts.Load() != 2 {
					t.Fatalf("retry: %v %d", err, attempts.Load())
				}
				return
			}
			if err == nil || strings.Contains(err.Error(), "echo-secret") || attempts.Load() != 1 {
				t.Fatalf("unsafe or incorrect failure: %v, attempts %d", err, attempts.Load())
			}
		})
	}
}

type grpcReceiver struct {
	collector.UnimplementedTraceServiceServer
	calls atomic.Int32
}

func (s *grpcReceiver) Export(ctx context.Context, req *collector.ExportTraceServiceRequest) (*collector.ExportTraceServiceResponse, error) {
	s.calls.Add(1)
	md, _ := metadata.FromIncomingContext(ctx)
	if len(md.Get("authorization")) != 1 || md.Get("authorization")[0] != "Bearer test" {
		return nil, status.Error(codes.Unauthenticated, "echo-secret")
	}
	return &collector.ExportTraceServiceResponse{}, nil
}
func TestGRPCExport(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := grpc.NewServer()
	receiver := &grpcReceiver{}
	collector.RegisterTraceServiceServer(s, receiver)
	go s.Serve(l)
	defer s.Stop()
	c := service.DefaultTraceExportSettings()
	c.Protocol = "grpc"
	c.Endpoint = "http://" + l.Addr().String()
	c.Headers["Authorization"] = "Bearer test"
	if err := Send(t.Context(), c, Request("one", nil, false)); err != nil {
		t.Fatal(err)
	}
	c.Headers["Authorization"] = "wrong"
	if err := Send(t.Context(), c, Request("one", nil, false)); err == nil || strings.Contains(err.Error(), "echo-secret") {
		t.Fatalf("bad auth: %v", err)
	}
	if receiver.calls.Load() != 2 {
		t.Fatal("unexpected retry on auth failure")
	}
}
