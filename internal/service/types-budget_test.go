package service

import (
	"strings"
	"testing"
)

// TestTruncateForAudit pins the cap behavior used when folding tool
// inputs/outputs into AuditEntry.Details. We rely on this to keep the
// audit_log table from blowing up when a tool returns a megabyte-class
// payload — the LLM message-history governor doesn't apply to what we
// persist for human review.
func TestTruncateForAudit(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantSuffix  string // expected trailing marker, "" means none
		wantMaxLen  int    // upper bound on length
		wantExactly string // when set, expect exact equality
	}{
		{
			name:        "short string passes through unchanged",
			input:       "hello world",
			wantExactly: "hello world",
		},
		{
			name:        "exactly at cap passes through",
			input:       strings.Repeat("a", AuditPayloadMaxBytes),
			wantExactly: strings.Repeat("a", AuditPayloadMaxBytes),
		},
		{
			name:       "one byte over gets truncated",
			input:      strings.Repeat("a", AuditPayloadMaxBytes+1),
			wantSuffix: "...[truncated]",
			wantMaxLen: AuditPayloadMaxBytes + len("...[truncated]"),
		},
		{
			name:       "very large payload",
			input:      strings.Repeat("x", 1_000_000),
			wantSuffix: "...[truncated]",
			wantMaxLen: AuditPayloadMaxBytes + len("...[truncated]"),
		},
		{
			name:        "empty",
			input:       "",
			wantExactly: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateForAudit(tt.input)
			if tt.wantExactly != "" || tt.input == "" {
				if got != tt.wantExactly {
					t.Errorf("got %q, want %q", got, tt.wantExactly)
				}
				return
			}
			if !strings.HasSuffix(got, tt.wantSuffix) {
				t.Errorf("got %q, want suffix %q", got[len(got)-30:], tt.wantSuffix)
			}
			if len(got) > tt.wantMaxLen {
				t.Errorf("got len %d, want <= %d", len(got), tt.wantMaxLen)
			}
		})
	}
}
