package server

import (
	"encoding/json"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// Tools that create unattended subjects must tell chat surfaces that a person
// still has to approve the Run as account, while keeping the record's fields.
func TestTriggerCreateResultRequestsBinding(t *testing.T) {
	tests := []struct {
		name   string
		record any
		kind   string
		id     string
		field  string
		want   string
	}{
		{name: "trigger", record: &service.Trigger{ID: "01TRG", Type: "cron"}, kind: "trigger", id: "01TRG", field: "type", want: "cron"},
		{name: "bot", record: &service.BotConfig{ID: "01BOT", Platform: "telegram"}, kind: "bot", id: "01BOT", field: "platform", want: "telegram"},
		{name: "mcp", record: &service.MCPServer{ID: "01MCP", Name: "tools"}, kind: "mcp", id: "01MCP", field: "name", want: "tools"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := withBindingRequired(tt.record, tt.kind, tt.id, "note")
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("result is not JSON: %v", err)
			}
			if got["id"] != tt.id || got[tt.field] != tt.want {
				t.Fatalf("record fields lost: %v", got)
			}
			req, _ := got["binding_required"].(map[string]any)
			if req["kind"] != tt.kind || req["subject_id"] != tt.id {
				t.Fatalf("binding_required = %v", req)
			}
		})
	}
}
