package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

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
	case errors.Is(err, service.ErrDeveloperSessionBusy):
		httpResponse(w, err.Error(), http.StatusConflict)
	case errors.Is(err, service.ErrAccessDenied):
		httpResponse(w, "developer space access denied", http.StatusForbidden)
	default:
		httpResponse(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) ListDeveloperSessionsAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	space, err := store.EnsureDeveloperSpace(r.Context())
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	records, err := store.ListDeveloperSessions(r.Context(), space.ID)
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
	var req struct {
		Title       string `json:"title"`
		ProjectPath string `json:"project_path"`
		Mode        string `json:"mode"`
		AgentID     string `json:"agent_id"`
		Provider    string `json:"provider"`
		Model       string `json:"model"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	space, err := store.EnsureDeveloperSpace(r.Context())
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	if req.Mode == "" {
		req.Mode = service.DeveloperModeBuild
	}
	if req.AgentID = strings.TrimSpace(req.AgentID); req.AgentID != "" {
		agent, ok := s.developerSessionAgent(w, r, req.AgentID)
		if !ok {
			return
		}
		// The agent's model is the starting choice; an explicit one wins.
		if strings.TrimSpace(req.Provider) == "" {
			req.Provider, req.Model = agent.Config.Provider, agent.Config.Model
		}
	}
	session := service.DeveloperSession{SpaceID: space.ID, Title: req.Title, ProjectPath: req.ProjectPath, Mode: req.Mode, AgentID: req.AgentID, Provider: req.Provider, Model: req.Model}
	if err := session.Validate(); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	record, err := store.CreateDeveloperSession(r.Context(), session)
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, record, http.StatusCreated)
}

// developerSessionAgent resolves an agent the caller may see and run. The
// store's visibility scope already hides other accounts' personal agents, so
// a foreign ID answers exactly like an unknown one.
func (s *Server) developerSessionAgent(w http.ResponseWriter, r *http.Request, id string) (*service.Agent, bool) {
	if s.agentStore == nil {
		httpResponse(w, "agent store not configured", http.StatusServiceUnavailable)
		return nil, false
	}
	agent, err := s.agentStore.GetAgent(r.Context(), id)
	if err != nil {
		httpResponse(w, fmt.Sprintf("get agent: %v", err), http.StatusInternalServerError)
		return nil, false
	}
	if agent == nil {
		httpResponse(w, "agent not found", http.StatusBadRequest)
		return nil, false
	}
	return agent, true
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

// UpdateDeveloperSessionAPI renames a session and/or changes the profile and
// model used by its next run.
func (s *Server) UpdateDeveloperSessionAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	var req struct {
		Title    *string `json:"title"`
		Mode     *string `json:"mode"`
		AgentID  *string `json:"agent_id"`
		Provider *string `json:"provider"`
		Model    *string `json:"model"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	record, err := store.GetDeveloperSession(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	if req.Title != nil {
		if record, err = store.RenameDeveloperSession(r.Context(), record.ID, *req.Title); err != nil {
			developerSpaceError(w, err)
			return
		}
	}
	if req.Mode != nil || req.AgentID != nil || req.Provider != nil || req.Model != nil {
		settings := service.DeveloperSessionSettings{Mode: record.Mode, AgentID: record.AgentID, Provider: record.Provider, Model: record.Model}
		if req.Mode != nil {
			settings.Mode = *req.Mode
		}
		if req.AgentID != nil {
			settings.AgentID = strings.TrimSpace(*req.AgentID)
			if settings.AgentID != "" && settings.AgentID != record.AgentID {
				agent, ok := s.developerSessionAgent(w, r, settings.AgentID)
				if !ok {
					return
				}
				// Switching agents adopts the new agent's model unless the
				// same request picks one explicitly.
				if req.Provider == nil && agent.Config.Provider != "" {
					settings.Provider, settings.Model = agent.Config.Provider, agent.Config.Model
				}
			}
		}
		if req.Provider != nil {
			settings.Provider = *req.Provider
		}
		if req.Model != nil {
			settings.Model = *req.Model
		}
		if !service.ValidDeveloperMode(settings.Mode) || strings.TrimSpace(settings.Provider) == "" {
			httpResponse(w, "mode must be plan, build or review and provider is required", http.StatusBadRequest)
			return
		}
		if record, err = store.UpdateDeveloperSessionSettings(r.Context(), record.ID, settings); err != nil {
			developerSpaceError(w, err)
			return
		}
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
