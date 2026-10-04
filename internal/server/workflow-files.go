package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"

	"github.com/rakunlabs/at/internal/service/workflow"
)

// writeWorkflowSyncResponse answers a synchronous run (webhook or run API with
// ?sync=true). The JSON envelope is the default; an Output node in file mode
// streams the file itself, and multipart mode sends the JSON envelope as the
// first part followed by every file, so one request returns text and files.
func writeWorkflowSyncResponse(w http.ResponseWriter, envelope runWorkflowResponse, resp *workflow.OutputResponse) {
	if resp == nil || resp.Mode == "" || resp.Mode == workflow.OutputResponseJSON {
		httpResponseJSON(w, envelope, http.StatusOK)
		return
	}

	// Open every file before writing anything, so a vanished file still gets
	// a real error status instead of a truncated 200.
	files, err := resp.OpenFiles()
	if err != nil {
		slog.Error("workflow response: open files failed", "run_id", envelope.RunID, "error", err)
		httpResponse(w, fmt.Sprintf("workflow response file unavailable: %v", err), http.StatusInternalServerError)
		return
	}
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()

	w.Header().Set("X-AT-Run-ID", envelope.RunID)
	w.Header().Set("X-AT-Workflow-ID", envelope.WorkflowID)
	w.Header().Set("X-Content-Type-Options", "nosniff")

	switch resp.Mode {
	case workflow.OutputResponseFile:
		if len(files) == 0 {
			httpResponse(w, "workflow response has no file", http.StatusInternalServerError)
			return
		}
		writeWorkflowFileResponse(w, files[0], resp.Disposition)
	case workflow.OutputResponseMultipart:
		writeWorkflowMultipartResponse(w, envelope, files)
	default:
		httpResponseJSON(w, envelope, http.StatusOK)
	}
}

func writeWorkflowFileResponse(w http.ResponseWriter, f *workflow.OpenedFile, disposition string) {
	if disposition != "inline" {
		disposition = "attachment"
	}
	// A file chosen by workflow data must never render as an active document
	// on the application origin.
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("Content-Type", f.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": f.Name}))
	w.Header().Set("Content-Length", strconv.FormatInt(f.Size, 10))
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, io.LimitReader(f.Reader, f.Size)); err != nil {
		slog.Warn("workflow response: stream file failed", "name", f.Name, "error", err)
	}
}

func writeWorkflowMultipartResponse(w http.ResponseWriter, envelope runWorkflowResponse, files []*workflow.OpenedFile) {
	mw := multipart.NewWriter(w)
	w.Header().Set("Content-Type", "multipart/mixed; boundary="+mw.Boundary())
	w.WriteHeader(http.StatusOK)

	header := textproto.MIMEHeader{}
	header.Set("Content-Type", "application/json")
	header.Set("Content-Disposition", `form-data; name="result"`)
	part, err := mw.CreatePart(header)
	if err == nil {
		var buf bytes.Buffer
		if err = json.NewEncoder(&buf).Encode(envelope); err == nil {
			_, err = part.Write(buf.Bytes())
		}
	}
	for i, f := range files {
		if err != nil {
			break
		}
		header := textproto.MIMEHeader{}
		header.Set("Content-Type", f.ContentType)
		header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
			"name":     "file" + strconv.Itoa(i),
			"filename": f.Name,
		}))
		header.Set("Content-Length", strconv.FormatInt(f.Size, 10))
		if part, err = mw.CreatePart(header); err == nil {
			_, err = io.Copy(part, io.LimitReader(f.Reader, f.Size))
		}
	}
	if err == nil {
		err = mw.Close()
	}
	if err != nil {
		slog.Warn("workflow response: stream multipart failed", "run_id", envelope.RunID, "error", err)
	}
}

const (
	maxWebhookUploadFiles = 20
	maxWebhookFormValues  = 256
)

// storeWebhookUploads moves file content of an inbound webhook request into
// the run workspace and returns the inputs that describe it:
//
//   - multipart/form-data: "files" (file references, form order), "form"
//     (text fields, first value per name) and "file" (the first file)
//   - any other non-text body (PDF, image, octet-stream…): "file", the body
//     stored under its Content-Disposition / X-File-Name name, else "upload"
//
// JSON, text and urlencoded bodies are left alone (nil, nil). The raw "body"
// input is unchanged in every case, so existing workflows behave as before.
func storeWebhookUploads(ctx context.Context, header http.Header, body []byte) (map[string]any, error) {
	if len(body) == 0 {
		return nil, nil
	}
	mediaType, params, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil {
		mediaType = ""
	}

	if mediaType == "multipart/form-data" {
		return storeWebhookMultipart(ctx, params["boundary"], body)
	}
	if !webhookBinaryBody(mediaType, body) {
		return nil, nil
	}

	name := ""
	if _, dp, err := mime.ParseMediaType(header.Get("Content-Disposition")); err == nil {
		name = workflow.SafeFileName(dp["filename"])
	}
	if name == "" {
		name = workflow.SafeFileName(header.Get("X-File-Name"))
	}
	if name == "" {
		name = "upload"
	}
	ref, err := workflow.WriteRunFile(ctx, "uploads/"+name, bytes.NewReader(body), int64(len(body)), mediaType)
	if err != nil {
		return nil, err
	}
	return map[string]any{"file": ref.ToMap(), "files": []any{ref.ToMap()}}, nil
}

// webhookBinaryBody reports whether a non-multipart body is file content.
// Without a declared type the bytes decide.
func webhookBinaryBody(mediaType string, body []byte) bool {
	if mediaType == "" {
		mediaType, _, _ = mime.ParseMediaType(http.DetectContentType(body))
	}
	switch {
	case strings.HasPrefix(mediaType, "text/"),
		mediaType == "application/json",
		strings.HasSuffix(mediaType, "+json"),
		mediaType == "application/xml",
		strings.HasSuffix(mediaType, "+xml"),
		mediaType == "application/x-www-form-urlencoded",
		mediaType == "application/x-ndjson":
		return false
	}
	return true
}

func storeWebhookMultipart(ctx context.Context, boundary string, body []byte) (map[string]any, error) {
	if boundary == "" {
		return nil, fmt.Errorf("multipart body without boundary")
	}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	form := map[string]any{}
	files := []any{}
	used := map[string]int{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read multipart body: %w", err)
		}
		field := part.FormName()
		filename := part.FileName()
		if filename == "" {
			if field != "" && len(form) < maxWebhookFormValues {
				if _, exists := form[field]; !exists {
					value, err := io.ReadAll(io.LimitReader(part, 1<<20))
					if err != nil {
						part.Close()
						return nil, fmt.Errorf("read form field %q: %w", field, err)
					}
					form[field] = string(value)
				}
			}
			part.Close()
			continue
		}
		if len(files) >= maxWebhookUploadFiles {
			part.Close()
			return nil, fmt.Errorf("too many uploaded files (maximum %d)", maxWebhookUploadFiles)
		}
		name := workflow.SafeFileName(filename)
		if name == "" {
			name = "upload"
		}
		// Two parts with the same file name must not overwrite each other.
		used[name]++
		target := name
		if used[name] > 1 {
			target = fmt.Sprintf("%d-%s", used[name], name)
		}
		ref, err := workflow.WriteRunFile(ctx, "uploads/"+target, part, int64(len(body)), part.Header.Get("Content-Type"))
		part.Close()
		if err != nil {
			return nil, err
		}
		item := ref.ToMap()
		item["name"] = name
		item["field"] = field
		files = append(files, item)
	}
	out := map[string]any{"form": form, "files": files}
	if len(files) > 0 {
		out["file"] = files[0]
	}
	return out, nil
}
