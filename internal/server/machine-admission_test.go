package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/loopgov"
	"github.com/rakunlabs/at/internal/store/postgres"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

type machineFixture struct {
	s         *Server
	store     *postgres.Postgres
	ctx       context.Context
	member    string
	workspace string
}

func newMachineFixture(t *testing.T) machineFixture {
	t.Helper()
	store := postgrestest.New(t, bytes.Repeat([]byte{1}, 32))
	admin, err := store.CreateAuthUser(t.Context(), service.AuthUser{Username: "machine-admin", Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	member, err := store.CreateAuthUser(t.Context(), service.AuthUser{Username: "machine-member"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAuthSession(t.Context(), service.AuthSession{Hash: "machine-login", UserID: admin.ID, Version: admin.SessionVersion, Transport: "web", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	workspace, err := store.CreateWorkspace(service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: admin.ID}), "machine workspace", member.ID)
	if err != nil {
		t.Fatal(err)
	}
	principal, _, err := store.ResolveWorkspaceAccess(t.Context(), workspace.ID, admin.ID, "machine-login")
	if err != nil {
		t.Fatal(err)
	}
	ctx := service.WithAccessPrincipal(t.Context(), principal)
	workspace.ExecutionEnabled = true
	if _, err := store.UpdateWorkspace(ctx, *workspace); err != nil {
		t.Fatal(err)
	}
	s := &Server{ctx: t.Context(), store: store, botConfigStore: store, mcpServerStore: store, mcpSetStore: store, tokenStore: store, taskStore: store, agentStore: store, chatSessionStore: store, loopGov: loopgov.New(loopgov.Config{WorkspaceRoot: t.TempDir()}, nil)}
	req := httptest.NewRequest(http.MethodPut, "/execution-policy", strings.NewReader(`{"mode":"trusted_host","version":0,"allowed_tools":["task_list"]}`)).WithContext(ctx)
	req.SetPathValue("workspace", workspace.ID)
	w := httptest.NewRecorder()
	s.RuntimeExecutionPolicyAPI(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("policy: %d %s", w.Code, w.Body.String())
	}
	ctx, err = s.bindRuntimePrincipal(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	return machineFixture{s: s, store: store, ctx: ctx, member: member.ID, workspace: workspace.ID}
}

func (f machineFixture) token(t *testing.T, name, mode string, mcps []string) string {
	t.Helper()
	raw := "gateway-token-" + name
	hash := sha256.Sum256([]byte(raw))
	if _, err := f.store.CreateAPIToken(f.ctx, service.APIToken{Name: name, AllowedMCPsMode: mode, AllowedMCPs: mcps}, hex.EncodeToString(hash[:])); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestGatewayMCPMachineIdentityEndToEnd(t *testing.T) {
	f := newMachineFixture(t)
	srv, err := f.store.CreateMCPServer(f.ctx, service.MCPServer{Name: "at-management", Config: service.MCPServerConfig{EnabledBuiltinTools: []string{"task_list"}}})
	if err != nil {
		t.Fatal(err)
	}
	token := f.token(t, "allowed", "list", []string{srv.Name})
	deniedToken := f.token(t, "denied", "none", nil)
	request := func(credential, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/gateway/v1/mcp/at-management", strings.NewReader(body))
		r.SetPathValue("name", srv.Name)
		if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
		}
		w := httptest.NewRecorder()
		f.s.GatewayMCPHandler(w, r)
		if w.Code != want {
			t.Fatalf("status %d want %d: %s", w.Code, want, w.Body.String())
		}
		return w
	}
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`
	request("", init, http.StatusUnauthorized)
	request("bad-token", init, http.StatusUnauthorized)
	request(token, init, http.StatusForbidden) // missing identity must never be 500
	if _, err := f.s.saveRuntimeBinding(f.ctx, "mcp", srv.ID, false, f.member); err != nil {
		t.Fatal(err)
	}
	request(deniedToken, init, http.StatusForbidden)
	request(token, init, http.StatusOK)
	listed := request(token, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, http.StatusOK)
	if !strings.Contains(listed.Body.String(), `"task_list"`) {
		t.Fatalf("tools not discovered: %s", listed.Body.String())
	}
	called := request(token, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"task_list","arguments":{}}}`, http.StatusOK)
	var rpc struct {
		Error  any `json:"error"`
		Result any `json:"result"`
	}
	if err := json.Unmarshal(called.Body.Bytes(), &rpc); err != nil || rpc.Error != nil || rpc.Result == nil {
		t.Fatalf("scoped tool call failed: %s (%v)", called.Body.String(), err)
	}
	if _, err := f.store.GetMCPServerByName(t.Context(), srv.Name); !errors.Is(err, service.ErrWorkspaceRequired) {
		t.Fatalf("anonymous business lookup allowed: %v", err)
	}
	// Browser logout must not revoke a separately bound machine identity.
	if err := f.store.DeleteAuthSession(t.Context(), "machine-login"); err != nil {
		t.Fatal(err)
	}
	request(token, init, http.StatusOK)
	if _, err := f.store.InvalidateAuthUser(t.Context(), f.member, true); err != nil {
		t.Fatal(err)
	}
	request(token, init, http.StatusForbidden)
}

func TestGatewayMCPRouteWorkspaceAndPublicAdmission(t *testing.T) {
	f := newMachineFixture(t)
	first, err := f.store.CreateMCPServer(f.ctx, service.MCPServer{Name: "shared-name", Public: true})
	if err != nil {
		t.Fatal(err)
	}
	actor, _ := service.AccessPrincipalFromContext(f.ctx)
	p, _, err := f.store.ResolveWorkspaceAccess(f.ctx, "legacy-default", actor.UserID, actor.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	otherCtx := service.WithAccessPrincipal(t.Context(), p)
	second, err := f.store.CreateMCPServer(otherCtx, service.MCPServer{Name: first.Name, Public: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, server := range []*service.MCPServer{first, second} {
		route, err := f.store.GetGatewayMCPRoute(t.Context(), server.Name, server.WorkspaceID)
		if err != nil || route == nil || route.ID != server.ID {
			t.Fatalf("workspace route: %+v %v", route, err)
		}
		if len(route.Config.EnabledBuiltinTools) != 0 {
			t.Fatal("admission metadata contained executable config")
		}
	}
	if _, err := f.store.GetGatewayMCPRoute(t.Context(), first.Name, ""); !errors.Is(err, service.ErrWorkspaceConflict) {
		t.Fatalf("ambiguous public route accepted: %v", err)
	}
	if route, err := f.store.GetGatewayMCPRoute(t.Context(), first.Name, "another-workspace"); err != nil || route != nil {
		t.Fatalf("cross workspace route: %+v %v", route, err)
	}
}

func TestBotMachineIdentitySessionAndMessage(t *testing.T) {
	f := newMachineFixture(t)
	if _, err := f.store.CreateProvider(f.ctx, service.ProviderRecord{Key: "test", Config: config.LLMConfig{Type: "openai", Model: "test", APIKey: "fixture"}}); err != nil {
		t.Fatal(err)
	}
	f.s.providerFactory = func(config.LLMConfig) (service.LLMProvider, error) {
		return &fakeObsProvider{responses: []*service.LLMResponse{{Content: "Merhaba!", Finished: true}}}, nil
	}
	agent, err := f.store.CreateAgent(f.ctx, service.Agent{Name: "bot-agent", Config: service.AgentConfig{Provider: "test", Model: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	bot, err := f.store.CreateBotConfig(f.ctx, service.BotConfig{Name: "test-bot", Platform: "telegram", Token: "fixture-token", DefaultAgentID: agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	start := httptest.NewRequest(http.MethodPost, "/api/v1/bots/"+bot.ID+"/start", nil).WithContext(f.ctx)
	start.SetPathValue("id", bot.ID)
	w := httptest.NewRecorder()
	f.s.StartBotAPI(w, start)
	if w.Code != http.StatusForbidden || f.s.isBotRunning(bot.ID) {
		t.Fatalf("unbound bot falsely started: %d %s", w.Code, w.Body.String())
	}
	if _, err := f.s.saveRuntimeBinding(f.ctx, "bot", bot.ID, false, f.member); err != nil {
		t.Fatal(err)
	}
	ctx, err := f.s.ResumeRuntimeSubject(t.Context(), "bot", bot.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := f.store.GetExecutionBotConfig(ctx, bot.ID)
	if err != nil || credentials == nil || credentials.Token != bot.Token {
		t.Fatalf("bot credential admission: %+v %v", credentials, err)
	}
	sessionID, _, err := f.s.findOrCreateBotSession(ctx, "telegram", bot.ID, "123", "456", agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	response, err := f.s.collectAgenticResponse(ctx, sessionID, "selam")
	if err != nil || response != "Merhaba!" {
		t.Fatalf("bot reply %q: %v", response, err)
	}
	messages, err := f.store.ListChatMessages(ctx, sessionID, 10)
	if err != nil || len(messages) != 2 {
		t.Fatalf("messages not persisted: %d %v", len(messages), err)
	}
	if _, err := f.s.saveRuntimeBinding(f.ctx, "bot", bot.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.GetExecutionBotConfig(ctx, bot.ID); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatalf("revoked bot credentials available: %v", err)
	}
	if _, _, err := f.s.findOrCreateBotSession(ctx, "telegram", bot.ID, "123", "456", agent.ID); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatalf("revoked bot session allowed: %v", err)
	}
}

func TestRuntimeBindingManagementAPI(t *testing.T) {
	f := newMachineFixture(t)
	bot, err := f.store.CreateBotConfig(f.ctx, service.BotConfig{Name: "binding-ui", Platform: "telegram"})
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		r := httptest.NewRequest(method, "/api/v1/bots/"+bot.ID+"/execution-binding", strings.NewReader(fmt.Sprintf(`{"run_as_user_id":%q}`, f.member))).WithContext(f.ctx)
		r.SetPathValue("id", bot.ID)
		w := httptest.NewRecorder()
		f.s.RuntimeBotBindingAPI(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", method, w.Code, w.Body.String())
		}
		if method == http.MethodGet && !strings.Contains(w.Body.String(), "machine-member") {
			t.Fatalf("eligible identity absent: %s", w.Body.String())
		}
	}
}

func TestBotMachineCredentialsDoNotDependOnSecretDTOGrants(t *testing.T) {
	f := newMachineFixture(t)
	member, err := f.store.CreateAuthUser(t.Context(), service.AuthUser{Username: "bot-runner"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetWorkspaceMember(f.ctx, service.WorkspaceMembership{WorkspaceID: f.workspace, UserID: member.ID, Role: "member", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	bot, err := f.store.CreateBotConfig(f.ctx, service.BotConfig{Name: "token-dto", Platform: "invalid-platform", Token: "actual-platform-token"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.saveRuntimeBinding(f.ctx, "bot", bot.ID, false, member.ID); err != nil {
		t.Fatal(err)
	}
	ctx, err := f.s.ResumeRuntimeSubject(t.Context(), "bot", bot.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	dto, err := f.store.GetBotConfig(ctx, bot.ID)
	if err != nil || dto == nil || dto.Token != "***" {
		t.Fatalf("human DTO leaked token: %+v %v", dto, err)
	}
	actual, err := f.store.GetExecutionBotConfig(ctx, bot.ID)
	if err != nil || actual == nil || actual.Token != bot.Token {
		t.Fatalf("dispatcher did not receive token: %+v %v", actual, err)
	}
	if _, err := f.store.GetExecutionBotConfig(f.ctx, bot.ID); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatalf("human principal entered machine credential accessor: %v", err)
	}
	// Unsupported platform exercises the adapter failure cleanup without making
	// an external Telegram/Discord request.
	if err := f.s.startBotFromConfig(t.Context(), dto); err == nil || f.s.isBotRunning(bot.ID) {
		t.Fatalf("failed adapter remained running: %v", err)
	}
}
