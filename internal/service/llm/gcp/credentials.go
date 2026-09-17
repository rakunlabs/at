// Package gcp resolves Google Cloud credentials for the Vertex AI provider
// types. It is deliberately a leaf — it knows about oauth2 and nothing about
// the LLM adapters — so the OpenAI-compatible `vertex` adapter and the native
// `vertex-gemini` one authenticate through exactly the same code path.
//
// Two credential sources are supported, in this order:
//
//  1. A service-account key file stored on the provider row (`credentials_json`).
//     It is encrypted at rest with the rest of the provider config, so it is
//     per-provider and per-workspace, and it can be set from the UI without
//     touching the server host.
//  2. Application Default Credentials resolved from the server *process*
//     (GOOGLE_APPLICATION_CREDENTIALS, the gcloud well-known file, or the
//     GCE/Cloud Run/GKE metadata server). This is installation-wide: every
//     workspace authenticates as the same host identity.
package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// Scope is the only OAuth scope a Vertex AI call needs. Both provider types
// use it, and it is not configurable: a narrower scope does not exist for
// aiplatform.googleapis.com.
const Scope = "https://www.googleapis.com/auth/cloud-platform"

// DefaultRegion is used when a provider names a project but no region. It
// matches the region in every Vertex example Google publishes.
const DefaultRegion = "us-central1"

// Credentials is the subset of a Google credentials file AT reads. The
// remaining fields (private key, token URIs, …) are handed to oauth2 verbatim;
// AT never reassembles the document.
type Credentials struct {
	Type        string `json:"type"`
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`

	// installed/web only appear in OAuth *client* files. Those are the usual
	// wrong download from the Cloud console and are worth naming explicitly:
	// they are JSON, they contain a client_id, and they authenticate nothing
	// on their own.
	Installed json.RawMessage `json:"installed"`
	Web       json.RawMessage `json:"web"`
}

// ParseCredentials validates a pasted credentials file and returns the fields
// AT uses for defaults. It is intentionally stricter than oauth2 in two ways.
//
// The first is diagnostic: the failure it usually catches is an operator
// downloading the wrong file from the Cloud console, and "unexpected end of
// JSON input" two screens later does not say which file to fetch instead.
//
// The second is a security boundary. Only the two self-contained credential
// kinds are accepted. Every other kind — external_account,
// external_account_authorized_user, impersonated_service_account — describes
// where to *go and get* a credential: a URL the server will fetch, or (for the
// executable source) a command it will run. Those files are submitted here by a
// workspace administrator, who in AT's model is not necessarily the platform
// operator, so accepting one would turn provider configuration into
// server-side request forgery against the host's own metadata server. Google
// documents the same requirement under "Validate credential configurations
// from external sources".
func ParseCredentials(raw string) (Credentials, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Credentials{}, fmt.Errorf("credentials JSON is empty")
	}

	var creds Credentials
	if err := json.Unmarshal([]byte(trimmed), &creds); err != nil {
		return Credentials{}, fmt.Errorf("credentials JSON is not valid JSON — paste the whole key file, including the outer braces: %w", err)
	}

	if len(creds.Installed) > 0 || len(creds.Web) > 0 {
		return Credentials{}, fmt.Errorf(`this is an OAuth client secret file (it has an "installed"/"web" block), not a service-account key; in the Cloud console open IAM & Admin → Service Accounts → your account → Keys → Add key → Create new key → JSON`)
	}

	switch creds.Type {
	case string(google.ServiceAccount):
		// Every one of these is required for the JWT assertion, and a key file
		// missing any of them was edited or truncated on the way here.
		var missing []string
		if creds.ProjectID == "" {
			missing = append(missing, "project_id")
		}
		if creds.ClientEmail == "" {
			missing = append(missing, "client_email")
		}
		if creds.PrivateKey == "" {
			missing = append(missing, "private_key")
		}
		if len(missing) > 0 {
			return Credentials{}, fmt.Errorf("service-account key is missing %s", strings.Join(missing, ", "))
		}
	case string(google.AuthorizedUser):
		// gcloud's application_default_credentials.json. Self-contained — a
		// refresh token exchanged at Google — and it carries no project, so the
		// provider has to name one itself.
	case "":
		return Credentials{}, fmt.Errorf(`credentials JSON has no "type" field; a service-account key has "type": "service_account"`)
	case string(google.ExternalAccount), string(google.ExternalAccountAuthorizedUser), string(google.ImpersonatedServiceAccount):
		return Credentials{}, fmt.Errorf("credentials of type %q are not accepted here: they tell the server to fetch the real credential from a URL or command named inside the file. "+
			"Use a service-account key, or configure workload identity federation on the server itself, where it is resolved as Application Default Credentials", creds.Type)
	default:
		return Credentials{}, fmt.Errorf("unsupported credentials type %q (expected service_account or authorized_user)", creds.Type)
	}

	return creds, nil
}

// TokenSource returns an auto-refreshing token source for the given credentials
// JSON, falling back to Application Default Credentials when it is empty.
//
// httpClient, when non-nil, carries the provider's proxy and TLS settings and
// is used for the token exchange itself. That matters in a restricted network:
// an installation that can only reach Google through a proxy cannot reach
// oauth2.googleapis.com either, and without this the provider would fail at
// the token fetch rather than at the inference call. The GCE metadata server
// is the one exception — it is link-local, so it is never proxied.
//
// ctx is stored by oauth2 and reused for every refresh for the lifetime of the
// provider, so callers must pass a long-lived context, never a request one.
func TokenSource(ctx context.Context, credentialsJSON string, httpClient *http.Client) (oauth2.TokenSource, error) {
	exchangeCtx := ctx
	if httpClient != nil {
		exchangeCtx = context.WithValue(ctx, oauth2.HTTPClient, httpClient)
	}

	if strings.TrimSpace(credentialsJSON) != "" {
		parsed, err := ParseCredentials(credentialsJSON)
		if err != nil {
			return nil, err
		}

		// …WithType rather than the deprecated CredentialsFromJSON: it re-reads
		// the type from the same bytes and refuses to build anything else, so
		// the kind this package vetted is the kind oauth2 constructs.
		creds, err := google.CredentialsFromJSONWithType(exchangeCtx, []byte(credentialsJSON), google.CredentialsType(parsed.Type), Scope)
		if err != nil {
			return nil, fmt.Errorf("read service-account credentials: %w", err)
		}

		return creds.TokenSource, nil
	}

	// Discovery runs on the unproxied context on purpose: on GCE this probes
	// the metadata server, and routing 169.254.169.254 through a corporate
	// proxy turns a working host into a broken one.
	creds, err := google.FindDefaultCredentials(ctx, Scope)
	if err != nil {
		return nil, fmt.Errorf("no service-account key on the provider and no Application Default Credentials on the server "+
			"(paste a key into the provider, set GOOGLE_APPLICATION_CREDENTIALS, run `gcloud auth application-default login`, "+
			"or run on GCE/Cloud Run/GKE): %w", err)
	}

	// FindDefaultCredentials returns the raw key material for file-based
	// credentials and nothing for the metadata server, which is exactly the
	// distinction needed here: rebuild the file case on the proxied context so
	// the provider's proxy applies, and leave the metadata case alone.
	//
	// The type restriction above does not apply on this path. This document
	// came from the host's own configuration, not from a workspace
	// administrator, so workload identity federation — the normal way to run on
	// GKE — stays supported; the type is read back only to satisfy the
	// non-deprecated constructor.
	if httpClient != nil && len(creds.JSON) > 0 {
		var kind struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(creds.JSON, &kind); err != nil || kind.Type == "" {
			// Unreadable or typeless: keep the source the library already built
			// rather than failing a host that authenticates perfectly well.
			return creds.TokenSource, nil
		}

		proxied, perr := google.CredentialsFromJSONWithType(exchangeCtx, creds.JSON, google.CredentialsType(kind.Type), Scope)
		if perr != nil {
			return nil, fmt.Errorf("apply provider proxy to Application Default Credentials: %w", perr)
		}

		return proxied.TokenSource, nil
	}

	return creds.TokenSource, nil
}

// Region returns the configured region or the default. Empty is not an error:
// a provider that names only a project is a normal configuration.
func Region(region string) string {
	if region = strings.TrimSpace(region); region != "" {
		return region
	}

	return DefaultRegion
}

// RegionalHost is the native Vertex AI host for a region, which is what the
// `vertex-gemini` adapter prefixes its per-model path onto.
func RegionalHost(region string) string {
	return fmt.Sprintf("https://%s-aiplatform.googleapis.com", Region(region))
}

// ChatCompletionsEndpoint builds the OpenAI-compatible Vertex endpoint. The
// shape is fixed by Google, so a provider that names its project and region
// does not also have to paste a 130-character URL and get every segment right.
func ChatCompletionsEndpoint(project, region string) string {
	region = Region(region)

	return fmt.Sprintf("%s/v1/projects/%s/locations/%s/endpoints/openapi/chat/completions",
		RegionalHost(region), project, region)
}
