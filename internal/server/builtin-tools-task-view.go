package server

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rakunlabs/at/internal/service"
)

// Projections and caps for the task tools an agent calls.
//
// These tools used to return whole object graphs: every comment with its full
// body, every child task with its full description and result, and every task
// in the workspace. The loop governor then cut the JSON at a byte boundary,
// which lands mid-structure and leaves the model parsing a truncated object it
// was never told was truncated.
//
// The rule here is the opposite: each tool decides what to drop, keeps the
// reply valid JSON, and says in the payload what it elided and how to fetch it.
// The worst case is a model that knows it is looking at a summary; the previous
// worst case was a model silently reasoning over half a record.
const (
	// taskCommentLimit is how many of the most recent comments a task view
	// carries. Review threads grow without bound; the newest carry the
	// instructions that still apply.
	taskCommentLimit = 20

	// taskCommentBodyLimit bounds one comment body. Long enough for a detailed
	// review note, short enough that twenty of them stay well inside the tool
	// result budget.
	taskCommentBodyLimit = 2000

	// taskChildLimit bounds how many child summaries a view lists.
	taskChildLimit = 50

	// taskTextPreviewLimit bounds the description and result previews inside a
	// child summary. The full text is one task_get away.
	taskTextPreviewLimit = 400

	// taskListDefaultLimit and taskListMaxLimit bound task_list paging.
	taskListDefaultLimit = 50
	taskListMaxLimit     = 200
)

// taskSummary is the compact projection used wherever a list of tasks is
// returned. It deliberately omits description and result in full.
type taskSummary struct {
	ID                 string `json:"id"`
	Identifier         string `json:"identifier,omitempty"`
	Title              string `json:"title"`
	Status             string `json:"status"`
	PriorityLevel      string `json:"priority_level,omitempty"`
	OrganizationID     string `json:"organization_id,omitempty"`
	AssignedAgentID    string `json:"assigned_agent_id,omitempty"`
	ParentID           string `json:"parent_id,omitempty"`
	UpdatedAt          string `json:"updated_at"`
	DescriptionPreview string `json:"description_preview,omitempty"`
	ResultPreview      string `json:"result_preview,omitempty"`
}

// taskCommentView is a comment trimmed for an agent's context.
type taskCommentView struct {
	ID         string `json:"id"`
	AuthorID   string `json:"author_id,omitempty"`
	AuthorType string `json:"author_type,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	Body       string `json:"body"`
}

// truncateRunes cuts on a rune boundary and marks the cut, so a model can tell
// a short field from a trimmed one.
func truncateRunes(text string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return strings.TrimRight(string(runes[:limit]), " \t\n") + "… (truncated)"
}

func newTaskSummary(t service.Task) taskSummary {
	return taskSummary{
		ID:                 t.ID,
		Identifier:         t.Identifier,
		Title:              t.Title,
		Status:             t.Status,
		PriorityLevel:      t.PriorityLevel,
		OrganizationID:     t.OrganizationID,
		AssignedAgentID:    t.AssignedAgentID,
		ParentID:           t.ParentID,
		UpdatedAt:          t.UpdatedAt,
		DescriptionPreview: truncateRunes(t.Description, taskTextPreviewLimit),
		ResultPreview:      truncateRunes(t.Result, taskTextPreviewLimit),
	}
}

// summarizeChildren projects child tasks and reports how many were dropped.
func summarizeChildren(children []service.Task) ([]taskSummary, int) {
	omitted := 0
	if len(children) > taskChildLimit {
		omitted = len(children) - taskChildLimit
		children = children[:taskChildLimit]
	}
	out := make([]taskSummary, 0, len(children))
	for _, c := range children {
		out = append(out, newTaskSummary(c))
	}
	return out, omitted
}

// summarizeComments keeps the most recent comments, trims long bodies, and
// drops the serialized conversation state the delegation loop parks on a task
// when it runs out of iterations.
//
// That state comment is machine resume data, not discussion: it can be hundreds
// of kilobytes of JSON, and returning it to an agent inspecting its own task
// meant replaying its entire previous conversation back into its context.
func summarizeComments(comments []service.IssueComment) (views []taskCommentView, older, state int) {
	kept := make([]service.IssueComment, 0, len(comments))
	for _, c := range comments {
		if strings.HasPrefix(strings.TrimSpace(c.Body), conversationStatePrefix) {
			state++
			continue
		}
		kept = append(kept, c)
	}
	if len(kept) > taskCommentLimit {
		older = len(kept) - taskCommentLimit
		kept = kept[len(kept)-taskCommentLimit:]
	}
	views = make([]taskCommentView, 0, len(kept))
	for _, c := range kept {
		views = append(views, taskCommentView{
			ID:         c.ID,
			AuthorID:   c.AuthorID,
			AuthorType: c.AuthorType,
			CreatedAt:  c.CreatedAt,
			Body:       truncateRunes(c.Body, taskCommentBodyLimit),
		})
	}
	return views, older, state
}

// elisionNotes turns the counts above into one plain-language list the model
// can act on. Empty when nothing was dropped, so a small task reads clean.
func elisionNotes(omittedChildren, olderComments, stateComments int) []string {
	var notes []string
	if omittedChildren > 0 {
		notes = append(notes, plural(omittedChildren, "more child task is", "more child tasks are")+
			" not shown; call task_list with parent filtering or open them by id.")
	}
	if olderComments > 0 {
		notes = append(notes, plural(olderComments, "older comment is", "older comments are")+
			" not shown; only the most recent "+itoa(taskCommentLimit)+" are included.")
	}
	if stateComments > 0 {
		notes = append(notes, "Internal conversation-state records were omitted; they are resume data for the delegation loop, not discussion.")
	}
	return notes
}

func itoa(n int) string { return strconv.Itoa(n) }

// plural renders "<n> <noun>" picking the form that agrees with n.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return itoa(n) + " " + many
}

// intArg reads a numeric tool argument. JSON decoding yields float64, but a
// caller constructing args in Go passes int, so accept both.
func intArg(value any, fallback int) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return fallback
}
