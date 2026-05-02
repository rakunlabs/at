package loopgov

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"unicode/utf8"
)

// TruncateToolResult applies the per-tool byte cap to a tool result
// before it is appended to the LLM message history. When the result
// fits within the cap, it is returned unchanged with didTruncate=false.
// When it does not fit:
//
//   - The full payload is written to ${WorkspaceRoot}/.at-tool-output/
//     <runID>/<tool>-<seq>.txt (sequence is monotonic per runID).
//   - The returned string is the truncated head plus a marker line
//     containing the kept bytes, original bytes, and a relative path
//     reference to the dump file. UTF-8 boundaries are respected so
//     the marker never produces invalid byte sequences.
//   - A workspace write failure does NOT block the loop; the marker
//     reads "full output unavailable" instead.
//
// runID is used to namespace dumps from a single run; pass the agentic
// loop's task or run identifier (or any stable string per loop). Empty
// runID is replaced with "unknown".
//
// When the governor is disabled, the body is returned unchanged.
func (g *Governor) TruncateToolResult(runID, toolName, body string) (kept string, didTruncate bool) {
	if g.cfg.Disabled {
		return body, false
	}
	if runID == "" {
		runID = "unknown"
	}

	cap := g.toolCap(toolName)
	original := len(body)
	if original <= cap {
		return body, false
	}

	// Find a UTF-8 boundary at or before cap so we never split a
	// multi-byte rune.
	boundary := cap
	for boundary > 0 && !utf8.RuneStart(body[boundary]) {
		boundary--
	}
	head := body[:boundary]

	// Write the full payload to a dump file. Failures are non-fatal.
	ref, dumpErr := g.dumpToolOutput(runID, toolName, body)

	var marker string
	if dumpErr != nil {
		marker = fmt.Sprintf("\n\n[truncated: %d of %d bytes shown; full output unavailable: %s]",
			boundary, original, dumpErr.Error())
		slog.Warn("loopgov.tool_dump_failed",
			"tool", toolName,
			"run_id", runID,
			"original_bytes", original,
			"kept_bytes", boundary,
			"error", dumpErr.Error())
	} else {
		marker = fmt.Sprintf("\n\n[truncated: %d of %d bytes shown; full output: %s]",
			boundary, original, ref)
	}

	slog.Info("loopgov.tool_truncated",
		"tool", toolName,
		"run_id", runID,
		"original_bytes", original,
		"kept_bytes", boundary,
		"ref", ref)

	return head + marker, true
}

// toolCap returns the byte cap for a tool result. Earlier revisions of
// this package supported per-tool and per-class overrides; those were
// removed because they over-truncated structured outputs from tools
// like the video-generation suite (FAL Veo / Sora / Runway) and the
// `delegate_to_*` channel. We now use a single generous global cap
// combined with the workspace dump (see TruncateToolResult), which
// gives the LLM ample inline context AND preserves the full payload
// on disk for follow-up reads.
//
// toolName is retained in the signature so callers (and the dump file
// naming logic) keep their per-tool granularity even though the cap is
// uniform.
func (g *Governor) toolCap(_ string) int {
	return g.cfg.ToolResultMaxBytes
}

// dumpToolOutput writes body to a uniquely-named file under the
// configured workspace root and returns the relative reference string
// that callers should embed in the truncation marker. If WorkspaceRoot
// is empty, no file is written; the function returns an error.
func (g *Governor) dumpToolOutput(runID, toolName, body string) (string, error) {
	if g.cfg.WorkspaceRoot == "" {
		return "", errors.New("no workspace root configured")
	}

	dir := filepath.Join(g.cfg.WorkspaceRoot, ".at-tool-output", runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}

	seq := g.nextDumpSeq(runID)
	name := fmt.Sprintf("%s-%d.txt", sanitizeForFilename(toolName), seq)
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}

	return filepath.Join(".at-tool-output", runID, name), nil
}

// nextDumpSeq returns a monotonic integer for the given runID.
func (g *Governor) nextDumpSeq(runID string) int64 {
	g.dumpSeqMu.Lock()
	c, ok := g.dumpSeq[runID]
	if !ok {
		c = &atomic.Int64{}
		g.dumpSeq[runID] = c
	}
	g.dumpSeqMu.Unlock()
	return c.Add(1)
}

// sanitizeForFilename replaces characters that are unsafe in path
// components with underscores. Tool names should already be safe but
// be defensive in case a future tool name slips a path separator in.
func sanitizeForFilename(s string) string {
	if s == "" {
		return "tool"
	}
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '/' || c == '\\' || c == ':' || c == '.' || c == ' ':
			out[i] = '_'
		default:
			out[i] = c
		}
	}
	return string(out)
}
