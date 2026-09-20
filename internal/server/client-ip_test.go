package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/config"
)

func testResolver(t *testing.T, header string, trusted ...string) clientIPResolver {
	t.Helper()
	resolver, err := newClientIPResolver(config.Server{TrustedProxies: trusted, TrustedProxyHeader: header})
	if err != nil {
		t.Fatalf("newClientIPResolver(%q, %q): %v", header, trusted, err)
	}
	return resolver
}

func ipRequest(peer string, header string, values ...string) *http.Request {
	r := httptest.NewRequest("POST", "/auth/login", nil)
	r.RemoteAddr = peer
	for _, value := range values {
		r.Header.Add(header, value)
	}
	return r
}

// A forwarded header is caller input. It is read only when the socket peer is a
// configured proxy, and only the suffix of hops that are themselves configured
// proxies may be stripped — everything to the left of that is client-supplied.
func TestClientIPResolver(t *testing.T) {
	tests := []struct {
		name    string
		trusted []string
		header  string
		peer    string
		values  []string
		want    string
	}{
		{"no trusted proxies ignores the header", nil, "X-Forwarded-For", "192.0.2.10:1234", []string{"203.0.113.9"}, "192.0.2.10"},
		{"untrusted peer cannot spoof", []string{"10.0.0.0/8"}, "X-Forwarded-For", "192.0.2.10:1234", []string{"203.0.113.9"}, "192.0.2.10"},
		{"trusted peer with no header", []string{"10.0.0.0/8"}, "X-Forwarded-For", "10.0.0.5:1234", nil, "10.0.0.5"},
		{"trusted peer reports client", []string{"10.0.0.0/8"}, "X-Forwarded-For", "10.0.0.5:1234", []string{"203.0.113.9"}, "203.0.113.9"},
		// The rightmost entry each hop appends is the one it observed itself.
		{"client-prepended entries are discarded", []string{"10.0.0.0/8"}, "X-Forwarded-For", "10.0.0.5:1234", []string{"198.51.100.1, 203.0.113.9"}, "203.0.113.9"},
		{"trusted chain is stripped", []string{"10.0.0.0/8"}, "X-Forwarded-For", "10.0.0.5:1234", []string{"203.0.113.9, 10.0.0.9, 10.0.0.7"}, "203.0.113.9"},
		{"split across repeated headers", []string{"10.0.0.0/8"}, "X-Forwarded-For", "10.0.0.5:1234", []string{"203.0.113.9", "10.0.0.9"}, "203.0.113.9"},
		{"whitespace and ports are tolerated", []string{"10.0.0.0/8"}, "X-Forwarded-For", "10.0.0.5:1234", []string{"  203.0.113.9:44321 , 10.0.0.9 "}, "203.0.113.9"},
		{"bracketed ipv6 with port", []string{"10.0.0.0/8"}, "X-Forwarded-For", "10.0.0.5:1234", []string{"[2001:db8::42]:443"}, "2001:db8::42"},
		{"ipv4-mapped client collapses", []string{"10.0.0.0/8"}, "X-Forwarded-For", "10.0.0.5:1234", []string{"::ffff:203.0.113.9"}, "203.0.113.9"},
		// Nothing left to attribute the request to but the proxy itself.
		{"all hops trusted falls back to peer", []string{"10.0.0.0/8"}, "X-Forwarded-For", "10.0.0.5:1234", []string{"10.0.0.9, 10.0.0.7"}, "10.0.0.5"},
		{"garbage ends the chain", []string{"10.0.0.0/8"}, "X-Forwarded-For", "10.0.0.5:1234", []string{"203.0.113.9, nonsense, 10.0.0.9"}, "10.0.0.5"},
		{"empty header value", []string{"10.0.0.0/8"}, "X-Forwarded-For", "10.0.0.5:1234", []string{""}, "10.0.0.5"},
		{"loopback alias", []string{"loopback"}, "X-Forwarded-For", "127.0.0.1:1234", []string{"203.0.113.9"}, "203.0.113.9"},
		{"local proxy without trust reports ipv6 loopback", nil, "X-Forwarded-For", "[::1]:1234", []string{"203.0.113.9"}, "::1"},
		{"private alias does not trust loopback", []string{"private"}, "X-Forwarded-For", "[::1]:1234", []string{"203.0.113.9"}, "::1"},
		{"local proxy with loopback trust reports client", []string{"loopback"}, "X-Forwarded-For", "[::1]:1234", []string{"203.0.113.9"}, "203.0.113.9"},
		{"local proxy appends observed client after forged prefix", []string{"loopback"}, "X-Forwarded-For", "[::1]:1234", []string{"198.51.100.1, 203.0.113.9"}, "203.0.113.9"},
		{"ipv6 trusted peer", []string{"2001:db8::/32"}, "X-Forwarded-For", "[2001:db8::5]:1234", []string{"203.0.113.9"}, "203.0.113.9"},
		{"peer port is dropped", []string{"10.0.0.0/8"}, "X-Forwarded-For", "192.0.2.10:9999", nil, "192.0.2.10"},

		{"x-real-ip", []string{"10.0.0.0/8"}, "X-Real-IP", "10.0.0.5:1234", []string{"203.0.113.9"}, "203.0.113.9"},
		{"x-real-ip is not a list", []string{"10.0.0.0/8"}, "X-Real-IP", "10.0.0.5:1234", []string{"203.0.113.9, 198.51.100.1"}, "10.0.0.5"},
		{"x-real-ip from untrusted peer", []string{"10.0.0.0/8"}, "X-Real-IP", "192.0.2.10:1234", []string{"203.0.113.9"}, "192.0.2.10"},
		{"x-forwarded-for ignored when x-real-ip selected", []string{"10.0.0.0/8"}, "X-Real-IP", "10.0.0.5:1234", nil, "10.0.0.5"},

		{"rfc7239 forwarded", []string{"10.0.0.0/8"}, "Forwarded", "10.0.0.5:1234", []string{`for=203.0.113.9`}, "203.0.113.9"},
		{"rfc7239 quoted ipv6", []string{"10.0.0.0/8"}, "Forwarded", "10.0.0.5:1234", []string{`for="[2001:db8::42]:443";proto=https`}, "2001:db8::42"},
		{"rfc7239 chain", []string{"10.0.0.0/8"}, "Forwarded", "10.0.0.5:1234", []string{`for=203.0.113.9, for=10.0.0.9;by=10.0.0.5`}, "203.0.113.9"},
		{"rfc7239 obfuscated hop", []string{"10.0.0.0/8"}, "Forwarded", "10.0.0.5:1234", []string{`for=203.0.113.9, for=_hidden`}, "10.0.0.5"},
		{"rfc7239 element without for", []string{"10.0.0.0/8"}, "Forwarded", "10.0.0.5:1234", []string{`proto=https, for=203.0.113.9`}, "203.0.113.9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := testResolver(t, tt.header, tt.trusted...)
			if got := resolver.clientIP(ipRequest(tt.peer, tt.header, tt.values...)); got != tt.want {
				t.Fatalf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

// An address the process cannot parse is not rendered as a plausible-looking
// value: an audit column must not be able to lie about what was observed.
func TestClientIPResolverUnknownPeer(t *testing.T) {
	resolver := testResolver(t, "", "10.0.0.0/8")
	r := ipRequest("@", "X-Forwarded-For", "203.0.113.9")
	if got := resolver.clientIP(r); got != "" {
		t.Fatalf("clientIP() = %q, want empty", got)
	}
	// Durable admission keeps the raw form so one odd transport cannot
	// rate-limit every other one with it; the in-process map collapses them
	// into a single bucket instead, because there the key space is the risk.
	if got := resolver.limitKey(r); got != "@" {
		t.Fatalf("limitKey() = %q, want %q", got, "@")
	}
	if got, other := resolver.sourceKey(r), resolver.sourceKey(ipRequest("!", "X-Forwarded-For")); got != other {
		t.Fatalf("sourceKey() = %q and %q, want one shared bucket", got, other)
	}
}

// The header is caller-influenced and net/http admits very large ones, so the
// walk is bounded rather than proportional to whatever was sent.
func TestClientIPResolverBoundsForwardedHops(t *testing.T) {
	resolver := testResolver(t, "", "10.0.0.0/8")
	hops := make([]string, 0, forwardedHopLimit*4)
	hops = append(hops, "203.0.113.9")
	for i := 0; i < forwardedHopLimit*4; i++ {
		hops = append(hops, "10.0.0.9")
	}
	r := ipRequest("10.0.0.5:1234", "X-Forwarded-For", strings.Join(hops, ","))
	// Past the bound the real client is unreachable, so the peer is reported
	// rather than an arbitrary hop from the middle of the chain.
	if got := resolver.clientIP(r); got != "10.0.0.5" {
		t.Fatalf("clientIP() = %q, want the peer", got)
	}
}
