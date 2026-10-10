package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/container"
)

type systemSettingsTestStore struct {
	saved *service.SystemSettings
	err   error
}

func (f *systemSettingsTestStore) GetSystemSettings(context.Context) (*service.SystemSettings, error) {
	return f.saved, f.err
}
func (f *systemSettingsTestStore) SaveSystemSettings(_ context.Context, s service.SystemSettings) (*service.SystemSettings, error) {
	if f.err != nil {
		return nil, f.err
	}
	s.Version++
	f.saved = &s
	return f.saved, nil
}

func TestSystemSettingsAPIImmediateApplyAndConflicts(t *testing.T) {
	initial := service.DefaultSystemSettings()
	store := &systemSettingsTestStore{}
	s := &Server{systemSettings: &initial, systemSettingsStore: store, containerManager: container.New(), config: config.Server{BasePath: "/at"}}
	put := func(settings service.SystemSettings, confirm bool) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(map[string]any{"settings": settings, "confirm_stop": confirm, "single_replica": confirm})
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		s.SystemSettingsAPI(w, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(string(body))))
		return w
	}
	next := initial
	next.Name = "My server"
	next.ExternalURL = "https://example.com"
	next.WorkspaceTTLHours = -1
	if w := put(next, false); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if s.activeSystemSettings().Name != next.Name || s.workspaceRetention() >= 0 || s.publicBaseURL(httptest.NewRequest("GET", "/", nil)) != "https://example.com/at" {
		t.Fatal("settings not applied immediately")
	}
	if w := put(next, false); w.Code != http.StatusConflict {
		t.Fatalf("stale save: %d", w.Code)
	}
	next = *store.saved
	s.containerManager.Suspend(errors.New("backend failed"))
	if w := put(next, false); w.Code != http.StatusBadRequest {
		t.Fatalf("recovery without confirmation: %d", w.Code)
	}
	if w := put(next, true); w.Code != http.StatusOK {
		t.Fatalf("recovery: %d %s", w.Code, w.Body.String())
	}
	if s.containerManager.RuntimeError() != nil {
		t.Fatal("recovery did not reopen admission")
	}
}

func TestInitialSystemSettingsAcceptsLegacyRetention(t *testing.T) {
	for in, want := range map[int]int{-24: -1, 100000: 87600, 48: 48} {
		s := initialSystemSettings(config.Server{Workspace: &config.Workspace{TTLHours: in}})
		if s.WorkspaceTTLHours != want {
			t.Fatalf("%d -> %d, want %d", in, s.WorkspaceTTLHours, want)
		}
		if err := s.Normalize(); err != nil {
			t.Fatalf("legacy retention %d cannot be saved: %v", in, err)
		}
	}
}

func TestLegacyHeadersCannotAdmitAdministrator(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-User", "admin")
	r.Header.Set("Authorization", "Bearer old-operator-token")
	if s.getUserEmail(r) != "" {
		t.Fatal("header accepted as identity")
	}
	w := httptest.NewRecorder()
	s.adminAuthMiddleware()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("missing authentication admitted") })).ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("missing auth: %d", w.Code)
	}
}
