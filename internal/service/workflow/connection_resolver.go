package workflow

import (
	"context"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// ConnectionKeySuffixes lists the trailing variable-name fragments that map
// to individual credential fields. A tool handler calling getVar("youtube_refresh_token")
// is broken into provider = "youtube" and suffix = "_refresh_token" so the
// resolver can look up the bound connection's credentials.
var ConnectionKeySuffixes = []string{
	"_refresh_token",
	"_client_secret",
	"_client_id",
	"_access_token",
	"_api_key",
}

// ResolveConnectionKey returns (provider, suffix, ok) for a variable key that
// follows the "<provider><suffix>" convention, where suffix is one of the
// well-known fragments above.
//
//	"youtube_refresh_token" -> ("youtube", "_refresh_token", true)
//	"openai_api_key"        -> ("openai",  "_api_key",        true)
//	"random_variable"       -> ("",         "",                false)
func ResolveConnectionKey(key string) (provider, suffix string, ok bool) {
	for _, s := range ConnectionKeySuffixes {
		if strings.HasSuffix(key, s) && len(key) > len(s) {
			return key[:len(key)-len(s)], s, true
		}
	}
	return "", "", false
}

// ConnectionCredentialForKey returns the credential bundle field that a given
// key suffix maps to. Returns "" if the credential is not set.
func ConnectionCredentialForKey(creds service.ConnectionCredentials, suffix, fullKey string) string {
	switch suffix {
	case "_client_id":
		return creds.ClientID
	case "_client_secret":
		return creds.ClientSecret
	case "_refresh_token":
		return creds.RefreshToken
	case "_api_key":
		return creds.APIKey
	}
	// Extra bag is keyed by the original full variable name.
	if v, ok := creds.Extra[fullKey]; ok {
		return v
	}
	return ""
}

// ConnectionBindings maps a provider name to a resolved Connection. Built
// per-tool-call from the agent's and skill's binding maps before executing
// a tool handler.
type ConnectionBindings map[string]*service.Connection

// WrapVarLookupWithConnections returns a VarLookup that first consults
// connection bindings for known provider-scoped keys, then falls back to
// the supplied base lookup (which typically handles user_preferences and
// global variables).
//
// If bindings is nil or empty the base lookup is returned unchanged.
func WrapVarLookupWithConnections(base VarLookup, bindings ConnectionBindings) VarLookup {
	if len(bindings) == 0 {
		return base
	}
	return func(key string) (string, error) {
		provider, suffix, ok := ResolveConnectionKey(key)
		if ok {
			if conn, bound := bindings[provider]; bound && conn != nil {
				if v := ConnectionCredentialForKey(conn.Credentials, suffix, key); v != "" {
					return v, nil
				}
			}
		}
		if base == nil {
			return "", nil
		}
		return base(key)
	}
}

// WrapVarListerWithConnections returns a VarLister that overlays connection-
// bound credential values onto the base lister's output. This is used by
// bash handlers which receive the full VAR_* environment map at once.
// Connection values take priority: if the base lister returns a value for
// a key that is also bound via a connection, the binding wins.
func WrapVarListerWithConnections(base VarLister, bindings ConnectionBindings) VarLister {
	if len(bindings) == 0 {
		return base
	}
	return func() (map[string]string, error) {
		var out map[string]string
		if base != nil {
			m, err := base()
			if err != nil {
				return nil, err
			}
			out = m
		}
		if out == nil {
			out = map[string]string{}
		}
		for provider, conn := range bindings {
			if conn == nil {
				continue
			}
			if conn.Credentials.ClientID != "" {
				out[provider+"_client_id"] = conn.Credentials.ClientID
			}
			if conn.Credentials.ClientSecret != "" {
				out[provider+"_client_secret"] = conn.Credentials.ClientSecret
			}
			if conn.Credentials.RefreshToken != "" {
				out[provider+"_refresh_token"] = conn.Credentials.RefreshToken
			}
			if conn.Credentials.APIKey != "" {
				out[provider+"_api_key"] = conn.Credentials.APIKey
			}
			for k, v := range conn.Credentials.Extra {
				if v != "" {
					out[k] = v
				}
			}
		}
		return out, nil
	}
}

// ResolveAgentConnectionBindings loads the Connection records referenced by
// an agent's Config.Connections map, overlaying per-skill overrides if
// skillConnections is non-nil. Providers with invalid IDs or missing
// connections are silently skipped; callers fall back to the base var lookup.
func ResolveAgentConnectionBindings(
	ctx context.Context,
	lookup ConnectionLookup,
	agentConnections map[string]string,
	skillConnections map[string]string,
) ConnectionBindings {
	if lookup == nil {
		return nil
	}
	merged := map[string]string{}
	for p, id := range agentConnections {
		if id != "" {
			merged[p] = id
		}
	}
	for p, id := range skillConnections {
		if id != "" {
			merged[p] = id // per-skill override wins
		}
	}
	if len(merged) == 0 {
		return nil
	}
	out := make(ConnectionBindings, len(merged))
	for provider, id := range merged {
		conn, err := lookup(ctx, id)
		if err != nil || conn == nil {
			continue
		}
		out[provider] = conn
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
