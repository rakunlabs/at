package config

import (
	"fmt"
	"net/netip"
	"strings"
)

// The three client-address headers AT knows how to read. They are canonical
// header names, not free-form strings: a typo in a header name would silently
// make every forwarded address unreadable and record the proxy instead, which
// is exactly the failure this configuration exists to fix.
const (
	TrustedProxyHeaderXForwardedFor = "X-Forwarded-For"
	TrustedProxyHeaderXRealIP       = "X-Real-IP"
	TrustedProxyHeaderForwarded     = "Forwarded"
)

// trustedProxyAliases name the ranges an operator cannot enumerate. In
// Kubernetes or Compose the ingress pod's address is assigned from a pool and
// changes on every reschedule, so requiring literal addresses would push
// operators toward trusting everything.
var trustedProxyAliases = map[string][]string{
	"loopback": {"127.0.0.0/8", "::1/128"},
	"private":  {"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "fc00::/7", "fe80::/10"},
}

// ParseTrustedProxies converts the configured entries into prefixes.
//
// Each entry is a CIDR block, a single IP address, or one of the aliases
// `loopback` / `private`. A single entry may itself be a comma-separated list
// so the value survives being passed through one environment variable.
//
// An empty result means no forwarded header is ever read, which is the default
// and the pre-existing behaviour: only the socket peer is recorded.
func ParseTrustedProxies(entries []string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for _, entry := range entries {
		for _, item := range strings.Split(entry, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if alias, ok := trustedProxyAliases[strings.ToLower(item)]; ok {
				for _, block := range alias {
					prefixes = append(prefixes, netip.MustParsePrefix(block))
				}
				continue
			}
			if prefix, err := netip.ParsePrefix(item); err == nil {
				prefixes = append(prefixes, prefix.Masked())
				continue
			}
			addr, err := netip.ParseAddr(item)
			if err != nil {
				return nil, fmt.Errorf("server.trusted_proxies %q must be an IP address, a CIDR block, or one of loopback/private", item)
			}
			// Unmap so an IPv4-mapped IPv6 literal compares against the peer
			// address, which is unmapped before the membership test.
			addr = addr.Unmap().WithZone("")
			prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
		}
	}
	return prefixes, nil
}

// NormalizeTrustedProxyHeader canonicalizes the configured header name and
// defaults to X-Forwarded-For, the one every reverse proxy sets by default.
func NormalizeTrustedProxyHeader(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "x-forwarded-for":
		return TrustedProxyHeaderXForwardedFor, nil
	case "x-real-ip":
		return TrustedProxyHeaderXRealIP, nil
	case "forwarded":
		return TrustedProxyHeaderForwarded, nil
	default:
		return "", fmt.Errorf("server.trusted_proxy_header %q must be one of X-Forwarded-For, X-Real-IP or Forwarded", raw)
	}
}
