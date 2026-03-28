package server

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Memory Method Interface ───

// memoryMethod defines the extraction and recall behavior for a specific
// memory strategy. Implementations are registered by name in memoryMethods.
type memoryMethod interface {
	// Extract generates and persists memory from a completed task conversation.
	Extract(ctx context.Context, s *Server, org *service.Organization, task *service.Task, agent *service.Agent, agentID string, messages []service.Message)
	// Recall retrieves relevant past memories and returns formatted text
	// for injection into the system prompt.
	Recall(ctx context.Context, s *Server, org *service.Organization, task *service.Task, agent *service.Agent, agentID string) string
}

// memoryMethods is the registry of available memory methods.
var memoryMethods = map[string]memoryMethod{
	"summary": &summaryMemoryMethod{},
}

// resolveMemoryMethod returns the effective memory method name for the given org-agent.
// Returns "none" if memory is disabled or unconfigured.
func resolveMemoryMethod(orgAgent *service.OrganizationAgent) string {
	if orgAgent == nil {
		return "none"
	}
	if orgAgent.MemoryMethod != "" {
		return orgAgent.MemoryMethod
	}
	return "none"
}

// ─── Dispatchers ───

// extractAndPersistMemory resolves the memory method for the org-agent and
// delegates to the method's Extract implementation. Errors are logged but
// never propagated (memory extraction must not block task completion).
func (s *Server) extractAndPersistMemory(ctx context.Context, org *service.Organization, task *service.Task, agent *service.Agent, agentID string, messages []service.Message) {
	if s.agentMemoryStore == nil || s.orgAgentStore == nil {
		return
	}

	orgAgent, err := s.orgAgentStore.GetOrganizationAgentByPair(ctx, org.ID, agentID)
	if err != nil {
		slog.Warn("org-memory: failed to get org-agent config", "agent_id", agentID, "org_id", org.ID, "error", err)
		return
	}

	methodName := resolveMemoryMethod(orgAgent)
	m, ok := memoryMethods[methodName]
	if !ok {
		return // "none" or unknown method
	}

	m.Extract(ctx, s, org, task, agent, agentID, messages)
}

// recallAgentMemories resolves the memory method for the org-agent and
// delegates to the method's Recall implementation. Returns empty string
// if no relevant memories found or memory is disabled.
func (s *Server) recallAgentMemories(ctx context.Context, org *service.Organization, task *service.Task, agent *service.Agent, agentID string) string {
	if s.agentMemoryStore == nil || s.orgAgentStore == nil {
		return ""
	}

	orgAgent, err := s.orgAgentStore.GetOrganizationAgentByPair(ctx, org.ID, agentID)
	if err != nil {
		slog.Warn("org-memory: failed to get org-agent config for recall", "agent_id", agentID, "error", err)
		return ""
	}

	methodName := resolveMemoryMethod(orgAgent)
	m, ok := memoryMethods[methodName]
	if !ok {
		return "" // "none" or unknown method
	}

	return m.Recall(ctx, s, org, task, agent, agentID)
}

// ─── "summary" Memory Method ───

type summaryMemoryMethod struct{}

func (m *summaryMemoryMethod) Extract(ctx context.Context, s *Server, org *service.Organization, task *service.Task, agent *service.Agent, agentID string, messages []service.Message) {
	orgAgent, _ := s.orgAgentStore.GetOrganizationAgentByPair(ctx, org.ID, agentID)

	// Resolve the summarization provider and model.
	provider, model := resolveMemoryProvider(s, agent, orgAgent)
	if provider == nil {
		slog.Warn("org-memory: no provider available for summarization", "agent_id", agentID)
		return
	}

	// Build and send summarization prompt.
	summary, err := generateMemorySummary(ctx, provider, model, task, messages)
	if err != nil {
		slog.Warn("org-memory: summarization failed", "agent_id", agentID, "task_id", task.ID, "error", err)
		return
	}

	// Persist AgentMemory (L0 + L1).
	mem, err := s.agentMemoryStore.CreateAgentMemory(ctx, service.AgentMemory{
		AgentID:        agentID,
		OrganizationID: org.ID,
		TaskID:         task.ID,
		TaskIdentifier: task.Identifier,
		SummaryL0:      summary.l0,
		SummaryL1:      summary.l1,
		Tags:           summary.tags,
	})
	if err != nil {
		slog.Warn("org-memory: failed to persist memory", "agent_id", agentID, "task_id", task.ID, "error", err)
		return
	}

	// Persist L2 messages.
	if err := s.agentMemoryStore.CreateAgentMemoryMessages(ctx, service.AgentMemoryMessages{
		MemoryID: mem.ID,
		Messages: messages,
	}); err != nil {
		slog.Warn("org-memory: failed to persist L2 messages", "memory_id", mem.ID, "error", err)
	}

	slog.Info("org-memory: memory extracted",
		"memory_id", mem.ID, "agent_id", agentID, "task_id", task.ID,
		"l0_len", len(summary.l0), "tags", summary.tags)
}

func (m *summaryMemoryMethod) Recall(ctx context.Context, s *Server, org *service.Organization, task *service.Task, _ *service.Agent, agentID string) string {
	// Load all org memories (cross-agent).
	memories, err := s.agentMemoryStore.ListOrgMemories(ctx, org.ID)
	if err != nil {
		slog.Warn("org-memory: failed to load org memories for recall", "org_id", org.ID, "error", err)
		return ""
	}

	if len(memories) == 0 {
		return ""
	}

	// Score and rank.
	scored := scoreMemories(memories, task, agentID)
	if len(scored) == 0 {
		return ""
	}

	// Format top memories within token budget.
	return formatMemoriesForPrompt(scored, agentID)
}

// ─── Shared Helpers ───

// resolveMemoryProvider returns the LLM provider and model to use for
// summarization. It checks the org-agent memory config first, then falls
// back to the agent's own provider/model.
func resolveMemoryProvider(s *Server, agent *service.Agent, orgAgent *service.OrganizationAgent) (service.LLMProvider, string) {
	providerKey := agent.Config.Provider
	model := agent.Config.Model

	if orgAgent != nil {
		if orgAgent.MemoryProvider != "" {
			providerKey = orgAgent.MemoryProvider
		}
		if orgAgent.MemoryModel != "" {
			model = orgAgent.MemoryModel
		}
	}

	info, ok := s.getProviderInfo(providerKey)
	if !ok {
		return nil, ""
	}

	if model == "" {
		model = info.defaultModel
	}

	return info.provider, model
}

type memorySummary struct {
	l0   string
	l1   string
	tags []string
}

// generateMemorySummary calls the LLM with a structured prompt to produce
// L0 (one-line summary), L1 (decisions + approach), and tags.
func generateMemorySummary(ctx context.Context, provider service.LLMProvider, model string, task *service.Task, messages []service.Message) (*memorySummary, error) {
	// Build a compact representation of the conversation for the prompt.
	var conversationBuf strings.Builder
	for _, msg := range messages {
		role := msg.Role
		var text string
		switch v := msg.Content.(type) {
		case string:
			text = v
		case []service.ContentBlock:
			for _, b := range v {
				if b.Type == "text" && b.Text != "" {
					text += b.Text + "\n"
				}
			}
		default:
			text = fmt.Sprintf("%v", v)
		}
		if text == "" {
			continue
		}
		// Truncate long messages to keep the summarization prompt reasonable.
		if len(text) > 2000 {
			text = text[:2000] + "... [truncated]"
		}
		conversationBuf.WriteString(fmt.Sprintf("[%s]: %s\n\n", role, text))
	}

	systemPrompt := `You are a memory summarization assistant. Your job is to create a concise, structured summary of a completed task conversation.

Respond in EXACTLY this format with no additional text:

SUMMARY: <one sentence describing what was accomplished>

DECISIONS:
<key technical decisions made and their rationale, as bullet points>

APPROACH:
<how the work was done, including tools, patterns, files involved, as bullet points>

TAGS: <3-5 comma-separated topic keywords>`

	userPrompt := fmt.Sprintf("Summarize the following completed task conversation.\n\nTask: %s\n", task.Title)
	if task.Description != "" {
		userPrompt += fmt.Sprintf("Description: %s\n", task.Description)
	}
	userPrompt += fmt.Sprintf("\nConversation:\n%s", conversationBuf.String())

	resp, err := provider.Chat(ctx, model, []service.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("summarization LLM call: %w", err)
	}

	return parseMemorySummary(resp.Content), nil
}

// parseMemorySummary extracts L0, L1, and tags from the LLM's response.
func parseMemorySummary(content string) *memorySummary {
	result := &memorySummary{}

	lines := strings.Split(content, "\n")
	var section string
	var decisionsLines, approachLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)

		switch {
		case strings.HasPrefix(upper, "SUMMARY:"):
			result.l0 = strings.TrimSpace(strings.TrimPrefix(trimmed, trimmed[:len("SUMMARY:")]))
			section = ""
		case strings.HasPrefix(upper, "DECISIONS:"):
			section = "decisions"
		case strings.HasPrefix(upper, "APPROACH:"):
			section = "approach"
		case strings.HasPrefix(upper, "TAGS:"):
			tagsRaw := strings.TrimSpace(strings.TrimPrefix(trimmed, trimmed[:len("TAGS:")]))
			for _, tag := range strings.Split(tagsRaw, ",") {
				tag = strings.TrimSpace(tag)
				if tag != "" {
					result.tags = append(result.tags, tag)
				}
			}
			section = ""
		default:
			if trimmed == "" {
				continue
			}
			switch section {
			case "decisions":
				decisionsLines = append(decisionsLines, trimmed)
			case "approach":
				approachLines = append(approachLines, trimmed)
			}
		}
	}

	// Build L1 from decisions + approach.
	var l1 strings.Builder
	if len(decisionsLines) > 0 {
		l1.WriteString("## Decisions\n")
		for _, line := range decisionsLines {
			l1.WriteString(line + "\n")
		}
	}
	if len(approachLines) > 0 {
		if l1.Len() > 0 {
			l1.WriteString("\n")
		}
		l1.WriteString("## Approach\n")
		for _, line := range approachLines {
			l1.WriteString(line + "\n")
		}
	}
	result.l1 = l1.String()

	// Fallback: if parsing failed, use the whole content as L0.
	if result.l0 == "" && content != "" {
		if len(content) > 200 {
			result.l0 = content[:200]
		} else {
			result.l0 = content
		}
	}

	return result
}

type scoredMemory struct {
	memory service.AgentMemory
	score  float64
}

// scoreMemories scores each memory based on recency, tag overlap, keyword
// match, own-memory bonus, and parent-task bonus.
func scoreMemories(memories []service.AgentMemory, task *service.Task, agentID string) []scoredMemory {
	// Extract keywords from the task title + description.
	taskText := strings.ToLower(task.Title + " " + task.Description)
	taskWords := extractWords(taskText)

	now := time.Now()
	var scored []scoredMemory

	for _, mem := range memories {
		var score float64

		// 1. Recency: 0-30 points (linear decay over 30 days).
		created, err := time.Parse(time.RFC3339, mem.CreatedAt)
		if err == nil {
			age := now.Sub(created).Hours() / 24 // days
			recency := math.Max(0, 30-age)
			score += recency
		}

		// 2. Tag overlap: 20 per matching tag, max 100.
		tagScore := 0.0
		for _, tag := range mem.Tags {
			tagLower := strings.ToLower(tag)
			for _, word := range taskWords {
				if strings.Contains(tagLower, word) || strings.Contains(word, tagLower) {
					tagScore += 20
					break
				}
			}
		}
		score += math.Min(tagScore, 100)

		// 3. L0 keyword match: 0-50 points.
		l0Lower := strings.ToLower(mem.SummaryL0)
		keywordScore := 0.0
		for _, word := range taskWords {
			if len(word) > 2 && strings.Contains(l0Lower, word) {
				keywordScore += 10
			}
		}
		score += math.Min(keywordScore, 50)

		// 4. Own-memory bonus: +25.
		if mem.AgentID == agentID {
			score += 25
		}

		// 5. Parent-task bonus: +50.
		if task.ParentID != "" && mem.TaskID == task.ParentID {
			score += 50
		}

		if score > 0 {
			scored = append(scored, scoredMemory{memory: mem, score: score})
		}
	}

	// Sort by score descending.
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	return scored
}

// formatMemoriesForPrompt formats the top scored memories as a system prompt
// section, respecting a ~2000 token budget (~8000 chars).
func formatMemoriesForPrompt(scored []scoredMemory, currentAgentID string) string {
	const maxChars = 8000 // ~2000 tokens

	var buf strings.Builder
	buf.WriteString("\n\n## Relevant Past Work\n\n")

	currentLen := buf.Len()
	included := 0

	for _, sm := range scored {
		var entry strings.Builder

		agentLabel := "you"
		if sm.memory.AgentID != currentAgentID {
			agentLabel = fmt.Sprintf("agent %s", sm.memory.AgentID)
		}

		entry.WriteString(fmt.Sprintf("### %s (by %s)\n", sm.memory.TaskIdentifier, agentLabel))
		entry.WriteString(fmt.Sprintf("**Summary**: %s\n", sm.memory.SummaryL0))

		if sm.memory.SummaryL1 != "" {
			entry.WriteString(sm.memory.SummaryL1)
			entry.WriteString("\n")
		}

		if len(sm.memory.Tags) > 0 {
			entry.WriteString(fmt.Sprintf("**Tags**: %s\n", strings.Join(sm.memory.Tags, ", ")))
		}

		entry.WriteString("\n")

		entryStr := entry.String()
		if currentLen+len(entryStr) > maxChars {
			break
		}

		buf.WriteString(entryStr)
		currentLen += len(entryStr)
		included++
	}

	if included == 0 {
		return ""
	}

	return buf.String()
}

// extractWords splits text into lowercase words for keyword matching.
func extractWords(text string) []string {
	// Replace common punctuation with spaces.
	replacer := strings.NewReplacer(
		",", " ", ".", " ", ":", " ", ";", " ",
		"(", " ", ")", " ", "[", " ", "]", " ",
		"\n", " ", "\t", " ",
	)
	text = replacer.Replace(text)

	words := strings.Fields(text)
	// Deduplicate and filter short words.
	seen := make(map[string]bool)
	var result []string
	for _, w := range words {
		w = strings.ToLower(w)
		if len(w) > 2 && !seen[w] {
			seen[w] = true
			result = append(result, w)
		}
	}

	return result
}
