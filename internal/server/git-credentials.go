package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/rakunlabs/at/internal/service"
)

type gitCredentialResponse struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Host         string   `json:"host"`
	Port         int      `json:"port"`
	PublicKey    string   `json:"public_key"`
	Fingerprints []string `json:"fingerprints"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

type gitHostScanResponse struct {
	Host         string   `json:"host"`
	Port         int      `json:"port"`
	Fingerprints []string `json:"fingerprints"`
}

func (s *Server) ListGitCredentialsAPI(w http.ResponseWriter, r *http.Request) {
	if s.connectionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	connections, err := s.connectionStore.ListConnectionsByProvider(r.Context(), service.GitSSHCredentialProvider)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to list Git credentials: %v", err), http.StatusInternalServerError)
		return
	}
	out := make([]gitCredentialResponse, 0, len(connections))
	for _, connection := range connections {
		out = append(out, gitCredentialPublicResponse(connection))
	}
	httpResponseJSON(w, out, http.StatusOK)
}

func (s *Server) ScanGitHostAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}
	host, port, err := validateGitHost(req.Host, req.Port)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	_, fingerprints, err := scanGitHost(r.Context(), host, port)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to scan Git host: %v", err), http.StatusBadGateway)
		return
	}
	httpResponseJSON(w, gitHostScanResponse{Host: host, Port: port, Fingerprints: fingerprints}, http.StatusOK)
}

func (s *Server) CreateGitCredentialAPI(w http.ResponseWriter, r *http.Request) {
	if s.connectionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	if status, ok := s.connectionStore.(interface{ EncryptionEnabled() bool }); !ok || !status.EncryptionEnabled() {
		httpResponse(w, "Git credentials require database encryption; configure the store encryption key first", http.StatusConflict)
		return
	}
	var req struct {
		Name         string   `json:"name"`
		Host         string   `json:"host"`
		Port         int      `json:"port"`
		Fingerprints []string `json:"fingerprints"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 128 {
		httpResponse(w, "name is required and must be at most 128 bytes", http.StatusBadRequest)
		return
	}
	host, port, err := validateGitHost(req.Host, req.Port)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	knownHosts, fingerprints, err := scanGitHost(r.Context(), host, port)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to verify Git host: %v", err), http.StatusBadGateway)
		return
	}
	if !equalStringSets(req.Fingerprints, fingerprints) {
		httpResponse(w, "Git host fingerprints changed; scan and confirm them again", http.StatusConflict)
		return
	}
	privateKey, publicKey, err := generateGitDeployKey(req.Name)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to generate deploy key: %v", err), http.StatusInternalServerError)
		return
	}
	connection := service.Connection{
		Provider:     service.GitSSHCredentialProvider,
		Name:         req.Name,
		AccountLabel: host,
		Description:  "Read-only Git SSH deploy key",
		Credentials: service.ConnectionCredentials{Extra: map[string]string{
			"private_key": privateKey,
			"known_hosts": knownHosts,
		}},
		Metadata: map[string]any{
			"host": host, "port": port, "public_key": publicKey, "fingerprints": fingerprints,
		},
		CreatedBy: s.getUserEmail(r), UpdatedBy: s.getUserEmail(r),
	}
	record, err := s.connectionStore.CreateConnection(r.Context(), connection)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to create Git credential: %v", err), http.StatusInternalServerError)
		return
	}
	httpResponseJSON(w, gitCredentialPublicResponse(*record), http.StatusCreated)
}

func (s *Server) RotateGitCredentialAPI(w http.ResponseWriter, r *http.Request) {
	if s.connectionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	record, err := s.connectionStore.GetConnection(r.Context(), id)
	if err != nil || record == nil || record.Provider != service.GitSSHCredentialProvider {
		httpResponse(w, "Git credential not found", http.StatusNotFound)
		return
	}
	privateKey, publicKey, err := generateGitDeployKey(record.Name)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to rotate deploy key: %v", err), http.StatusInternalServerError)
		return
	}
	if record.Credentials.Extra == nil {
		record.Credentials.Extra = map[string]string{}
	}
	if record.Metadata == nil {
		record.Metadata = map[string]any{}
	}
	record.Credentials.Extra["private_key"] = privateKey
	record.Metadata["public_key"] = publicKey
	record.UpdatedBy = s.getUserEmail(r)
	updated, err := s.connectionStore.UpdateConnection(r.Context(), id, *record)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to rotate Git credential: %v", err), http.StatusInternalServerError)
		return
	}
	httpResponseJSON(w, gitCredentialPublicResponse(*updated), http.StatusOK)
}

func (s *Server) TestGitCredentialAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryURL string `json:"repository_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.RepositoryURL) == "" {
		httpResponse(w, "repository_url is required", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	env, cleanup, err := s.gitCredentialEnvironment(ctx, req.RepositoryURL, r.PathValue("id"))
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer cleanup()
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--heads", "--", req.RepositoryURL)
	cmd.Env = append(os.Environ(), append([]string{"GIT_TERMINAL_PROMPT=0"}, env...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		httpResponse(w, fmt.Sprintf("repository access failed: %s: %v", strings.TrimSpace(string(out)), err), http.StatusBadGateway)
		return
	}
	httpResponseJSON(w, map[string]any{"ok": true}, http.StatusOK)
}

func (s *Server) DeleteGitCredentialAPI(w http.ResponseWriter, r *http.Request) {
	if s.connectionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	record, err := s.connectionStore.GetConnection(r.Context(), id)
	if err != nil || record == nil || record.Provider != service.GitSSHCredentialProvider {
		httpResponse(w, "Git credential not found", http.StatusNotFound)
		return
	}
	if err := s.connectionStore.DeleteConnection(r.Context(), id); err != nil {
		httpResponse(w, fmt.Sprintf("failed to delete Git credential: %v", err), http.StatusInternalServerError)
		return
	}
	httpResponse(w, "deleted", http.StatusOK)
}

func generateGitDeployKey(name string) (privatePEM, publicAuthorized string, err error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	comment := gitKeyComment(name)
	privateBlock, err := ssh.MarshalPrivateKey(privateKey, comment)
	if err != nil {
		return "", "", err
	}
	sshPublic, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		return "", "", err
	}
	privatePEM = string(pem.EncodeToMemory(privateBlock))
	publicAuthorized = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublic))) + " " + comment
	return privatePEM, publicAuthorized, nil
}

func gitKeyComment(name string) string {
	var b strings.Builder
	b.WriteString("at-")
	lastDash := true
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
			lastDash = r == '-'
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
		if b.Len() >= 64 {
			break
		}
	}
	return strings.TrimRight(b.String(), "-")
}

func scanGitHost(parent context.Context, host string, port int) (string, []string, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh-keyscan", "-T", "5", "-p", strconv.Itoa(port), host)
	out, err := cmd.Output()
	if err != nil {
		return "", nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var valid []string
	var fingerprints []string
	seen := map[string]bool{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		_, _, key, _, _, parseErr := ssh.ParseKnownHosts([]byte(line))
		if parseErr != nil {
			continue
		}
		fingerprint := key.Type() + " " + ssh.FingerprintSHA256(key)
		if !seen[fingerprint] {
			seen[fingerprint] = true
			fingerprints = append(fingerprints, fingerprint)
		}
		valid = append(valid, line)
	}
	if len(valid) == 0 {
		return "", nil, fmt.Errorf("host returned no usable SSH keys")
	}
	sort.Strings(fingerprints)
	return strings.Join(valid, "\n") + "\n", fingerprints, nil
}

func validateGitHost(raw string, port int) (string, int, error) {
	host := strings.TrimSpace(strings.TrimSuffix(raw, "."))
	if port == 0 {
		port = 22
	}
	if host == "" || len(host) > 253 || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("valid host and port are required")
	}
	if net.ParseIP(host) == nil {
		for _, label := range strings.Split(host, ".") {
			if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
				return "", 0, fmt.Errorf("invalid Git host")
			}
			for _, ch := range label {
				if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '-' {
					return "", 0, fmt.Errorf("invalid Git host")
				}
			}
		}
	}
	return strings.ToLower(host), port, nil
}

func gitCredentialPublicResponse(connection service.Connection) gitCredentialResponse {
	port := metadataInt(connection.Metadata["port"], 22)
	return gitCredentialResponse{
		ID: connection.ID, Name: connection.Name, Host: metadataString(connection.Metadata["host"]), Port: port,
		PublicKey: metadataString(connection.Metadata["public_key"]), Fingerprints: metadataStrings(connection.Metadata["fingerprints"]),
		CreatedAt: connection.CreatedAt, UpdatedAt: connection.UpdatedAt,
	}
}

func (s *Server) gitCredentialEnvironment(ctx context.Context, repositoryURL, credentialID string) ([]string, func(), error) {
	if s.connectionStore == nil {
		return nil, nil, fmt.Errorf("Git credential store not configured")
	}
	connection, err := s.connectionStore.GetConnection(ctx, credentialID)
	if err != nil {
		return nil, nil, fmt.Errorf("load Git credential: %w", err)
	}
	if connection == nil || connection.Provider != service.GitSSHCredentialProvider {
		return nil, nil, fmt.Errorf("Git credential not found")
	}
	repositoryHost, repositoryPort, err := gitRepositoryEndpoint(repositoryURL)
	if err != nil {
		return nil, nil, err
	}
	credentialHost := strings.ToLower(metadataString(connection.Metadata["host"]))
	credentialPort := metadataInt(connection.Metadata["port"], 22)
	if repositoryHost != credentialHost || repositoryPort != credentialPort {
		return nil, nil, fmt.Errorf("Git credential for %s:%d cannot be used with repository endpoint %s:%d", credentialHost, credentialPort, repositoryHost, repositoryPort)
	}
	privateKey := connection.Credentials.Extra["private_key"]
	knownHosts := connection.Credentials.Extra["known_hosts"]
	if privateKey == "" || knownHosts == "" {
		return nil, nil, fmt.Errorf("Git credential is incomplete")
	}
	dir, err := os.MkdirTemp("", "at-git-credential-")
	if err != nil {
		return nil, nil, fmt.Errorf("create Git credential files: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	keyPath := filepath.Join(dir, "key")
	hostsPath := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(keyPath, []byte(privateKey), 0o600); err != nil {
		cleanup()
		return nil, nil, err
	}
	if err := os.WriteFile(hostsPath, []byte(knownHosts), 0o600); err != nil {
		cleanup()
		return nil, nil, err
	}
	command := fmt.Sprintf("ssh -p %d -i %s -o IdentitiesOnly=yes -o UserKnownHostsFile=%s -o StrictHostKeyChecking=yes", credentialPort, shellSingleQuote(keyPath), shellSingleQuote(hostsPath))
	return []string{"GIT_SSH_COMMAND=" + command}, cleanup, nil
}

func gitRepositoryEndpoint(raw string) (string, int, error) {
	if parsed, err := url.Parse(raw); err == nil && parsed.Scheme != "" {
		if parsed.Scheme != "ssh" {
			return "", 0, fmt.Errorf("SSH credential requires an ssh:// or user@host:path clone URL")
		}
		if parsed.Hostname() == "" {
			return "", 0, fmt.Errorf("repository URL has no host")
		}
		port := 22
		if parsed.Port() != "" {
			parsedPort, parseErr := strconv.Atoi(parsed.Port())
			if parseErr != nil || parsedPort < 1 || parsedPort > 65535 {
				return "", 0, fmt.Errorf("repository URL has an invalid SSH port")
			}
			port = parsedPort
		}
		return strings.ToLower(parsed.Hostname()), port, nil
	}
	colon := strings.Index(raw, ":")
	if colon <= 0 || strings.Contains(raw[:colon], "/") {
		return "", 0, fmt.Errorf("SSH credential requires an ssh:// or user@host:path clone URL")
	}
	hostPart := raw[:colon]
	if at := strings.LastIndex(hostPart, "@"); at >= 0 {
		hostPart = hostPart[at+1:]
	}
	if hostPart == "" {
		return "", 0, fmt.Errorf("repository URL has no host")
	}
	return strings.ToLower(hostPart), 22, nil
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa, bb := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(aa)
	sort.Strings(bb)
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}

func metadataString(value any) string { v, _ := value.(string); return v }
func metadataInt(value any, fallback int) int {
	switch v := value.(type) {
	case int:
		return v
	case float64:
		return int(v)
	default:
		return fallback
	}
}
func metadataStrings(value any) []string {
	if values, ok := value.([]string); ok {
		return values
	}
	items, _ := value.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if value, ok := item.(string); ok {
			out = append(out, value)
		}
	}
	return out
}
