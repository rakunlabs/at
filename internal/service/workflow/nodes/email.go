package nodes

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/render"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
	"github.com/wneessen/go-mail"
)

// emailNode sends an email via SMTP using a referenced NodeConfig for server
// settings. All string config fields (to, cc, bcc, subject, body, from,
// reply_to) support Go text/template syntax.
//
// Config (node.Data):
//
//	"config_id":     string — ID of the NodeConfig (type "email") with SMTP settings (required)
//	"to":            string — comma-separated recipient list template (required)
//	"cc":            string — comma-separated CC list template (optional)
//	"bcc":           string — comma-separated BCC list template (optional)
//	"subject":       string — subject line template (required)
//	"body":          string — email body template (required)
//	"content_type":  string — "text/plain" or "text/html" (default "text/plain")
//	"from":          string — sender address override template (optional; defaults to config value)
//	"reply_to":      string — Reply-To header template (optional)
//
// NodeConfig Data (type "email"):
//
//	"host":                 string — SMTP server hostname
//	"port":                 float64 — SMTP server port (25, 465, 587)
//	"username":             string — SMTP auth username
//	"password":             string — SMTP auth password (encrypted at rest)
//	"from":                 string — default sender address
//	"tls":                  bool   — use implicit TLS (port 465); false = STARTTLS
//	"no_tls":               bool   — disable TLS entirely (plain SMTP, default false)
//	"insecure_skip_verify": bool   — skip TLS verification (default false)
//	"proxy":                string — HTTP Connect Proxy URL (optional, e.g. http://user:pass@proxy:8080)
//
// Input ports:
//
//	"data"   — upstream data; also available as template context
//	"values" — additional template variables (merged on top of data)
//
// Output ports (selection-based):
//
//	index 0 = "error"   — activated on send failure
//	index 1 = "success" — activated on successful send
//	index 2 = "always"  — always activated
type emailNode struct {
	configID    string
	toTmpl      string
	ccTmpl      string
	bccTmpl     string
	subjectTmpl string
	bodyTmpl    string
	contentType string
	fromTmpl    string
	replyToTmpl string
}

func init() {
	workflow.RegisterNodeType("email", newEmailNode)
}

func newEmailNode(node service.WorkflowNode) (workflow.Noder, error) {
	configID, _ := node.Data["config_id"].(string)
	to, _ := node.Data["to"].(string)
	cc, _ := node.Data["cc"].(string)
	bcc, _ := node.Data["bcc"].(string)
	subject, _ := node.Data["subject"].(string)
	body, _ := node.Data["body"].(string)
	contentType, _ := node.Data["content_type"].(string)
	if contentType == "" {
		contentType = "text/plain"
	}
	from, _ := node.Data["from"].(string)
	replyTo, _ := node.Data["reply_to"].(string)

	return &emailNode{
		configID:    configID,
		toTmpl:      to,
		ccTmpl:      cc,
		bccTmpl:     bcc,
		subjectTmpl: subject,
		bodyTmpl:    body,
		contentType: contentType,
		fromTmpl:    from,
		replyToTmpl: replyTo,
	}, nil
}

func (n *emailNode) Type() string { return "email" }

func (n *emailNode) Validate(_ context.Context, reg *workflow.Registry) error {
	if n.configID == "" {
		return fmt.Errorf("email: 'config_id' is required")
	}
	if n.toTmpl == "" {
		return fmt.Errorf("email: 'to' is required")
	}
	if n.subjectTmpl == "" {
		return fmt.Errorf("email: 'subject' is required")
	}
	if n.bodyTmpl == "" {
		return fmt.Errorf("email: 'body' is required")
	}
	if reg.NodeConfigLookup == nil {
		return fmt.Errorf("email: node config lookup not available")
	}
	return nil
}

// smtpConfig holds parsed SMTP settings from the NodeConfig Data blob.
type smtpConfig struct {
	Host               string `json:"host"`
	Port               int    `json:"port"`
	Username           string `json:"username"`
	Password           string `json:"password"`
	From               string `json:"from"`
	TLS                bool   `json:"tls"`
	NoTLS              bool   `json:"no_tls"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify"`
	Proxy              string `json:"proxy"`
}

func (n *emailNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	// Look up SMTP configuration.
	cfg, err := reg.NodeConfigLookup(n.configID)
	if err != nil {
		return nil, fmt.Errorf("email: lookup config %q: %w", n.configID, err)
	}
	if cfg == nil {
		return nil, fmt.Errorf("email: config %q not found", n.configID)
	}
	if cfg.Type != "email" {
		return nil, fmt.Errorf("email: config %q has type %q, expected \"email\"", n.configID, cfg.Type)
	}

	var sc smtpConfig
	if err := json.Unmarshal([]byte(cfg.Data), &sc); err != nil {
		return nil, fmt.Errorf("email: parse config data: %w", err)
	}

	if sc.Host == "" {
		return nil, fmt.Errorf("email: config %q missing 'host'", n.configID)
	}
	if sc.Port == 0 {
		sc.Port = 587
	}

	// Build template context.
	tmplCtx := buildTemplateContext(inputs)
	extraFuncs := varFuncMap(reg)

	// Render all template fields.
	to, err := renderEmailTemplate("to", n.toTmpl, tmplCtx, extraFuncs)
	if err != nil {
		return nil, err
	}
	cc, err := renderEmailTemplate("cc", n.ccTmpl, tmplCtx, extraFuncs)
	if err != nil {
		return nil, err
	}
	bcc, err := renderEmailTemplate("bcc", n.bccTmpl, tmplCtx, extraFuncs)
	if err != nil {
		return nil, err
	}
	subject, err := renderEmailTemplate("subject", n.subjectTmpl, tmplCtx, extraFuncs)
	if err != nil {
		return nil, err
	}
	body, err := renderEmailTemplate("body", n.bodyTmpl, tmplCtx, extraFuncs)
	if err != nil {
		return nil, err
	}

	// Resolve sender: node override > config default.
	from := sc.From
	if n.fromTmpl != "" {
		rendered, err := renderEmailTemplate("from", n.fromTmpl, tmplCtx, extraFuncs)
		if err != nil {
			return nil, err
		}
		if rendered != "" {
			from = rendered
		}
	}
	if from == "" {
		return nil, fmt.Errorf("email: no 'from' address configured")
	}

	replyTo, err := renderEmailTemplate("reply_to", n.replyToTmpl, tmplCtx, extraFuncs)
	if err != nil {
		return nil, err
	}

	// Create the message
	m := mail.NewMsg()
	if err := m.From(from); err != nil {
		return nil, fmt.Errorf("email: set from: %w", err)
	}
	if err := m.To(splitAddresses(to)...); err != nil {
		return nil, fmt.Errorf("email: set to: %w", err)
	}
	if ccAddresses := splitAddresses(cc); len(ccAddresses) > 0 {
		if err := m.Cc(ccAddresses...); err != nil {
			return nil, fmt.Errorf("email: set cc: %w", err)
		}
	}
	if bccAddresses := splitAddresses(bcc); len(bccAddresses) > 0 {
		if err := m.Bcc(bccAddresses...); err != nil {
			return nil, fmt.Errorf("email: set bcc: %w", err)
		}
	}
	m.Subject(subject)
	m.SetBodyString(mail.ContentType(n.contentType), body)

	if replyTo != "" {
		if err := m.ReplyTo(replyTo); err != nil {
			return nil, fmt.Errorf("email: set reply-to: %w", err)
		}
	}

	// Configure the client
	opts := []mail.Option{
		mail.WithPort(sc.Port),
		mail.WithTimeout(30 * time.Second),
	}

	if sc.Username != "" || sc.Password != "" {
		opts = append(opts, mail.WithSMTPAuth(mail.SMTPAuthPlain), mail.WithUsername(sc.Username), mail.WithPassword(sc.Password))
	}

	// TLS Configuration
	if sc.NoTLS {
		// No TLS at all (plain SMTP)
		opts = append(opts, mail.WithTLSPolicy(mail.NoTLS))
	} else {
		tlsConfig := &tls.Config{
			ServerName:         sc.Host,
			InsecureSkipVerify: sc.InsecureSkipVerify,
		}
		opts = append(opts, mail.WithTLSConfig(tlsConfig))

		if sc.TLS {
			// Implicit TLS (usually port 465)
			opts = append(opts, mail.WithSSL(), mail.WithTLSPolicy(mail.TLSMandatory))
		} else {
			// STARTTLS (usually port 587) or plain
			opts = append(opts, mail.WithTLSPolicy(mail.TLSOpportunistic))
		}
	}

	// Proxy Configuration
	if sc.Proxy != "" {
		proxyURL, err := url.Parse(sc.Proxy)
		if err != nil {
			return nil, fmt.Errorf("email: parse proxy url: %w", err)
		}

		dialFunc := func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialViaProxy(ctx, proxyURL, addr)
		}
		opts = append(opts, mail.WithDialContextFunc(dialFunc))
	}

	c, err := mail.NewClient(sc.Host, opts...)
	if err != nil {
		return nil, fmt.Errorf("email: create client: %w", err)
	}

	sendErr := c.DialAndSend(m)

	outData := map[string]any{
		"status": "sent",
	}

	// Selection-based routing by port name.
	selection := []string{"always"}
	if sendErr != nil {
		outData["error"] = sendErr.Error()
		outData["status"] = "failed"
		selection = append(selection, "error")
	} else {
		selection = append(selection, "success")
	}

	return workflow.NewSelectionResult(outData, selection), nil
}

// renderEmailTemplate renders a Go text/template string, returning empty string for empty templates.
func renderEmailTemplate(name, tmplText string, ctx map[string]any, funcs map[string]any) (string, error) {
	if tmplText == "" {
		return "", nil
	}
	result, err := render.ExecuteWithFuncs(tmplText, ctx, funcs)
	if err != nil {
		return "", fmt.Errorf("email: template %q: %w", name, err)
	}
	return string(result), nil
}

// splitAddresses splits a list of email addresses by comma or semicolon,
// trimming whitespace and stripping brackets.
func splitAddresses(s string) []string {
	if s == "" {
		return nil
	}
	// Normalize separators: replace semicolon with comma
	s = strings.ReplaceAll(s, ";", ",")
	// Remove brackets if present (e.g. ["a@b.com", "c@d.com"] -> "a@b.com", "c@d.com")
	s = strings.ReplaceAll(s, "[", "")
	s = strings.ReplaceAll(s, "]", "")
	s = strings.ReplaceAll(s, "\"", "") // Remove quotes commonly found in JSON arrays

	parts := strings.Split(s, ",")
	addrs := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			addrs = append(addrs, p)
		}
	}
	return addrs
}

// dialViaProxy establishes a connection to targetAddr via the HTTP proxy at proxyURL.
func dialViaProxy(ctx context.Context, proxyURL *url.URL, targetAddr string) (net.Conn, error) {
	proxyAddr := proxyURL.Host
	if !strings.Contains(proxyAddr, ":") {
		proxyAddr = net.JoinHostPort(proxyAddr, "8080")
	}

	d := net.Dialer{Timeout: 30 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("dial proxy: %w", err)
	}

	// "CONNECT target:port HTTP/1.1\r\nHost: target:port\r\n\r\n"
	connectReq := &http.Request{
		Method: "CONNECT",
		URL:    &url.URL{Opaque: targetAddr},
		Host:   targetAddr,
		Header: make(http.Header),
	}

	if user := proxyURL.User; user != nil {
		password, _ := user.Password()
		auth := user.Username() + ":" + password
		basicAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
		connectReq.Header.Set("Proxy-Authorization", basicAuth)
	}

	if err := connectReq.Write(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write connect req: %w", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, connectReq)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read connect resp: %w", err)
	}
	if resp.StatusCode != 200 {
		conn.Close()
		return nil, fmt.Errorf("proxy connect failed: %s", resp.Status)
	}

	// If the buffer has data, we need to return a wrapped connection that yields that data first.
	if br.Buffered() > 0 {
		return &bufferedConn{Conn: conn, r: br}, nil
	}

	return conn, nil
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (bc *bufferedConn) Read(b []byte) (int, error) {
	return bc.r.Read(b)
}
