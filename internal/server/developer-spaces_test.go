package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func developerSpaceContext(t *testing.T, userID string) *http.Request {
	t.Helper()
	ctx := service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: userID, WorkspaceID: service.DefaultWorkspaceID})
	return httptest.NewRequest(http.MethodGet, "/api/v1/developer-spaces", nil).WithContext(ctx)
}

func TestDeveloperSpaceHTTP(t *testing.T) {
	store := postgrestest.New(t, nil)
	user, err := store.CreateAuthUser(t.Context(), service.AuthUser{Username: "developer-http", PasswordHash: "hash"}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: store}
	base := developerSpaceContext(t, user.ID)

	w := httptest.NewRecorder()
	s.GetDeveloperSpaceAPI(w, base)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"pending"`) || !strings.Contains(w.Body.String(), `"system_prompt"`) {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	first := w.Body.String()
	w = httptest.NewRecorder()
	s.GetDeveloperSpaceAPI(w, base)
	if w.Body.String() != first {
		t.Fatalf("second visit returned a different space: %s", w.Body.String())
	}

	create := httptest.NewRequest(http.MethodPost, "/api/v1/developer-sessions", strings.NewReader(`{"project_path":"at","provider":"openai","model":"gpt"}`)).WithContext(base.Context())
	w = httptest.NewRecorder()
	s.CreateDeveloperSessionAPI(w, create)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"project_path":"at"`) || !strings.Contains(w.Body.String(), `"mode":"build"`) {
		t.Fatalf("create session: %d %s", w.Code, w.Body.String())
	}

	for _, body := range []string{`{"project_path":"../etc","provider":"openai"}`, `{"project_path":"/etc","provider":"openai"}`, `{"worktree_id":"x","provider":"openai"}`} {
		bad := httptest.NewRequest(http.MethodPost, "/api/v1/developer-sessions", strings.NewReader(body)).WithContext(base.Context())
		w = httptest.NewRecorder()
		s.CreateDeveloperSessionAPI(w, bad)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("accepted %s: %d %s", body, w.Code, w.Body.String())
		}
	}

	w = httptest.NewRecorder()
	s.ListDeveloperSessionsAPI(w, base)
	if w.Code != http.StatusOK || strings.Count(w.Body.String(), `"id"`) != 1 {
		t.Fatalf("list sessions: %d %s", w.Code, w.Body.String())
	}

	reset := httptest.NewRequest(http.MethodPost, "/api/v1/developer-space/reset", strings.NewReader(`{}`)).WithContext(base.Context())
	w = httptest.NewRecorder()
	s.ResetDeveloperSpaceAPI(w, reset)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("reset without confirmation: %d %s", w.Code, w.Body.String())
	}
}
