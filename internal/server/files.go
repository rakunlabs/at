package server

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type fileEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

// allowedFileBrowseRoots restricts the file browser / server / delete handlers
// to a small set of well-known agent workspace directories.
//
// Without this, the handlers will happily serve any path the daemon's UID can
// read (e.g. /etc/passwd, ~/.aws/credentials, the binary itself). This is
// fine on a developer laptop with the UI on localhost, but the server is
// designed to be exposed behind reverse proxies, and the file browser must
// not become an arbitrary-file-read primitive.
//
// Each path is symlink-resolved (EvalSymlinks) at startup if possible; the
// fallback uses Clean() so we still have a baseline. A request is allowed
// only if the resolved request path is the root itself OR a descendant
// (HasPrefix on the cleaned path with a trailing separator to prevent the
// classic /tmp/at-tasks-evil bypass).
//
// The list mirrors the constants spread across the codebase:
//   - /tmp/at-tasks      — task workspaces (org-delegation.go)
//   - /tmp/at-sandbox    — exec node sandbox root (nodes/exec.go)
//   - /tmp/at-audio      — TTS staging (audio.go)
//   - /tmp/at-git-cache  — RAG / git-fetch cache (rag/sync.go, gateway-rag-mcp.go)
//   - /tmp/at-org-*      — per-org container workspaces (container/manager.go)
//     handled separately because the suffix is dynamic.
var allowedFileBrowseRoots = []string{
	"/tmp/at-tasks",
	"/tmp/at-sandbox",
	"/tmp/at-audio",
	"/tmp/at-git-cache",
}

// allowedFileBrowsePrefixes are root prefixes (without exact match) that
// permit any descendant. Used for /tmp/at-org-<id>/... where the suffix is
// dynamic per-organization.
var allowedFileBrowsePrefixes = []string{
	"/tmp/at-org-",
}

// resolveAndCheckPath cleans the requested path, resolves symlinks where
// possible, and returns the canonical absolute path along with the file
// info. Errors with http.Error already and returns (_, _, false) when the
// path is outside the allow-list, missing, or otherwise invalid.
//
// requireDir flips the not-a-dir / is-a-dir error.
func resolveAndCheckPath(w http.ResponseWriter, raw string, requireDir bool) (string, os.FileInfo, bool) {
	if raw == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return "", nil, false
	}

	// 1) Make absolute and clean. filepath.Clean removes "." and ".." segments.
	// Abs ensures relative paths don't escape via the daemon's CWD.
	cleaned, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return "", nil, false
	}

	// 2) Allow-list check on the LEXICAL path first. We check this before
	// EvalSymlinks so that a missing path (which can't be resolved) still
	// gets a clean 404 within an allowed root rather than a confusing
	// "outside allowed roots" message.
	if !isWithinAllowedRoot(cleaned) {
		http.Error(w, "path is outside the allowed file roots", http.StatusForbidden)
		return "", nil, false
	}

	// 3) Resolve symlinks. If the path exists but the resolved target
	// escapes the allow-list, reject. EvalSymlinks fails on non-existent
	// paths — for those we fall through to the os.Stat below for a 404.
	resolved := cleaned
	if real, err := filepath.EvalSymlinks(cleaned); err == nil {
		resolved = real
		if !isWithinAllowedRoot(resolved) {
			http.Error(w, "resolved path escapes the allowed file roots", http.StatusForbidden)
			return "", nil, false
		}
	}

	// 4) Stat the resolved path (Lstat would give us symlink-on-symlink,
	// which we don't want; we already followed via EvalSymlinks).
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

// isWithinAllowedRoot returns true when path is one of the allow-listed
// roots OR a strict descendant of one. Strict-descendant means we require
// a trailing separator so /tmp/at-tasks-evil cannot piggy-back on
// /tmp/at-tasks.
func isWithinAllowedRoot(path string) bool {
	clean := filepath.Clean(path)
	for _, root := range allowedFileBrowseRoots {
		if clean == root {
			return true
		}
		if strings.HasPrefix(clean, root+string(filepath.Separator)) {
			return true
		}
	}
	for _, prefix := range allowedFileBrowsePrefixes {
		// Prefix-style roots have no exact-match form (the suffix is dynamic),
		// so we always require at least one character past the prefix.
		if strings.HasPrefix(clean, prefix) && len(clean) > len(prefix) {
			return true
		}
	}
	return false
}

// FileBrowseAPI lists the contents of a directory.
// GET /api/v1/files/browse?path=/tmp/at-tasks
func (s *Server) FileBrowseAPI(w http.ResponseWriter, r *http.Request) {
	dirPath := r.URL.Query().Get("path")
	if dirPath == "" {
		// Default to the task-workspace root since that's the most common
		// browse target. The previous default was bare /tmp, which exposed
		// every cache under there to the browser.
		dirPath = "/tmp/at-tasks"
	}

	resolved, _, ok := resolveAndCheckPath(w, dirPath, true)
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

	// Compute the parent only when it's still inside an allowed root,
	// otherwise the UI's "go up" button would offer a destination that
	// the next browse call would reject. When parent escapes, surface
	// the same root as the parent so /up navigates to itself (no-op).
	parent := filepath.Dir(resolved)
	if !isWithinAllowedRoot(parent) {
		parent = resolved
	}

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
	resolved, info, ok := resolveAndCheckPath(w, r.URL.Query().Get("path"), false)
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

// FileDeleteAPI deletes a file or empty directory.
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

	if !isWithinAllowedRoot(cleaned) {
		http.Error(w, "path is outside the allowed file roots", http.StatusForbidden)
		return
	}

	// Don't allow deleting an allowed-root itself or a top-level
	// /tmp/at-org-<id> root — those are managed by the platform and
	// removing them would break in-flight org delegations.
	for _, root := range allowedFileBrowseRoots {
		if cleaned == root {
			http.Error(w, "cannot delete an allowed-root directory", http.StatusForbidden)
			return
		}
	}
	for _, prefix := range allowedFileBrowsePrefixes {
		// A path is a top-level prefix root if it equals prefix+<segment>
		// with no further separators (e.g. /tmp/at-org-abc but not
		// /tmp/at-org-abc/sub).
		if strings.HasPrefix(cleaned, prefix) {
			rest := cleaned[len(prefix):]
			if rest != "" && !strings.Contains(rest, string(filepath.Separator)) {
				http.Error(w, "cannot delete an allowed-root directory", http.StatusForbidden)
				return
			}
		}
	}

	// Resolve symlinks before deletion so we don't follow a malicious
	// link out of the allowed roots.
	if real, err := filepath.EvalSymlinks(cleaned); err == nil {
		if !isWithinAllowedRoot(real) {
			http.Error(w, "resolved path escapes the allowed file roots", http.StatusForbidden)
			return
		}
		cleaned = real
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
