package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/ada"

	"github.com/rakunlabs/at/internal/service"
)

// TestAPITokenRotatePostgres pins the contract that makes rotation worth having
// over delete-and-recreate: the token keeps its identity, restrictions, pause
// state and accumulated usage while its secret is replaced, and the superseded
// secret stops authenticating immediately. It runs against a real store because
// the interesting parts (workspace scoping, usage rows keyed on the unchanged
// id, last_used_at) live in SQL, not in the handler.
func TestAPITokenRotatePostgres(t *testing.T) {
	f := newMachineFixture(t)
	actor, _ := service.AccessPrincipalFromContext(f.ctx)
	if _, err := f.store.SetAuthUserPassword(t.Context(), actor.UserID, testPasswordHash, nil); err != nil {
		t.Fatal(err)
	}
	a, err := newNativeAuth(nativeTestConfig(), f.store)
	if err != nil {
		t.Fatal(err)
	}
	f.s.nativeAuth = a
	f.s.config.BasePath = "/at"
	f.s.tokenUsageStore = f.store
	mux := ada.New()
	a.register(mux, "/at")
	api := mux.Group("/at/api")
	api.Use(f.s.workspaceBusinessAuthentication())
	api.POST("/v1/api-tokens", f.s.CreateAPITokenAPI)
	api.POST("/v1/api-tokens/{id}/rotate", f.s.RotateAPITokenAPI)
	api.PUT("/v1/api-tokens/{id}/pause", f.s.SetAPITokenPausedAPI)
	api.GET("/v1/api-tokens/{id}/usage", f.s.GetTokenUsageAPI)
	cookie := nativeLoginCookie(t, mux, "machine-admin")
	other, err := f.store.CreateWorkspace(service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: actor.UserID}), "other", actor.UserID)
	if err != nil {
		t.Fatal(err)
	}

	call := func(method, path, workspace, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/at/api/v1"+path, strings.NewReader(body))
		r.AddCookie(cookie)
		r.Header.Set("Origin", a.cfg.Origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-AT-Workspace-ID", workspace)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	create := func(workspace, body string) createTokenResponse {
		t.Helper()
		w := call(http.MethodPost, "/api-tokens", workspace, body)
		var resp createTokenResponse
		if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &resp) != nil {
			t.Fatalf("create token: %d %s", w.Code, w.Body.String())
		}
		return resp
	}
	authenticate := func(secret string) (*authResult, string) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/gateway/v1/models", nil)
		r.Header.Set("Authorization", "Bearer "+secret)
		return f.s.authenticateRequest(r)
	}

	created := create(f.workspace, `{"name":"deploy key","total_token_limit":5000,"allowed_models_mode":"list","allowed_models":["upstream/model"]}`)
	foreign := create(other.ID, `{"name":"other key"}`)

	if err := f.store.RecordUsage(f.ctx, created.Info.ID, "upstream/model", service.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}); err != nil {
		t.Fatal(err)
	}
	if auth, reason := authenticate(created.Token); auth == nil {
		t.Fatalf("fresh token rejected: %s", reason)
	}
	if _, ok := f.s.tokenLastUsed.Load(created.Info.ID); !ok {
		t.Fatal("use did not record a last_used throttle entry")
	}
	if w := call(http.MethodPut, "/api-tokens/"+created.Info.ID+"/pause", f.workspace, `{"paused":true}`); w.Code != http.StatusOK {
		t.Fatalf("pause: %d %s", w.Code, w.Body.String())
	}

	w := call(http.MethodPost, "/api-tokens/"+created.Info.ID+"/rotate", f.workspace, "")
	var rotated createTokenResponse
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &rotated) != nil {
		t.Fatalf("rotate: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), created.Token) {
		t.Fatal("rotate response leaked the superseded secret")
	}
	if rotated.Token == created.Token || !strings.HasPrefix(rotated.Token, "at_") || len(rotated.Token) != 67 {
		t.Fatalf("rotated secret has the wrong shape: %q", rotated.Token)
	}
	if rotated.Info.ID != created.Info.ID || rotated.Info.WorkspaceID != f.workspace {
		t.Fatalf("rotation changed identity: %+v", rotated.Info)
	}
	if rotated.Info.TokenPrefix != rotated.Token[:8] || rotated.Info.TokenPrefix == created.Info.TokenPrefix {
		t.Fatalf("prefix not refreshed: %q vs %q", rotated.Info.TokenPrefix, created.Info.TokenPrefix)
	}
	if rotated.Info.Name != "deploy key" || !rotated.Info.TotalTokenLimit.Valid || rotated.Info.TotalTokenLimit.V != 5000 {
		t.Fatalf("rotation dropped settings: %+v", rotated.Info)
	}
	if rotated.Info.AllowedModelsMode != "list" || len(rotated.Info.AllowedModels) != 1 || rotated.Info.AllowedModels[0] != "upstream/model" {
		t.Fatalf("rotation dropped restrictions: %+v", rotated.Info)
	}
	if !rotated.Info.Paused {
		t.Fatal("rotation silently resumed a paused token")
	}
	if rotated.Info.LastUsedAt.Valid {
		t.Fatal("rotated secret reported as already used")
	}
	if _, ok := f.s.tokenLastUsed.Load(created.Info.ID); ok {
		t.Fatal("stale last_used throttle would suppress the first use of the new secret")
	}

	// The old secret is dead the moment the rotation commits; the new one is
	// live but still subject to the pause that was in place before.
	if auth, reason := authenticate(created.Token); auth != nil || !strings.Contains(reason, "invalid") {
		t.Fatalf("superseded secret still authenticates: %v %q", auth, reason)
	}
	if auth, reason := authenticate(rotated.Token); auth != nil || !strings.Contains(reason, "paused") {
		t.Fatalf("paused rotated secret: %v %q", auth, reason)
	}
	if w := call(http.MethodPut, "/api-tokens/"+created.Info.ID+"/pause", f.workspace, `{"paused":false}`); w.Code != http.StatusOK {
		t.Fatalf("resume: %d %s", w.Code, w.Body.String())
	}
	if auth, reason := authenticate(rotated.Token); auth == nil {
		t.Fatalf("rotated secret rejected: %s", reason)
	}

	// Usage rows key off the unchanged token id, so limits keep counting.
	w = call(http.MethodGet, "/api-tokens/"+created.Info.ID+"/usage", f.workspace, "")
	var usage []service.TokenUsage
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &usage) != nil || len(usage) != 1 || usage[0].TotalTokens != 15 {
		t.Fatalf("rotation reset usage: %d %s", w.Code, w.Body.String())
	}

	// Rotating twice must not reissue the same secret.
	w = call(http.MethodPost, "/api-tokens/"+created.Info.ID+"/rotate", f.workspace, "")
	var again createTokenResponse
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &again) != nil || again.Token == rotated.Token {
		t.Fatalf("second rotate: %d %s", w.Code, w.Body.String())
	}
	if auth, _ := authenticate(rotated.Token); auth != nil {
		t.Fatal("previous rotation still authenticates")
	}

	// A token belonging to another workspace is neither rotatable nor disturbed.
	if w := call(http.MethodPost, "/api-tokens/"+foreign.Info.ID+"/rotate", f.workspace, ""); w.Code != http.StatusNotFound && w.Code != http.StatusForbidden {
		t.Fatalf("foreign rotate: %d %s", w.Code, w.Body.String())
	}
	if auth, reason := authenticate(foreign.Token); auth == nil {
		t.Fatalf("foreign token disturbed by denied rotate: %s", reason)
	}
	if w := call(http.MethodPost, "/api-tokens/missing/rotate", f.workspace, ""); w.Code != http.StatusNotFound && w.Code != http.StatusForbidden {
		t.Fatalf("unknown rotate: %d %s", w.Code, w.Body.String())
	}
}
