package mcpauth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestDiscoverAuthorization(t *testing.T) {
	for _, tt := range []struct {
		name, resource, issuer string
		pkce                   []string
		wantError              bool
	}{
		{name: "valid", pkce: []string{"S256"}},
		{name: "wrong resource", resource: "/other", pkce: []string{"S256"}, wantError: true},
		{name: "wrong issuer", issuer: "/other", pkce: []string{"S256"}, wantError: true},
		{name: "missing PKCE", wantError: true},
		{name: "plain PKCE", pkce: []string{"plain"}, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var origin string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/mcp":
					w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/resource", scope="read write"`, origin))
					w.WriteHeader(http.StatusUnauthorized)
				case "/resource":
					resource := origin + "/mcp"
					if tt.resource != "" {
						resource = origin + tt.resource
					}
					_ = json.NewEncoder(w).Encode(protectedResourceMetadata{Resource: resource, AuthorizationServers: []string{origin + "/tenant"}})
				case "/.well-known/oauth-authorization-server/tenant":
					issuer := origin + "/tenant"
					if tt.issuer != "" {
						issuer = origin + tt.issuer
					}
					_ = json.NewEncoder(w).Encode(authorizationServerMetadata{Issuer: issuer, AuthorizationEndpoint: origin + "/authorize", TokenEndpoint: origin + "/token", CodeChallengeMethodsSupported: tt.pkce})
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			origin = srv.URL
			meta, err := Discover(t.Context(), Client(true), origin+"/mcp", "")
			if (err != nil) != tt.wantError {
				t.Fatalf("metadata=%+v error=%v", meta, err)
			}
			if err == nil && (meta.Resource != origin+"/mcp" || strings.Join(meta.DefaultScopes(), " ") != "read write") {
				t.Fatalf("unexpected metadata: %+v", meta)
			}
		})
	}
}

func TestDiscoveryDoesNotGuessEndpoints(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if _, err := Discover(t.Context(), Client(true), srv.URL+"/mcp", ""); err == nil {
		t.Fatal("missing resource metadata must fail")
	}
	if _, err := Discover(t.Context(), Client(true), srv.URL+"/mcp", srv.URL); err == nil {
		t.Fatal("missing issuer metadata must fail")
	}
}

func TestPKCEAndAuthorizationURL(t *testing.T) {
	a, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(a.Verifier))
	if len(a.Verifier) != 43 || a.Verifier == b.Verifier || a.Challenge != base64.RawURLEncoding.EncodeToString(sum[:]) {
		t.Fatal("invalid PKCE verifier/challenge")
	}
	raw, err := AuthorizeURL(&Metadata{AuthorizationEndpoint: "https://auth.example/authorize?existing=1", Resource: "https://mcp.example/mcp"}, "client", "https://at.example/callback", []string{"read", "write"}, "state", a.Challenge)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	for key, want := range map[string]string{"existing": "1", "response_type": "code", "resource": "https://mcp.example/mcp", "code_challenge_method": "S256", "scope": "read write", "state": "state"} {
		if got := u.Query().Get(key); got != want {
			t.Fatalf("%s=%q, want %q", key, got, want)
		}
	}
}

func TestTokenExchangeAndRefresh(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("resource") != "https://mcp.example/mcp" || r.Form.Get("client_secret") != "secret" {
			t.Error("missing resource or client credentials")
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code_verifier") != "verifier" || r.Form.Get("code") != "code" || r.Form.Get("redirect_uri") != "https://at.example/callback" {
				t.Error("missing authorization code fields")
			}
		case "refresh_token":
			if r.Form.Get("refresh_token") != "refresh" {
				t.Error("missing refresh token")
			}
		default:
			t.Error("wrong grant")
		}
		_, _ = w.Write([]byte(`{"access_token":"access","refresh_token":"rotated","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()
	token, err := Exchange(t.Context(), Client(true), srv.URL, "client", "secret", "code", "https://at.example/callback", "verifier", "https://mcp.example/mcp")
	if err != nil || token.AccessToken != "access" {
		t.Fatalf("exchange: %+v %v", token, err)
	}
	token, err = Refresh(t.Context(), Client(true), srv.URL, "client", "secret", "refresh", "https://mcp.example/mcp")
	if err != nil || token.RefreshToken != "rotated" {
		t.Fatalf("refresh: %+v %v", token, err)
	}
}

func TestTokenResponseFailures(t *testing.T) {
	for _, tt := range []struct {
		name, body string
	}{
		{"OAuth error with HTTP 200", `{"error":"invalid_grant","error_description":"secret-token"}`},
		{"missing token", `{}`},
		{"unsupported token", `{"access_token":"a","token_type":"MAC"}`},
		{"oversized", strings.Repeat("x", maxMetadataBytes+1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tt.body)) }))
			defer srv.Close()
			_, err := Refresh(t.Context(), Client(true), srv.URL, "c", "s", "r", "resource")
			if err == nil || strings.Contains(err.Error(), "secret-token") {
				t.Fatalf("unsafe/missing error: %v", err)
			}
			if tt.name == "OAuth error with HTTP 200" && !IsInvalidGrant(fmt.Errorf("wrapped: %w", err)) {
				t.Fatal("invalid_grant not recognized")
			}
		})
	}
}

func TestAuthorizationNetworkSafety(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/target", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	if _, err := Refresh(t.Context(), Client(true), srv.URL, "c", "s", "r", "resource"); err == nil {
		t.Fatal("token POST redirect was followed")
	}
	if _, err := Client(false).Get(srv.URL); err == nil {
		t.Fatal("public OAuth client dialled loopback")
	}
	for _, raw := range []string{"https://user:password@example.com", "https://example.com#fragment", "file:///etc/passwd", "http://example.com"} {
		if err := checkEndpoint(raw, true); err == nil {
			t.Errorf("unsafe endpoint accepted: %s", raw)
		}
	}
	if got := (&Token{ExpiresIn: math.MaxInt64}).ExpiresAt(time.Now()); !got.After(time.Now()) {
		t.Fatal("expiry overflowed")
	}
	if IsInvalidGrant(errors.New("invalid_grant")) {
		t.Fatal("untyped error matched")
	}
}

func TestBearerChallenge(t *testing.T) {
	got := ParseBearerChallenge(`Bearer resource_metadata="https://mcp.example/meta", scope="read write", error="invalid_token"`)
	if got["resource_metadata"] != "https://mcp.example/meta" || got["scope"] != "read write" || got["error"] != "invalid_token" {
		t.Fatalf("challenge: %#v", got)
	}
	if got := ParseBearerChallenge(`Basic realm="Bearer resource_metadata=evil"`); len(got) != 0 {
		t.Fatalf("Basic realm interpreted as Bearer: %#v", got)
	}
	got = ParseBearerChallenge(`Basic realm="private", Bearer scope="read", Basic realm="other", scope="admin"`)
	if got["scope"] != "read" {
		t.Fatalf("later scheme overwrote Bearer scope: %#v", got)
	}
}

func TestDynamicRegistration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["token_endpoint_auth_method"] != "none" || body["client_name"] != "AT" {
			t.Errorf("registration body: %#v", body)
		}
		redirects, ok := body["redirect_uris"].([]any)
		if !ok || len(redirects) != 1 || redirects[0] != "https://at.example/callback" {
			t.Errorf("registration redirect: %#v", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"client_id":"registered"}`))
	}))
	defer srv.Close()
	id, secret, err := Register(t.Context(), Client(true), &Metadata{RegistrationEndpoint: srv.URL}, "https://at.example/callback", "AT")
	if err != nil || id != "registered" || secret != "" {
		t.Fatalf("registration: id=%q error=%v", id, err)
	}
	if _, _, err := Register(t.Context(), Client(true), &Metadata{}, "https://at.example/callback", "AT"); !errors.Is(err, ErrRegistrationUnsupported) {
		t.Fatalf("missing DCR: %v", err)
	}
}
