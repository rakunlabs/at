package nodes

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

// Nodes that produce files (model-generated images, files an agent writes)
// store them in a fresh directory of the run workspace and emit run file
// references under "files", the shape Email's attachments port accepts.

const (
	maxGeneratedImageBytes = 25 << 20
	maxRunOutputFiles      = 100
)

// generatedImageExt lists the image types a model may return, with the
// extension used when storing them.
var generatedImageExt = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/jpg":  ".jpg",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

// newRunOutputDir names a directory unique to one node invocation, so fan-out
// branches and repeated nodes never overwrite each other's files.
func newRunOutputDir(prefix string) string {
	return prefix + "-" + strings.ToLower(ulid.Make().String())
}

// createRunOutputDir creates rel inside the run workspace and returns its
// absolute path, for tools that address files by host path.
func createRunOutputDir(ctx context.Context, rel string) (string, error) {
	runDir, err := runWorkspaceDir(ctx)
	if err != nil {
		return "", err
	}
	root, name, err := service.OpenExecutionRoot(ctx, path.Join(runDir, rel), true)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err := root.MkdirAll(name, 0o755); err != nil {
		return "", fmt.Errorf("create output directory: %w", err)
	}
	_, base, _ := service.ExecutionFromContext(ctx)
	return filepath.Join(base, name), nil
}

// collectRunOutputFiles lists the regular, non-hidden files under rel (run
// workspace relative) in lexical order. Symlinks are skipped.
func collectRunOutputFiles(ctx context.Context, rel string) ([]runFileRef, error) {
	runDir, err := runWorkspaceDir(ctx)
	if err != nil {
		return nil, err
	}
	root, name, err := service.OpenExecutionRoot(ctx, path.Join(runDir, rel), false)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	fsys, err := fs.Sub(root.FS(), filepath.ToSlash(name))
	if err != nil {
		return nil, err
	}

	var refs []runFileRef
	err = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if len(refs) >= maxRunOutputFiles {
			return fs.SkipAll
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		refs = append(refs, runFileRef{
			Path:          path.Join(rel, p),
			WorkspacePath: path.Join(runDir, rel, p),
			Name:          path.Base(p),
			ContentType:   attachmentContentType("", p, sniffHead(fsys, p)),
			SizeBytes:     info.Size(),
		})
		return nil
	})
	if err != nil {
		return refs, fmt.Errorf("list output files: %w", err)
	}
	return refs, nil
}

func sniffHead(fsys fs.FS, name string) []byte {
	f, err := fsys.Open(name)
	if err != nil {
		return nil
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	return head[:n]
}

// saveInlineImages stores base64 images returned by a model in dir as
// generated-image-NN.<ext>, numbering from start+1.
func saveInlineImages(ctx context.Context, dir string, start int, images []service.InlineImage) ([]runFileRef, error) {
	refs := make([]runFileRef, 0, len(images))
	for i, img := range images {
		data, err := decodeImageBase64(img.Data)
		if err != nil {
			return refs, fmt.Errorf("image %d: %w", start+i+1, err)
		}
		ref, err := writeGeneratedImage(ctx, dir, start+i+1, data, img.MimeType)
		if err != nil {
			return refs, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// writeGeneratedImage refuses anything that is not a known image type, so a
// provider cannot place arbitrary content under an image name.
func writeGeneratedImage(ctx context.Context, dir string, index int, data []byte, mimeType string) (runFileRef, error) {
	mimeType = strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	if _, ok := generatedImageExt[mimeType]; !ok {
		mimeType = strings.TrimSpace(strings.Split(http.DetectContentType(data), ";")[0])
	}
	ext, ok := generatedImageExt[mimeType]
	if !ok {
		return runFileRef{}, fmt.Errorf("image %d: unsupported type %q", index, mimeType)
	}
	name := path.Join(dir, fmt.Sprintf("generated-image-%02d%s", index, ext))
	ref, err := writeRunFile(ctx, name, bytes.NewReader(data), maxGeneratedImageBytes, mimeType)
	if err != nil {
		return runFileRef{}, fmt.Errorf("image %d: %w", index, err)
	}
	return ref, nil
}

// decodeImageBase64 accepts padded or unpadded standard base64 and data URIs.
func decodeImageBase64(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "data:") {
		if i := strings.Index(raw, ","); i >= 0 {
			raw = raw[i+1:]
		}
	}
	raw = strings.Join(strings.Fields(raw), "")
	if data, err := base64.StdEncoding.DecodeString(raw); err == nil {
		return data, nil
	}
	data, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(raw, "="))
	if err != nil {
		return nil, fmt.Errorf("invalid base64: %w", err)
	}
	return data, nil
}

// dataURIMimeType returns the media type of a data URI, or "".
func dataURIMimeType(raw string) string {
	if !strings.HasPrefix(raw, "data:") {
		return ""
	}
	meta, _, _ := strings.Cut(strings.TrimPrefix(raw, "data:"), ",")
	mediaType, _, _ := strings.Cut(meta, ";")
	return mediaType
}

func runFileRefsToAny(refs []runFileRef) []any {
	out := make([]any, len(refs))
	for i, ref := range refs {
		out[i] = ref.toMap()
	}
	return out
}
