package server

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/container"
)

// developerHomeKey stores the account's home settings in user_preferences, so
// they follow the account into every workspace and need no migration.
const developerHomeKey = "developer_home"

// developerHome is a persistent home directory owned by one account. Its
// contents live in a Docker volume shared by that account's spaces in every
// workspace; this record only says whether and where it is mounted.
type developerHome struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path,omitempty"`
}

func (h developerHome) path() string {
	if h.Path == "" {
		return container.DefaultHomePath
	}
	return h.Path
}

// developerHomeScope keys the home volume by account only, unlike the space
// scope, which also carries the workspace.
func developerHomeScope(owner string) string { return "developer-home:" + owner }

func (s *Server) loadDeveloperHome(ctx context.Context, owner string) (developerHome, error) {
	if s.userPrefStore == nil || owner == "" {
		return developerHome{}, nil
	}
	pref, err := s.userPrefStore.GetUserPreference(ctx, owner, developerHomeKey)
	if err != nil {
		return developerHome{}, err
	}
	var home developerHome
	if pref != nil && len(pref.Value) > 0 {
		// A value written by a newer version must not stop the space.
		_ = json.Unmarshal(pref.Value, &home)
	}
	if home.Path != "" && !container.ValidHomePath(home.Path) {
		home.Path = ""
	}
	return home, nil
}

func developerHomeResponse(home developerHome) map[string]any {
	return map[string]any{"enabled": home.Enabled, "path": home.path(), "default_path": container.DefaultHomePath}
}

// DeveloperHomeAPI handles GET and PUT /api/v1/developer-space/home. The owner
// is the space owner, i.e. the authenticated account, never a request field.
func (s *Server) DeveloperHomeAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	if s.userPrefStore == nil {
		httpResponse(w, "preference storage unavailable", http.StatusServiceUnavailable)
		return
	}
	space, err := store.EnsureDeveloperSpace(r.Context())
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		home, err := s.loadDeveloperHome(r.Context(), space.OwnerUserID)
		if err != nil {
			httpResponse(w, "home settings unavailable", http.StatusServiceUnavailable)
			return
		}
		httpResponseJSON(w, developerHomeResponse(home), http.StatusOK)
	case http.MethodPut:
		var req developerHome
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpResponse(w, "invalid request body", http.StatusBadRequest)
			return
		}
		req.Path = strings.TrimSpace(req.Path)
		if req.Path == container.DefaultHomePath {
			req.Path = ""
		}
		if req.Path != "" && !container.ValidHomePath(req.Path) {
			httpResponse(w, "home path must be an absolute directory outside /workspace and system directories, such as /root or /home/dev", http.StatusBadRequest)
			return
		}
		encoded, _ := json.Marshal(req)
		if err := s.userPrefStore.SetUserPreference(r.Context(), service.UserPreference{UserID: space.OwnerUserID, Key: developerHomeKey, Value: encoded}); err != nil {
			httpResponse(w, "home settings unavailable", http.StatusServiceUnavailable)
			return
		}
		httpResponseJSON(w, developerHomeResponse(req), http.StatusOK)
	default:
		httpResponse(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// UploadDeveloperHomeFileAPI writes one multipart file into the home, at
// ?path= relative to it. It is copied in without running anything in the
// container (docker cp resolves paths inside the container's own filesystem),
// and mode defaults to 600, which is what SSH keys need.
func (s *Server) UploadDeveloperHomeFileAPI(w http.ResponseWriter, r *http.Request) {
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
	target := strings.TrimSpace(r.FormValue("path"))
	if target == "" {
		target = path.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
	}
	rel, err := service.CleanDeveloperPath(target)
	if err != nil || rel == "" {
		httpResponse(w, "path must be a file path inside the home, such as .ssh/id_ed25519", http.StatusBadRequest)
		return
	}
	mode := fs.FileMode(0o600)
	if raw := strings.TrimSpace(r.FormValue("mode")); raw != "" {
		parsed, err := strconv.ParseUint(raw, 8, 32)
		if err != nil || parsed > 0o777 {
			httpResponse(w, "mode must be an octal permission such as 600 or 644", http.StatusBadRequest)
			return
		}
		mode = fs.FileMode(parsed)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		httpResponse(w, "could not read the upload", http.StatusBadRequest)
		return
	}
	h, ok := s.developerRuntime(w, r)
	if !ok {
		return
	}
	if h.cfg.HomeScope == "" {
		httpResponse(w, "enable the persistent home first", http.StatusConflict)
		return
	}
	dest := path.Join(h.cfg.HomePath, rel)
	if err := s.containerManager.CopyFile(r.Context(), h.scope, h.cfg, dest, data, mode); err != nil {
		httpResponse(w, "could not write into the home: "+err.Error(), http.StatusBadGateway)
		return
	}
	httpResponseJSON(w, map[string]any{"path": dest, "size": len(data), "mode": strconv.FormatUint(uint64(mode), 8)}, http.StatusOK)
}

// removeDeveloperHome deletes a deleted account's home volume. Its space
// containers are removed with it, because Docker refuses to remove a volume
// that a container (even a stopped one) still mounts.
func (s *Server) removeDeveloperHome(ctx context.Context, userID string) {
	if s.containerManager == nil || userID == "" {
		return
	}
	if err := s.containerManager.RemoveHome(ctx, developerHomeScope(userID)); err != nil {
		slog.Warn("account deleted but its developer home could not be removed", "user_id", userID, "error", err.Error())
	}
}

// ResetDeveloperHomeAPI permanently deletes the account's home volume. Space
// containers that mount it are removed; /workspace volumes are untouched.
func (s *Server) ResetDeveloperHomeAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirm bool `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.Confirm {
		httpResponse(w, "resetting the home deletes all of its files; send {\"confirm\": true}", http.StatusBadRequest)
		return
	}
	h, ok := s.developerRuntime(w, r)
	if !ok {
		return
	}
	if err := s.containerManager.RemoveHome(r.Context(), developerHomeScope(h.space.OwnerUserID), h.scope); err != nil {
		httpResponse(w, "could not delete the home: "+err.Error(), http.StatusBadGateway)
		return
	}
	_, _ = h.store.SetDeveloperSpaceRuntime(r.Context(), h.space.ID, service.DeveloperSpaceStopped, "")
	httpResponseJSON(w, map[string]any{"deleted": true}, http.StatusOK)
}
