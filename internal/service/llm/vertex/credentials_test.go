package vertex

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

const testEndpoint = "https://us-central1-aiplatform.googleapis.com/v1/projects/p/locations/us-central1/endpoints/openapi/chat/completions"

type stubTokenSource struct{ token string }

func (s stubTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: s.token, Expiry: time.Now().Add(time.Hour)}, nil
}

// TestNewPrefersSuppliedTokenSource is the regression for a provider that
// carries its own service-account key: resolving Application Default
// Credentials unconditionally failed construction on a host that has none,
// even though the provider never needed them.
func TestNewPrefersSuppliedTokenSource(t *testing.T) {
	// Point ADC at a file that does not exist so its absence is deterministic
	// rather than a property of the machine running the test.
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "absent.json"))

	p, err := New("gemini-2.5-flash", testEndpoint, "", false, WithTokenSource(stubTokenSource{token: "from-provider"}))
	if err != nil {
		t.Fatalf("New with an explicit token source: %v", err)
	}

	tok, err := p.tokenSource.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok.AccessToken != "from-provider" {
		t.Fatalf("access token = %q, want the supplied one", tok.AccessToken)
	}
}

func TestNewFallsBackToADC(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "absent.json"))

	_, err := New("gemini-2.5-flash", testEndpoint, "", false)
	if err == nil {
		t.Fatal("expected ADC resolution to fail without a token source")
	}
	// The message has to name both ways out, because "failed to get Google
	// credentials" alone does not say that pasting a key is now an option.
	if !strings.Contains(err.Error(), "service-account key") || !strings.Contains(err.Error(), "GOOGLE_APPLICATION_CREDENTIALS") {
		t.Fatalf("error does not point at either credential source: %v", err)
	}
}

func TestNewRequiresEndpoint(t *testing.T) {
	if _, err := New("m", "", "", false, WithTokenSource(stubTokenSource{})); err == nil {
		t.Fatal("expected an error for an empty endpoint URL")
	}
}
