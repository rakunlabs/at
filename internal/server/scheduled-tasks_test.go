package server

import (
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestParseScheduledTaskConfig(t *testing.T) {
	tests := []struct {
		name  string
		cfg   map[string]any
		valid bool
	}{
		{"minimal", map[string]any{"task_title": "Daily"}, true},
		{"notify", map[string]any{"task_title": "Daily", "notify_bot_id": "b", "notify_chat_id": "42", "max_iterations": float64(80)}, true},
		{"no title", map[string]any{"task_description": "x"}, false},
		{"bot without chat", map[string]any{"task_title": "Daily", "notify_bot_id": "b"}, false},
		{"group chat", map[string]any{"task_title": "Daily", "notify_bot_id": "b", "notify_chat_id": "-100"}, false},
		{"negative iterations", map[string]any{"task_title": "Daily", "max_iterations": float64(-1)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseScheduledTaskConfig(tt.cfg); (err == nil) != tt.valid {
				t.Fatalf("valid=%v err=%v", tt.valid, err)
			}
		})
	}
}

func TestValidateScheduledTaskTriggerNotifyChat(t *testing.T) {
	s := &Server{botConfigStore: &videoCommandBotStore{cfg: service.BotConfig{ID: "b", Platform: "telegram", AllowedUsers: []string{"42"}}}}
	trigger := func(chat string) service.Trigger {
		return service.Trigger{TargetType: service.TriggerTargetOrganization, TargetID: "org", Type: "cron",
			Config: map[string]any{"task_title": "Daily", "notify_bot_id": "b", "notify_chat_id": chat}}
	}
	if err := s.validateScheduledTaskTrigger(t.Context(), trigger("42")); err != nil {
		t.Fatalf("allowed chat refused: %v", err)
	}
	if err := s.validateScheduledTaskTrigger(t.Context(), trigger("7")); err == nil {
		t.Fatal("chat outside the bot's allowed users accepted")
	}
	http := trigger("42")
	http.Type = "http"
	if err := s.validateScheduledTaskTrigger(t.Context(), http); err == nil {
		t.Fatal("http organization trigger accepted")
	}
	workflow := service.Trigger{TargetType: service.TriggerTargetWorkflow, TargetID: "wf", Type: "cron"}
	if err := s.validateScheduledTaskTrigger(t.Context(), workflow); err != nil {
		t.Fatalf("workflow triggers must be untouched: %v", err)
	}
}
