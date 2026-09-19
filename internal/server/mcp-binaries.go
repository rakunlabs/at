package server

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/rakunlabs/at/internal/service/workflow"
)

// The MCP program library (workflow.MCPDir()) hosts binaries and config files
// that stdio MCP upstreams reference by absolute path (command, args, or an
// env var pointing at a config file). It lives beside the assets library under
// server.workspace.root, is exempt from the workspace janitor, and survives
// restarts when that root is a mounted volume.
//
// These routes are registered on apiGroup without a BusinessRoutePolicy, so
// they are installation-administrator only: an uploaded file is executed on
// the host by whatever MCP config names it.

// mcpBinaryInfo describes one entry in the MCP program library — a plain
// file, or a directory created by extracting an uploaded archive.
type mcpBinaryInfo struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	Mode       string `json:"mode"`
	Executable bool   `json:"executable"`
	ModifiedAt string `json:"modified_at"`
	Dir        bool   `json:"dir,omitempty"`
	Entries    int    `json:"entries,omitempty"` // direct children of a directory
}

// Archive extraction bounds. The archive body is caller-supplied, so both the
// entry count and the decompressed size are capped: a small .tar.gz can
// otherwise expand into an arbitrarily large write (decompression bomb).
const (
	maxArchiveFiles = 10000
	maxArchiveBytes = 1 << 30 // 1 GiB decompressed
)

// isArchiveName reports whether the filename looks like a supported archive.
func isArchiveName(name string) bool {
	l := strings.ToLower(name)
	return strings.HasSuffix(l, ".tar.gz") || strings.HasSuffix(l, ".tgz") || strings.HasSuffix(l, ".tar")
}

// archiveBaseName strips the archive extension: "foo-mcp-v1.tar.gz" → "foo-mcp-v1".
func archiveBaseName(name string) string {
	l := strings.ToLower(name)
	switch {
	case strings.HasSuffix(l, ".tar.gz"):
		return name[:len(name)-len(".tar.gz")]
	case strings.HasSuffix(l, ".tgz"):
		return name[:len(name)-len(".tgz")]
	case strings.HasSuffix(l, ".tar"):
		return name[:len(name)-len(".tar")]
	}
	return name
}

// extractTarArchive extracts a (optionally gzipped) tar stream into destRoot.
// Entry paths are validated against traversal ("../", absolute paths) and the
// result is double-checked to stay under destRoot. Symlinks, hardlinks and
// device nodes are skipped: a symlink pointing outside the library would turn
// a later read or delete into an escape. Regular files keep their exec bits
// (normalized to 0755/0644).
func extractTarArchive(r io.Reader, gzipped bool, destRoot string) (int, int64, error) {
	var tr *tar.Reader
	if gzipped {
		gz, err := gzip.NewReader(r)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid gzip stream: %w", err)
		}
		defer gz.Close()
		tr = tar.NewReader(gz)
	} else {
		tr = tar.NewReader(r)
	}

	count := 0
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return count, total, fmt.Errorf("invalid tar stream: %w", err)
		}

		clean := path.Clean(hdr.Name)
		if clean == "." || clean == "" {
			continue
		}
		if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			return count, total, fmt.Errorf("archive entry %q escapes the target directory", hdr.Name)
		}
		target := filepath.Join(destRoot, filepath.FromSlash(clean))
		if rel, err := filepath.Rel(destRoot, target); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return count, total, fmt.Errorf("archive entry %q escapes the target directory", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return count, total, err
			}
		case tar.TypeReg:
			count++
			if count > maxArchiveFiles {
				return count, total, fmt.Errorf("archive has more than %d files", maxArchiveFiles)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return count, total, err
			}
			mode := os.FileMode(0o644)
			if hdr.FileInfo().Mode().Perm()&0o111 != 0 {
				mode = 0o755
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return count, total, err
			}
			n, err := io.Copy(f, io.LimitReader(tr, maxArchiveBytes-total+1))
			closeErr := f.Close()
			total += n
			if err != nil {
				return count, total, err
			}
			if closeErr != nil {
				return count, total, closeErr
			}
			if total > maxArchiveBytes {
				return count, total, fmt.Errorf("archive expands past %d bytes", int64(maxArchiveBytes))
			}
		default:
			// Symlinks, hardlinks, devices: skipped deliberately.
			continue
		}
	}
	return count, total, nil
}

// validateMCPBinaryName refuses path traversal and hidden files. The library
// is a flat directory: names never contain separators.
func validateMCPBinaryName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
		return fmt.Errorf("name must be a plain filename without path separators")
	}
	if strings.HasPrefix(name, ".") {
		return fmt.Errorf("name must not start with a dot")
	}
	return nil
}

// ListMCPBinariesAPI handles GET /api/v1/mcp/binaries.
func (s *Server) ListMCPBinariesAPI(w http.ResponseWriter, r *http.Request) {
	dir, err := workflow.EnsureMCPDirReady()
	if err != nil {
		httpResponse(w, fmt.Sprintf("MCP program library unavailable: %v", err), http.StatusServiceUnavailable)
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to read MCP program library: %v", err), http.StatusInternalServerError)
		return
	}

	files := make([]mcpBinaryInfo, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if entry.IsDir() {
			// Extracted archive root. Report direct-child count instead of a
			// recursive size, which could be arbitrarily expensive to walk.
			children, _ := os.ReadDir(filepath.Join(dir, entry.Name()))
			files = append(files, mcpBinaryInfo{
				Name:       entry.Name(),
				Mode:       info.Mode().Perm().String(),
				ModifiedAt: info.ModTime().UTC().Format("2006-01-02T15:04:05Z07:00"),
				Dir:        true,
				Entries:    len(children),
			})
			continue
		}
		files = append(files, mcpBinaryInfo{
			Name:       entry.Name(),
			Size:       info.Size(),
			Mode:       info.Mode().Perm().String(),
			Executable: info.Mode().Perm()&0o100 != 0,
			ModifiedAt: info.ModTime().UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	httpResponseJSON(w, map[string]any{"dir": dir, "files": files}, http.StatusOK)
}

// UploadMCPBinaryAPI handles POST /api/v1/mcp/binaries. Multipart form:
// `file` (required), `name` (optional, defaults to the uploaded filename),
// `executable` ("true"/"false", default "true" — set false for config files),
// `extract` ("false" to store an archive as-is).
//
// Archives (.tar.gz / .tgz / .tar) are extracted server-side into
// <library>/<archive base name>/ unless extract=false: release tarballs
// usually carry a directory tree (bin/, lib/) that a flat file upload cannot
// represent. Extraction preserves tar exec bits and replaces an existing
// directory of the same name, which is the upgrade path.
//
// Both variants land atomically (temp file/dir + rename), so a binary a
// running process is about to spawn is never observed half-written.
func (s *Server) UploadMCPBinaryAPI(w http.ResponseWriter, r *http.Request) {
	dir, err := workflow.EnsureMCPDirReady()
	if err != nil {
		httpResponse(w, fmt.Sprintf("MCP program library unavailable: %v", err), http.StatusServiceUnavailable)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 512<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		httpResponse(w, "invalid multipart form", http.StatusBadRequest)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpResponse(w, "file field is required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	name := r.FormValue("name")
	if name == "" {
		name = filepath.Base(header.Filename)
	}

	// Archives are extracted into a directory named after the archive.
	if isArchiveName(name) && r.FormValue("extract") != "false" {
		s.extractMCPArchive(w, dir, name, header.Filename, file)
		return
	}

	if err := validateMCPBinaryName(name); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}

	executable := true
	if v := r.FormValue("executable"); v != "" {
		executable = v == "true" || v == "1"
	}
	mode := os.FileMode(0o755)
	if !executable {
		mode = 0o644
	}

	tmp, err := os.CreateTemp(dir, ".at-upload-*")
	if err != nil {
		httpResponse(w, fmt.Sprintf("upload failed: %v", err), http.StatusInternalServerError)
		return
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	n, err := io.Copy(tmp, file)
	if err != nil {
		cleanup()
		httpResponse(w, fmt.Sprintf("upload failed: %v", err), http.StatusInternalServerError)
		return
	}
	if err := tmp.Chmod(mode); err != nil {
		cleanup()
		httpResponse(w, fmt.Sprintf("upload failed: %v", err), http.StatusInternalServerError)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		httpResponse(w, fmt.Sprintf("upload failed: %v", err), http.StatusInternalServerError)
		return
	}
	dest := filepath.Join(dir, name)
	if err := os.Rename(tmpName, dest); err != nil {
		os.Remove(tmpName)
		httpResponse(w, fmt.Sprintf("upload failed: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, map[string]any{
		"name":       name,
		"path":       dest,
		"size":       n,
		"executable": executable,
	}, http.StatusCreated)
}

// extractMCPArchive extracts an uploaded tar[.gz] into <dir>/<base>/,
// staging in a temp directory and renaming into place on success.
func (s *Server) extractMCPArchive(w http.ResponseWriter, dir, name, uploadedFilename string, file io.Reader) {
	base := archiveBaseName(name)
	if err := validateMCPBinaryName(base); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}

	tmpDir, err := os.MkdirTemp(dir, ".at-extract-*")
	if err != nil {
		httpResponse(w, fmt.Sprintf("extract failed: %v", err), http.StatusInternalServerError)
		return
	}
	// The gzip decision follows the *uploaded* filename when it names an
	// archive: a custom `name` usually only renames the target directory.
	ref := uploadedFilename
	if !isArchiveName(ref) {
		ref = name
	}
	gzipped := !strings.HasSuffix(strings.ToLower(ref), ".tar")
	count, total, err := extractTarArchive(file, gzipped, tmpDir)
	if err != nil {
		os.RemoveAll(tmpDir)
		httpResponse(w, fmt.Sprintf("extract failed: %v", err), http.StatusBadRequest)
		return
	}
	if count == 0 {
		os.RemoveAll(tmpDir)
		httpResponse(w, "archive contains no files", http.StatusBadRequest)
		return
	}
	if err := os.Chmod(tmpDir, 0o755); err != nil { // MkdirTemp creates 0700
		os.RemoveAll(tmpDir)
		httpResponse(w, fmt.Sprintf("extract failed: %v", err), http.StatusInternalServerError)
		return
	}

	dest := filepath.Join(dir, base)
	// Replace an existing extraction of the same name: re-uploading the
	// archive is the upgrade path.
	if err := os.RemoveAll(dest); err != nil {
		os.RemoveAll(tmpDir)
		httpResponse(w, fmt.Sprintf("extract failed: %v", err), http.StatusInternalServerError)
		return
	}
	if err := os.Rename(tmpDir, dest); err != nil {
		os.RemoveAll(tmpDir)
		httpResponse(w, fmt.Sprintf("extract failed: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, map[string]any{
		"name":      base,
		"path":      dest,
		"size":      total,
		"files":     count,
		"extracted": true,
	}, http.StatusCreated)
}

// DeleteMCPBinaryAPI handles DELETE /api/v1/mcp/binaries/{name}. Removes a
// plain file or a whole extracted-archive directory. The name is validated to
// a flat entry, so RemoveAll can only reach direct children of the library.
func (s *Server) DeleteMCPBinaryAPI(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := validateMCPBinaryName(name); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}

	target := filepath.Join(workflow.MCPDir(), name)
	if _, err := os.Lstat(target); err != nil {
		if os.IsNotExist(err) {
			httpResponse(w, fmt.Sprintf("file %q not found", name), http.StatusNotFound)
			return
		}
		httpResponse(w, fmt.Sprintf("failed to delete: %v", err), http.StatusInternalServerError)
		return
	}
	if err := os.RemoveAll(target); err != nil {
		httpResponse(w, fmt.Sprintf("failed to delete: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, map[string]string{"status": "deleted"}, http.StatusOK)
}
