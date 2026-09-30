package service

import "testing"

func TestNormalizeChatPresetsReasoningEffort(t *testing.T) {
	tests := []struct {
		name    string
		effort  string
		wantErr bool
	}{
		{name: "default", effort: ""},
		{name: "high", effort: "high"},
		{name: "xhigh", effort: "xhigh"},
		{name: "max", effort: "max"},
		{name: "invalid", effort: "extreme", wantErr: true},
		{name: "case sensitive", effort: "High", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := NormalizeChatPresets([]ChatPreset{{Name: "p", ChatWorkbenchSetup: ChatWorkbenchSetup{ReasoningEffort: tt.effort}}})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && out[0].ReasoningEffort != tt.effort {
				t.Fatalf("effort = %q, want %q", out[0].ReasoningEffort, tt.effort)
			}
		})
	}
}
