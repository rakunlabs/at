package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/rakunlabs/at/internal/devfs"
	"github.com/rakunlabs/at/internal/devfs/devfsbin"
	"github.com/rakunlabs/at/internal/service"
)

const (
	developerWriteMaxBytes  = 2 << 20
	developerUploadMaxBytes = 64 << 20
)

// developerFSError is a refusal reported by the in-container file helper.
type developerFSError struct{ message string }

func (e *developerFSError) Error() string { return e.message }

// developerFSHelperPath is where the embedded at-devfs helper is installed in
// a space container. It is outside /workspace, so it is neither in the user's
// projects nor counted against their quota, and it is re-copied whenever the
// container is (re)started by this process, so an AT upgrade replaces it.
const developerFSHelperPath = "/usr/local/bin/at-devfs"

// runDeveloperFS executes one file operation in the space container. root is
// the project directory relative to /workspace ("" is the whole space); every
// argument is resolved against it and anything that escapes is refused.
//
// The embedded at-devfs helper is used when this binary carries one for the
// container's platform, so no tool is required in the image. Otherwise the
// equivalent devfs.PythonScript runs through the image's python3.
func (s *Server) runDeveloperFS(ctx context.Context, h *developerRuntimeHandle, stdin io.Reader, op, root string, args ...string) (json.RawMessage, error) {
	command, argv := "python3", append([]string{"-c", devfs.PythonScript, op, root}, args...)
	capabilities := s.containerManager.Capabilities()
	if capabilities.FileHelperPath != "" && capabilities.FileHelperPath != developerFSHelperPath {
		// Kubernetes' versioned init image installs the helper independently of
		// the host build. This also works when AT carries no embedded binaries.
		command, argv = capabilities.FileHelperPath, append([]string{op, root}, args...)
	} else if devfsbin.Available() {
		installed, err := s.containerManager.EnsureFile(ctx, h.scope, h.cfg, developerFSHelperPath, 0o755, devfsbin.For)
		if err != nil {
			return nil, err
		}
		if installed {
			command, argv = developerFSHelperPath, append([]string{op, root}, args...)
		}
	}
	stdout, stderr, code, err := s.containerManager.ExecArgsInput(ctx, h.scope, h.cfg, service.DeveloperWorkspaceRoot, nil, stdin, command, argv...)
	if err != nil {
		return nil, err
	}
	if code == 3 {
		var refusal struct {
			Error string `json:"error"`
		}
		if json.Unmarshal([]byte(stdout), &refusal) == nil && refusal.Error != "" {
			return nil, &developerFSError{message: refusal.Error}
		}
	}
	if code != 0 {
		return nil, developerCommandFailure("file operation", stderr, nil)
	}
	return json.RawMessage(stdout), nil
}

func developerFSResponse(w http.ResponseWriter, raw json.RawMessage, err error) {
	if err != nil {
		var refusal *developerFSError
		if errors.As(err, &refusal) {
			status := http.StatusBadRequest
			switch {
			case strings.HasPrefix(refusal.message, "conflict"):
				status = http.StatusConflict
			case strings.HasPrefix(refusal.message, "not found"):
				status = http.StatusNotFound
			case strings.HasPrefix(refusal.message, "already exists"), strings.HasPrefix(refusal.message, "destination already exists"):
				status = http.StatusConflict
			}
			httpResponse(w, refusal.message, status)
			return
		}
		httpResponse(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func developerQueryPath(r *http.Request, key string) (string, error) {
	return service.CleanDeveloperPath(r.URL.Query().Get(key))
}

// ListDeveloperFilesAPI lists one directory. The tree loads lazily, one level
// per expanded folder, so a large node_modules never has to be walked.
func (s *Server) ListDeveloperFilesAPI(w http.ResponseWriter, r *http.Request) {
	rel, err := developerQueryPath(r, "path")
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	h, ok := s.developerRuntime(w, r)
	if !ok {
		return
	}
	raw, err := s.runDeveloperFS(r.Context(), h, nil, "list", "", rel)
	developerFSResponse(w, raw, err)
}

// ReadDeveloperFileAPI returns UTF-8 content plus a version token used to
// refuse a save over changes made elsewhere (the agent, the terminal).
func (s *Server) ReadDeveloperFileAPI(w http.ResponseWriter, r *http.Request) {
	rel, err := developerQueryPath(r, "path")
	if err != nil || rel == "" {
		httpResponse(w, "path is required", http.StatusBadRequest)
		return
	}
	h, ok := s.developerRuntime(w, r)
	if !ok {
		return
	}
	raw, err := s.runDeveloperFS(r.Context(), h, nil, "read", "", rel)
	developerFSResponse(w, raw, err)
}

func (s *Server) WriteDeveloperFileAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Version string `json:"version"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, developerWriteMaxBytes*2)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, "invalid request body", http.StatusBadRequest)
		return
	}
	rel, err := service.CleanDeveloperPath(req.Path)
	if err != nil || rel == "" {
		httpResponse(w, "path is required", http.StatusBadRequest)
		return
	}
	if len(req.Content) > developerWriteMaxBytes {
		httpResponse(w, "file exceeds the 2 MiB editor limit", http.StatusRequestEntityTooLarge)
		return
	}
	h, ok := s.developerRuntime(w, r)
	if !ok {
		return
	}
	raw, err := s.runDeveloperFS(r.Context(), h, strings.NewReader(req.Content), "write", "", rel, req.Version)
	developerFSResponse(w, raw, err)
}

func (s *Server) CreateDeveloperFileAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
		Type string `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || (req.Type != "file" && req.Type != "dir") {
		httpResponse(w, "path and type (file or dir) are required", http.StatusBadRequest)
		return
	}
	rel, err := service.CleanDeveloperPath(req.Path)
	if err != nil || rel == "" {
		httpResponse(w, "path is required", http.StatusBadRequest)
		return
	}
	h, ok := s.developerRuntime(w, r)
	if !ok {
		return
	}
	raw, err := s.runDeveloperFS(r.Context(), h, nil, "create", "", req.Type, rel)
	developerFSResponse(w, raw, err)
}

func (s *Server) RenameDeveloperFileAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, "invalid request body", http.StatusBadRequest)
		return
	}
	from, err1 := service.CleanDeveloperPath(req.From)
	to, err2 := service.CleanDeveloperPath(req.To)
	if err1 != nil || err2 != nil || from == "" || to == "" {
		httpResponse(w, "from and to must be paths inside the space", http.StatusBadRequest)
		return
	}
	h, ok := s.developerRuntime(w, r)
	if !ok {
		return
	}
	raw, err := s.runDeveloperFS(r.Context(), h, nil, "rename", "", from, to)
	developerFSResponse(w, raw, err)
}

func (s *Server) DeleteDeveloperFileAPI(w http.ResponseWriter, r *http.Request) {
	rel, err := developerQueryPath(r, "path")
	if err != nil || rel == "" {
		httpResponse(w, "path is required", http.StatusBadRequest)
		return
	}
	h, ok := s.developerRuntime(w, r)
	if !ok {
		return
	}
	raw, err := s.runDeveloperFS(r.Context(), h, nil, "delete", "", rel)
	developerFSResponse(w, raw, err)
}

// UploadDeveloperFileAPI stores one multipart file into a folder of the space.
func (s *Server) UploadDeveloperFileAPI(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, developerUploadMaxBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		httpResponse(w, "upload exceeds the 64 MiB limit or is not multipart", http.StatusRequestEntityTooLarge)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpResponse(w, "file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	dir, err := service.CleanDeveloperPath(r.FormValue("path"))
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := path.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
	if name == "" || name == "." || name == ".." || name == "/" {
		httpResponse(w, "invalid file name", http.StatusBadRequest)
		return
	}
	target := name
	if dir != "" {
		target = dir + "/" + name
	}
	h, ok := s.developerRuntime(w, r)
	if !ok {
		return
	}
	raw, err := s.runDeveloperFS(r.Context(), h, file, "write", "", target, "")
	developerFSResponse(w, raw, err)
}

// DownloadDeveloperFileAPI returns raw bytes, used for image previews and
// downloads. Output is bounded by the container runtime's 16 MiB cap.
func (s *Server) DownloadDeveloperFileAPI(w http.ResponseWriter, r *http.Request) {
	rel, err := developerQueryPath(r, "path")
	if err != nil || rel == "" {
		httpResponse(w, "path is required", http.StatusBadRequest)
		return
	}
	h, ok := s.developerRuntime(w, r)
	if !ok {
		return
	}
	if _, err := s.runDeveloperFS(r.Context(), h, nil, "read", "", rel); err != nil {
		developerFSResponse(w, nil, err)
		return
	}
	data, stderr, code, err := h.exec(r.Context(), s, service.DeveloperWorkspaceRoot, "cat", "--", service.DeveloperAbsolutePath(rel))
	if err != nil || code != 0 {
		httpResponse(w, developerCommandFailure("read file", stderr, err).Error(), http.StatusBadGateway)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(rel))
	if contentType == "" || strings.HasPrefix(contentType, "text/html") || strings.Contains(contentType, "svg") || strings.Contains(contentType, "javascript") {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, no-store")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(rel)}))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, bytes.NewReader([]byte(data)))
}

func (s *Server) SearchDeveloperFilesAPI(w http.ResponseWriter, r *http.Request) {
	pattern := r.URL.Query().Get("pattern")
	if strings.TrimSpace(pattern) == "" || len(pattern) > 1000 {
		httpResponse(w, "pattern is required", http.StatusBadRequest)
		return
	}
	rel, err := developerQueryPath(r, "path")
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	h, ok := s.developerRuntime(w, r)
	if !ok {
		return
	}
	raw, err := s.runDeveloperFS(r.Context(), h, nil, "search", rel, pattern, "")
	developerFSResponse(w, raw, err)
}

// CloneDeveloperProjectAPI clones a remote into a new top-level project folder.
func (s *Server) CloneDeveloperProjectAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Remote string `json:"remote"`
		Name   string `json:"name"`
		Branch string `json:"branch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.Remote = strings.TrimSpace(req.Remote)
	if err := service.ValidDeveloperRemote(req.Remote); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = strings.TrimSuffix(path.Base(strings.TrimSuffix(strings.ReplaceAll(req.Remote, ":", "/"), "/")), ".git")
	}
	if clean, err := service.CleanDeveloperPath(name); err != nil || clean == "" || strings.Contains(clean, "/") || strings.HasPrefix(clean, "-") {
		httpResponse(w, "project name must be a single folder name", http.StatusBadRequest)
		return
	}
	if req.Branch != "" && !service.ValidDeveloperBranch(req.Branch) {
		httpResponse(w, "branch is invalid", http.StatusBadRequest)
		return
	}
	if err := developerRemoteAllowed(r.Context(), req.Remote); err != nil {
		httpResponse(w, err.Error(), http.StatusForbidden)
		return
	}
	h, ok := s.developerRuntime(w, r)
	if !ok {
		return
	}
	args := []string{"clone", "--origin", "origin"}
	if req.Branch != "" {
		args = append(args, "--branch", req.Branch)
	}
	args = append(args, "--", req.Remote, service.DeveloperAbsolutePath(name))
	if _, stderr, code, err := h.exec(r.Context(), s, service.DeveloperWorkspaceRoot, "git", args...); err != nil || code != 0 {
		httpResponse(w, developerCommandFailure("git clone", stderr, err).Error(), http.StatusBadGateway)
		return
	}
	httpResponseJSON(w, map[string]any{"path": name}, http.StatusOK)
}
