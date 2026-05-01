package server

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
)

type fileEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

// File browse / serve / delete handlers operate on the daemon's filesystem
// directly with no allow-list. Earlier versions restricted reads to a small
// set of /tmp/at-* roots; that was removed at the operator's request so the
// UI can navigate the full host filesystem (debugging, log inspection,
// arbitrary workspace browsing).
//
// Safety still in place:
//   - Paths are cleaned with filepath.Abs+Clean before use, so traversal
//     segments are normalised away.
//   - Symlinks are resolved with EvalSymlinks where possible; the resolved
//     path is what we open / stat / delete.
//   - Delete refuses to remove the filesystem root ("/").
//
// All file I/O is still bounded by the daemon's UID — running this as an
// unprivileged user remains the operator's responsibility.

// resolvePath cleans the requested path and resolves symlinks. Returns the
// canonical absolute path and stat info, or writes an http.Error and
// returns ok=false. requireDir flips the not-a-dir / is-a-dir error.
func resolvePath(w http.ResponseWriter, raw string, requireDir bool) (string, os.FileInfo, bool) {
	if raw == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return "", nil, false
	}

	// 1) Make absolute and clean. filepath.Clean removes "." and ".." segments.
	cleaned, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return "", nil, false
	}

	// 2) Resolve symlinks where possible. EvalSymlinks fails on non-existent
	// paths, in which case we fall through to os.Stat for a clean 404.
	resolved := cleaned
	if real, err := filepath.EvalSymlinks(cleaned); err == nil {
		resolved = real
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "path not found", http.StatusNotFound)
		} else {
			http.Error(w, fmt.Sprintf("stat failed: %v", err), http.StatusInternalServerError)
		}
		return "", nil, false
	}

	if requireDir && !info.IsDir() {
		http.Error(w, "path is not a directory", http.StatusBadRequest)
		return "", nil, false
	}
	if !requireDir && info.IsDir() {
		http.Error(w, "path is a directory", http.StatusBadRequest)
		return "", nil, false
	}

	return resolved, info, true
}

// FileBrowseAPI lists the contents of a directory.
// GET /api/v1/files/browse?path=/tmp/at-tasks
func (s *Server) FileBrowseAPI(w http.ResponseWriter, r *http.Request) {
	dirPath := r.URL.Query().Get("path")
	if dirPath == "" {
		// Default to the task-workspace root since that's the most common
		// browse target. The user can navigate elsewhere from there.
		dirPath = "/tmp/at-tasks"
	}

	resolved, _, ok := resolvePath(w, dirPath, true)
	if !ok {
		return
	}

	entries, err := os.ReadDir(resolved)
	if err != nil {
		http.Error(w, fmt.Sprintf("cannot read directory: %v", err), http.StatusInternalServerError)
		return
	}

	files := make([]fileEntry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}

		files = append(files, fileEntry{
			Name:    e.Name(),
			Path:    filepath.Join(resolved, e.Name()),
			IsDir:   e.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().Format("2006-01-02 15:04:05"),
		})
	}

	// Sort: directories first, then by name.
	sort.Slice(files, func(i, j int) bool {
		if files[i].IsDir != files[j].IsDir {
			return files[i].IsDir
		}
		return files[i].Name < files[j].Name
	})

	parent := filepath.Dir(resolved)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"path":    resolved,
		"parent":  parent,
		"entries": files,
	})
}

// FileServeAPI serves a file for viewing/downloading.
//
// Uses http.ServeContent so HTML5 <video> / <audio> can scrub via Range
// requests (responds with 206 Partial Content + Accept-Ranges: bytes).
// Without this, clicking the timeline in the Files preview pane is a
// no-op because the browser can't request a sub-range from byte 0.
//
// GET /api/v1/files/serve?path=/tmp/at-tasks/<id>/video.mp4
func (s *Server) FileServeAPI(w http.ResponseWriter, r *http.Request) {
	resolved, info, ok := resolvePath(w, r.URL.Query().Get("path"), false)
	if !ok {
		return
	}

	f, err := os.Open(resolved)
	if err != nil {
		http.Error(w, "cannot open file", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	// Set Content-Type explicitly from the extension. http.ServeContent
	// will fall back to sniffing the first 512 bytes when this is unset,
	// which sometimes mis-classifies legitimate video/audio as
	// application/octet-stream — and that prevents the browser from
	// using its native <video> / <audio> player.
	if ct := mime.TypeByExtension(filepath.Ext(resolved)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("inline; filename=%q", filepath.Base(resolved)))

	// ServeContent writes Accept-Ranges, handles Range requests with 206,
	// sets Last-Modified and Content-Length, and honours
	// If-Modified-Since / If-Range. The seekable *os.File we just opened
	// is exactly the io.ReadSeeker it expects.
	http.ServeContent(w, r, filepath.Base(resolved), info.ModTime(), f)
}

// FileDeleteAPI deletes a file or directory.
// DELETE /api/v1/files?path=/tmp/at-tasks/<id>/scratch.png
func (s *Server) FileDeleteAPI(w http.ResponseWriter, r *http.Request) {
	rawPath := r.URL.Query().Get("path")
	if rawPath == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	cleaned, err := filepath.Abs(filepath.Clean(rawPath))
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	// Resolve symlinks before deletion so we don't follow a malicious
	// link to delete an unintended target.
	if real, err := filepath.EvalSymlinks(cleaned); err == nil {
		cleaned = real
	}

	// Refuse to delete the filesystem root. Everything else is fair game
	// for the daemon's UID.
	if cleaned == "/" || cleaned == "." {
		http.Error(w, "cannot delete filesystem root", http.StatusForbidden)
		return
	}

	info, err := os.Stat(cleaned)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "path not found", http.StatusNotFound)
		} else {
			http.Error(w, fmt.Sprintf("stat failed: %v", err), http.StatusInternalServerError)
		}
		return
	}

	if info.IsDir() {
		err = os.RemoveAll(cleaned)
	} else {
		err = os.Remove(cleaned)
	}

	if err != nil {
		http.Error(w, fmt.Sprintf("delete failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"deleted": cleaned})
}
