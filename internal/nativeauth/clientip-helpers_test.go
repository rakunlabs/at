package nativeauth

import (
	"testing"

	"github.com/rakunlabs/at/internal/clientip"
	"github.com/rakunlabs/at/internal/config"
)

func testResolver(t *testing.T, header string, trusted ...string) clientip.Resolver {
	t.Helper()
	resolver, err := clientip.New(config.Server{TrustedProxies: trusted, TrustedProxyHeader: header})
	if err != nil {
		t.Fatalf("clientip.New(%q, %q): %v", header, trusted, err)
	}
	return resolver
}
