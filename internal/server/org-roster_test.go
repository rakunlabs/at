package server

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/loopgov"
	"github.com/rakunlabs/query"
)

// mockSkillStoreForRoster resolves skills by ID or name from an in-memory
// map keyed by both.
type mockSkillStoreForRoster struct {
	byKey map[string]*service.Skill
}

func (m *mockSkillStoreForRoster) ListSkills(_ context.Context, _ *query.Query) (*service.ListResult[service.Skill], error) {
	return nil, nil
}
func (m *mockSkillStoreForRoster) GetSkill(_ context.Context, id string) (*service.Skill, error) {
	return m.byKey[id], nil
}
func (m *mockSkillStoreForRoster) GetSkillByName(_ context.Context, name string) (*service.Skill, error) {
	return m.byKey[name], nil
}
func (m *mockSkillStoreForRoster) CreateSkill(_ context.Context, s service.Skill) (*service.Skill, error) {
	return &s, nil
}
func (m *mockSkillStoreForRoster) UpdateSkill(_ context.Context, _ string, s service.Skill) (*service.Skill, error) {
	return &s, nil
}
func (m *mockSkillStoreForRoster) DeleteSkill(_ context.Context, _ string) error { return nil }

func TestAgentCapabilitySummary(t *testing.T) {
	s := &Server{
		skillStore: &mockSkillStoreForRoster{byKey: map[string]*service.Skill{
			"skill-vc": {ID: "skill-vc", Name: "Video Composer"},
			"ffmpeg":   {ID: "ffmpeg", Name: "FFmpeg Guide"},
		}},
	}

	agent := &service.Agent{
		Name: "Producer",
		Config: service.AgentConfig{
			Skills:       []service.SkillRef{{ID: "skill-vc"}, {ID: "ffmpeg"}, {ID: "unknown-skill"}},
			BuiltinTools: []string{"bash_execute", "task_create"},
			MCPSets:      []string{"elevenlabs"},
		},
	}

	got := s.agentCapabilitySummary(context.Background(), agent)

	// Resolved names, unresolved fallback to raw id, then tools + mcp.
	for _, want := range []string{"Video Composer", "FFmpeg Guide", "unknown-skill", "bash_execute", "elevenlabs"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary %q missing %q", got, want)
		}
	}
	if !strings.HasPrefix(got, "skills:") {
		t.Fatalf("summary should lead with skills, got %q", got)
	}
	if !strings.Contains(got, "tools:") || !strings.Contains(got, "mcp:") {
		t.Fatalf("summary missing sections: %q", got)
	}
}

func TestAgentCapabilitySummary_EmptyWhenNoCapabilities(t *testing.T) {
	s := &Server{}
	got := s.agentCapabilitySummary(context.Background(), &service.Agent{Name: "Bare"})
	if got != "" {
		t.Fatalf("expected empty summary for bare agent, got %q", got)
	}
	if s.agentCapabilitySummary(context.Background(), nil) != "" {
		t.Fatal("nil agent must yield empty summary")
	}
}

func TestJoinCapped(t *testing.T) {
	if got := joinCapped([]string{"a", "b"}, 8); got != "a, b" {
		t.Fatalf("under cap = %q", got)
	}
	got := joinCapped([]string{"a", "b", "c"}, 2)
	if got != "a, b, +1 more" {
		t.Fatalf("over cap = %q", got)
	}
}

func TestOrgContextPrompt(t *testing.T) {
	org := &service.Organization{Name: "YouTube Studio", Description: "We ship shorts."}

	head := orgContextPrompt(org, 0)
	if !strings.Contains(head, "YouTube Studio") || !strings.Contains(head, "We ship shorts.") {
		t.Fatalf("head prompt missing org info: %q", head)
	}
	if !strings.Contains(head, "head") {
		t.Fatalf("depth-0 prompt should note head ownership: %q", head)
	}

	child := orgContextPrompt(org, 2)
	if strings.Contains(child, "head") {
		t.Fatalf("non-head prompt must not claim ownership: %q", child)
	}

	if orgContextPrompt(nil, 0) != "" {
		t.Fatal("nil org must yield empty prompt")
	}
	if orgContextPrompt(&service.Organization{}, 0) != "" {
		t.Fatal("nameless org must yield empty prompt")
	}
}

func TestDelegationContextPrompt(t *testing.T) {
	org := &service.Organization{ID: "org1", Name: "Studio"}
	parent := &service.Task{ID: "parent-1", Identifier: "STU-1", Title: "Make a short about cats", AssignedAgentID: "agent-head"}
	child := &service.Task{ID: "child-1", ParentID: "parent-1", Title: "Write the script"}

	taskStore := &mockTaskStoreForDelegation{}
	// Seed the parent so GetTask finds it.
	taskStore.tasks = []service.Task{*parent}

	s := &Server{
		taskStore:     taskStore,
		agentStore:    &mockAgentStoreForDelegation{agents: map[string]*service.Agent{"agent-head": {ID: "agent-head", Name: "Content Director"}}},
		orgAgentStore: &mockOrgAgentStoreForDelegation{agents: []service.OrganizationAgent{{OrganizationID: "org1", AgentID: "agent-head", Role: "head", Title: "Content Director"}}},
	}

	got := s.delegationContextPrompt(context.Background(), org, child)
	if !strings.Contains(got, "Content Director") {
		t.Fatalf("missing delegator name: %q", got)
	}
	if !strings.Contains(got, "STU-1") || !strings.Contains(got, "Make a short about cats") {
		t.Fatalf("missing parent task reference: %q", got)
	}
	if !strings.Contains(got, "Delegation Context") {
		t.Fatalf("missing header: %q", got)
	}

	// Root task → no delegation context.
	if s.delegationContextPrompt(context.Background(), org, parent) != "" {
		t.Fatal("root task must yield empty delegation context")
	}
}

// capturingProvider records the system + user content of every Chat call so
// tests can assert what actually reached the model.
type capturingProvider struct {
	mu        sync.Mutex
	responses []*service.LLMResponse
	calls     int
	systems   []string
	users     []string
}

func (c *capturingProvider) Chat(_ context.Context, _ string, messages []service.Message, _ []service.Tool, _ *service.ChatOptions) (*service.LLMResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var sys, usr strings.Builder
	for _, m := range messages {
		text := messageText(m)
		switch m.Role {
		case "system":
			sys.WriteString(text)
		case "user":
			usr.WriteString(text)
		}
	}
	c.systems = append(c.systems, sys.String())
	c.users = append(c.users, usr.String())

	if c.calls >= len(c.responses) {
		c.calls++
		return &service.LLMResponse{Content: "done", Finished: true}, nil
	}
	resp := c.responses[c.calls]
	c.calls++
	return resp, nil
}

// messageText flattens a message's content (string or content blocks) to
// searchable text.
func messageText(m service.Message) string {
	if s, ok := m.Content.(string); ok {
		return s
	}
	if blocks, ok := m.Content.([]service.ContentBlock); ok {
		var b strings.Builder
		for _, blk := range blocks {
			b.WriteString(blk.Text)
			b.WriteString(" ")
			b.WriteString(blk.Content)
			b.WriteString(" ")
		}
		return b.String()
	}
	return ""
}

func (c *capturingProvider) allSystems() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.systems, "\n----\n")
}

func (c *capturingProvider) allUsers() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.users, "\n----\n")
}

// TestOrgDelegation_EnrichedPromptsEndToEnd runs a real delegation and
// asserts the manager sees a capability-annotated team roster and the child
// sees who delegated it + the passed context.
func TestOrgDelegation_EnrichedPromptsEndToEnd(t *testing.T) {
	provider := &capturingProvider{responses: []*service.LLMResponse{
		// Alpha (head) iter 0: delegate to Bee with context.
		{ToolCalls: []service.ToolCall{{ID: "tc1", Name: "delegate_to_bee", Arguments: map[string]any{
			"task":    "Write a 60s script about otters",
			"context": "This is for a kids channel; keep it playful and under 120 words.",
		}}}},
		// Bee iter 0: finish.
		{Content: "script done", Finished: true},
		// Alpha iter 1: finish.
		{Content: "all wrapped up", Finished: true},
	}}

	agents := map[string]*service.Agent{
		"agent-a": {ID: "agent-a", Name: "Alpha", Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 5, Description: "Coordinates the team."}},
		"agent-b": {ID: "agent-b", Name: "Bee", Config: service.AgentConfig{
			Provider: "prov1", Model: "m1", MaxIterations: 5,
			Description:  "Writes scripts.",
			Skills:       []service.SkillRef{{ID: "screenwriting"}},
			BuiltinTools: []string{"task_complete"},
		}},
	}
	orgAgents := []service.OrganizationAgent{
		{ID: "oa1", OrganizationID: "org1", AgentID: "agent-a", ParentAgentID: "", Status: "active", Role: "head", Title: "Content Director"},
		{ID: "oa2", OrganizationID: "org1", AgentID: "agent-b", ParentAgentID: "agent-a", Status: "active", Role: "member", Title: "Script Writer"},
	}

	taskStore := &mockTaskStoreForDelegation{}
	s := &Server{
		agentStore:        &mockAgentStoreForDelegation{agents: agents},
		taskStore:         taskStore,
		orgAgentStore:     &mockOrgAgentStoreForDelegation{agents: orgAgents},
		organizationStore: &mockOrgStoreForDelegation{orgs: map[string]*service.Organization{"org1": {ID: "org1", Name: "Otter Studio", Description: "We make short animal videos.", IssuePrefix: "OTR"}}},
		skillStore:        &mockSkillStoreForRoster{byKey: map[string]*service.Skill{"screenwriting": {ID: "screenwriting", Name: "Screenwriting"}}},
		loopGov:           loopgov.New(loopgov.Config{WorkspaceRoot: t.TempDir()}, nil),
		providers: map[string]ProviderInfo{
			"prov1": {provider: provider, providerType: "openai", defaultModel: "m1"},
		},
	}

	task, err := taskStore.CreateTask(context.Background(), service.Task{
		OrganizationID: "org1", Title: "Make an otter short", Status: service.TaskStatusOpen, AssignedAgentID: "agent-a",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	org := &service.Organization{ID: "org1", Name: "Otter Studio", Description: "We make short animal videos.", IssuePrefix: "OTR", MaxDelegationDepth: 5}
	if err := s.runOrgDelegation(context.Background(), org, task, "agent-a", 0); err != nil {
		t.Fatalf("runOrgDelegation: %v", err)
	}

	systems := provider.allSystems()
	users := provider.allUsers()

	// Manager (Alpha) sees a capability-annotated roster of Bee.
	if !strings.Contains(systems, "## Your Team") {
		t.Fatalf("manager prompt missing team roster:\n%s", systems)
	}
	if !strings.Contains(systems, "Bee") || !strings.Contains(systems, "Script Writer") {
		t.Fatalf("roster missing report identity:\n%s", systems)
	}
	if !strings.Contains(systems, "Screenwriting") {
		t.Fatalf("roster missing resolved capability (skill name):\n%s", systems)
	}
	// Org mission + head framing reached the manager.
	if !strings.Contains(systems, "Otter Studio") || !strings.Contains(systems, "head") {
		t.Fatalf("manager prompt missing org mission / head framing:\n%s", systems)
	}

	// Child (Bee) sees who delegated + parent task.
	if !strings.Contains(systems, "## Delegation Context") {
		t.Fatalf("child prompt missing delegation context:\n%s", systems)
	}
	if !strings.Contains(systems, "Alpha") {
		t.Fatalf("child prompt missing delegator name:\n%s", systems)
	}
	if !strings.Contains(systems, "Make an otter short") {
		t.Fatalf("child prompt missing parent task title:\n%s", systems)
	}

	// The passed context reached the child via its task description.
	if !strings.Contains(users, "Context from delegator") || !strings.Contains(users, "kids channel") {
		t.Fatalf("child user prompt missing delegator context:\n%s", users)
	}
}
