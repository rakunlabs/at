package server

import (
	"context"

	"github.com/rakunlabs/at/internal/nativeauth"
)

// The janitor only needs the durable credential/mobile stores, so it runs even
// while the installation is unclaimed and independently of any policy version.
func (s *Server) startAuthJanitor(ctx context.Context) {
	if s.nativeAuth != nil {
		go s.nativeAuth.RunJanitor(ctx)
		return
	}
	if a := nativeauth.NewJanitor(s.store); a != nil {
		go a.RunJanitor(ctx)
	}
}
