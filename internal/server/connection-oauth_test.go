package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// TestInlineToolUsesCallersPersonalConnection covers the Chats case: no agent
// binding exists, so a skill tool reading youtube_* resolves the caller's own
// authorized personal connection rather than the global variables.
func TestInlineToolUsesCallersPersonalConnection(t *testing.T) {
	f := newMachineFixture(t)
	f.s.connectionStore = f.store
	f.s.variableStore = f.store
	admin := mustPrincipal(t, f.ctx)
	if _, err := f.store.CreateVariable(f.ctx, service.Variable{Key: "youtube_refresh_token", Value: "global-token", Secret: true}); err != nil {
		t.Fatal(err)
	}
	tool := service.MCPInlineTool{SourceSkillID: "yt", Handler: `getVar('youtube_refresh_token'); getVar('youtube_client_id')`}
	lookup := func() string {
		t.Helper()
		get, _, err := f.s.inlineToolVariables(f.ctx, tool)
		if err != nil {
			t.Fatal(err)
		}
		v, err := get("youtube_refresh_token")
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if got := lookup(); got != "global-token" {
		t.Fatalf("without a personal connection: %q", got)
	}
	// An unauthorized personal row does not shadow the global fallback.
	if _, err := f.store.CreateConnection(f.ctx, service.Connection{OwnerUserID: admin.UserID, Provider: "youtube", Name: "draft", Credentials: service.ConnectionCredentials{ClientID: "cid"}}); err != nil {
		t.Fatal(err)
	}
	if got := lookup(); got != "global-token" {
		t.Fatalf("half-configured connection shadowed globals: %q", got)
	}
	if _, err := f.store.CreateConnection(f.ctx, service.Connection{OwnerUserID: admin.UserID, Provider: "youtube", Name: "mine", Credentials: service.ConnectionCredentials{ClientID: "cid", RefreshToken: "my-token"}}); err != nil {
		t.Fatal(err)
	}
	if got := lookup(); got != "my-token" {
		t.Fatalf("personal connection not used: %q", got)
	}
}

func TestConnectionOAuthPolicy(t *testing.T) {
	for _, tt := range []struct {
		method, target string
		scoped         bool
	}{
		{http.MethodGet, "/api/v1/oauth/start?provider=youtube&connection_id=c1", true},
		{http.MethodGet, "/api/v1/oauth/start?provider=youtube", false},
		{http.MethodGet, "/api/v1/oauth/manual-url?provider=youtube&connection_id=c1", true},
		{http.MethodPost, "/api/v1/oauth/exchange?connection_id=c1", true},
		{http.MethodPost, "/api/v1/oauth/exchange", false},
		{http.MethodGet, "/api/v1/oauth/callback?state=ws1.nonce", true},
		{http.MethodGet, "/api/v1/oauth/callback?state=nonce", false},
		{http.MethodGet, "/api/v1/oauth/connections", false},
	} {
		r := httptest.NewRequest(tt.method, tt.target, nil)
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		if got := connectionOAuthPolicy(r, path) != nil; got != tt.scoped {
			t.Errorf("%s %s scoped=%v want %v", tt.method, tt.target, got, tt.scoped)
		}
	}
}

func TestConnectionOverviewAndBinding(t *testing.T) {
	f := newMachineFixture(t)
	f.s.connectionStore = f.store
	f.s.variableStore = f.store
	f.s.skillStore = f.store
	admin := mustPrincipal(t, f.ctx)
	skill, err := f.store.CreateSkill(f.ctx, service.Skill{Name: "yt_publish", SystemPrompt: "## Legacy tool references\n\n```javascript\nvar t = getVar('youtube_refresh_token');\n```\n"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := f.store.CreateAgent(f.ctx, service.Agent{Name: "YT", Config: service.AgentConfig{Skills: []service.SkillRef{{ID: skill.Name}}}})
	if err != nil {
		t.Fatal(err)
	}
	shared, err := f.store.CreateConnection(f.ctx, service.Connection{Provider: "youtube", Name: "channel", Credentials: service.ConnectionCredentials{RefreshToken: "r"}})
	if err != nil {
		t.Fatal(err)
	}
	personal, err := f.store.CreateConnection(f.ctx, service.Connection{OwnerUserID: admin.UserID, Provider: "youtube", Name: "mine", Credentials: service.ConnectionCredentials{RefreshToken: "r"}})
	if err != nil {
		t.Fatal(err)
	}

	overview := func() connectionOverviewProvider {
		t.Helper()
		w := httptest.NewRecorder()
		f.s.ConnectionOverviewAPI(w, httptest.NewRequest(http.MethodGet, "/api/v1/connections/overview", nil).WithContext(f.ctx))
		var out connectionOverview
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("overview: %d %s", w.Code, w.Body.String())
		}
		for _, p := range out.Providers {
			if p.Provider == "youtube" {
				return p
			}
		}
		t.Fatalf("youtube missing: %s", w.Body.String())
		return connectionOverviewProvider{}
	}
	p := overview()
	if len(p.Agents) != 1 || p.Agents[0].ID != agent.ID || p.Agents[0].ConnectionID != "" || len(p.Agents[0].Skills) != 1 || !p.Agents[0].Editable {
		t.Fatalf("agent needing youtube not reported as falling back: %+v", p)
	}

	bind := func(connID string, body string) []connectionBindResult {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/connections/"+connID+"/agents", strings.NewReader(body)).WithContext(f.ctx)
		r.SetPathValue("id", connID)
		w := httptest.NewRecorder()
		f.s.BindConnectionAgentsAPI(w, r)
		var out struct {
			Results []connectionBindResult `json:"results"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("bind: %d %s", w.Code, w.Body.String())
		}
		return out.Results
	}
	// A personal account may not be bound to a workspace agent.
	if res := bind(personal.ID, `{"agent_ids":["`+agent.ID+`"]}`); res[0].OK || !strings.Contains(res[0].Error, "personal") {
		t.Fatalf("personal on workspace agent: %+v", res)
	}
	if res := bind(shared.ID, `{"agent_ids":["`+agent.ID+`","missing"]}`); !res[0].OK || res[1].OK {
		t.Fatalf("bind results: %+v", res)
	}
	if p := overview(); p.Agents[0].ConnectionID != shared.ID || p.Agents[0].Level != "agent" {
		t.Fatalf("binding not reflected: %+v", p.Agents)
	}
	if res := bind(shared.ID, `{"agent_ids":["`+agent.ID+`"],"unbind":true}`); !res[0].OK {
		t.Fatalf("unbind: %+v", res)
	}
	if p := overview(); p.Agents[0].ConnectionID != "" {
		t.Fatalf("unbind not reflected: %+v", p.Agents)
	}
}

func TestConnectionTestRefreshesToken(t *testing.T) {
	var refreshToken string
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		if r.Form.Get("refresh_token") != "good" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`))
			return
		}
		refreshToken = "rotated"
		_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rotated","expires_in":3600}`))
	}))
	defer tokenSrv.Close()

	f := newMachineFixture(t)
	f.s.connectionStore = f.store
	f.s.connectorStore = f.store
	if _, err := f.store.CreateConnector(f.ctx, service.Connector{Slug: "acme", Name: "Acme", AuthKind: service.ConnectorAuthOAuth2, OAuth: &service.ConnectorOAuth{AuthURL: "https://acme.example/auth", TokenURL: tokenSrv.URL, AccessType: "offline"}}); err != nil {
		t.Fatal(err)
	}
	conn, err := f.store.CreateConnection(f.ctx, service.Connection{Provider: "acme", Name: "main", Credentials: service.ConnectionCredentials{ClientID: "c", ClientSecret: "s", RefreshToken: "good"}})
	if err != nil {
		t.Fatal(err)
	}
	test := func() map[string]any {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/connections/"+conn.ID+"/test", nil).WithContext(f.ctx)
		r.SetPathValue("id", conn.ID)
		w := httptest.NewRecorder()
		f.s.TestConnectionAPI(w, r)
		var out map[string]any
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("test: %d %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), `"at"`) {
			t.Fatal("access token leaked in the response")
		}
		return out
	}
	if out := test(); out["ok"] != true {
		t.Fatalf("first test: %+v", out)
	}
	stored, _ := f.store.GetConnection(f.ctx, conn.ID)
	if stored.Credentials.RefreshToken != refreshToken || stored.Metadata["oauth_status"] != "verified" {
		t.Fatalf("rotation/metadata not saved: %+v %+v", stored.Credentials.RefreshToken, stored.Metadata)
	}
	// The rotated token is not "good" any more: the next test fails and says so.
	if out := test(); out["ok"] != false || !strings.Contains(out["message"].(string), "invalid_grant") {
		t.Fatalf("second test: %+v", out)
	}
	stored, _ = f.store.GetConnection(f.ctx, conn.ID)
	if stored.Metadata["oauth_status"] != "failed" {
		t.Fatalf("failure not recorded: %+v", stored.Metadata)
	}
}

func TestConnectorSaveRequiresSlugPrefix(t *testing.T) {
	c := service.Connector{Slug: "twitch", Fields: []service.ConnectorField{{Key: "twitch_client_id"}, {Key: "client_secret"}}}
	if problem := connectorFieldPrefixProblem(c); !strings.Contains(problem, `"client_secret"`) {
		t.Fatalf("problem = %q", problem)
	}
	c.Fields[1].Key = "twitch_client_secret"
	if problem := connectorFieldPrefixProblem(c); problem != "" {
		t.Fatalf("problem = %q", problem)
	}
}
