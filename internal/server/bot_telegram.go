package server

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/rakunlabs/at/internal/config"
)

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
				agentID := cfg.DefaultAgentID
				if id, ok := cfg.ChatAgents[chatIDStr]; ok {
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
					s.handleTelegramMessage(ctx, bot, msg, agentID)
				}(update.Message)
			}
		}
	}()
}

func (s *Server) handleTelegramMessage(ctx context.Context, bot *tgbotapi.BotAPI, msg *tgbotapi.Message, agentID string) {
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
		case "help":
			helpText := "Available commands:\n" +
				"/reset - Clear conversation history and start fresh\n" +
				"/help - Show this help message"
			reply := tgbotapi.NewMessage(msg.Chat.ID, helpText)
			bot.Send(reply) //nolint:errcheck
			return
		}
	}

	response, err := s.collectAgenticResponse(ctx, sessionID, content)
	typingCancel()

	if err != nil {
		slog.Error("telegram bot: agentic loop failed", "session_id", sessionID, "error", err)
		reply := tgbotapi.NewMessage(msg.Chat.ID, "Sorry, an error occurred processing your message.")
		bot.Send(reply) //nolint:errcheck
		return
	}

	if response == "" {
		response = "(no response)"
	}

	// Telegram message limit is 4096 chars.
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
