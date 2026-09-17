package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

// A disabled provider keeps its stored credentials but must disappear from the
// workspace catalog and be refused by every scoped execution path.
func TestDisabledProviderIsHiddenAndRefusedForExecution(t *testing.T) {
	f := newMachineFixture(t)
	if _, err := f.store.CreateProvider(f.ctx, service.ProviderRecord{Key: "parked", Config: config.LLMConfig{Type: "openai", Model: "m1", Models: []string{"m1"}, APIKey: "parked-secret"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateProvider(f.ctx, service.ProviderRecord{Key: "live", Config: config.LLMConfig{Type: "openai", Model: "m2", APIKey: "live-secret"}}); err != nil {
		t.Fatal(err)
	}
	catalogKeys := func() []string {
		t.Helper()
		w := httptest.NewRecorder()
		f.s.InfoAPI(w, httptest.NewRequest(http.MethodGet, "/api/v1/info", nil).WithContext(f.ctx))
		var response infoResponse
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil {
			t.Fatalf("workspace info: %d %s", w.Code, w.Body.String())
		}
		keys := make([]string, 0, len(response.Providers))
		for _, p := range response.Providers {
			keys = append(keys, p.Key)
		}
		return keys
	}
	if got := catalogKeys(); len(got) != 2 {
		t.Fatalf("catalog before disabling: %v", got)
	}
	if _, err := f.store.ResolveWorkspaceProviderForUse(f.ctx, "parked", "m1"); err != nil {
		t.Fatalf("provider unusable before disabling: %v", err)
	}

	if err := f.store.SetProviderDisabled(f.ctx, "parked", true, "admin@example.com"); err != nil {
		t.Fatalf("disable provider: %v", err)
	}

	if _, err := f.store.ResolveWorkspaceProviderForUse(f.ctx, "parked", "m1"); !errors.Is(err, service.ErrProviderDisabled) {
		t.Fatalf("disabled provider still resolved credentials: %v", err)
	}
	// The disabled error must keep behaving as a denial for existing callers.
	if _, err := f.store.ResolveWorkspaceProviderForUse(f.ctx, "parked", "m1"); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatal("disabled provider error is not a denial")
	}
	if _, err := f.store.ExecutionProviderDefaultModel(f.ctx, "parked"); !errors.Is(err, service.ErrProviderDisabled) {
		t.Fatalf("disabled provider returned execution metadata: %v", err)
	}
	if _, _, err := f.s.runtimeProviderLookup(f.ctx, "parked"); !errors.Is(err, service.ErrProviderDisabled) {
		t.Fatalf("runtime lookup admitted a disabled provider: %v", err)
	}
	if got := catalogKeys(); len(got) != 1 || got[0] != "live" {
		t.Fatalf("catalog after disabling: %v", got)
	}
	// Availability is not credential deletion: the configuration survives.
	stored, err := f.store.GetProvider(f.ctx, "parked")
	if err != nil || stored == nil {
		t.Fatalf("read disabled provider: %+v %v", stored, err)
	}
	if stored.Config.APIKey != "parked-secret" || len(stored.Config.Models) != 1 || !stored.Config.Disabled {
		t.Fatalf("disabling damaged the configuration: %+v", stored.Config)
	}

	// Discovery must not spend the credentials of a parked provider either.
	body := `{"key":"parked","config":{"type":"openai"}}`
	w := httptest.NewRecorder()
	f.s.DiscoverModelsAPI(w, httptest.NewRequest(http.MethodPost, "/api/v1/providers/discover-models", strings.NewReader(body)).WithContext(f.ctx))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "disabled") {
		t.Fatalf("discovery on a disabled provider: %d %s", w.Code, w.Body.String())
	}

	// An ordinary config save must not silently resume the provider.
	update := httptest.NewRequest(http.MethodPut, "/api/v1/providers/parked", strings.NewReader(`{"config":{"type":"openai","model":"m1","models":["m1","m3"]}}`)).WithContext(f.ctx)
	update.SetPathValue("key", "parked")
	w = httptest.NewRecorder()
	f.s.UpdateProviderAPI(w, update)
	if w.Code != http.StatusOK {
		t.Fatalf("update disabled provider: %d %s", w.Code, w.Body.String())
	}
	stored, err = f.store.GetProvider(f.ctx, "parked")
	if err != nil || stored == nil || !stored.Config.Disabled {
		t.Fatalf("config save resumed a disabled provider: %+v %v", stored, err)
	}
	if len(stored.Config.Models) != 2 {
		t.Fatalf("config save did not apply: %+v", stored.Config)
	}

	// Enabling through the endpoint restores use with the original credentials.
	enable := httptest.NewRequest(http.MethodPut, "/api/v1/providers/parked/disable", strings.NewReader(`{"disabled":false}`)).WithContext(f.ctx)
	enable.SetPathValue("key", "parked")
	w = httptest.NewRecorder()
	f.s.SetProviderDisabledAPI(w, enable)
	if w.Code != http.StatusOK {
		t.Fatalf("enable provider: %d %s", w.Code, w.Body.String())
	}
	resolved, err := f.store.ResolveWorkspaceProviderForUse(f.ctx, "parked", "m1")
	if err != nil || resolved == nil || resolved.Config.APIKey != "parked-secret" {
		t.Fatalf("enabled provider unusable: %+v %v", resolved, err)
	}
	if got := catalogKeys(); len(got) != 2 {
		t.Fatalf("catalog after enabling: %v", got)
	}
}

func TestSetProviderDisabledRejectsUnknownKeyAndBadBody(t *testing.T) {
	f := newMachineFixture(t)
	missing := httptest.NewRequest(http.MethodPut, "/api/v1/providers/ghost/disable", strings.NewReader(`{"disabled":true}`)).WithContext(f.ctx)
	missing.SetPathValue("key", "ghost")
	w := httptest.NewRecorder()
	f.s.SetProviderDisabledAPI(w, missing)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown provider: %d %s", w.Code, w.Body.String())
	}

	if _, err := f.store.CreateProvider(f.ctx, service.ProviderRecord{Key: "p", Config: config.LLMConfig{Type: "openai", Model: "m"}}); err != nil {
		t.Fatal(err)
	}
	// An absent field is not an implicit "false": it must be rejected.
	bad := httptest.NewRequest(http.MethodPut, "/api/v1/providers/p/disable", strings.NewReader(`{}`)).WithContext(f.ctx)
	bad.SetPathValue("key", "p")
	w = httptest.NewRecorder()
	f.s.SetProviderDisabledAPI(w, bad)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing disabled field: %d %s", w.Code, w.Body.String())
	}
}

// The gateway registry must fail closed for a disabled provider on every
// surface, and say why rather than claiming the provider does not exist.
func TestDisabledProviderIsRefusedByGateway(t *testing.T) {
	provider := &proxyCaptureProvider{}
	s := &Server{
		providers: map[string]ProviderInfo{
			"parked": NewProviderInfo(provider, config.LLMConfig{Type: "anthropic", Model: "m1", Models: []string{"m1"}, EmbeddingModels: []string{"e1"}, Disabled: true}),
			"live":   NewProviderInfo(provider, config.LLMConfig{Type: "anthropic", Model: "m2", Models: []string{"m2"}}),
		},
		tokenStore: gatewayTestToken("secret", service.APIToken{ID: "token"}),
	}
	authorized := func(method, target, body string) *http.Request {
		r := httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer secret")
		return r
	}

	if _, ok := s.getProviderInfo("parked"); ok {
		t.Fatal("registry admitted a disabled provider")
	}
	if keys := s.availableProviderKeys(); len(keys) != 1 || keys[0] != "live" {
		t.Fatalf("available providers: %v", keys)
	}

	w := httptest.NewRecorder()
	s.ListModels(w, authorized(http.MethodGet, "/gateway/v1/models", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("list models: %d %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); strings.Contains(body, "parked/") || !strings.Contains(body, "live/m2") {
		t.Fatalf("disabled provider advertised models: %s", body)
	}

	w = httptest.NewRecorder()
	s.ChatCompletions(w, authorized(http.MethodPost, "/gateway/v1/chat/completions", `{"model":"parked/m1","messages":[{"role":"user","content":"hi"}]}`))
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "is disabled") {
		t.Fatalf("chat on a disabled provider: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	s.Embeddings(w, authorized(http.MethodPost, "/gateway/v1/embeddings", `{"model":"parked/e1","input":"hi"}`))
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "is disabled") {
		t.Fatalf("embeddings on a disabled provider: %d %s", w.Code, w.Body.String())
	}

	proxy := authorized(http.MethodPost, "/gateway/v1/providers/parked/v1/messages", `{"model":"m1"}`)
	proxy.SetPathValue("provider", "parked")
	proxy.SetPathValue("*", "v1/messages")
	w = httptest.NewRecorder()
	s.ProxyRequest(w, proxy)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "is disabled") {
		t.Fatalf("passthrough on a disabled provider: %d %s", w.Code, w.Body.String())
	}
	if provider.called {
		t.Fatal("a disabled provider reached upstream")
	}

	w = httptest.NewRecorder()
	s.HealthOverall(w, httptest.NewRequest(http.MethodGet, "/gateway/v1/health", nil))
	var health struct {
		Providers map[string]string `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if health.Providers["parked"] != "disabled" || health.Providers["live"] != "ok" {
		t.Fatalf("health: %+v", health.Providers)
	}

	healthOne := httptest.NewRequest(http.MethodGet, "/gateway/v1/health/parked", nil)
	healthOne.SetPathValue("provider", "parked")
	w = httptest.NewRecorder()
	s.HealthProvider(w, healthOne)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "disabled") {
		t.Fatalf("provider health: %d %s", w.Code, w.Body.String())
	}
}
