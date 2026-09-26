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

func TestDeveloperSpaceHTTPCRUD(t *testing.T) {
	store := postgrestest.New(t, nil)
	user, err := store.CreateAuthUser(t.Context(), service.AuthUser{Username: "developer-http", PasswordHash: "hash"}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: store}

	base := developerSpaceContext(t, user.ID)
	create := httptest.NewRequest(http.MethodPost, "/api/v1/developer-spaces", strings.NewReader(`{"name":"Main"}`)).WithContext(base.Context())
	w := httptest.NewRecorder()
	s.CreateDeveloperSpaceAPI(w, create)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"status":"pending"`) || !strings.Contains(w.Body.String(), `"system_prompt"`) {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	s.ListDeveloperSpacesAPI(w, base)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"Main"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}

	bad := httptest.NewRequest(http.MethodPost, "/api/v1/developer-spaces", strings.NewReader(`{"name":"","host_path":"/tmp"}`)).WithContext(base.Context())
	w = httptest.NewRecorder()
	s.CreateDeveloperSpaceAPI(w, bad)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("caller-selected host path accepted: %d %s", w.Code, w.Body.String())
	}
}
