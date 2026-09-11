package server

import (
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestSummarizeCommentsDropsResumeStateAndOldestFirst(t *testing.T) {
	comments := []service.IssueComment{
		{ID: "old", Body: "oldest note"},
		{ID: "state", Body: conversationStatePrefix + `[{"role":"user","content":"` + strings.Repeat("x", 50000) + `"}]`},
	}
	for i := 0; i < taskCommentLimit; i++ {
		comments = append(comments, service.IssueComment{ID: "keep", Body: "recent note"})
	}

	views, older, state := summarizeComments(comments)

	if state != 1 {
		t.Errorf("state comments counted = %d, want 1", state)
	}
	if older != 1 {
		t.Errorf("older comments dropped = %d, want 1", older)
	}
	if len(views) != taskCommentLimit {
		t.Fatalf("kept %d comments, want %d", len(views), taskCommentLimit)
	}
	// The dropped one must be the oldest, never a recent instruction.
	for _, v := range views {
		if v.ID == "old" {
			t.Error("dropped a recent comment and kept the oldest")
		}
		if strings.Contains(v.Body, conversationStatePrefix) {
			t.Fatal("resume state leaked into an agent-visible comment")
		}
	}
}

func TestSummarizeCommentsTrimsLongBodies(t *testing.T) {
	long := strings.Repeat("é", taskCommentBodyLimit*2)
	views, _, _ := summarizeComments([]service.IssueComment{{ID: "a", Body: long}})
	if len(views) != 1 {
		t.Fatalf("views = %d, want 1", len(views))
	}
	body := views[0].Body
	if !strings.HasSuffix(body, "(truncated)") {
		t.Error("trimmed body does not say it was trimmed")
	}
	// Multi-byte input must not be cut mid-rune.
	if strings.ContainsRune(body, '\uFFFD') {
		t.Error("body was cut inside a UTF-8 sequence")
	}
	if len([]rune(body)) > taskCommentBodyLimit+len([]rune("… (truncated)")) {
		t.Errorf("trimmed body is %d runes, over the cap", len([]rune(body)))
	}
}

func TestSummarizeChildrenProjectsAndCaps(t *testing.T) {
	var children []service.Task
	for i := 0; i < taskChildLimit+5; i++ {
		children = append(children, service.Task{
			ID: "c", Title: "child", Status: service.TaskStatusDone,
			Description: strings.Repeat("d", 5000),
			Result:      strings.Repeat("r", 5000),
		})
	}
	summaries, omitted := summarizeChildren(children)
	if omitted != 5 {
		t.Errorf("omitted = %d, want 5", omitted)
	}
	if len(summaries) != taskChildLimit {
		t.Fatalf("summaries = %d, want %d", len(summaries), taskChildLimit)
	}
	// The whole point: a full result must not ride along in a list view.
	if len([]rune(summaries[0].ResultPreview)) > taskTextPreviewLimit+len([]rune("… (truncated)")) {
		t.Error("child result preview is not bounded")
	}
	if len([]rune(summaries[0].DescriptionPreview)) > taskTextPreviewLimit+len([]rune("… (truncated)")) {
		t.Error("child description preview is not bounded")
	}
}

func TestElisionNotesOnlyWhenSomethingWasDropped(t *testing.T) {
	if notes := elisionNotes(0, 0, 0); len(notes) != 0 {
		t.Errorf("notes = %v, want none when nothing was elided", notes)
	}
	notes := strings.Join(elisionNotes(1, 3, 1), " ")
	for _, want := range []string{"1 more child task is", "3 older comments are", "conversation-state"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes %q missing %q", notes, want)
		}
	}
}

func TestTruncateRunesLeavesShortTextAlone(t *testing.T) {
	for _, text := range []string{"", "short", strings.Repeat("a", taskTextPreviewLimit)} {
		if got := truncateRunes(text, taskTextPreviewLimit); got != text {
			t.Errorf("truncateRunes(%d runes) modified text within the cap", len([]rune(text)))
		}
	}
}
