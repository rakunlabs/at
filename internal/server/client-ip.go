package server

import (
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/rakunlabs/at/internal/config"
)

// forwardedHopLimit bounds how many forwarded entries are inspected. The header
// is caller-influenced and net/http admits headers up to a megabyte, so an
// unbounded walk would be a free parsing loop. The client address is at the
// far end of the trusted suffix, which no real deployment measures in dozens.
const forwardedHopLimit = 64

// clientIPResolver resolves the address AT attributes a request to: what login
// history records, what the lockout audit logs, and what the per-source
// admission limiters bucket on.
//
// The socket peer is the only value the process observes directly. A forwarded
// header is caller input — anybody can send one — so it is read *only* when the
// peer is a configured trusted proxy, and the walk stops at the first address
// that is not itself a trusted proxy. That is what makes the result
// unforgeable: a client can prepend anything it likes to X-Forwarded-For, but
// its own connection appends the one entry it cannot choose, and everything to
// the left of the trusted suffix is ignored.
//
// With no trusted proxies configured (the default) this is exactly the previous
// behaviour: the peer, and nothing else.
type clientIPResolver struct {
	trusted []netip.Prefix
	header  string
}

func newClientIPResolver(cfg config.Server) (clientIPResolver, error) {
	trusted, err := config.ParseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		return clientIPResolver{}, err
	}
	header, err := config.NormalizeTrustedProxyHeader(cfg.TrustedProxyHeader)
	if err != nil {
		return clientIPResolver{}, err
	}
	return clientIPResolver{trusted: trusted, header: header}, nil
}

// requestPeer is the socket peer address, normalized so that the same client
// yields one key: the port is dropped, IPv4-mapped IPv6 collapses to IPv4, and
// the interface zone (which describes this host, not the peer) is removed.
func requestPeer(r *http.Request) netip.Addr {
	if addrPort, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		return addrPort.Addr().Unmap().WithZone("")
	}
	addr, err := netip.ParseAddr(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap().WithZone("")
}

func (c clientIPResolver) trusts(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	for _, prefix := range c.trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// addr reports the resolved client address, or the zero Addr when the peer is
// not an IP (an invalid RemoteAddr, or a unix socket).
func (c clientIPResolver) addr(r *http.Request) netip.Addr {
	peer := requestPeer(r)
	if len(c.trusted) == 0 || !c.trusts(peer) {
		return peer
	}
	// X-Real-IP carries a single address — the immediate proxy's view of its
	// own client — so there is no chain to walk and no suffix to strip.
	if c.header == config.TrustedProxyHeaderXRealIP {
		for _, value := range r.Header.Values(c.header) {
			if addr, ok := parseForwardedAddr(value); ok && !c.trusts(addr) {
				return addr
			}
		}
		return peer
	}
	for _, hop := range c.forwardedHops(r) {
		addr, ok := parseForwardedAddr(hop)
		if !ok {
			// An obfuscated or malformed entry ends the verifiable chain:
			// everything further left is hearsay. Fall back to the peer rather
			// than guessing, since both remaining candidates are proxies.
			return peer
		}
		if !c.trusts(addr) {
			return addr
		}
	}
	return peer
}

// clientIP renders the resolved address for storage and logging, or "" when it
// is unknown. An empty string is deliberate: a placeholder in an audit column
// would be indistinguishable from a real value.
func (c clientIPResolver) clientIP(r *http.Request) string {
	addr := c.addr(r)
	if !addr.IsValid() {
		return ""
	}
	return addr.String()
}

// limitKey is the durable admission bucket key. An address the process cannot
// parse keeps its raw form rather than collapsing into a shared bucket, so an
// unusual transport cannot rate-limit every other one along with it.
func (c clientIPResolver) limitKey(r *http.Request) string {
	if addr := c.addr(r); addr.IsValid() {
		return addr.String()
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// sourceKey is the in-process bucket key for maps that are capped by entry
// count. Unparseable addresses share one fallback bucket, because there the
// unbounded key space is the risk.
func (c clientIPResolver) sourceKey(r *http.Request) string {
	return c.addr(r).String()
}

// forwardedHops returns the header's addresses closest-proxy-first, which is
// the order the trusted suffix has to be stripped in. Entries appear in the
// header client-first, appended by each hop, so this reverses both the header
// values and each value's comma-separated list.
func (c clientIPResolver) forwardedHops(r *http.Request) []string {
	values := r.Header.Values(c.header)
	hops := make([]string, 0, 8)
	for i := len(values) - 1; i >= 0; i-- {
		elements := strings.Split(values[i], ",")
		for j := len(elements) - 1; j >= 0; j-- {
			hop := elements[j]
			if c.header == config.TrustedProxyHeaderForwarded {
				value, ok := forwardedForParam(hop)
				if !ok {
					// An element with no for= parameter carries no address at
					// all (by=/proto=/host= only); it is not a broken hop.
					continue
				}
				hop = value
			}
			hops = append(hops, hop)
			if len(hops) >= forwardedHopLimit {
				return hops
			}
		}
	}
	return hops
}

// forwardedForParam extracts the for= parameter of one RFC 7239 element.
// Quoted commas are not handled: splitting the header on commas first is the
// pragmatic reading, and a quoted comma only ever appears inside a value AT
// then fails to parse, which is handled as a broken hop.
func forwardedForParam(element string) (string, bool) {
	for _, param := range strings.Split(element, ";") {
		key, value, ok := strings.Cut(param, "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "for") {
			return value, true
		}
	}
	return "", false
}

// parseForwardedAddr accepts every shape these headers use in practice: a bare
// address, an address with a port, a bracketed IPv6 literal with or without a
// port, and RFC 7239's quoted form. RFC 7239 obfuscated identifiers (`_hidden`)
// and `unknown` are addresses the proxy declined to disclose, not parse errors,
// but they are equally unusable.
func parseForwardedAddr(raw string) (netip.Addr, bool) {
	value := strings.TrimSpace(strings.Trim(strings.TrimSpace(raw), `"`))
	if value == "" || strings.EqualFold(value, "unknown") || strings.HasPrefix(value, "_") {
		return netip.Addr{}, false
	}
	if addrPort, err := netip.ParseAddrPort(value); err == nil {
		return addrPort.Addr().Unmap().WithZone(""), true
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		value = value[1 : len(value)-1]
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap().WithZone(""), true
}
