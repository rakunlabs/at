package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

func (s *Server) workspaceLifecycleStore(w http.ResponseWriter) service.WorkspaceLifecycleStorer {
	store, ok := s.store.(service.WorkspaceLifecycleStorer)
	if !ok {
		nativeError(w, 503, "workspace lifecycle store unavailable")
		return nil
	}
	return store
}

func (s *Server) GetWorkspacePreferencesAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceLifecycleStore(w)
	if store == nil {
		return
	}
	v, err := store.GetWorkspacePreferences(r.Context())
	if err != nil {
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, v, 200)
}

func (s *Server) SaveWorkspacePreferencesAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceLifecycleStore(w)
	if store == nil {
		return
	}
	var req struct {
		Mode        string `json:"mode"`
		WorkspaceID string `json:"workspace_id"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	v, err := store.SaveWorkspacePreferences(r.Context(), service.WorkspacePreferences{Mode: req.Mode, WorkspaceID: req.WorkspaceID})
	if err != nil {
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, v, 200)
}

func (s *Server) SelectWorkspaceAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceLifecycleStore(w)
	if store == nil {
		return
	}
	var req struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	if err := store.SelectWorkspace(r.Context(), req.WorkspaceID); err != nil {
		workspaceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) DeleteWorkspaceAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceLifecycleStore(w)
	if store == nil {
		return
	}
	var req struct {
		Confirmation string `json:"confirmation"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	if r.PathValue("workspace") == "legacy-default" {
		nativeError(w, 409, "the default workspace is required and cannot be deleted")
		return
	}
	deleted, err := store.DeleteWorkspace(r.Context(), req.Confirmation)
	if err != nil {
		workspaceError(w, err)
		return
	}
	// Persisted resources and execution bindings are gone before cancellation;
	// every replica's subsequent admission checks now fail closed.
	s.activeRuns.Range(func(_, value any) bool {
		run := value.(*activeRun)
		if run.WorkspaceID == deleted.WorkspaceID {
			run.Cancel()
		}
		return true
	})
	s.activeChatTurns.Range(func(_, value any) bool {
		turn := value.(*activeChatTurn)
		if turn.WorkspaceID == deleted.WorkspaceID {
			turn.Cancel()
		}
		return true
	})
	for _, id := range deleted.TaskIDs {
		if value, ok := s.activeDelegations.Load(id); ok {
			value.(*activeDelegation).Cancel()
		}
	}
	for _, id := range deleted.BotIDs {
		s.stopBot(id)
	}
	s.deleteChatMediaBlobs(r.Context(), deleted.MediaObjects)
	if s.scheduler != nil {
		if err := s.scheduler.Reload(); err != nil {
			slog.Warn("reload scheduler after workspace deletion", "error", err)
		}
	}
	result := map[string]any{"deleted": true, "workspace_id": deleted.WorkspaceID}
	if err := s.removeWorkspaceFiles(deleted.WorkspaceID); err != nil {
		slog.Error("workspace deleted but file cleanup failed", "workspace_id", deleted.WorkspaceID, "error", err)
		result["cleanup_warning"] = "Workspace records were deleted, but its execution files could not be removed. Ask the operator to clean up the workspace directory."
	}
	httpResponseJSON(w, result, 200)
}

func (s *Server) removeWorkspaceFiles(id string) error {
	if id == "legacy-default" || !filepath.IsLocal(id) || filepath.Base(id) != id || id == "." {
		return service.ErrAccessDenied
	}
	root, err := os.OpenRoot(s.taskWorkspaceBase())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	return root.RemoveAll(filepath.Join("workspaces", id))
}

type activeChatTurn struct {
	WorkspaceID string
	Cancel      context.CancelFunc
}

func (s *Server) registerChatTurn(ctx context.Context) (context.Context, func()) {
	p, _, ok := service.ExecutionFromContext(ctx)
	if !ok {
		return ctx, func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	id := ulid.Make().String()
	s.activeChatTurns.Store(id, &activeChatTurn{WorkspaceID: p.WorkspaceID, Cancel: cancel})
	return ctx, func() { s.activeChatTurns.Delete(id); cancel() }
}
