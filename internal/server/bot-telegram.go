package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)



// sendTelegramText sends a UTF-8 sanitized text message to a Telegram chat.
func sendTelegramText(bot *tgbotapi.BotAPI, chatID int64, text string) {
	text = sanitizeUTF8(text)
	if text == "" {
		text = "(empty)"
	}
	// Telegram limit is 4096 chars
	for len(text) > 0 {
		chunk := text
		if len(chunk) > 4096 {
			chunk = text[:4096]
			text = text[4096:]
		} else {
			text = ""
		}
		reply := tgbotapi.NewMessage(chatID, chunk)
		bot.Send(reply) //nolint:errcheck
	}
}

// sanitizeUTF8 ensures a string is valid UTF-8 by replacing invalid bytes.
func sanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	// Replace invalid UTF-8 sequences
	v := make([]rune, 0, len(s))
	for i, r := range s {
		if r == utf8.RuneError {
			_, size := utf8.DecodeRuneInString(s[i:])
			if size == 1 {
				continue // skip invalid byte
			}
		}
		v = append(v, r)
	}
	return string(v)
}

// extractMediaFiles scans task result text for video and image file paths.
func extractMediaFiles(result string) (videos []string, images []string) {
	videoExts := map[string]bool{".mp4": true, ".mov": true, ".webm": true, ".avi": true}
	imageExts := map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}

	// Try parsing as JSON first — look for "video_file", "file", "image" keys
	var data map[string]any
	if json.Unmarshal([]byte(result), &data) == nil {
		for _, key := range []string{"video_file", "file", "output", "video"} {
			if v, ok := data[key].(string); ok && v != "" {
				ext := strings.ToLower(filepath.Ext(v))
				if videoExts[ext] {
					videos = append(videos, v)
				} else if imageExts[ext] {
					images = append(images, v)
				}
			}
		}
		for _, key := range []string{"image", "image_file", "thumbnail"} {
			if v, ok := data[key].(string); ok && v != "" {
				ext := strings.ToLower(filepath.Ext(v))
				if imageExts[ext] {
					images = append(images, v)
				}
			}
		}
	}

	// Also scan for file paths using regex (catches paths in nested JSON or plain text)
	pathPattern := regexp.MustCompile(`(/[a-zA-Z0-9._\-/]+\.(mp4|mov|webm|avi|png|jpg|jpeg|gif|webp))`)
	matches := pathPattern.FindAllStringSubmatch(result, -1)
	seen := make(map[string]bool)
	for _, v := range videos {
		seen[v] = true
	}
	for _, v := range images {
		seen[v] = true
	}
	for _, m := range matches {
		path := m[1]
		if seen[path] {
			continue
		}
		seen[path] = true
		ext := strings.ToLower(filepath.Ext(path))
		if videoExts[ext] {
			videos = append(videos, path)
		} else if imageExts[ext] {
			images = append(images, path)
		}
	}

	return
}

// telegramContext holds per-bot context passed to message handlers.
type telegramContext struct {
	botID           string
	defaultAgentID  string
	chatAgents      map[string]string
	allowedAgentIDs []string
	activeTask      sync.Map // chatID (string) -> task identifier (string)
}

// startTelegramBot starts a Telegram bot that routes messages to the agentic loop.
func (s *Server) startTelegramBot(ctx context.Context, botID string, cfg *config.TelegramBotConfig) {
	bot, err := tgbotapi.NewBotAPI(cfg.Token)
	if err != nil {
		slog.Error("telegram bot: failed to create bot", "error", err)
		return
	}

	slog.Info("telegram bot started", "user", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30

	updates := bot.GetUpdatesChan(u)

	tgCtx := &telegramContext{
		botID:           botID,
		defaultAgentID:  cfg.DefaultAgentID,
		chatAgents:      cfg.ChatAgents,
		allowedAgentIDs: cfg.AllowedAgentIDs,
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				slog.Info("telegram bot: shutting down")
				bot.StopReceivingUpdates()
				return
			case update := <-updates:
				if update.Message == nil {
					continue
				}

				// Determine agent ID for this chat.
				chatIDStr := fmt.Sprintf("%d", update.Message.Chat.ID)
				agentID := tgCtx.defaultAgentID
				if id, ok := tgCtx.chatAgents[chatIDStr]; ok {
					agentID = id
				}
				if agentID == "" {
					continue
				}

				// Access control check.
				userIDStr := fmt.Sprintf("%d", update.Message.From.ID)
				allowed, wasPending := s.checkBotAccess(ctx, botID, userIDStr, cfg.AccessMode, cfg.PendingApproval, cfg.AllowedUsers)
				if !allowed {
					if wasPending {
						reply := tgbotapi.NewMessage(update.Message.Chat.ID, "Your access is pending approval.")
						bot.Send(reply) //nolint:errcheck
					}
					continue
				}

				go func(msg *tgbotapi.Message) {
					s.handleTelegramMessage(ctx, bot, msg, agentID, tgCtx)
				}(update.Message)
			}
		}
	}()
}

func (s *Server) handleTelegramMessage(ctx context.Context, bot *tgbotapi.BotAPI, msg *tgbotapi.Message, agentID string, tgCtx *telegramContext) {
	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	userIDStr := fmt.Sprintf("%d", msg.From.ID)

	sessionID, err := s.findOrCreateBotSession(ctx, "telegram", userIDStr, chatIDStr, agentID)
	if err != nil {
		slog.Error("telegram bot: session lookup failed", "error", err)
		return
	}

	// Send typing indicator periodically.
	typingCtx, typingCancel := context.WithCancel(ctx)
	defer typingCancel()
	go func() {
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		action := tgbotapi.NewChatAction(msg.Chat.ID, tgbotapi.ChatTyping)
		bot.Send(action) //nolint:errcheck
		for {
			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
				bot.Send(action) //nolint:errcheck
			}
		}
	}()

	content := msg.Text
	if content == "" {
		content = msg.Caption
	}
	if content == "" {
		return
	}

	// Handle bot commands.
	if msg.IsCommand() {
		slog.Info("telegram bot: command received", "command", msg.Command(), "text", content[:min(len(content), 50)])
		switch msg.Command() {
		case "reset":
			if err := s.chatSessionStore.DeleteChatMessages(ctx, sessionID); err != nil {
				slog.Error("telegram bot: reset failed", "session_id", sessionID, "error", err)
				reply := tgbotapi.NewMessage(msg.Chat.ID, "Failed to reset session.")
				bot.Send(reply) //nolint:errcheck
				return
			}
			reply := tgbotapi.NewMessage(msg.Chat.ID, "Session cleared. Starting fresh!")
			bot.Send(reply) //nolint:errcheck
			return
		case "new":
			// /new <topic> — create an org task and run it in the background
			topic := strings.TrimSpace(msg.CommandArguments())
			if topic == "" {
				reply := tgbotapi.NewMessage(msg.Chat.ID, "Usage: /new <topic or task>\nExample: /new top 5 deadliest animals")
				bot.Send(reply) //nolint:errcheck
				return
			}

			// Callback to notify user when task finishes
			chatID := msg.Chat.ID
			onDone := func(ident, status, result string) {
				switch status {
				case "done", "completed":
					sendTelegramText(bot, chatID, fmt.Sprintf("Task %s completed!\nUse /result %s to get the output.", sanitizeUTF8(ident), sanitizeUTF8(ident)))
				case "failed", "cancelled", "blocked":
					errMsg := sanitizeUTF8(result)
					if len(errMsg) > 500 {
						errMsg = errMsg[:500] + "..."
					}
					sendTelegramText(bot, chatID, fmt.Sprintf("Task %s failed.\n\n%s\n\nTry again:\n/new %s", sanitizeUTF8(ident), errMsg, sanitizeUTF8(topic)))
				}
			}

			taskID, identifier, createErr := s.createBotTask(ctx, agentID, topic, onDone)
			if createErr != nil {
				slog.Error("telegram bot: create task failed", "error", createErr)
				reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("Failed to create task: %v", createErr))
				bot.Send(reply) //nolint:errcheck
				return
			}

			// Auto-pick this task as active
			tgCtx.activeTask.Store(chatIDStr, identifier)

			reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("✅ Task %s created and running in background.\n📌 Set as active task.\n\nTopic: %s\n\nI'll notify you when it's done. You can also check with /status", identifier, topic))
			bot.Send(reply) //nolint:errcheck
			_ = taskID
			return
		case "status":
			// /status [identifier] — check task status. If no ID given, show the latest task.
			identifier := strings.TrimSpace(msg.CommandArguments())

			var task *service.Task
			var statusErr error

			if identifier == "" {
				// Use active task if set, otherwise latest
				if activeID, ok := tgCtx.activeTask.Load(chatIDStr); ok {
					identifier = activeID.(string)
				}
			}

			if identifier == "" {
				task, statusErr = s.findLatestTask(ctx, agentID)
				if task == nil && statusErr == nil {
					reply := tgbotapi.NewMessage(msg.Chat.ID, "No tasks found. Create one with /new <topic>")
					bot.Send(reply) //nolint:errcheck
					return
				}
			} else {
				task, statusErr = s.findTaskByIdentifier(ctx, agentID, identifier)
			}
			if statusErr != nil || task == nil {
				reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("Task %s not found.", identifier))
				bot.Send(reply) //nolint:errcheck
				return
			}

			// Send status message
			var statusEmoji string
			switch task.Status {
			case "done", "completed":
				statusEmoji = "✅"
			case "in_progress":
				statusEmoji = "⏳"
			case "cancelled", "blocked":
				statusEmoji = "❌"
			default:
				statusEmoji = "📋"
			}

			taskRef := sanitizeUTF8(task.Identifier)
			if taskRef == "" {
				taskRef = task.ID
			}
			statusMsg := fmt.Sprintf("%s %s\nStatus: %s", statusEmoji, taskRef, task.Status)
			if task.Title != "" {
				statusMsg += fmt.Sprintf("\nTitle: %s", sanitizeUTF8(task.Title))
			}
			if task.Status == "done" || task.Status == "completed" {
				statusMsg += fmt.Sprintf("\n\nUse /result %s to get the output", taskRef)
			}
			sendTelegramText(bot, msg.Chat.ID, statusMsg)
			return
		case "result":
			// /result [identifier] — get the full result + send video/images
			identifier := strings.TrimSpace(msg.CommandArguments())

			var task *service.Task
			var resultErr error

			if identifier == "" {
				if activeID, ok := tgCtx.activeTask.Load(chatIDStr); ok {
					identifier = activeID.(string)
				}
			}
			if identifier == "" {
				task, resultErr = s.findLatestTask(ctx, agentID)
			} else {
				task, resultErr = s.findTaskByIdentifier(ctx, agentID, identifier)
			}
			if resultErr != nil || task == nil {
				sendTelegramText(bot, msg.Chat.ID, "Task not found.")
				return
			}

			if task.Result == "" {
				sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf("Task %s has no result yet. Status: %s", sanitizeUTF8(task.Identifier), task.Status))
				return
			}

			// Try to send video/image files
			videoFiles, imageFiles := extractMediaFiles(task.Result)

			for _, vf := range videoFiles {
				if _, err := os.Stat(vf); err == nil {
					// Send as document to preserve original quality and aspect ratio
					doc := tgbotapi.NewDocument(msg.Chat.ID, tgbotapi.FilePath(vf))
					doc.Caption = sanitizeUTF8(task.Identifier)
					if _, err := bot.Send(doc); err != nil {
						slog.Warn("telegram bot: send video failed", "file", vf, "error", err)
						sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf("Video at: %s (too large for Telegram)", vf))
					}
				}
			}

			for _, imgf := range imageFiles {
				if _, err := os.Stat(imgf); err == nil {
					photo := tgbotapi.NewPhoto(msg.Chat.ID, tgbotapi.FilePath(imgf))
					photo.Caption = sanitizeUTF8(task.Identifier)
					bot.Send(photo) //nolint:errcheck
				}
			}

			// Send text result if no media or always
			if len(videoFiles) == 0 && len(imageFiles) == 0 {
				sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf("Result:\n%s", sanitizeUTF8(task.Result)))
			}
			return
		case "tasks":
			// /tasks — list recent tasks with status
			orgIDs := s.agentOrgIDs(ctx, agentID)
			slog.Info("telegram bot: /tasks", "agent_id", agentID, "org_ids", orgIDs)

			var tasks []service.Task
			var tasksErr error
			func() {
				defer func() {
					if r := recover(); r != nil {
						slog.Error("telegram bot: /tasks panic", "recover", r)
						tasksErr = fmt.Errorf("internal error: %v", r)
					}
				}()
				tasks, tasksErr = s.listBotTasks(ctx, agentID)
			}()
			if tasksErr != nil {
				slog.Error("telegram bot: list tasks failed", "agent_id", agentID, "error", tasksErr)
				reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("⚠️ Failed to list tasks: %v\n\nAgent: %s\nOrgs found: %d", tasksErr, agentID, len(orgIDs)))
				bot.Send(reply) //nolint:errcheck
				return
			}
			if len(tasks) == 0 {
				reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("No tasks found.\n\nAgent: %s\nOrgs: %d\n\nCreate one with /new <topic>", agentID, len(orgIDs)))
				bot.Send(reply) //nolint:errcheck
				return
			}

			slog.Info("telegram bot: tasks found, building message", "count", len(tasks))
			var sb strings.Builder
			sb.WriteString("Recent tasks:\n\n")
			for _, t := range tasks {
				emoji := "[ ]"
				switch t.Status {
				case "done", "completed":
					emoji = "[done]"
				case "in_progress":
					emoji = "[running]"
				case "cancelled", "blocked":
					emoji = "[failed]"
				case "open", "todo":
					emoji = "[open]"
				}
				status := emoji
				title := sanitizeUTF8(t.Title)
				if len(title) > 40 {
					title = title[:40] + "..."
				}
				taskRef := sanitizeUTF8(t.Identifier)
				if taskRef == "" {
					taskRef = sanitizeUTF8(t.ID)
				}
				sb.WriteString(fmt.Sprintf("%s %s - %s\n/status %s\n\n", status, taskRef, title, taskRef))
			}
			sendTelegramText(bot, msg.Chat.ID, sb.String())
			return
		case "current":
			// /current — show the active task
			if activeID, ok := tgCtx.activeTask.Load(chatIDStr); ok {
				taskRef := activeID.(string)
				task, _ := s.findTaskByIdentifier(ctx, agentID, taskRef)
				if task != nil {
					title := sanitizeUTF8(task.Title)
					if len(title) > 50 {
						title = title[:50] + "..."
					}
					sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf("Active task: %s\nStatus: %s\nTitle: %s", sanitizeUTF8(taskRef), task.Status, title))
				} else {
					sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf("Active task: %s (not found in DB)", sanitizeUTF8(taskRef)))
				}
			} else {
				sendTelegramText(bot, msg.Chat.ID, "No active task. Use /pick <id> to select one.")
			}
			return
		case "pick":
			// /pick <identifier> — set the active task for this chat
			target := strings.TrimSpace(msg.CommandArguments())
			if target == "" {
				// No argument — clear active task
				tgCtx.activeTask.Delete(chatIDStr)
				reply := tgbotapi.NewMessage(msg.Chat.ID, "Active task cleared. Normal chat mode.")
				bot.Send(reply) //nolint:errcheck
				return
			}

			task, pickErr := s.findTaskByIdentifier(ctx, agentID, target)
			if pickErr != nil || task == nil {
				reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("Task %s not found. Use /tasks to see available tasks.", target))
				bot.Send(reply) //nolint:errcheck
				return
			}

			taskRef := task.Identifier
			if taskRef == "" {
				taskRef = task.ID[:12]
			}
			tgCtx.activeTask.Store(chatIDStr, taskRef)

			// Clear the chat session so context is fresh for this task
			if err := s.chatSessionStore.DeleteChatMessages(ctx, sessionID); err != nil {
				slog.Warn("telegram bot: pick clear session failed", "error", err)
			}

			title := task.Title
			if len(title) > 50 {
				title = title[:50] + "..."
			}
			reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("📌 Active task: %s — %s\nStatus: %s\n\nYou can now chat about this task. Messages will include task context.\nUse /pick (no args) to deselect.", taskRef, title, task.Status))
			bot.Send(reply) //nolint:errcheck
			return
		case "login":
			provider := msg.CommandArguments()
			if provider == "" {
				provider = "google"
			}
			loginURL := s.buildOAuthLoginURL(ctx, provider, "telegram", userIDStr)
			if loginURL == "" {
				reply := tgbotapi.NewMessage(msg.Chat.ID, "OAuth login is not available. Make sure external_url is configured and the provider's client_id variable is set.")
				bot.Send(reply) //nolint:errcheck
				return
			}
			reply := tgbotapi.NewMessage(msg.Chat.ID, "Click the link below to connect your "+provider+" account:\n"+loginURL)
			bot.Send(reply) //nolint:errcheck
			return
		case "agents":
			agents := s.listAllowedAgents(ctx, tgCtx.botID, tgCtx.allowedAgentIDs)
			if len(agents) == 0 {
				reply := tgbotapi.NewMessage(msg.Chat.ID, "Agent switching is not enabled for this bot.")
				bot.Send(reply) //nolint:errcheck
				return
			}
			var sb strings.Builder
			sb.WriteString("Available agents:\n")
			for _, a := range agents {
				desc := a.Config.Description
				if desc != "" {
					sb.WriteString(fmt.Sprintf("• %s - %s\n", a.Name, desc))
				} else {
					sb.WriteString(fmt.Sprintf("• %s\n", a.Name))
				}
			}
			sb.WriteString("\nUsage: /switch <agent name>")
			reply := tgbotapi.NewMessage(msg.Chat.ID, sb.String())
			bot.Send(reply) //nolint:errcheck
			return
		case "switch":
			target := strings.TrimSpace(msg.CommandArguments())
			if target == "" {
				reply := tgbotapi.NewMessage(msg.Chat.ID, "Usage: /switch <agent name>\nUse /agents to see available agents.")
				bot.Send(reply) //nolint:errcheck
				return
			}
			name, switchErr := s.switchBotAgent(ctx, tgCtx.botID, sessionID, target, tgCtx.allowedAgentIDs)
			if switchErr != nil {
				reply := tgbotapi.NewMessage(msg.Chat.ID, switchErr.Error())
				bot.Send(reply) //nolint:errcheck
				return
			}
			reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("Switched to %s. Session cleared.", name))
			bot.Send(reply) //nolint:errcheck
			return
		case "help":
			helpText := "Available commands:\n" +
				"/new <topic> - Create a background task\n" +
				"/tasks - List recent tasks\n" +
				"/status [id] - Check task status\n" +
				"/result [id] - Get task output + video\n" +
				"/pick <id> - Select task to chat about\n" +
				"/current - Show active task\n" +
				"/reset - Clear conversation history\n" +
				"/login - Connect your Google account (usage: /login or /login google)\n" +
				"/agents - List available agents you can switch to\n" +
				"/switch - Switch to a different agent (usage: /switch <agent name>)\n" +
				"/help - Show this help message"
			reply := tgbotapi.NewMessage(msg.Chat.ID, helpText)
			bot.Send(reply) //nolint:errcheck
			return
		default:
			slog.Warn("telegram bot: unknown command, passing to agent", "command", msg.Command())
		}
	}

	// If there's an active task, prepend context so the agent knows which task
	if activeID, ok := tgCtx.activeTask.Load(chatIDStr); ok {
		taskRef := activeID.(string)
		task, _ := s.findTaskByIdentifier(ctx, agentID, taskRef)
		if task != nil {
			taskContext := fmt.Sprintf("[Active task: %s | Status: %s | Title: %s]", taskRef, task.Status, task.Title)
			if task.Result != "" {
				resultPreview := task.Result
				if len(resultPreview) > 500 {
					resultPreview = resultPreview[:500] + "..."
				}
				taskContext += fmt.Sprintf("\n[Task result: %s]", resultPreview)
			}
			content = taskContext + "\n\n" + content
		}
	}

	response, err := s.collectAgenticResponse(ctx, sessionID, content)
	typingCancel()

	if err != nil {
		slog.Error("telegram bot: agentic loop failed", "session_id", sessionID, "error", err)
		sendTelegramText(bot, msg.Chat.ID, "Sorry, an error occurred processing your message.")
		return
	}

	if response == "" {
		response = "(no response)"
	}

	// If there's an active task, always update its result with the latest response
	if activeID, ok := tgCtx.activeTask.Load(chatIDStr); ok && s.taskStore != nil {
		taskRef := activeID.(string)
		task, _ := s.findTaskByIdentifier(ctx, agentID, taskRef)
		if task != nil {
			_, _ = s.taskStore.UpdateTask(ctx, task.ID, service.Task{
				Result: response,
			})
			slog.Info("telegram bot: task result updated", "task", taskRef)
		}
	}

	// Send response to Telegram
	for len(response) > 0 {
		chunk := response
		if len(chunk) > 4096 {
			cutAt := 4096
			if idx := lastIndexBefore(response, '\n', 4096); idx > 0 {
				cutAt = idx + 1
			}
			chunk = response[:cutAt]
			response = response[cutAt:]
		} else {
			response = ""
		}

		reply := tgbotapi.NewMessage(msg.Chat.ID, chunk)
		if _, err := bot.Send(reply); err != nil {
			slog.Error("telegram bot: failed to send message", "error", err)
			return
		}
	}
}
