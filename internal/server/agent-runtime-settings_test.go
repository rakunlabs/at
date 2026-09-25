package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

type memoryAgentRuntimeSettings struct {
	settings service.AgentRuntimeSettings
}

func (m *memoryAgentRuntimeSettings) GetAgentRuntimeSettings(context.Context) (*service.AgentRuntimeSettings, error) {
	copy := m.settings
	return &copy, nil
}

func (m *memoryAgentRuntimeSettings) SaveAgentRuntimeSettings(_ context.Context, settings service.AgentRuntimeSettings) (*service.AgentRuntimeSettings, error) {
	settings.Version++
	m.settings = settings
	return &settings, nil
}

func TestAgentRuntimeSettingsAPI(t *testing.T) {
	store := &memoryAgentRuntimeSettings{settings: service.DefaultAgentRuntimeSettings()}
	s := &Server{agentRuntimeSettingsStore: store}

	w := httptest.NewRecorder()
	s.AgentRuntimeSettingsAPI(w, httptest.NewRequest(http.MethodGet, "/api/v1/settings/agent-runtime", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d: %s", w.Code, w.Body.String())
	}
	var got service.AgentRuntimeSettings
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.MaxBackgroundSubagentsPerOwner != 16 {
		t.Fatalf("GET settings = %+v, err=%v", got, err)
	}

	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/agent-runtime", strings.NewReader(`{"version":1,"max_background_subagents_per_owner":24}`))
	s.AgentRuntimeSettingsAPI(w, req)
	if w.Code != http.StatusOK || store.settings.MaxBackgroundSubagentsPerOwner != 24 {
		t.Fatalf("PUT status = %d, settings=%+v: %s", w.Code, store.settings, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/settings/agent-runtime", strings.NewReader(`{"version":2,"max_background_subagents_per_owner":129}`))
	s.AgentRuntimeSettingsAPI(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid PUT status = %d: %s", w.Code, w.Body.String())
	}
}
