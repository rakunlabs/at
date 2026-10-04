package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/rakunlabs/at/internal/service"
)

// webhookListenerManager runs the dedicated webhook listeners. Each enabled
// webhook server gets its own http.Server whose only routes are a health check
// and the webhooks bound to it — no UI, /api, /auth or /gateway — so opening
// the port exposes nothing else.
//
// State is per replica: every replica binds the same ports, as every replica
// binds the main port. A bind failure (port in use, permission) is recorded as
// the listener's status and never takes the process down.
type webhookListenerManager struct {
	s *Server

	mu        sync.Mutex
	listeners map[string]*webhookListener
	statuses  map[string]service.WebhookServerStatus
	closed    bool

	// reloadMu serializes Reload so two writes cannot interleave stop/start.
	reloadMu sync.Mutex
}

type webhookListener struct {
	cfg     service.WebhookServer
	key     string
	srv     *http.Server
	ln      net.Listener
	cidrs   []netip.Prefix
	limiter *webhookRateLimiter
	done    chan struct{}
}

func newWebhookListenerManager(s *Server) *webhookListenerManager {
	return &webhookListenerManager{s: s, listeners: map[string]*webhookListener{}, statuses: map[string]service.WebhookServerStatus{}}
}

// listenerKey changes whenever anything that affects the listener changes, so
// Reload restarts only the servers that were actually edited.
func webhookListenerKey(c service.WebhookServer) string {
	return strings.Join([]string{c.BindHost, strconv.Itoa(c.Port), c.BasePath, c.TLSCert, c.TLSKey,
		strings.Join(c.AllowedCIDRs, ","), strconv.FormatInt(c.MaxBodyBytes, 10), strconv.Itoa(c.RateLimitPerMinute), c.Name}, "\x00")
}

func webhookListenAddress(c service.WebhookServer) string {
	return net.JoinHostPort(c.BindHost, strconv.Itoa(c.Port))
}

// Reload brings the running listeners in line with the stored configuration
// and the webhook_triggers/webhook_servers features.
func (m *webhookListenerManager) Reload(ctx context.Context) {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()

	store, ok := m.s.store.(service.WebhookServerStorer)
	if !ok {
		return
	}
	enabled := true
	if m.s.featureStore != nil {
		on, err := m.s.isFeatureEnabled(ctx, service.FeatureWebhookServers)
		if err != nil {
			slog.Error("webhook servers: feature check failed", "error", err)
			return
		}
		enabled = on
	}
	var configs []service.WebhookServer
	if enabled {
		var err error
		configs, err = store.ListRuntimeWebhookServers(service.WithExecutionMaintenance(ctx))
		if err != nil {
			slog.Error("webhook servers: load configuration failed", "error", err)
			return
		}
	}

	want := map[string]service.WebhookServer{}
	for _, c := range configs {
		if c.Enabled {
			want[c.ID] = c
		}
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	var stop []*webhookListener
	for id, l := range m.listeners {
		if c, ok := want[id]; !ok || webhookListenerKey(c) != l.key {
			stop = append(stop, l)
			delete(m.listeners, id)
		}
	}
	m.statuses = map[string]service.WebhookServerStatus{}
	for _, c := range configs {
		if !c.Enabled || !enabled {
			m.statuses[c.ID] = service.WebhookServerStatus{State: "disabled"}
		}
	}
	m.mu.Unlock()

	// Stop before start: an edited server usually keeps its port.
	for _, l := range stop {
		l.shutdown()
	}

	for id, c := range want {
		m.mu.Lock()
		_, running := m.listeners[id]
		m.mu.Unlock()
		if running {
			m.setStatus(id, m.listenerStatus(id))
			continue
		}
		l, err := m.start(c)
		m.mu.Lock()
		if err != nil {
			m.statuses[id] = service.WebhookServerStatus{State: "error", Error: err.Error(), Address: webhookListenAddress(c), Since: time.Now().UTC().Format(time.RFC3339)}
			m.mu.Unlock()
			slog.Error("webhook server: listen failed", "server", c.Name, "address", webhookListenAddress(c), "error", err)
			continue
		}
		if m.closed {
			m.mu.Unlock()
			l.shutdown()
			continue
		}
		m.listeners[id] = l
		m.statuses[id] = service.WebhookServerStatus{State: "running", Address: l.ln.Addr().String(), Since: time.Now().UTC().Format(time.RFC3339)}
		m.mu.Unlock()
		slog.Info("webhook server: listening", "server", c.Name, "address", l.ln.Addr().String(), "tls", c.TLSEnabled())
	}
}

func (m *webhookListenerManager) listenerStatus(id string) service.WebhookServerStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.statuses[id]; ok && st.State != "" {
		return st
	}
	if l, ok := m.listeners[id]; ok {
		return service.WebhookServerStatus{State: "running", Address: l.ln.Addr().String()}
	}
	return service.WebhookServerStatus{State: "stopped"}
}

func (m *webhookListenerManager) setStatus(id string, st service.WebhookServerStatus) {
	m.mu.Lock()
	m.statuses[id] = st
	m.mu.Unlock()
}

// Status reports this replica's state for one server.
func (m *webhookListenerManager) Status(id string) service.WebhookServerStatus {
	if m == nil {
		return service.WebhookServerStatus{State: "stopped"}
	}
	return m.listenerStatus(id)
}

// Close stops every listener; used at shutdown.
func (m *webhookListenerManager) Close() {
	m.mu.Lock()
	m.closed = true
	ls := make([]*webhookListener, 0, len(m.listeners))
	for id, l := range m.listeners {
		ls = append(ls, l)
		delete(m.listeners, id)
	}
	m.mu.Unlock()
	for _, l := range ls {
		l.shutdown()
	}
}

func (m *webhookListenerManager) start(c service.WebhookServer) (*webhookListener, error) {
	cidrs, err := service.ParseWebhookCIDRs(c.AllowedCIDRs)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", webhookListenAddress(c))
	if err != nil {
		return nil, err
	}
	if c.TLSEnabled() {
		cert, err := tls.X509KeyPair([]byte(c.TLSCert), []byte(c.TLSKey))
		if err != nil {
			ln.Close()
			return nil, fmt.Errorf("load TLS certificate: %w", err)
		}
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	}
	l := &webhookListener{cfg: c, key: webhookListenerKey(c), ln: ln, cidrs: cidrs, done: make(chan struct{})}
	if c.RateLimitPerMinute > 0 {
		l.limiter = newWebhookRateLimiter(c.RateLimitPerMinute)
	}
	l.srv = &http.Server{
		Handler:           m.handler(l),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn),
	}
	go func() {
		defer close(l.done)
		if err := l.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("webhook server: serve failed", "server", c.Name, "error", err)
			m.setStatus(c.ID, service.WebhookServerStatus{State: "error", Error: err.Error(), Address: webhookListenAddress(c), Since: time.Now().UTC().Format(time.RFC3339)})
		}
	}()
	return l, nil
}

func (l *webhookListener) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := l.srv.Shutdown(ctx); err != nil {
		l.srv.Close()
	}
	<-l.done
}

// handler is the whole surface of a dedicated listener.
func (m *webhookListenerManager) handler(l *webhookListener) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				slog.Error("webhook server: handler panic", "server", l.cfg.Name, "panic", v)
				httpResponse(w, "internal error", http.StatusInternalServerError)
			}
		}()
		// The allowlist uses the same resolution as sign-in auditing: the
		// socket peer, or a forwarded address only when the peer is one of
		// server.trusted_proxies, so a caller cannot choose its own address.
		if len(l.cidrs) > 0 && !webhookPeerAllowed(m.s.clientIPs.Addr(r), l.cidrs) {
			httpResponse(w, "forbidden", http.StatusForbidden)
			return
		}
		path := r.URL.Path
		if l.cfg.BasePath != "" {
			if path != l.cfg.BasePath && !strings.HasPrefix(path, l.cfg.BasePath+"/") {
				httpResponse(w, "webhook not found", http.StatusNotFound)
				return
			}
			path = strings.TrimPrefix(path, l.cfg.BasePath)
		}
		path = strings.Trim(path, "/")
		if path == service.WebhookHealthPath && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			httpResponseJSON(w, map[string]string{"status": "ok", "server": l.cfg.Name}, http.StatusOK)
			return
		}
		if l.limiter != nil && !l.limiter.Allow(m.s.clientIPs.LimitKey(r)) {
			w.Header().Set("Retry-After", "60")
			httpResponse(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		enabled, err := m.s.isFeatureEnabled(r.Context(), service.FeatureWebhookTriggers)
		if err != nil {
			httpResponse(w, "feature check failed", http.StatusInternalServerError)
			return
		}
		if !enabled {
			httpResponse(w, "webhook not found", http.StatusNotFound)
			return
		}
		store, ok := m.s.store.(service.WebhookServerStorer)
		if !ok || path == "" {
			httpResponse(w, "webhook not found", http.StatusNotFound)
			return
		}
		match, err := store.ResolveWebhookRoute(r.Context(), l.cfg.ID, path)
		if err != nil {
			if errors.Is(err, service.ErrWebhookRouteConflict) {
				httpResponse(w, "webhook path is ambiguous on this server", http.StatusConflict)
				return
			}
			slog.Error("webhook server: resolve route failed", "server", l.cfg.Name, "path", path, "error", err)
			httpResponse(w, "internal error", http.StatusInternalServerError)
			return
		}
		if match == nil {
			httpResponse(w, "webhook not found", http.StatusNotFound)
			return
		}
		if !methodAllowed(match.Methods, r.Method) {
			w.Header().Set("Allow", strings.Join(match.Methods, ", "))
			httpResponse(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Header.Del("Cookie")
		m.s.serveWebhook(w, r, match, &l.cfg)
	})
}

func methodAllowed(methods []string, method string) bool {
	for _, m := range methods {
		if m == method {
			return true
		}
	}
	return false
}

func webhookPeerAllowed(addr netip.Addr, cidrs []netip.Prefix) bool {
	if !addr.IsValid() {
		return false
	}
	for _, p := range cidrs {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// webhookRateLimiter is a per-client token bucket with a bounded key set.
type webhookRateLimiter struct {
	mu      sync.Mutex
	perMin  int
	clients map[string]*webhookRateEntry
}

type webhookRateEntry struct {
	limiter *rate.Limiter
	seen    time.Time
}

const webhookRateLimiterMaxKeys = 10000

func newWebhookRateLimiter(perMin int) *webhookRateLimiter {
	return &webhookRateLimiter{perMin: perMin, clients: map[string]*webhookRateEntry{}}
}

func (l *webhookRateLimiter) Allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.clients[key]
	if !ok {
		if len(l.clients) >= webhookRateLimiterMaxKeys {
			for k, v := range l.clients {
				if now.Sub(v.seen) > 2*time.Minute {
					delete(l.clients, k)
				}
			}
			if len(l.clients) >= webhookRateLimiterMaxKeys {
				// Collapse new clients into one bucket rather than grow
				// without bound; the key space is caller-controlled.
				key = ""
				if e, ok = l.clients[key]; ok {
					e.seen = now
					return e.limiter.Allow()
				}
			}
		}
		e = &webhookRateEntry{limiter: rate.NewLimiter(rate.Limit(float64(l.perMin)/60), l.perMin)}
		l.clients[key] = e
	}
	e.seen = now
	return e.limiter.Allow()
}
