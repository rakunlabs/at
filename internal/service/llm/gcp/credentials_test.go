package gcp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// serviceAccountJSON builds a syntactically real key file. The private key is
// generated rather than pasted so the fixture is not a credential-shaped string
// that scanners have to be told to ignore.
func serviceAccountJSON(t *testing.T, tokenURI string) string {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	doc := map[string]string{
		"type":         "service_account",
		"project_id":   "at-test-project",
		"private_key":  string(pemBytes),
		"client_email": "at@at-test-project.iam.gserviceaccount.com",
		"token_uri":    tokenURI,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	return string(raw)
}

func TestParseCredentials(t *testing.T) {
	valid := serviceAccountJSON(t, "https://oauth2.googleapis.com/token")

	tests := []struct {
		name    string
		raw     string
		wantErr string // substring
		project string
	}{
		{
			name:    "service account",
			raw:     valid,
			project: "at-test-project",
		},
		{
			name:    "surrounding whitespace is tolerated",
			raw:     "\n  " + valid + "\n",
			project: "at-test-project",
		},
		{
			name:    "empty",
			raw:     "   ",
			wantErr: "empty",
		},
		{
			name:    "not json",
			raw:     "/Users/me/key.json",
			wantErr: "not valid JSON",
		},
		{
			// The most common wrong download: an OAuth client secret looks like
			// a credentials file and authenticates nothing on its own.
			name:    "oauth client secret file",
			raw:     `{"installed":{"client_id":"x.apps.googleusercontent.com","client_secret":"y"}}`,
			wantErr: "OAuth client secret file",
		},
		{
			name:    "web oauth client",
			raw:     `{"web":{"client_id":"x","client_secret":"y"}}`,
			wantErr: "OAuth client secret file",
		},
		{
			name:    "missing type",
			raw:     `{"project_id":"p"}`,
			wantErr: `no "type" field`,
		},
		{
			name:    "unknown type",
			raw:     `{"type":"gdch_service_account"}`,
			wantErr: "unsupported credentials type",
		},
		{
			name:    "truncated service account",
			raw:     `{"type":"service_account","project_id":"p"}`,
			wantErr: "missing client_email, private_key",
		},
		{
			// gcloud's application_default_credentials.json. It works, and it
			// carries no project, so the provider must name one itself.
			name: "authorized user",
			raw:  `{"type":"authorized_user","client_id":"x","client_secret":"y","refresh_token":"z"}`,
		},
		{
			// The security boundary: this file tells the server which URL to
			// fetch the real credential from, and it is submitted by a
			// workspace administrator. Pointed at the metadata server it would
			// mint a token for the host's own identity.
			name:    "workload identity federation is refused",
			raw:     `{"type":"external_account","audience":"//iam.googleapis.com/x","credential_source":{"url":"http://169.254.169.254/computeMetadata/v1/instance/service-accounts/default/token"}}`,
			wantErr: "not accepted here",
		},
		{
			name:    "impersonation is refused",
			raw:     `{"type":"impersonated_service_account","service_account_impersonation_url":"http://127.0.0.1/x"}`,
			wantErr: "not accepted here",
		},
		{
			name:    "external account authorized user is refused",
			raw:     `{"type":"external_account_authorized_user","token_url":"http://127.0.0.1/x"}`,
			wantErr: "not accepted here",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creds, err := ParseCredentials(tt.raw)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if creds.ProjectID != tt.project {
				t.Fatalf("project = %q, want %q", creds.ProjectID, tt.project)
			}
		})
	}
}

func TestTokenSourceRejectsUnusableJSONBeforeAnyCall(t *testing.T) {
	// A bad paste must fail where the operator can see it, not on the first
	// inference request hours later.
	if _, err := TokenSource(context.Background(), `{"installed":{}}`, nil); err == nil {
		t.Fatal("expected an error for an OAuth client secret file")
	}
}

// TestTokenSourceUsesProviderProxy is the regression for the restricted-network
// case: the token exchange has to traverse the provider's proxy, or a
// deployment that can only reach Google through one fails while fetching the
// token rather than while calling the model.
func TestTokenSourceUsesProviderProxy(t *testing.T) {
	var proxied atomic.Int32

	// A CONNECT-less forward proxy: the oauth2 exchange is a plain POST, so the
	// transport sends the absolute-URI form here instead of dialing Google.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"proxied-token","token_type":"Bearer","expires_in":3600}`))
	}))
	defer proxy.Close()

	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("parse proxy url: %v", err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)

	// http:// so the exchange is proxied as a plain request; an https token_uri
	// would be CONNECT-tunnelled and this stub is not a tunnel.
	raw := serviceAccountJSON(t, "http://oauth2.googleapis.com/token")

	ts, err := TokenSource(context.Background(), raw, &http.Client{Transport: transport})
	if err != nil {
		t.Fatalf("TokenSource: %v", err)
	}

	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok.AccessToken != "proxied-token" {
		t.Fatalf("access token = %q, want the one the proxy returned", tok.AccessToken)
	}
	if got := proxied.Load(); got != 1 {
		t.Fatalf("proxy saw %d exchanges, want exactly 1", got)
	}
}

func TestEndpointDerivation(t *testing.T) {
	if got := Region(""); got != DefaultRegion {
		t.Fatalf("Region(\"\") = %q, want %q", got, DefaultRegion)
	}
	if got := Region("  europe-west4 "); got != "europe-west4" {
		t.Fatalf("Region trimming: got %q", got)
	}
	if got := RegionalHost("europe-west4"); got != "https://europe-west4-aiplatform.googleapis.com" {
		t.Fatalf("RegionalHost = %q", got)
	}

	want := "https://us-central1-aiplatform.googleapis.com/v1/projects/p/locations/us-central1/endpoints/openapi/chat/completions"
	if got := ChatCompletionsEndpoint("p", ""); got != want {
		t.Fatalf("ChatCompletionsEndpoint =\n%q\nwant\n%q", got, want)
	}
}
