package server

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// Scheduled organization tasks: a cron trigger whose target is an
// organization opens one task for the org's head agent per tick, exactly
// like a /new from Telegram. When the trigger names a Telegram bot and chat,
// that chat receives the start message, telegram_notify progress and the
// final result.

const scheduledTaskDescriptionMax = 16 * 1024

// scheduledTaskConfig is the organization-target part of trigger.Config.
type scheduledTaskConfig struct {
	Title         string
	Description   string
	MaxIterations int
	NotifyBotID   string
	NotifyChatID  int64
}

func parseScheduledTaskConfig(cfg map[string]any) (scheduledTaskConfig, error) {
	var out scheduledTaskConfig
	out.Title, _ = cfg["task_title"].(string)
	out.Title = strings.TrimSpace(out.Title)
	out.Description, _ = cfg["task_description"].(string)
	out.Description = strings.TrimSpace(out.Description)
	if out.Title == "" {
		return out, fmt.Errorf("organization schedule requires config.task_title")
	}
	if len(out.Title) > 200 {
		return out, fmt.Errorf("config.task_title must be at most 200 bytes")
	}
	if len(out.Description) > scheduledTaskDescriptionMax {
		return out, fmt.Errorf("config.task_description must be at most %d bytes", scheduledTaskDescriptionMax)
	}
	switch v := cfg["max_iterations"].(type) {
	case nil:
	case float64:
		out.MaxIterations = int(v)
	case int:
		out.MaxIterations = v
	default:
		return out, fmt.Errorf("config.max_iterations must be a number")
	}
	if out.MaxIterations < 0 {
		return out, fmt.Errorf("config.max_iterations must not be negative")
	}
	out.NotifyBotID, _ = cfg["notify_bot_id"].(string)
	out.NotifyBotID = strings.TrimSpace(out.NotifyBotID)
	chat := ""
	switch v := cfg["notify_chat_id"].(type) {
	case string:
		chat = strings.TrimSpace(v)
	case float64:
		chat = strconv.FormatInt(int64(v), 10)
	}
	if (out.NotifyBotID == "") != (chat == "") {
		return out, fmt.Errorf("config.notify_bot_id and config.notify_chat_id must be set together")
	}
	if chat != "" {
		id, err := strconv.ParseInt(chat, 10, 64)
		if err != nil || id <= 0 {
			return out, fmt.Errorf("config.notify_chat_id must be the numeric Telegram user ID of a private chat")
		}
		out.NotifyChatID = id
	}
	return out, nil
}

// validateScheduledTaskTrigger checks an organization-target trigger before
// it is stored. The notify chat must be an allowed user of that bot, so a
// schedule cannot message anyone the bot does not already admit.
func (s *Server) validateScheduledTaskTrigger(ctx context.Context, t service.Trigger) error {
	if t.TargetType != service.TriggerTargetOrganization {
		return nil
	}
	if t.Type != "cron" {
		return fmt.Errorf("organization targets are only supported for cron triggers")
	}
	cfg, err := parseScheduledTaskConfig(t.Config)
	if err != nil {
		return err
	}
	if cfg.NotifyBotID == "" {
		return nil
	}
	if s.botConfigStore == nil {
		return fmt.Errorf("bot store not configured")
	}
	bot, err := s.botConfigStore.GetBotConfig(ctx, cfg.NotifyBotID)
	if err != nil || bot == nil {
		return fmt.Errorf("notify bot %q not found", cfg.NotifyBotID)
	}
	if bot.Platform != "telegram" {
		return fmt.Errorf("notify bot must be a Telegram bot")
	}
	if !slices.Contains(bot.AllowedUsers, strconv.FormatInt(cfg.NotifyChatID, 10)) {
		return fmt.Errorf("notify_chat_id must be one of the bot's allowed users")
	}
	return nil
}

// launchScheduledOrganizationTask runs under the trigger's execution
// identity (bound by the scheduler).
func (s *Server) launchScheduledOrganizationTask(ctx context.Context, trigger service.Trigger) error {
	cfg, err := parseScheduledTaskConfig(trigger.Config)
	if err != nil {
		return err
	}
	if s.organizationStore == nil || s.taskStore == nil {
		return fmt.Errorf("task/org stores not configured")
	}
	org, err := s.organizationStore.GetOrganization(ctx, trigger.TargetID)
	if err != nil {
		return fmt.Errorf("get organization: %w", err)
	}
	if org == nil || org.HeadAgentID == "" {
		return fmt.Errorf("organization %s not found or has no head agent", trigger.TargetID)
	}

	var bot *tgbotapi.BotAPI
	if cfg.NotifyBotID != "" {
		bot = s.runningTelegramBot(cfg.NotifyBotID)
		if bot == nil {
			slog.Warn("scheduled task: notify bot is not running; the task runs without Telegram messages", "trigger_id", trigger.ID, "bot_id", cfg.NotifyBotID)
			workflow.CronRunFromContext(ctx).Log(service.CronRunLogError, "The Telegram notify bot is not running; this run sends no Telegram messages.")
		}
	}

	counter, err := s.organizationStore.IncrementIssueCounter(ctx, org.ID)
	if err != nil {
		return fmt.Errorf("generate identifier: %w", err)
	}
	prefix := org.IssuePrefix
	if prefix == "" {
		prefix = org.ID
		if len(prefix) > 4 {
			prefix = prefix[:4]
		}
	}
	identifier := fmt.Sprintf("%s-%d", prefix, counter)

	loc := time.UTC
	if tz, _ := trigger.Config["timezone"].(string); tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	title := fmt.Sprintf("%s (%s)", cfg.Title, time.Now().In(loc).Format("2006-01-02"))
	description := cfg.Description
	if description == "" {
		description = cfg.Title
	}

	record, err := s.createRuntimeTask(ctx, service.Task{
		OrganizationID:  org.ID,
		AssignedAgentID: org.HeadAgentID,
		Title:           title,
		Description:     description,
		Status:          service.TaskStatusTodo,
		Identifier:      identifier,
		MaxIterations:   cfg.MaxIterations,
		CreatedBy:       "schedule:" + trigger.ID,
	})
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}

	run := workflow.CronRunFromContext(ctx)
	run.LinkTask(record.ID, identifier)
	run.Log(service.CronRunLogSystem, fmt.Sprintf("Task %s created for the head agent: %s", identifier, title))

	callbacks := []TaskDoneCallback{func(ident, status, result string) {
		run.Log(service.CronRunLogSystem, fmt.Sprintf("Task %s finished with status %s.", ident, status))
		final := cronRunStatusForTask(status)
		errMsg := ""
		if final != service.CronRunCompleted {
			errMsg = truncateTelegram(result, 2000)
		}
		run.Finish(final, errMsg, result)
	}}
	if bot != nil {
		chatID := cfg.NotifyChatID
		s.registerTelegramTaskChannel(s.ctxOrBackground(), bot, chatID, record.ID, identifier)
		callbacks = append(callbacks, func(ident, status, result string) {
			s.unregisterTelegramTaskChannel(record.ID)
			sendTelegramTaskOutcome(bot, chatID, ident, status, result)
		})
		prefix := "⏰ Scheduled task"
		if run.Source == service.CronRunSourceManual {
			prefix = "▶️ Manually started task"
		}
		sendTelegramText(bot, chatID, fmt.Sprintf("%s %s started.\nTitle: %s\n\nProgress and the result will be sent here.", prefix, identifier, title))
	}
	completion := s.botTaskDoneCallback(record.ID, identifier, callbacks)

	if err := s.startDelegationRun(context.WithoutCancel(ctx), org, record, org.HeadAgentID, 0, completion); err != nil {
		s.unregisterTelegramTaskChannel(record.ID)
		if bot != nil {
			sendTelegramText(bot, cfg.NotifyChatID, fmt.Sprintf("Scheduled task %s could not start: %v", identifier, err))
		}
		return fmt.Errorf("start delegation: %w", err)
	}
	slog.Info("scheduled task started", "trigger_id", trigger.ID, "task_id", record.ID, "identifier", identifier, "organization_id", org.ID)
	return nil
}

func (s *Server) ctxOrBackground() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

// sendTelegramTaskOutcome reports a finished task the same way custom
// commands do.
func sendTelegramTaskOutcome(bot *tgbotapi.BotAPI, chatID int64, ident, status, result string) {
	switch status {
	case "done", "completed":
		sendTelegramCompletedResult(bot, chatID, ident, result, "Task")
	case "blocked":
		if strings.HasPrefix(result, "[ITERATION_LIMIT]") {
			sendTelegramText(bot, chatID, fmt.Sprintf("Task %s paused at the iteration limit. Partial progress saved.\n/resume %s to continue.", sanitizeUTF8(ident), sanitizeUTF8(ident)))
			return
		}
		sendTelegramText(bot, chatID, fmt.Sprintf("Task %s blocked.\n\n%s\n\n/resume %s to retry.", sanitizeUTF8(ident), truncateTelegram(result, 500), sanitizeUTF8(ident)))
	default:
		sendTelegramText(bot, chatID, fmt.Sprintf("Task %s %s.\n\n%s", sanitizeUTF8(ident), status, truncateTelegram(result, 500)))
	}
}

func truncateTelegram(s string, n int) string {
	s = sanitizeUTF8(s)
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}
