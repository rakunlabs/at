package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// The Kanban board layout is data, not code. Each column collects one or more
// task statuses; dropping a card into a column moves the task to that column's
// first status.
//
// Two rules protect the board from becoming a liar:
//
//   - A status may appear in at most one column. Otherwise the same task would
//     be drawn twice and dragging it would be ambiguous.
//   - A status left out of every column is allowed — hiding cancelled work is a
//     reasonable thing to want — but the reader is told how many tasks that
//     hides. A board that silently omits work is worse than a rigid one.
type TaskBoardColumn struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Color    string   `json:"color,omitempty"`
	Statuses []string `json:"statuses"`
}

// DropStatus is the status a card takes when dragged into this column.
func (c TaskBoardColumn) DropStatus() string {
	if len(c.Statuses) == 0 {
		return ""
	}
	return c.Statuses[0]
}

// TaskBoardSettings is one workspace's board.
type TaskBoardSettings struct {
	WorkspaceID string            `json:"workspace_id"`
	Version     int64             `json:"version"`
	Columns     []TaskBoardColumn `json:"columns"`
	UpdatedAt   string            `json:"updated_at,omitempty"`
	UpdatedBy   string            `json:"updated_by,omitempty"`
	// Default reports that no layout is stored and these are the shipped
	// columns, so a client can show "customise" rather than "edit".
	Default bool `json:"default"`
}

// UncoveredStatuses lists canonical statuses no column collects, in vocabulary
// order. The board uses it to say exactly what it is not showing.
func (s TaskBoardSettings) UncoveredStatuses() []string {
	covered := map[string]bool{}
	for _, column := range s.Columns {
		for _, status := range column.Statuses {
			covered[status] = true
		}
	}
	var missing []string
	for _, status := range TaskStatuses {
		if !covered[status] {
			missing = append(missing, status)
		}
	}
	return missing
}

const (
	taskBoardMaxColumns    = 12
	taskBoardMaxLabelRunes = 40
)

// ErrInvalidTaskBoard is returned for a layout the board cannot render.
var ErrInvalidTaskBoard = errors.New("invalid task board")

// DefaultTaskBoardColumns is the layout a workspace gets before it customises
// anything.
//
// Blocked is its own column. The previous hardcoded board folded it into
// "Done", which told anyone scanning the board that stuck work had finished.
func DefaultTaskBoardColumns() []TaskBoardColumn {
	return []TaskBoardColumn{
		{ID: "todo", Label: "To Do", Color: "blue", Statuses: []string{TaskStatusBacklog, TaskStatusTodo}},
		{ID: "in_progress", Label: "In Progress", Color: "yellow", Statuses: []string{TaskStatusInProgress, TaskStatusInReview}},
		{ID: "blocked", Label: "Blocked", Color: "red", Statuses: []string{TaskStatusBlocked}},
		{ID: "done", Label: "Done", Color: "green", Statuses: []string{TaskStatusDone, TaskStatusCancelled}},
	}
}

// DefaultTaskBoardSettings is the layout for a workspace with no stored row.
func DefaultTaskBoardSettings(workspaceID string) *TaskBoardSettings {
	return &TaskBoardSettings{
		WorkspaceID: workspaceID,
		Version:     0,
		Columns:     DefaultTaskBoardColumns(),
		Default:     true,
	}
}

// ValidateTaskBoardColumns normalizes and checks a submitted layout, returning
// the cleaned columns. Errors name the offending column so the editor can point
// at it.
func ValidateTaskBoardColumns(columns []TaskBoardColumn) ([]TaskBoardColumn, error) {
	if len(columns) == 0 {
		return nil, fmt.Errorf("%w: a board needs at least one column", ErrInvalidTaskBoard)
	}
	if len(columns) > taskBoardMaxColumns {
		return nil, fmt.Errorf("%w: %d columns exceeds the maximum of %d", ErrInvalidTaskBoard, len(columns), taskBoardMaxColumns)
	}

	cleaned := make([]TaskBoardColumn, 0, len(columns))
	seenID := map[string]bool{}
	claimedBy := map[string]string{}

	for i, column := range columns {
		id := strings.TrimSpace(column.ID)
		label := strings.TrimSpace(column.Label)
		if label == "" {
			return nil, fmt.Errorf("%w: column %d has no label", ErrInvalidTaskBoard, i+1)
		}
		if len([]rune(label)) > taskBoardMaxLabelRunes {
			return nil, fmt.Errorf("%w: label %q is longer than %d characters", ErrInvalidTaskBoard, label, taskBoardMaxLabelRunes)
		}
		if id == "" {
			id = slugifyBoardLabel(label, i)
		}
		if seenID[id] {
			return nil, fmt.Errorf("%w: duplicate column id %q", ErrInvalidTaskBoard, id)
		}
		seenID[id] = true

		if len(column.Statuses) == 0 {
			return nil, fmt.Errorf("%w: column %q collects no statuses, so nothing could ever appear in it", ErrInvalidTaskBoard, label)
		}
		statuses := make([]string, 0, len(column.Statuses))
		for _, raw := range column.Statuses {
			status, err := ParseTaskStatus(raw)
			if err != nil {
				return nil, fmt.Errorf("%w: column %q: %s", ErrInvalidTaskBoard, label, err)
			}
			if owner, taken := claimedBy[status]; taken {
				if owner == label {
					continue // Same column listed it twice; harmless, drop the repeat.
				}
				return nil, fmt.Errorf("%w: status %q is in both %q and %q; a task can only be in one column", ErrInvalidTaskBoard, status, owner, label)
			}
			claimedBy[status] = label
			statuses = append(statuses, status)
		}

		cleaned = append(cleaned, TaskBoardColumn{
			ID:       id,
			Label:    label,
			Color:    strings.TrimSpace(column.Color),
			Statuses: statuses,
		})
	}
	return cleaned, nil
}

// slugifyBoardLabel derives a stable id from a label so a client may omit it.
func slugifyBoardLabel(label string, index int) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(label) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return fmt.Sprintf("column-%d", index+1)
	}
	return slug
}

// ErrTaskBoardConflict reports a concurrent edit.
var ErrTaskBoardConflict = errors.New("task board changed since it was read")

// TaskBoardStorer persists one board per workspace.
type TaskBoardStorer interface {
	// GetTaskBoard returns the stored layout, or the shipped default with
	// Default set when the workspace has never customised its board.
	GetTaskBoard(ctx context.Context) (*TaskBoardSettings, error)
	// SaveTaskBoard writes a layout. expectedVersion must match the stored
	// version (0 when none is stored) or ErrTaskBoardConflict is returned.
	SaveTaskBoard(ctx context.Context, columns []TaskBoardColumn, expectedVersion int64, updatedBy string) (*TaskBoardSettings, error)
	// ResetTaskBoard drops the stored layout, returning the workspace to the
	// shipped default.
	ResetTaskBoard(ctx context.Context) error
}
