package server

import (
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestDeveloperGlobRegexp(t *testing.T) {
	tests := []struct {
		pattern string
		match   []string
		miss    []string
	}{
		{"*.go", []string{"main.go", "internal/server/x.go"}, []string{"main.go.txt", "go"}},
		{"**/*.go", []string{"main.go", "a/b/c.go"}, []string{"a/b/c.ts"}},
		{"src/*.ts", []string{"src/a.ts"}, []string{"src/x/a.ts", "a.ts"}},
		{"src/**/*.test.ts", []string{"src/a.test.ts", "src/x/y/a.test.ts"}, []string{"lib/a.test.ts"}},
		{"*.{ts,tsx}", []string{"a.ts", "b/c.tsx"}, []string{"a.js"}},
		{"file?.txt", []string{"file1.txt"}, []string{"file10.txt"}},
		{"[ab].md", []string{"a.md", "x/b.md"}, []string{"c.md"}},
		{"./cmd/*/main.go", []string{"cmd/at/main.go"}, []string{"cmd/main.go"}},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			rx, err := developerGlobRegexp(tt.pattern)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range tt.match {
				if !rx.MatchString(path) {
					t.Errorf("%q should match %q", tt.pattern, path)
				}
			}
			for _, path := range tt.miss {
				if rx.MatchString(path) {
					t.Errorf("%q should not match %q", tt.pattern, path)
				}
			}
		})
	}
	for _, bad := range []string{"", "{a,b", "[ab"} {
		if _, err := developerGlobRegexp(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestDeveloperNumberedLines(t *testing.T) {
	content := "one\ntwo\r\nthree\n"
	if got := developerNumberedLines(content, 1, 0); got != "     1\tone\n     2\ttwo\n     3\tthree\n" {
		t.Fatalf("full read = %q", got)
	}
	got := developerNumberedLines(content, 2, 1)
	if !strings.HasPrefix(got, "     2\ttwo\n") || !strings.Contains(got, "pass offset 3") {
		t.Fatalf("paged read = %q", got)
	}
	if got := developerNumberedLines(content, 9, 1); !strings.Contains(got, "past the end") {
		t.Fatalf("offset past end = %q", got)
	}
	if got := developerNumberedLines("", 1, 1); got != "(empty file)" {
		t.Fatalf("empty = %q", got)
	}
}

func TestDeveloperClipOutputKeepsBothEnds(t *testing.T) {
	long := "START" + strings.Repeat("x", developerBashOutputMaxBytes*2) + "END"
	got := developerClipOutput(long)
	if !strings.HasPrefix(got, "START") || !strings.HasSuffix(got, "END") || !strings.Contains(got, "bytes omitted") {
		t.Fatalf("clipped output lost an end: %q…", got[:40])
	}
	if developerClipOutput("short") != "short" {
		t.Fatal("short output must be unchanged")
	}
}

func TestDeveloperMessagesStartAtCompaction(t *testing.T) {
	records := []service.DeveloperSessionMessage{
		{ID: "1", Role: "user", Content: "old question"},
		{ID: "2", Role: "assistant", Content: []service.ContentBlock{{Type: "text", Text: "old answer"}}},
		{ID: "3", Role: "assistant", Kind: service.DeveloperMessageKindCompaction, Content: []service.ContentBlock{{Type: "text", Text: "summary"}}},
		{ID: "4", Role: "user", Content: "new question"},
	}
	messages := developerMessages(records, "profile", "proj", "Instructions from AGENTS.md:\nuse tabs")
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want system + summary + new question", len(messages))
	}
	system, _ := messages[0].Content.(string)
	if !strings.Contains(system, "profile") || !strings.Contains(system, "use tabs") || !strings.Contains(system, "/workspace/proj") {
		t.Fatalf("system prompt misses profile, instructions or folder: %q", system)
	}
	if messages[1].Role != "user" || messages[1].Content != "summary" {
		t.Fatalf("summary replayed as %q/%v", messages[1].Role, messages[1].Content)
	}
}

func TestNormalizeDeveloperTodos(t *testing.T) {
	todos, err := service.NormalizeDeveloperTodos([]service.DeveloperSessionTodo{{Content: " write tests "}, {Content: "ship", Status: "in_progress", Priority: "high"}})
	if err != nil {
		t.Fatal(err)
	}
	if todos[0].Status != "pending" || todos[0].ID != "1" || todos[0].Content != "write tests" {
		t.Fatalf("defaults not applied: %+v", todos[0])
	}
	for _, bad := range [][]service.DeveloperSessionTodo{{{Content: ""}}, {{Content: "x", Status: "done"}}, {{Content: "x", Priority: "urgent"}}} {
		if _, err := service.NormalizeDeveloperTodos(bad); err == nil {
			t.Errorf("%+v should be refused", bad)
		}
	}
	if got := developerTodoText(todos); !strings.Contains(got, "[ ] write tests") || !strings.Contains(got, "[~] ship (high)") || !strings.Contains(got, "2 of 2 open") {
		t.Fatalf("todo text = %q", got)
	}
}

func TestDeveloperTodoToolsBypassProfiles(t *testing.T) {
	plan := service.DefaultDeveloperSpaceConfig().Plan
	if got := developerToolEffect(plan, service.ToolCall{Name: "todo_write"}); got != "allow" {
		t.Fatalf("plan todo_write = %q, want allow", got)
	}
	agentKit := &developerToolkit{agent: &service.Agent{}, confirm: map[string]bool{"todo_write": true}}
	if got := agentKit.effect(service.ToolCall{Name: "todo_write"}); got != "allow" {
		t.Fatalf("agent todo_write = %q, want allow", got)
	}
}
