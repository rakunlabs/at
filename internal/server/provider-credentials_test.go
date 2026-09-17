package server

import (
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

const storedKey = `{"type":"service_account","project_id":"p","client_email":"a@p.iam.gserviceaccount.com","private_key":"-----BEGIN PRIVATE KEY-----\nx\n-----END PRIVATE KEY-----\n"}`

// TestPreserveProviderCredentialsJSON covers the round-trip every writer makes:
// it reads a redacted record and writes the whole config back. Without
// preservation an edit to the model list would wipe a key the writer never saw.
func TestPreserveProviderCredentialsJSON(t *testing.T) {
	existing := config.LLMConfig{Type: "vertex", CredentialsJSON: storedKey}

	tests := []struct {
		name  string
		next  string
		clear bool
		want  string
	}{
		{name: "sentinel round-trip keeps the key", next: redactedSecret, want: storedKey},
		{name: "omitted field keeps the key", next: "", want: storedKey},
		{name: "a new key replaces the old one", next: `{"type":"authorized_user"}`, want: `{"type":"authorized_user"}`},
		{name: "explicit clear returns to ADC", next: redactedSecret, clear: true, want: ""},
		{name: "clear wins over a supplied key", next: storedKey, clear: true, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := config.LLMConfig{Type: "vertex", CredentialsJSON: tt.next}
			preserveProviderCredentialsJSON(&next, existing, tt.clear)
			if next.CredentialsJSON != tt.want {
				t.Fatalf("credentials_json = %q, want %q", next.CredentialsJSON, tt.want)
			}
		})
	}
}

func TestValidateProviderCredentialsJSON(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.LLMConfig
		wantMsg string // substring; empty means accepted
	}{
		{name: "no credentials", cfg: config.LLMConfig{Type: "vertex"}},
		{name: "sentinel is not re-validated", cfg: config.LLMConfig{Type: "vertex", CredentialsJSON: redactedSecret}},
		{name: "valid key on vertex", cfg: config.LLMConfig{Type: "vertex", CredentialsJSON: storedKey}},
		{name: "valid key on vertex-gemini", cfg: config.LLMConfig{Type: "vertex-gemini", CredentialsJSON: storedKey}},
		{
			// Storing an unused secret on a provider that cannot read it is a
			// configuration mistake worth naming.
			name:    "wrong provider type",
			cfg:     config.LLMConfig{Type: "openai", CredentialsJSON: storedKey},
			wantMsg: "vertex and vertex-gemini",
		},
		{
			name:    "oauth client secret file",
			cfg:     config.LLMConfig{Type: "vertex", CredentialsJSON: `{"installed":{"client_id":"x"}}`},
			wantMsg: "OAuth client secret file",
		},
		{
			name:    "not json",
			cfg:     config.LLMConfig{Type: "vertex", CredentialsJSON: "~/key.json"},
			wantMsg: "not valid JSON",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := validateProviderCredentialsJSON(tt.cfg)
			if tt.wantMsg == "" {
				if msg != "" {
					t.Fatalf("expected the config to be accepted, got %q", msg)
				}
				return
			}
			if !strings.Contains(msg, tt.wantMsg) {
				t.Fatalf("message %q does not contain %q", msg, tt.wantMsg)
			}
		})
	}
}

func TestRedactProviderCredentials(t *testing.T) {
	rec := service.ProviderRecord{Key: "vx", Config: config.LLMConfig{Type: "vertex", CredentialsJSON: storedKey}}
	redactProviderRecord(&rec)
	if rec.Config.CredentialsJSON != redactedSecret {
		t.Fatalf("credentials_json = %q, want the redaction sentinel", rec.Config.CredentialsJSON)
	}

	// An absent key must stay absent: "stored" and "not configured" are
	// distinguishable in the UI by exactly this.
	empty := service.ProviderRecord{Key: "vx", Config: config.LLMConfig{Type: "vertex"}}
	redactProviderRecord(&empty)
	if empty.Config.CredentialsJSON != "" {
		t.Fatalf("credentials_json = %q, want empty", empty.Config.CredentialsJSON)
	}
}
