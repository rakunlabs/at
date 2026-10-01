package nativeauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/ada"
	str2duration "github.com/xhit/go-str2duration/v2"
	"golang.org/x/time/rate"

	"github.com/rakunlabs/at/internal/clientip"
	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/httpx"
	"github.com/rakunlabs/at/internal/service"
)

// Settings owns immutable per-version coordinators, not mutable a.cfg.
// Every request reads the durable policy; replicas observe changes without TTLs.
type Settings struct {
	store    service.AuthSettingsStorer
	backend  any
	cfg      config.Server
	clientIP clientip.Resolver

	mu      sync.Mutex
	version int64
	native  *Auth
	routes  http.Handler
	limit   *rate.Limiter
	slots   chan struct{}
}

type nativeRuntimeContextKey struct{}

func RuntimeFromRequest(r *http.Request, fallback *Auth) *Auth {
	if a, ok := r.Context().Value(nativeRuntimeContextKey{}).(*Auth); ok {
		return a
	}
	return fallback
}

func NewSettings(ctx context.Context, cfg config.Server, backend any) (*Settings, error) {
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
	resolver, err := clientip.New(cfg)
	if err != nil {
		return nil, err
	}
	m := &Settings{store: store, backend: backend, cfg: cfg, clientIP: resolver, limit: rate.NewLimiter(rate.Every(6*time.Second), 5), slots: make(chan struct{}, 2)}
	if !state.SetupRequired {
		if _, _, err := m.snapshot(state.Settings); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *Settings) snapshot(v service.AuthSettings) (*Auth, http.Handler, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.native != nil && m.version == v.Version && m.native.cfg.Origin == v.Origin {
		return m.native, m.routes, nil
	}
	a, err := New(withAuthSettings(m.cfg, v), m.backend)
	if err != nil {
		return nil, nil, err
	}
	if a == nil {
		return nil, nil, fmt.Errorf("native authentication unavailable")
	}
	// Changing policy must not reset password admission or concurrency limits.
	a.LoginLimit, a.passwordSlots = m.limit, m.slots
	a.allowedOrigins = append([]string(nil), v.AllowedOrigins...)
	mux := ada.New()
	a.Register(mux, m.cfg.BasePath)
	external, err := newNativeExternalAuth(a, m.backend, nativeExternalCoordinatorHooks(a))
	if err != nil {
		return nil, nil, err
	}
	external.register(mux, m.cfg.BasePath)
	m.version, m.native, m.routes = v.Version, a, mux
	return a, mux, nil
}

func (m *Settings) load(w http.ResponseWriter, r *http.Request) *service.AuthSettingsState {
	w.Header().Set("Cache-Control", "no-store")
	v, err := m.store.GetAuthSettings(r.Context())
	if err != nil || v == nil {
		WriteError(w, 503, "authentication unavailable")
		return nil
	}
	return v
}

func (m *Settings) status(w http.ResponseWriter, r *http.Request) {
	v := m.load(w, r)
	if v == nil {
		return
	}
	// The collapse flag is reported only while local sign-in is enabled, so a
	// stale value cannot describe a form the browser is not allowed to show.
	status := map[string]any{"enabled": true, "setup_required": v.SetupRequired, "local_login": v.Settings.LocalLoginEnabled, "local_login_collapsed": v.Settings.LocalLoginEnabled && v.Settings.LocalLoginCollapsed, "display_title": v.Settings.DisplayTitle, "signup_admission": v.Settings.SignupAdmission, "remember_me": true, "passkey_login": "discoverable", "passkeys": false, "passkey_login_enabled": false}
	if !v.SetupRequired {
		a, _, err := m.snapshot(v.Settings)
		if err != nil {
			WriteError(w, 503, "authentication unavailable")
			return
		}
		// `passkeys` reports that the subsystem is usable at all, which is what
		// account management and step-up verification depend on;
		// `passkey_login_enabled` reports only whether it is accepted as a
		// first factor. Collapsing the two would hide the Account security
		// passkey list behind a switch that does not govern it.
		status["passkeys"] = a.passkey != nil && v.Settings.LocalLoginEnabled
		status["passkey_login_enabled"] = status["passkeys"] == true && !v.Settings.PasskeyLoginDisabled
		status["origin"] = v.Settings.Origin
		status["allowed_origins"] = v.Settings.AllowedOrigins
		if a.mobileStore != nil {
			status["mobile_auth"] = a.mobileDescriptor()
		}
	}
	httpx.JSON(w, status, 200)
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

func (m *Settings) setup(w http.ResponseWriter, r *http.Request) {
	v := m.load(w, r)
	if v == nil {
		return
	}
	if !v.SetupRequired {
		WriteError(w, 409, "installation setup already completed")
		return
	}
	if _, err := nativeSetupOrigin(r, v.Settings.Origin, ""); err != nil {
		WriteError(w, 403, err.Error())
		return
	}
	if !m.limit.Allow() {
		WriteError(w, 429, "setup rate limit exceeded")
		return
	}
	// Durable source bounds survive replicas/restarts; hash the resolved client
	// address, which is a forwarded one only behind a configured trusted proxy.
	admission, ok := m.backend.(service.AuthSecurityAdmissionStorer)
	if !ok {
		WriteError(w, 503, "authentication admission unavailable")
		return
	}
	allowed, err := admission.AdmitAuthSecuritySource(r.Context(), SessionHash(m.clientIP.LimitKey(r)))
	if err != nil {
		WriteError(w, 503, "authentication unavailable")
		return
	}
	if !allowed {
		WriteError(w, 429, "setup rate limit exceeded")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Origin   string `json:"origin"`
	}
	if !DecodeBody(w, r, &body) {
		return
	}
	origin, err := nativeSetupOrigin(r, v.Settings.Origin, body.Origin)
	if err != nil {
		WriteError(w, 403, err.Error())
		return
	}
	body.Username = normalizeNativeUsername(body.Username)
	if !validNativeUsername(body.Username) {
		WriteError(w, 400, "username must be 3-128 ASCII letters, digits, or ._@+-")
		return
	}
	select {
	case m.slots <- struct{}{}:
	default:
		WriteError(w, 429, "authentication busy; retry later")
		return
	}
	defer func() { <-m.slots }()
	hasher := nativePasswordHasher()
	hash, err := hasher.Hash(body.Password)
	if err != nil {
		WriteError(w, 400, nativePasswordMessage(body.Password))
		return
	}
	v.Settings.Origin = origin
	// Validate the complete coordinator before committing an installation claim.
	if _, _, err := m.snapshot(v.Settings); err != nil {
		WriteError(w, 503, "authentication configuration unavailable")
		return
	}
	user, err := m.store.SetupAuth(r.Context(), service.AuthUser{Username: body.Username, PasswordHash: hash}, v.Settings)
	if errors.Is(err, service.ErrAuthConflict) {
		WriteError(w, 409, "installation setup already completed or username unavailable")
		return
	}
	if err != nil {
		WriteError(w, 503, "authentication unavailable")
		return
	}
	httpx.JSON(w, Identity(user), 201)
}

// Require is the runtime replacement for a.Require on management groups. Parent
// authorization can compose this with workspace membership checks unchanged.
func (m *Settings) Require(admin bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return m.WithRuntime(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a := RuntimeFromRequest(r, nil)
			a.Require(admin)(next).ServeHTTP(w, r)
		}))
	}
}

// WithRuntime only loads policy and gates setup; it does NOT authenticate a user.
// Workspace authentication uses this before its own transport/session middleware,
// which intentionally permits mobile access beyond the native /auth/me route.
func (m *Settings) WithRuntime(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := m.load(w, r)
		if v == nil {
			return
		}
		if v.SetupRequired {
			WriteError(w, 403, "installation setup required")
			return
		}
		a, _, err := m.snapshot(v.Settings)
		if err != nil {
			WriteError(w, 503, "authentication unavailable")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), nativeRuntimeContextKey{}, a)))
	})
}

type authSettingsRequest struct {
	service.AuthSettings
	SessionTTL  *string `json:"session_ttl,omitempty"`
	RememberTTL *string `json:"remember_ttl,omitempty"`
}

func (v authSettingsRequest) settings() (service.AuthSettings, error) {
	settings := v.AuthSettings
	for _, field := range []struct {
		name    string
		text    *string
		seconds *int64
	}{{"session lifetime", v.SessionTTL, &settings.SessionTTLSeconds}, {"remembered lifetime", v.RememberTTL, &settings.RememberTTLSeconds}} {
		if field.text == nil {
			continue
		}
		duration, err := str2duration.ParseDuration(strings.TrimSpace(*field.text))
		if err != nil || duration <= 0 || duration%time.Second != 0 {
			return settings, fmt.Errorf("%s must be a positive whole-second duration, for example 8h or 4w1d2h", field.name)
		}
		seconds := int64(duration / time.Second)
		if *field.seconds != 0 && *field.seconds != seconds {
			return settings, fmt.Errorf("%s conflicts with its seconds value", field.name)
		}
		*field.seconds = seconds
	}
	return settings, settings.Validate(false)
}

func (m *Settings) settings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		v := m.load(w, r)
		if v != nil {
			httpx.JSON(w, v.Settings, 200)
		}
		return
	}
	var req authSettingsRequest
	if !DecodeBody(w, r, &req) {
		return
	}
	v, err := req.settings()
	if err != nil {
		WriteError(w, 400, err.Error())
		return
	}
	saved, err := m.store.SaveAuthSettings(r.Context(), v)
	if errors.Is(err, service.ErrAuthConflict) {
		WriteError(w, 409, "settings changed or no usable external administrator; reload settings and retry")
		return
	}
	if err != nil {
		WriteError(w, 503, "authentication unavailable")
		return
	}
	httpx.JSON(w, saved, 200)
}

func (m *Settings) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, m.cfg.BasePath)
	switch {
	case path == "/auth/status" && r.Method == http.MethodGet:
		m.status(w, r)
		return
	case path == "/auth/setup" && r.Method == http.MethodPost:
		m.setup(w, r)
		return
	case path == "/auth/settings" && (r.Method == http.MethodGet || r.Method == http.MethodPut):
		m.Require(true)(http.HandlerFunc(m.settings)).ServeHTTP(w, r)
		return
	}
	v := m.load(w, r)
	if v == nil {
		return
	}
	if v.SetupRequired {
		// Preserve the configured token bootstrap API, using its exact origin.
		if path != "/auth/bootstrap" || m.cfg.NativeAuth == nil || m.cfg.NativeAuth.BootstrapToken == "" || v.Settings.Origin == "" {
			WriteError(w, 403, "installation setup required")
			return
		}
	}
	if !v.Settings.LocalLoginEnabled && (path == "/auth/login" || strings.HasPrefix(path, "/auth/passkeys/login")) {
		WriteError(w, 403, "local login is disabled")
		return
	}
	// Only the sign-in ceremony. Enrolment, listing, deletion and passkey
	// reauthentication stay available, so an account can still manage its keys
	// (and step up with one) while the installation does not accept them as a
	// first factor — hiding the button alone would be a decorative switch.
	if v.Settings.PasskeyLoginDisabled && strings.HasPrefix(path, "/auth/passkeys/login") {
		WriteError(w, 403, "passkey sign-in is disabled")
		return
	}
	a, routes, err := m.snapshot(v.Settings)
	if err != nil {
		WriteError(w, 503, "authentication unavailable")
		return
	}
	routes.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), nativeRuntimeContextKey{}, a)))
}

func (m *Settings) Register(mux *ada.Server, base string) {
	mux.Handle(base+"/auth/*", m)
}
