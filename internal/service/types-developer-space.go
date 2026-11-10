package service

import (
	"context"
	"errors"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const (
	// DefaultDeveloperImage is the base image of a space that has not chosen
	// one. It is a stock distribution image: the container manager derives a
	// runtime image from it that adds bash/git/python3/coreutils.
	DefaultDeveloperImage = "debian:13.7-slim"

	DeveloperSpacePending = "pending"
	DeveloperSpaceReady   = "ready"
	DeveloperSpaceStopped = "stopped"
	DeveloperSpaceError   = "error"
)

var (
	ErrDeveloperSpaceNotFound = errors.New("developer space not found")
	ErrDeveloperSpaceBusy     = errors.New("developer space has an unresolved control operation")

	developerMemoryPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?[bkmgBKMG]?$`)
	containerImagePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]*$`)
)

// ValidContainerImage accepts a Docker image reference (`debian:13.7-slim`,
// `ghcr.io/org/tool:1.2`, `repo@sha256:…`). It is deliberately strict: the
// value becomes a Dockerfile FROM line and a docker argument, so whitespace,
// a leading dash or any other syntax must never get through.
func ValidContainerImage(ref string) bool {
	return len(ref) <= 255 && containerImagePattern.MatchString(ref)
}

type DeveloperSpaceConfig struct {
	// DisableFormatter turns off formatting files after Developer Space tools
	// edit them from Chats.
	DisableFormatter bool `json:"disable_formatter,omitempty"`
}

type DeveloperSpace struct {
	ID                 string               `json:"id"`
	WorkspaceID        string               `json:"workspace_id"`
	OwnerUserID        string               `json:"owner_user_id"`
	Name               string               `json:"name"`
	Status             string               `json:"status"`
	ExecutionSuspended bool                 `json:"execution_suspended"`
	ActiveControlID    string               `json:"active_control_id,omitempty"`
	Image              string               `json:"image,omitempty"`
	CPULimit           string               `json:"cpu_limit,omitempty"`
	MemoryLimit        string               `json:"memory_limit,omitempty"`
	DiskLimitBytes     int64                `json:"disk_limit_bytes,omitempty"`
	Config             DeveloperSpaceConfig `json:"config"`
	Error              string               `json:"error,omitempty"`
	LastActiveAt       string               `json:"last_active_at,omitempty"`
	CreatedAt          string               `json:"created_at"`
	UpdatedAt          string               `json:"updated_at"`
}

func (v DeveloperSpace) Validate() error {
	if v.DiskLimitBytes < 0 {
		return errors.New("disk_limit_bytes must not be negative")
	}
	if image := strings.TrimSpace(v.Image); image != "" && !ValidContainerImage(image) {
		return errors.New("image must be a Docker image reference such as debian:13.7-slim")
	}
	if cpu := strings.TrimSpace(v.CPULimit); cpu != "" {
		if value, err := strconv.ParseFloat(cpu, 64); err != nil || value <= 0 || value > 256 {
			return errors.New("cpu_limit must be a positive number of cores, e.g. 2 or 0.5")
		}
	}
	if memory := strings.TrimSpace(v.MemoryLimit); memory != "" && !developerMemoryPattern.MatchString(memory) {
		return errors.New("memory_limit must be a size such as 512m or 4g")
	}
	return nil
}

type DeveloperSpaceStorer interface {
	// EnsureDeveloperSpace returns the caller's single space in the selected
	// workspace, creating it on first use.
	EnsureDeveloperSpace(ctx context.Context) (*DeveloperSpace, error)
	GetDeveloperSpace(ctx context.Context, id string) (*DeveloperSpace, error)
	UpdateDeveloperSpace(ctx context.Context, space DeveloperSpace) (*DeveloperSpace, error)
	SetDeveloperSpaceRuntime(ctx context.Context, id, status, runtimeError string) (*DeveloperSpace, error)
	DeleteDeveloperSpace(ctx context.Context, id string) error
}

// DeveloperWorkspaceRoot is the persistent volume mount inside a space.
const DeveloperWorkspaceRoot = "/workspace"

// CleanDeveloperPath canonicalizes a path relative to the space root. The
// empty string is the root itself. Absolute paths, backslashes and parent
// segments are refused rather than resolved, so a typo cannot address a
// different location than the one displayed.
func CleanDeveloperPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, "/")
	if raw == "" || raw == "." {
		return "", nil
	}
	if strings.HasPrefix(raw, "/") || strings.ContainsRune(raw, '\\') || strings.ContainsRune(raw, 0) {
		return "", errors.New("path must be relative to the space")
	}
	for _, part := range strings.Split(raw, "/") {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("path contains an invalid segment")
		}
	}
	if len(raw) > 4096 {
		return "", errors.New("path is too long")
	}
	return raw, nil
}

// DeveloperAbsolutePath joins a cleaned relative path onto the space root.
func DeveloperAbsolutePath(rel string) string {
	if rel == "" {
		return DeveloperWorkspaceRoot
	}
	return path.Join(DeveloperWorkspaceRoot, rel)
}

// ValidDeveloperRemote accepts https, ssh, git and scp-style remotes without
// embedded credentials. Host reachability is checked separately at clone time.
func ValidDeveloperRemote(remote string) error {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return errors.New("remote URL is required")
	}
	if strings.HasPrefix(remote, "-") {
		return errors.New("remote URL is invalid")
	}
	if strings.HasPrefix(remote, "git@") && strings.Contains(remote, ":") {
		return nil
	}
	parsed, err := url.Parse(remote)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "ssh" && parsed.Scheme != "git") {
		return errors.New("remote URL must be an https, ssh, git, or git@host remote")
	}
	if parsed.User != nil {
		return errors.New("remote URL must not contain credentials")
	}
	return nil
}

// ValidDeveloperBranch applies Git's safety-relevant ref-name constraints
// before a value reaches checkout or push argv. Git remains the authority for
// less common repository-specific restrictions.
func ValidDeveloperBranch(branch string) bool {
	if branch == "" || branch == "@" || strings.HasPrefix(branch, "-") || strings.HasPrefix(branch, ".") ||
		strings.HasSuffix(branch, ".") || strings.HasSuffix(branch, "/") || strings.Contains(branch, "..") ||
		strings.Contains(branch, "@{") || strings.Contains(branch, "//") || strings.Contains(branch, "/.") ||
		strings.ContainsAny(branch, "\x00\r\n ~^:?*[\\") {
		return false
	}
	for _, part := range strings.Split(branch, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}
