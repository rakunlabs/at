package server

import (
	"context"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/rakunlabs/at/internal/service"
)

func TestValidateBotCommandSchedules(t *testing.T) {
	base := func(cmd service.BotCustomCommand) service.BotConfig {
		return service.BotConfig{Platform: "telegram", AccessMode: "allowlist", AllowedUsers: []string{"42"}, CustomCommands: []service.BotCustomCommand{cmd}}
	}
	tests := []struct {
		name  string
		cfg   service.BotConfig
		valid bool
	}{
		{"no schedule", base(service.BotCustomCommand{Command: "x"}), true},
		{"valid", base(service.BotCustomCommand{Command: "x", Schedule: "0 7 * * *", ScheduleTimezone: "Europe/Amsterdam", ScheduleChatID: "42", ScheduleArgs: "topic"}), true},
		{"bad cron", base(service.BotCustomCommand{Command: "x", Schedule: "nope", ScheduleChatID: "42"}), false},
		{"bad timezone", base(service.BotCustomCommand{Command: "x", Schedule: "0 7 * * *", ScheduleTimezone: "Mars/Base", ScheduleChatID: "42"}), false},
		{"chat not allowed", base(service.BotCustomCommand{Command: "x", Schedule: "0 7 * * *", ScheduleChatID: "43"}), false},
		{"group chat id", base(service.BotCustomCommand{Command: "x", Schedule: "0 7 * * *", ScheduleChatID: "-100"}), false},
		{"multiline args", base(service.BotCustomCommand{Command: "x", Schedule: "0 7 * * *", ScheduleChatID: "42", ScheduleArgs: "a\nb"}), false},
		{"chat without schedule", base(service.BotCustomCommand{Command: "x", ScheduleChatID: "42"}), false},
		{"open bot", service.BotConfig{Platform: "telegram", AccessMode: "open", AllowedUsers: []string{"42"}, CustomCommands: []service.BotCustomCommand{{Command: "x", Schedule: "0 7 * * *", ScheduleChatID: "42"}}}, false},
		{"discord", service.BotConfig{Platform: "discord", AccessMode: "allowlist", AllowedUsers: []string{"42"}, CustomCommands: []service.BotCustomCommand{{Command: "x", Schedule: "0 7 * * *", ScheduleChatID: "42"}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateBotVideoCommands(tt.cfg); (err == nil) != tt.valid {
				t.Fatalf("valid=%v err=%v", tt.valid, err)
			}
		})
	}
}

func TestTelegramScheduledMessageParsesAsCommand(t *testing.T) {
	at := time.Date(2026, 10, 7, 5, 0, 0, 0, time.UTC)
	msg := telegramScheduledMessage(service.BotCustomCommand{Command: "shorts", ScheduleArgs: "daily pick"}, 42, at)
	if msg.Command() != "shorts" || msg.CommandArguments() != "daily pick" {
		t.Fatalf("command=%q args=%q", msg.Command(), msg.CommandArguments())
	}
	if msg.Chat.ID != 42 || msg.From.ID != 42 || msg.MessageID <= 0 {
		t.Fatalf("unexpected message: %+v", msg)
	}
	bare := telegramScheduledMessage(service.BotCustomCommand{Command: "/shorts"}, 42, at)
	if bare.Command() != "shorts" || bare.CommandArguments() != "" {
		t.Fatalf("bare command=%q args=%q", bare.Command(), bare.CommandArguments())
	}
	if !strings.HasPrefix(telegramCronSpec(service.BotCustomCommand{Schedule: "0 7 * * *", ScheduleTimezone: "Europe/Amsterdam"}), "CRON_TZ=Europe/Amsterdam ") {
		t.Fatal("timezone not applied")
	}
}

func TestSameCommandSchedules(t *testing.T) {
	a := []service.BotCustomCommand{{Command: "x", Schedule: "0 7 * * *", ScheduleChatID: "1"}, {Command: "y", Brief: "b"}}
	b := []service.BotCustomCommand{{Command: "y", Brief: "changed"}, {Command: "x", Schedule: "0 7 * * *", ScheduleChatID: "1"}}
	if !sameCommandSchedules(a, b) {
		t.Fatal("brief changes must not restart the bot")
	}
	b[1].Schedule = "0 8 * * *"
	if sameCommandSchedules(a, b) {
		t.Fatal("schedule change not detected")
	}
}

type notifyTaskStore struct {
	service.TaskStorer
	tasks map[string]*service.Task
}

func (m *notifyTaskStore) GetTask(_ context.Context, id string) (*service.Task, error) {
	return m.tasks[id], nil
}

func TestTelegramNotifyResolvesRootChannel(t *testing.T) {
	s := &Server{taskStore: &notifyTaskStore{tasks: map[string]*service.Task{
		"child": {ID: "child", ParentID: "root"},
		"root":  {ID: "root"},
		"other": {ID: "other"},
	}}}
	ctx, cancel := context.WithCancel(t.Context())
	s.registerTelegramTaskChannel(ctx, &tgbotapi.BotAPI{}, 42, "root", "YTS-1")

	channel, root := s.telegramChannelForTask(t.Context(), "child")
	if channel == nil || root != "root" || channel.chatID != 42 {
		t.Fatalf("child did not resolve root channel: %v %q", channel, root)
	}
	if channel, _ := s.telegramChannelForTask(t.Context(), "other"); channel != nil {
		t.Fatal("unrelated task resolved a channel")
	}

	out, err := s.execTelegramNotify(contextWithTaskID(t.Context(), "other"), map[string]any{"message": "hi"})
	if err != nil || !strings.Contains(out, `"sent":false`) {
		t.Fatalf("non-telegram task: out=%s err=%v", out, err)
	}
	if _, err := s.execTelegramNotify(t.Context(), map[string]any{"message": "hi"}); err == nil {
		t.Fatal("notify outside a task succeeded")
	}
	if _, err := s.execTelegramNotify(contextWithTaskID(t.Context(), "child"), map[string]any{}); err == nil {
		t.Fatal("empty message accepted")
	}

	cancel()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, ok := s.telegramTaskChannels.Load("root"); !ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("channel survived bot shutdown")
}
