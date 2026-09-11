package server

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

// InitialAuthSettings is used only for an insert-only import, never at every
// request. Enabled:false no longer opens management access on an upgraded store.
func initialAuthSettings(s config.Server) (service.AuthSettings, error) {
	v := service.DefaultAuthSettings()
	if s.Name != "" {
		v.DisplayTitle = s.Name
	}
	if n := s.NativeAuth; n != nil {
		v.Origin = n.Origin
		if n.SessionTTL != 0 {
			v.SessionTTLSeconds = int64(n.SessionTTL / time.Second)
		}
		if n.RememberTTL != 0 {
			v.RememberTTLSeconds = int64(n.RememberTTL / time.Second)
		}
	}
	if v.Origin == "" && s.ExternalURL != "" {
		u, err := url.Parse(s.ExternalURL)
		if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (strings.TrimSuffix(u.Path, "/") != s.BasePath && u.Path != "") {
			return v, fmt.Errorf("external_url must match the deployment origin and base_path")
		}
		v.Origin = u.Scheme + "://" + u.Host
	}
	return v, v.Validate(true)
}

// WithAuthSettings creates an immutable native coordinator config. Infrastructure
// settings and the optional migration-only bootstrap token remain in config.
func withAuthSettings(s config.Server, v service.AuthSettings) config.Server {
	n := config.NativeAuth{}
	if s.NativeAuth != nil {
		n = *s.NativeAuth
	}
	n.Enabled, n.Origin = true, v.Origin
	n.SessionTTL = time.Duration(v.SessionTTLSeconds) * time.Second
	n.RememberTTL = time.Duration(v.RememberTTLSeconds) * time.Second
	n.InsecureHTTP = strings.HasPrefix(v.Origin, "http://")
	s.NativeAuth = &n
	// Legacy forward-header identity is not a second management authentication
	// path once native runtime policy owns the installation. The outer server
	// must likewise stop installing ForwardAuth for management routes.
	s.ForwardAuth = nil
	return s
}
