package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/password"
	"golang.org/x/time/rate"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

// nativeAuthSettings owns immutable per-version coordinators, not mutable a.cfg.
// Every request reads the durable policy; replicas observe changes without TTLs.
type nativeAuthSettings struct {
	store   service.AuthSettingsStorer
	backend any
	cfg     config.Server
	mu      sync.Mutex
	version int64
	native  *nativeAuth
	routes  http.Handler
	limit   *rate.Limiter
	slots   chan struct{}
}

type nativeRuntimeContextKey struct{}

func nativeRuntimeFromRequest(r *http.Request, fallback *nativeAuth) *nativeAuth {
	if a, ok := r.Context().Value(nativeRuntimeContextKey{}).(*nativeAuth); ok {
		return a
	}
	return fallback
}

func newNativeAuthSettings(ctx context.Context, cfg config.Server, backend any) (*nativeAuthSettings, error) {
	store, ok := backend.(service.AuthSettingsStorer)
	if !ok {
		return nil, fmt.Errorf("runtime authentication requires persistent AuthSettingsStorer")
	}
	// Don't validate stale legacy configuration after a durable import exists.
	state, err := store.GetAuthSettings(ctx)
	if err != nil {
		return nil, err
	}
	if state == nil {
		initial, err := initialAuthSettings(cfg)
		if err != nil {
			return nil, err
		}
		state, err = store.InitializeAuthSettings(ctx, initial)
		if err != nil {
			return nil, err
		}
	}
	if state == nil {
		return nil, fmt.Errorf("authentication settings unavailable")
	}
	m := &nativeAuthSettings{store: store, backend: backend, cfg: cfg, limit: rate.NewLimiter(rate.Every(6*time.Second), 5), slots: make(chan struct{}, 2)}
	if !state.SetupRequired {
		if _, _, err := m.snapshot(state.Settings); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *nativeAuthSettings) snapshot(v service.AuthSettings) (*nativeAuth, http.Handler, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.native != nil && m.version == v.Version && m.native.cfg.Origin == v.Origin {
		return m.native, m.routes, nil
	}
	a, err := newNativeAuth(withAuthSettings(m.cfg, v), m.backend)
	if err != nil {
		return nil, nil, err
	}
	if a == nil {
		return nil, nil, fmt.Errorf("native authentication unavailable")
	}
	// Changing policy must not reset password admission or concurrency limits.
	a.loginLimit, a.passwordSlots = m.limit, m.slots
	mux := ada.New()
	a.register(mux, m.cfg.BasePath)
	external, err := newNativeExternalAuth(a, m.backend, nativeExternalCoordinatorHooks(a))
	if err != nil {
		return nil, nil, err
	}
	external.register(mux, m.cfg.BasePath)
	m.version, m.native, m.routes = v.Version, a, mux
	return a, mux, nil
}

func (m *nativeAuthSettings) load(w http.ResponseWriter, r *http.Request) *service.AuthSettingsState {
	w.Header().Set("Cache-Control", "no-store")
	v, err := m.store.GetAuthSettings(r.Context())
	if err != nil || v == nil {
		nativeError(w, 503, "authentication unavailable")
		return nil
	}
	return v
}

func (m *nativeAuthSettings) status(w http.ResponseWriter, r *http.Request) {
	v := m.load(w, r)
	if v == nil {
		return
	}
	status := map[string]any{"enabled": true, "setup_required": v.SetupRequired, "local_login": v.Settings.LocalLoginEnabled, "display_title": v.Settings.DisplayTitle, "signup_admission": v.Settings.SignupAdmission, "remember_me": true, "passkey_login": "username-first", "passkeys": false}
	if !v.SetupRequired {
		a, _, err := m.snapshot(v.Settings)
		if err != nil {
			nativeError(w, 503, "authentication unavailable")
			return
		}
		status["passkeys"] = a.passkey != nil && v.Settings.LocalLoginEnabled
		if a.mobileStore != nil {
			status["mobile_auth"] = a.mobileDescriptor()
		}
	}
	httpResponseJSON(w, status, 200)
}

// The socket Host and exact Origin must agree. Reverse proxies may terminate TLS
// and preserve Host; forwarded host/proto/client headers never establish trust.
// An operator-pinned origin also rejects a malicious but self-consistent Host.
func nativeSetupOrigin(r *http.Request, pinned, requested string) (string, error) {
	origin := r.Header.Get("Origin")
	if len(r.Header.Values("Origin")) != 1 || service.ValidateAuthOrigin(origin) != nil || (requested != "" && requested != origin) || (pinned != "" && pinned != origin) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return "", fmt.Errorf("same-origin setup required")
	}
	u, _ := url.Parse(origin)
	if r.Host != u.Host || (r.TLS != nil && u.Scheme != "https") {
		return "", fmt.Errorf("deployment Host and Origin must match")
	}
	return origin, nil
}

func (m *nativeAuthSettings) setup(w http.ResponseWriter, r *http.Request) {
	v := m.load(w, r)
	if v == nil {
		return
	}
	if !v.SetupRequired {
		nativeError(w, 409, "installation setup already completed")
		return
	}
	if _, err := nativeSetupOrigin(r, v.Settings.Origin, ""); err != nil {
		nativeError(w, 403, err.Error())
		return
	}
	if !m.limit.Allow() {
		nativeError(w, 429, "setup rate limit exceeded")
		return
	}
	// Durable source bounds survive replicas/restarts; don't hash arbitrary XFF.
	admission, ok := m.backend.(service.AuthSecurityAdmissionStorer)
	if !ok {
		nativeError(w, 503, "authentication admission unavailable")
		return
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	allowed, err := admission.AdmitAuthSecuritySource(r.Context(), nativeSessionHash(host))
	if err != nil {
		nativeError(w, 503, "authentication unavailable")
		return
	}
	if !allowed {
		nativeError(w, 429, "setup rate limit exceeded")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Origin   string `json:"origin"`
	}
	if !decodeNativeBody(w, r, &body) {
		return
	}
	origin, err := nativeSetupOrigin(r, v.Settings.Origin, body.Origin)
	if err != nil {
		nativeError(w, 403, err.Error())
		return
	}
	body.Username = normalizeNativeUsername(body.Username)
	if !validNativeUsername(body.Username) {
		nativeError(w, 400, "username must be 3-128 ASCII letters, digits, or ._@+-")
		return
	}
	select {
	case m.slots <- struct{}{}:
	default:
		nativeError(w, 429, "authentication busy; retry later")
		return
	}
	defer func() { <-m.slots }()
	hasher := password.PBKDF2{MinLength: nativePasswordMinLength}
	hash, err := hasher.Hash(body.Password)
	if err != nil {
		nativeError(w, 400, nativePasswordMessage(body.Password))
		return
	}
	v.Settings.Origin = origin
	// Validate the complete coordinator before committing an installation claim.
	if _, _, err := m.snapshot(v.Settings); err != nil {
		nativeError(w, 503, "authentication configuration unavailable")
		return
	}
	user, err := m.store.SetupAuth(r.Context(), service.AuthUser{Username: body.Username, PasswordHash: hash}, v.Settings)
	if errors.Is(err, service.ErrAuthConflict) {
		nativeError(w, 409, "installation setup already completed or username unavailable")
		return
	}
	if err != nil {
		nativeError(w, 503, "authentication unavailable")
		return
	}
	httpResponseJSON(w, authIdentity(user), 201)
}

// require is the runtime replacement for a.require on management groups. Parent
// authorization can compose this with workspace membership checks unchanged.
func (m *nativeAuthSettings) require(admin bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return m.withRuntime(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a := nativeRuntimeFromRequest(r, nil)
			a.require(admin)(next).ServeHTTP(w, r)
		}))
	}
}

// withRuntime only loads policy and gates setup; it does NOT authenticate a user.
// Workspace authentication uses this before its own transport/session middleware,
// which intentionally permits mobile access beyond the native /auth/me route.
func (m *nativeAuthSettings) withRuntime(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := m.load(w, r)
		if v == nil {
			return
		}
		if v.SetupRequired {
			nativeError(w, 403, "installation setup required")
			return
		}
		a, _, err := m.snapshot(v.Settings)
		if err != nil {
			nativeError(w, 503, "authentication unavailable")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), nativeRuntimeContextKey{}, a)))
	})
}

func (m *nativeAuthSettings) settings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		v := m.load(w, r)
		if v != nil {
			httpResponseJSON(w, v.Settings, 200)
		}
		return
	}
	var v service.AuthSettings
	if !decodeNativeBody(w, r, &v) {
		return
	}
	if err := v.Validate(false); err != nil {
		nativeError(w, 400, err.Error())
		return
	}
	saved, err := m.store.SaveAuthSettings(r.Context(), v)
	if errors.Is(err, service.ErrAuthConflict) {
		nativeError(w, 409, "settings changed, origin is pinned, or no usable external administrator")
		return
	}
	if err != nil {
		nativeError(w, 503, "authentication unavailable")
		return
	}
	httpResponseJSON(w, saved, 200)
}

func (m *nativeAuthSettings) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, m.cfg.BasePath)
	switch {
	case path == "/auth/status" && r.Method == http.MethodGet:
		m.status(w, r)
		return
	case path == "/auth/setup" && r.Method == http.MethodPost:
		m.setup(w, r)
		return
	case path == "/auth/settings" && (r.Method == http.MethodGet || r.Method == http.MethodPut):
		m.require(true)(http.HandlerFunc(m.settings)).ServeHTTP(w, r)
		return
	}
	v := m.load(w, r)
	if v == nil {
		return
	}
	if v.SetupRequired {
		// Preserve the configured token bootstrap API, using its exact origin.
		if path != "/auth/bootstrap" || m.cfg.NativeAuth == nil || m.cfg.NativeAuth.BootstrapToken == "" || v.Settings.Origin == "" {
			nativeError(w, 403, "installation setup required")
			return
		}
	}
	if !v.Settings.LocalLoginEnabled && (path == "/auth/login" || strings.HasPrefix(path, "/auth/passkeys/login")) {
		nativeError(w, 403, "local login is disabled")
		return
	}
	a, routes, err := m.snapshot(v.Settings)
	if err != nil {
		nativeError(w, 503, "authentication unavailable")
		return
	}
	routes.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), nativeRuntimeContextKey{}, a)))
}

func (m *nativeAuthSettings) register(mux *ada.Server, base string) {
	mux.Handle(base+"/auth/*", m)
}
