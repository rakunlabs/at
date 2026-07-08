package server

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// llmAuditJanitorInterval is how often the LLM audit janitor sweeps. Once
// per hour is plenty: retention is measured in days.
const llmAuditJanitorInterval = 1 * time.Hour

// startLLMAuditJanitor starts a background goroutine that periodically
// removes llm_calls rows (and their spilled body files) older than
// service.LLMCallRetention. No-op when no store is wired.
func (s *Server) startLLMAuditJanitor(ctx context.Context) {
	if s.llmCallStore == nil {
		return
	}

	go func() {
		// Run once shortly after start so a fresh process reclaims stale
		// audit data from a previous run.
		s.sweepLLMAuditOnce(ctx)

		ticker := time.NewTicker(llmAuditJanitorInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sweepLLMAuditOnce(ctx)
			}
		}
	}()
}

// sweepLLMAuditOnce deletes old DB rows and old spill files. Exposed
// (package-internal) so tests can drive it.
func (s *Server) sweepLLMAuditOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	cutoff := time.Now().UTC().Add(-service.LLMCallRetention)
	cutoffStr := cutoff.Format(time.RFC3339)

	if s.llmCallStore != nil {
		if n, err := s.llmCallStore.DeleteLLMCallsBefore(ctx, cutoffStr); err != nil {
			slog.Debug("llm_audit_janitor: delete rows failed", "error", err.Error())
		} else if n > 0 {
			slog.Info("llm_audit_janitor: swept rows", "removed", n, "cutoff", cutoffStr)
		}
	}

	// Sweep spilled body dirs (<workspace>/.at-llm-audit/<yyyy-mm-dd>/) by
	// mtime. The per-day layout means whole directories age out together.
	root := s.llmAuditRoot()
	if root == "" {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Debug("llm_audit_janitor: read spill root failed", "root", root, "error", err.Error())
		}
		return
	}
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, iErr := entry.Info()
		if iErr != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		full := filepath.Join(root, entry.Name())
		if rmErr := os.RemoveAll(full); rmErr != nil {
			slog.Debug("llm_audit_janitor: RemoveAll failed", "path", full, "error", rmErr.Error())
			continue
		}
		removed++
	}
	if removed > 0 {
		slog.Info("llm_audit_janitor: swept spill dirs", "removed", removed, "root", root)
	}
}
