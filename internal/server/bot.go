package server

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
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
		s.startDiscordBot(ctx, bot.ID, &config.DiscordBotConfig{
			Token:           bot.Token,
			DefaultAgentID:  bot.DefaultAgentID,
			ChannelAgents:   bot.ChannelAgents,
			AllowedAgentIDs: bot.AllowedAgentIDs,
			AccessMode:      bot.AccessMode,
			PendingApproval: bot.PendingApproval,
			AllowedUsers:    bot.AllowedUsers,
		})
	case "telegram":
		s.startTelegramBot(ctx, bot.ID, &config.TelegramBotConfig{
			Token:           bot.Token,
			DefaultAgentID:  bot.DefaultAgentID,
			ChatAgents:      bot.ChannelAgents,
			AllowedAgentIDs: bot.AllowedAgentIDs,
			AccessMode:      bot.AccessMode,
			PendingApproval: bot.PendingApproval,
			AllowedUsers:    bot.AllowedUsers,
		})
	default:
		slog.Warn("unknown bot platform", "platform", bot.Platform, "id", bot.ID)
	}
}

// listAllowedAgents returns the agents a user may switch to.
// It resolves agent names from the store for display purposes.
// The returned slice contains (id, name) pairs.
func (s *Server) listAllowedAgents(ctx context.Context, botID string, allowedAgentIDs []string) []service.Agent {
	// For DB bots, fetch current config for dynamic updates.
	if botID != "" && s.botConfigStore != nil {
		dbCfg, err := s.botConfigStore.GetBotConfig(ctx, botID)
		if err == nil && dbCfg != nil {
			allowedAgentIDs = dbCfg.AllowedAgentIDs
		}
	}

	if len(allowedAgentIDs) == 0 || s.agentStore == nil {
		return nil
	}

	var agents []service.Agent
	for _, id := range allowedAgentIDs {
		agent, err := s.agentStore.GetAgent(ctx, id)
		if err != nil || agent == nil {
			continue
		}
		agents = append(agents, *agent)
	}

	return agents
}

// switchBotAgent switches the session to a different agent and clears conversation history.
// It returns the agent name on success, or an error message on failure.
func (s *Server) switchBotAgent(ctx context.Context, botID, sessionID, targetAgent string, allowedAgentIDs []string) (string, error) {
	// For DB bots, fetch current config for dynamic updates.
	if botID != "" && s.botConfigStore != nil {
		dbCfg, err := s.botConfigStore.GetBotConfig(ctx, botID)
		if err == nil && dbCfg != nil {
			allowedAgentIDs = dbCfg.AllowedAgentIDs
		}
	}

	if len(allowedAgentIDs) == 0 {
		return "", fmt.Errorf("agent switching is not enabled for this bot")
	}

	// Find the agent by name or ID.
	if s.agentStore == nil {
		return "", fmt.Errorf("agent store not configured")
	}

	var matchedAgent *service.Agent
	for _, id := range allowedAgentIDs {
		agent, err := s.agentStore.GetAgent(ctx, id)
		if err != nil || agent == nil {
			continue
		}
		if strings.EqualFold(agent.Name, targetAgent) || agent.ID == targetAgent {
			matchedAgent = agent
			break
		}
	}

	if matchedAgent == nil {
		return "", fmt.Errorf("agent %q not found in the allowed list", targetAgent)
	}

	// Load session and update agent ID.
	session, err := s.chatSessionStore.GetChatSession(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("load session: %w", err)
	}
	if session == nil {
		return "", fmt.Errorf("session not found")
	}

	session.AgentID = matchedAgent.ID
	if _, err := s.chatSessionStore.UpdateChatSession(ctx, sessionID, *session); err != nil {
		return "", fmt.Errorf("update session: %w", err)
	}

	// Clear conversation history.
	if err := s.chatSessionStore.DeleteChatMessages(ctx, sessionID); err != nil {
		slog.Warn("failed to clear messages on agent switch", "session_id", sessionID, "error", err)
	}

	return matchedAgent.Name, nil
}

// checkBotAccess checks if a user is allowed to use the bot.
// Returns: allowed bool, wasPending bool (true if pending_approval is on and user was added to pending).
func (s *Server) checkBotAccess(ctx context.Context, botID, userID, accessMode string, pendingApproval bool, allowedUsers []string) (bool, bool) {
	// For DB bots, fetch current config for dynamic updates.
	if botID != "" && s.botConfigStore != nil {
		dbCfg, err := s.botConfigStore.GetBotConfig(ctx, botID)
		if err == nil && dbCfg != nil {
			accessMode = dbCfg.AccessMode
			pendingApproval = dbCfg.PendingApproval
			allowedUsers = dbCfg.AllowedUsers

			if accessMode == "allowlist" {
				if slices.Contains(allowedUsers, userID) {
					return true, false
				}
				if pendingApproval {
					// Add to pending if not already there.
					if !slices.Contains(dbCfg.PendingUsers, userID) {
						dbCfg.PendingUsers = append(dbCfg.PendingUsers, userID)
						if _, err := s.botConfigStore.UpdateBotConfig(ctx, botID, *dbCfg); err != nil {
							slog.Error("failed to update pending users", "bot_id", botID, "error", err)
						}
					}
					return false, true
				}
				return false, false
			}
			return true, false
		}
	}

	// Static config (YAML) or fallback.
	if accessMode == "allowlist" && len(allowedUsers) > 0 {
		return slices.Contains(allowedUsers, userID), false
	}
	return true, false // open mode
}
