package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestHTMLToMarkdown(t *testing.T) {
	page := `<!doctype html><html><head><title>Guide  Page</title><style>p{}</style><script>alert(1)</script></head>
<body><nav><a href="/">Home</a></nav>
<main>
<h1>Install</h1>
<p>Run the <strong>installer</strong> from <a href="/dl/latest">the downloads page</a>.<br>Then restart.</p>
<ul><li>First <em>step</em></li><li>Second<ul><li>Nested</li></ul></li></ul>
<ol><li>One</li><li>Two</li></ol>
<pre><code class="language-sh">make build
make run</code></pre>
<p>Inline <code>go test</code> and a hidden <span style="display: none">secret</span> bit.</p>
<blockquote><p>Quoted text</p></blockquote>
<table><tr><th>Name</th><th>Value</th></tr><tr><td>a|b</td><td>1</td></tr></table>
<img src="/logo.png" alt="Logo">
<form><input value="x"><button>Send</button></form>
</main>
<footer>Copyright</footer>
</body></html>`
	base, _ := url.Parse("https://example.com/docs/guide")
	got, err := htmlToMarkdown(strings.NewReader(page), base)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Install",
		"Run the **installer** from [the downloads page](https://example.com/dl/latest).\nThen restart.",
		"- First *step*",
		"- Second\n  - Nested",
		"1. One\n2. Two",
		"```sh\nmake build\nmake run\n```",
		"Inline `go test` and a hidden bit.",
		"> Quoted text",
		"| Name | Value |\n| --- | --- |\n| a\\|b | 1 |",
		"![Logo](https://example.com/logo.png)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"alert", "p{}", "Home", "Copyright", "secret", "Send"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("unexpected %q in:\n%s", unwanted, got)
		}
	}
}

func TestHTMLToMarkdownUsesTitleWithoutHeading(t *testing.T) {
	got, err := htmlToMarkdown(strings.NewReader(`<html><head><title>Only Title</title></head><body><p>Body</p></body></html>`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "# Only Title\n\nBody") {
		t.Fatalf("got %q", got)
	}
}

func TestFetchURLContent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body><h2>Hello</h2><p>World <a href="next">link</a></p></body></html>`))
	})
	mux.HandleFunc("/latin1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=iso-8859-1")
		_, _ = w.Write([]byte{'c', 'a', 'f', 0xe9})
	})
	mux.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n\x00\x00"))
	})
	mux.HandleFunc("/missing", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no such page", http.StatusNotFound)
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/json", http.StatusFound)
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("ab", 2000)))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	fetch := func(args map[string]any) (string, error) {
		return fetchURLContent(context.Background(), urlFetchClient, args)
	}

	got, err := fetch(map[string]any{"url": srv.URL + "/page"})
	if err != nil || !strings.Contains(got, "## Hello") || !strings.Contains(got, "[link]("+srv.URL+"/next)") {
		t.Fatalf("markdown: %q %v", got, err)
	}
	got, err = fetch(map[string]any{"url": srv.URL + "/page", "format": "text"})
	if err != nil || strings.Contains(got, "##") || !strings.Contains(got, "Hello\n\nWorld link") {
		t.Fatalf("text: %q %v", got, err)
	}
	got, err = fetch(map[string]any{"url": srv.URL + "/page", "format": "html"})
	if err != nil || !strings.Contains(got, "<h2>Hello</h2>") {
		t.Fatalf("html: %q %v", got, err)
	}
	if got, err = fetch(map[string]any{"url": srv.URL + "/latin1"}); err != nil || got != "café" {
		t.Fatalf("charset: %q %v", got, err)
	}
	if got, err = fetch(map[string]any{"url": srv.URL + "/redirect"}); err != nil || !strings.HasPrefix(got, `{"ok":true}`) || !strings.Contains(got, "Redirected to") {
		t.Fatalf("redirect: %q %v", got, err)
	}
	if _, err = fetch(map[string]any{"url": srv.URL + "/png"}); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("binary: %v", err)
	}
	if _, err = fetch(map[string]any{"url": srv.URL + "/missing"}); err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "no such page") {
		t.Fatalf("status: %v", err)
	}
	if got, err = fetch(map[string]any{"url": srv.URL + "/big", "max_size": float64(100)}); err != nil || !strings.HasPrefix(got, strings.Repeat("ab", 50)+"\n\n[Content truncated at 100 bytes") {
		t.Fatalf("truncate: %q %v", got, err)
	}
	for _, bad := range []map[string]any{{"url": "file:///etc/passwd"}, {"url": ""}, {"url": srv.URL, "format": "pdf"}} {
		if _, err := fetch(bad); err == nil {
			t.Fatalf("expected error for %v", bad)
		}
	}
}
