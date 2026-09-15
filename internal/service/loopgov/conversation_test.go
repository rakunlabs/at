package loopgov

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestLimitLargeToolResultsPreserveConversation(t *testing.T) {
	for _, followUp := range []bool{false, true} {
		name := "within turn"
		if followUp {
			name = "next user turn"
		}
		t.Run(name, func(t *testing.T) {
			g := New(Config{}, nil)
			large := strings.Repeat("large task list ", 10000)
			messages := []service.Message{
				{Role: "system", Content: "You manage tasks."},
				{Role: "user", Content: "Resume the unfinished YouTube Shorts."},
				{Role: "assistant", Content: "May I inspect the tasks?"},
				{Role: "user", Content: "Approved."},
				{Role: "assistant", Content: []service.ContentBlock{
					{Type: "text", Text: "Inspecting tasks."},
					{Type: "tool_use", ID: "tasks", Name: "task_list", Input: map[string]any{}},
				}},
				{Role: "user", Content: []service.ContentBlock{{Type: "tool_result", ToolUseID: "tasks", Content: large}}},
			}
			if followUp {
				messages = append(messages, service.Message{Role: "assistant", Content: "I inspected the tasks."}, service.Message{Role: "user", Content: "Continue with those."})
			}
			tools := []service.Tool{{Name: "task_list", Description: strings.Repeat("tool schema ", 1000)}}
			got, err := g.LimitWithTools(context.Background(), "agent", "session", messages, tools)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"Resume the unfinished YouTube Shorts.", "May I inspect the tasks?", "Approved.", "Inspecting tasks."} {
				found := false
				for _, m := range got {
					found = found || stringContent(m) == want || strings.TrimSpace(stringContent(m)) == want
				}
				if !found {
					t.Errorf("conversation text lost: %q", want)
				}
			}
			if followUp && stringContent(got[len(got)-1]) != "Continue with those." {
				t.Fatal("latest user message lost")
			}
			if estimateMessages(got)+estimateTools(tools) > g.Config().WindowTokens {
				t.Fatal("input window exceeded")
			}
			if !reflect.DeepEqual(got, RepairToolPairs(got)) {
				t.Fatal("window has orphan tool calls/results")
			}
			if messages[5].Content.([]service.ContentBlock)[0].Content != large || len(messages[4].Content.([]service.ContentBlock)) != 2 {
				t.Fatal("original history was mutated")
			}
			notice := false
			for _, m := range got {
				notice = notice || strings.Contains(stringContent(m), "Tool activity omitted")
			}
			if !notice {
				t.Fatal("missing explicit context omission notice")
			}
		})
	}
}

func TestLimitKeepsRecentToolExchangeAfterEvictingLargeHistory(t *testing.T) {
	g := New(Config{}, nil)
	messages := []service.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "Find and resume tasks."},
		{Role: "assistant", Content: []service.ContentBlock{{Type: "tool_use", ID: "old", Name: "task_list"}}},
		{Role: "user", Content: []service.ContentBlock{{Type: "tool_result", ToolUseID: "old", Content: strings.Repeat("x", 120000)}}},
		{Role: "assistant", Content: []service.ContentBlock{{Type: "tool_use", ID: "new", Name: "task_get"}}},
		{Role: "user", Content: []service.ContentBlock{{Type: "tool_result", ToolUseID: "new", Content: "Task 42 is ready."}}},
	}
	got, err := g.Limit(context.Background(), "agent", "session", messages)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got[len(got)-2:], messages[len(messages)-2:]) {
		t.Fatal("recent tool exchange was not preserved intact")
	}
}
