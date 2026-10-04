package nodes

import (
	"context"
	"io"
	"mime"
	"path"

	"github.com/rakunlabs/at/internal/service/workflow"
)

// The run-file primitives live in the workflow package so the HTTP layer
// (webhook uploads and file responses) shares them; these names keep the
// node code readable.

const (
	maxDownloadBytes        = 100 << 20
	maxAttachmentCount      = 20
	maxAttachmentTotalBytes = 25 << 20
)

type runFileRef = workflow.RunFile

func runWorkspaceDir(ctx context.Context) (string, error) { return workflow.RunWorkspaceDir(ctx) }

func cleanRunPath(raw string) (string, error) { return workflow.CleanRunPath(raw) }

func writeRunFile(ctx context.Context, relPath string, r io.Reader, limit int64, contentType string) (runFileRef, error) {
	return workflow.WriteRunFile(ctx, relPath, r, limit, contentType)
}

func attachmentContentType(explicit, name string, head []byte) string {
	return workflow.FileContentType(explicit, name, head)
}

func safeFileName(name string) string { return workflow.SafeFileName(name) }

// downloadFileName derives a name from Content-Disposition, then the URL path.
func downloadFileName(contentDisposition, rawURLPath string) string {
	if _, params, err := mime.ParseMediaType(contentDisposition); err == nil {
		if name := safeFileName(params["filename"]); name != "" {
			return name
		}
	}
	if name := safeFileName(path.Base(rawURLPath)); name != "" {
		return name
	}
	return "download"
}
