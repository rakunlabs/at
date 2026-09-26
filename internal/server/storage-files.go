package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/blob"
)

const storageFileMaxBytes = 64 << 20

func normalizeStorageFilePath(raw string, rootOK bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "." {
		if rootOK {
			return "", nil
		}
		return "", errors.New("path is required")
	}
	if strings.HasPrefix(raw, "/") || strings.ContainsRune(raw, '\\') {
		return "", errors.New("path must be relative")
	}
	for _, part := range strings.Split(raw, "/") {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("path contains an invalid segment")
		}
	}
	clean := path.Clean(raw)
	if clean == "." || strings.HasPrefix(clean, "../") {
		return "", errors.New("path must stay inside storage")
	}
	return clean, nil
}

func (s *Server) durableStorage(w http.ResponseWriter, r *http.Request, action, logicalPath string) (service.StorageObjectStorer, blob.Store, service.StorageSettings, service.ExecutionProvenance, bool) {
	if err := service.CheckExecution(r.Context(), service.ExecutionAction{Kind: "file", Name: action, Path: logicalPath}); err != nil {
		fileAccessError(w, err)
		return nil, nil, service.StorageSettings{}, service.ExecutionProvenance{}, false
	}
	principal, _, ok := service.ExecutionFromContext(r.Context())
	if !ok || principal.WorkspaceID == "" {
		http.Error(w, "storage requires a selected workspace", http.StatusForbidden)
		return nil, nil, service.StorageSettings{}, principal, false
	}
	objects, ok := s.store.(service.StorageObjectStorer)
	if !ok {
		http.Error(w, "storage object catalog unavailable", http.StatusServiceUnavailable)
		return nil, nil, service.StorageSettings{}, principal, false
	}
	settingsStore, ok := s.store.(service.StorageSettingsStorer)
	if !ok {
		http.Error(w, "storage settings unavailable", http.StatusServiceUnavailable)
		return nil, nil, service.StorageSettings{}, principal, false
	}
	settings, err := settingsStore.GetStorageSettings(r.Context())
	if err != nil || !settings.Enabled() {
		http.Error(w, "storage is disabled", http.StatusServiceUnavailable)
		return nil, nil, service.StorageSettings{}, principal, false
	}
	target, err := blob.New(*settings)
	if err != nil {
		http.Error(w, "storage is misconfigured", http.StatusServiceUnavailable)
		return nil, nil, service.StorageSettings{}, principal, false
	}
	return objects, target, *settings, principal, true
}

// StorageFileBrowseAPI lists virtual directories from durable object metadata.
func (s *Server) StorageFileBrowseAPI(w http.ResponseWriter, r *http.Request) {
	dir, err := normalizeStorageFilePath(r.URL.Query().Get("path"), true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	objects, _, _, principal, ok := s.durableStorage(w, r, "files.read", dir)
	if !ok {
		return
	}
	prefix := dir
	if prefix != "" {
		prefix += "/"
	}
	stored, err := objects.ListStorageObjects(r.Context(), principal.WorkspaceID, service.StorageNamespaceFiles, prefix)
	if err != nil {
		http.Error(w, "could not list storage files", http.StatusInternalServerError)
		return
	}
	entries := make(map[string]fileEntry)
	for _, object := range stored {
		remainder := strings.TrimPrefix(object.Path, prefix)
		name, rest, _ := strings.Cut(remainder, "/")
		entryPath := name
		if dir != "" {
			entryPath = path.Join(dir, name)
		}
		if rest != "" {
			entries[name] = fileEntry{Name: name, Path: entryPath, IsDir: true, ModTime: object.UpdatedAt}
			continue
		}
		entries[name] = fileEntry{Name: name, Path: object.Path, Size: object.SizeBytes, ModTime: object.UpdatedAt}
	}
	list := make([]fileEntry, 0, len(entries))
	for _, entry := range entries {
		list = append(list, entry)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].IsDir != list[j].IsDir {
			return list[i].IsDir
		}
		return list[i].Name < list[j].Name
	})
	parent := "."
	if dir != "" {
		parent = path.Dir(dir)
	}
	shown := dir
	if shown == "" {
		shown = "."
	}
	httpResponseJSON(w, map[string]any{"path": shown, "parent": parent, "entries": list}, http.StatusOK)
}

func (s *Server) StorageFileUploadAPI(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, storageFileMaxBytes+(64<<10))
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		http.Error(w, "invalid multipart form or file exceeds 64 MiB", http.StatusBadRequest)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll() //nolint:errcheck
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file field is required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = header.Filename
	}
	dir, err := normalizeStorageFilePath(r.FormValue("path"), true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	logicalPath, err := normalizeStorageFilePath(path.Join(dir, name), false)
	if err != nil || path.Base(logicalPath) != name {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}
	objects, target, settings, principal, ok := s.durableStorage(w, r, "files.write", logicalPath)
	if !ok {
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, storageFileMaxBytes+1))
	if err != nil || len(data) == 0 {
		http.Error(w, "could not read uploaded file", http.StatusBadRequest)
		return
	}
	if len(data) > storageFileMaxBytes {
		http.Error(w, "file exceeds 64 MiB", http.StatusRequestEntityTooLarge)
		return
	}
	contentType := strings.TrimSpace(strings.Split(http.DetectContentType(data), ";")[0])
	key := "workspaces/" + principal.WorkspaceID + "/files/" + logicalPath
	if settings.Backend == service.StorageBackendS3 {
		key = service.NormalizeStoragePrefix(settings.S3.Prefix) + key
	}
	if err := target.Put(r.Context(), key, contentType, data); err != nil {
		slog.Error("storage file upload failed", "key", key, "error", err.Error())
		http.Error(w, "storage rejected the upload", http.StatusBadGateway)
		return
	}
	sum := sha256.Sum256(data)
	stored, err := objects.PutStorageObject(r.Context(), service.StoredObject{
		WorkspaceID: principal.WorkspaceID, OwnerUserID: principal.UserID,
		Namespace: service.StorageNamespaceFiles, Path: logicalPath,
		Backend: settings.Backend, StorageKey: key, ContentType: contentType,
		SizeBytes: int64(len(data)), Checksum: hex.EncodeToString(sum[:]),
	})
	if err != nil {
		http.Error(w, "could not record uploaded file", http.StatusInternalServerError)
		return
	}
	httpResponseJSON(w, map[string]any{"id": stored.ID, "path": stored.Path, "name": path.Base(stored.Path), "size": stored.SizeBytes}, http.StatusOK)
}

func (s *Server) StorageFileServeAPI(w http.ResponseWriter, r *http.Request) {
	logicalPath, err := normalizeStorageFilePath(r.URL.Query().Get("path"), false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	objects, target, settings, principal, ok := s.durableStorage(w, r, "files.read", logicalPath)
	if !ok {
		return
	}
	object, err := objects.GetStorageObject(r.Context(), principal.WorkspaceID, service.StorageNamespaceFiles, logicalPath)
	if errors.Is(err, service.ErrStorageObjectNotFound) {
		http.Error(w, "path not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "could not read storage metadata", http.StatusInternalServerError)
		return
	}
	if object.Backend != settings.Backend {
		http.Error(w, "file belongs to a different storage backend", http.StatusConflict)
		return
	}
	reader, _, err := target.Get(r.Context(), object.StorageKey)
	if err != nil {
		http.Error(w, "storage could not return the file", http.StatusBadGateway)
		return
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, storageFileMaxBytes+1))
	if err != nil || int64(len(data)) != object.SizeBytes {
		http.Error(w, "stored file is incomplete", http.StatusBadGateway)
		return
	}
	contentType := object.ContentType
	if contentType == "" {
		contentType = mime.TypeByExtension(path.Ext(logicalPath))
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", path.Base(logicalPath)))
	w.Header().Set("Cache-Control", "private, no-store")
	updated, _ := time.Parse(time.RFC3339, object.UpdatedAt)
	http.ServeContent(w, r, path.Base(logicalPath), updated, bytes.NewReader(data))
}

func (s *Server) StorageFileDeleteAPI(w http.ResponseWriter, r *http.Request) {
	logicalPath, err := normalizeStorageFilePath(r.URL.Query().Get("path"), false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	objects, target, settings, principal, ok := s.durableStorage(w, r, "files.write", logicalPath)
	if !ok {
		return
	}
	object, err := objects.DeleteStorageObject(r.Context(), principal.WorkspaceID, service.StorageNamespaceFiles, logicalPath)
	if errors.Is(err, service.ErrStorageObjectNotFound) {
		children, listErr := objects.ListStorageObjects(r.Context(), principal.WorkspaceID, service.StorageNamespaceFiles, logicalPath+"/")
		if listErr == nil && len(children) > 0 {
			http.Error(w, "directory is not empty", http.StatusConflict)
			return
		}
		http.Error(w, "path not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "could not delete storage file", http.StatusInternalServerError)
		return
	}
	if object.Backend == settings.Backend {
		if err := target.Delete(r.Context(), object.StorageKey); err != nil {
			slog.Error("storage file blob deletion failed", "key", object.StorageKey, "error", err.Error())
		}
	}
	httpResponseJSON(w, map[string]any{"deleted": logicalPath}, http.StatusOK)
}
