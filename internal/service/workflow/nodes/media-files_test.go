package nodes_test

import (
	"context"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
	"github.com/rakunlabs/at/internal/service/workflow"
)

var testPNG = append([]byte("\x89PNG\r\n\x1a\n"), []byte("not really a png but sniffed as one")...)

type mailPart struct {
	contentType string
	disposition string
	contentID   string
	body        []byte
}

// mailParts flattens a raw message into its leaf parts, decoding base64.
func mailParts(t *testing.T, raw string) []mailPart {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	var parts []mailPart
	var walk func(header func(string) string, body io.Reader)
	walk = func(header func(string) string, body io.Reader) {
		mediaType, params, _ := mime.ParseMediaType(header("Content-Type"))
		if strings.HasPrefix(mediaType, "multipart/") {
			mr := multipart.NewReader(body, params["boundary"])
			for {
				p, err := mr.NextPart()
				if err != nil {
					return
				}
				walk(p.Header.Get, p)
			}
		}
		content, err := io.ReadAll(body)
		if err != nil {
			t.Fatal(err)
		}
		switch strings.ToLower(header("Content-Transfer-Encoding")) {
		case "base64":
			content, err = base64.StdEncoding.DecodeString(strings.Join(strings.Fields(string(content)), ""))
			if err != nil {
				t.Fatal(err)
			}
		case "quoted-printable":
			content, _ = io.ReadAll(quotedprintable.NewReader(strings.NewReader(string(content))))
		}
		parts = append(parts, mailPart{contentType: mediaType, disposition: header("Content-Disposition"), contentID: header("Content-ID"), body: content})
	}
	walk(msg.Header.Get, msg.Body)
	return parts
}

// An image-output model's picture travels from LLM Call to Email: embedded in
// the HTML body via Content-ID and attached as a file, with the model's
// Markdown answer rendered into the body.
func TestLLMCallImageToEmail(t *testing.T) {
	executiontest.RequireExecutionFileAccess(t)
	host, port, messages := fakeSMTP(t)
	root := t.TempDir()

	provider := &mockProvider{chatFunc: func(context.Context, string, []service.Message, []service.Tool, *service.ChatOptions) (*service.LLMResponse, error) {
		return &service.LLMResponse{
			Content:      "Here is **your cat**.",
			InlineImages: []service.InlineImage{{MimeType: "image/png", Data: base64.StdEncoding.EncodeToString(testPNG)}},
			Finished:     true,
		}, nil
	}}
	graph := service.WorkflowGraph{Nodes: []service.WorkflowNode{
		{ID: "in", Type: "input"},
		{ID: "llm", Type: "llm_call", Data: map[string]any{"provider": "p"}},
		{ID: "mail", Type: "email", Data: map[string]any{
			"config_id": "smtp", "to": "ops@example.com", "subject": "Picture", "content_type": "text/html",
			"body": `<h1>Result</h1>{{markdown .data}}`,
		}},
		{ID: "out", Type: "output"},
	}, Edges: []service.WorkflowEdge{
		{Source: "in", SourceHandle: "data", Target: "llm", TargetHandle: "prompt"},
		{Source: "llm", SourceHandle: "response", Target: "mail", TargetHandle: "data"},
		{Source: "llm", SourceHandle: "image", Target: "mail", TargetHandle: "inline_images"},
		{Source: "llm", SourceHandle: "files", Target: "mail", TargetHandle: "attachments"},
		{Source: "mail", SourceHandle: "success", Target: "out", TargetHandle: "input"},
	}}
	e := workflow.NewEngineWithDependencies(workflow.Dependencies{
		NodeConfigLookup: smtpNodeConfig(t, host, port),
		ProviderLookup:   func(string) (service.LLMProvider, string, error) { return provider, "m", nil },
	})
	result, err := e.Run(executiontest.WithRoot(t, root), graph, map[string]any{"prompt": "draw a cat"}, []string{"in"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertDataJSON(t, result.Outputs, `{"input":{"status":"sent","attachments":["generated-image-01.png"],"inline_images":["generated-image-01.png"]}}`)

	matches, _ := filepath.Glob(filepath.Join(root, "runs", "test-run", "llm-*", "generated-image-01.png"))
	if len(matches) != 1 {
		t.Fatalf("stored images = %v", matches)
	}

	var html, embedded, attached *mailPart
	parts := mailParts(t, <-messages)
	for i := range parts {
		p := &parts[i]
		switch {
		case p.contentType == "text/html":
			html = p
		case strings.HasPrefix(p.disposition, "inline"):
			embedded = p
		case strings.HasPrefix(p.disposition, "attachment"):
			attached = p
		}
	}
	if html == nil || embedded == nil || attached == nil {
		t.Fatalf("parts = %+v", parts)
	}
	cid := strings.Trim(embedded.contentID, "<>")
	body := string(html.body)
	if !strings.Contains(body, `src="cid:`+cid+`"`) || !strings.Contains(body, "<strong>your cat</strong>") {
		t.Fatalf("html body = %s (cid %q)", body, cid)
	}
	if string(embedded.body) != string(testPNG) || string(attached.body) != string(testPNG) {
		t.Fatal("image bytes changed in transit")
	}
}

// The body chooses where an inline image goes with {{cid ...}}; raw HTML in
// model Markdown is dropped; non-image files are refused as inline images.
func TestEmailInlineImageReferences(t *testing.T) {
	executiontest.RequireExecutionFileAccess(t)
	host, port, messages := fakeSMTP(t)
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "test-run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "chart.png"), testPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(data map[string]any) error {
		base := map[string]any{"config_id": "smtp", "to": "ops@example.com", "subject": "S", "content_type": "text/html"}
		for k, v := range data {
			base[k] = v
		}
		graph := service.WorkflowGraph{Nodes: []service.WorkflowNode{
			{ID: "in", Type: "input"},
			{ID: "mail", Type: "email", Data: base},
		}, Edges: []service.WorkflowEdge{{Source: "in", SourceHandle: "data", Target: "mail", TargetHandle: "data"}}}
		e := workflow.NewEngineWithDependencies(workflow.Dependencies{NodeConfigLookup: smtpNodeConfig(t, host, port)})
		_, err := e.Run(executiontest.WithRoot(t, root), graph, map[string]any{"text": "ok <script>x</script>"}, []string{"in"}, nil)
		return err
	}

	if err := run(map[string]any{"inline_images": "chart.png", "body": `<p>{{markdown .text}}</p><img src="{{cid "chart.png"}}"><img src="{{cid 0}}">`}); err != nil {
		t.Fatal(err)
	}
	for _, p := range mailParts(t, <-messages) {
		if p.contentType != "text/html" {
			continue
		}
		body := string(p.body)
		if strings.Count(body, `src="cid:chart.png@at"`) != 2 || strings.Contains(body, "<script>") || strings.Contains(body, "<p><img") {
			t.Fatalf("body = %s", body)
		}
	}

	if err := run(map[string]any{"inline_images": "notes.txt", "body": "x"}); err == nil || !strings.Contains(err.Error(), "not an image") {
		t.Fatalf("non-image inline error = %v", err)
	}
	if err := run(map[string]any{"inline_images": "chart.png", "body": `{{cid "missing.png"}}`}); err == nil || !strings.Contains(err.Error(), "missing.png") {
		t.Fatalf("unknown cid error = %v", err)
	}
	select {
	case <-messages:
		t.Fatal("email sent despite an invalid inline image")
	default:
	}
}

// Files an agent's tools write to their working directory, and images the
// model returns directly, become the node's "files" output.
func TestAgentCallEmitsProducedFiles(t *testing.T) {
	executiontest.RequireExecutionFileAccess(t)
	ctx := executiontest.Context(t)
	calls := 0
	var sawPrompt string
	mp := &mockProvider{chatFunc: func(_ context.Context, _ string, messages []service.Message, _ []service.Tool, _ *service.ChatOptions) (*service.LLMResponse, error) {
		calls++
		if calls == 1 {
			sawPrompt, _ = messages[len(messages)-1].Content.(string)
			return &service.LLMResponse{
				InlineImages: []service.InlineImage{{MimeType: "image/png", Data: base64.StdEncoding.EncodeToString(testPNG)}},
				ToolCalls:    []service.ToolCall{{ID: "tc1", Name: "bash_execute", Arguments: map[string]any{}}},
			}, nil
		}
		return &service.LLMResponse{Content: "done", Finished: true}, nil
	}}
	reg := newTestRegistryWithProvider(mp)
	reg.AgentLookup = func(context.Context, string) (*service.Agent, error) {
		return &service.Agent{Config: service.AgentConfig{Provider: "test-provider", BuiltinTools: []string{"bash_execute"}}}, nil
	}
	reg.BuiltinToolDefs = []workflow.BuiltinToolDef{{Name: "bash_execute"}}
	reg.BuiltinToolDispatcher = func(toolCtx context.Context, _ string, _ map[string]any) (string, error) {
		dir := workflow.WorkDirFromContext(toolCtx)
		if dir == "" {
			t.Fatal("tool has no working directory")
		}
		if err := os.WriteFile(filepath.Join(dir, "report.txt"), []byte("report"), 0o644); err != nil {
			return "", err
		}
		return "ok", os.WriteFile(filepath.Join(dir, ".hidden"), []byte("x"), 0o644)
	}
	node := makeNode(t, "agent_call", map[string]any{"agent_id": "a", "max_iterations": float64(3)})
	res, err := node.Run(ctx, reg, map[string]any{"prompt": "make things"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sawPrompt, "Save every file you produce") {
		t.Fatalf("prompt does not name the output directory: %q", sawPrompt)
	}
	data := res.Data()
	files, _ := data["files"].([]any)
	if data["response"] != "done" || len(files) != 2 {
		t.Fatalf("output = %+v", data)
	}
	names := []string{files[0].(map[string]any)["name"].(string), files[1].(map[string]any)["name"].(string)}
	if names[0] != "generated-image-01.png" || names[1] != "report.txt" {
		t.Fatalf("file names = %v", names)
	}
	image, _ := data["image"].(map[string]any)
	if image["name"] != "generated-image-01.png" || image["content_type"] != "image/png" {
		t.Fatalf("image = %+v", image)
	}
}

// Without a run workspace the agent still answers; it just reports no files.
func TestAgentCallWithoutWorkspace(t *testing.T) {
	mp := &mockProvider{}
	node := makeNode(t, "agent_call", map[string]any{"provider": "test-provider", "max_iterations": float64(1)})
	res, err := node.Run(executiontest.Context(t), newTestRegistryWithProvider(mp), map[string]any{"prompt": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Data()["response"] != "mock response" {
		t.Fatalf("output = %+v", res.Data())
	}
}

// Files connected to LLM Call reach the model as native content blocks next
// to the prompt: a PDF from the run workspace as a document, an image as an
// image, small text inline. Without files the prompt stays a plain string.
func TestLLMCallSendsFilesToModel(t *testing.T) {
	executiontest.RequireExecutionFileAccess(t)
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "test-run")
	if err := os.MkdirAll(filepath.Join(runDir, "in"), 0o755); err != nil {
		t.Fatal(err)
	}
	pdf := []byte("%PDF-1.7\n%\xff\xfe binary")
	for name, content := range map[string][]byte{"in/report.pdf": pdf, "in/chart.png": testPNG, "in/notes.md": []byte("# Notes\nok")} {
		if err := os.WriteFile(filepath.Join(runDir, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var got any
	mp := &mockProvider{chatFunc: func(_ context.Context, _ string, messages []service.Message, _ []service.Tool, _ *service.ChatOptions) (*service.LLMResponse, error) {
		got = messages[len(messages)-1].Content
		return &service.LLMResponse{Content: "ok", Finished: true}, nil
	}}
	node := makeNode(t, "llm_call", map[string]any{"provider": "test-provider"})
	ctx := executiontest.WithRoot(t, root)
	files := []any{
		map[string]any{"path": "in/report.pdf"},
		"in/chart.png",
		map[string]any{"path": "in/notes.md"},
		"data:image/png;base64," + base64.StdEncoding.EncodeToString(testPNG),
	}
	if _, err := node.Run(ctx, newTestRegistryWithProvider(mp), map[string]any{"prompt": "summarise", "attachments": files}); err != nil {
		t.Fatal(err)
	}
	blocks, ok := got.([]service.ContentBlock)
	if !ok {
		t.Fatalf("content = %T", got)
	}
	var kinds []string
	for _, b := range blocks {
		switch {
		case b.Type == "text":
			kinds = append(kinds, "text:"+b.Text)
		case b.Source != nil:
			data, _ := base64.StdEncoding.DecodeString(b.Source.Data)
			kinds = append(kinds, b.Type+":"+b.Source.MediaType+":"+b.Source.Filename)
			if (b.Source.MediaType == "application/pdf" && string(data) != string(pdf)) || (b.Type == "image" && string(data) != string(testPNG)) {
				t.Fatalf("%s bytes changed", b.Source.Filename)
			}
		}
	}
	want := []string{
		"text:summarise",
		"text:Attached file: report.pdf", "document:application/pdf:report.pdf",
		"text:Attached file: chart.png", "image:image/png:chart.png",
		"text:Attached file: notes.md", "text:# Notes\nok",
		"text:Attached file: file", "image:image/png:file",
	}
	if strings.Join(kinds, "|") != strings.Join(want, "|") {
		t.Fatalf("blocks =\n%s\nwant\n%s", strings.Join(kinds, "\n"), strings.Join(want, "\n"))
	}

	if _, err := node.Run(ctx, newTestRegistryWithProvider(mp), map[string]any{"prompt": "plain"}); err != nil {
		t.Fatal(err)
	}
	if got != "plain" {
		t.Fatalf("content without files = %#v", got)
	}
}

// Remote URLs and paths outside the run workspace are refused before the
// model is called.
func TestModelFilesRefused(t *testing.T) {
	executiontest.RequireExecutionFileAccess(t)
	called := false
	mp := &mockProvider{chatFunc: func(context.Context, string, []service.Message, []service.Tool, *service.ChatOptions) (*service.LLMResponse, error) {
		called = true
		return &service.LLMResponse{Content: "ok", Finished: true}, nil
	}}
	for _, tt := range []struct {
		name  string
		files any
		want  string
	}{
		{"remote url", "https://example.com/a.pdf", "HTTP Request"},
		{"traversal", "../secret.txt", "must not contain '..'"},
		{"missing", "nope.pdf", "nope.pdf"},
		{"too many", []any{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}, "too many files"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, nodeType := range []string{"llm_call", "agent_call"} {
				node := makeNode(t, nodeType, map[string]any{"provider": "test-provider", "max_iterations": float64(1)})
				_, err := node.Run(executiontest.Context(t), newTestRegistryWithProvider(mp), map[string]any{"prompt": "p", "attachments": tt.files})
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("%s error = %v, want %q", nodeType, err, tt.want)
				}
			}
		})
	}
	if called {
		t.Fatal("model called despite unusable files")
	}
}
