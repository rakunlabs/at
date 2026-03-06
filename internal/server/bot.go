package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

// findOrCreateBotSession looks up an existing chat session by platform identifiers,
// or creates a new one if none exists.
func (s *Server) findOrCreateBotSession(ctx context.Context, platform, userID, channelID, agentID string) (string, error) {
	if s.chatSessionStore == nil {
		return "", fmt.Errorf("chat session store not configured")
	}

	session, err := s.chatSessionStore.GetChatSessionByPlatform(ctx, platform, userID, channelID)
	if err != nil {
		return "", fmt.Errorf("lookup platform session: %w", err)
	}
	if session != nil {
		return session.ID, nil
	}

	// Create new session.
	name := fmt.Sprintf("%s-%s", platform, channelID)
	if channelID == "" {
		name = fmt.Sprintf("%s-%s", platform, userID)
	}

	newSession, err := s.chatSessionStore.CreateChatSession(ctx, service.ChatSession{
		AgentID: agentID,
		Name:    name,
		Config: service.ChatSessionConfig{
			Platform:          platform,
			PlatformUserID:    userID,
			PlatformChannelID: channelID,
		},
		CreatedBy: platform + "-bot",
		UpdatedBy: platform + "-bot",
	})
	if err != nil {
		return "", fmt.Errorf("create platform session: %w", err)
	}

	slog.Info("created bot chat session", "platform", platform, "session_id", newSession.ID, "agent_id", agentID)
	return newSession.ID, nil
}

// collectAgenticResponse runs the agentic loop and collects all text content into a single string.
func (s *Server) collectAgenticResponse(ctx context.Context, sessionID, content string) (string, error) {
	var builder strings.Builder

	err := s.RunAgenticLoop(ctx, sessionID, content, func(ev AgenticEvent) {
		switch ev.Type {
		case "content":
			builder.WriteString(ev.Content)
		case "error":
			if builder.Len() > 0 {
				builder.WriteString("\n")
			}
			builder.WriteString("[Error: " + ev.Error + "]")
		}
	})
	if err != nil {
		return "", err
	}

	return builder.String(), nil
}

// startBotsFromDB loads enabled bot configs from the database and starts them.
func (s *Server) startBotsFromDB(ctx context.Context) {
	if s.botConfigStore == nil {
		return
	}

	result, err := s.botConfigStore.ListBotConfigs(ctx, nil)
	if err != nil {
		slog.Error("failed to load bot configs from DB", "error", err)
		return
	}

	for i := range result.Data {
		bot := &result.Data[i]
		if !bot.Enabled || bot.Token == "" {
			continue
		}
		s.startBotFromConfig(ctx, bot)
	}
}

// startBotFromConfig starts a single bot based on its DB configuration.
func (s *Server) startBotFromConfig(ctx context.Context, bot *service.BotConfig) {
	switch bot.Platform {
	case "discord":
		s.startDiscordBot(ctx, &config.DiscordBotConfig{
			Token:          bot.Token,
			DefaultAgentID: bot.DefaultAgentID,
			ChannelAgents:  bot.ChannelAgents,
		})
	case "telegram":
		s.startTelegramBot(ctx, &config.TelegramBotConfig{
			Token:          bot.Token,
			DefaultAgentID: bot.DefaultAgentID,
			ChatAgents:     bot.ChannelAgents,
		})
	default:
		slog.Warn("unknown bot platform", "platform", bot.Platform, "id", bot.ID)
	}
}
