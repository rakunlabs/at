package service

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// A local chat provider is an OpenAI-compatible endpoint that the account
// holder's browser calls directly from Chats — a model server on their own
// computer (Ollama, LM Studio, llama.cpp, vLLM) or a hosted API with their own
// key. AT stores the record and never sends a request to it.
//
// That is the security position of the feature, as for local MCP servers: the
// address is chosen by any account and is frequently a loopback or private
// one, so a server-side request would be a request from AT into AT's own
// network. The browser is the only party that ever dials it.

// LocalChatProvider is one personal endpoint. It is deliberately not
// convertible into a service.Provider — a provider is something the server
// calls.
type LocalChatProvider struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// BaseURL is the OpenAI-compatible API root, e.g. http://127.0.0.1:11434/v1.
	// The browser calls <base>/models and <base>/chat/completions.
	BaseURL string `json:"base_url"`
	// APIKey and Headers are secret: stored encrypted, redacted on ordinary
	// reads and returned only through an explicit per-record reveal.
	APIKey  string            `json:"api_key,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`

	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// LocalChatProviderPrefix marks a model reference served by a local provider:
// "local:<name>/<model>". The browser resolves such a reference only against
// the account's own local registry and never sends it to the server's
// completion endpoint.
const LocalChatProviderPrefix = "local:"

const (
	LocalChatProviderMax            = 20
	LocalChatProviderMaxURLBytes    = 2048
	LocalChatProviderMaxKeyBytes    = 4096
	LocalChatProviderMaxHeaders     = 16
	LocalChatProviderMaxHeaderKey   = 128
	LocalChatProviderMaxHeaderValue = 4096
)

// The name becomes part of the model reference, so it is restricted to a
// shape that cannot contain the "/" separating it from the model.
var localChatProviderName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

// ValidateLocalChatProviderURL accepts an http(s) API root. Plain http is
// allowed only for local addresses: a key sent over http to a public host is
// readable by every network in between, and browsers block such requests from
// an https page anyway.
func ValidateLocalChatProviderURL(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fmt.Errorf("base URL is required")
	}
	if len(trimmed) > LocalChatProviderMaxURLBytes {
		return fmt.Errorf("base URL is too long")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("invalid base URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("base URL must use http or https")
	}
	if u.Host == "" {
		return fmt.Errorf("base URL must include a host")
	}
	if u.User != nil {
		return fmt.Errorf("put credentials in the API key or a header, not in the URL")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("base URL must not contain a query or fragment")
	}
	if u.Scheme == "http" && !LocalMCPHostAllowed(u.Hostname()) {
		return fmt.Errorf("%q is not a local address; use https", u.Hostname())
	}

	return nil
}

// NormalizeLocalChatProviders validates and canonicalizes a submitted
// registry, naming the offending entry in the first problem found.
func NormalizeLocalChatProviders(providers []LocalChatProvider) ([]LocalChatProvider, error) {
	if len(providers) > LocalChatProviderMax {
		return nil, fmt.Errorf("at most %d local providers are supported", LocalChatProviderMax)
	}

	out := make([]LocalChatProvider, 0, len(providers))
	seenID := make(map[string]bool, len(providers))
	seenName := make(map[string]bool, len(providers))

	for i := range providers {
		p := providers[i]
		p.Name = strings.ToLower(strings.TrimSpace(p.Name))
		p.BaseURL = strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")

		if p.Name == "" {
			return nil, fmt.Errorf("provider %d: name is required", i+1)
		}
		if !localChatProviderName.MatchString(p.Name) {
			return nil, fmt.Errorf("provider %q: name must be 1-32 lowercase letters, digits, dots, underscores or hyphens", p.Name)
		}
		if seenName[p.Name] {
			return nil, fmt.Errorf("provider %q: name is already used", p.Name)
		}
		seenName[p.Name] = true

		if err := ValidateLocalChatProviderURL(p.BaseURL); err != nil {
			return nil, fmt.Errorf("provider %q: %w", p.Name, err)
		}
		if p.ID != "" {
			if seenID[p.ID] {
				return nil, fmt.Errorf("provider %q: duplicate id", p.Name)
			}
			seenID[p.ID] = true
		}

		p.APIKey = strings.TrimSpace(p.APIKey)
		if len(p.APIKey) > LocalChatProviderMaxKeyBytes {
			return nil, fmt.Errorf("provider %q: API key is too long", p.Name)
		}

		if len(p.Headers) > LocalChatProviderMaxHeaders {
			return nil, fmt.Errorf("provider %q: at most %d headers are supported", p.Name, LocalChatProviderMaxHeaders)
		}
		if len(p.Headers) > 0 {
			headers := make(map[string]string, len(p.Headers))
			for k, v := range p.Headers {
				key := strings.TrimSpace(k)
				if key == "" {
					return nil, fmt.Errorf("provider %q: header name is required", p.Name)
				}
				if len(key) > LocalChatProviderMaxHeaderKey {
					return nil, fmt.Errorf("provider %q: header name is too long", p.Name)
				}
				if len(v) > LocalChatProviderMaxHeaderValue {
					return nil, fmt.Errorf("provider %q: header %q value is too long", p.Name, key)
				}
				headers[key] = v
			}
			p.Headers = headers
		} else {
			p.Headers = nil
		}

		out = append(out, p)
	}

	return out, nil
}
