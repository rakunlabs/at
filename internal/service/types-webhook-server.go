package service

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// WebhookSecretSentinel is returned in place of write-only values (TLS private
// key, signing secret). Submitting it back preserves the stored value.
const WebhookSecretSentinel = "***"

// Webhook server limits. A dedicated listener exposes only webhook routes, but
// it is still a network surface, so its bounds are explicit.
const (
	WebhookServerMinPort         = 1024
	WebhookDefaultMaxBodyBytes   = 10 << 20
	WebhookMaxBodyBytesCeiling   = 256 << 20
	WebhookMaxCIDRs              = 64
	WebhookMaxRoutesPerTrigger   = 16
	WebhookRoutePathMaxLen       = 200
	WebhookDeliveryRetention     = 100
	WebhookHealthPath            = "healthz"
	WebhookSignatureMaxSecretLen = 4096
)

var (
	ErrWebhookServerConflict = errors.New("webhook server conflict")
	ErrWebhookRouteConflict  = errors.New("webhook path is already used on that server")
)

// WebhookServer is a dedicated HTTP listener (for example :5050) whose only
// routes are the webhooks bound to it. Managed by installation administrators;
// AllWorkspaces/WorkspaceIDs decide whose webhooks may be published on it.
type WebhookServer struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	BindHost      string   `json:"bind_host"`
	Port          int      `json:"port"`
	BasePath      string   `json:"base_path"`
	Enabled       bool     `json:"enabled"`
	AllWorkspaces bool     `json:"all_workspaces"`
	WorkspaceIDs  []string `json:"workspace_ids"`

	// Encrypted settings.
	TLSCert            string   `json:"tls_cert"`
	TLSKey             string   `json:"tls_key"`
	AllowedCIDRs       []string `json:"allowed_cidrs"`
	MaxBodyBytes       int64    `json:"max_body_bytes"`
	RateLimitPerMinute int      `json:"rate_limit_per_minute"`
	// PublicURL is how callers reach this listener (for example behind a load
	// balancer); used only to display copyable webhook URLs.
	PublicURL string `json:"public_url"`

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	CreatedBy string `json:"created_by"`
	UpdatedBy string `json:"updated_by"`

	// Status is this replica's listener state; never persisted.
	Status *WebhookServerStatus `json:"status,omitempty"`
}

// WebhookServerSettings is the encrypted part of a webhook server row.
type WebhookServerSettings struct {
	TLSCert            string   `json:"tls_cert,omitempty"`
	TLSKey             string   `json:"tls_key,omitempty"`
	AllowedCIDRs       []string `json:"allowed_cidrs,omitempty"`
	MaxBodyBytes       int64    `json:"max_body_bytes,omitempty"`
	RateLimitPerMinute int      `json:"rate_limit_per_minute,omitempty"`
	PublicURL          string   `json:"public_url,omitempty"`
}

func (s WebhookServer) Settings() WebhookServerSettings {
	return WebhookServerSettings{TLSCert: s.TLSCert, TLSKey: s.TLSKey, AllowedCIDRs: s.AllowedCIDRs, MaxBodyBytes: s.MaxBodyBytes, RateLimitPerMinute: s.RateLimitPerMinute, PublicURL: s.PublicURL}
}

func (s *WebhookServer) ApplySettings(c WebhookServerSettings) {
	s.TLSCert, s.TLSKey, s.AllowedCIDRs, s.MaxBodyBytes, s.RateLimitPerMinute, s.PublicURL = c.TLSCert, c.TLSKey, c.AllowedCIDRs, c.MaxBodyBytes, c.RateLimitPerMinute, c.PublicURL
}

// Redacted hides the TLS private key for management reads.
func (s WebhookServer) Redacted() WebhookServer {
	if s.TLSKey != "" {
		s.TLSKey = WebhookSecretSentinel
	}
	return s
}

// TLSEnabled reports whether the listener serves HTTPS.
func (s WebhookServer) TLSEnabled() bool { return s.TLSCert != "" && s.TLSKey != "" }

// EffectiveMaxBodyBytes applies the default request body bound.
func (s WebhookServer) EffectiveMaxBodyBytes() int64 {
	if s.MaxBodyBytes <= 0 {
		return WebhookDefaultMaxBodyBytes
	}
	return s.MaxBodyBytes
}

// AdmitsWorkspace reports whether a workspace may publish webhooks here.
func (s WebhookServer) AdmitsWorkspace(id string) bool {
	return s.AllWorkspaces || slices.Contains(s.WorkspaceIDs, id)
}

// WebhookServerStatus is the runtime state of one listener on one replica.
type WebhookServerStatus struct {
	State   string `json:"state"` // running | stopped | error | disabled
	Error   string `json:"error,omitempty"`
	Address string `json:"address,omitempty"`
	Since   string `json:"since,omitempty"`
}

var webhookHostPattern = regexp.MustCompile(`^[A-Za-z0-9.\-:\[\]]*$`)

// Normalize canonicalizes user input before validation.
func (s *WebhookServer) Normalize() {
	s.Name = strings.TrimSpace(s.Name)
	s.Description = strings.TrimSpace(s.Description)
	s.BindHost = strings.Trim(strings.TrimSpace(s.BindHost), "[]")
	s.BasePath = strings.TrimSpace(s.BasePath)
	if s.BasePath != "" {
		s.BasePath = "/" + strings.Trim(s.BasePath, "/")
		if s.BasePath == "/" {
			s.BasePath = ""
		}
	}
	s.PublicURL = strings.TrimRight(strings.TrimSpace(s.PublicURL), "/")
	s.TLSCert = strings.TrimSpace(s.TLSCert)
	s.TLSKey = strings.TrimSpace(s.TLSKey)
	var cidrs []string
	for _, c := range s.AllowedCIDRs {
		if c = strings.TrimSpace(c); c != "" && !slices.Contains(cidrs, c) {
			cidrs = append(cidrs, c)
		}
	}
	s.AllowedCIDRs = cidrs
	var ws []string
	for _, w := range s.WorkspaceIDs {
		if w = strings.TrimSpace(w); w != "" && !slices.Contains(ws, w) {
			ws = append(ws, w)
		}
	}
	s.WorkspaceIDs = ws
}

// Validate checks a normalized server. The TLS key may still be the sentinel
// here; the store resolves it before the key pair is checked.
func (s WebhookServer) Validate() error {
	if s.Name == "" || len(s.Name) > 100 {
		return fmt.Errorf("name is required (at most 100 characters)")
	}
	if len(s.Description) > 1000 {
		return fmt.Errorf("description is at most 1000 characters")
	}
	if len(s.BindHost) > 255 || !webhookHostPattern.MatchString(s.BindHost) {
		return fmt.Errorf("bind host must be an IP address or host name")
	}
	if s.Port < WebhookServerMinPort || s.Port > 65535 {
		return fmt.Errorf("port must be between %d and 65535", WebhookServerMinPort)
	}
	if len(s.BasePath) > 100 || (s.BasePath != "" && !validWebhookPath(strings.TrimPrefix(s.BasePath, "/"))) {
		return fmt.Errorf("base path may contain only letters, digits, '.', '_', '~', '-' and '/' segments")
	}
	if len(s.AllowedCIDRs) > WebhookMaxCIDRs {
		return fmt.Errorf("at most %d allowed address ranges", WebhookMaxCIDRs)
	}
	if _, err := ParseWebhookCIDRs(s.AllowedCIDRs); err != nil {
		return err
	}
	if s.MaxBodyBytes < 0 || s.MaxBodyBytes > WebhookMaxBodyBytesCeiling {
		return fmt.Errorf("max body bytes must be between 0 (default) and %d", WebhookMaxBodyBytesCeiling)
	}
	if s.RateLimitPerMinute < 0 || s.RateLimitPerMinute > 100000 {
		return fmt.Errorf("rate limit must be between 0 (off) and 100000 requests per minute")
	}
	if (s.TLSCert == "") != (s.TLSKey == "") {
		return fmt.Errorf("TLS needs both a certificate and a private key")
	}
	if s.PublicURL != "" {
		u, err := url.Parse(s.PublicURL)
		if err != nil || len(s.PublicURL) > 2048 || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("public URL must be an http:// or https:// URL without credentials, query or fragment")
		}
	}
	if s.AllWorkspaces && len(s.WorkspaceIDs) > 0 {
		return fmt.Errorf("choose either all workspaces or a workspace list")
	}
	return nil
}

// ValidateTLS checks that the certificate and key form a usable pair.
func (s WebhookServer) ValidateTLS() error {
	if !s.TLSEnabled() {
		return nil
	}
	if _, err := tls.X509KeyPair([]byte(s.TLSCert), []byte(s.TLSKey)); err != nil {
		return fmt.Errorf("invalid TLS certificate/key pair: %w", err)
	}
	return nil
}

// ParseWebhookCIDRs accepts CIDR blocks and single addresses.
func ParseWebhookCIDRs(values []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(values))
	for _, v := range values {
		if strings.Contains(v, "/") {
			p, err := netip.ParsePrefix(v)
			if err != nil {
				return nil, fmt.Errorf("invalid address range %q", v)
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(v)
		if err != nil {
			return nil, fmt.Errorf("invalid address %q", v)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// ─── Trigger routes ───

// WebhookRoute publishes a trigger on a dedicated server. An empty path makes
// it reachable by the trigger's alias or ID; a custom path replaces both.
type WebhookRoute struct {
	ServerID string `json:"server_id"`
	Path     string `json:"path"`
}

var webhookSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9._~\-]+$`)

func validWebhookPath(p string) bool {
	if p == "" {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." || !webhookSegmentPattern.MatchString(seg) {
			return false
		}
	}
	return true
}

// NormalizeWebhookPath trims slashes; "" stays "" (default ID/alias routing).
func NormalizeWebhookPath(p string) (string, error) {
	p = strings.Trim(strings.TrimSpace(p), "/")
	if p == "" {
		return "", nil
	}
	if len(p) > WebhookRoutePathMaxLen || !validWebhookPath(p) {
		return "", fmt.Errorf("webhook path %q may contain only letters, digits, '.', '_', '~', '-' and '/' segments", p)
	}
	if p == WebhookHealthPath {
		return "", fmt.Errorf("webhook path %q is reserved for the health check", p)
	}
	return p, nil
}

// NormalizeWebhookRoutes validates a trigger's server bindings.
func NormalizeWebhookRoutes(routes []WebhookRoute) ([]WebhookRoute, error) {
	if len(routes) > WebhookMaxRoutesPerTrigger {
		return nil, fmt.Errorf("a webhook can be published on at most %d servers", WebhookMaxRoutesPerTrigger)
	}
	out := make([]WebhookRoute, 0, len(routes))
	seen := map[string]bool{}
	for _, r := range routes {
		r.ServerID = strings.TrimSpace(r.ServerID)
		if r.ServerID == "" {
			return nil, fmt.Errorf("webhook server is required")
		}
		if seen[r.ServerID] {
			return nil, fmt.Errorf("webhook server %q is listed twice", r.ServerID)
		}
		seen[r.ServerID] = true
		p, err := NormalizeWebhookPath(r.Path)
		if err != nil {
			return nil, err
		}
		r.Path = p
		out = append(out, r)
	}
	return out, nil
}

// ─── Signature verification ───

// Signature schemes.
const (
	WebhookSignatureGitHub = "github"      // X-Hub-Signature-256: sha256=<hex>
	WebhookSignatureStripe = "stripe"      // Stripe-Signature: t=..,v1=..
	WebhookSignatureHMAC   = "hmac_sha256" // configurable header/prefix/encoding
)

// WebhookSignature is a trigger's HMAC verification setting. The whole value is
// stored encrypted; Secret is write-only in the management API.
type WebhookSignature struct {
	Scheme   string `json:"scheme"`
	Secret   string `json:"secret,omitempty"`
	Header   string `json:"header,omitempty"`
	Prefix   string `json:"prefix,omitempty"`
	Encoding string `json:"encoding,omitempty"` // hex (default) | base64
}

// Normalize fills scheme defaults.
func (s *WebhookSignature) Normalize() {
	s.Scheme = strings.TrimSpace(strings.ToLower(s.Scheme))
	s.Header = strings.TrimSpace(s.Header)
	s.Prefix = strings.TrimSpace(s.Prefix)
	s.Encoding = strings.TrimSpace(strings.ToLower(s.Encoding))
	switch s.Scheme {
	case WebhookSignatureGitHub:
		s.Header, s.Prefix, s.Encoding = "X-Hub-Signature-256", "sha256=", "hex"
	case WebhookSignatureStripe:
		s.Header, s.Prefix, s.Encoding = "Stripe-Signature", "", "hex"
	case WebhookSignatureHMAC:
		if s.Header == "" {
			s.Header = "X-Signature"
		}
		if s.Encoding == "" {
			s.Encoding = "hex"
		}
	}
}

func (s WebhookSignature) Validate() error {
	switch s.Scheme {
	case "":
		return nil
	case WebhookSignatureGitHub, WebhookSignatureStripe, WebhookSignatureHMAC:
	default:
		return fmt.Errorf("signature scheme must be github, stripe or hmac_sha256")
	}
	if s.Secret == "" || len(s.Secret) > WebhookSignatureMaxSecretLen {
		return fmt.Errorf("signature secret is required (at most %d characters)", WebhookSignatureMaxSecretLen)
	}
	if s.Encoding != "hex" && s.Encoding != "base64" {
		return fmt.Errorf("signature encoding must be hex or base64")
	}
	if len(s.Header) > 100 || len(s.Prefix) > 64 || strings.ContainsAny(s.Header, " :\r\n") {
		return fmt.Errorf("invalid signature header")
	}
	return nil
}

// Redacted hides the signing secret.
func (s *WebhookSignature) Redacted() *WebhookSignature {
	if s == nil || s.Scheme == "" {
		return nil
	}
	c := *s
	c.Secret = WebhookSecretSentinel
	return &c
}

// WebhookRouteMatch is routing metadata resolved before any authentication.
// It is internal to the server and never serialized to a client.
type WebhookRouteMatch struct {
	TriggerID    string
	WorkspaceID  string
	WorkflowID   string
	Alias        string
	Type         string
	Public       bool
	Enabled      bool
	HideFromMain bool
	Methods      []string
	Signature    *WebhookSignature
}

// WebhookDelivery is one received request, kept for troubleshooting.
type WebhookDelivery struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	TriggerID   string `json:"trigger_id"`
	WorkflowID  string `json:"workflow_id"`
	ServerID    string `json:"server_id"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	Status      int    `json:"status"`
	RunID       string `json:"run_id"`
	Error       string `json:"error"`
	ClientIP    string `json:"client_ip"`
	BodyBytes   int64  `json:"body_bytes"`
	DurationMS  int64  `json:"duration_ms"`
	CreatedAt   string `json:"created_at"`
}

// WebhookServerStorer manages dedicated webhook listeners and their routing.
type WebhookServerStorer interface {
	// Management (installation administrators).
	ListWebhookServers(ctx context.Context) ([]WebhookServer, error)
	GetWebhookServer(ctx context.Context, id string) (*WebhookServer, error)
	CreateWebhookServer(ctx context.Context, s WebhookServer) (*WebhookServer, error)
	UpdateWebhookServer(ctx context.Context, id string, s WebhookServer) (*WebhookServer, error)
	DeleteWebhookServer(ctx context.Context, id string) error

	// Runtime: listener configuration (decrypted) under maintenance authority.
	ListRuntimeWebhookServers(ctx context.Context) ([]WebhookServer, error)
	// Machine routing metadata; never authenticates the caller.
	ResolveWebhookRoute(ctx context.Context, serverID, path string) (*WebhookRouteMatch, error)
	ResolveMainWebhookRoute(ctx context.Context, idOrAlias string) (*WebhookRouteMatch, error)

	RecordWebhookDelivery(ctx context.Context, d WebhookDelivery) error
	ListWebhookDeliveries(ctx context.Context, triggerID string, limit int) ([]WebhookDelivery, error)
}
