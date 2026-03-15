package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Mock AgentMemoryStorer ───

type mockAgentMemoryStore struct {
	memories []service.AgentMemory
	messages map[string]*service.AgentMemoryMessages
	deleted  []string
}

func newMockAgentMemoryStore() *mockAgentMemoryStore {
	return &mockAgentMemoryStore{
		messages: make(map[string]*service.AgentMemoryMessages),
	}
}

func (m *mockAgentMemoryStore) CreateAgentMemory(_ context.Context, mem service.AgentMemory) (*service.AgentMemory, error) {
	mem.ID = "new-mem-id"
	m.memories = append(m.memories, mem)
	return &mem, nil
}

func (m *mockAgentMemoryStore) GetAgentMemory(_ context.Context, id string) (*service.AgentMemory, error) {
	for _, mem := range m.memories {
		if mem.ID == id {
			return &mem, nil
		}
	}
	return nil, nil
}

func (m *mockAgentMemoryStore) ListAgentMemories(_ context.Context, agentID, orgID string) ([]service.AgentMemory, error) {
	var result []service.AgentMemory
	for _, mem := range m.memories {
		if mem.AgentID == agentID && mem.OrganizationID == orgID {
			result = append(result, mem)
		}
	}
	return result, nil
}

func (m *mockAgentMemoryStore) ListOrgMemories(_ context.Context, orgID string) ([]service.AgentMemory, error) {
	var result []service.AgentMemory
	for _, mem := range m.memories {
		if mem.OrganizationID == orgID {
			result = append(result, mem)
		}
	}
	return result, nil
}

func (m *mockAgentMemoryStore) SearchAgentMemories(_ context.Context, agentID, orgID, query string) ([]service.AgentMemory, error) {
	// Simple substring search for testing.
	var result []service.AgentMemory
	for _, mem := range m.memories {
		if mem.OrganizationID != orgID {
			continue
		}
		if agentID != "" && mem.AgentID != agentID {
			continue
		}
		if contains(mem.SummaryL0, query) || contains(mem.SummaryL1, query) {
			result = append(result, mem)
		}
	}
	return result, nil
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) > 0 && bytes.Contains([]byte(s), []byte(sub))
}

func (m *mockAgentMemoryStore) DeleteAgentMemory(_ context.Context, id string) error {
	m.deleted = append(m.deleted, id)
	var kept []service.AgentMemory
	for _, mem := range m.memories {
		if mem.ID != id {
			kept = append(kept, mem)
		}
	}
	m.memories = kept
	return nil
}

func (m *mockAgentMemoryStore) GetAgentMemoryMessages(_ context.Context, memoryID string) (*service.AgentMemoryMessages, error) {
	if msgs, ok := m.messages[memoryID]; ok {
		return msgs, nil
	}
	return nil, nil
}

func (m *mockAgentMemoryStore) CreateAgentMemoryMessages(_ context.Context, msgs service.AgentMemoryMessages) error {
	m.messages[msgs.MemoryID] = &msgs
	return nil
}

// ─── Handler tests ───

func TestListOrgMemoriesAPI(t *testing.T) {
	store := newMockAgentMemoryStore()
	store.memories = []service.AgentMemory{
		{ID: "m1", AgentID: "a1", OrganizationID: "org1", SummaryL0: "Did thing 1"},
		{ID: "m2", AgentID: "a2", OrganizationID: "org1", SummaryL0: "Did thing 2"},
		{ID: "m3", AgentID: "a1", OrganizationID: "org2", SummaryL0: "Other org"},
	}
	s := &Server{agentMemoryStore: store}

	tests := []struct {
		name       string
		orgID      string
		agentID    string
		wantStatus int
		wantCount  int
	}{
		{
			name:       "list all in org1",
			orgID:      "org1",
			agentID:    "",
			wantStatus: http.StatusOK,
			wantCount:  2,
		},
		{
			name:       "filter by agent_id",
			orgID:      "org1",
			agentID:    "a1",
			wantStatus: http.StatusOK,
			wantCount:  1,
		},
		{
			name:       "empty org returns empty array",
			orgID:      "org-empty",
			agentID:    "",
			wantStatus: http.StatusOK,
			wantCount:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/api/v1/organizations/" + tt.orgID + "/memories"
			if tt.agentID != "" {
				url += "?agent_id=" + tt.agentID
			}

			req := httptest.NewRequest(http.MethodGet, url, nil)
			req.SetPathValue("id", tt.orgID)
			w := httptest.NewRecorder()

			s.ListOrgMemoriesAPI(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}

			var result []service.AgentMemory
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if len(result) != tt.wantCount {
				t.Errorf("count = %d, want %d", len(result), tt.wantCount)
			}
		})
	}
}

func TestListOrgMemoriesAPI_NoStore(t *testing.T) {
	s := &Server{agentMemoryStore: nil}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org1/memories", nil)
	req.SetPathValue("id", "org1")
	w := httptest.NewRecorder()

	s.ListOrgMemoriesAPI(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestGetAgentMemoryAPI(t *testing.T) {
	store := newMockAgentMemoryStore()
	store.memories = []service.AgentMemory{
		{ID: "m1", AgentID: "a1", OrganizationID: "org1", SummaryL0: "Test memory"},
	}
	s := &Server{agentMemoryStore: store}

	tests := []struct {
		name       string
		memoryID   string
		wantStatus int
	}{
		{
			name:       "found",
			memoryID:   "m1",
			wantStatus: http.StatusOK,
		},
		{
			name:       "not found",
			memoryID:   "nonexistent",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/agent-memories/"+tt.memoryID, nil)
			req.SetPathValue("id", tt.memoryID)
			w := httptest.NewRecorder()

			s.GetAgentMemoryAPI(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestGetAgentMemoryMessagesAPI(t *testing.T) {
	store := newMockAgentMemoryStore()
	store.messages["m1"] = &service.AgentMemoryMessages{
		MemoryID: "m1",
		Messages: []service.Message{{Role: "user", Content: "hello"}},
	}
	s := &Server{agentMemoryStore: store}

	tests := []struct {
		name       string
		memoryID   string
		wantStatus int
	}{
		{
			name:       "found",
			memoryID:   "m1",
			wantStatus: http.StatusOK,
		},
		{
			name:       "not found",
			memoryID:   "nonexistent",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/agent-memories/"+tt.memoryID+"/messages", nil)
			req.SetPathValue("id", tt.memoryID)
			w := httptest.NewRecorder()

			s.GetAgentMemoryMessagesAPI(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}

			if tt.wantStatus == http.StatusOK {
				var msgs service.AgentMemoryMessages
				if err := json.Unmarshal(w.Body.Bytes(), &msgs); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}
				if len(msgs.Messages) != 1 {
					t.Errorf("messages count = %d, want 1", len(msgs.Messages))
				}
			}
		})
	}
}

func TestDeleteAgentMemoryAPI(t *testing.T) {
	store := newMockAgentMemoryStore()
	store.memories = []service.AgentMemory{
		{ID: "m1", AgentID: "a1", OrganizationID: "org1", SummaryL0: "To be deleted"},
	}
	s := &Server{agentMemoryStore: store}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/agent-memories/m1", nil)
	req.SetPathValue("id", "m1")
	w := httptest.NewRecorder()

	s.DeleteAgentMemoryAPI(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	if len(store.deleted) != 1 || store.deleted[0] != "m1" {
		t.Errorf("deleted = %v, want [m1]", store.deleted)
	}

	if len(store.memories) != 0 {
		t.Errorf("memories after delete = %d, want 0", len(store.memories))
	}
}

func TestSearchOrgMemoriesAPI(t *testing.T) {
	store := newMockAgentMemoryStore()
	store.memories = []service.AgentMemory{
		{ID: "m1", AgentID: "a1", OrganizationID: "org1", SummaryL0: "Implemented authentication"},
		{ID: "m2", AgentID: "a2", OrganizationID: "org1", SummaryL0: "Fixed database bug"},
	}
	s := &Server{agentMemoryStore: store}

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCount  int
	}{
		{
			name:       "search matching one",
			body:       `{"query": "authentication"}`,
			wantStatus: http.StatusOK,
			wantCount:  1,
		},
		{
			name:       "search matching none",
			body:       `{"query": "kubernetes"}`,
			wantStatus: http.StatusOK,
			wantCount:  0,
		},
		{
			name:       "empty query returns error",
			body:       `{"query": ""}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid JSON body",
			body:       `not json`,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org1/memories/search", bytes.NewBufferString(tt.body))
			req.SetPathValue("id", "org1")
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			s.SearchOrgMemoriesAPI(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d; body = %s", w.Code, tt.wantStatus, w.Body.String())
			}

			if tt.wantStatus == http.StatusOK {
				var result []service.AgentMemory
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}
				if len(result) != tt.wantCount {
					t.Errorf("count = %d, want %d", len(result), tt.wantCount)
				}
			}
		})
	}
}
