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

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/container"
)

func developerContainerScope(space *service.DeveloperSpace) string {
	return "developer:" + space.WorkspaceID + ":" + space.OwnerUserID + ":" + space.ID
}

func developerContainerConfig(space *service.DeveloperSpace) container.Config {
	image := strings.TrimSpace(space.Image)
	if image == "" {
		image = "at-agent-runtime:latest"
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
	return container.Config{
		Enabled: true, Image: image, CPU: cpu, Memory: memory,
		Network: true, PersistentVolume: true, RequireRootless: true, DiskLimitBytes: diskLimit, PidsLimit: 256,
	}
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
	store := s.developerSpaceStore(w)
	if store == nil {
		return nil, false
	}
	space, err := store.EnsureDeveloperSpace(r.Context())
	if err != nil {
		developerSpaceError(w, err)
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
	return &developerRuntimeHandle{store: store, space: space, cfg: developerContainerConfig(space), scope: developerContainerScope(space)}, true
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
	httpResponseJSON(w, space, http.StatusOK)
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
	httpResponseJSON(w, record, http.StatusOK)
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
			response["cleanup_warning"] = "The space records were deleted, but its container volume could not be removed."
		}
	}
	httpResponseJSON(w, response, http.StatusOK)
}

func (s *Server) StartDeveloperSpaceAPI(w http.ResponseWriter, r *http.Request) {
	h, ok := s.developerRuntime(w, r)
	if !ok {
		return
	}
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
	httpResponseJSON(w, updated, http.StatusOK)
}

func (s *Server) StopDeveloperSpaceAPI(w http.ResponseWriter, r *http.Request) {
	h, ok := s.developerRuntime(w, r)
	if !ok {
		return
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
	httpResponseJSON(w, updated, http.StatusOK)
}
