package config

import (
	"net/netip"
	"testing"
)

// The parsed set decides whose forwarded header is believed, so every accepted
// spelling must produce prefixes that actually contain the addresses an
// operator meant, and nothing wider.
func TestParseTrustedProxies(t *testing.T) {
	tests := []struct {
		name     string
		entries  []string
		contains []string
		excludes []string
	}{
		{"empty trusts nothing", nil, nil, []string{"127.0.0.1", "10.0.0.1"}},
		{"single address", []string{"203.0.113.7"}, []string{"203.0.113.7"}, []string{"203.0.113.8"}},
		{"cidr block", []string{"10.0.0.0/8"}, []string{"10.0.0.1", "10.255.255.255"}, []string{"11.0.0.1", "::1"}},
		{"unmasked cidr is normalized", []string{"10.1.2.3/8"}, []string{"10.9.9.9"}, []string{"11.0.0.1"}},
		{"ipv6 address", []string{"2001:db8::1"}, []string{"2001:db8::1"}, []string{"2001:db8::2"}},
		{"loopback alias", []string{"loopback"}, []string{"127.0.0.1", "127.9.9.9", "::1"}, []string{"10.0.0.1"}},
		{"private alias", []string{"private"}, []string{"10.0.0.1", "172.16.5.5", "192.168.1.1", "fd00::1"}, []string{"203.0.113.1", "172.32.0.1"}},
		{"comma separated single entry", []string{"10.0.0.0/8, 192.168.0.0/16"}, []string{"10.0.0.1", "192.168.0.1"}, []string{"172.16.0.1"}},
		{"mixed entries and blanks", []string{"loopback", "", "  ", "203.0.113.7"}, []string{"::1", "203.0.113.7"}, []string{"198.51.100.1"}},
		// An IPv4-mapped literal must compare against the unmapped peer form,
		// or a configured proxy would silently never match.
		{"ipv4-mapped literal", []string{"::ffff:203.0.113.7"}, []string{"203.0.113.7"}, []string{"203.0.113.8"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefixes, err := ParseTrustedProxies(tt.entries)
			if err != nil {
				t.Fatalf("ParseTrustedProxies(%q): %v", tt.entries, err)
			}
			contains := func(raw string) bool {
				addr := netip.MustParseAddr(raw).Unmap()
				for _, prefix := range prefixes {
					if prefix.Contains(addr) {
						return true
					}
				}
				return false
			}
			for _, want := range tt.contains {
				if !contains(want) {
					t.Fatalf("%q should trust %s", tt.entries, want)
				}
			}
			for _, reject := range tt.excludes {
				if contains(reject) {
					t.Fatalf("%q must not trust %s", tt.entries, reject)
				}
			}
		})
	}
}

// A typo degrades into "trust nothing", whose symptom is identical to not
// configuring the feature at all, so it is reported at load instead.
func TestParseTrustedProxiesRejectsGarbage(t *testing.T) {
	for _, entry := range []string{"not-an-ip", "10.0.0.0/64", "10.0.0.256", "public", "10.0.0.1:8080", "http://proxy"} {
		t.Run(entry, func(t *testing.T) {
			if got, err := ParseTrustedProxies([]string{entry}); err == nil {
				t.Fatalf("ParseTrustedProxies(%q) = %v, want an error", entry, got)
			}
		})
	}
}

func TestNormalizeTrustedProxyHeader(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", TrustedProxyHeaderXForwardedFor},
		{"  ", TrustedProxyHeaderXForwardedFor},
		{"x-forwarded-for", TrustedProxyHeaderXForwardedFor},
		{"X-Forwarded-For", TrustedProxyHeaderXForwardedFor},
		{"X-Real-IP", TrustedProxyHeaderXRealIP},
		{"x-real-ip", TrustedProxyHeaderXRealIP},
		{"forwarded", TrustedProxyHeaderForwarded},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := NormalizeTrustedProxyHeader(tt.in)
			if err != nil || got != tt.want {
				t.Fatalf("NormalizeTrustedProxyHeader(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
			}
		})
	}
	// An unknown header name would read nothing and record the proxy forever.
	for _, in := range []string{"X-Client-IP", "CF-Connecting-IP", "true-client-ip"} {
		if _, err := NormalizeTrustedProxyHeader(in); err == nil {
			t.Fatalf("NormalizeTrustedProxyHeader(%q) must be rejected", in)
		}
	}
}
