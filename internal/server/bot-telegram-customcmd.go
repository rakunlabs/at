package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/rakunlabs/at/internal/service"
)

// resolveTelegramCustomCommand returns the matching custom command from the
// bot's stored config, or nil if the command is not defined or the bot has no
// custom commands configured. Lookup is case-insensitive. The leading "/" must
// already be stripped (matches msg.Command() output).
func (s *Server) resolveTelegramCustomCommand(ctx context.Context, botID, command string) *service.BotCustomCommand {
	if botID == "" || command == "" || s.botConfigStore == nil {
		return nil
	}
	cfg, err := s.botConfigStore.GetBotConfig(ctx, botID)
	if err != nil || cfg == nil || len(cfg.CustomCommands) == 0 {
		return nil
	}
	want := strings.ToLower(strings.TrimPrefix(command, "/"))
	for i := range cfg.CustomCommands {
		c := &cfg.CustomCommands[i]
		if strings.ToLower(strings.TrimPrefix(c.Command, "/")) == want {
			return c
		}
	}
	return nil
}

// formatTelegramCustomCommandsHelp renders the bot's custom commands as a
// help-text block. Returns an empty string if the bot has none configured.
func (s *Server) formatTelegramCustomCommandsHelp(ctx context.Context, botID string) string {
	if botID == "" || s.botConfigStore == nil {
		return ""
	}
	cfg, err := s.botConfigStore.GetBotConfig(ctx, botID)
	if err != nil || cfg == nil || len(cfg.CustomCommands) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, c := range cfg.CustomCommands {
		name := strings.TrimPrefix(c.Command, "/")
		if name == "" {
			continue
		}
		desc := c.Description
		if desc == "" {
			desc = "(custom command)"
		}
		sb.WriteString(fmt.Sprintf("/%s - %s\n", name, desc))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// handleTelegramCustomCommand looks up the slash command on the bot's stored
// custom-commands list and, if a match is found, creates a background task
// using the configured routing (org or specific agent) with a brief built
// from the template. Returns true if the command was handled.
func (s *Server) handleTelegramCustomCommand(
	ctx context.Context,
	bot *tgbotapi.BotAPI,
	msg *tgbotapi.Message,
	tgCtx *telegramContext,
	chatIDStr string,
	defaultAgentID string,
) bool {
	cmd := s.resolveTelegramCustomCommand(ctx, tgCtx.botID, msg.Command())
	if cmd == nil {
		return false
	}

	args := strings.TrimSpace(msg.CommandArguments())

	// Build the task description from the brief template. The literal
	// token "{args}" is replaced with whatever the user typed after the
	// slash command. If the brief is empty we just use the args verbatim
	// (or a friendly placeholder when args are empty too).
	brief := cmd.Brief
	if brief == "" {
		brief = "{args}"
	}
	brief = strings.ReplaceAll(brief, "{args}", args)
	brief = strings.TrimSpace(brief)
	if brief == "" {
		brief = fmt.Sprintf("Run /%s with no extra arguments.", cmd.Command)
	}

	// Title: prefix + first line of args (or command name if no args).
	titleSeed := args
	if titleSeed == "" {
		titleSeed = cmd.Description
	}
	if titleSeed == "" {
		titleSeed = "/" + strings.TrimPrefix(cmd.Command, "/")
	}
	if idx := strings.IndexAny(titleSeed, "\n\r"); idx > 0 {
		titleSeed = titleSeed[:idx]
	}
	if len(titleSeed) > 100 {
		titleSeed = titleSeed[:100] + "..."
	}
	title := titleSeed
	if cmd.TitlePrefix != "" {
		title = strings.TrimSpace(cmd.TitlePrefix) + " " + title
	}

	// Determine routing. If an org is configured, use org_task_intake (head
	// agent receives the brief and delegates). Otherwise fall back to the
	// configured agent, or the bot's current default agent.
	chatID := msg.Chat.ID
	onDone := func(ident, status, result string) {
		switch status {
		case "done", "completed":
			sendTelegramText(bot, chatID, fmt.Sprintf("Task %s completed.\nUse /result %s to get the output.", sanitizeUTF8(ident), sanitizeUTF8(ident)))
		case "blocked":
			// Iteration-limit pause is recoverable via /resume — surface that
			// to the user instead of presenting the task as a hard failure.
			if strings.HasPrefix(result, "[ITERATION_LIMIT]") {
				sendTelegramText(bot, chatID, fmt.Sprintf(
					"Task %s paused at the iteration limit. Partial progress saved.\n/resume %s to continue, or /result %s to see partial output.",
					sanitizeUTF8(ident), sanitizeUTF8(ident), sanitizeUTF8(ident)))
			} else {
				errMsg := sanitizeUTF8(result)
				if len(errMsg) > 500 {
					errMsg = errMsg[:500] + "..."
				}
				sendTelegramText(bot, chatID, fmt.Sprintf("Task %s blocked.\n\n%s\n\n/resume %s to retry.",
					sanitizeUTF8(ident), errMsg, sanitizeUTF8(ident)))
			}
		case "failed", "cancelled":
			errMsg := sanitizeUTF8(result)
			if len(errMsg) > 500 {
				errMsg = errMsg[:500] + "..."
			}
			sendTelegramText(bot, chatID, fmt.Sprintf("Task %s failed.\n\n%s", sanitizeUTF8(ident), errMsg))
		}
	}

	var (
		taskID, identifier string
		err                error
	)

	switch {
	case cmd.OrganizationID != "":
		taskID, identifier, err = s.createBotOrgTask(ctx, cmd.OrganizationID, title, brief, cmd.MaxIterations, onDone)
	case cmd.AgentID != "":
		taskID, identifier, err = s.createBotTaskWithOptions(ctx, cmd.AgentID, brief, BotTaskOptions{MaxIterations: cmd.MaxIterations}, onDone)
	default:
		taskID, identifier, err = s.createBotTaskWithOptions(ctx, defaultAgentID, brief, BotTaskOptions{MaxIterations: cmd.MaxIterations}, onDone)
	}

	if err != nil {
		slog.Error("telegram bot: custom command failed", "command", cmd.Command, "error", err)
		sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf("Failed to run /%s: %v", strings.TrimPrefix(cmd.Command, "/"), err))
		return true
	}

	slog.Info("telegram bot: custom command dispatched",
		"command", cmd.Command, "task_id", taskID, "identifier", identifier,
		"org", cmd.OrganizationID, "agent", cmd.AgentID)

	tgCtx.activeTask.Store(chatIDStr, identifier)

	ack := fmt.Sprintf("Task %s created and running in background.\nSet as active task.\n\nCommand: /%s\nTitle: %s\n\nI'll notify you when it's done.\n/status to check",
		sanitizeUTF8(identifier),
		sanitizeUTF8(strings.TrimPrefix(cmd.Command, "/")),
		sanitizeUTF8(title))
	sendTelegramText(bot, msg.Chat.ID, ack)
	return true
}

// createBotOrgTask submits a task directly to an organization's head agent. It
// mirrors createBotTaskWithOptions but is keyed on org id rather than agent
// membership. Used by custom commands that target an org.
func (s *Server) createBotOrgTask(
	ctx context.Context,
	orgID, title, description string,
	maxIterations int,
	onDone TaskDoneCallback,
) (string, string, error) {
	if s.organizationStore == nil || s.taskStore == nil {
		return "", "", fmt.Errorf("task/org stores not configured")
	}

	org, err := s.organizationStore.GetOrganization(ctx, orgID)
	if err != nil {
		return "", "", fmt.Errorf("get organization %s: %w", orgID, err)
	}
	if org == nil {
		return "", "", fmt.Errorf("organization %s not found", orgID)
	}
	if org.HeadAgentID == "" {
		return "", "", fmt.Errorf("organization %s has no head agent", orgID)
	}

	counter, err := s.organizationStore.IncrementIssueCounter(ctx, org.ID)
	if err != nil {
		return "", "", fmt.Errorf("generate identifier: %w", err)
	}
	prefix := org.IssuePrefix
	if prefix == "" {
		prefix = org.ID
		if len(prefix) > 4 {
			prefix = prefix[:4]
		}
	}
	identifier := fmt.Sprintf("%s-%d", prefix, counter)

	task := service.Task{
		OrganizationID:  org.ID,
		AssignedAgentID: org.HeadAgentID,
		Title:           title,
		Description:     description,
		Status:          service.TaskStatusOpen,
		Identifier:      identifier,
		RequestDepth:    0,
		MaxIterations:   maxIterations,
		CreatedBy:       "telegram-bot",
	}

	record, err := s.taskStore.CreateTask(ctx, task)
	if err != nil {
		return "", "", fmt.Errorf("create task: %w", err)
	}

	go func() {
		delegCtx, cleanup := s.registerDelegation(context.Background(), record.ID, org.HeadAgentID, org.ID)
		defer cleanup()
		if err := s.runOrgDelegation(delegCtx, org, record, org.HeadAgentID, 0); err != nil {
			slog.Error("bot-task: org-routed delegation failed",
				"org_id", org.ID,
				"task_id", record.ID,
				"error", err,
			)
			errResult := fmt.Sprintf("delegation failed: %v", err)
			if s.taskStore != nil {
				_, _ = s.taskStore.UpdateTask(context.Background(), record.ID, service.Task{
					Status: service.TaskStatusCancelled,
					Result: errResult,
				})
			}
			if onDone != nil {
				onDone(identifier, "failed", errResult)
			}
			return
		}
		if s.taskStore != nil {
			if updated, err := s.taskStore.GetTask(delegCtx, record.ID); err == nil && updated != nil {
				if onDone != nil {
					onDone(identifier, updated.Status, updated.Result)
				}
				return
			}
		}
		if onDone != nil {
			onDone(identifier, "done", "")
		}
	}()

	return record.ID, identifier, nil
}
