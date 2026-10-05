package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"testing"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

// A personal API token used to reach only the bare-key registry, which holds
// Default-workspace providers. The owner's personal providers and the
// providers of a non-default workspace were therefore neither listed by
// /gateway/v1/models nor routable, although Chats offered them.
func TestGatewayPersonalTokenReachesOwnerProviders(t *testing.T) {
	f := newMachineFixture(t)
	f.s.providerFactory = func(config.LLMConfig) (service.LLMProvider, error) { return userUsageProvider{}, nil }
	f.s.providers = map[string]ProviderInfo{}

	member, _, err := f.store.ResolveWorkspaceAccess(t.Context(), f.workspace, f.member, "")
	if err != nil {
		t.Fatal(err)
	}
	memberCtx := service.WithAccessPrincipal(t.Context(), member)

	personal, err := f.store.CreatePersonalProvider(memberCtx, service.ProviderRecord{Key: "mine", Config: config.LLMConfig{Type: "openai", Model: "m1", Models: []string{"m1", "m2"}, APIKey: "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateProvider(memberCtx, service.ProviderRecord{Key: "team", Config: config.LLMConfig{Type: "openai", Model: "t1", APIKey: "secret"}}); err != nil {
		t.Fatal(err)
	}

	token := func(ctx context.Context, name, owner string) *authResult {
		t.Helper()
		sum := sha256.Sum256([]byte(name))
		created, err := f.store.CreateAPIToken(ctx, service.APIToken{Name: name, OwnerUserID: owner}, hex.EncodeToString(sum[:]))
		if err != nil {
			t.Fatal(err)
		}
		return &authResult{token: created}
	}
	personalToken := token(memberCtx, "personal", f.member)
	sharedToken := token(memberCtx, "shared", "")

	ids := func(auth *authResult) []string {
		var out []string
		for _, m := range f.s.gatewayModels(t.Context(), auth) {
			out = append(out, m.ID)
		}
		return out
	}
	ref := personal.Reference
	got := ids(personalToken)
	for _, want := range []string{ref + "/m1", ref + "/m2", "team/t1"} {
		if !slices.Contains(got, want) {
			t.Fatalf("personal token models %v missing %q", got, want)
		}
	}
	if got := ids(sharedToken); slices.Contains(got, ref+"/m1") {
		t.Fatalf("workspace token listed a personal provider: %v", got)
	}

	for _, model := range []string{ref + "/m2", "team/t1"} {
		if _, _, info, err := f.s.resolveModel(t.Context(), personalToken, model); err != nil || info.provider == nil {
			t.Fatalf("resolve %q: %v", model, err)
		}
	}
	if _, _, _, err := f.s.resolveModel(t.Context(), personalToken, ref+"/unlisted"); err == nil {
		t.Fatal("model outside the provider's list was admitted")
	}
	if _, _, _, err := f.s.resolveModel(t.Context(), sharedToken, ref+"/m1"); err == nil {
		t.Fatal("workspace token resolved a personal provider")
	}

	// Restrictions on the token still apply to catalog providers.
	restricted := token(memberCtx, "restricted", f.member)
	restricted.token.AllowedModelsMode = service.AccessModeList
	restricted.token.AllowedProvidersMode = service.AccessModeNone
	restricted.token.AllowedModels = []string{ref + "/m1"}
	if got := ids(restricted); !slices.Equal(got, []string{ref + "/m1"}) {
		t.Fatalf("restricted token models = %v", got)
	}

	if err := f.store.SetPersonalProviderDisabled(memberCtx, personal.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	if got := ids(personalToken); slices.Contains(got, ref+"/m1") {
		t.Fatalf("disabled personal provider still listed: %v", got)
	}
	if _, _, _, err := f.s.resolveModel(t.Context(), personalToken, ref+"/m1"); err == nil {
		t.Fatal("disabled personal provider resolved")
	}
}
