package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/worldline-go/hardloop"

	"github.com/rakunlabs/at/internal/service"
)

// Scheduled Telegram commands run a bot's custom command on a cron schedule
// as if the configured chat had typed it, so the acknowledgement, progress
// notifications and completion message reach that chat exactly as they do
// for a manual command. The schedule lives on the running bot: it starts
// and stops with the adapter and uses the bot's execution identity.

const telegramScheduledArgsMax = 2000

// validateBotCommandSchedules checks schedule fields on custom commands.
// Commands without a schedule are untouched.
func validateBotCommandSchedules(cfg service.BotConfig) error {
	for _, cmd := range cfg.CustomCommands {
		if cmd.Schedule == "" {
			if cmd.ScheduleChatID != "" || cmd.ScheduleTimezone != "" || cmd.ScheduleArgs != "" {
				return fmt.Errorf("command /%s: schedule_chat_id, schedule_timezone and schedule_args require schedule", cmd.Command)
			}
			continue
		}
		if cfg.Platform != "telegram" {
			return fmt.Errorf("command /%s: schedules are only supported on Telegram bots", cmd.Command)
		}
		if _, err := hardloop.ParseStandard(telegramCronSpec(cmd)); err != nil {
			return fmt.Errorf("command /%s: invalid schedule %q: %w", cmd.Command, cmd.Schedule, err)
		}
		if cmd.ScheduleTimezone != "" {
			if cmd.ScheduleTimezone == "Local" {
				return fmt.Errorf("command /%s: schedule_timezone must be an explicit IANA name", cmd.Command)
			}
			if _, err := time.LoadLocation(cmd.ScheduleTimezone); err != nil {
				return fmt.Errorf("command /%s: invalid schedule_timezone %q", cmd.Command, cmd.ScheduleTimezone)
			}
		}
		chatID, err := strconv.ParseInt(strings.TrimSpace(cmd.ScheduleChatID), 10, 64)
		if err != nil || chatID <= 0 {
			return fmt.Errorf("command /%s: schedule_chat_id must be the numeric Telegram user ID of a private chat", cmd.Command)
		}
		if !slices.Contains(cfg.AllowedUsers, strconv.FormatInt(chatID, 10)) {
			return fmt.Errorf("command /%s: schedule_chat_id %d must be in the bot's allowed users", cmd.Command, chatID)
		}
		if cfg.AccessMode != "allowlist" && cfg.AccessMode != "pending" {
			return fmt.Errorf("command /%s: scheduled commands require an allowlist or pending access mode", cmd.Command)
		}
		if len(cmd.ScheduleArgs) > telegramScheduledArgsMax || strings.ContainsAny(cmd.ScheduleArgs, "\r\n") {
			return fmt.Errorf("command /%s: schedule_args must be a single line of at most %d bytes", cmd.Command, telegramScheduledArgsMax)
		}
	}
	return nil
}

// sameCommandSchedules reports whether two command lists schedule the same
// runs; a running bot only reloads its schedules when this changes.
func sameCommandSchedules(a, b []service.BotCustomCommand) bool {
	key := func(cmds []service.BotCustomCommand) []string {
		var out []string
		for _, c := range cmds {
			if c.Schedule != "" {
				out = append(out, strings.Join([]string{c.Command, c.Schedule, c.ScheduleTimezone, c.ScheduleChatID}, "\x00"))
			}
		}
		slices.Sort(out)
		return out
	}
	return slices.Equal(key(a), key(b))
}

func telegramCronSpec(cmd service.BotCustomCommand) string {
	spec := strings.TrimSpace(cmd.Schedule)
	if cmd.ScheduleTimezone != "" && !strings.HasPrefix(spec, "@every") {
		spec = "CRON_TZ=" + cmd.ScheduleTimezone + " " + spec
	}
	return spec
}

// telegramScheduledMessage builds the synthetic message a scheduled run
// dispatches. Its Entities mark the leading "/command" as a bot command, so
// Command()/CommandArguments() behave exactly as for a typed message. The
// message ID is the minute of the run, which makes video-template projects
// idempotent per scheduled occurrence.
func telegramScheduledMessage(cmd service.BotCustomCommand, chatID int64, at time.Time) *tgbotapi.Message {
	name := "/" + strings.TrimPrefix(cmd.Command, "/")
	text := name
	if args := strings.TrimSpace(cmd.ScheduleArgs); args != "" {
		text += " " + args
	}
	return &tgbotapi.Message{
		MessageID: int(at.Unix() / 60),
		From:      &tgbotapi.User{ID: chatID},
		Chat:      &tgbotapi.Chat{ID: chatID, Type: "private"},
		Date:      int(at.Unix()),
		Text:      text,
		Entities:  []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: len(name)}},
	}
}

// startTelegramCommandSchedules runs every scheduled custom command of the
// bot until ctx (the bot's own context) ends.
func (s *Server) startTelegramCommandSchedules(ctx context.Context, bot *tgbotapi.BotAPI, tgCtx *telegramContext, commands []service.BotCustomCommand) {
	var crons []hardloop.Cron
	for _, cmd := range commands {
		if cmd.Schedule == "" {
			continue
		}
		command := cmd
		chatID, err := strconv.ParseInt(strings.TrimSpace(command.ScheduleChatID), 10, 64)
		if err != nil || chatID <= 0 {
			slog.Warn("telegram bot: scheduled command has no valid chat", "bot_id", tgCtx.botID, "command", command.Command)
			continue
		}
		crons = append(crons, hardloop.Cron{
			Name:  fmt.Sprintf("telegram-%s-%s", tgCtx.botID, command.Command),
			Specs: []string{telegramCronSpec(command)},
			Func: func(runCtx context.Context) error {
				s.runTelegramScheduledCommand(runCtx, bot, tgCtx, command.Command, chatID)
				return nil
			},
		})
	}
	if len(crons) == 0 {
		return
	}
	job, err := hardloop.NewCron(crons...)
	if err != nil {
		slog.Error("telegram bot: invalid command schedules", "bot_id", tgCtx.botID, "error", err)
		return
	}
	if err := job.Start(ctx); err != nil {
		slog.Error("telegram bot: start command schedules", "bot_id", tgCtx.botID, "error", err)
		return
	}
	slog.Info("telegram bot: command schedules started", "bot_id", tgCtx.botID, "count", len(crons))
	go func() {
		<-ctx.Done()
		job.Stop()
	}()
}

// runTelegramScheduledCommand re-reads the stored command so a schedule that
// was edited or removed since the bot started never runs stale settings, and
// re-checks the chat's access exactly like an incoming message.
func (s *Server) runTelegramScheduledCommand(ctx context.Context, bot *tgbotapi.BotAPI, tgCtx *telegramContext, command string, chatID int64) {
	cmd := s.resolveTelegramCustomCommand(ctx, tgCtx.botID, command)
	if cmd == nil || cmd.Schedule == "" || strings.TrimSpace(cmd.ScheduleChatID) != strconv.FormatInt(chatID, 10) {
		slog.Info("telegram bot: scheduled command no longer configured, skipping", "bot_id", tgCtx.botID, "command", command)
		return
	}
	cfg, err := s.botConfigStore.GetBotConfig(ctx, tgCtx.botID)
	if err != nil || cfg == nil {
		slog.Warn("telegram bot: scheduled command config unavailable", "bot_id", tgCtx.botID, "command", command, "error", err)
		return
	}
	userID := strconv.FormatInt(chatID, 10)
	if allowed, _ := s.checkBotAccess(ctx, tgCtx.botID, userID, cfg.AccessMode, cfg.PendingApproval, cfg.AllowedUsers); !allowed {
		slog.Warn("telegram bot: scheduled command chat is not allowed, skipping", "bot_id", tgCtx.botID, "command", command)
		return
	}

	msg := telegramScheduledMessage(*cmd, chatID, time.Now().UTC())
	slog.Info("telegram bot: running scheduled command", "bot_id", tgCtx.botID, "command", command)

	chatIDStr := userID
	lockValue, _ := tgCtx.messageLocks.LoadOrStore(chatIDStr, &sync.Mutex{})
	chatLock := lockValue.(*sync.Mutex)
	chatLock.Lock()
	defer chatLock.Unlock()

	agentID := tgCtx.defaultAgentID
	if id, ok := tgCtx.chatAgents[chatIDStr]; ok {
		agentID = id
	}
	sessionID := ""
	if agentID != "" {
		sessionID, _, err = s.findOrCreateBotSession(ctx, "telegram", tgCtx.botID, userID, chatIDStr, agentID)
		if err != nil {
			slog.Error("telegram bot: scheduled command session lookup failed", "bot_id", tgCtx.botID, "error", err)
			sendTelegramText(bot, chatID, fmt.Sprintf("Scheduled /%s could not start: the bot's execution identity or agent permissions need attention.", cmd.Command))
			return
		}
	}
	sendTelegramText(bot, chatID, fmt.Sprintf("⏰ Scheduled /%s starting.", cmd.Command))
	s.dispatchTelegramCustomCommand(ctx, bot, msg, tgCtx, chatIDStr, sessionID, agentID, cmd)
}

// ─── Task progress notifications ───

// telegramTaskChannel is where a Telegram-started task reports progress.
type telegramTaskChannel struct {
	bot    *tgbotapi.BotAPI
	chatID int64
	sent   int
	ident  string
}

const telegramNotifyMaxPerTask = 20

// registerTelegramTaskChannel lets the task tree rooted at taskID send
// progress messages to the chat that started it. The registration ends with
// the bot's context; tasks never choose the destination themselves.
func (s *Server) registerTelegramTaskChannel(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64, taskID, identifier string) {
	if taskID == "" || bot == nil || chatID == 0 {
		return
	}
	s.telegramTaskChannels.Store(taskID, &telegramTaskChannel{bot: bot, chatID: chatID, ident: identifier})
	go func() {
		<-ctx.Done()
		s.telegramTaskChannels.Delete(taskID)
	}()
}

func (s *Server) unregisterTelegramTaskChannel(taskID string) {
	if taskID != "" {
		s.telegramTaskChannels.Delete(taskID)
	}
}

// telegramChannelForTask walks to the root of the delegation tree, so
// specialists deep in the pipeline report to the same chat as the head.
func (s *Server) telegramChannelForTask(ctx context.Context, taskID string) (*telegramTaskChannel, string) {
	for range 16 {
		if taskID == "" {
			return nil, ""
		}
		if v, ok := s.telegramTaskChannels.Load(taskID); ok {
			return v.(*telegramTaskChannel), taskID
		}
		if s.taskStore == nil {
			return nil, ""
		}
		task, err := s.taskStore.GetTask(ctx, taskID)
		if err != nil || task == nil {
			return nil, ""
		}
		taskID = task.ParentID
	}
	return nil, ""
}

var telegramNotifyMu sync.Mutex

// execTelegramNotify sends a short progress message to the Telegram chat
// that started the current task. The destination comes from the task, never
// from arguments, so an agent cannot message anybody else.
func (s *Server) execTelegramNotify(ctx context.Context, args map[string]any) (string, error) {
	message, _ := args["message"].(string)
	message = strings.TrimSpace(message)
	if message == "" {
		return "", fmt.Errorf("message is required")
	}
	if len(message) > 1500 {
		message = message[:1500] + "…"
	}
	taskID := taskIDFromContext(ctx)
	if taskID == "" {
		return "", fmt.Errorf("telegram_notify works only inside a task started from Telegram")
	}
	// Scheduled runs keep a run log on the Cron schedules page; the same
	// milestone is recorded there whether or not a Telegram chat is attached.
	logged, _ := s.appendCronRunLogForTask(ctx, taskID, service.CronRunLogMilestone, message)
	channel, _ := s.telegramChannelForTask(ctx, taskID)
	if channel == nil {
		if logged {
			out, _ := json.Marshal(map[string]any{"sent": false, "logged": true, "reason": "no Telegram chat is attached to this scheduled run; the message was recorded in its run log"})
			return string(out), nil
		}
		out, _ := json.Marshal(map[string]any{"sent": false, "reason": "this task was not started from a Telegram chat; nothing was sent"})
		return string(out), nil
	}
	telegramNotifyMu.Lock()
	if channel.sent >= telegramNotifyMaxPerTask {
		telegramNotifyMu.Unlock()
		return "", fmt.Errorf("notification limit reached for this task (%d)", telegramNotifyMaxPerTask)
	}
	channel.sent++
	telegramNotifyMu.Unlock()

	text := message
	if channel.ident != "" {
		text = fmt.Sprintf("[%s] %s", sanitizeUTF8(channel.ident), message)
	}
	sendTelegramText(channel.bot, channel.chatID, text)
	out, _ := json.Marshal(map[string]any{"sent": true, "logged": logged})
	return string(out), nil
}
