package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/container"
)

func developerContainerScope(space *service.DeveloperSpace) string {
	return "developer:" + space.WorkspaceID + ":" + space.OwnerUserID + ":" + space.ID
}

func developerContainerConfig(space *service.DeveloperSpace, home developerHome) container.Config {
	image := strings.TrimSpace(space.Image)
	if image == "" {
		image = service.DefaultDeveloperImage
	}
	cpu := strings.TrimSpace(space.CPULimit)
	if cpu == "" {
		cpu = "2"
	}
	memory := strings.TrimSpace(space.MemoryLimit)
	if memory == "" {
		memory = "4g"
	}
	diskLimit := space.DiskLimitBytes
	if diskLimit <= 0 {
		diskLimit = 20 << 30
	}
	cfg := container.Config{
		Enabled: true, Image: image, CPU: cpu, Memory: memory,
		Network: true, PersistentVolume: true, PreferRootless: true, KeepAlive: true, RetainWhenIdle: true, DiskLimitBytes: diskLimit, PidsLimit: 256,
		CapAdd: container.PackageManagerCapabilities,
	}
	if home.Enabled {
		cfg.HomeScope, cfg.HomePath = developerHomeScope(space.OwnerUserID), home.path()
	}
	return cfg
}

func developerCommandFailure(operation, stderr string, err error) error {
	detail := strings.TrimSpace(stderr)
	if err != nil && detail != "" {
		return fmt.Errorf("%s: %s: %w", operation, detail, err)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	if detail != "" {
		return fmt.Errorf("%s: %w", operation, errors.New(detail))
	}
	return fmt.Errorf("%s failed", operation)
}

func developerRemoteHost(remote string) string {
	if strings.HasPrefix(remote, "git@") {
		host, _, _ := strings.Cut(strings.TrimPrefix(remote, "git@"), ":")
		return host
	}
	parsed, err := url.Parse(remote)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func developerRemoteAllowed(ctx context.Context, remote string) error {
	host := developerRemoteHost(remote)
	if host == "" || strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return errors.New("repository remote host is not allowed")
	}
	addresses, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("resolve repository remote host: %w", err)
	}
	for _, address := range addresses {
		if address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsUnspecified() {
			return errors.New("repository remote resolves to a private or local address")
		}
	}
	return nil
}

// developerRuntimeHandle is everything one request needs to act inside the
// caller's space container.
type developerRuntimeHandle struct {
	store service.DeveloperSpaceStorer
	space *service.DeveloperSpace
	cfg   container.Config
	scope string
}

// developerRuntime rebinds the live execution identity, checks execution.run,
// and resolves the caller's own space. There is no space ID in the URL: a
// caller can only ever reach the space owned by its principal.
func (s *Server) developerRuntime(w http.ResponseWriter, r *http.Request) (*developerRuntimeHandle, bool) {
	return s.developerRuntimeAdmission(w, r, false)
}

func (s *Server) developerRuntimeAdmission(w http.ResponseWriter, r *http.Request, control bool) (*developerRuntimeHandle, bool) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return nil, false
	}
	space, err := store.EnsureDeveloperSpace(r.Context())
	if err != nil {
		developerSpaceError(w, err)
		return nil, false
	}
	if space.ExecutionSuspended && !control {
		httpResponse(w, "space stopped; use Start explicitly before accessing its runtime", http.StatusConflict)
		return nil, false
	}
	bound, err := s.bindRuntimePrincipal(r.Context(), "developer_space")
	if err != nil {
		fileAccessError(w, err)
		return nil, false
	}
	if err := service.CheckExecution(bound, service.ExecutionAction{Kind: "resource", Name: "execution.run", ResourceID: space.ID}); err != nil {
		fileAccessError(w, err)
		return nil, false
	}
	*r = *r.WithContext(bound)
	if s.containerManager == nil {
		httpResponse(w, "container runtime unavailable", http.StatusServiceUnavailable)
		return nil, false
	}
	home, err := s.loadDeveloperHome(r.Context(), space.OwnerUserID)
	if err != nil {
		httpResponse(w, "home settings unavailable", http.StatusServiceUnavailable)
		return nil, false
	}
	return &developerRuntimeHandle{store: store, space: space, cfg: developerContainerConfig(space, home), scope: developerContainerScope(space)}, true
}

func (h *developerRuntimeHandle) exec(ctx context.Context, s *Server, workDir string, command string, args ...string) (string, string, int, error) {
	return s.containerManager.ExecArgs(ctx, h.scope, h.cfg, workDir, nil, command, args...)
}

// GetDeveloperSpaceAPI returns (creating on first use) the caller's space.
func (s *Server) GetDeveloperSpaceAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	space, err := store.EnsureDeveloperSpace(r.Context())
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, s.developerSpaceResponse(space), http.StatusOK)
}

// UpdateDeveloperSpaceAPI edits profiles and resource limits. The owner,
// workspace and identity are taken from the stored row, never the body.
func (s *Server) UpdateDeveloperSpaceAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	current, err := store.EnsureDeveloperSpace(r.Context())
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
	req.ID, req.WorkspaceID, req.OwnerUserID, req.Name = current.ID, current.WorkspaceID, current.OwnerUserID, current.Name
	if err := req.Validate(); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	record, err := store.UpdateDeveloperSpace(r.Context(), req)
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, s.developerSpaceResponse(record), http.StatusOK)
}

// ResetDeveloperSpaceAPI permanently deletes the space, its sessions and its
// volume. The next visit provisions a fresh, empty space.
func (s *Server) ResetDeveloperSpaceAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	var req struct {
		Confirm bool `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.Confirm {
		httpResponse(w, "resetting a space deletes all of its files and sessions; send {\"confirm\": true}", http.StatusBadRequest)
		return
	}
	space, err := store.EnsureDeveloperSpace(r.Context())
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	sessions, err := store.ListDeveloperSessions(r.Context(), space.ID)
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	finish, ok := s.developerSpaceControl(w, r, space.ID, "reset")
	if !ok {
		return
	}
	defer finish(true)
	// Re-read after closing admission: another session/run may have appeared
	// between the original metadata read and acquiring control.
	sessions, err = store.ListDeveloperSessions(r.Context(), space.ID)
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	for _, session := range sessions {
		s.cancelDeveloperStreams(&session)
		if value, ok := s.activeDeveloperSessions.Load(session.ID); ok {
			value.(*activeDeveloperSession).cancel()
		}
	}
	// Cancellation is a request, not proof of termination. Keep the space and
	// volume until every owner has released its receipt, including expired ones.
	if runs, ok := s.store.(service.DeveloperRunStorer); ok {
		for _, session := range sessions {
			run, err := runs.GetDeveloperRun(r.Context(), session.ID)
			if err != nil {
				developerSpaceError(w, err)
				return
			}
			if run != nil || session.Status == service.DeveloperSessionRunning {
				httpResponse(w, "space suspended; wait for active runs to finish before retrying Reset. An interrupted owner requires operator verification", http.StatusConflict)
				return
			}
		}
	}
	if s.containerManager == nil {
		httpResponse(w, "container runtime unavailable; space retained", http.StatusServiceUnavailable)
		return
	}
	if err := s.containerManager.RemoveScope(r.Context(), developerContainerScope(space)); err != nil {
		httpResponse(w, fmt.Sprintf("could not reset the runtime; space retained: %v", err), http.StatusServiceUnavailable)
		return
	}
	if err := store.DeleteDeveloperSpace(r.Context(), space.ID); err != nil {
		developerSpaceError(w, err)
		return
	}
	for _, session := range sessions {
		s.deleteDeveloperSnapshotObjects(r.Context(), session)
	}
	response := map[string]any{"deleted": space.ID}
	httpResponseJSON(w, response, http.StatusOK)
}

func (s *Server) StartDeveloperSpaceAPI(w http.ResponseWriter, r *http.Request) {
	h, ok := s.developerRuntimeAdmission(w, r, true)
	if !ok {
		return
	}
	finish, ok := s.developerSpaceControl(w, r, h.space.ID, "start")
	if !ok {
		return
	}
	released := false
	defer func() {
		if !released {
			_ = finish(true)
		}
	}()
	if _, err := s.containerManager.EnsureContainer(r.Context(), h.scope, h.cfg); err != nil {
		_, _ = h.store.SetDeveloperSpaceRuntime(r.Context(), h.space.ID, service.DeveloperSpaceError, err.Error())
		httpResponse(w, fmt.Sprintf("could not start the space: %v", err), http.StatusServiceUnavailable)
		return
	}
	updated, err := h.store.SetDeveloperSpaceRuntime(r.Context(), h.space.ID, service.DeveloperSpaceReady, "")
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	if err := finish(false); err != nil {
		developerSpaceError(w, err)
		return
	}
	released = true
	updated.ExecutionSuspended = false
	updated.ActiveControlID = ""
	httpResponseJSON(w, s.developerSpaceResponse(updated), http.StatusOK)
}

func (s *Server) StopDeveloperSpaceAPI(w http.ResponseWriter, r *http.Request) {
	h, ok := s.developerRuntimeAdmission(w, r, true)
	if !ok {
		return
	}
	finish, ok := s.developerSpaceControl(w, r, h.space.ID, "stop")
	if !ok {
		return
	}
	defer finish(true)
	sessions, err := h.store.ListDeveloperSessions(r.Context(), h.space.ID)
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	for _, session := range sessions {
		s.cancelDeveloperStreams(&session)
		if value, ok := s.activeDeveloperSessions.Load(session.ID); ok {
			value.(*activeDeveloperSession).cancel()
		}
	}
	if err := s.containerManager.StopContainer(r.Context(), h.scope); err != nil {
		httpResponse(w, fmt.Sprintf("could not stop the space: %v", err), http.StatusInternalServerError)
		return
	}
	updated, err := h.store.SetDeveloperSpaceRuntime(r.Context(), h.space.ID, service.DeveloperSpaceStopped, "")
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	if err := finish(true); err != nil {
		developerSpaceError(w, err)
		return
	}
	updated.ActiveControlID = ""
	httpResponseJSON(w, s.developerSpaceResponse(updated), http.StatusOK)
}

func (s *Server) developerSpaceControl(w http.ResponseWriter, r *http.Request, id, operation string) (func(bool) error, bool) {
	store, ok := s.store.(service.DeveloperSpaceControlStorer)
	if !ok {
		httpResponse(w, "durable space admission store unavailable", http.StatusServiceUnavailable)
		return nil, false
	}
	controlID := ulid.Make().String()
	if err := store.AcquireDeveloperSpaceControl(r.Context(), id, controlID, operation); err != nil {
		developerSpaceError(w, err)
		return nil, false
	}
	*r = *r.WithContext(service.ContextWithDeveloperSpaceControl(r.Context(), controlID))
	return func(suspended bool) error {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
		defer cancel()
		if err := store.ReleaseDeveloperSpaceControl(ctx, id, controlID, suspended); err != nil {
			slog.Error("developer space control release failed", "space_id", id, "operation", operation, "error", err.Error())
			return err
		}
		return nil
	}, true
}
