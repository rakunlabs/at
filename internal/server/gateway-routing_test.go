package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

// gatewayRoutingToken stores a gateway token with a known plaintext, mirroring
// the hashing CreateAPITokenAPI performs.
func gatewayRoutingToken(t *testing.T, ctx context.Context, p *postgres.Postgres, plain string, token service.APIToken) string {
	t.Helper()
	sum := sha256.Sum256([]byte(plain))
	token.Name = plain
	token.TokenPrefix = plain[:8]
	if _, err := p.CreateAPIToken(service.WithLegacyWorkspaceAccess(ctx), token, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	return plain
}

// TestGatewayRouting covers the two ways a misconfigured OpenAI client used to
// be told "this gateway has no models" when the real answer was different:
//
//   - an empty model list serialized as `null`, which clients iterate without a
//     nil check, and
//   - any unmatched path under /gateway falling through to the SPA catch-all,
//     which answers HTTP 200 with index.html.
//
// Both are indistinguishable from an empty registry at the client, so both are
// asserted on the production mux rather than on the handlers in isolation.
func TestGatewayRouting(t *testing.T) {
	p := postgrestest.New(t, nil)
	cfg := nativeTestConfig()
	cfg.Workspace = &config.Workspace{Root: t.TempDir(), TTLHours: -1}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	// Deliberately unsorted keys and models: the response must not depend on
	// Go's map iteration order.
	s, err := New(ctx, cfg, map[string]ProviderInfo{
		"zeta":  {defaultModel: "fallback", models: []string{"delta", "beta"}},
		"alpha": {defaultModel: "only"},
		"empty": {},
	}, p, "postgres", nil, nil, "test", "", "")
	if err != nil {
		t.Fatal(err)
	}

	full := gatewayRoutingToken(t, ctx, p, "at_routing_full_token", service.APIToken{})
	denied := gatewayRoutingToken(t, ctx, p, "at_routing_denied_tokn", service.APIToken{
		AllowedProvidersMode: service.AccessModeNone,
		AllowedModelsMode:    service.AccessModeNone,
	})

	do := func(method, path, bearer string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		s.server.ServeHTTP(w, r)
		return w
	}

	t.Run("models are a sorted array", func(t *testing.T) {
		w := do(http.MethodGet, "/at/gateway/v1/models", full)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		var got ModelsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(got.Data))
		for _, m := range got.Data {
			ids = append(ids, m.ID)
		}
		// "empty" declares neither a model list nor a default, so it
		// contributes nothing; the rest are sorted by full ID.
		want := []string{"alpha/only", "zeta/beta", "zeta/delta"}
		if strings.Join(ids, ",") != strings.Join(want, ",") {
			t.Fatalf("ids %v want %v", ids, want)
		}
	})

	t.Run("an empty list is [] and never null", func(t *testing.T) {
		w := do(http.MethodGet, "/at/gateway/v1/models", denied)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		// Asserted on the raw body: unmarshalling into a slice cannot tell
		// `[]` from `null`, and the client-visible difference is the point.
		if body := strings.ReplaceAll(w.Body.String(), " ", ""); !strings.Contains(body, `"data":[]`) {
			t.Fatalf("empty list must serialize as []: %s", w.Body)
		}
	})

	t.Run("unmatched gateway paths answer JSON, not the SPA", func(t *testing.T) {
		for _, tt := range []struct{ method, path string }{
			{http.MethodGet, "/at/gateway/v1/models/"},   // trailing slash
			{http.MethodGet, "/at/gateway/v1/modelz"},    // typo
			{http.MethodPost, "/at/gateway/v1/models"},   // wrong method
			{http.MethodGet, "/at/gateway/v1"},           // base URL itself
			{http.MethodGet, "/at/gateway/v1/"},          // base URL with slash
			{http.MethodGet, "/at/gateway"},              // prefix only
			{http.MethodGet, "/at/gateway/v2/models"},    // wrong version
			{http.MethodPost, "/at/gateway/v1/chat"},     // truncated path
			{http.MethodGet, "/at/gateway/v1/providers"}, // passthrough without a provider
		} {
			w := do(tt.method, tt.path, full)
			if w.Code != http.StatusNotFound {
				t.Errorf("%s %s: status %d want 404: %s", tt.method, tt.path, w.Code, w.Body)
			}
			if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
				t.Errorf("%s %s: content-type %q", tt.method, tt.path, ct)
			}
			var envelope struct {
				Error struct{ Message, Type, Code string } `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
				t.Errorf("%s %s: %v: %s", tt.method, tt.path, err, w.Body)
				continue
			}
			if envelope.Error.Code != "unknown_endpoint" || !strings.Contains(envelope.Error.Message, "/at/gateway/v1") {
				t.Errorf("%s %s: unhelpful error %+v", tt.method, tt.path, envelope.Error)
			}
		}
	})

	t.Run("registered routes and the SPA are unaffected", func(t *testing.T) {
		// Health needs no credentials and reads the same registry.
		if w := do(http.MethodGet, "/at/gateway/v1/health", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"providers"`) {
			t.Fatalf("health: %d %s", w.Code, w.Body)
		}
		if w := do(http.MethodGet, "/at/gateway/v1/health/zeta", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"model_count":2`) {
			t.Fatalf("provider health: %d %s", w.Code, w.Body)
		}
		// Authentication still runs before the catch-all can see the path.
		if w := do(http.MethodGet, "/at/gateway/v1/models", ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated models: %d %s", w.Code, w.Body)
		}
		// The SPA keeps every path outside the gateway prefix.
		w := do(http.MethodGet, "/at/", "")
		if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("spa: %d %s", w.Code, w.Header().Get("Content-Type"))
		}
	})

	// The most common misconfiguration is a base URL that drops the /gateway
	// prefix, which lands outside the group above. The SPA answers HTTP 200
	// there, so that one path is redirected to the same explanation.
	t.Run("the versioned root without the gateway prefix explains itself", func(t *testing.T) {
		w := do(http.MethodGet, "/at/v1/models", full)
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "unknown_endpoint") {
			t.Fatalf("/v1/models: %d %s", w.Code, w.Body)
		}
	})
}
