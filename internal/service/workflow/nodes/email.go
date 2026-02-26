package nodes

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/render"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
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
//	"host":     string — SMTP server hostname
//	"port":     float64 — SMTP server port (25, 465, 587)
//	"username": string — SMTP auth username
//	"password": string — SMTP auth password (encrypted at rest)
//	"from":     string — default sender address
//	"tls":      bool   — use implicit TLS (port 465); false = STARTTLS
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
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	TLS      bool   `json:"tls"`
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

	// Render all template fields.
	to, err := renderEmailTemplate("to", n.toTmpl, tmplCtx)
	if err != nil {
		return nil, err
	}
	cc, err := renderEmailTemplate("cc", n.ccTmpl, tmplCtx)
	if err != nil {
		return nil, err
	}
	bcc, err := renderEmailTemplate("bcc", n.bccTmpl, tmplCtx)
	if err != nil {
		return nil, err
	}
	subject, err := renderEmailTemplate("subject", n.subjectTmpl, tmplCtx)
	if err != nil {
		return nil, err
	}
	body, err := renderEmailTemplate("body", n.bodyTmpl, tmplCtx)
	if err != nil {
		return nil, err
	}

	// Resolve sender: node override > config default.
	from := sc.From
	if n.fromTmpl != "" {
		rendered, err := renderEmailTemplate("from", n.fromTmpl, tmplCtx)
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

	replyTo, err := renderEmailTemplate("reply_to", n.replyToTmpl, tmplCtx)
	if err != nil {
		return nil, err
	}

	// Collect all recipients for the SMTP envelope.
	toAddrs := splitAddresses(to)
	ccAddrs := splitAddresses(cc)
	bccAddrs := splitAddresses(bcc)

	allRecipients := make([]string, 0, len(toAddrs)+len(ccAddrs)+len(bccAddrs))
	allRecipients = append(allRecipients, toAddrs...)
	allRecipients = append(allRecipients, ccAddrs...)
	allRecipients = append(allRecipients, bccAddrs...)

	if len(allRecipients) == 0 {
		return nil, fmt.Errorf("email: no recipients specified")
	}

	// Build RFC 2822 message.
	var msg strings.Builder
	msg.WriteString("From: " + from + "\r\n")
	msg.WriteString("To: " + to + "\r\n")
	if cc != "" {
		msg.WriteString("Cc: " + cc + "\r\n")
	}
	if replyTo != "" {
		msg.WriteString("Reply-To: " + replyTo + "\r\n")
	}
	msg.WriteString("Subject: " + subject + "\r\n")
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: " + n.contentType + "; charset=UTF-8\r\n")
	msg.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(body)

	// Send email.
	addr := net.JoinHostPort(sc.Host, fmt.Sprintf("%d", sc.Port))

	sendErr := sendEmail(ctx, addr, sc, from, allRecipients, []byte(msg.String()))

	outData := map[string]any{
		"to":      to,
		"cc":      cc,
		"bcc":     bcc,
		"subject": subject,
		"from":    from,
	}

	// Selection-based routing: Port 0 = error, Port 1 = success, Port 2 = always
	selection := []int{2} // always
	if sendErr != nil {
		outData["error"] = sendErr.Error()
		selection = append(selection, 0) // error
	} else {
		outData["status"] = "sent"
		selection = append(selection, 1) // success
	}

	return workflow.NewSelectionResult(outData, selection), nil
}

// sendEmail handles the SMTP connection, TLS negotiation, authentication, and
// message transmission. It supports both implicit TLS (port 465) and STARTTLS.
func sendEmail(_ context.Context, addr string, sc smtpConfig, from string, recipients []string, msg []byte) error {
	tlsConfig := &tls.Config{
		ServerName: sc.Host,
	}

	var c *smtp.Client

	if sc.TLS {
		// Implicit TLS (typically port 465).
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", addr, tlsConfig)
		if err != nil {
			return fmt.Errorf("email: TLS dial: %w", err)
		}
		c, err = smtp.NewClient(conn, sc.Host)
		if err != nil {
			conn.Close()
			return fmt.Errorf("email: create SMTP client: %w", err)
		}
	} else {
		// Plain connection, optionally upgrading via STARTTLS.
		conn, err := net.DialTimeout("tcp", addr, 15*time.Second)
		if err != nil {
			return fmt.Errorf("email: dial: %w", err)
		}
		c, err = smtp.NewClient(conn, sc.Host)
		if err != nil {
			conn.Close()
			return fmt.Errorf("email: create SMTP client: %w", err)
		}

		// Attempt STARTTLS if the server supports it.
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsConfig); err != nil {
				c.Close()
				return fmt.Errorf("email: STARTTLS: %w", err)
			}
		}
	}
	defer c.Close()

	// Authenticate if credentials are provided.
	if sc.Username != "" || sc.Password != "" {
		auth := smtp.PlainAuth("", sc.Username, sc.Password, sc.Host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("email: auth: %w", err)
		}
	}

	// Set sender.
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("email: MAIL FROM: %w", err)
	}

	// Set recipients.
	for _, rcpt := range recipients {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("email: RCPT TO %q: %w", rcpt, err)
		}
	}

	// Send message body.
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("email: DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("email: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("email: close body: %w", err)
	}

	return c.Quit()
}

// renderEmailTemplate renders a Go text/template string, returning empty string for empty templates.
func renderEmailTemplate(name, tmplText string, ctx map[string]any) (string, error) {
	if tmplText == "" {
		return "", nil
	}
	result, err := render.ExecuteWithData(tmplText, ctx)
	if err != nil {
		return "", fmt.Errorf("email: template %q: %w", name, err)
	}
	return string(result), nil
}

// splitAddresses splits a comma-separated list of email addresses,
// trimming whitespace and filtering out empty entries.
func splitAddresses(s string) []string {
	if s == "" {
		return nil
	}
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
