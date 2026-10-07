package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/blob"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// A skill run started from Chats gets its own output directory, exposed to
// its tools as AT_WORK_DIR. Whatever the run leaves there — an image, a PDF,
// a spreadsheet — is copied into the caller's media storage and reported as
// an artifact, so the browser can show or download it. Without this the files
// stayed in the server's temporary directory, where nobody could reach them.

const (
	chatArtifactDirName = "chat-runs"
)

type chatMediaRefsContextKey struct{}

// contextWithChatMediaRefs marks a call made by the Chats page, which renders
// `media:<id>` Markdown references. Other surfaces (Sessions, bots, the
// gateway) do not, so artifacts carry no such snippet there.
func contextWithChatMediaRefs(ctx context.Context) context.Context {
	return context.WithValue(ctx, chatMediaRefsContextKey{}, true)
}

func chatMediaRefsFromContext(ctx context.Context) bool {
	on, _ := ctx.Value(chatMediaRefsContextKey{}).(bool)
	return on
}

// chatArtifact is what the browser receives for one produced file.
type chatArtifact struct {
	MediaID     string `json:"media_id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	// Markdown is set for Chats callers only: a `media:` reference the chat
	// page resolves, so the model can place the file in its answer without
	// knowing (or inventing) a URL.
	Markdown string `json:"markdown,omitempty"`

	// downloadKey is the plaintext gateway download key, only ever placed in
	// the download_url returned to the producing call.
	downloadKey     string
	downloadExpires time.Time
	// relPath is the file's path inside the run directory, used to rewrite
	// server paths the run mentioned in its answer.
	relPath string
}

// chatArtifactCollection is the outcome of one sweep. Note explains files that
// could not be delivered, so a missing result is never silent.
type chatArtifactCollection struct {
	Artifacts []chatArtifact `json:"artifacts,omitempty"`
	Note      string         `json:"artifacts_note,omitempty"`

	// dir is the run directory the artifacts were collected from.
	dir string
}

// rewriteText replaces server paths of delivered files in a run's answer.
// The run only knows where it wrote a file; the user cannot open that path,
// and a model repeating it shows a broken location. A Markdown target becomes
// the file's `media:` reference (when the caller renders those), any other
// mention becomes the file name.
func (c chatArtifactCollection) rewriteText(text string) string {
	if c.dir == "" || text == "" || len(c.Artifacts) == 0 {
		return text
	}
	rel := path.Join(chatArtifactDirName, filepath.Base(c.dir))
	absDir := filepath.ToSlash(c.dir)
	// Longest prefixes first: the relative directory is a suffix of the
	// absolute one, so replacing it first would leave a mangled absolute path.
	for _, prefix := range []string{"file://" + absDir, absDir, rel} {
		for _, a := range c.Artifacts {
			if a.relPath == "" {
				continue
			}
			full := prefix + "/" + a.relPath
			if !strings.Contains(text, full) {
				continue
			}
			if a.Markdown != "" {
				target := regexp.MustCompile(`\]\(\s*<?` + regexp.QuoteMeta(full) + `>?\s*\)`)
				text = target.ReplaceAllString(text, "](media:"+a.MediaID+")")
			}
			text = strings.ReplaceAll(text, full, a.Name)
		}
	}
	return text
}

// mediaInlineContentTypes may render in the browser. Everything else is served
// as a download: HTML, SVG and other active formats must never run on the
// application origin, whatever produced them.
func mediaInlineContentType(contentType string) bool {
	if _, ok := mediaAllowedContentTypes[contentType]; ok {
		return true
	}
	switch contentType {
	case "application/pdf",
		"audio/mpeg", "audio/wave", "audio/wav", "audio/ogg", "audio/aac", "audio/mp4", "audio/webm", "audio/flac",
		"video/mp4", "video/webm", "video/ogg":
		return true
	}
	return false
}

var artifactExtensionPattern = regexp.MustCompile(`^\.[A-Za-z0-9]{1,10}$`)

// artifactContentType decides from the bytes. The file name only refines a
// generic sniff (plain text, zip, unknown binary) and can never promote a file
// into a type the browser renders inline.
func artifactContentType(name string, data []byte) string {
	sniffed := strings.TrimSpace(strings.Split(http.DetectContentType(data), ";")[0])
	switch sniffed {
	case "application/octet-stream", "text/plain", "application/zip":
		if byExt := strings.TrimSpace(strings.Split(mime.TypeByExtension(strings.ToLower(filepath.Ext(name))), ";")[0]); byExt != "" && !mediaInlineContentType(byExt) {
			return byExt
		}
	}
	return sniffed
}

// chatRunWorkDir creates the per-run output directory inside the caller's
// execution root and returns its absolute and root-relative paths.
func chatRunWorkDir(ctx context.Context) (string, string, error) {
	_, base, ok := service.ExecutionFromContext(ctx)
	if !ok || base == "" {
		return "", "", service.ErrExecutionDenied
	}
	rel := path.Join(chatArtifactDirName, ulid.Make().String())
	root, err := os.OpenRoot(base)
	if err != nil {
		return "", "", fmt.Errorf("open execution root: %w", err)
	}
	defer root.Close()
	if err := root.MkdirAll(rel, 0o700); err != nil {
		return "", "", fmt.Errorf("create run directory: %w", err)
	}
	return filepath.Join(base, filepath.FromSlash(rel)), rel, nil
}

// withChatRunWorkDir points the run's tools at dir and tells the agent where
// its deliverables belong.
func withChatRunWorkDir(ctx context.Context, task, dir, rel string) (context.Context, string) {
	ctx = workflow.ContextWithWorkDir(ctx, dir)
	ctx = contextWithChatInlineImageSink(ctx, newChatInlineImageSink(dir))
	task += fmt.Sprintf("\n\nSave every file you produce for the user (images, PDFs, documents, audio, …) in this directory: %s (relative to the workspace root: %s). Files saved there are delivered to the user automatically when you finish. In your answer refer to them by file name only: this directory is on the server and the user cannot open it, so never show its path or a file path inside it.", dir, rel)
	return ctx, task
}

type chatInlineImageSink func([]service.InlineImage) ([]string, error)
type chatInlineImageSinkContextKey struct{}

func contextWithChatInlineImageSink(ctx context.Context, sink chatInlineImageSink) context.Context {
	return context.WithValue(ctx, chatInlineImageSinkContextKey{}, sink)
}

func saveChatInlineImages(ctx context.Context, images []service.InlineImage) ([]string, error) {
	sink, _ := ctx.Value(chatInlineImageSinkContextKey{}).(chatInlineImageSink)
	if sink == nil || len(images) == 0 {
		return nil, nil
	}
	return sink(images)
}

// newChatInlineImageSink turns images returned directly by a multimodal chat
// model into ordinary run files. They then travel through the same storage,
// ownership and transcript path as files written by tools in AT_WORK_DIR.
func newChatInlineImageSink(dir string) chatInlineImageSink {
	var mu sync.Mutex
	sequence := 0
	return func(images []service.InlineImage) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()

		root, err := os.OpenRoot(dir)
		if err != nil {
			return nil, fmt.Errorf("open run directory: %w", err)
		}
		defer root.Close()

		var names []string
		var failures []string
		for _, image := range images {
			mimeType := strings.ToLower(strings.TrimSpace(strings.Split(image.MimeType, ";")[0]))
			ext, ok := mediaAllowedContentTypes[mimeType]
			if !ok {
				failures = append(failures, fmt.Sprintf("unsupported image type %q", image.MimeType))
				continue
			}
			sequence++
			name := fmt.Sprintf("generated-image-%02d%s", sequence, ext)
			file, openErr := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if openErr != nil {
				failures = append(failures, fmt.Sprintf("save %s: %v", name, openErr))
				continue
			}
			_, decodeErr := io.Copy(file, base64.NewDecoder(base64.StdEncoding, strings.NewReader(image.Data)))
			closeErr := file.Close()
			if decodeErr != nil || closeErr != nil {
				_ = root.Remove(name)
				if decodeErr != nil {
					failures = append(failures, "generated image contains invalid base64 data")
				} else {
					failures = append(failures, fmt.Sprintf("save %s: %v", name, closeErr))
				}
				continue
			}
			if info, statErr := root.Stat(name); statErr != nil || info.Size() == 0 {
				_ = root.Remove(name)
				failures = append(failures, fmt.Sprintf("save %s: generated image is empty", name))
				continue
			}
			names = append(names, name)
		}
		if len(failures) > 0 {
			return names, fmt.Errorf("%s", strings.Join(failures, "; "))
		}
		return names, nil
	}
}

// collectChatArtifacts stores every regular file under dir as a media object
// owned by the run's user, then removes the directory when all of them were
// delivered. It never fails the run: problems are reported in Note.
func (s *Server) collectChatArtifacts(ctx context.Context, dir string) chatArtifactCollection {
	var out chatArtifactCollection
	root, err := os.OpenRoot(dir)
	if err != nil {
		return out
	}
	defer root.Close()

	type found = pendingArtifact
	var files []found
	walkErr := fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		base := path.Base(name)
		if name != "." && strings.HasPrefix(base, ".") {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		// Symlinks could point anywhere; only files the run actually wrote count.
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil
		}
		if info.Size() == 0 {
			return nil
		}
		files = append(files, found{name: name})
		return nil
	})
	if walkErr != nil {
		slog.Warn("chat artifacts: scan failed", "dir", dir, "error", walkErr)
	}
	if len(files) == 0 {
		_ = os.RemoveAll(dir)
		return out
	}

	delivered, note := s.storeChatArtifacts(ctx, root, files)
	out.dir = dir
	out.Artifacts = delivered
	out.Note = note
	if len(delivered) == len(files) {
		_ = os.RemoveAll(dir)
	} else if out.Note != "" {
		out.Note += " The files remain on the server in " + dir + "."
	}
	return out
}

type pendingArtifact struct {
	name string
}

func (s *Server) storeChatArtifacts(ctx context.Context, root *os.Root, files []pendingArtifact) ([]chatArtifact, string) {
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.name)
	}
	provenance, _, ok := service.ExecutionFromContext(ctx)
	store, storeOK := s.store.(service.MediaStorer)
	if !ok || provenance.UserID == "" || provenance.WorkspaceID == "" || !storeOK {
		return nil, "Produced files could not be delivered because media storage is unavailable: " + strings.Join(names, ", ") + "."
	}
	settings, err := store.GetMediaSettings(ctx)
	if err != nil || settings == nil || !settings.Enabled() {
		return nil, "Produced files could not be delivered because media storage is disabled; an administrator can enable it in storage settings: " + strings.Join(names, ", ") + "."
	}
	target, err := blob.New(*settings)
	if err != nil {
		slog.Error("chat artifacts: storage misconfigured", "backend", settings.Backend, "error", err)
		return nil, "Produced files could not be delivered because media storage is misconfigured: " + strings.Join(names, ", ") + "."
	}

	var delivered []chatArtifact
	var failed []string
	for _, f := range files {
		file, openErr := root.Open(f.name)
		if openErr != nil {
			failed = append(failed, f.name)
			continue
		}
		head := make([]byte, 512)
		headN, readErr := io.ReadFull(file, head)
		if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			file.Close()
			failed = append(failed, f.name)
			continue
		}
		hash := sha256.New()
		_, _ = hash.Write(head[:headN])
		if _, readErr = io.Copy(hash, file); readErr != nil {
			file.Close()
			failed = append(failed, f.name)
			continue
		}
		info, statErr := file.Stat()
		if statErr != nil || info.Size() == 0 {
			file.Close()
			failed = append(failed, f.name)
			continue
		}
		if _, seekErr := file.Seek(0, io.SeekStart); seekErr != nil {
			file.Close()
			failed = append(failed, f.name)
			continue
		}
		contentType := artifactContentType(f.name, head[:headN])
		ext := strings.ToLower(filepath.Ext(f.name))
		if !artifactExtensionPattern.MatchString(ext) {
			ext = mediaAllowedContentTypes[contentType]
		}
		key := mediaStorageKey(*settings, provenance.WorkspaceID, provenance.UserID, ext)
		checksum := hex.EncodeToString(hash.Sum(nil))
		if err := target.PutReader(ctx, key, contentType, file, info.Size(), checksum); err != nil {
			file.Close()
			slog.Error("chat artifacts: upload failed", "key", key, "error", err)
			failed = append(failed, f.name)
			continue
		}
		file.Close()
		tokenID := ""
		var downloadKey, downloadKeyHash string
		var downloadExpires time.Time
		if token := gatewayTokenFromContext(ctx); token != nil && token.WorkspaceID == provenance.WorkspaceID {
			tokenID = token.ID
			downloadKey, downloadKeyHash = newGatewayMediaKey()
			downloadExpires = time.Now().Add(gatewayMediaTTL(ctx))
		}
		created, err := store.CreateMediaObject(ctx, service.MediaObject{
			WorkspaceID:       provenance.WorkspaceID,
			OwnerUserID:       provenance.UserID,
			TokenID:           tokenID,
			DownloadKeyHash:   downloadKeyHash,
			DownloadExpiresAt: downloadExpires,
			Backend:           settings.Backend,
			StorageKey:        key,
			ContentType:       contentType,
			SizeBytes:         info.Size(),
			Checksum:          checksum,
		})
		if err != nil {
			if deleteErr := target.Delete(ctx, key); deleteErr != nil && !errors.Is(deleteErr, os.ErrNotExist) {
				slog.Error("chat artifacts: orphaned blob", "key", key, "error", deleteErr)
			}
			failed = append(failed, f.name)
			continue
		}
		artifact := chatArtifact{MediaID: created.ID, Name: path.Base(f.name), ContentType: contentType, SizeBytes: created.SizeBytes, downloadKey: downloadKey, downloadExpires: downloadExpires, relPath: f.name}
		if chatMediaRefsFromContext(ctx) {
			_, image := mediaAllowedContentTypes[contentType]
			artifact.Markdown = service.MediaRefMarkdown(created.ID, artifact.Name, image)
		}
		delivered = append(delivered, artifact)
	}
	if len(failed) > 0 {
		return delivered, "Some produced files could not be stored: " + strings.Join(failed, ", ") + "."
	}
	return delivered, ""
}
