package server

import (
	"context"
	"crypto/sha256"
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
	chatArtifactMaxFiles      = 20
	chatArtifactMaxTotalBytes = 64 << 20
	chatArtifactDirName       = "chat-runs"
)

// chatArtifact is what the browser receives for one produced file.
type chatArtifact struct {
	MediaID     string `json:"media_id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

// chatArtifactCollection is the outcome of one sweep. Note explains files that
// could not be delivered, so a missing result is never silent.
type chatArtifactCollection struct {
	Artifacts []chatArtifact `json:"artifacts,omitempty"`
	Note      string         `json:"artifacts_note,omitempty"`
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
	task += fmt.Sprintf("\n\nSave every file you produce for the user (images, PDFs, documents, audio, …) in this directory: %s (relative to the workspace root: %s). Files saved there are delivered to the user automatically; mention them by file name in your answer.", dir, rel)
	return ctx, task
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
	var skipped []string
	var total int64
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
		if len(files) >= chatArtifactMaxFiles {
			skipped = append(skipped, name+" (file limit)")
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil
		}
		if info.Size() == 0 {
			return nil
		}
		if info.Size() > mediaUploadMaxBytes || total+info.Size() > chatArtifactMaxTotalBytes {
			skipped = append(skipped, name+" (too large)")
			return nil
		}
		f, openErr := root.Open(name)
		if openErr != nil {
			return nil
		}
		data, readErr := io.ReadAll(io.LimitReader(f, mediaUploadMaxBytes+1))
		f.Close()
		if readErr != nil || int64(len(data)) > mediaUploadMaxBytes {
			skipped = append(skipped, name+" (unreadable)")
			return nil
		}
		total += int64(len(data))
		files = append(files, found{name: name, data: data})
		return nil
	})
	if walkErr != nil {
		slog.Warn("chat artifacts: scan failed", "dir", dir, "error", walkErr)
	}
	if len(files) == 0 {
		if len(skipped) > 0 {
			out.Note = "Some produced files were not delivered: " + strings.Join(skipped, ", ")
		}
		_ = os.RemoveAll(dir)
		return out
	}

	delivered, note := s.storeChatArtifacts(ctx, files)
	out.Artifacts = delivered
	if len(skipped) > 0 {
		note = strings.TrimSpace(note + " Not delivered: " + strings.Join(skipped, ", ") + ".")
	}
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
	data []byte
}

func (s *Server) storeChatArtifacts(ctx context.Context, files []pendingArtifact) ([]chatArtifact, string) {
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
		contentType := artifactContentType(f.name, f.data)
		ext := strings.ToLower(filepath.Ext(f.name))
		if !artifactExtensionPattern.MatchString(ext) {
			ext = mediaAllowedContentTypes[contentType]
		}
		key := mediaStorageKey(*settings, provenance.WorkspaceID, provenance.UserID, ext)
		if err := target.Put(ctx, key, contentType, f.data); err != nil {
			slog.Error("chat artifacts: upload failed", "key", key, "error", err)
			failed = append(failed, f.name)
			continue
		}
		sum := sha256.Sum256(f.data)
		created, err := store.CreateMediaObject(ctx, service.MediaObject{
			WorkspaceID: provenance.WorkspaceID,
			OwnerUserID: provenance.UserID,
			Backend:     settings.Backend,
			StorageKey:  key,
			ContentType: contentType,
			SizeBytes:   int64(len(f.data)),
			Checksum:    hex.EncodeToString(sum[:]),
		})
		if err != nil {
			if deleteErr := target.Delete(ctx, key); deleteErr != nil && !errors.Is(deleteErr, os.ErrNotExist) {
				slog.Error("chat artifacts: orphaned blob", "key", key, "error", deleteErr)
			}
			failed = append(failed, f.name)
			continue
		}
		delivered = append(delivered, chatArtifact{MediaID: created.ID, Name: path.Base(f.name), ContentType: contentType, SizeBytes: created.SizeBytes})
	}
	if len(failed) > 0 {
		return delivered, "Some produced files could not be stored: " + strings.Join(failed, ", ") + "."
	}
	return delivered, ""
}
