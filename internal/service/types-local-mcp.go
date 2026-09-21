package service

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// A local MCP server is one running on the account holder's own computer,
// dialled by their browser rather than by AT. Every other MCP path in the
// product dials from the server process, so "localhost" there means the
// server's loopback; here it means the user's.
//
// The server stores these records and never opens a connection to one. That
// is the whole security position of the feature: the addresses are loopback
// and private by construction, so a server-side dial would be a request from
// AT into AT's own network, configured by any account.

// LocalMCPServer is one personal endpoint. It is deliberately not convertible
// into an MCPUpstream — an upstream is something the server dials.
type LocalMCPServer struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
	// Headers are secret. They are stored encrypted, redacted on ordinary
	// reads and returned in full only through an explicit per-record reveal.
	Headers map[string]string `json:"headers,omitempty"`

	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// Bounds. A personal registry is a handful of entries; these exist so a
// client cannot store an unbounded blob in a preference row.
const (
	LocalMCPMaxServers     = 20
	LocalMCPMaxNameRunes   = 64
	LocalMCPMaxURLBytes    = 2048
	LocalMCPMaxHeaders     = 16
	LocalMCPMaxHeaderKey   = 128
	LocalMCPMaxHeaderValue = 4096
)

// ValidateLocalMCPURL accepts only addresses a browser can reach on the user's
// own machine or network.
//
// This is not defence against the person configuring it — they control their
// own browser. It is what keeps the feature from becoming a workspace-policy
// bypass: a publicly reachable MCP routed through the browser would run with
// no CheckExecution, no mcp_tool admission and no server-side trace, while
// being exactly as usable as one configured in an MCP set. Restricting to
// addresses the server plausibly cannot reach keeps "local" meaning local.
func ValidateLocalMCPURL(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fmt.Errorf("URL is required")
	}
	if len(trimmed) > LocalMCPMaxURLBytes {
		return fmt.Errorf("URL is too long")
	}

	u, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL must use http or https")
	}
	if u.Host == "" {
		return fmt.Errorf("URL must include a host")
	}
	// Credentials in the URL would be stored in a field that is shown in
	// clear; headers are the encrypted place for them.
	if u.User != nil {
		return fmt.Errorf("put credentials in a header, not in the URL")
	}
	if !LocalMCPHostAllowed(u.Hostname()) {
		return fmt.Errorf(
			"%q is not a local address; register a reachable MCP server as an MCP set instead, "+
				"where workspace execution policy and tracing apply", u.Hostname())
	}

	return nil
}

// LocalMCPHostAllowed reports whether a host names the user's own machine or
// local network. Exported because the browser client re-checks it before
// dialling: a record may have been written by an older or hostile client.
func LocalMCPHostAllowed(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	h = strings.Trim(h, "[]")
	if h == "" {
		return false
	}
	// RFC 6761 reserves localhost (and anything under it) for the loopback.
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	// mDNS names on the local link.
	if strings.HasSuffix(h, ".local") {
		return true
	}

	ip := net.ParseIP(h)
	if ip == nil {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		// Carrier-grade NAT, which net.IP.IsPrivate does not cover and which
		// Tailscale and similar overlays hand out.
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true
		}
	}

	return ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

// NormalizeLocalMCPServers validates and canonicalizes a submitted registry.
// Returns the first problem found, naming the offending entry, because the
// caller is a person editing a form.
func NormalizeLocalMCPServers(servers []LocalMCPServer) ([]LocalMCPServer, error) {
	if len(servers) > LocalMCPMaxServers {
		return nil, fmt.Errorf("at most %d local MCP servers are supported", LocalMCPMaxServers)
	}

	out := make([]LocalMCPServer, 0, len(servers))
	seenID := make(map[string]bool, len(servers))
	seenName := make(map[string]bool, len(servers))

	for i := range servers {
		s := servers[i]
		s.Name = strings.TrimSpace(s.Name)
		s.URL = strings.TrimSpace(s.URL)

		if s.Name == "" {
			return nil, fmt.Errorf("server %d: name is required", i+1)
		}
		if len([]rune(s.Name)) > LocalMCPMaxNameRunes {
			return nil, fmt.Errorf("server %q: name is too long", s.Name)
		}
		// The name qualifies tool names on collision, so it has to be
		// unambiguous and usable inside an identifier.
		lower := strings.ToLower(s.Name)
		if seenName[lower] {
			return nil, fmt.Errorf("server %q: name is already used", s.Name)
		}
		seenName[lower] = true

		if err := ValidateLocalMCPURL(s.URL); err != nil {
			return nil, fmt.Errorf("server %q: %w", s.Name, err)
		}
		if s.ID != "" {
			if seenID[s.ID] {
				return nil, fmt.Errorf("server %q: duplicate id", s.Name)
			}
			seenID[s.ID] = true
		}

		if len(s.Headers) > LocalMCPMaxHeaders {
			return nil, fmt.Errorf("server %q: at most %d headers are supported", s.Name, LocalMCPMaxHeaders)
		}
		if len(s.Headers) > 0 {
			headers := make(map[string]string, len(s.Headers))
			for k, v := range s.Headers {
				key := strings.TrimSpace(k)
				if key == "" {
					return nil, fmt.Errorf("server %q: header name is required", s.Name)
				}
				if len(key) > LocalMCPMaxHeaderKey {
					return nil, fmt.Errorf("server %q: header name is too long", s.Name)
				}
				if len(v) > LocalMCPMaxHeaderValue {
					return nil, fmt.Errorf("server %q: header %q value is too long", s.Name, key)
				}
				headers[key] = v
			}
			s.Headers = headers
		} else {
			s.Headers = nil
		}

		out = append(out, s)
	}

	return out, nil
}
