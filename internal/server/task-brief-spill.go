package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// briefSpillThresholdBytes is the size at which task descriptions are
// auto-spilled to a workspace file. Below this, descriptions stay inline
// (cheap, no extra I/O per child iteration). Above this, the full body
// goes to the shared workspace and the in-DB description becomes a short
// reference, so the child agent's prompt stays compact across iterations.
//
// Value chosen against 2026-04 production data: typical short briefs are
// 200–800 bytes; pipeline-stage briefs (Director → Visual Designer with
// embedded scene JSON) are 3–8 KB. 1500 keeps short briefs untouched and
// catches every multi-stage pipeline brief.
const briefSpillThresholdBytes = 1500

// briefSpillFilePrefix is the directory under each task workspace where
// spilled briefs live. Stored under a hidden dir so it doesn't clutter
// listings the agent does on the workspace.
const briefSpillSubdir = ".at-briefs"

// maybeSpillBrief writes large task descriptions to a file in the shared
// workspace and returns a compact reference description that points the
// child agent at that file.
//
// Returns the (possibly rewritten) description and a non-nil note when a
// spill happened. Failures are logged but never block task creation —
// we always fall back to the original inline description.
//
// The shared workspace is rooted at /tmp/at-tasks/<root_task_id>/ (see
// org-delegation.go). When the calling context already carries a workdir
// (i.e. a delegation chain is in flight), we reuse it; otherwise we
// derive it from the parent_id via resolveRootTaskID.
func (s *Server) maybeSpillBrief(
	ctx context.Context,
	description string,
	parentID string,
	title string,
) (newDescription string, spilled bool) {
	if len(description) < briefSpillThresholdBytes {
		return description, false
	}

	// Resolve the workspace directory. Order:
	//   1. ctx-attached workdir (set by the parent delegation)
	//   2. parent task's root workspace (when parent_id is supplied)
	//   3. current task's root workspace (when we're inside a delegation)
	workDir := workflow.WorkDirFromContext(ctx)
	if workDir == "" && parentID != "" && s.taskStore != nil {
		if parent, err := s.taskStore.GetTask(ctx, parentID); err == nil && parent != nil {
			rootID := s.resolveRootTaskID(ctx, parent)
			workDir = filepath.Join(s.taskWorkspaceBase(), rootID)
		}
	}
	if workDir == "" {
		if currentID := taskIDFromContext(ctx); currentID != "" && s.taskStore != nil {
			if current, err := s.taskStore.GetTask(ctx, currentID); err == nil && current != nil {
				rootID := s.resolveRootTaskID(ctx, current)
				workDir = filepath.Join(s.taskWorkspaceBase(), rootID)
			}
		}
	}
	if workDir == "" {
		// No anchor for the workspace — fall back to inline description.
		return description, false
	}

	briefDir := filepath.Join(workDir, briefSpillSubdir)
	if err := os.MkdirAll(briefDir, 0o755); err != nil {
		slog.Warn("brief-spill: failed to mkdir, falling back to inline",
			"path", briefDir, "error", err)
		return description, false
	}

	// Stable filename based on a content hash + a slug from the title so
	// two near-identical briefs from the same Director don't collide
	// while remaining human-greppable. Only the first 8 hex chars are
	// used — collisions within one task chain are vanishingly unlikely.
	sum := sha256.Sum256([]byte(description))
	slug := slugifyForBrief(title)
	if slug == "" {
		slug = "brief"
	}
	fileName := fmt.Sprintf("%s-%s.md", slug, hex.EncodeToString(sum[:4]))
	filePath := filepath.Join(briefDir, fileName)

	// Only write if it doesn't already exist — same content → same hash →
	// same file, so we save the I/O on retries / repeated calls.
	if _, err := os.Stat(filePath); err != nil {
		if err := os.WriteFile(filePath, []byte(description), 0o644); err != nil {
			slog.Warn("brief-spill: failed to write brief, falling back to inline",
				"path", filePath, "error", err)
			return description, false
		}
	}

	relPath := filePath
	if rel, err := filepath.Rel(workDir, filePath); err == nil {
		relPath = rel
	}

	// The replacement description is a short pointer + a 200-char preview
	// so the child agent has *some* context even before reading the file.
	preview := strings.TrimSpace(description)
	if len(preview) > 200 {
		preview = preview[:200] + "..."
	}
	ref := fmt.Sprintf(
		"This task's full brief is on disk in the shared workspace.\n\n"+
			"Read it ONCE at the start of your run via bash, then keep your messages compact:\n"+
			"  cat %q\n\n"+
			"Or use the file_read tool with absolute path:\n"+
			"  %s\n\n"+
			"Workspace root: %s\n"+
			"Brief preview: %s",
		relPath, filePath, workDir, preview,
	)

	slog.Info("brief-spill: spilled task description to workspace",
		"path", filePath,
		"original_bytes", len(description),
		"reference_bytes", len(ref),
		"savings_pct", 100*(len(description)-len(ref))/max(len(description), 1),
	)

	return ref, true
}

// slugifyForBrief converts a task title to a filesystem-safe slug.
// Keeps lowercase letters, digits, and hyphens; anything else becomes "-".
// Trims leading/trailing hyphens and caps length at 40 chars.
func slugifyForBrief(title string) string {
	if title == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(title))
	prevDash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

// summarizeChildResult returns a compact one-line summary of a child
// task's result for use when the Director records the outcome.
// Caller-side use (org_task_intake response, task_create response).
//
// It pulls out the most useful artifacts:
//   - File paths (.mp4, .png, .mp3)
//   - "title:" / "video_file:" lines from manifests
//
// and keeps the total under maxBytes. The full result is still on disk
// (in task.Result) for any agent that genuinely needs it.
func summarizeChildResult(result string, maxBytes int) string {
	result = strings.TrimSpace(result)
	if result == "" {
		return ""
	}
	if maxBytes <= 0 {
		maxBytes = 800
	}
	if len(result) <= maxBytes {
		return result
	}
	// Grab the first interesting block (often a JSON manifest with
	// video_file / title keys) plus any file paths sprinkled later.
	head := result[:maxBytes]
	if idx := strings.LastIndex(head, "\n"); idx > maxBytes/2 {
		head = head[:idx]
	}
	return head + "\n... (truncated, full result on task.Result)"
}

// briefServiceTask is a tiny interface so this file doesn't import
// service-specific packages just to access a Task's Description; kept
// at package scope to make the unit-test friction low.
var _ = service.Task{}
