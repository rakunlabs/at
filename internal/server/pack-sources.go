package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// ─── API Handlers ───

// ListPackSourcesAPI handles GET /api/v1/pack-sources.
func (s *Server) ListPackSourcesAPI(w http.ResponseWriter, r *http.Request) {
	if s.packSourceStore == nil {
		httpResponseJSON(w, []any{}, http.StatusOK)
		return
	}

	result, err := s.packSourceStore.ListPackSources(r.Context(), nil)
	if err != nil {
		slog.Error("list pack sources failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list pack sources: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, result, http.StatusOK)
}

// CreatePackSourceAPI handles POST /api/v1/pack-sources.
// Registers a Git repo and triggers an initial clone.
func (s *Server) CreatePackSourceAPI(w http.ResponseWriter, r *http.Request) {
	if s.packSourceStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Name   string `json:"name"`
		URL    string `json:"url"`
		Branch string `json:"branch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}
	if req.URL == "" {
		httpResponse(w, "url is required", http.StatusBadRequest)
		return
	}
	if req.Branch == "" {
		req.Branch = "main"
	}
	if req.Name == "" {
		// Auto-detect name from URL.
		req.Name = repoNameFromURL(req.URL)
	}

	ps := service.PackSource{
		Name:   req.Name,
		URL:    req.URL,
		Branch: req.Branch,
		Status: "pending",
	}

	record, err := s.packSourceStore.CreatePackSource(r.Context(), ps)
	if err != nil {
		slog.Error("create pack source failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to create pack source: %v", err), http.StatusInternalServerError)
		return
	}

	// Clone in background, then update status.
	go s.syncPackSourceAsync(record.ID)

	httpResponseJSON(w, record, http.StatusCreated)
}

// DeletePackSourceAPI handles DELETE /api/v1/pack-sources/{id}.
func (s *Server) DeletePackSourceAPI(w http.ResponseWriter, r *http.Request) {
	if s.packSourceStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")

	// Remove cloned directory.
	repoDir := s.repoDir(id)
	if repoDir != "" {
		os.RemoveAll(repoDir)
	}

	if err := s.packSourceStore.DeletePackSource(r.Context(), id); err != nil {
		slog.Error("delete pack source failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete pack source: %v", err), http.StatusInternalServerError)
		return
	}

	// Reload packs.
	s.reloadPacks()

	httpResponse(w, "deleted", http.StatusOK)
}

// SyncPackSourceAPI handles POST /api/v1/pack-sources/{id}/sync.
func (s *Server) SyncPackSourceAPI(w http.ResponseWriter, r *http.Request) {
	if s.packSourceStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")

	ps, err := s.packSourceStore.GetPackSource(r.Context(), id)
	if err != nil {
		slog.Error("get pack source failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get pack source: %v", err), http.StatusInternalServerError)
		return
	}
	if ps == nil {
		httpResponse(w, fmt.Sprintf("pack source %q not found", id), http.StatusNotFound)
		return
	}

	// Sync in background.
	go s.syncPackSourceAsync(id)

	httpResponseJSON(w, ps, http.StatusOK)
}

// ─── Git Operations ───

// syncPackSourceAsync clones or pulls a pack source repo and updates its status.
func (s *Server) syncPackSourceAsync(id string) {
	ctx := s.ctx
	if ctx == nil {
		return
	}

	ps, err := s.packSourceStore.GetPackSource(ctx, id)
	if err != nil || ps == nil {
		return
	}

	// Update status to syncing.
	ps.Status = "syncing"
	s.packSourceStore.UpdatePackSource(ctx, id, *ps)

	repoDir := s.repoDir(id)
	if repoDir == "" {
		ps.Status = "error"
		ps.Error = "packs directory not configured"
		s.packSourceStore.UpdatePackSource(ctx, id, *ps)
		return
	}

	var syncErr error
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); os.IsNotExist(err) {
		// Initial clone.
		syncErr = gitClone(ps.URL, ps.Branch, repoDir)
	} else {
		// Pull latest.
		syncErr = gitPull(ps.Branch, repoDir)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if syncErr != nil {
		ps.Status = "error"
		ps.Error = syncErr.Error()
		ps.LastSync = now
		slog.Error("sync pack source failed", "id", id, "name", ps.Name, "error", syncErr)
	} else {
		ps.Status = "synced"
		ps.Error = ""
		ps.LastSync = now
		slog.Info("pack source synced", "id", id, "name", ps.Name)
	}

	s.packSourceStore.UpdatePackSource(ctx, id, *ps)

	// Reload all packs.
	s.reloadPacks()
}

// gitClone performs a shallow single-branch clone.
func gitClone(url, branch, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}

	cmd := exec.Command("git", "clone", "--depth", "1", "--single-branch", "--branch", branch, url, dest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return nil
}

// gitPull fetches and resets to latest on the branch.
func gitPull(branch, repoDir string) error {
	fetch := exec.Command("git", "-C", repoDir, "fetch", "origin", branch)
	if out, err := fetch.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch: %s: %w", strings.TrimSpace(string(out)), err)
	}

	reset := exec.Command("git", "-C", repoDir, "reset", "--hard", "origin/"+branch)
	if out, err := reset.CombinedOutput(); err != nil {
		return fmt.Errorf("git reset: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return nil
}

// ─── Helpers ───

// repoDir returns the local directory for a pack source clone.
func (s *Server) repoDir(sourceID string) string {
	packsDir := s.getPacksDir()
	if packsDir == "" {
		return ""
	}
	return filepath.Join(packsDir, "_repos", sourceID)
}

// repoNameFromURL extracts a human-readable name from a Git URL.
func repoNameFromURL(url string) string {
	// https://github.com/rakunlabs/arpa -> arpa
	// https://github.com/rakunlabs/arpa.git -> arpa
	url = strings.TrimSuffix(url, ".git")
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return "unknown"
}
