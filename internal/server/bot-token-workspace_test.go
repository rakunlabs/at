package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/password"

	"github.com/rakunlabs/at/internal/service"
)

func TestBotAndTokenHTTPWorkspaceIsolation(t *testing.T) {
	f := newMachineFixture(t)
	actor, _ := service.AccessPrincipalFromContext(f.ctx)
	if _, err := f.store.SetAuthUserPassword(t.Context(), actor.UserID, password.Dummy, nil); err != nil {
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
	api.GET("/v1/bots", f.s.ListBotConfigsAPI)
	api.POST("/v1/bots", f.s.CreateBotConfigAPI)
	api.GET("/v1/bots/video-templates", f.s.ListBotVideoTemplatesAPI)
	api.GET("/v1/bots/{id}", f.s.GetBotConfigAPI)
	api.PUT("/v1/bots/{id}", f.s.UpdateBotConfigAPI)
	api.DELETE("/v1/bots/{id}", f.s.DeleteBotConfigAPI)
	api.GET("/v1/bots/{id}/status", f.s.GetBotStatusAPI)
	api.POST("/v1/bots/{id}/start", f.s.StartBotAPI)
	api.POST("/v1/bots/{id}/stop", f.s.StopBotAPI)
	api.GET("/v1/api-tokens", f.s.ListAPITokensAPI)
	api.POST("/v1/api-tokens", f.s.CreateAPITokenAPI)
	api.PUT("/v1/api-tokens/{id}", f.s.UpdateAPITokenAPI)
	api.DELETE("/v1/api-tokens/{id}", f.s.DeleteAPITokenAPI)
	api.GET("/v1/api-tokens/{id}/usage", f.s.GetTokenUsageAPI)
	api.POST("/v1/api-tokens/{id}/usage/reset", f.s.ResetTokenUsageAPI)
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
	bots := map[string]service.BotConfig{}
	tokens := map[string]service.APIToken{}
	for _, workspace := range []string{f.workspace, other.ID} {
		w := call(http.MethodPost, "/bots", workspace, `{"name":"same bot","platform":"telegram","token":"fixture-token","enabled":false}`)
		var bot service.BotConfig
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &bot) != nil || bot.WorkspaceID != workspace {
			t.Fatalf("create bot: %d %s", w.Code, w.Body.String())
		}
		bots[workspace] = bot
		w = call(http.MethodGet, "/bots", workspace, "")
		var listed service.ListResult[service.BotConfig]
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &listed) != nil || len(listed.Data) != 1 || listed.Data[0].ID != bot.ID {
			t.Fatalf("list bot: %d %s", w.Code, w.Body.String())
		}
		w = call(http.MethodPost, "/api-tokens", workspace, `{"name":"same token"}`)
		var token createTokenResponse
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &token) != nil || token.Info.WorkspaceID != workspace {
			t.Fatalf("create token: %d %s", w.Code, w.Body.String())
		}
		tokens[workspace] = token.Info
		w = call(http.MethodGet, "/api-tokens", workspace, "")
		var tokenList service.ListResult[service.APIToken]
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &tokenList) != nil || len(tokenList.Data) != 1 || tokenList.Data[0].ID != token.Info.ID || tokenList.Data[0].WorkspaceID != workspace {
			t.Fatalf("token list: %d %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), token.Token) {
			t.Fatal("list leaked plaintext token")
		}
	}
	foreignBot, foreignToken := bots[other.ID].ID, tokens[other.ID].ID
	running, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	f.s.runningBots.Store(foreignBot, &runningBot{cancel: cancel, platform: "telegram"})
	t.Cleanup(func() { f.s.runningBots.Delete(foreignBot) })
	for _, route := range []struct{ method, path, body string }{
		{http.MethodGet, "/bots/" + foreignBot, ""},
		{http.MethodPut, "/bots/" + foreignBot, `{"name":"changed","platform":"telegram"}`},
		{http.MethodDelete, "/bots/" + foreignBot, ""},
		{http.MethodGet, "/bots/" + foreignBot + "/status", ""},
		{http.MethodPost, "/bots/" + foreignBot + "/start", ""},
		{http.MethodPost, "/bots/" + foreignBot + "/stop", ""},
		{http.MethodPut, "/api-tokens/" + foreignToken, `{"name":"changed"}`},
		{http.MethodDelete, "/api-tokens/" + foreignToken, ""},
		{http.MethodGet, "/api-tokens/" + foreignToken + "/usage", ""},
		{http.MethodPost, "/api-tokens/" + foreignToken + "/usage/reset", ""},
	} {
		w := call(route.method, route.path, f.workspace, route.body)
		if w.Code != 404 && w.Code != 403 {
			t.Fatalf("foreign %s %s: %d %s", route.method, route.path, w.Code, w.Body.String())
		}
	}
	principal, _, err := f.store.ResolveWorkspaceAccess(t.Context(), f.workspace, actor.UserID, "")
	if err != nil {
		t.Fatal(err)
	}
	scoped := service.WithAccessPrincipal(t.Context(), principal)
	for _, tool := range []func(context.Context, map[string]any) (string, error){f.s.execBotStop, f.s.execBotDelete, f.s.execBotStatus} {
		if _, err := tool(scoped, map[string]any{"id": foreignBot}); err == nil {
			t.Fatal("MCP bot tool bypassed workspace")
		}
	}
	for _, tool := range []func(context.Context, map[string]any) (string, error){f.s.execAPITokenGetUsage, f.s.execAPITokenResetUsage} {
		if _, err := tool(scoped, map[string]any{"id": foreignToken}); err == nil {
			t.Fatal("MCP token tool bypassed workspace")
		}
	}
	if running.Err() != nil || !f.s.isBotRunning(foreignBot) {
		t.Fatal("foreign bot stopped before admission")
	}
	if w := call(http.MethodGet, "/bots/"+foreignBot+"/status", other.ID, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"running":true`) {
		t.Fatalf("own bot status: %d %s", w.Code, w.Body.String())
	}
	if w := call(http.MethodPost, "/bots/"+foreignBot+"/stop", other.ID, ""); w.Code != 200 || running.Err() == nil {
		t.Fatalf("own bot stop: %d %s", w.Code, w.Body.String())
	}
	if w := call(http.MethodGet, "/bots/video-templates", f.workspace, ""); w.Code != 200 {
		t.Fatalf("template route mistaken for bot ID: %d %s", w.Code, w.Body.String())
	}
	if w := call(http.MethodPut, "/api-tokens/"+foreignToken, other.ID, `{"name":"updated token"}`); w.Code != 200 {
		t.Fatalf("own token update: %d %s", w.Code, w.Body.String())
	}
}
