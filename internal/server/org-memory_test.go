package server

import (
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// ─── parseMemorySummary tests ───

func TestParseMemorySummary(t *testing.T) {
	t.Helper()

	tests := []struct {
		name     string
		input    string
		wantL0   string
		wantTags []string
		wantL1   string // substring check
	}{
		{
			name: "well-formed response",
			input: `SUMMARY: Implemented user authentication with JWT tokens

DECISIONS:
- Chose JWT over session cookies for stateless auth
- Used bcrypt for password hashing

APPROACH:
- Created auth middleware in internal/server/auth.go
- Added login and register endpoints

TAGS: auth, jwt, security, middleware`,
			wantL0:   "Implemented user authentication with JWT tokens",
			wantTags: []string{"auth", "jwt", "security", "middleware"},
			wantL1:   "## Decisions",
		},
		{
			name:   "empty input uses fallback",
			input:  "",
			wantL0: "",
		},
		{
			name:     "no sections, plain text as L0 fallback",
			input:    "This is just some plain text without any sections.",
			wantL0:   "This is just some plain text without any sections.",
			wantTags: nil,
		},
		{
			name: "only summary and tags",
			input: `SUMMARY: Fixed database connection pooling issue

TAGS: database, postgres, performance`,
			wantL0:   "Fixed database connection pooling issue",
			wantTags: []string{"database", "postgres", "performance"},
			wantL1:   "",
		},
		{
			name: "case insensitive headers",
			input: `Summary: Did some work

Decisions:
- Decided to use Go

Tags: go, backend`,
			wantL0:   "Did some work",
			wantTags: []string{"go", "backend"},
			wantL1:   "## Decisions",
		},
		{
			name:   "very long plain text gets truncated for L0 fallback",
			input:  strings.Repeat("a", 300),
			wantL0: strings.Repeat("a", 200),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseMemorySummary(tt.input)

			if result.l0 != tt.wantL0 {
				t.Errorf("l0 = %q, want %q", result.l0, tt.wantL0)
			}

			if tt.wantTags != nil {
				if len(result.tags) != len(tt.wantTags) {
					t.Errorf("tags = %v, want %v", result.tags, tt.wantTags)
				} else {
					for i, tag := range result.tags {
						if tag != tt.wantTags[i] {
							t.Errorf("tags[%d] = %q, want %q", i, tag, tt.wantTags[i])
						}
					}
				}
			}

			if tt.wantL1 != "" && !strings.Contains(result.l1, tt.wantL1) {
				t.Errorf("l1 = %q, want to contain %q", result.l1, tt.wantL1)
			}
			if tt.wantL1 == "" && tt.name != "well-formed response" && tt.name != "case insensitive headers" {
				// For tests where we expect empty L1.
				if result.l1 != "" {
					t.Errorf("l1 = %q, want empty", result.l1)
				}
			}
		})
	}
}

// ─── scoreMemories tests ───

func TestScoreMemories(t *testing.T) {
	t.Helper()

	now := time.Now().UTC().Format(time.RFC3339)
	oldDate := time.Now().Add(-60 * 24 * time.Hour).UTC().Format(time.RFC3339)

	agentA := "agent-a"
	agentB := "agent-b"

	task := &service.Task{
		ID:          "task-1",
		Title:       "Fix authentication bug",
		Description: "Users cannot log in with SSO",
		ParentID:    "parent-task-1",
	}

	memories := []service.AgentMemory{
		{
			ID:             "mem-1",
			AgentID:        agentA,
			OrganizationID: "org-1",
			TaskID:         "task-0",
			TaskIdentifier: "TSK-1",
			SummaryL0:      "Implemented SSO authentication flow",
			Tags:           []string{"auth", "sso", "security"},
			CreatedAt:      now,
		},
		{
			ID:             "mem-2",
			AgentID:        agentB,
			OrganizationID: "org-1",
			TaskID:         "task-0b",
			TaskIdentifier: "TSK-2",
			SummaryL0:      "Updated database schema for users",
			Tags:           []string{"database", "schema"},
			CreatedAt:      now,
		},
		{
			ID:             "mem-3",
			AgentID:        agentA,
			OrganizationID: "org-1",
			TaskID:         "task-0c",
			TaskIdentifier: "TSK-3",
			SummaryL0:      "Refactored logging subsystem",
			Tags:           []string{"logging", "refactor"},
			CreatedAt:      oldDate,
		},
		{
			ID:             "mem-4",
			AgentID:        agentB,
			OrganizationID: "org-1",
			TaskID:         "parent-task-1",
			TaskIdentifier: "TSK-4",
			SummaryL0:      "Set up parent authentication project",
			Tags:           []string{"auth"},
			CreatedAt:      now,
		},
	}

	scored := scoreMemories(memories, task, agentA)

	if len(scored) == 0 {
		t.Fatal("expected scored memories, got 0")
	}

	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{
			name: "results sorted by score descending",
			check: func(t *testing.T) {
				for i := 1; i < len(scored); i++ {
					if scored[i].score > scored[i-1].score {
						t.Errorf("scored[%d].score (%f) > scored[%d].score (%f)", i, scored[i].score, i-1, scored[i-1].score)
					}
				}
			},
		},
		{
			name: "own-memory bonus applied",
			check: func(t *testing.T) {
				// mem-1 belongs to agentA, mem-2 belongs to agentB.
				// Both are recent and have some tag overlap, but mem-1 should get +25 own-memory bonus.
				var mem1Score, mem2Score float64
				for _, s := range scored {
					switch s.memory.ID {
					case "mem-1":
						mem1Score = s.score
					case "mem-2":
						mem2Score = s.score
					}
				}
				if mem1Score <= mem2Score {
					t.Errorf("expected mem-1 (own agent, auth tags) > mem-2, got %f <= %f", mem1Score, mem2Score)
				}
			},
		},
		{
			name: "parent-task bonus applied",
			check: func(t *testing.T) {
				var mem4Score float64
				for _, s := range scored {
					if s.memory.ID == "mem-4" {
						mem4Score = s.score
						break
					}
				}
				if mem4Score < 50 {
					t.Errorf("expected parent-task memory (mem-4) to have score >= 50, got %f", mem4Score)
				}
			},
		},
		{
			name: "old memory has lower recency than recent",
			check: func(t *testing.T) {
				var mem1Score, mem3Score float64
				for _, s := range scored {
					switch s.memory.ID {
					case "mem-1":
						mem1Score = s.score
					case "mem-3":
						mem3Score = s.score
					}
				}
				// mem-3 is 60 days old (0 recency) while mem-1 is recent (~30 recency).
				// Both belong to agentA, but mem-1 has strong keyword/tag overlap
				// with the auth task. mem-3 may get incidental keyword matches
				// (e.g. "log" from "log in" matching "logging"), but should still
				// score lower than mem-1.
				if mem3Score >= mem1Score {
					t.Errorf("expected old mem-3 (%f) < recent mem-1 (%f)", mem3Score, mem1Score)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t)
		})
	}
}

func TestScoreMemories_Empty(t *testing.T) {
	task := &service.Task{ID: "t1", Title: "something"}
	scored := scoreMemories(nil, task, "agent-x")

	if len(scored) != 0 {
		t.Errorf("expected empty scored slice, got %d", len(scored))
	}
}

// ─── formatMemoriesForPrompt tests ───

func TestFormatMemoriesForPrompt(t *testing.T) {
	t.Helper()

	tests := []struct {
		name      string
		scored    []scoredMemory
		agentID   string
		wantSub   string // substring expected
		wantEmpty bool
	}{
		{
			name:      "empty scored returns empty",
			scored:    nil,
			agentID:   "a1",
			wantEmpty: true,
		},
		{
			name: "single own memory uses 'you' label",
			scored: []scoredMemory{
				{
					memory: service.AgentMemory{
						AgentID:        "a1",
						TaskIdentifier: "TSK-5",
						SummaryL0:      "Did something",
						Tags:           []string{"tag1"},
					},
					score: 50,
				},
			},
			agentID: "a1",
			wantSub: "by you",
		},
		{
			name: "other agent memory uses agent ID",
			scored: []scoredMemory{
				{
					memory: service.AgentMemory{
						AgentID:        "other-agent",
						TaskIdentifier: "TSK-6",
						SummaryL0:      "Did something else",
					},
					score: 40,
				},
			},
			agentID: "a1",
			wantSub: "agent other-agent",
		},
		{
			name: "includes header and summary",
			scored: []scoredMemory{
				{
					memory: service.AgentMemory{
						AgentID:        "a1",
						TaskIdentifier: "TSK-7",
						SummaryL0:      "Created the API",
						SummaryL1:      "## Decisions\n- Used REST\n",
						Tags:           []string{"api", "rest"},
					},
					score: 80,
				},
			},
			agentID: "a1",
			wantSub: "## Relevant Past Work",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatMemoriesForPrompt(tt.scored, tt.agentID)

			if tt.wantEmpty {
				if result != "" {
					t.Errorf("expected empty, got %q", result)
				}
				return
			}

			if !strings.Contains(result, tt.wantSub) {
				t.Errorf("result = %q, want to contain %q", result, tt.wantSub)
			}
		})
	}
}

func TestFormatMemoriesForPrompt_TokenBudget(t *testing.T) {
	// Create many large memories to test the budget cutoff.
	var scored []scoredMemory
	for i := 0; i < 100; i++ {
		scored = append(scored, scoredMemory{
			memory: service.AgentMemory{
				AgentID:        "a1",
				TaskIdentifier: "TSK-X",
				SummaryL0:      "Summary " + strings.Repeat("x", 200),
				SummaryL1:      strings.Repeat("Decision detail ", 50),
				Tags:           []string{"tag1", "tag2", "tag3"},
			},
			score: float64(100 - i),
		})
	}

	result := formatMemoriesForPrompt(scored, "a1")

	// Result should be under the 8000 char budget.
	if len(result) > 8200 { // slight buffer for the header
		t.Errorf("result length %d exceeds budget", len(result))
	}

	// Should include at least one memory.
	if !strings.Contains(result, "## Relevant Past Work") {
		t.Error("expected header in output")
	}
}

// ─── extractWords tests ───

func TestExtractWords(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "basic words",
			input: "Fix authentication bug",
			want:  []string{"fix", "authentication", "bug"},
		},
		{
			name:  "filters short words",
			input: "a to be or not",
			want:  []string{"not"},
		},
		{
			name:  "removes punctuation",
			input: "hello, world. (test)",
			want:  []string{"hello", "world", "test"},
		},
		{
			name:  "deduplicates",
			input: "bug bug bug fix fix",
			want:  []string{"bug", "fix"},
		},
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractWords(tt.input)

			if tt.want == nil {
				if len(got) != 0 {
					t.Errorf("got %v, want nil/empty", got)
				}
				return
			}

			if len(got) != len(tt.want) {
				t.Errorf("got %v (len %d), want %v (len %d)", got, len(got), tt.want, len(tt.want))
				return
			}

			for i, w := range tt.want {
				if got[i] != w {
					t.Errorf("got[%d] = %q, want %q", i, got[i], w)
				}
			}
		})
	}
}
