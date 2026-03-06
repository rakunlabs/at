package server

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/rakunlabs/at/internal/config"
)

// startDiscordBot starts a Discord bot that routes messages to the agentic loop.
func (s *Server) startDiscordBot(ctx context.Context, botID string, cfg *config.DiscordBotConfig) {
	dg, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		slog.Error("discord bot: failed to create session", "error", err)
		return
	}

	dg.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages | discordgo.IntentsMessageContent

	dg.AddHandler(func(sess *discordgo.Session, m *discordgo.MessageCreate) {
		// Ignore own messages.
		if m.Author.ID == sess.State.User.ID {
			return
		}

		// Determine agent ID for this channel.
		agentID := cfg.DefaultAgentID
		if id, ok := cfg.ChannelAgents[m.ChannelID]; ok {
			agentID = id
		}
		if agentID == "" {
			return
		}

		// Access control check.
		allowed, wasPending := s.checkBotAccess(ctx, botID, m.Author.ID, cfg.AccessMode, cfg.PendingApproval, cfg.AllowedUsers)
		if !allowed {
			if wasPending {
				sess.ChannelMessageSend(m.ChannelID, "Your access is pending approval.") //nolint:errcheck
			}
			return
		}

		go func() {
			s.handleDiscordMessage(ctx, sess, m, agentID)
		}()
	})

	if err := dg.Open(); err != nil {
		slog.Error("discord bot: failed to open connection", "error", err)
		return
	}

	slog.Info("discord bot started", "user", dg.State.User.Username)

	// Wait for context cancellation, then close.
	go func() {
		<-ctx.Done()
		slog.Info("discord bot: shutting down")
		dg.Close()
	}()
}

func (s *Server) handleDiscordMessage(ctx context.Context, sess *discordgo.Session, m *discordgo.MessageCreate, agentID string) {
	sessionID, err := s.findOrCreateBotSession(ctx, "discord", m.Author.ID, m.ChannelID, agentID)
	if err != nil {
		slog.Error("discord bot: session lookup failed", "error", err)
		return
	}

	// Handle bot commands.
	switch {
	case m.Content == "!reset":
		if err := s.chatSessionStore.DeleteChatMessages(ctx, sessionID); err != nil {
			slog.Error("discord bot: reset failed", "session_id", sessionID, "error", err)
			sess.ChannelMessageSend(m.ChannelID, "Failed to reset session.") //nolint:errcheck
			return
		}
		sess.ChannelMessageSend(m.ChannelID, "Session cleared. Starting fresh!") //nolint:errcheck
		return
	case m.Content == "!login" || strings.HasPrefix(m.Content, "!login "):
		provider := strings.TrimPrefix(m.Content, "!login")
		provider = strings.TrimSpace(provider)
		if provider == "" {
			provider = "google"
		}
		loginURL := s.buildOAuthLoginURL(ctx, provider, "discord", m.Author.ID)
		if loginURL == "" {
			sess.ChannelMessageSend(m.ChannelID, "OAuth login is not available. Make sure external_url is configured and the provider's client_id variable is set.") //nolint:errcheck
			return
		}
		sess.ChannelMessageSend(m.ChannelID, "Click the link below to connect your "+provider+" account:\n"+loginURL) //nolint:errcheck
		return
	case m.Content == "!help":
		helpText := "Available commands:\n" +
			"**!reset** - Clear conversation history and start fresh\n" +
			"**!login** - Connect your Google account (usage: !login or !login google)\n" +
			"**!help** - Show this help message"
		sess.ChannelMessageSend(m.ChannelID, helpText) //nolint:errcheck
		return
	}

	// Send typing indicator periodically.
	typingCtx, typingCancel := context.WithCancel(ctx)
	defer typingCancel()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		sess.ChannelTyping(m.ChannelID) //nolint:errcheck
		for {
			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
				sess.ChannelTyping(m.ChannelID) //nolint:errcheck
			}
		}
	}()

	response, err := s.collectAgenticResponse(ctx, sessionID, m.Content)
	typingCancel()

	if err != nil {
		slog.Error("discord bot: agentic loop failed", "session_id", sessionID, "error", err)
		sess.ChannelMessageSend(m.ChannelID, "Sorry, an error occurred processing your message.") //nolint:errcheck
		return
	}

	if response == "" {
		response = "(no response)"
	}

	// Discord message limit is 2000 chars.
	for len(response) > 0 {
		chunk := response
		if len(chunk) > 2000 {
			// Try to break at last newline before limit.
			cutAt := 2000
			if idx := lastIndexBefore(response, '\n', 2000); idx > 0 {
				cutAt = idx + 1
			}
			chunk = response[:cutAt]
			response = response[cutAt:]
		} else {
			response = ""
		}

		if _, err := sess.ChannelMessageSend(m.ChannelID, chunk); err != nil {
			slog.Error("discord bot: failed to send message", "error", err)
			return
		}
	}
}

// lastIndexBefore returns the last index of byte b in s before position limit, or -1.
func lastIndexBefore(s string, b byte, limit int) int {
	if limit > len(s) {
		limit = len(s)
	}
	for i := limit - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}
