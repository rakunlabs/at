package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

type fileEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

func fileAccessError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrExecutionDenied):
		http.Error(w, "file access denied", http.StatusForbidden)
	case errors.Is(err, os.ErrNotExist):
		http.Error(w, "path not found", http.StatusNotFound)
	default:
		http.Error(w, "file operation failed", http.StatusBadRequest)
	}
}

func (s *Server) FileBrowseAPI(w http.ResponseWriter, r *http.Request) {
	var ok bool
	if r, ok = s.runtimeRequest(w, r); !ok {
		return
	}
	root, name, err := service.OpenExecutionRoot(r.Context(), r.URL.Query().Get("path"), false)
	if err != nil {
		fileAccessError(w, err)
		return
	}
	defer root.Close()
	dir, err := service.OpenExecutionFile(root, name, os.O_RDONLY, 0)
	if err != nil {
		fileAccessError(w, err)
		return
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		fileAccessError(w, err)
		return
	}
	files := make([]fileEntry, 0, len(entries))
	for _, e := range entries {
		// Do not leak metadata from links or non-regular host devices.
		if e.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(name, e.Name())
		child, err := service.OpenExecutionFile(root, path, os.O_RDONLY, 0)
		if err != nil {
			continue
		}
		info, err := child.Stat()
		child.Close()
		if err != nil || (!info.Mode().IsRegular() && !info.IsDir()) {
			continue
		}
		if service.CheckExecution(r.Context(), service.ExecutionAction{Kind: "file", Name: "files.read", Path: path}) != nil {
			continue
		}
		files = append(files, fileEntry{e.Name(), path, info.IsDir(), info.Size(), info.ModTime().Format("2006-01-02 15:04:05")})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].IsDir != files[j].IsDir {
			return files[i].IsDir
		}
		return files[i].Name < files[j].Name
	})
	httpResponseJSON(w, map[string]any{"path": name, "parent": filepath.Dir(name), "entries": files}, http.StatusOK)
}

// Authorization runs before ServeContent for every request, including Range,
// HEAD and conditional cache requests. Open and Stat use the same rooted handle.
func (s *Server) FileServeAPI(w http.ResponseWriter, r *http.Request) {
	var ok bool
	if r, ok = s.runtimeRequest(w, r); !ok {
		return
	}
	root, name, err := service.OpenExecutionRoot(r.Context(), r.URL.Query().Get("path"), false)
	if err != nil {
		fileAccessError(w, err)
		return
	}
	defer root.Close()
	f, err := service.OpenExecutionFile(root, name, os.O_RDONLY, 0)
	if err != nil {
		fileAccessError(w, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		fileAccessError(w, err)
		return
	}
	if !info.Mode().IsRegular() {
		http.Error(w, "path is not a regular file", http.StatusBadRequest)
		return
	}
	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	// Untrusted active documents must not run scripts on the admin origin.
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", filepath.Base(name)))
	http.ServeContent(w, r, filepath.Base(name), info.ModTime(), f)
}

func (s *Server) FileDeleteAPI(w http.ResponseWriter, r *http.Request) {
	var ok bool
	if r, ok = s.runtimeRequest(w, r); !ok {
		return
	}
	raw := r.URL.Query().Get("path")
	if raw == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}
	root, name, err := service.OpenExecutionRoot(r.Context(), raw, true)
	if err != nil {
		fileAccessError(w, err)
		return
	}
	defer root.Close()
	if name == "." {
		http.Error(w, "cannot delete workspace root", http.StatusForbidden)
		return
	}
	// Remove only files/empty directories. Recursive deletion could exceed a
	// path-pattern grant covering a directory but not its descendants.
	if err := service.RemoveExecutionFile(root, name); err != nil {
		fileAccessError(w, err)
		return
	}
	httpResponseJSON(w, map[string]any{"deleted": name}, http.StatusOK)
}

func (s *Server) FileUploadAPI(w http.ResponseWriter, r *http.Request) {
	var ok bool
	if r, ok = s.runtimeRequest(w, r); !ok {
		return
	}
	if err := service.CheckExecution(r.Context(), service.ExecutionAction{Kind: "resource", Name: "execution.run"}); err != nil {
		fileAccessError(w, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "invalid multipart form", http.StatusBadRequest)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file field is required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	name := r.FormValue("name")
	if name == "" {
		name = header.Filename
	}
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}
	dir := r.FormValue("path")
	for _, part := range strings.Split(filepath.ToSlash(dir), "/") {
		if part == ".." {
			http.Error(w, "invalid upload path", http.StatusForbidden)
			return
		}
	}
	if dir == "" {
		dir = "assets/uploads"
	}
	root, path, err := service.OpenExecutionRoot(r.Context(), filepath.Join(dir, name), true)
	if err != nil {
		fileAccessError(w, err)
		return
	}
	defer root.Close()
	if err := service.MkdirExecutionAll(root, filepath.Dir(path), 0700); err != nil {
		fileAccessError(w, err)
		return
	}
	n, err := service.ReplaceExecutionFile(root, path, file)
	if err != nil {
		http.Error(w, "upload failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{"path": path, "name": name, "size": n})
}
