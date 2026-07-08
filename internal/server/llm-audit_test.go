package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/loopgov"
)

func TestClipOrSpill_SmallBodyInline(t *testing.T) {
	s := &Server{loopGov: loopgov.New(loopgov.Config{WorkspaceRoot: t.TempDir()}, nil)}

	body := []byte(`{"hello":"world"}`)
	inline, truncated, ref := s.clipOrSpill("id1", "request", body)
	if truncated {
		t.Fatal("small body should not be truncated")
	}
	if ref != "" {
		t.Fatalf("small body should not spill, got ref=%q", ref)
	}
	if inline != string(body) {
		t.Fatalf("inline body mismatch: %q", inline)
	}
}

func TestClipOrSpill_LargeBodySpills(t *testing.T) {
	root := t.TempDir()
	s := &Server{loopGov: loopgov.New(loopgov.Config{WorkspaceRoot: root}, nil)}

	big := []byte(strings.Repeat("y", service.LLMCallBodyMaxBytes+1000))
	inline, truncated, ref := s.clipOrSpill("id2", "response", big)
	if !truncated {
		t.Fatal("large body must be truncated")
	}
	if ref == "" {
		t.Fatal("large body must spill to a file")
	}
	if len(inline) <= service.LLMCallBodyMaxBytes {
		// inline should be the cap plus the truncation marker
		if !strings.Contains(inline, "truncated") {
			t.Fatalf("inline should carry truncation marker, got len=%d", len(inline))
		}
	}
	// The spill file must contain the full payload.
	saved, err := os.ReadFile(ref)
	if err != nil {
		t.Fatalf("read spill file: %v", err)
	}
	if len(saved) != len(big) {
		t.Fatalf("spill file size mismatch: got=%d want=%d", len(saved), len(big))
	}
	// The spill file must live under the .at-llm-audit dir.
	if !strings.HasPrefix(ref, filepath.Join(root, llmAuditDumpDir)) {
		t.Fatalf("spill file in unexpected location: %q", ref)
	}
}

func TestBuildLLMCall_Attribution(t *testing.T) {
	s := &Server{}

	p := llmAuditParams{
		source:         "gateway",
		endpoint:       "/gateway/v1/chat/completions",
		traceID:        "trace-x",
		sessionID:      "sess-x",
		requestedModel: "openai/gpt-4o",
		fullModel:      "openai/gpt-4o",
		usage:          service.Usage{PromptTokens: 12, CompletionTokens: 8},
		latencyMs:      500,
		status:         "ok",
	}
	call := s.buildLLMCall(t.Context(), p)

	if call.Provider != "openai" || call.Model != "gpt-4o" {
		t.Fatalf("provider/model split wrong: %q/%q", call.Provider, call.Model)
	}
	if call.TraceID != "trace-x" || call.SessionID != "sess-x" {
		t.Fatalf("trace/session not carried: %+v", call)
	}
	if call.InputTokens != 12 || call.OutputTokens != 8 {
		t.Fatalf("token mapping wrong: %+v", call)
	}
	if call.ID == "" || call.CreatedAt == "" {
		t.Fatal("id/created_at must be populated")
	}
}

func TestBuildLLMCall_GeneratesTraceID(t *testing.T) {
	s := &Server{}
	call := s.buildLLMCall(t.Context(), llmAuditParams{source: "gateway", fullModel: "openai/gpt-4o"})
	if call.TraceID == "" {
		t.Fatal("trace ID should be auto-generated when not supplied")
	}
}

func TestStreamAuditResponseBody_Reconstructs(t *testing.T) {
	usage := &service.Usage{PromptTokens: 3, CompletionTokens: 4}
	body := streamAuditResponseBody("chatcmpl-1", "openai/gpt-4o", "hello world", "", nil, "stop", usage)
	s := string(body)
	if !strings.Contains(s, "hello world") {
		t.Fatalf("content not in reconstructed body: %s", s)
	}
	if !strings.Contains(s, "chatcmpl-1") {
		t.Fatalf("id not in reconstructed body: %s", s)
	}
	if !strings.Contains(s, "stop") {
		t.Fatalf("finish_reason not in reconstructed body: %s", s)
	}
}
