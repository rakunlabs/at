package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/blob"
)

func (s *Server) developerSpaceStore(w http.ResponseWriter) service.DeveloperSpaceStorer {
	if s.store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return nil
	}
	store, ok := s.store.(service.DeveloperSpaceStorer)
	if !ok {
		httpResponse(w, "developer spaces unavailable", http.StatusServiceUnavailable)
		return nil
	}
	return store
}

func developerSpaceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrDeveloperSpaceNotFound):
		httpResponse(w, "developer space not found", http.StatusNotFound)
	case errors.Is(err, service.ErrDeveloperSpaceConflict):
		httpResponse(w, "a developer space with this name already exists", http.StatusConflict)
	case errors.Is(err, service.ErrDeveloperSessionBusy):
		httpResponse(w, err.Error(), http.StatusConflict)
	case errors.Is(err, service.ErrAccessDenied):
		httpResponse(w, "developer space access denied", http.StatusForbidden)
	default:
		httpResponse(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) ListDeveloperSpacesAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	records, err := store.ListDeveloperSpaces(r.Context())
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, records, http.StatusOK)
}

func (s *Server) CreateDeveloperSpaceAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	var req service.DeveloperSpace
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if err := req.Validate(); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	record, err := store.CreateDeveloperSpace(r.Context(), req)
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, record, http.StatusCreated)
}

func (s *Server) GetDeveloperSpaceAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	record, err := store.GetDeveloperSpace(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, record, http.StatusOK)
}

func (s *Server) UpdateDeveloperSpaceAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	current, err := store.GetDeveloperSpace(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	var req service.DeveloperSpace
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	req.ID = current.ID
	req.WorkspaceID = current.WorkspaceID
	req.OwnerUserID = current.OwnerUserID
	if err := req.Validate(); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	record, err := store.UpdateDeveloperSpace(r.Context(), req)
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, record, http.StatusOK)
}

func (s *Server) DeleteDeveloperSpaceAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	space, err := store.GetDeveloperSpace(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	sessions, err := store.ListDeveloperSessions(r.Context(), space.ID)
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	for _, session := range sessions {
		if value, ok := s.activeDeveloperSessions.Load(session.ID); ok {
			value.(*activeDeveloperSession).cancel()
		}
	}
	if err := store.DeleteDeveloperSpace(r.Context(), space.ID); err != nil {
		developerSpaceError(w, err)
		return
	}
	for _, session := range sessions {
		s.deleteDeveloperSnapshotObjects(r.Context(), session)
	}
	response := map[string]any{"deleted": space.ID}
	if s.containerManager != nil {
		if err := s.containerManager.RemoveScope(r.Context(), developerContainerScope(space)); err != nil {
			slog.Warn("developer space deleted but runtime cleanup failed", "space_id", space.ID, "error", err.Error())
			response["cleanup_warning"] = "Developer-space records were deleted, but its container volume could not be removed."
		}
	}
	httpResponseJSON(w, response, http.StatusOK)
}

func (s *Server) ListDeveloperRepositoriesAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	records, err := store.ListDeveloperRepositories(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, records, http.StatusOK)
}

func (s *Server) CreateDeveloperRepositoryAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	var req service.DeveloperRepository
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	req.SpaceID = r.PathValue("id")
	if err := req.Validate(); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	record, err := store.CreateDeveloperRepository(r.Context(), req)
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, record, http.StatusCreated)
}

func (s *Server) GetDeveloperRepositoryAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	record, err := store.GetDeveloperRepository(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, record, http.StatusOK)
}

func (s *Server) DeleteDeveloperRepositoryAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	repository, err := store.GetDeveloperRepository(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	sessions, _ := store.ListDeveloperSessions(r.Context(), repository.SpaceID)
	for _, session := range sessions {
		if session.RepositoryID == repository.ID {
			if value, ok := s.activeDeveloperSessions.Load(session.ID); ok {
				value.(*activeDeveloperSession).cancel()
			}
		}
	}
	cleanupWarning := s.cleanupDeveloperRepositoryRuntime(r.Context(), store, repository)
	if err := store.DeleteDeveloperRepository(r.Context(), repository.ID); err != nil {
		developerSpaceError(w, err)
		return
	}
	response := map[string]any{"deleted": repository.ID}
	if cleanupWarning != "" {
		response["cleanup_warning"] = cleanupWarning
	}
	httpResponseJSON(w, response, http.StatusOK)
}

func (s *Server) cleanupDeveloperRepositoryRuntime(ctx context.Context, store service.DeveloperSpaceStorer, repository *service.DeveloperRepository) string {
	space, err := store.GetDeveloperSpace(ctx, repository.SpaceID)
	if err != nil || s.containerManager == nil {
		return "Repository metadata was deleted, but its runtime files could not be removed."
	}
	cfg, scope := developerContainerConfig(space), developerContainerScope(space)
	worktrees, _ := store.ListDeveloperWorktrees(ctx, repository.ID)
	for _, worktree := range worktrees {
		if _, stderr, code, err := s.containerManager.ExecArgs(ctx, scope, cfg, "/workspace", nil, "rm", "-rf", "--", developerWorktreePath(worktree.ID)); err != nil || code != 0 {
			slog.Warn("remove developer worktree runtime", "worktree_id", worktree.ID, "error", developerCommandFailure("remove worktree", stderr, err).Error())
		}
	}
	if _, stderr, code, err := s.containerManager.ExecArgs(ctx, scope, cfg, "/workspace", nil, "rm", "-rf", "--", path.Dir(developerRepositoryPath(repository.ID))); err != nil || code != 0 {
		slog.Warn("remove developer repository runtime", "repository_id", repository.ID, "error", developerCommandFailure("remove repository", stderr, err).Error())
		return "Repository metadata was deleted, but its runtime files could not be removed."
	}
	return ""
}

func (s *Server) ListDeveloperWorktreesAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	records, err := store.ListDeveloperWorktrees(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, records, http.StatusOK)
}

func (s *Server) CreateDeveloperWorktreeAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	var req service.DeveloperWorktree
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	req.RepositoryID = r.PathValue("id")
	if err := req.Validate(); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	record, err := store.CreateDeveloperWorktree(r.Context(), req)
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, record, http.StatusCreated)
}

func (s *Server) GetDeveloperWorktreeAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	record, err := store.GetDeveloperWorktree(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, record, http.StatusOK)
}

func (s *Server) DeleteDeveloperWorktreeAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	worktree, err := store.GetDeveloperWorktree(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	sessions, _ := store.ListDeveloperSessions(r.Context(), worktree.SpaceID)
	for _, session := range sessions {
		if session.WorktreeID == worktree.ID {
			if value, ok := s.activeDeveloperSessions.Load(session.ID); ok {
				value.(*activeDeveloperSession).cancel()
			}
		}
	}
	cleanupWarning := ""
	space, spaceErr := store.GetDeveloperSpace(r.Context(), worktree.SpaceID)
	if spaceErr == nil && s.containerManager != nil {
		cfg, scope := developerContainerConfig(space), developerContainerScope(space)
		if _, stderr, code, execErr := s.containerManager.ExecArgs(r.Context(), scope, cfg, "/workspace", nil, "rm", "-rf", "--", developerWorktreePath(worktree.ID)); execErr != nil || code != 0 {
			slog.Warn("remove developer worktree runtime", "worktree_id", worktree.ID, "error", developerCommandFailure("remove worktree", stderr, execErr).Error())
			cleanupWarning = "Worktree metadata was deleted, but its runtime files could not be removed."
		} else if repository, repoErr := store.GetDeveloperRepository(r.Context(), worktree.RepositoryID); repoErr == nil {
			if _, stderr, code, pruneErr := s.containerManager.ExecArgs(r.Context(), scope, cfg, developerRepositoryPath(repository.ID), nil, "git", "worktree", "prune"); pruneErr != nil || code != 0 {
				slog.Warn("prune developer worktree metadata", "worktree_id", worktree.ID, "error", developerCommandFailure("prune worktree", stderr, pruneErr).Error())
			}
		}
	} else {
		cleanupWarning = "Worktree metadata was deleted, but its runtime files could not be removed."
	}
	if err := store.DeleteDeveloperWorktree(r.Context(), worktree.ID); err != nil {
		developerSpaceError(w, err)
		return
	}
	response := map[string]any{"deleted": worktree.ID}
	if cleanupWarning != "" {
		response["cleanup_warning"] = cleanupWarning
	}
	httpResponseJSON(w, response, http.StatusOK)
}

func (s *Server) ListDeveloperSessionsAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	records, err := store.ListDeveloperSessions(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, records, http.StatusOK)
}

func (s *Server) CreateDeveloperSessionAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	var req service.DeveloperSession
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	req.SpaceID = r.PathValue("id")
	if err := req.Validate(); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	record, err := store.CreateDeveloperSession(r.Context(), req)
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, record, http.StatusCreated)
}

func (s *Server) GetDeveloperSessionAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	record, err := store.GetDeveloperSession(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, record, http.StatusOK)
}

func (s *Server) DeleteDeveloperSessionAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	session, err := store.GetDeveloperSession(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	if value, ok := s.activeDeveloperSessions.Load(session.ID); ok {
		value.(*activeDeveloperSession).cancel()
	}
	if err := store.DeleteDeveloperSession(r.Context(), session.ID); err != nil {
		developerSpaceError(w, err)
		return
	}
	s.deleteDeveloperSnapshotObjects(r.Context(), *session)
	httpResponseJSON(w, map[string]any{"deleted": r.PathValue("id")}, http.StatusOK)
}

func (s *Server) deleteDeveloperSnapshotObjects(ctx context.Context, session service.DeveloperSession) {
	objects, ok := s.store.(service.StorageObjectStorer)
	if !ok {
		return
	}
	prefix := "developer-sessions/" + session.ID + "/"
	records, err := objects.ListStorageObjects(ctx, session.WorkspaceID, service.StorageNamespaceSnapshots, prefix)
	if err != nil {
		slog.Warn("list developer snapshot cleanup", "session_id", session.ID, "error", err.Error())
		return
	}
	var target blob.Store
	backend := ""
	if settingsStore, ok := s.store.(service.StorageSettingsStorer); ok {
		if settings, settingsErr := settingsStore.GetStorageSettings(ctx); settingsErr == nil {
			target, _ = blob.New(*settings)
			backend = settings.Backend
		}
	}
	for _, record := range records {
		if _, err := objects.DeleteStorageObject(ctx, record.WorkspaceID, record.Namespace, record.Path); err != nil {
			slog.Warn("delete developer snapshot metadata", "session_id", session.ID, "path", record.Path, "error", err.Error())
			continue
		}
		if target != nil && record.Backend == backend {
			if err := target.Delete(ctx, record.StorageKey); err != nil {
				slog.Warn("delete developer snapshot blob", "session_id", session.ID, "key", record.StorageKey, "error", err.Error())
			}
		}
	}
}

func (s *Server) ListDeveloperSessionMessagesAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	records, err := store.ListDeveloperSessionMessages(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, records, http.StatusOK)
}

func (s *Server) ListDeveloperSessionSnapshotsAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	records, err := store.ListDeveloperSessionSnapshots(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, records, http.StatusOK)
}

func (s *Server) GetDeveloperSessionSnapshotAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	session, err := store.GetDeveloperSession(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	snapshots, err := store.ListDeveloperSessionSnapshots(r.Context(), session.ID)
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	var snapshot *service.DeveloperSessionSnapshot
	for index := range snapshots {
		if snapshots[index].ID == r.PathValue("snapshotID") {
			snapshot = &snapshots[index]
			break
		}
	}
	if snapshot == nil || snapshot.StorageObjectID == "" {
		httpResponse(w, "developer session snapshot not found", http.StatusNotFound)
		return
	}
	objects, ok := s.store.(service.StorageObjectStorer)
	if !ok {
		httpResponse(w, "storage object catalog unavailable", http.StatusServiceUnavailable)
		return
	}
	logicalPath := fmt.Sprintf("developer-sessions/%s/%06d-%s.json", session.ID, snapshot.Step, snapshot.Phase)
	object, err := objects.GetStorageObject(r.Context(), session.WorkspaceID, service.StorageNamespaceSnapshots, logicalPath)
	if err != nil || object.ID != snapshot.StorageObjectID {
		httpResponse(w, "developer session snapshot not found", http.StatusNotFound)
		return
	}
	settingsStore, ok := s.store.(service.StorageSettingsStorer)
	if !ok {
		httpResponse(w, "storage settings unavailable", http.StatusServiceUnavailable)
		return
	}
	settings, err := settingsStore.GetStorageSettings(r.Context())
	if err != nil || object.Backend != settings.Backend {
		httpResponse(w, "snapshot storage backend is unavailable", http.StatusConflict)
		return
	}
	target, err := blob.New(*settings)
	if err != nil {
		httpResponse(w, "snapshot storage backend is unavailable", http.StatusServiceUnavailable)
		return
	}
	reader, _, err := target.Get(r.Context(), object.StorageKey)
	if err != nil {
		httpResponse(w, "could not read developer session snapshot", http.StatusBadGateway)
		return
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, developerSnapshotMaxBytes+(1<<20)))
	if err != nil || int64(len(data)) != object.SizeBytes {
		httpResponse(w, "developer session snapshot is incomplete", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) GetDeveloperSessionPendingToolAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	if _, err := store.GetDeveloperSession(r.Context(), r.PathValue("id")); err != nil {
		developerSpaceError(w, err)
		return
	}
	record, err := store.GetDeveloperPendingTool(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, record, http.StatusOK)
}
