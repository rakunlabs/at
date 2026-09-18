package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
	"github.com/worldline-go/types"
)

const routingTestWorkspace = "legacy-default"

func routingProfileServer(t *testing.T) (*Server, *postgres.Postgres, context.Context) {
	t.Helper()
	p := postgrestest.New(t, nil)
	cfg := nativeTestConfig()
	cfg.Workspace = &config.Workspace{Root: t.TempDir(), TTLHours: -1}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	s, err := New(ctx, cfg, map[string]ProviderInfo{
		"alpha": {defaultModel: "only", models: []string{"only"}},
		"zeta":  {defaultModel: "fallback", models: []string{"beta"}},
	}, p, "postgres", nil, nil, "test", "", "")
	if err != nil {
		t.Fatal(err)
	}

	return s, p, ctx
}

func seedRoutingProfile(t *testing.T, ctx context.Context, p *postgres.Postgres, name string, targets ...string) {
	t.Helper()
	if _, err := p.CreateRoutingProfile(service.WithLegacyWorkspaceAccess(ctx), service.RoutingProfile{
		Name:    name,
		Targets: types.Slice[string](targets),
	}); err != nil {
		t.Fatalf("seed routing profile %q: %v", name, err)
	}
}

func workspaceToken(workspace string, token service.APIToken) *authResult {
	token.ID = "tok-routing"
	token.WorkspaceID = workspace

	return &authResult{token: &token}
}

func chainModels(chain []chatCallTarget) []string {
	out := make([]string, 0, len(chain))
	for _, target := range chain {
		out = append(out, target.fullModel)
	}

	return out
}

func TestRoutingProfileChainResolution(t *testing.T) {
	s, p, ctx := routingProfileServer(t)
	seedRoutingProfile(t, ctx, p, "my-stack", "alpha/only", "zeta/beta")

	auth := workspaceToken(routingTestWorkspace, service.APIToken{})

	t.Run("bare name expands to the profile chain", func(t *testing.T) {
		chain, profile := s.chatCallChain(ctx, auth, "my-stack", nil)
		if profile != "my-stack" {
			t.Fatalf("profile %q want my-stack", profile)
		}
		if got := strings.Join(chainModels(chain), ","); got != "alpha/only,zeta/beta" {
			t.Fatalf("chain %q want alpha/only,zeta/beta", got)
		}
		for _, target := range chain {
			if target.err != nil {
				t.Fatalf("target %q unexpectedly unresolved: %v", target.fullModel, target.err)
			}
		}
	})

	t.Run("bare name with no matching profile keeps the pre-change rejection", func(t *testing.T) {
		chain, profile := s.chatCallChain(ctx, auth, "gpt-4.1", nil)
		if profile != "" {
			t.Fatalf("profile %q want empty", profile)
		}
		if len(chain) != 1 || chain[0].err == nil {
			t.Fatalf("expected a single error target, got %+v", chain)
		}
		if !strings.Contains(chain[0].err.Error(), "provider/model") {
			t.Fatalf("error should state the provider/model requirement: %v", chain[0].err)
		}
	})

	t.Run("a qualified model is never looked up as a profile", func(t *testing.T) {
		chain, profile := s.chatCallChain(ctx, auth, "alpha/only", nil)
		if profile != "" {
			t.Fatalf("profile %q want empty for a qualified model", profile)
		}
		if len(chain) != 1 || chain[0].err != nil || chain[0].providerKey != "alpha" {
			t.Fatalf("qualified model did not route directly: %+v", chain)
		}
	})

	t.Run("explicit at_fallbacks takes precedence over a profile", func(t *testing.T) {
		chain, profile := s.chatCallChain(ctx, auth, "my-stack", []string{"alpha/only"})
		if profile != "" {
			t.Fatalf("profile %q want empty when at_fallbacks is set", profile)
		}
		// "my-stack" is now an ordinary primary, and an unqualified one, so it
		// is reported as the invalid primary rather than silently expanded.
		if len(chain) == 0 || chain[0].err == nil {
			t.Fatalf("expected the primary to be reported invalid: %+v", chain)
		}
	})

	t.Run("a token bound to another workspace resolves nothing", func(t *testing.T) {
		foreign := workspaceToken("some-other-workspace", service.APIToken{})
		_, profile := s.chatCallChain(ctx, foreign, "my-stack", nil)
		if profile != "" {
			t.Fatalf("profile leaked across workspaces: %q", profile)
		}
	})

	t.Run("a token with no workspace binding resolves nothing", func(t *testing.T) {
		unbound := workspaceToken("", service.APIToken{})
		_, profile := s.chatCallChain(ctx, unbound, "my-stack", nil)
		if profile != "" {
			t.Fatalf("unbound token resolved a profile: %q", profile)
		}
	})
}

// A profile grants routing, never authorization: every expanded target passes
// the same token model-access check an explicit at_fallbacks entry would.
func TestRoutingProfileDoesNotGrantAccess(t *testing.T) {
	s, p, ctx := routingProfileServer(t)
	seedRoutingProfile(t, ctx, p, "mixed", "alpha/only", "zeta/beta")
	seedRoutingProfile(t, ctx, p, "all-denied", "alpha/only")

	t.Run("denied targets are skipped, usable ones still serve", func(t *testing.T) {
		// Provider and model access are OR'd (gateway.go:54), so restricting to
		// one provider means listing it *and* closing the model mode; a provider
		// list alone leaves modelMode "all", which permits everything.
		auth := workspaceToken(routingTestWorkspace, service.APIToken{
			AllowedProvidersMode: service.AccessModeList,
			AllowedProviders:     types.Slice[string]{"zeta"},
			AllowedModelsMode:    service.AccessModeNone,
		})
		chain, profile := s.chatCallChain(ctx, auth, "mixed", nil)
		if profile != "mixed" {
			t.Fatalf("profile %q want mixed", profile)
		}
		if got := strings.Join(chainModels(chain), ","); got != "zeta/beta" {
			t.Fatalf("chain %q want zeta/beta only", got)
		}
	})

	t.Run("a profile whose every target is denied is an error, not an escalation", func(t *testing.T) {
		auth := workspaceToken(routingTestWorkspace, service.APIToken{
			AllowedProvidersMode: service.AccessModeNone,
			AllowedModelsMode:    service.AccessModeNone,
		})
		chain, profile := s.chatCallChain(ctx, auth, "all-denied", nil)
		if profile != "all-denied" {
			t.Fatalf("profile %q want all-denied", profile)
		}
		if len(chain) != 1 || chain[0].err == nil {
			t.Fatalf("expected a single error target, got %+v", chain)
		}
		// The message names the profile, not the models the token may not enumerate.
		if msg := chain[0].err.Error(); !strings.Contains(msg, "all-denied") || strings.Contains(msg, "alpha/only") {
			t.Fatalf("error should name the profile and not its targets: %v", msg)
		}
	})
}

func TestRoutingProfileAppearsInModelList(t *testing.T) {
	s, p, ctx := routingProfileServer(t)
	seedRoutingProfile(t, ctx, p, "my-stack", "alpha/only")
	seedRoutingProfile(t, ctx, p, "unreachable", "ghost/model")

	full := gatewayRoutingToken(t, ctx, p, "at_profile_models_tokn", service.APIToken{})

	r := httptest.NewRequest(http.MethodGet, "/at/gateway/v1/models", nil)
	r.Header.Set("Authorization", "Bearer "+full)
	w := httptest.NewRecorder()
	s.server.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var got ModelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	var profiles []string
	for _, m := range got.Data {
		if m.RoutingProfile {
			profiles = append(profiles, m.ID)
		}
	}
	// "unreachable" names a provider that does not exist, so advertising it
	// would offer a model that fails on use.
	if len(profiles) != 1 || profiles[0] != "my-stack" {
		t.Fatalf("advertised profiles %v want [my-stack]", profiles)
	}

	// Concrete models are still listed, and before the profiles.
	if len(got.Data) < 2 || got.Data[0].RoutingProfile {
		t.Fatalf("profiles must follow concrete models: %+v", got.Data)
	}
}
