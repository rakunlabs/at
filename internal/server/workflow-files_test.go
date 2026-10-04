package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rakunlabs/at/internal/service/executiontest"
	"github.com/rakunlabs/at/internal/service/workflow"
)

func TestStoreWebhookUploads(t *testing.T) {
	executiontest.RequireExecutionFileAccess(t)

	t.Run("multipart files and form fields", func(t *testing.T) {
		root := t.TempDir()
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		_ = mw.WriteField("title", "invoice")
		fw, _ := mw.CreateFormFile("doc", "a.pdf")
		_, _ = fw.Write([]byte("%PDF-1.4 one"))
		fw, _ = mw.CreateFormFile("doc", "a.pdf")
		_, _ = fw.Write([]byte("%PDF-1.4 two"))
		_ = mw.Close()

		header := http.Header{"Content-Type": {mw.FormDataContentType()}}
		got, err := storeWebhookUploads(executiontest.WithRoot(t, root), header, body.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if got["form"].(map[string]any)["title"] != "invoice" {
			t.Fatalf("form = %#v", got["form"])
		}
		files := got["files"].([]any)
		if len(files) != 2 {
			t.Fatalf("files = %#v", files)
		}
		second := files[1].(map[string]any)
		if second["path"] != "uploads/2-a.pdf" || second["name"] != "a.pdf" || second["field"] != "doc" {
			t.Fatalf("second file = %#v", second)
		}
		content, _ := os.ReadFile(filepath.Join(root, "runs", "test-run", "uploads", "2-a.pdf"))
		if string(content) != "%PDF-1.4 two" {
			t.Fatalf("stored content = %q", content)
		}
		if got["file"].(map[string]any)["path"] != "uploads/a.pdf" {
			t.Fatalf("file = %#v", got["file"])
		}
	})

	t.Run("raw binary body", func(t *testing.T) {
		header := http.Header{"Content-Type": {"image/png"}, "X-File-Name": {"../../cat.png"}}
		got, err := storeWebhookUploads(executiontest.WithRoot(t, t.TempDir()), header, []byte("\x89PNG\r\n\x1a\nxx"))
		if err != nil {
			t.Fatal(err)
		}
		file := got["file"].(map[string]any)
		if file["path"] != "uploads/cat.png" || file["content_type"] != "image/png" {
			t.Fatalf("file = %#v", file)
		}
	})

	t.Run("json body is left alone", func(t *testing.T) {
		header := http.Header{"Content-Type": {"application/json"}}
		got, err := storeWebhookUploads(executiontest.WithRoot(t, t.TempDir()), header, []byte(`{"a":1}`))
		if err != nil || got != nil {
			t.Fatalf("got %#v, %v", got, err)
		}
	})
}

func TestWriteWorkflowSyncResponse(t *testing.T) {
	executiontest.RequireExecutionFileAccess(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "runs", "test-run"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "runs", "test-run", "report.pdf"), []byte("%PDF-1.4 x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := executiontest.WithRoot(t, root)
	stored := workflow.ResponseFile{WorkspacePath: "runs/test-run/report.pdf", Name: "report.pdf", ContentType: "application/pdf"}
	inline := workflow.ResponseFile{Name: "note.txt", ContentType: "text/plain", Content: []byte("hi"), HasContent: true}
	envelope := runWorkflowResponse{RunID: "run_1", WorkflowID: "wf", Status: "completed", Outputs: map[string]any{"text": "done"}}

	t.Run("json default", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writeWorkflowSyncResponse(rec, envelope, nil)
		if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("%d %s", rec.Code, rec.Header().Get("Content-Type"))
		}
	})

	t.Run("file", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writeWorkflowSyncResponse(rec, envelope, workflow.NewOutputResponse(ctx, workflow.OutputResponseFile, "attachment", []workflow.ResponseFile{stored}))
		if rec.Code != 200 || rec.Body.String() != "%PDF-1.4 x" {
			t.Fatalf("%d %q", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Content-Type") != "application/pdf" || rec.Header().Get("Content-Disposition") != `attachment; filename=report.pdf` || rec.Header().Get("X-AT-Run-ID") != "run_1" {
			t.Fatalf("headers = %#v", rec.Header())
		}
	})

	t.Run("multipart", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writeWorkflowSyncResponse(rec, envelope, workflow.NewOutputResponse(ctx, workflow.OutputResponseMultipart, "", []workflow.ResponseFile{stored, inline}))
		mediaType, params, _ := mime.ParseMediaType(rec.Header().Get("Content-Type"))
		if mediaType != "multipart/mixed" {
			t.Fatalf("content type = %s", mediaType)
		}
		mr := multipart.NewReader(rec.Body, params["boundary"])
		var parts []string
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(p)
			if p.FormName() == "result" {
				var got runWorkflowResponse
				if err := json.Unmarshal(b, &got); err != nil || got.Outputs["text"] != "done" {
					t.Fatalf("result part = %s", b)
				}
			}
			parts = append(parts, p.FileName()+"="+string(b))
		}
		if len(parts) != 3 || parts[1] != "report.pdf=%PDF-1.4 x" || parts[2] != "note.txt=hi" {
			t.Fatalf("parts = %q", parts)
		}
	})

	t.Run("missing file is an error before headers", func(t *testing.T) {
		rec := httptest.NewRecorder()
		missing := workflow.ResponseFile{WorkspacePath: "runs/test-run/gone.pdf", Name: "gone.pdf"}
		writeWorkflowSyncResponse(rec, envelope, workflow.NewOutputResponse(ctx, workflow.OutputResponseFile, "", []workflow.ResponseFile{missing}))
		if rec.Code != 500 {
			t.Fatalf("code = %d", rec.Code)
		}
	})
}
