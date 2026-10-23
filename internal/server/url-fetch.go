package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html/charset"
)

const (
	urlFetchDefaultOutput = 100 << 10
	urlFetchMaxOutput     = 1 << 20
	// Pages are read up to this size before conversion; Markdown is usually
	// far smaller than the HTML it came from.
	urlFetchMaxBody        = 5 << 20
	urlFetchDefaultTimeout = 30 * time.Second
	urlFetchMaxTimeout     = 120 * time.Second
)

// urlFetchClient follows at most five redirects and never uses an ambient
// proxy configuration different from the process's own. url_fetch is a host
// tool: like http_request it may reach any address the server can.
var urlFetchClient = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("redirect to unsupported scheme %q", req.URL.Scheme)
		}
		return nil
	},
}

func fetchURLContent(ctx context.Context, client *http.Client, args map[string]any) (string, error) {
	raw, _ := args["url"].(string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("url is required")
	}
	target, err := url.Parse(raw)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return "", fmt.Errorf("only absolute http/https URLs are supported, got: %s", raw)
	}
	format, _ := args["format"].(string)
	switch format {
	case "", "markdown", "text", "html":
	default:
		return "", fmt.Errorf("format must be markdown, text or html")
	}
	if format == "" {
		format = "markdown"
	}
	maxOutput := urlFetchDefaultOutput
	if n, ok := args["max_size"].(float64); ok && n > 0 {
		maxOutput = min(int(n), urlFetchMaxOutput)
	}
	timeout := urlFetchDefaultTimeout
	if n, ok := args["timeout"].(float64); ok && n > 0 {
		timeout = min(time.Duration(n)*time.Second, urlFetchMaxTimeout)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	accept := "text/markdown;q=1.0, text/html;q=0.9, text/plain;q=0.8, application/json;q=0.8, */*;q=0.1"
	if format == "html" {
		accept = "text/html;q=1.0, application/xhtml+xml;q=0.9, */*;q=0.1"
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; AT-url_fetch/1.0)")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(io.LimitReader(resp.Body, urlFetchMaxBody+1))
	if err != nil {
		return "", fmt.Errorf("failed to read content: %w", err)
	}
	bodyCut := len(body) > urlFetchMaxBody
	if bodyCut {
		body = body[:urlFetchMaxBody]
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet := strings.TrimSpace(string(bytes.ToValidUTF8(body[:min(len(body), 500)], nil)))
		if snippet != "" {
			return "", fmt.Errorf("URL returned HTTP %d: %s", resp.StatusCode, snippet)
		}
		return "", fmt.Errorf("URL returned HTTP %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	mediaType, params, _ := mime.ParseMediaType(contentType)
	if mediaType == "" {
		mediaType = http.DetectContentType(body)
		mediaType, params, _ = mime.ParseMediaType(mediaType)
	}
	if !textualMediaType(mediaType) && !looksLikeText(body) {
		return "", fmt.Errorf("URL returned binary content (%s, %d bytes); url_fetch returns text only", firstNonEmpty(mediaType, "unknown type"), len(body))
	}
	if enc, _ := charset.Lookup(params["charset"]); enc != nil && params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8") {
		if decoded, decErr := enc.NewDecoder().Bytes(body); decErr == nil {
			body = decoded
		}
	} else if isHTMLMediaType(mediaType) && !utf8.Valid(body) {
		if reader, readErr := charset.NewReader(bytes.NewReader(body), contentType); readErr == nil {
			if decoded, decErr := io.ReadAll(reader); decErr == nil {
				body = decoded
			}
		}
	}

	text := string(bytes.ToValidUTF8(body, []byte("\uFFFD")))
	if isHTMLMediaType(mediaType) && format != "html" {
		final := resp.Request.URL
		if format == "text" {
			text, err = htmlToText(strings.NewReader(text))
		} else {
			text, err = htmlToMarkdown(strings.NewReader(text), final)
		}
		if err != nil {
			return "", err
		}
	}

	var notes []string
	if resp.Request.URL.String() != target.String() {
		notes = append(notes, "Redirected to "+resp.Request.URL.String())
	}
	if len(text) > maxOutput {
		cut := maxOutput
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut]
		notes = append(notes, fmt.Sprintf("Content truncated at %d bytes; raise max_size (up to %d) or fetch a more specific page.", maxOutput, urlFetchMaxOutput))
	} else if bodyCut {
		notes = append(notes, fmt.Sprintf("The response exceeded %d MiB and was cut before conversion.", urlFetchMaxBody>>20))
	}
	if len(notes) > 0 {
		text += "\n\n[" + strings.Join(notes, " ") + "]"
	}
	return text, nil
}

func isHTMLMediaType(mediaType string) bool {
	return mediaType == "text/html" || mediaType == "application/xhtml+xml"
}

func textualMediaType(mediaType string) bool {
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	switch mediaType {
	case "application/json", "application/xml", "application/javascript", "application/ecmascript",
		"application/x-yaml", "application/yaml", "application/toml", "application/xhtml+xml",
		"application/rss+xml", "application/atom+xml", "application/ld+json", "application/x-ndjson",
		"application/graphql", "application/sql", "image/svg+xml":
		return true
	}
	return strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml")
}

// looksLikeText accepts responses with a generic or missing type whose bytes
// are valid UTF-8 without control characters other than whitespace.
func looksLikeText(body []byte) bool {
	sample := body[:min(len(body), 8192)]
	if len(body) > len(sample) {
		// Allow a rune cut at the sample boundary.
		for i := 0; i < utf8.UTFMax-1 && len(sample) > 0 && !utf8.Valid(sample); i++ {
			sample = sample[:len(sample)-1]
		}
	}
	if !utf8.Valid(sample) {
		return false
	}
	for _, b := range sample {
		if b < 0x20 && b != '\n' && b != '\r' && b != '\t' && b != '\f' {
			return false
		}
	}
	return true
}
