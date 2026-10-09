package mcpauth

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// ParseProxy validates an explicit forward proxy. Credentials belong in a
// separately secured proxy, not in the readable MCP configuration URL.
func ParseProxy(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("proxy must be a URL with a host and optional port, without credentials, path, query or fragment")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, errors.New("proxy scheme must be http, https, socks5 or socks5h")
	}
	if port := u.Port(); strings.HasSuffix(u.Host, ":") || (port != "" && !validProxyPort(port)) {
		return nil, errors.New("proxy port must be between 1 and 65535")
	}
	return u, nil
}

func validProxyPort(port string) bool {
	n := 0
	for _, c := range port {
		if c < '0' || c > '9' {
			return false
		}
		n = n*10 + int(c-'0')
		if n > 65535 {
			return false
		}
	}
	return n > 0
}

// ClientWithProxy keeps endpoint/redirect validation, but explicitly trusts
// the configured proxy to route and resolve destinations. Ambient proxies are
// still never used. Callers must admit host execution before using a proxy.
func ClientWithProxy(allowPrivate bool, raw string) (*http.Client, error) {
	return ClientWithTransport(allowPrivate, raw, false)
}

// ClientWithTransport applies explicit per-upstream network settings to OAuth.
// TLS verification stays enabled unless the operator deliberately disables it.
func ClientWithTransport(allowPrivate bool, raw string, insecureSkipVerify bool) (*http.Client, error) {
	u, err := ParseProxy(raw)
	if err != nil {
		return nil, err
	}
	client := Client(allowPrivate)
	client.Transport.(*endpointTransport).base.TLSClientConfig = &tls.Config{InsecureSkipVerify: insecureSkipVerify} // explicit operator opt-in
	if u != nil {
		transport := client.Transport.(*endpointTransport)
		// Validate the proxy dial with the private-network client (operators
		// commonly run proxies on loopback), while retaining destination checks.
		transport.base.DialContext = Client(true).Transport.(*endpointTransport).base.DialContext
		transport.base.Proxy = http.ProxyURL(u)
	}
	return client, nil
}
