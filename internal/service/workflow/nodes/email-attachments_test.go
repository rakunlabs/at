package nodes_test

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// fakeSMTP accepts one plain-SMTP conversation and hands back the DATA payload.
func fakeSMTP(t *testing.T) (host string, port int, messages <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	out := make(chan string, 4)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveSMTP(conn, out)
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port, out
}

func serveSMTP(conn net.Conn, out chan<- string) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	reply := func(s string) { _, _ = io.WriteString(conn, s+"\r\n") }
	reply("220 test ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			reply("250 test")
		case strings.HasPrefix(cmd, "DATA"):
			reply("354 go ahead")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(strings.TrimPrefix(l, "."))
			}
			out <- b.String()
			reply("250 queued")
		case strings.HasPrefix(cmd, "QUIT"):
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

// attachmentsOf parses a raw message and returns filename → decoded content.
func attachmentsOf(t *testing.T, raw string) map[string][]byte {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string][]byte{}
	var walk func(mediaType string, params map[string]string, body io.Reader)
	walk = func(mediaType string, params map[string]string, body io.Reader) {
		if !strings.HasPrefix(mediaType, "multipart/") {
			return
		}
		mr := multipart.NewReader(body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				return
			}
			partType, partParams, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
			if strings.HasPrefix(partType, "multipart/") {
				walk(partType, partParams, part)
				continue
			}
			if name := part.FileName(); name != "" {
				content, err := io.ReadAll(part)
				if err != nil {
					t.Fatal(err)
				}
				if strings.EqualFold(part.Header.Get("Content-Transfer-Encoding"), "base64") {
					content, err = base64.StdEncoding.DecodeString(strings.Join(strings.Fields(string(content)), ""))
					if err != nil {
						t.Fatal(err)
					}
				}
				found[name] = content
			}
		}
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	walk(mediaType, params, msg.Body)
	return found
}

func smtpNodeConfig(t *testing.T, host string, port int) workflow.NodeConfigLookup {
	t.Helper()
	data, err := json.Marshal(map[string]any{"host": host, "port": port, "from": "at@example.com", "no_tls": true})
	if err != nil {
		t.Fatal(err)
	}
	return func(string) (*service.NodeConfig, error) {
		return &service.NodeConfig{ID: "smtp", Type: "email", Data: string(data)}, nil
	}
}

// A binary download is saved to the run workspace by HTTP Request and reaches
// the recipient byte-for-byte as an Email attachment, via the file reference
// on the attachments port.
func TestHTTPDownloadAttachedToEmail(t *testing.T) {
	executiontest.RequireExecutionFileAccess(t)
	pdf := append([]byte("%PDF-1.7\n"), 0x00, 0xff, 0x10, 0x80, '\n')
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="invoice.pdf"`)
		_, _ = w.Write(pdf)
	}))
	defer upstream.Close()

	host, port, messages := fakeSMTP(t)
	root := t.TempDir()
	graph := service.WorkflowGraph{Nodes: []service.WorkflowNode{
		{ID: "in", Type: "input"},
		{ID: "get", Type: "http_request", Data: map[string]any{"url": upstream.URL + "/files/42", "save_response": true, "save_path": "reports/{{.id}}.pdf"}},
		{ID: "mail", Type: "email", Data: map[string]any{"config_id": "smtp", "to": "ops@example.com", "subject": "Invoice", "body": "Attached."}},
		{ID: "out", Type: "output"},
	}, Edges: []service.WorkflowEdge{
		{Source: "in", SourceHandle: "data", Target: "get", TargetHandle: "data"},
		{Source: "get", SourceHandle: "success", Target: "mail", TargetHandle: "attachments"},
		{Source: "mail", SourceHandle: "success", Target: "out", TargetHandle: "input"},
	}}
	e := workflow.NewEngineWithDependencies(workflow.Dependencies{NodeConfigLookup: smtpNodeConfig(t, host, port)})
	result, err := e.Run(executiontest.WithRoot(t, root), graph, map[string]any{"id": "42"}, []string{"in"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertDataJSON(t, result.Outputs, `{"input":{"status":"sent","attachments":["42.pdf"]}}`)

	stored, err := os.ReadFile(filepath.Join(root, "runs", "test-run", "reports", "42.pdf"))
	if err != nil || string(stored) != string(pdf) {
		t.Fatalf("stored file = %q, %v", stored, err)
	}
	got := attachmentsOf(t, <-messages)
	if string(got["42.pdf"]) != string(pdf) {
		t.Fatalf("attachments = %q", got)
	}
}

// Paths in the templated field, inline base64 items and a missing file.
func TestEmailAttachmentSources(t *testing.T) {
	executiontest.RequireExecutionFileAccess(t)
	host, port, messages := fakeSMTP(t)
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "test-run")
	if err := os.MkdirAll(filepath.Join(runDir, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "out", "report.csv"), []byte("a,b\n1,2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("outside run"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(t *testing.T, data map[string]any, attachments any) (map[string]any, error) {
		t.Helper()
		graph := service.WorkflowGraph{Nodes: []service.WorkflowNode{
			{ID: "in", Type: "input"},
			{ID: "mail", Type: "email", Data: data},
			{ID: "out", Type: "output"},
		}, Edges: []service.WorkflowEdge{
			{Source: "in", SourceHandle: "data", Target: "mail", TargetHandle: "data"},
			{Source: "mail", SourceHandle: "always", Target: "out", TargetHandle: "input"},
		}}
		if attachments != nil {
			graph.Nodes = append(graph.Nodes, service.WorkflowNode{ID: "files", Type: "input"})
			graph.Edges = append(graph.Edges, service.WorkflowEdge{Source: "files", SourceHandle: "data", Target: "mail", TargetHandle: "attachments"})
		}
		e := workflow.NewEngineWithDependencies(workflow.Dependencies{NodeConfigLookup: smtpNodeConfig(t, host, port)})
		entries := []string{"in"}
		inputs := map[string]any{"name": "report"}
		if attachments != nil {
			entries = append(entries, "files")
			inputs["attachments"] = attachments
		}
		result, err := e.Run(executiontest.WithRoot(t, root), graph, inputs, entries, nil)
		if err != nil {
			return nil, err
		}
		return result.Outputs, nil
	}
	base := func(extra map[string]any) map[string]any {
		data := map[string]any{"config_id": "smtp", "to": "ops@example.com", "subject": "S", "body": "B"}
		for k, v := range extra {
			data[k] = v
		}
		return data
	}

	t.Run("templated path and inline base64", func(t *testing.T) {
		inline := map[string]any{"name": "note.bin", "content_base64": base64.StdEncoding.EncodeToString([]byte{1, 2, 3})}
		if _, err := run(t, base(map[string]any{"attachments": "out/{{.name}}.csv"}), []any{inline}); err != nil {
			t.Fatal(err)
		}
		got := attachmentsOf(t, <-messages)
		if string(got["report.csv"]) != "a,b\n1,2\n" || string(got["note.bin"]) != "\x01\x02\x03" || len(got) != 2 {
			t.Fatalf("attachments = %q", got)
		}
	})

	for _, tt := range []struct{ name, field, want string }{
		{"missing file", "out/nope.pdf", "nope.pdf"},
		{"traversal", "../secret.txt", "must not contain '..'"},
		{"absolute", "/etc/passwd", "must be relative"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := run(t, base(map[string]any{"attachments": tt.field}), nil)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			select {
			case <-messages:
				t.Fatal("email sent despite an unresolvable attachment")
			default:
			}
		})
	}
}

func TestGateWaitsForSignalBranch(t *testing.T) {
	graph := func(signalActive bool) service.WorkflowGraph {
		expr := "false"
		if signalActive {
			expr = "true"
		}
		return service.WorkflowGraph{Nodes: []service.WorkflowNode{
			{ID: "in", Type: "input"},
			{ID: "check", Type: "conditional", Data: map[string]any{"expression": expr}},
			{ID: "work", Type: "edit_fields", Data: map[string]any{"keep_input": false, "fields": []any{map[string]any{"name": "done", "source": "value", "value_type": "boolean", "value": "true"}}}},
			{ID: "gate", Type: "gate", Data: map[string]any{"pass_signal": true}},
			{ID: "out", Type: "output"},
		}, Edges: []service.WorkflowEdge{
			{Source: "in", SourceHandle: "data", Target: "check", TargetHandle: "data"},
			{Source: "check", SourceHandle: "true", Target: "work", TargetHandle: "data"},
			{Source: "work", SourceHandle: "data", Target: "gate", TargetHandle: "signal"},
			{Source: "in", SourceHandle: "data", Target: "gate", TargetHandle: "data"},
			{Source: "gate", SourceHandle: "data", Target: "out", TargetHandle: "input"},
		}}
	}

	t.Run("signal delivered", func(t *testing.T) {
		result, err := workflow.NewEngineWithDependencies(workflow.Dependencies{}).Run(executiontest.Context(t), graph(true), map[string]any{"order": 7}, []string{"in"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		assertDataJSON(t, result.Outputs, `{"input":{"order":7}}`)
	})
	t.Run("signal branch inactive", func(t *testing.T) {
		e := workflow.NewEngineWithDependencies(workflow.Dependencies{})
		events := make(chan workflow.NodeEvent, 64)
		e.SetEventChannel(events)
		result, err := e.Run(executiontest.Context(t), graph(false), map[string]any{"order": 7}, []string{"in"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Outputs) != 0 {
			t.Fatalf("data passed the gate without a signal: %v", result.Outputs)
		}
		skipped := false
		for len(events) > 0 {
			if ev := <-events; ev.NodeID == "gate" && ev.EventType == "skipped" {
				skipped = true
			}
		}
		if !skipped {
			t.Fatal("gate did not report its branch as skipped")
		}
	})
}

func TestGatePassSignal(t *testing.T) {
	n := makeNode(t, "gate", map[string]any{"pass_signal": true})
	result, err := n.Run(t.Context(), nil, map[string]any{"data": "payload", "signal": map[string]any{"ok": true}})
	if err != nil {
		t.Fatal(err)
	}
	assertDataJSON(t, result.Data(), `{"data":"payload","signal":{"ok":true}}`)
	if _, err := n.Run(t.Context(), nil, map[string]any{"data": "payload"}); !errors.Is(err, workflow.ErrStopBranch) {
		t.Fatalf("missing signal error = %v", err)
	}
}
