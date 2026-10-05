package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// Media produced by a gateway caller (generate_image over MCP, used from
// OpenCode or Claude Code) is recorded against the API token that made the
// call. It can be downloaded two ways:
//
//   - with that token in Authorization, exactly as before;
//   - with the per-object key embedded in the returned download_url. The
//     agent's shell never sees the API token configured in its MCP client, so
//     a link that requires Authorization is unusable to the agent that asked
//     for the image. The key is 256 random bits, only its SHA-256 is stored,
//     it names one object, and it expires after gatewayMediaKeyTTL.
//
// Nothing else is reachable: browser and agent media carry no token ID or key,
// and a foreign, unknown or expired object answers 404 exactly like a missing
// one.

const (
	gatewayMediaKeyTTL     = 24 * time.Hour
	gatewayMediaKeyTTLText = "24 hours"
)

type gatewayTokenContextKey struct{}
type gatewayBaseURLContextKey struct{}

func contextWithGatewayToken(ctx context.Context, token *service.APIToken) context.Context {
	return context.WithValue(ctx, gatewayTokenContextKey{}, token)
}

func gatewayTokenFromContext(ctx context.Context) *service.APIToken {
	token, _ := ctx.Value(gatewayTokenContextKey{}).(*service.APIToken)
	return token
}

func contextWithGatewayBaseURL(ctx context.Context, base string) context.Context {
	return context.WithValue(ctx, gatewayBaseURLContextKey{}, base)
}

// newGatewayMediaKey returns a download key and the hash that is stored.
func newGatewayMediaKey() (key, hash string) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", ""
	}
	key = hex.EncodeToString(raw)
	return key, gatewayMediaKeyHash(key)
}

func gatewayMediaKeyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// gatewayMediaURL is the download address for a token-owned object, or "" when
// the call did not arrive through the gateway. A non-empty key is embedded so
// the link works without credentials.
func gatewayMediaURL(ctx context.Context, id, key string) string {
	base, _ := ctx.Value(gatewayBaseURLContextKey{}).(string)
	if base == "" || gatewayTokenFromContext(ctx) == nil {
		return ""
	}
	link := base + "/gateway/v1/media/" + url.PathEscape(id)
	if key != "" {
		link += "?key=" + url.QueryEscape(key)
	}
	return link
}

// GatewayMediaAPI handles GET /gateway/v1/media/{id}.
func (s *Server) GatewayMediaAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	store, ok := s.store.(service.MediaStorer)
	gatewayStore, gatewayOK := s.store.(service.GatewayMediaStorer)

	var object *service.MediaObject
	var err error
	if key := r.URL.Query().Get("key"); key != "" {
		if !ok || !gatewayOK {
			gatewayMediaError(w, http.StatusServiceUnavailable, "media storage is not available", "unavailable")
			return
		}
		object, err = gatewayStore.GetGatewayMediaObjectByKey(r.Context(), r.PathValue("id"), gatewayMediaKeyHash(key))
	} else {
		auth, errMsg := s.authenticateRequest(r)
		if auth == nil {
			gatewayMediaError(w, http.StatusUnauthorized, errMsg, "invalid_api_key")
			return
		}
		if auth.token == nil || auth.token.WorkspaceID == "" {
			gatewayMediaError(w, http.StatusForbidden, "media downloads require a workspace-bound API token", "permission_denied")
			return
		}
		if !ok || !gatewayOK {
			gatewayMediaError(w, http.StatusServiceUnavailable, "media storage is not available", "unavailable")
			return
		}
		object, err = gatewayStore.GetGatewayMediaObject(r.Context(), auth.token.WorkspaceID, auth.token.ID, r.PathValue("id"))
	}
	if errors.Is(err, service.ErrMediaNotFound) {
		gatewayMediaError(w, http.StatusNotFound, "media object not found or link expired", "not_found")
		return
	}
	if err != nil {
		slog.Error("gateway media lookup failed", "error", err)
		gatewayMediaError(w, http.StatusInternalServerError, "media lookup failed", "internal_error")
		return
	}
	target, settings, ok := s.mediaBackend(w, r, store)
	if !ok {
		return
	}
	if object.Backend != settings.Backend {
		gatewayMediaError(w, http.StatusConflict, fmt.Sprintf("media object is stored on the %q backend while %q is configured", object.Backend, settings.Backend), "conflict")
		return
	}
	reader, _, err := target.Get(r.Context(), object.StorageKey)
	if err != nil {
		slog.Error("gateway media read failed", "backend", object.Backend, "key", object.StorageKey, "error", err.Error())
		gatewayMediaError(w, http.StatusBadGateway, "media storage could not return the object", "upstream_error")
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", object.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(object.SizeBytes, 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	ext := mediaAllowedContentTypes[object.ContentType]
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", object.ID+ext))
	if _, err := io.Copy(w, reader); err != nil {
		slog.Warn("gateway media stream interrupted", "id", object.ID, "error", err.Error())
	}
}

func gatewayMediaError(w http.ResponseWriter, status int, message, code string) {
	httpResponseJSON(w, map[string]any{"error": map[string]any{
		"message": message, "type": "invalid_request_error", "code": code,
	}}, status)
}
