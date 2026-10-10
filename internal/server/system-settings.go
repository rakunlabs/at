package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/rakunlabs/logi"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/container"
)

func initialSystemSettings(cfg config.Server) service.SystemSettings {
	s := service.DefaultSystemSettings()
	if cfg.Name != "" {
		s.Name = cfg.Name
	}
	s.ExternalURL = strings.TrimRight(cfg.ExternalURL, "/")
	// Preserve bootstrap logging until the administrator explicitly saves it.
	for _, level := range []struct {
		name  string
		level slog.Level
	}{{"debug", slog.LevelDebug}, {"info", slog.LevelInfo}, {"warn", slog.LevelWarn}, {"error", slog.LevelError}} {
		if slog.Default().Enabled(context.Background(), level.level) {
			s.LogLevel = level.name
			break
		}
	}
	if cfg.Workspace != nil && cfg.Workspace.TTLHours != 0 {
		s.WorkspaceTTLHours = cfg.Workspace.TTLHours
	}
	// Bootstrap treated every negative TTL as disabled and had no ceiling;
	// present it in the stored vocabulary so the first save is accepted.
	if s.WorkspaceTTLHours < 0 {
		s.WorkspaceTTLHours = -1
	} else if s.WorkspaceTTLHours > 87600 {
		s.WorkspaceTTLHours = 87600
	}
	return s
}

func (s *Server) activeSystemSettings() service.SystemSettings {
	s.systemMu.RLock()
	defer s.systemMu.RUnlock()
	if s.systemSettings != nil {
		return *s.systemSettings
	}
	return initialSystemSettings(s.config)
}

func (s *Server) workspaceRetention() time.Duration {
	s.systemMu.RLock()
	initialized := s.systemSettings != nil
	s.systemMu.RUnlock()
	if !initialized && s.loopGov != nil {
		return s.loopGov.Config().WorkspaceTTL
	}
	hours := s.activeSystemSettings().WorkspaceTTLHours
	if hours == 0 {
		hours = 24
	}
	return time.Duration(hours) * time.Hour
}

type systemSettingsResponse struct {
	Settings   service.SystemSettings        `json:"settings"`
	Active     service.SystemSettings        `json:"active"`
	Runtime    container.RuntimeCapabilities `json:"runtime"`
	ApplyError string                        `json:"apply_error,omitempty"`
}

func (s *Server) systemResponse(settings service.SystemSettings) systemSettingsResponse {
	out := systemSettingsResponse{Settings: settings, Active: s.activeSystemSettings()}
	if s.containerManager != nil {
		out.Runtime = s.containerManager.Capabilities()
		if err := s.containerManager.RuntimeError(); err != nil {
			out.ApplyError = err.Error()
		}
	}
	return out
}

// SystemSettingsAPI is also guarded by the administrator-only settings group.
// The store independently admits a live installation administrator.
func (s *Server) SystemSettingsAPI(w http.ResponseWriter, r *http.Request) {
	if s.systemSettingsStore == nil || s.containerManager == nil {
		httpResponse(w, "system settings store not configured", http.StatusServiceUnavailable)
		return
	}
	// Reads stay available while a transition drains (up to two minutes).
	if r.Method == http.MethodPut {
		s.systemApplyMu.Lock()
		defer s.systemApplyMu.Unlock()
	}
	current, err := s.systemSettingsStore.GetSystemSettings(r.Context())
	if err != nil {
		systemSettingsError(w, err)
		return
	}
	if current == nil {
		initial := s.activeSystemSettings()
		initial.Version = 0
		current = &initial
	}
	if r.Method == http.MethodGet {
		httpResponseJSON(w, s.systemResponse(*current), http.StatusOK)
		return
	}
	if r.Method != http.MethodPut {
		httpResponse(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Settings      service.SystemSettings `json:"settings"`
		ConfirmStop   bool                   `json:"confirm_stop"`
		SingleReplica bool                   `json:"single_replica"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpResponse(w, "invalid system settings body", http.StatusBadRequest)
		return
	}
	if err := req.Settings.Normalize(); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Settings.Version != current.Version {
		systemSettingsError(w, service.ErrSystemSettingsConflict)
		return
	}
	active := s.activeSystemSettings()
	changeBackend := !reflect.DeepEqual(active.Sandbox, req.Settings.Sandbox) || s.containerManager.RuntimeError() != nil
	var next *container.Manager
	if changeBackend {
		if !req.ConfirmStop || !req.SingleReplica {
			httpResponse(w, "confirm stopping sandbox activity and that only one AT replica is running before applying a backend change", http.StatusBadRequest)
			return
		}
		next, err = sandboxManager(&req.Settings.Sandbox)
		if err != nil {
			httpResponse(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	saved, err := s.systemSettingsStore.SaveSystemSettings(r.Context(), req.Settings)
	if err != nil {
		if next != nil {
			_ = next.Close()
		}
		systemSettingsError(w, err)
		return
	}
	if changeBackend {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		err = s.containerManager.Reconfigure(ctx, next.Driver())
		cancel()
		if err != nil {
			_ = next.Close()
			// Other settings are independent of the sandbox; apply them so
			// "active" reports exactly what this server runs.
			partial := *saved
			partial.Sandbox = active.Sandbox
			if levelErr := logi.SetLogLevel(partial.LogLevel); levelErr == nil {
				s.systemMu.Lock()
				s.systemSettings = &partial
				s.systemMu.Unlock()
			}
			out := s.systemResponse(*saved)
			out.ApplyError = fmt.Sprintf("Settings saved, but the sandbox change was not applied: %v. Other settings were applied. Sandbox execution stays suspended until a retry succeeds.", err)
			slog.Error("system settings: sandbox transition failed", "error", err.Error())
			httpResponseJSON(w, out, http.StatusConflict)
			return
		}
	}
	if err := logi.SetLogLevel(saved.LogLevel); err != nil {
		systemSettingsError(w, err)
		return
	}
	s.systemMu.Lock()
	s.systemSettings = saved
	s.systemMu.Unlock()
	slog.Info("system settings applied", "version", saved.Version, "backend", saved.Sandbox.Backend)
	httpResponseJSON(w, s.systemResponse(*saved), http.StatusOK)
}

func systemSettingsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrAccessDenied), errors.Is(err, service.ErrWorkspaceRequired):
		httpResponse(w, "installation administrator access required", http.StatusForbidden)
	case errors.Is(err, service.ErrSystemSettingsConflict):
		httpResponse(w, err.Error(), http.StatusConflict)
	default:
		httpResponse(w, "could not load or save system settings", http.StatusInternalServerError)
	}
}
