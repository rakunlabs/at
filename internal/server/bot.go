package server

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// findOrCreateBotSession looks up an existing chat session by platform identifiers,
// or creates a new one if none exists. Returns the session ID and the session's
// actual agent ID (which may differ from defaultAgentID if the user switched agents).
func (s *Server) findOrCreateBotSession(ctx context.Context, platform, userID, channelID, defaultAgentID string) (string, string, error) {
	if s.chatSessionStore == nil {
		return "", "", fmt.Errorf("chat session store not configured")
	}

	session, err := s.chatSessionStore.GetChatSessionByPlatform(ctx, platform, userID, channelID)
	if err != nil {
		return "", "", fmt.Errorf("lookup platform session: %w", err)
	}
	if session != nil {
		// Return the session's current agent — respect /switch choices.
		return session.ID, session.AgentID, nil
	}

	// Create new session with the bot's default agent.
	name := fmt.Sprintf("%s-%s", platform, channelID)
	if channelID == "" {
		name = fmt.Sprintf("%s-%s", platform, userID)
	}

	newSession, err := s.chatSessionStore.CreateChatSession(ctx, service.ChatSession{
		AgentID: defaultAgentID,
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
		return "", "", fmt.Errorf("create platform session: %w", err)
	}

	slog.Info("created bot chat session", "platform", platform, "session_id", newSession.ID, "agent_id", defaultAgentID)
	return newSession.ID, defaultAgentID, nil
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

// runningBot tracks a running bot instance with its cancel function.
type runningBot struct {
	cancel    context.CancelFunc
	platform  string
	startedAt string
}

// stopBot stops a running bot by cancelling its context.
func (s *Server) stopBot(botID string) bool {
	if v, ok := s.runningBots.LoadAndDelete(botID); ok {
		rb := v.(*runningBot)
		rb.cancel()
		slog.Info("bot stopped", "id", botID, "platform", rb.platform)
		return true
	}
	return false
}

// isBotRunning checks if a bot is currently running.
func (s *Server) isBotRunning(botID string) bool {
	_, ok := s.runningBots.Load(botID)
	return ok
}

// getBotRunningInfo returns running info for a bot, or nil if not running.
func (s *Server) getBotRunningInfo(botID string) *runningBot {
	if v, ok := s.runningBots.Load(botID); ok {
		return v.(*runningBot)
	}
	return nil
}

// startBotFromConfig starts a single bot based on its DB configuration.
func (s *Server) startBotFromConfig(ctx context.Context, bot *service.BotConfig) {
	// Stop any existing instance first.
	s.stopBot(bot.ID)

	// Create per-bot cancellable context.
	botCtx, cancel := context.WithCancel(ctx)

	rb := &runningBot{
		cancel:    cancel,
		platform:  bot.Platform,
		startedAt: time.Now().UTC().Format(time.RFC3339),
	}
	s.runningBots.Store(bot.ID, rb)
	switch bot.Platform {
	case "discord":
		s.startDiscordBot(botCtx, bot.ID, &config.DiscordBotConfig{
			Token:           bot.Token,
			DefaultAgentID:  bot.DefaultAgentID,
			ChannelAgents:   bot.ChannelAgents,
			AllowedAgentIDs: bot.AllowedAgentIDs,
			AccessMode:      bot.AccessMode,
			PendingApproval: bot.PendingApproval,
			AllowedUsers:    bot.AllowedUsers,
		})
	case "telegram":
		s.startTelegramBot(botCtx, bot.ID, &config.TelegramBotConfig{
			Token:           bot.Token,
			DefaultAgentID:  bot.DefaultAgentID,
			ChatAgents:      bot.ChannelAgents,
			AllowedAgentIDs: bot.AllowedAgentIDs,
			AccessMode:      bot.AccessMode,
			PendingApproval: bot.PendingApproval,
			AllowedUsers:    bot.AllowedUsers,
		})
	default:
		cancel() // no bot started, clean up context
		s.runningBots.Delete(bot.ID)
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

// TaskDoneCallback is called when a bot-created task finishes (success or failure).
type TaskDoneCallback func(identifier, status, result string)

// createBotTask creates a background org task for the given agent and topic.
// It finds the agent's organization, creates a task, and starts async delegation.
// An optional callback is called when the task finishes.
// Returns (taskID, identifier, error).
func (s *Server) createBotTask(ctx context.Context, agentID, topic string, onDone ...TaskDoneCallback) (string, string, error) {
	if s.orgAgentStore == nil || s.organizationStore == nil || s.taskStore == nil {
		return "", "", fmt.Errorf("task/org stores not configured")
	}

	// Find which org this agent belongs to — check memberships first, then head agent.
	var org *service.Organization

	// Check org memberships
	orgMemberships, err := s.orgAgentStore.ListAgentOrganizations(ctx, agentID)
	if err == nil && len(orgMemberships) > 0 {
		org, _ = s.organizationStore.GetOrganization(ctx, orgMemberships[0].OrganizationID)
	}

	// If not found via membership, search all orgs for this agent as head
	if org == nil {
		allOrgs, err := s.organizationStore.ListOrganizations(ctx, nil)
		if err == nil {
			for _, o := range allOrgs.Data {
				if o.HeadAgentID == agentID {
					org = &o
					break
				}
			}
		}
	}

	if org == nil {
		return "", "", fmt.Errorf("agent is not a member or head of any organization. Add the agent to an organization first.")
	}

	// The head agent of the org handles task delegation
	if org.HeadAgentID == "" {
		return "", "", fmt.Errorf("organization has no head agent configured")
	}

	// Generate identifier
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

	// Create task
	task := service.Task{
		OrganizationID:  org.ID,
		AssignedAgentID: org.HeadAgentID,
		Title:           topic,
		Status:          service.TaskStatusOpen,
		Identifier:      identifier,
		RequestDepth:    0,
		CreatedBy:       "telegram-bot",
	}

	record, err := s.taskStore.CreateTask(ctx, task)
	if err != nil {
		return "", "", fmt.Errorf("create task: %w", err)
	}

	// Fire async delegation with optional completion callback
	go func() {
		delegCtx := context.Background()
		if err := s.runOrgDelegation(delegCtx, org, record, org.HeadAgentID, 0); err != nil {
			slog.Error("bot-task: delegation failed",
				"org_id", org.ID,
				"task_id", record.ID,
				"error", err,
			)
			errResult := fmt.Sprintf("delegation failed: %v", err)
			if s.taskStore != nil {
				_, _ = s.taskStore.UpdateTask(delegCtx, record.ID, service.Task{
					Status: service.TaskStatusCancelled,
					Result: errResult,
				})
			}
			// Notify callback of failure
			for _, cb := range onDone {
				cb(identifier, "failed", errResult)
			}
			return
		}

		// Task succeeded — get the final state and notify
		if s.taskStore != nil {
			if updated, err := s.taskStore.GetTask(delegCtx, record.ID); err == nil && updated != nil {
				for _, cb := range onDone {
					cb(identifier, updated.Status, updated.Result)
				}
				return
			}
		}

		// Fallback notify
		for _, cb := range onDone {
			cb(identifier, "done", "")
		}
	}()

	return record.ID, identifier, nil
}

// createBotSubtask creates a subtask under a parent task and runs it in background.
func (s *Server) createBotSubtask(ctx context.Context, parentTask *service.Task, title, description string, onDone ...TaskDoneCallback) (string, string, error) {
	if s.taskStore == nil || s.organizationStore == nil {
		return "", "", fmt.Errorf("stores not configured")
	}

	org, err := s.organizationStore.GetOrganization(ctx, parentTask.OrganizationID)
	if err != nil || org == nil {
		return "", "", fmt.Errorf("organization not found")
	}

	if org.HeadAgentID == "" {
		return "", "", fmt.Errorf("organization has no head agent")
	}

	// Generate identifier
	counter, err := s.organizationStore.IncrementIssueCounter(ctx, parentTask.OrganizationID)
	if err != nil {
		return "", "", fmt.Errorf("generate identifier: %w", err)
	}

	prefix := org.IssuePrefix
	if prefix == "" {
		prefix = parentTask.OrganizationID
		if len(prefix) > 4 {
			prefix = prefix[:4]
		}
	}
	identifier := fmt.Sprintf("%s-%d", prefix, counter)

	task := service.Task{
		OrganizationID:  parentTask.OrganizationID,
		ParentID:        parentTask.ID,
		AssignedAgentID: org.HeadAgentID,
		Title:           title,
		Description:     description,
		Status:          service.TaskStatusOpen,
		Identifier:      identifier,
		RequestDepth:    0,
		CreatedBy:       "telegram-bot",
	}

	record, err := s.taskStore.CreateTask(ctx, task)
	if err != nil {
		return "", "", fmt.Errorf("create subtask: %w", err)
	}

	go func() {
		delegCtx := context.Background()
		if err := s.runOrgDelegation(delegCtx, org, record, org.HeadAgentID, 0); err != nil {
			slog.Error("bot-subtask: delegation failed",
				"parent_id", parentTask.ID,
				"subtask_id", record.ID,
				"error", err,
			)
			errResult := fmt.Sprintf("delegation failed: %v", err)
			if s.taskStore != nil {
				_, _ = s.taskStore.UpdateTask(delegCtx, record.ID, service.Task{
					Status: service.TaskStatusCancelled,
					Result: errResult,
				})
			}
			for _, cb := range onDone {
				cb(identifier, "failed", errResult)
			}
			return
		}

		if s.taskStore != nil {
			if updated, err := s.taskStore.GetTask(delegCtx, record.ID); err == nil && updated != nil {
				for _, cb := range onDone {
					cb(identifier, updated.Status, updated.Result)
				}
				return
			}
		}

		for _, cb := range onDone {
			cb(identifier, "done", "")
		}
	}()

	return record.ID, identifier, nil
}

// agentOrgIDs returns all organization IDs this agent belongs to (via membership or as head agent).
func (s *Server) agentOrgIDs(ctx context.Context, agentID string) []string {
	orgIDs := make(map[string]bool)

	if s.orgAgentStore != nil {
		memberships, _ := s.orgAgentStore.ListAgentOrganizations(ctx, agentID)
		for _, m := range memberships {
			orgIDs[m.OrganizationID] = true
		}
	}

	if s.organizationStore != nil {
		q := query.New().SetLimit(100)
		allOrgs, _ := s.organizationStore.ListOrganizations(ctx, q)
		if allOrgs != nil {
			for _, o := range allOrgs.Data {
				if o.HeadAgentID == agentID {
					orgIDs[o.ID] = true
				}
			}
		}
	}

	result := make([]string, 0, len(orgIDs))
	for id := range orgIDs {
		result = append(result, id)
	}
	return result
}

// findTaskByIdentifier finds a task by its human-readable identifier (e.g., YTS-1).
func (s *Server) findTaskByIdentifier(ctx context.Context, agentID, identifier string) (*service.Task, error) {
	if s.taskStore == nil {
		return nil, fmt.Errorf("task store not configured")
	}

	identifier = strings.TrimSpace(identifier)

	result, err := s.taskStore.ListTasks(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range result.Data {
		t := &result.Data[i]
		if strings.EqualFold(t.Identifier, identifier) || t.ID == identifier {
			return t, nil
		}
	}

	return nil, nil
}

// listBotTasks returns recent tasks (up to 10) for the agent's org, newest first.
func (s *Server) listBotTasks(ctx context.Context, agentID string) ([]service.Task, error) {
	if s.taskStore == nil {
		return nil, fmt.Errorf("task store not configured")
	}

	orgIDs := s.agentOrgIDs(ctx, agentID)
	if len(orgIDs) == 0 {
		return nil, fmt.Errorf("agent is not a member of any organization")
	}

	orgIDSet := make(map[string]bool, len(orgIDs))
	for _, id := range orgIDs {
		orgIDSet[id] = true
	}

	// Simple approach: get all tasks, filter + sort in Go
	result, err := s.taskStore.ListTasks(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	if result == nil {
		return nil, nil
	}

	var tasks []service.Task
	for i := range result.Data {
		t := result.Data[i]
		if t.ParentID != "" {
			continue // skip subtasks
		}
		if !orgIDSet[t.OrganizationID] {
			continue // skip other orgs
		}
		tasks = append(tasks, t)
	}

	// Sort by updated_at descending
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].UpdatedAt > tasks[j].UpdatedAt
	})

	if len(tasks) > 10 {
		tasks = tasks[:10]
	}

	return tasks, nil
}

// findLatestTask returns the most recently updated task for the agent's org.
func (s *Server) findLatestTask(ctx context.Context, agentID string) (*service.Task, error) {
	tasks, err := s.listBotTasks(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, nil
	}
	return &tasks[0], nil
}
