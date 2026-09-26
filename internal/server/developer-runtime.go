package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/container"
)

func developerContainerScope(space *service.DeveloperSpace) string {
	return "developer:" + space.WorkspaceID + ":" + space.OwnerUserID + ":" + space.ID
}

func developerContainerConfig(space *service.DeveloperSpace) container.Config {
	image := strings.TrimSpace(space.Image)
	if image == "" {
		image = "at-agent-runtime:latest"
	}
	cpu := strings.TrimSpace(space.CPULimit)
	if cpu == "" {
		cpu = "2"
	}
	memory := strings.TrimSpace(space.MemoryLimit)
	if memory == "" {
		memory = "4g"
	}
	diskLimit := space.DiskLimitBytes
	if diskLimit <= 0 {
		diskLimit = 20 << 30
	}
	return container.Config{
		Enabled: true, Image: image, CPU: cpu, Memory: memory,
		Network: true, PersistentVolume: true, RequireRootless: true, DiskLimitBytes: diskLimit, PidsLimit: 256,
	}
}

func developerRepositoryPath(id string) string {
	return path.Join("/workspace/repositories", id, "main")
}
func developerWorktreePath(id string) string { return path.Join("/workspace/worktrees", id) }

func developerCommandFailure(operation, stderr string, err error) error {
	detail := strings.TrimSpace(stderr)
	if err != nil && detail != "" {
		return fmt.Errorf("%s: %s: %w", operation, detail, err)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	if detail != "" {
		return fmt.Errorf("%s: %w", operation, errors.New(detail))
	}
	return fmt.Errorf("%s failed", operation)
}

func developerRemoteHost(remote string) string {
	if strings.HasPrefix(remote, "git@") {
		host, _, _ := strings.Cut(strings.TrimPrefix(remote, "git@"), ":")
		return host
	}
	parsed, err := url.Parse(remote)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func developerRemoteAllowed(ctx context.Context, remote string) error {
	host := developerRemoteHost(remote)
	if host == "" || strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return errors.New("repository remote host is not allowed")
	}
	addresses, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("resolve repository remote host: %w", err)
	}
	for _, address := range addresses {
		if address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsUnspecified() {
			return errors.New("repository remote resolves to a private or local address")
		}
	}
	return nil
}

func (s *Server) developerRuntime(w http.ResponseWriter, r *http.Request, spaceID string) (service.DeveloperSpaceStorer, *service.DeveloperSpace, container.Config, string, bool) {
	bound, err := s.bindRuntimePrincipal(r.Context(), "developer_space")
	if err != nil {
		fileAccessError(w, err)
		return nil, nil, container.Config{}, "", false
	}
	if err := service.CheckExecution(bound, service.ExecutionAction{Kind: "resource", Name: "execution.run", ResourceID: spaceID}); err != nil {
		fileAccessError(w, err)
		return nil, nil, container.Config{}, "", false
	}
	*r = *r.WithContext(bound)
	store := s.developerSpaceStore(w)
	if store == nil {
		return nil, nil, container.Config{}, "", false
	}
	space, err := store.GetDeveloperSpace(r.Context(), spaceID)
	if err != nil {
		developerSpaceError(w, err)
		return nil, nil, container.Config{}, "", false
	}
	if s.containerManager == nil {
		httpResponse(w, "container runtime unavailable", http.StatusServiceUnavailable)
		return nil, nil, container.Config{}, "", false
	}
	return store, space, developerContainerConfig(space), developerContainerScope(space), true
}

func (s *Server) StartDeveloperSpaceAPI(w http.ResponseWriter, r *http.Request) {
	store, space, cfg, scope, ok := s.developerRuntime(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if _, err := s.containerManager.EnsureContainer(r.Context(), scope, cfg); err != nil {
		_, _ = store.SetDeveloperSpaceRuntime(r.Context(), space.ID, service.DeveloperSpaceError, err.Error())
		httpResponse(w, fmt.Sprintf("could not start developer space: %v", err), http.StatusServiceUnavailable)
		return
	}
	updated, err := store.SetDeveloperSpaceRuntime(r.Context(), space.ID, service.DeveloperSpaceReady, "")
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, updated, http.StatusOK)
}

func (s *Server) StopDeveloperSpaceAPI(w http.ResponseWriter, r *http.Request) {
	store, space, _, scope, ok := s.developerRuntime(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if err := s.containerManager.StopContainer(r.Context(), scope); err != nil {
		httpResponse(w, fmt.Sprintf("could not stop developer space: %v", err), http.StatusInternalServerError)
		return
	}
	updated, err := store.SetDeveloperSpaceRuntime(r.Context(), space.ID, service.DeveloperSpaceStopped, "")
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, updated, http.StatusOK)
}

func (s *Server) CloneDeveloperRepositoryAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	repository, err := store.GetDeveloperRepository(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	if err := developerRemoteAllowed(r.Context(), repository.RemoteURL); err != nil {
		httpResponse(w, err.Error(), http.StatusForbidden)
		return
	}
	store, space, cfg, scope, ok := s.developerRuntime(w, r, repository.SpaceID)
	if !ok {
		return
	}
	_, _ = store.SetDeveloperRepositoryRuntime(r.Context(), repository.ID, service.DeveloperRepositoryCloning, repository.HeadSHA, "")
	repoPath := developerRepositoryPath(repository.ID)
	if _, stderr, code, err := s.containerManager.ExecArgs(r.Context(), scope, cfg, "/workspace", nil, "mkdir", "-p", path.Dir(repoPath)); err != nil || code != 0 {
		s.developerRepositoryFailed(r, store, repository, developerCommandFailure("prepare repository directory", stderr, err), w)
		return
	}
	_, _, existingCode, existingErr := s.containerManager.ExecArgs(r.Context(), scope, cfg, "/workspace", nil, "git", "-C", repoPath, "rev-parse", "--verify", "HEAD")
	if existingErr == nil && existingCode == 0 {
		if _, stderr, code, err := s.containerManager.ExecArgs(r.Context(), scope, cfg, repoPath, nil, "git", "remote", "set-url", "origin", repository.RemoteURL); err != nil || code != 0 {
			s.developerRepositoryFailed(r, store, repository, developerCommandFailure("restore repository remote", stderr, err), w)
			return
		}
		if _, stderr, code, err := s.containerManager.ExecArgs(r.Context(), scope, cfg, repoPath, nil, "git", "fetch", "--all", "--prune"); err != nil || code != 0 {
			s.developerRepositoryFailed(r, store, repository, developerCommandFailure("fetch repository", stderr, err), w)
			return
		}
	} else {
		args := []string{"clone", "--origin", "origin"}
		if repository.DefaultBranch != "" {
			args = append(args, "--branch", repository.DefaultBranch)
		}
		args = append(args, "--", repository.RemoteURL, repoPath)
		if _, stderr, code, err := s.containerManager.ExecArgs(r.Context(), scope, cfg, "/workspace", nil, "git", args...); err != nil || code != 0 {
			s.developerRepositoryFailed(r, store, repository, developerCommandFailure("clone repository", stderr, err), w)
			return
		}
	}
	head, stderr, code, err := s.containerManager.ExecArgs(r.Context(), scope, cfg, repoPath, nil, "git", "rev-parse", "HEAD")
	if err != nil || code != 0 {
		s.developerRepositoryFailed(r, store, repository, developerCommandFailure("read repository head", stderr, err), w)
		return
	}
	updated, err := store.SetDeveloperRepositoryRuntime(r.Context(), repository.ID, service.DeveloperRepositoryReady, strings.TrimSpace(head), "")
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	_, _ = store.SetDeveloperSpaceRuntime(r.Context(), space.ID, service.DeveloperSpaceReady, "")
	httpResponseJSON(w, updated, http.StatusOK)
}

func (s *Server) developerRepositoryFailed(r *http.Request, store service.DeveloperSpaceStorer, repository *service.DeveloperRepository, failure error, w http.ResponseWriter) {
	message := failure.Error()
	_, _ = store.SetDeveloperRepositoryRuntime(r.Context(), repository.ID, service.DeveloperRepositoryError, repository.HeadSHA, message)
	httpResponse(w, message, http.StatusBadGateway)
}

func (s *Server) ProvisionDeveloperWorktreeAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	worktree, err := store.GetDeveloperWorktree(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	repository, err := store.GetDeveloperRepository(r.Context(), worktree.RepositoryID)
	if err != nil || repository.Status != service.DeveloperRepositoryReady {
		httpResponse(w, "repository must be cloned before creating a worktree", http.StatusConflict)
		return
	}
	store, _, cfg, scope, ok := s.developerRuntime(w, r, worktree.SpaceID)
	if !ok {
		return
	}
	repoPath := developerRepositoryPath(repository.ID)
	wtPath := developerWorktreePath(worktree.ID)
	base := worktree.BaseRef
	if base == "" {
		base = repository.DefaultBranch
	}
	if base == "" {
		base = "HEAD"
	}
	_, _, branchCode, _ := s.containerManager.ExecArgs(r.Context(), scope, cfg, repoPath, nil, "git", "show-ref", "--verify", "--quiet", "refs/heads/"+worktree.Branch)
	args := []string{"worktree", "add"}
	target := worktree.Branch
	if branchCode != 0 {
		args = append(args, "-b", worktree.Branch)
		target = base
	}
	args = append(args, "--", wtPath, target)
	if _, stderr, code, err := s.containerManager.ExecArgs(r.Context(), scope, cfg, repoPath, nil, "git", args...); err != nil || code != 0 {
		message := fmt.Sprintf("create worktree: %s: %v", strings.TrimSpace(stderr), err)
		_, _ = store.SetDeveloperWorktreeRuntime(r.Context(), worktree.ID, service.DeveloperWorktreeInvalid, worktree.HeadSHA, message)
		httpResponse(w, message, http.StatusBadGateway)
		return
	}
	head, stderr, code, err := s.containerManager.ExecArgs(r.Context(), scope, cfg, wtPath, nil, "git", "rev-parse", "HEAD")
	if err != nil || code != 0 {
		message := fmt.Sprintf("read worktree head: %s: %v", strings.TrimSpace(stderr), err)
		_, _ = store.SetDeveloperWorktreeRuntime(r.Context(), worktree.ID, service.DeveloperWorktreeInvalid, "", message)
		httpResponse(w, message, http.StatusBadGateway)
		return
	}
	updated, err := store.SetDeveloperWorktreeRuntime(r.Context(), worktree.ID, service.DeveloperWorktreeReady, strings.TrimSpace(head), "")
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, updated, http.StatusOK)
}

func (s *Server) DeveloperWorktreeStatusAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	worktree, err := store.GetDeveloperWorktree(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	_, _, cfg, scope, ok := s.developerRuntime(w, r, worktree.SpaceID)
	if !ok {
		return
	}
	output, stderr, code, err := s.containerManager.ExecArgs(r.Context(), scope, cfg, developerWorktreePath(worktree.ID), nil, "git", "status", "--porcelain=v2", "--branch")
	if err != nil || code != 0 {
		if strings.Contains(stderr, "No such file") || strings.Contains(stderr, "does not exist") {
			_, _ = store.SetDeveloperWorktreeRuntime(r.Context(), worktree.ID, service.DeveloperWorktreeMissing, worktree.HeadSHA, "worktree directory is missing")
		}
		httpResponse(w, fmt.Sprintf("git status failed: %s: %v", strings.TrimSpace(stderr), err), http.StatusBadGateway)
		return
	}
	httpResponseJSON(w, map[string]any{"worktree_id": worktree.ID, "porcelain_v2": output}, http.StatusOK)
}

func (s *Server) DeveloperWorktreeDiffAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	worktree, err := store.GetDeveloperWorktree(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	_, _, cfg, scope, ok := s.developerRuntime(w, r, worktree.SpaceID)
	if !ok {
		return
	}
	args := []string{"diff", "--no-ext-diff"}
	if r.URL.Query().Get("staged") == "true" {
		args = append(args, "--cached")
	}
	args = append(args, "--")
	output, stderr, code, err := s.containerManager.ExecArgs(r.Context(), scope, cfg, developerWorktreePath(worktree.ID), nil, "git", args...)
	if err != nil || code != 0 {
		httpResponse(w, fmt.Sprintf("git diff failed: %s: %v", strings.TrimSpace(stderr), err), http.StatusBadGateway)
		return
	}
	httpResponseJSON(w, map[string]any{"worktree_id": worktree.ID, "diff": output}, http.StatusOK)
}

func (s *Server) developerWorktreeCommand(w http.ResponseWriter, r *http.Request) (service.DeveloperSpaceStorer, *service.DeveloperWorktree, container.Config, string, bool) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return nil, nil, container.Config{}, "", false
	}
	worktree, err := store.GetDeveloperWorktree(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return nil, nil, container.Config{}, "", false
	}
	store, _, cfg, scope, ok := s.developerRuntime(w, r, worktree.SpaceID)
	return store, worktree, cfg, scope, ok
}

func (s *Server) StageDeveloperWorktreeAPI(w http.ResponseWriter, r *http.Request) {
	_, worktree, cfg, scope, ok := s.developerWorktreeCommand(w, r)
	if !ok {
		return
	}
	var req struct {
		Paths []string `json:"paths"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || len(req.Paths) == 0 || len(req.Paths) > 100 {
		httpResponse(w, "paths must contain 1 to 100 relative paths", http.StatusBadRequest)
		return
	}
	args := []string{"add", "--"}
	for _, candidate := range req.Paths {
		if candidate == "." {
			args = append(args, candidate)
			continue
		}
		clean, err := normalizeStorageFilePath(candidate, false)
		if err != nil {
			httpResponse(w, fmt.Sprintf("invalid path %q", candidate), http.StatusBadRequest)
			return
		}
		args = append(args, clean)
	}
	_, stderr, code, err := s.containerManager.ExecArgs(r.Context(), scope, cfg, developerWorktreePath(worktree.ID), nil, "git", args...)
	if err != nil || code != 0 {
		httpResponse(w, developerCommandFailure("git add", stderr, err).Error(), http.StatusBadGateway)
		return
	}
	httpResponseJSON(w, map[string]any{"staged": req.Paths}, http.StatusOK)
}

func (s *Server) CommitDeveloperWorktreeAPI(w http.ResponseWriter, r *http.Request) {
	store, worktree, cfg, scope, ok := s.developerWorktreeCommand(w, r)
	if !ok {
		return
	}
	var req struct {
		Message string `json:"message"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || strings.TrimSpace(req.Message) == "" || len(req.Message) > 10000 {
		httpResponse(w, "message is required and must not exceed 10000 bytes", http.StatusBadRequest)
		return
	}
	_, stderr, code, err := s.containerManager.ExecArgs(r.Context(), scope, cfg, developerWorktreePath(worktree.ID), nil, "git", "commit", "-m", req.Message)
	if err != nil || code != 0 {
		httpResponse(w, developerCommandFailure("git commit", stderr, err).Error(), http.StatusBadGateway)
		return
	}
	head, stderr, code, err := s.containerManager.ExecArgs(r.Context(), scope, cfg, developerWorktreePath(worktree.ID), nil, "git", "rev-parse", "HEAD")
	if err != nil || code != 0 {
		httpResponse(w, developerCommandFailure("read committed head", stderr, err).Error(), http.StatusBadGateway)
		return
	}
	updated, err := store.SetDeveloperWorktreeRuntime(r.Context(), worktree.ID, service.DeveloperWorktreeReady, strings.TrimSpace(head), "")
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, updated, http.StatusOK)
}

func (s *Server) PushDeveloperWorktreeAPI(w http.ResponseWriter, r *http.Request) {
	store, worktree, cfg, scope, ok := s.developerWorktreeCommand(w, r)
	if !ok {
		return
	}
	var req struct {
		Confirm bool   `json:"confirm"`
		Branch  string `json:"branch"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpResponse(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !req.Confirm || req.Branch != worktree.Branch {
		httpResponse(w, "push requires explicit confirmation of the exact worktree branch", http.StatusConflict)
		return
	}
	repository, err := store.GetDeveloperRepository(r.Context(), worktree.RepositoryID)
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	remote, stderr, code, err := s.containerManager.ExecArgs(r.Context(), scope, cfg, developerWorktreePath(worktree.ID), nil, "git", "remote", "get-url", "--push", "origin")
	if err != nil || code != 0 {
		httpResponse(w, developerCommandFailure("read push remote", stderr, err).Error(), http.StatusBadGateway)
		return
	}
	if strings.TrimSpace(remote) != repository.RemoteURL {
		httpResponse(w, "push remote changed since the repository was registered; clone again before pushing", http.StatusConflict)
		return
	}
	if err := developerRemoteAllowed(r.Context(), remote); err != nil {
		httpResponse(w, err.Error(), http.StatusForbidden)
		return
	}
	refspec := "HEAD:refs/heads/" + worktree.Branch
	output, stderr, code, err := s.containerManager.ExecArgs(r.Context(), scope, cfg, developerWorktreePath(worktree.ID), nil, "git", "push", "origin", refspec)
	if err != nil || code != 0 {
		httpResponse(w, developerCommandFailure("git push", stderr, err).Error(), http.StatusBadGateway)
		return
	}
	httpResponseJSON(w, map[string]any{"branch": worktree.Branch, "output": output + stderr}, http.StatusOK)
}
