package server

import (
	"bufio"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

type agentGatewayIdP struct {
	srv *httptest.Server
	key *rsa.PrivateKey
}

func newAgentGatewayIdP(t *testing.T) *agentGatewayIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &agentGatewayIdP{key: key}
	idp.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{
			"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	t.Cleanup(idp.srv.Close)
	return idp
}

func (i *agentGatewayIdP) token(t *testing.T, claims map[string]any) string {
	t.Helper()
	base := map[string]any{"iss": "https://idp.example", "aud": "website", "exp": time.Now().Add(5 * time.Minute).Unix(), "iat": time.Now().Unix()}
	for k, v := range claims {
		base[k] = v
	}
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"k1","typ":"JWT"}`))
	payload, _ := json.Marshal(base)
	body := h + "." + base64.RawURLEncoding.EncodeToString(payload)
	sum := sha256.Sum256([]byte(body))
	sig, err := rsa.SignPKCS1v15(rand.Reader, i.key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return body + "." + base64.RawURLEncoding.EncodeToString(sig)
}

type sseEvent struct {
	name string
	data map[string]any
}

func readSSE(t *testing.T, body string) []sseEvent {
	t.Helper()
	var out []sseEvent
	var name string
	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			var data map[string]any
			_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data)
			out = append(out, sseEvent{name: name, data: data})
		}
	}
	return out
}

func findEvent(events []sseEvent, name string) map[string]any {
	for _, e := range events {
		if e.name == name {
			return e.data
		}
	}
	return nil
}

// TestAgentGatewayEndToEnd drives the agent gateway as a website backend
// does: an act_for_users token plus an IdP token for the person. The person
// has never signed in to AT; an SSO group mapping admits them to the
// workspace on first use, the run executes as them (session owner, cost
// user, observation user), and a client tool round-trips through
// client_tool_calls and tool_results.
func TestAgentGatewayEndToEnd(t *testing.T) {
	f := newMachineFixture(t)
	idp := newAgentGatewayIdP(t)
	f.s.config.NativeAuth = &config.NativeAuth{InsecureHTTP: true}

	provider, err := f.store.SaveAuthIdentityProvider(t.Context(), service.AuthIdentityProvider{
		Label: "Company SSO", Enabled: true, ClientID: "website", SubjectClaim: "sub", AuthURL: "https://idp.example/auth", TokenURL: "https://idp.example/token",
		JWKSURL: idp.srv.URL, Scopes: []string{"openid"}, RolesClaims: []string{"groups"},
		TokenExchange: true, Issuer: "https://idp.example", Audiences: []string{"website"}, ExchangeProvisioning: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SavePermissionMapping(f.ctx, service.PermissionMapping{ProviderID: provider.ID, ClaimKind: "roles", ClaimValue: "site-users", AdmitRole: "member"}); err != nil {
		t.Fatal(err)
	}

	if _, err := f.store.CreateProvider(f.ctx, service.ProviderRecord{Key: "test", Config: config.LLMConfig{Type: "openai", Model: "test", APIKey: "fixture"}}); err != nil {
		t.Fatal(err)
	}
	llm := &fakeObsProvider{responses: []*service.LLMResponse{
		{Content: "Let me check your cart.", ToolCalls: []service.ToolCall{{ID: "call_cart", Name: "get_cart", Arguments: map[string]any{"currency": "TRY"}}}},
		{Content: "You have 2 items.", Finished: true},
	}}
	f.s.providerFactory = func(config.LLMConfig) (service.LLMProvider, error) { return llm, nil }
	_, err = f.store.CreateAgent(f.ctx, service.Agent{Name: "shop-assistant", Config: service.AgentConfig{Provider: "test", Model: "test"}})
	if err != nil {
		t.Fatal(err)
	}

	raw := "at_site_token"
	hash := sha256.Sum256([]byte(raw))
	site, err := f.store.CreateAPIToken(f.ctx, service.APIToken{Name: "website", ActForUsers: true, AllowedAgentsMode: "list", AllowedAgents: []string{"shop-assistant"}}, hex.EncodeToString(hash[:]))
	if err != nil || !site.ActForUsers {
		t.Fatalf("token: %+v %v", site, err)
	}
	plainRaw := "at_plain_token"
	plainHash := sha256.Sum256([]byte(plainRaw))
	if _, err := f.store.CreateAPIToken(f.ctx, service.APIToken{Name: "plain"}, hex.EncodeToString(plainHash[:])); err != nil {
		t.Fatal(err)
	}

	call := func(token, agentRef string, body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPost, "/gateway/v1/agents/"+agentRef+"/chat", strings.NewReader(string(raw)))
		r.Header.Set("Authorization", "Bearer "+token)
		r.SetPathValue("agent", agentRef)
		w := httptest.NewRecorder()
		f.s.AgentGatewayChatAPI(w, r)
		return w
	}
	person := idp.token(t, map[string]any{"sub": "u-1001", "preferred_username": "ayse", "email": "ayse@example.com", "email_verified": true, "groups": []string{"site-users"}})
	tools := []map[string]any{{"name": "get_cart", "description": "Read the shopping cart in the browser", "input_schema": map[string]any{"type": "object", "properties": map[string]any{"currency": map[string]any{"type": "string"}}}}}

	// Refusals before any run.
	if w := call(plainRaw, "shop-assistant", map[string]any{"subject_token": person, "message": "hi"}); w.Code != http.StatusForbidden {
		t.Fatalf("plain token acted for a user: %d %s", w.Code, w.Body.String())
	}
	forged := strings.Replace(person, person[len(person)-10:], "AAAAAAAAAA", 1)
	if w := call(raw, "shop-assistant", map[string]any{"subject_token": forged, "message": "hi"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("forged subject token: %d %s", w.Code, w.Body.String())
	}
	otherAudience := idp.token(t, map[string]any{"sub": "u-1001", "aud": "another-service"})
	if w := call(raw, "shop-assistant", map[string]any{"subject_token": otherAudience, "message": "hi"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("token for another audience accepted: %d", w.Code)
	}
	outsider := idp.token(t, map[string]any{"sub": "u-2002", "groups": []string{"guests"}})
	if w := call(raw, "shop-assistant", map[string]any{"subject_token": outsider, "message": "hi"}); w.Code != http.StatusForbidden {
		t.Fatalf("unmapped person reached the workspace: %d %s", w.Code, w.Body.String())
	}
	other, err := f.store.CreateAgent(f.ctx, service.Agent{Name: "admin-agent", Config: service.AgentConfig{Provider: "test", Model: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	if w := call(raw, other.ID, map[string]any{"subject_token": person, "message": "hi"}); w.Code != http.StatusNotFound {
		t.Fatalf("agent outside allowed_agents: %d", w.Code)
	}

	// 1. First turn: the model calls the client tool; the stream asks for it.
	w := call(raw, "shop-assistant", map[string]any{"subject_token": person, "message": "What is in my cart?", "client_tools": tools})
	if w.Code != http.StatusOK {
		t.Fatalf("turn 1: %d %s", w.Code, w.Body.String())
	}
	events := readSSE(t, w.Body.String())
	sessionEvt := findEvent(events, "session")
	pending := findEvent(events, "client_tool_calls")
	if sessionEvt == nil || pending == nil || findEvent(events, "done")["status"] != "requires_action" {
		t.Fatalf("turn 1 events: %s", w.Body.String())
	}
	sessionID := sessionEvt["session_id"].(string)
	userID := sessionEvt["user_id"].(string)
	calls := pending["calls"].([]any)
	if len(calls) != 1 || calls[0].(map[string]any)["tool_call_id"] != "call_cart" || calls[0].(map[string]any)["arguments"].(map[string]any)["currency"] != "TRY" {
		t.Fatalf("client tool calls: %v", calls)
	}
	offered := false
	for _, tool := range llm.tools[0] {
		offered = offered || tool.Name == "get_cart"
	}
	if !offered {
		t.Fatal("client tool not offered to the model")
	}

	// The run executed as the person: they were provisioned, admitted by the
	// group mapping, and own the session.
	session, err := f.store.GetChatSession(f.ctx, sessionID)
	if err != nil || session == nil || session.OwnerUserID != userID || session.Config.GatewayTokenID != site.ID {
		t.Fatalf("session: %+v %v", session, err)
	}
	if userID == "" || userID == f.member {
		t.Fatalf("unexpected user %q", userID)
	}
	if _, _, err := f.store.ResolveWorkspaceAccess(t.Context(), f.workspace, userID, ""); err != nil {
		t.Fatalf("claim admission did not make the person a member: %v", err)
	}

	// A new message while the call is open is refused.
	if w := call(raw, "shop-assistant", map[string]any{"subject_token": person, "session_id": sessionID, "message": "hello?"}); w.Code != http.StatusConflict {
		t.Fatalf("message accepted while waiting for tool results: %d", w.Code)
	}
	// Another person cannot continue this session.
	stranger := idp.token(t, map[string]any{"sub": "u-3003", "groups": []string{"site-users"}})
	if w := call(raw, "shop-assistant", map[string]any{"subject_token": stranger, "session_id": sessionID, "tool_results": []map[string]any{{"tool_call_id": "call_cart", "content": "x"}}}); w.Code != http.StatusNotFound {
		t.Fatalf("another person continued the session: %d", w.Code)
	}

	// 2. Continuation with the tool result: the model sees it and answers.
	w = call(raw, "shop-assistant", map[string]any{"subject_token": person, "session_id": sessionID, "client_tools": tools,
		"tool_results": []map[string]any{{"tool_call_id": "call_cart", "content": `{"items":2}`}}})
	events = readSSE(t, w.Body.String())
	if w.Code != http.StatusOK || findEvent(events, "done")["status"] != "completed" {
		t.Fatalf("turn 2: %d %s", w.Code, w.Body.String())
	}
	final := ""
	for _, e := range events {
		if e.name == "message" && e.data["final"] == true {
			final = e.data["content"].(string)
		}
	}
	if final != "You have 2 items." {
		t.Fatalf("final answer %q", final)
	}
	sawResult := false
	for _, m := range llm.requests[1] {
		if blocks, ok := m.Content.([]service.ContentBlock); ok {
			for _, b := range blocks {
				sawResult = sawResult || (b.Type == "tool_result" && b.ToolUseID == "call_cart" && strings.Contains(b.ContentText(), `"items":2`))
			}
		}
	}
	if !sawResult {
		t.Fatalf("model did not receive the client tool result: %+v", llm.requests[1])
	}
	msgs, _ := f.store.ListChatMessages(f.ctx, sessionID, 20)
	users := 0
	for _, m := range msgs {
		if m.Role == "user" {
			users++
		}
	}
	if users != 1 {
		t.Fatalf("continuation stored an extra user message: %d", users)
	}

	// Pausing the token stops further runs.
	if err := f.store.SetAPITokenPaused(f.ctx, site.ID, true, "test"); err != nil {
		t.Fatal(err)
	}
	if w := call(raw, "shop-assistant", map[string]any{"subject_token": person, "message": "again"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("paused token still runs: %d", w.Code)
	}
}

// A run bound by an act_for_users token stops when the token loses the
// capability, even though the person's own access is unchanged.
func TestAgentGatewayRevalidation(t *testing.T) {
	f := newMachineFixture(t)
	raw := sha256.Sum256([]byte("at_revalidate"))
	tok, err := f.store.CreateAPIToken(f.ctx, service.APIToken{Name: "site", ActForUsers: true}, hex.EncodeToString(raw[:]))
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := f.s.bindAgentGatewayUser(t.Context(), tok, f.member)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "execution.run"}); err != nil {
		t.Fatalf("bound run refused: %v", err)
	}
	if _, err := f.store.UpdateAPIToken(f.ctx, tok.ID, service.APIToken{Name: "site", ActForUsers: false}); err != nil {
		t.Fatal(err)
	}
	agentGatewayActorCache.Range(func(k, _ any) bool { agentGatewayActorCache.Delete(k); return true })
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "execution.run"}); err == nil {
		t.Fatal("run continued after act_for_users was turned off")
	}
}
