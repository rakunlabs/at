package server

import (
	"encoding/json"
	"fmt"
	"io"
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

// FileBrowseAPI lists the contents of a directory.
// GET /api/v1/files/browse?path=/tmp
func (s *Server) FileBrowseAPI(w http.ResponseWriter, r *http.Request) {
	dirPath := r.URL.Query().Get("path")
	if dirPath == "" {
		dirPath = "/tmp"
	}

	// Clean the path to prevent traversal
	dirPath = filepath.Clean(dirPath)

	info, err := os.Stat(dirPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("path not found: %v", err), http.StatusNotFound)
		return
	}
	if !info.IsDir() {
		http.Error(w, "path is not a directory", http.StatusBadRequest)
		return
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("cannot read directory: %v", err), http.StatusInternalServerError)
		return
	}

	var files []fileEntry
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}

		files = append(files, fileEntry{
			Name:    e.Name(),
			Path:    filepath.Join(dirPath, e.Name()),
			IsDir:   e.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().Format("2006-01-02 15:04:05"),
		})
	}

	// Sort: directories first, then by name
	sort.Slice(files, func(i, j int) bool {
		if files[i].IsDir != files[j].IsDir {
			return files[i].IsDir
		}
		return files[i].Name < files[j].Name
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"path":    dirPath,
		"parent":  filepath.Dir(dirPath),
		"entries": files,
	})
}

// FileServeAPI serves a file for viewing/downloading.
// GET /api/v1/files/serve?path=/tmp/video.mp4
func (s *Server) FileServeAPI(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	filePath = filepath.Clean(filePath)

	info, err := os.Stat(filePath)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	if info.IsDir() {
		http.Error(w, "path is a directory", http.StatusBadRequest)
		return
	}

	// Detect content type
	ext := filepath.Ext(filePath)
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	f, err := os.Open(filePath)
	if err != nil {
		http.Error(w, "cannot open file", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", filepath.Base(filePath)))
	io.Copy(w, f)
}

// FileDeleteAPI deletes a file or empty directory.
// DELETE /api/v1/files?path=/tmp/video.mp4
func (s *Server) FileDeleteAPI(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	filePath = filepath.Clean(filePath)

	// Safety: don't allow deleting root-level paths
	if filePath == "/" || filePath == "/tmp" || filePath == "/home" || filePath == "/Users" {
		http.Error(w, "cannot delete this path", http.StatusForbidden)
		return
	}

	info, err := os.Stat(filePath)
	if err != nil {
		http.Error(w, "path not found", http.StatusNotFound)
		return
	}

	if info.IsDir() {
		err = os.RemoveAll(filePath)
	} else {
		err = os.Remove(filePath)
	}

	if err != nil {
		http.Error(w, fmt.Sprintf("delete failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"deleted": filePath})
}
