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

func newConnectorTestServer(t *testing.T) *Server {
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
	return &Server{
		connectorStore: store,
		builtinConnectors: []service.Connector{
			{
				Slug:     "google",
				Name:     "Google",
				AuthKind: service.ConnectorAuthOAuth2,
				Builtin:  true,
				OAuth: &service.ConnectorOAuth{
					AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
					TokenURL: "https://oauth2.googleapis.com/token",
					Scopes:   []string{"a", "b"},
				},
				Fields: []service.ConnectorField{
					{Key: "google_client_id", Type: "text"},
					{Key: "google_client_secret", Type: "secret"},
				},
			},
		},
	}
}

func TestConnectorRegistry_MergeAndResolve(t *testing.T) {
	ctx := context.Background()
	s := newConnectorTestServer(t)

	// Only the built-in is present initially.
	list, err := s.listConnectors(ctx)
	if err != nil {
		t.Fatalf("listConnectors: %v", err)
	}
	if len(list) != 1 || list[0].Slug != "google" || !list[0].Builtin {
		t.Fatalf("expected 1 builtin connector, got %+v", list)
	}

	// Add a user connector.
	if _, err := s.connectorStore.CreateConnector(ctx, service.Connector{
		Slug: "spotify", Name: "Spotify", AuthKind: service.ConnectorAuthOAuth2,
		OAuth: &service.ConnectorOAuth{AuthURL: "x", TokenURL: "y"},
	}); err != nil {
		t.Fatalf("create user connector: %v", err)
	}

	// Override the built-in google with a DB row.
	if _, err := s.connectorStore.CreateConnector(ctx, service.Connector{
		Slug: "google", Name: "Google (custom)", AuthKind: service.ConnectorAuthOAuth2,
		OAuth: &service.ConnectorOAuth{AuthURL: "x", TokenURL: "y"},
	}); err != nil {
		t.Fatalf("create override: %v", err)
	}

	list, _ = s.listConnectors(ctx)
	if len(list) != 2 {
		t.Fatalf("expected 2 connectors after merge, got %d", len(list))
	}

	// resolveConnector: DB override wins over built-in.
	g, err := s.resolveConnector(ctx, "google")
	if err != nil {
		t.Fatalf("resolveConnector: %v", err)
	}
	if g.Name != "Google (custom)" || g.Builtin {
		t.Errorf("override did not win: %+v", g)
	}
}

func TestConnectorCRUDAPI(t *testing.T) {
	s := newConnectorTestServer(t)

	// Create a new connector.
	body := `{
		"slug": "notion",
		"name": "Notion",
		"auth_kind": "oauth2",
		"oauth": {"auth_url": "https://api.notion.com/v1/oauth/authorize", "token_url": "https://api.notion.com/v1/oauth/token"},
		"fields": [{"key": "notion_client_id", "type": "text", "required": true}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connectors", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.CreateConnectorAPI(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", w.Code, w.Body.String())
	}

	// Editing the built-in 'google' (no DB row yet) must create an override.
	put := `{"slug": "google", "name": "G", "auth_kind": "oauth2", "oauth": {"auth_url": "x", "token_url": "y"}}`
	req = httptest.NewRequest(http.MethodPut, "/api/v1/connectors/google", bytes.NewBufferString(put))
	req.SetPathValue("slug", "google")
	w = httptest.NewRecorder()
	s.UpdateConnectorAPI(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update builtin override: status %d, body=%s", w.Code, w.Body.String())
	}

	// Deleting a pure built-in (no DB row) is rejected; here google now has an
	// override row, so deletion reverts it.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/connectors/google", nil)
	req.SetPathValue("slug", "google")
	w = httptest.NewRecorder()
	s.DeleteConnectorAPI(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete override: status %d, body=%s", w.Code, w.Body.String())
	}

	// Invalid auth kind rejected.
	bad := `{"slug": "x", "auth_kind": "weird"}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/connectors", bytes.NewBufferString(bad))
	w = httptest.NewRecorder()
	s.CreateConnectorAPI(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad auth kind, got %d", w.Code)
	}

	// oauth2 without endpoints rejected.
	bad = `{"slug": "y", "auth_kind": "oauth2"}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/connectors", bytes.NewBufferString(bad))
	w = httptest.NewRecorder()
	s.CreateConnectorAPI(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oauth2 w/o endpoints, got %d", w.Code)
	}
}

func TestDeletePureBuiltinConnectorRejected(t *testing.T) {
	s := newConnectorTestServer(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/connectors/google", nil)
	req.SetPathValue("slug", "google")
	w := httptest.NewRecorder()
	s.DeleteConnectorAPI(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 deleting pure built-in, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestConnectorCredentialsFromValues(t *testing.T) {
	creds := connectorCredentialsFromValues(map[string]string{
		"google_client_id":     "id",
		"google_client_secret": "secret",
		"google_refresh_token": "refresh",
		"pexels_api_key":       "key",
		"acme_access_token":    "atok",
		"acme_weird_field":     "w",
	})
	if creds.ClientID != "id" || creds.ClientSecret != "secret" || creds.RefreshToken != "refresh" || creds.APIKey != "key" {
		t.Errorf("well-known mapping wrong: %+v", creds)
	}
	// access_token + non-standard keys land in Extra under their full key.
	if creds.Extra["acme_access_token"] != "atok" {
		t.Errorf("access_token not in extra: %+v", creds.Extra)
	}
	if creds.Extra["acme_weird_field"] != "w" {
		t.Errorf("custom field not in extra: %+v", creds.Extra)
	}
}

func TestJSONDotPath(t *testing.T) {
	var data any
	_ = json.Unmarshal([]byte(`{"email":"a@b.com","items":[{"snippet":{"title":"Chan"}}]}`), &data)

	if got := jsonDotPath(data, "email"); got != "a@b.com" {
		t.Errorf("email path: got %q", got)
	}
	if got := jsonDotPath(data, "items.0.snippet.title"); got != "Chan" {
		t.Errorf("array path: got %q", got)
	}
	if got := jsonDotPath(data, "items.5.snippet.title"); got != "" {
		t.Errorf("out-of-range path should be empty, got %q", got)
	}
	if got := jsonDotPath(data, "missing.key"); got != "" {
		t.Errorf("missing path should be empty, got %q", got)
	}
}

func TestBuildAuthorizeURL_PKCE(t *testing.T) {
	s := newConnectorTestServer(t)
	c := &service.Connector{
		Slug:     "spotify",
		AuthKind: service.ConnectorAuthOAuth2,
		OAuth: &service.ConnectorOAuth{
			AuthURL:  "https://accounts.spotify.com/authorize",
			TokenURL: "https://accounts.spotify.com/api/token",
			Scopes:   []string{"user-read-email"},
			UsePKCE:  true,
		},
	}
	u, err := s.buildAuthorizeURL(c, "cid", "https://cb", "user-read-email", "state123", "state123")
	if err != nil {
		t.Fatalf("buildAuthorizeURL: %v", err)
	}
	if !bytes.Contains([]byte(u), []byte("code_challenge=")) {
		t.Errorf("expected code_challenge in URL: %s", u)
	}
	if !bytes.Contains([]byte(u), []byte("code_challenge_method=S256")) {
		t.Errorf("expected S256 method in URL: %s", u)
	}
	// Verifier must be retrievable once and only once.
	if v := s.pkceTake("state123"); v == "" {
		t.Error("expected stored PKCE verifier")
	}
	if v := s.pkceTake("state123"); v != "" {
		t.Error("verifier should be single-use")
	}
}

func TestBuildAuthorizeURL_AccessTypePrompt(t *testing.T) {
	s := newConnectorTestServer(t)
	c := &service.Connector{
		Slug:     "google",
		AuthKind: service.ConnectorAuthOAuth2,
		OAuth: &service.ConnectorOAuth{
			AuthURL:    "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL:   "https://oauth2.googleapis.com/token",
			AccessType: "offline",
			Prompt:     "consent",
		},
	}
	u, err := s.buildAuthorizeURL(c, "cid", "https://cb", "scope", "state", "state")
	if err != nil {
		t.Fatalf("buildAuthorizeURL: %v", err)
	}
	if !bytes.Contains([]byte(u), []byte("access_type=offline")) {
		t.Errorf("missing access_type: %s", u)
	}
	if !bytes.Contains([]byte(u), []byte("prompt=consent")) {
		t.Errorf("missing prompt: %s", u)
	}
}
