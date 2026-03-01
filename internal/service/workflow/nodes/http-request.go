package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rytsh/mugo/templatex"

	"github.com/rakunlabs/at/internal/render"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
	"github.com/worldline-go/klient"
)

// httpRequestNode makes an HTTP request and returns the response.
// All string config fields (url, method, header values, body) support
// Go text/template syntax. Template data is built from the "values" input
// port merged on top of the "data" input port, enabling dynamic URLs,
// methods, headers, and bodies.
//
// Config (node.Data):
//
//	"url":                  string — request URL template (required)
//	"method":               string — HTTP method template (default "GET")
//	"headers":              map[string]any — request header templates (optional)
//	"body":                 string — request body template (optional; used for POST/PUT/PATCH)
//	"timeout":              float64 — timeout in seconds (default 30)
//	"proxy":                string — HTTP proxy URL (optional)
//	"insecure_skip_verify": bool   — skip TLS verification (default false)
//	"retry":                bool   — enable automatic retry (default false)
//
// Input ports:
//
//	"data"   — upstream data; also available as template context
//	"values" — additional template variables (merged on top of data)
//
// Output ports (selection-based):
//
//	index 0 = "error"   — activated when status >= 400
//	index 1 = "success" — activated when status 2xx
//	index 2 = "always"  — always activated
//
// Output data includes "response", "status_code", and "headers".
type httpRequestNode struct {
	urlTmpl            string
	methodTmpl         string
	headerTmpls        map[string]string
	bodyTmpl           string
	timeout            time.Duration
	proxy              string
	insecureSkipVerify bool
	retry              bool
}

func init() {
	workflow.RegisterNodeType("http_request", newHTTPRequestNode)
}

func newHTTPRequestNode(node service.WorkflowNode) (workflow.Noder, error) {
	urlStr, _ := node.Data["url"].(string)
	method, _ := node.Data["method"].(string)
	if method == "" {
		method = "GET"
	}

	timeout := 30.0
	if t, ok := node.Data["timeout"].(float64); ok && t > 0 {
		timeout = t
	}

	headers := make(map[string]string)
	if h, ok := node.Data["headers"].(map[string]any); ok {
		for k, v := range h {
			if s, ok := v.(string); ok {
				headers[k] = s
			}
		}
	}

	bodyTmpl, _ := node.Data["body"].(string)
	proxy, _ := node.Data["proxy"].(string)
	insecure, _ := node.Data["insecure_skip_verify"].(bool)
	retry, _ := node.Data["retry"].(bool)

	return &httpRequestNode{
		urlTmpl:            urlStr,
		methodTmpl:         strings.ToUpper(method),
		headerTmpls:        headers,
		bodyTmpl:           bodyTmpl,
		timeout:            time.Duration(timeout * float64(time.Second)),
		proxy:              proxy,
		insecureSkipVerify: insecure,
		retry:              retry,
	}, nil
}

func (n *httpRequestNode) Type() string { return "http_request" }

func (n *httpRequestNode) Validate(_ context.Context, _ *workflow.Registry) error {
	if n.urlTmpl == "" {
		return fmt.Errorf("http_request: 'url' is required")
	}
	return nil
}

func (n *httpRequestNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()

	// Build template context: data merged with values (values override).
	tmplCtx := buildTemplateContext(inputs)
	extraFuncs := varFuncMap(reg)

	// Render URL.
	resolvedURL, err := renderTemplate("url", n.urlTmpl, tmplCtx, extraFuncs)
	if err != nil {
		return nil, fmt.Errorf("http_request: %w", err)
	}

	// Render method.
	resolvedMethod, err := renderTemplate("method", n.methodTmpl, tmplCtx, extraFuncs)
	if err != nil {
		return nil, fmt.Errorf("http_request: %w", err)
	}
	resolvedMethod = strings.ToUpper(strings.TrimSpace(resolvedMethod))
	if resolvedMethod == "" {
		resolvedMethod = "GET"
	}

	// Render body.
	var body io.Reader
	if n.bodyTmpl != "" {
		rendered, err := renderTemplate("body", n.bodyTmpl, tmplCtx, extraFuncs)
		if err != nil {
			return nil, fmt.Errorf("http_request: %w", err)
		}
		body = strings.NewReader(rendered)
	} else if resolvedMethod == "POST" || resolvedMethod == "PUT" || resolvedMethod == "PATCH" {
		// Fall back: use input data as JSON body when no body template is set.
		if data := inputs["data"]; data != nil {
			b, err := json.Marshal(data)
			if err != nil {
				return nil, fmt.Errorf("http_request: marshal body: %w", err)
			}
			body = bytes.NewReader(b)
		}
	}

	req, err := http.NewRequestWithContext(ctx, resolvedMethod, resolvedURL, body)
	if err != nil {
		return nil, fmt.Errorf("http_request: create request: %w", err)
	}

	// Set default Content-Type for requests with a body.
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Render and apply configured headers.
	for k, tmpl := range n.headerTmpls {
		val, err := renderTemplate("header:"+k, tmpl, tmplCtx, extraFuncs)
		if err != nil {
			return nil, fmt.Errorf("http_request: %w", err)
		}
		req.Header.Set(k, val)
	}

	// Build klient HTTP client with proxy / TLS / retry options.
	client, err := n.buildClient()
	if err != nil {
		return nil, fmt.Errorf("http_request: build client: %w", err)
	}

	resp, err := client.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http_request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("http_request: read response: %w", err)
	}

	// Try to parse as JSON; fall back to string.
	var parsed any
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		parsed = string(respBody)
	}

	// Collect response headers.
	respHeaders := make(map[string]string, len(resp.Header))
	for k := range resp.Header {
		respHeaders[k] = resp.Header.Get(k)
	}

	outData := map[string]any{
		"response":    parsed,
		"status_code": resp.StatusCode,
		"headers":     respHeaders,
	}

	// Selection-based routing by port name.
	selection := []string{"always"}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		selection = append(selection, "success")
	}
	if resp.StatusCode >= 400 {
		selection = append(selection, "error")
	}

	return workflow.NewSelectionResult(outData, selection), nil
}

// buildClient creates a klient.Client with the node's proxy / TLS / retry settings.
func (n *httpRequestNode) buildClient() (*klient.Client, error) {
	opts := []klient.OptionClientFn{
		klient.WithDisableBaseURLCheck(true),
		klient.WithDisableEnvValues(true),
	}
	if n.proxy != "" {
		opts = append(opts, klient.WithProxy(n.proxy))
	}
	if n.insecureSkipVerify {
		opts = append(opts, klient.WithInsecureSkipVerify(true))
	}
	if n.retry {
		opts = append(opts, klient.WithDisableRetry(false))
	} else {
		opts = append(opts, klient.WithDisableRetry(true))
	}

	return klient.New(opts...)
}

// buildTemplateContext merges "data" and "values" inputs into a single map.
// Values override data keys. All inputs are also available at the top level
// so templates can reference {{.data.field}} or {{.values.field}} if needed.
func buildTemplateContext(inputs map[string]any) map[string]any {
	ctx := make(map[string]any)

	// Start with data.
	if data, ok := inputs["data"]; ok {
		if m, ok := data.(map[string]any); ok {
			for k, v := range m {
				ctx[k] = v
			}
		}
	}

	// Overlay values (higher precedence).
	if values, ok := inputs["values"]; ok {
		if m, ok := values.(map[string]any); ok {
			for k, v := range m {
				ctx[k] = v
			}
		}
	}

	// Also make the raw inputs accessible by port name.
	ctx["data"] = inputs["data"]
	ctx["values"] = inputs["values"]

	return ctx
}

// renderTemplate renders a Go text/template string with the given context.
func renderTemplate(name, tmplText string, ctx map[string]any, funcs map[string]any) (string, error) {
	result, err := render.ExecuteWithData(tmplText, ctx, templatex.WithExecFuncMap(funcs))
	if err != nil {
		return "", fmt.Errorf("template %q: %w", name, err)
	}

	return string(result), nil
}
