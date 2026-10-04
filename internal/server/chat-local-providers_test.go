package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func localProviderRequest(s *Server, token, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/at/api/v1/chats/local-providers"+path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("X-AT-Workspace-ID", "legacy-default")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.server.ServeHTTP(w, r)

	return w
}

func decodeLocalProviderRegistry(t *testing.T, w *httptest.ResponseRecorder) localChatProviderRegistry {
	t.Helper()
	var out localChatProviderRegistry
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", w.Body, err)
	}

	return out
}

// The registry is per account; the API key and header values are secrets
// revealed per record, and a replayed sentinel preserves what is stored.
func TestLocalProviderRegistryOwnerScopeAndSecrets(t *testing.T) {
	s, owners, tokens := playgroundFixture(t)

	if w := localProviderRequest(s, "", "GET", "", ""); w.Code != 401 {
		t.Fatalf("anonymous: %d %s", w.Code, w.Body)
	}
	w := localProviderRequest(s, tokens[0], "GET", "", "")
	if w.Code != 200 || len(decodeLocalProviderRegistry(t, w).Providers) != 0 {
		t.Fatalf("empty registry: %d %s", w.Code, w.Body)
	}

	saved := `{"providers":[{"name":"Ollama","base_url":"http://127.0.0.1:11434/v1/","api_key":"sk-local","headers":{"X-Org":"acme"}}]}`
	w = localProviderRequest(s, tokens[0], "PUT", "", saved)
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	created := decodeLocalProviderRegistry(t, w)
	if len(created.Providers) != 1 || created.Providers[0].ID == "" {
		t.Fatalf("no id minted: %s", w.Body)
	}
	p := created.Providers[0]
	id := p.ID
	if p.Name != "ollama" || p.BaseURL != "http://127.0.0.1:11434/v1" {
		t.Fatalf("not normalized: %+v", p)
	}
	if p.APIKey != redactedSecret || p.Headers["X-Org"] != redactedSecret {
		t.Fatalf("secrets echoed back: %s", w.Body)
	}

	type revealed struct {
		APIKey  string            `json:"api_key"`
		Headers map[string]string `json:"headers"`
	}
	reveal := func(token string) (int, revealed) {
		w := localProviderRequest(s, token, "POST", "/"+id+"/reveal", "")
		var out revealed
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	if code, got := reveal(tokens[0]); code != 200 || got.APIKey != "sk-local" || got.Headers["X-Org"] != "acme" {
		t.Fatalf("reveal: %d %+v", code, got)
	}

	rename := `{"providers":[{"id":"` + id + `","name":"desk","base_url":"http://127.0.0.1:11434/v1","api_key":"***","headers":{"X-Org":"***"}}]}`
	if w = localProviderRequest(s, tokens[0], "PUT", "", rename); w.Code != 200 {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	if code, got := reveal(tokens[0]); code != 200 || got.APIKey != "sk-local" || got.Headers["X-Org"] != "acme" {
		t.Fatalf("sentinel replay lost secrets: %d %+v", code, got)
	}

	orphan := `{"providers":[{"name":"new","base_url":"http://127.0.0.1:1234/v1","api_key":"***"}]}`
	if w = localProviderRequest(s, tokens[0], "PUT", "", orphan); w.Code != 400 {
		t.Fatalf("orphan sentinel: %d %s", w.Code, w.Body)
	}

	// Another account sees nothing and cannot reveal this record.
	w = localProviderRequest(s, tokens[1], "GET", "", "")
	if w.Code != 200 || len(decodeLocalProviderRegistry(t, w).Providers) != 0 {
		t.Fatalf("registry leaked: %d %s", w.Code, w.Body)
	}
	if code, _ := reveal(tokens[1]); code != 404 {
		t.Fatalf("foreign reveal: %d", code)
	}

	pref, err := s.userPrefStore.GetUserPreference(t.Context(), owners[0], localChatProvidersKey)
	if err != nil || pref == nil || !pref.Secret {
		t.Fatalf("registry not stored as a secret preference: %+v %v", pref, err)
	}
}

func TestNormalizeLocalChatProviders(t *testing.T) {
	tests := []struct {
		name    string
		in      service.LocalChatProvider
		wantErr string
	}{
		{name: "loopback http", in: service.LocalChatProvider{Name: "ollama", BaseURL: "http://localhost:11434/v1"}},
		{name: "lan http", in: service.LocalChatProvider{Name: "lab", BaseURL: "http://192.168.1.20:8000/v1"}},
		{name: "public https", in: service.LocalChatProvider{Name: "openai", BaseURL: "https://api.openai.com/v1"}},
		{name: "public http", in: service.LocalChatProvider{Name: "x", BaseURL: "http://api.example.com/v1"}, wantErr: "use https"},
		{name: "slash in name", in: service.LocalChatProvider{Name: "a/b", BaseURL: "http://localhost/v1"}, wantErr: "name must"},
		{name: "credentials in url", in: service.LocalChatProvider{Name: "x", BaseURL: "https://u:p@api.example.com/v1"}, wantErr: "credentials"},
		{name: "query", in: service.LocalChatProvider{Name: "x", BaseURL: "https://api.example.com/v1?k=1"}, wantErr: "query"},
		{name: "scheme", in: service.LocalChatProvider{Name: "x", BaseURL: "ftp://localhost/v1"}, wantErr: "http or https"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.NormalizeLocalChatProviders([]service.LocalChatProvider{tt.in})
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}

	if _, err := service.NormalizeLocalChatProviders([]service.LocalChatProvider{
		{Name: "a", BaseURL: "http://localhost/v1"},
		{Name: "A", BaseURL: "http://localhost/v2"},
	}); err == nil {
		t.Fatal("duplicate names accepted")
	}
}

// Local providers are Chats configuration on the Chats entry capability, with
// their own feature switch.
func TestLocalProviderAdmission(t *testing.T) {
	policies := map[string]string{}
	for _, p := range workspaceBusinessPolicies() {
		policies[p.Method+" "+p.Pattern] = p.Capability
	}
	for _, pattern := range []string{
		"GET /chats/local-providers",
		"PUT /chats/local-providers",
		"POST /chats/local-providers/{id}/reveal",
		"POST /chats/generation-observations",
	} {
		if got := policies[pattern]; got != "models.use" {
			t.Errorf("%s admitted on %q, want models.use", pattern, got)
		}
	}
	for _, path := range []string{"/at/api/v1/chats/local-providers", "/at/api/v1/chats/generation-observations"} {
		if got := featureKeyForRoute(path, "GET", "/at"); got != service.FeatureChatLocalProviders {
			t.Errorf("%s feature = %q, want %q", path, got, service.FeatureChatLocalProviders)
		}
	}
}

// A browser-called generation is recorded as client-asserted and never
// priced: the call was not made or paid through AT.
func TestLocalGenerationObservationIsUnpriced(t *testing.T) {
	s := &Server{}
	call := s.buildLLMCall(t.Context(), llmAuditParams{
		source:         "chat",
		fullModel:      "local:ollama/llama3",
		usage:          service.Usage{PromptTokens: 1000, CompletionTokens: 1000},
		noCostEstimate: true,
	})
	if call.CostCents != 0 {
		t.Fatalf("local generation priced at %v", call.CostCents)
	}
	if call.Provider != "local:ollama" || call.Model != "llama3" {
		t.Fatalf("provider/model = %q/%q", call.Provider, call.Model)
	}
}
