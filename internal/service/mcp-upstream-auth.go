package service

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/rakunlabs/at/internal/service/mcpauth"
)

// MCPAuthTypeOAuth2 is the only upstream authentication type.
const MCPAuthTypeOAuth2 = "oauth2"

// NormalizeMCPUpstreamAuth validates and canonicalizes the auth block of every
// upstream in place: the provider key defaults from the URL, account sources
// default to ["user"], and scopes are trimmed. It checks shape only;
// the shared connection reference is checked against the store by the caller.
func NormalizeMCPUpstreamAuth(upstreams []MCPUpstream) error {
	for i := range upstreams {
		u := &upstreams[i]
		if u.Auth == nil {
			continue
		}
		label := fmt.Sprintf("upstream %d", i+1)
		a := u.Auth
		a.Type = strings.TrimSpace(a.Type)
		if a.Type == "" {
			a.Type = MCPAuthTypeOAuth2
		}
		if a.Type != MCPAuthTypeOAuth2 {
			return fmt.Errorf("%s: unsupported authentication type %q", label, a.Type)
		}
		if strings.TrimSpace(u.Command) != "" {
			return fmt.Errorf("%s: OAuth applies to HTTP MCP servers only; stdio servers take credentials through env", label)
		}
		if strings.TrimSpace(u.URL) == "" {
			return fmt.Errorf("%s: OAuth requires an HTTP URL", label)
		}
		for key := range u.Headers {
			if strings.EqualFold(http.CanonicalHeaderKey(strings.TrimSpace(key)), "Authorization") {
				return fmt.Errorf("%s: remove the static Authorization header; OAuth supplies it", label)
			}
		}
		a.Provider = strings.TrimSpace(a.Provider)
		if a.Provider == "" {
			a.Provider = mcpauth.ProviderForURL(u.URL)
		}
		if !mcpauth.ValidProvider(a.Provider) {
			return fmt.Errorf("%s: provider key %q must be 2-64 lowercase letters, digits, '-' or '_'", label, a.Provider)
		}
		if a.Provider == GitSSHCredentialProvider {
			return fmt.Errorf("%s: provider key %q is reserved", label, a.Provider)
		}
		if len(a.Accounts) == 0 {
			a.Accounts = []string{MCPAccountUser}
		}
		seen := map[string]bool{}
		for j, source := range a.Accounts {
			source = strings.TrimSpace(source)
			a.Accounts[j] = source
			if source != MCPAccountUser && source != MCPAccountAgent && source != MCPAccountShared {
				return fmt.Errorf("%s: unknown account source %q (use user, agent or shared)", label, source)
			}
			if seen[source] {
				return fmt.Errorf("%s: account source %q is listed twice", label, source)
			}
			seen[source] = true
		}
		a.SharedConnectionID = strings.TrimSpace(a.SharedConnectionID)
		if seen[MCPAccountShared] && a.SharedConnectionID == "" {
			return fmt.Errorf("%s: the shared account source needs a shared connection", label)
		}
		if !seen[MCPAccountShared] {
			// A shared connection that is not listed would never be used; keeping
			// it would only make the configuration look like it was.
			a.SharedConnectionID = ""
		}
		var scopes []string
		for _, scope := range a.Scopes {
			for _, s := range strings.Fields(scope) {
				if !slices.Contains(scopes, s) {
					scopes = append(scopes, s)
				}
			}
		}
		a.Scopes = scopes
		a.ClientID = strings.TrimSpace(a.ClientID)
		a.AuthorizationServer = strings.TrimSpace(a.AuthorizationServer)
		if a.AuthorizationServer != "" && !strings.HasPrefix(a.AuthorizationServer, "https://") && !(strings.HasPrefix(a.AuthorizationServer, "http://") && mcpauth.AllowsPrivate(u.URL)) {
			return fmt.Errorf("%s: the authorization server must be an https URL", label)
		}
	}
	return nil
}

// ErrMCPAccountMissing reports that no listed account source has a usable
// connection. Its message tells the user how to connect one.
var ErrMCPAccountMissing = errors.New("no connected MCP account")

// SameMCPResource reports whether a credential authorized for authorizedURL
// may be sent to upstreamURL. Tokens are audience-bound to one server, and a
// provider key is just a label any set writer can choose, so the comparison
// is on the canonical URL itself.
func SameMCPResource(authorizedURL, upstreamURL string) bool {
	a := strings.TrimSuffix(mcpauth.CanonicalResource(authorizedURL), "/")
	b := strings.TrimSuffix(mcpauth.CanonicalResource(upstreamURL), "/")
	return a != "" && a == b
}

// ErrInvalidMCPConfig marks an MCP set/server configuration the caller must
// correct (HTTP 400), as opposed to a storage failure.
var ErrInvalidMCPConfig = errors.New("invalid MCP configuration")
