package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func localMCPRequest(s *Server, token, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/at/api/v1/chats/local-mcp-servers"+path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("X-AT-Workspace-ID", "legacy-default")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.server.ServeHTTP(w, r)

	return w
}

func decodeLocalMCPRegistry(t *testing.T, w *httptest.ResponseRecorder) localMCPRegistry {
	t.Helper()
	var out localMCPRegistry
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", w.Body, err)
	}

	return out
}

// The registry is per account, its header values are secret, and the URL it
// stores is the URL the browser will dial — none of which the server itself
// ever connects to.
func TestLocalMCPRegistryOwnerScopeAndSecrets(t *testing.T) {
	s, _, tokens := playgroundFixture(t)

	if w := localMCPRequest(s, "", "GET", "", ""); w.Code != 401 {
		t.Fatalf("anonymous: %d %s", w.Code, w.Body)
	}

	// An account that has never configured one reads an empty registry rather
	// than a 404: the page always asks.
	w := localMCPRequest(s, tokens[0], "GET", "", "")
	if w.Code != 200 || len(decodeLocalMCPRegistry(t, w).Servers) != 0 {
		t.Fatalf("empty registry: %d %s", w.Code, w.Body)
	}

	saved := `{"servers":[{"name":"laptop","url":"http://127.0.0.1:3000/mcp/api","headers":{"Authorization":"Bearer local-secret"}}]}`
	w = localMCPRequest(s, tokens[0], "PUT", "", saved)
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	created := decodeLocalMCPRegistry(t, w)
	if len(created.Servers) != 1 || created.Servers[0].ID == "" {
		t.Fatalf("no id minted: %s", w.Body)
	}
	id := created.Servers[0].ID
	if created.Servers[0].Headers["Authorization"] != redactedSecret {
		t.Fatalf("secret echoed back on write: %s", w.Body)
	}

	// The endpoint path is preserved exactly; nothing appends /mcp.
	w = localMCPRequest(s, tokens[0], "GET", "", "")
	stored := decodeLocalMCPRegistry(t, w)
	if stored.Servers[0].URL != "http://127.0.0.1:3000/mcp/api" {
		t.Fatalf("url rewritten: %q", stored.Servers[0].URL)
	}
	if stored.Servers[0].Headers["Authorization"] != redactedSecret {
		t.Fatalf("secret returned on an ordinary read: %s", w.Body)
	}

	// Reveal is the only way to the real value, and it is per record.
	w = localMCPRequest(s, tokens[0], "POST", "/"+id+"/reveal", "")
	if w.Code != 200 {
		t.Fatalf("reveal: %d %s", w.Code, w.Body)
	}
	var revealed struct {
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &revealed); err != nil {
		t.Fatal(err)
	}
	if revealed.Headers["Authorization"] != "Bearer local-secret" {
		t.Fatalf("reveal did not return the stored value: %s", w.Body)
	}

	// Replaying the sentinel preserves the stored secret: every writer submits
	// the whole record and would otherwise wipe what it never saw.
	rename := `{"servers":[{"id":"` + id + `","name":"desk","url":"http://127.0.0.1:3000/mcp/api","headers":{"Authorization":"***"}}]}`
	if w = localMCPRequest(s, tokens[0], "PUT", "", rename); w.Code != 200 {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	w = localMCPRequest(s, tokens[0], "POST", "/"+id+"/reveal", "")
	if err := json.Unmarshal(w.Body.Bytes(), &revealed); err != nil {
		t.Fatal(err)
	}
	if revealed.Headers["Authorization"] != "Bearer local-secret" {
		t.Fatalf("sentinel replay wiped the secret: %s", w.Body)
	}

	// A sentinel with nothing behind it is an error, not a stored "***".
	orphan := `{"servers":[{"id":"` + id + `","name":"desk","url":"http://127.0.0.1:3000/mcp/api","headers":{"X-Other":"***"}}]}`
	if w = localMCPRequest(s, tokens[0], "PUT", "", orphan); w.Code != 400 {
		t.Fatalf("orphan sentinel: %d %s", w.Code, w.Body)
	}

	// A public endpoint belongs in an MCP set, where execution policy and
	// server-side tracing apply.
	public := `{"servers":[{"name":"remote","url":"https://mcp.example.com/mcp"}]}`
	if w = localMCPRequest(s, tokens[0], "PUT", "", public); w.Code != 400 {
		t.Fatalf("public endpoint accepted: %d %s", w.Code, w.Body)
	}

	// Another account sees its own empty registry and cannot reveal this one.
	w = localMCPRequest(s, tokens[1], "GET", "", "")
	if w.Code != 200 || len(decodeLocalMCPRegistry(t, w).Servers) != 0 {
		t.Fatalf("registry leaked across accounts: %d %s", w.Code, w.Body)
	}
	if w = localMCPRequest(s, tokens[1], "POST", "/"+id+"/reveal", ""); w.Code != 404 {
		t.Fatalf("foreign reveal: %d %s", w.Code, w.Body)
	}

	// Unknown fields are refused like the rest of the Chats surface.
	if w = localMCPRequest(s, tokens[0], "PUT", "", `{"nope":1}`); w.Code != 400 {
		t.Fatalf("unknown field: %d %s", w.Code, w.Body)
	}
}

// The stored blob is written through the preference table's secret path, so a
// database reader does not find the header value in clear.
func TestLocalMCPRegistryStoredEncrypted(t *testing.T) {
	s, owners, tokens := playgroundFixture(t)

	saved := `{"servers":[{"name":"laptop","url":"http://127.0.0.1:3000/mcp","headers":{"Authorization":"Bearer local-secret"}}]}`
	if w := localMCPRequest(s, tokens[0], "PUT", "", saved); w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}

	pref, err := s.userPrefStore.GetUserPreference(t.Context(), owners[0], localMCPServersKey)
	if err != nil || pref == nil {
		t.Fatalf("preference: %v %v", pref, err)
	}
	if !pref.Secret {
		t.Fatal("registry stored without the secret flag; header values would sit in clear")
	}
	// The accessor decrypts, so the returned value is readable; what matters
	// is that it round-trips through the encrypted path.
	if !strings.Contains(string(pref.Value), "local-secret") {
		t.Fatalf("stored value lost the header: %s", pref.Value)
	}
}

// The server stores these endpoints; it must never dial one. A registry entry
// addresses the caller's own machine, so a server-side connection would be a
// request from AT into AT's own network, chosen by any account.
func TestLocalMCPRegistryIsNotAnUpstream(t *testing.T) {
	// The record type deliberately has no conversion into an MCP upstream.
	// This is a compile-time reminder, not a runtime check: the day somebody
	// adds one, this is where the reason lives.
	var srv service.LocalMCPServer
	if _, ok := any(srv).(interface{ Upstream() service.MCPUpstream }); ok {
		t.Fatal("LocalMCPServer gained an upstream conversion; the server must never dial a personal registry entry")
	}

	// Nothing outside the registry handler reads the preference key.
	if localMCPServersKey != "local_mcp_servers" {
		t.Fatalf("preference key renamed to %q; existing registries would silently disappear", localMCPServersKey)
	}
}

// Local MCP endpoints are Chats-only configuration and ride the Chats entry
// capability. Their own feature switch lets an installation refuse the
// capability without turning off Chats.
func TestLocalMCPRegistryAdmission(t *testing.T) {
	policies := map[string]string{}
	for _, p := range workspaceBusinessPolicies() {
		policies[p.Method+" "+p.Pattern] = p.Capability
	}
	for pattern, want := range map[string]string{
		"GET /chats/local-mcp-servers":              "models.use",
		"PUT /chats/local-mcp-servers":              "models.use",
		"POST /chats/local-mcp-servers/{id}/reveal": "models.use",
		"POST /chats/tool-observations":             "models.use",
	} {
		if got := policies[pattern]; got != want {
			t.Errorf("%s admitted on %q, want %q", pattern, got, want)
		}
	}

	if got := featureKeyForRoute("/at/api/v1/chats/local-mcp-servers", "GET", "/at"); got != service.FeatureChatLocalMCP {
		t.Errorf("registry feature = %q, want %q", got, service.FeatureChatLocalMCP)
	}
	// The rest of Chats keeps its own key, so turning the registry off does
	// not take conversations with it.
	if got := featureKeyForRoute("/at/api/v1/chats/conversations", "GET", "/at"); got != service.FeaturePlayground {
		t.Errorf("conversations feature = %q, want %q", got, service.FeaturePlayground)
	}
	if got := featureKeyForRoute("/at/api/v1/chats/tool-observations", "POST", "/at"); got != service.FeaturePlayground {
		t.Errorf("tool observations feature = %q, want %q", got, service.FeaturePlayground)
	}
}
