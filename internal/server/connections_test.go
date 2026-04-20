package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/sqlite3"
)

func newConnTestServer(t *testing.T) (*Server, *sqlite3.SQLite) {
	t.Helper()
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "test.sqlite") + "?cache=shared"
	store, err := sqlite3.New(context.Background(), &config.StoreSQLite{
		Datasource: dsn,
		Migrate:    config.Migrate{Datasource: dsn},
	}, nil)
	if err != nil {
		t.Fatalf("sqlite3.New: %v", err)
	}
	t.Cleanup(store.Close)
	s := &Server{
		connectionStore: store,
		agentStore:      store,
		variableStore:   store,
	}
	return s, store
}

func TestConnectionAPI_CreateAndList(t *testing.T) {
	s, _ := newConnTestServer(t)

	// Create
	body := `{
		"provider": "youtube",
		"name": "Main Channel",
		"account_label": "@rakunlabs",
		"credentials": {
			"client_id": "id-1",
			"client_secret": "secret-1",
			"refresh_token": "refresh-1"
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connections", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.CreateConnectionAPI(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", w.Code, w.Body.String())
	}
	var created connectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.ID == "" {
		t.Fatal("missing ID")
	}
	if !created.Credentials.RefreshTokenSet {
		t.Error("RefreshTokenSet should be true")
	}
	if created.Credentials.RefreshToken != "" {
		t.Error("secrets should be redacted in create response (no ?reveal)")
	}

	// List: filter by provider.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/connections?provider=youtube", nil)
	w = httptest.NewRecorder()
	s.ListConnectionsAPI(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: status %d", w.Code)
	}
	var list []connectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Main Channel" {
		t.Fatalf("list: %+v", list)
	}
}

func TestConnectionAPI_UniqueViolation(t *testing.T) {
	s, _ := newConnTestServer(t)

	body := `{"provider":"youtube","name":"X","credentials":{"refresh_token":"r"}}`
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/connections", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.CreateConnectionAPI(w, req)
		if i == 0 && w.Code != http.StatusCreated {
			t.Fatalf("first create: status %d", w.Code)
		}
		if i == 1 && w.Code != http.StatusConflict {
			t.Fatalf("duplicate create: expected 409, got %d: %s", w.Code, w.Body.String())
		}
	}
}

func TestConnectionAPI_DeleteWithReferences(t *testing.T) {
	s, store := newConnTestServer(t)

	// Create the connection.
	conn, err := store.CreateConnection(context.Background(), service.Connection{
		Provider: "youtube",
		Name:     "Main",
		Credentials: service.ConnectionCredentials{
			RefreshToken: "r1",
		},
	})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}

	// Create two agents referencing it.
	agent1, err := store.CreateAgent(context.Background(), service.Agent{
		Name: "Agent One",
		Config: service.AgentConfig{
			Provider:    "anthropic",
			Connections: map[string]string{"youtube": conn.ID},
		},
	})
	if err != nil {
		t.Fatalf("CreateAgent 1: %v", err)
	}
	agent2, err := store.CreateAgent(context.Background(), service.Agent{
		Name: "Agent Two",
		Config: service.AgentConfig{
			Provider: "anthropic",
			Skills: []service.SkillRef{
				{ID: "youtube_publish", Connections: map[string]string{"youtube": conn.ID}},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateAgent 2: %v", err)
	}

	// Delete without force: should 409 with the list.
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/connections/"+conn.ID, nil)
	req.SetPathValue("id", conn.ID)
	w := httptest.NewRecorder()
	s.DeleteConnectionAPI(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("delete without force: expected 409, got %d: %s", w.Code, w.Body.String())
	}
	var conflict map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &conflict)
	refs, _ := conflict["used_by_agents"].([]any)
	if len(refs) != 2 {
		t.Errorf("expected 2 agent refs, got %d: %v", len(refs), conflict)
	}

	// Delete with force: should succeed and strip references.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/connections/"+conn.ID+"?force=true", nil)
	req.SetPathValue("id", conn.ID)
	w = httptest.NewRecorder()
	s.DeleteConnectionAPI(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete with force: status %d: %s", w.Code, w.Body.String())
	}

	// Verify references were stripped.
	a1, _ := store.GetAgent(context.Background(), agent1.ID)
	if a1 != nil && a1.Config.Connections["youtube"] != "" {
		t.Errorf("agent1 still references connection: %v", a1.Config.Connections)
	}
	a2, _ := store.GetAgent(context.Background(), agent2.ID)
	if a2 != nil && len(a2.Config.Skills) > 0 && a2.Config.Skills[0].Connections["youtube"] != "" {
		t.Errorf("agent2 skill override still present: %v", a2.Config.Skills[0].Connections)
	}
	gone, _ := store.GetConnection(context.Background(), conn.ID)
	if gone != nil {
		t.Errorf("connection not deleted: %+v", gone)
	}
}

func TestConnectionAPI_ListIncludesUsage(t *testing.T) {
	s, store := newConnTestServer(t)

	conn, _ := store.CreateConnection(context.Background(), service.Connection{
		Provider: "youtube",
		Name:     "Shared",
		Credentials: service.ConnectionCredentials{
			RefreshToken: "r",
		},
	})
	// Two agents share the same connection.
	for _, name := range []string{"A", "B"} {
		_, err := store.CreateAgent(context.Background(), service.Agent{
			Name: name,
			Config: service.AgentConfig{
				Provider:    "anthropic",
				Connections: map[string]string{"youtube": conn.ID},
			},
		})
		if err != nil {
			t.Fatalf("CreateAgent %s: %v", name, err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/connections", nil)
	w := httptest.NewRecorder()
	s.ListConnectionsAPI(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	var list []connectionResponse
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 1 {
		t.Fatalf("expected 1 connection, got %d", len(list))
	}
	if len(list[0].UsedByAgents) != 2 {
		t.Errorf("expected 2 using agents, got %d: %+v", len(list[0].UsedByAgents), list[0].UsedByAgents)
	}
}

func TestConnectionAPI_RevealSecrets(t *testing.T) {
	s, store := newConnTestServer(t)

	conn, _ := store.CreateConnection(context.Background(), service.Connection{
		Provider: "youtube",
		Name:     "Secret",
		Credentials: service.ConnectionCredentials{
			ClientID:     "id-x",
			ClientSecret: "secret-x",
			RefreshToken: "refresh-x",
		},
	})

	// Without ?reveal: secrets redacted.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/connections/"+conn.ID, nil)
	req.SetPathValue("id", conn.ID)
	w := httptest.NewRecorder()
	s.GetConnectionAPI(w, req)
	var got connectionResponse
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Credentials.ClientSecret != "" {
		t.Error("ClientSecret leaked without reveal")
	}
	if !got.Credentials.ClientSecretSet {
		t.Error("ClientSecretSet should be true")
	}
	if got.Credentials.ClientID != "id-x" {
		t.Errorf("ClientID should not be redacted: got %q", got.Credentials.ClientID)
	}

	// With ?reveal=true: secrets returned.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/connections/"+conn.ID+"?reveal=true", nil)
	req.SetPathValue("id", conn.ID)
	w = httptest.NewRecorder()
	s.GetConnectionAPI(w, req)
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Credentials.ClientSecret != "secret-x" || got.Credentials.RefreshToken != "refresh-x" {
		t.Errorf("expected secrets revealed, got %+v", got.Credentials)
	}
}
