package server

import (
	"context"
	"fmt"
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// deliverChatReplyToPlatform forwards a message written into a bot-owned chat
// session (for example by a workflow chat_reply node) to the platform chat
// that session belongs to. Sessions without a platform are left alone. The
// destination comes from the stored session, never from the caller.
func (s *Server) deliverChatReplyToPlatform(ctx context.Context, sessionID, content string) error {
	if s.chatSessionStore == nil {
		return nil
	}
	session, err := s.chatSessionStore.GetChatSession(ctx, sessionID)
	if err != nil || session == nil {
		return nil
	}
	cfg := session.Config
	if cfg.Platform != "telegram" || cfg.BotConfigID == "" {
		return nil
	}
	chatID, err := strconv.ParseInt(cfg.PlatformChannelID, 10, 64)
	if err != nil || chatID == 0 {
		return fmt.Errorf("telegram session %s has no valid chat id", sessionID)
	}
	bot := s.runningTelegramBot(cfg.BotConfigID)
	if bot == nil {
		return fmt.Errorf("message saved, but telegram bot %s is not running; nothing was sent", cfg.BotConfigID)
	}
	sendTelegramText(bot, chatID, content)
	return nil
}

func (s *Server) runningTelegramBot(botID string) *tgbotapi.BotAPI {
	rb := s.getBotRunningInfo(botID)
	if rb == nil {
		return nil
	}
	return rb.telegram.Load()
}
