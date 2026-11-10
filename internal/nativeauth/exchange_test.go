package nativeauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

type exchangeTestStore struct {
	externalTestStore
	linked bool
}

func (s *exchangeTestStore) HasAuthIdentityLink(_ context.Context, _, _, _ string) (bool, error) {
	return s.linked, nil
}

// TestTokenExchange covers the checks a browser sign-in gets from the code
// flow, made explicit for a token another backend hands over.
func TestTokenExchange(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "test", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	}))
	defer jwks.Close()
	provider := service.AuthIdentityProvider{
		ID: "sso", Label: "SSO", Enabled: true, Version: 3, ClientID: "website", SubjectClaim: "sub", JWKSURL: jwks.URL,
		RolesClaims: []string{"realm_access.roles"}, TokenExchange: true, Issuer: "https://sso.example", Audiences: []string{"website"}, ExchangeProvisioning: true,
	}
	claims := func(extra map[string]any) map[string]any {
		c := map[string]any{"iss": "https://sso.example", "aud": "website", "sub": "u-1", "exp": time.Now().Add(time.Minute).Unix(),
			"preferred_username": "ayse", "email": "ayse@firma.com", "email_verified": true, "realm_access": map[string]any{"roles": []string{"site-users"}}}
		for k, v := range extra {
			if v == nil {
				delete(c, k)
				continue
			}
			c[k] = v
		}
		return c
	}
	tests := []struct {
		name   string
		setup  func(*exchangeTestStore)
		token  func() string
		want   error
		checks func(*testing.T, *exchangeTestStore, *ExchangeResult)
	}{
		{name: "valid", token: func() string { return externalJWT(t, key, claims(nil)) }, checks: func(t *testing.T, s *exchangeTestStore, r *ExchangeResult) {
			if r.User.ID != "local-user" || s.last.Subject != "u-1" || s.last.Issuer != "oauth2:sso" || !s.last.EmailVerified {
				t.Fatalf("link: %+v", s.last)
			}
			var asserted struct {
				Roles []string `json:"roles"`
			}
			_ = json.Unmarshal(s.last.AssertedPermissions, &asserted)
			if !slices.Contains(asserted.Roles, "site-users") {
				t.Fatalf("nested role claim not asserted: %s", s.last.AssertedPermissions)
			}
		}},
		{name: "wrong signature", token: func() string { return externalJWT(t, other, claims(nil)) }, want: ErrExchangeRejected},
		{name: "wrong issuer", token: func() string { return externalJWT(t, key, claims(map[string]any{"iss": "https://sso.example/"})) }, want: ErrExchangeRejected},
		{name: "wrong audience", token: func() string { return externalJWT(t, key, claims(map[string]any{"aud": "other-api"})) }, want: ErrExchangeRejected},
		{name: "expired", token: func() string {
			return externalJWT(t, key, claims(map[string]any{"exp": time.Now().Add(-10 * time.Minute).Unix()}))
		}, want: ErrExchangeRejected},
		{name: "no expiry", token: func() string { return externalJWT(t, key, claims(map[string]any{"exp": nil})) }, want: ErrExchangeRejected},
		{name: "exchange off", setup: func(s *exchangeTestStore) { s.provider.TokenExchange = false }, token: func() string { return externalJWT(t, key, claims(nil)) }, want: ErrExchangeRejected},
		{name: "provider disabled", setup: func(s *exchangeTestStore) { s.provider.Enabled = false }, token: func() string { return externalJWT(t, key, claims(nil)) }, want: ErrExchangeRejected},
		{name: "allowed email refuses", setup: func(s *exchangeTestStore) { s.provider.AllowedEmails = []string{"@other.com"} }, token: func() string { return externalJWT(t, key, claims(nil)) }, want: ErrExchangeRejected},
		{name: "unverified email refused by list", setup: func(s *exchangeTestStore) { s.provider.AllowedEmails = []string{"@firma.com"} }, token: func() string {
			return externalJWT(t, key, claims(map[string]any{"email_verified": false}))
		}, want: ErrExchangeRejected},
		{name: "unknown person without provisioning", setup: func(s *exchangeTestStore) { s.provider.ExchangeProvisioning = false }, token: func() string { return externalJWT(t, key, claims(nil)) }, want: ErrExchangeUnknownUser},
		{name: "known person without provisioning", setup: func(s *exchangeTestStore) { s.provider.ExchangeProvisioning, s.linked = false, true }, token: func() string { return externalJWT(t, key, claims(nil)) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &exchangeTestStore{externalTestStore: externalTestStore{provider: provider}}
			if tt.setup != nil {
				tt.setup(store)
			}
			x := NewTokenExchanger(store, true)
			res, err := x.Exchange(t.Context(), "sso", tt.token())
			if tt.want != nil {
				if !errors.Is(err, tt.want) || store.completions != 0 {
					t.Fatalf("want %v without completion, got %v (completions %d)", tt.want, err, store.completions)
				}
				return
			}
			if err != nil || res == nil {
				t.Fatalf("exchange: %v", err)
			}
			if tt.checks != nil {
				tt.checks(t, store, res)
			}
		})
	}
}

func TestTokenExchangeProviderValidation(t *testing.T) {
	base := service.AuthIdentityProvider{Label: "SSO", ClientID: "c", SubjectClaim: "sub", AuthURL: "https://sso.example/a", TokenURL: "https://sso.example/t", JWKSURL: "https://sso.example/k", TokenExchange: true, Issuer: "https://sso.example", Audiences: []string{"website"}}
	if err := validateExternalProvider(base, false); err != nil {
		t.Fatalf("valid: %v", err)
	}
	for name, mutate := range map[string]func(*service.AuthIdentityProvider){
		"no jwks":     func(p *service.AuthIdentityProvider) { p.JWKSURL, p.UserInfoURL = "", "https://sso.example/u" },
		"no issuer":   func(p *service.AuthIdentityProvider) { p.Issuer = "" },
		"no audience": func(p *service.AuthIdentityProvider) { p.Audiences = nil },
	} {
		p := base
		mutate(&p)
		if validateExternalProvider(p, false) == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
