package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/ada/middleware/auth/identity"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/blob"
)

const (
	// mediaUploadMaxBytes caps one stored image. Playground attachments are
	// screenshots and photographs, not video; 16 MiB covers a 6000x4000 PNG
	// while keeping a single request's in-memory payload bounded (the whole
	// body is buffered to hash it and to sign it for S3).
	mediaUploadMaxBytes = 16 << 20
	// mediaMultipartSlackBytes allows for the MIME envelope around the 16 MiB
	// payload (boundaries, part headers, the optional extra form fields) so a
	// legitimately sized image is never rejected for its wrapper.
	mediaMultipartSlackBytes = 64 << 10
	// mediaMultipartMemoryBytes is the in-memory share of the multipart
	// parser; the remainder spills to a temporary file that is removed with
	// the form.
	mediaMultipartMemoryBytes = 1 << 20
	// mediaSettingsBodyMaxBytes bounds a settings request. The document is a
	// handful of short strings.
	mediaSettingsBodyMaxBytes = 64 << 10
	// mediaServeCacheSeconds lets a browser reuse an attachment it just
	// rendered. Objects are immutable (the key contains a ULID), but the
	// cache must stay private: the response is owner scoped.
	mediaServeCacheSeconds = 300
)

// mediaAllowedContentTypes maps the types AT is willing to store to the
// extension used in the storage key. The map is the allowlist: the sniffed
// type of the uploaded bytes must be a key, whatever the client claimed.
var mediaAllowedContentTypes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// mediaSettingsResponse is MediaSettings with the S3 secret removed and
// replaced by a boolean. The secret never leaves the server: it is readable
// only by the uploader inside the process.
type mediaSettingsResponse struct {
	service.MediaSettings
	SecretAccessKeySet bool `json:"secret_access_key_set"`
}

// mediaSettingsRequest mirrors MediaSettings and additionally tolerates the
// read-only secret_access_key_set field, so a client can PUT back exactly the
// document GET returned.
type mediaSettingsRequest struct {
	Version            int64                           `json:"version"`
	Backend            string                          `json:"backend"`
	Filesystem         service.MediaFilesystemSettings `json:"filesystem"`
	S3                 service.MediaS3Settings         `json:"s3"`
	SecretAccessKeySet *bool                           `json:"secret_access_key_set"`
}

func mediaSettingsRedacted(settings service.MediaSettings) mediaSettingsResponse {
	out := mediaSettingsResponse{MediaSettings: settings, SecretAccessKeySet: settings.S3.SecretAccessKey != ""}
	out.MediaSettings.S3.SecretAccessKey = ""
	return out
}

// mediaAccess mirrors playgroundAccess: native authentication must be
// configured and the store must actually implement media storage. Settings are
// administrator-only; object routes only need an authenticated native subject,
// because every object read and write is scoped to that subject. The apiGroup
// middleware is the outer boundary and currently admits administrators only,
// so this handler-level check is deliberately the narrower of the two rather
// than the only one.
func (s *Server) mediaAccess(w http.ResponseWriter, r *http.Request, admin bool) (service.MediaStorer, string) {
	w.Header().Set("Cache-Control", "no-store")
	// Authentication settings live in the database, so the coordinator is
	// resolved per request; the boot-time field is nil on a normal server.
	if nativeRuntimeFromRequest(r, s.nativeAuth) == nil {
		nativeError(w, http.StatusServiceUnavailable, "media storage requires native authentication")
		return nil, ""
	}
	id := identity.FromContext(r.Context())
	if id == nil || id.Subject == "" {
		nativeError(w, http.StatusForbidden, "media storage requires a native user")
		return nil, ""
	}
	if admin && !id.HasRole("admin") {
		nativeError(w, http.StatusForbidden, "media storage settings require a native administrator")
		return nil, ""
	}
	if s.store == nil {
		nativeError(w, http.StatusServiceUnavailable, "store not configured")
		return nil, ""
	}
	store, ok := s.store.(service.MediaStorer)
	if !ok {
		nativeError(w, http.StatusServiceUnavailable, "media storage unavailable")
		return nil, ""
	}
	return store, id.Subject
}

func mediaStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrMediaNotFound):
		nativeError(w, http.StatusNotFound, "media object not found")
	case errors.Is(err, service.ErrMediaConflict):
		nativeError(w, http.StatusConflict, "media settings were changed by someone else; reload and retry")
	default:
		nativeError(w, http.StatusServiceUnavailable, "media storage unavailable")
	}
}

func decodeMediaSettingsBody(w http.ResponseWriter, r *http.Request, dst *mediaSettingsRequest) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, mediaSettingsBodyMaxBytes))
	d.DisallowUnknownFields()
	err := d.Decode(dst)
	if err == nil {
		if trailing := d.Decode(new(any)); !errors.Is(trailing, io.EOF) {
			err = trailing
			if err == nil {
				err = errors.New("trailing JSON")
			}
		}
	}
	if err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			nativeError(w, http.StatusRequestEntityTooLarge, "request body is too large")
			return false
		}
		nativeError(w, http.StatusBadRequest, "invalid media settings request")
		return false
	}
	return true
}

// mediaSettingsFromRequest folds a submitted document onto the stored one. An
// empty incoming secret means "keep the stored secret": the browser never
// receives it, so it cannot echo it back, and requiring a retype on every
// unrelated edit would train administrators to paste secrets around.
func mediaSettingsFromRequest(req mediaSettingsRequest, current service.MediaSettings) service.MediaSettings {
	settings := service.MediaSettings{Version: req.Version, Backend: req.Backend, Filesystem: req.Filesystem, S3: req.S3}
	if settings.S3.SecretAccessKey == "" {
		settings.S3.SecretAccessKey = current.S3.SecretAccessKey
	}
	return settings.Normalized()
}

// MediaSettingsAPI handles GET and PUT /v1/media/settings.
func (s *Server) MediaSettingsAPI(w http.ResponseWriter, r *http.Request) {
	store, _ := s.mediaAccess(w, r, true)
	if store == nil {
		return
	}
	current, err := store.GetMediaSettings(r.Context())
	if err != nil {
		mediaStoreError(w, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		httpResponseJSON(w, mediaSettingsRedacted(*current), http.StatusOK)
	case http.MethodPut:
		var req mediaSettingsRequest
		if !decodeMediaSettingsBody(w, r, &req) {
			return
		}
		settings := mediaSettingsFromRequest(req, *current)
		// Validate here so a malformed configuration is a 400, distinct from
		// the 409 a stale version earns in the store.
		if err := settings.Validate(); err != nil {
			nativeError(w, http.StatusBadRequest, err.Error())
			return
		}
		saved, err := store.SaveMediaSettings(r.Context(), settings)
		if err != nil {
			mediaStoreError(w, err)
			return
		}
		httpResponseJSON(w, mediaSettingsRedacted(*saved), http.StatusOK)
	default:
		nativeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// MediaSettingsTestAPI handles POST /v1/media/settings/test. It probes the
// SUBMITTED settings, not the saved ones, so an administrator can verify a
// bucket before committing a configuration that would break uploads.
func (s *Server) MediaSettingsTestAPI(w http.ResponseWriter, r *http.Request) {
	store, _ := s.mediaAccess(w, r, true)
	if store == nil {
		return
	}
	if r.Method != http.MethodPost {
		nativeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	current, err := store.GetMediaSettings(r.Context())
	if err != nil {
		mediaStoreError(w, err)
		return
	}
	var req mediaSettingsRequest
	if !decodeMediaSettingsBody(w, r, &req) {
		return
	}
	settings := mediaSettingsFromRequest(req, *current)
	if err := settings.Validate(); err != nil {
		nativeError(w, http.StatusBadRequest, err.Error())
		return
	}
	target, err := blob.New(settings)
	if err != nil {
		nativeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// The upstream message is returned verbatim: "NoSuchBucket" and
	// "SignatureDoesNotMatch" are the whole value of this endpoint, and the
	// caller is already an administrator who can read the configuration.
	if err := target.Check(r.Context()); err != nil {
		httpResponseJSON(w, map[string]any{"ok": false, "message": err.Error()}, http.StatusBadGateway)
		return
	}
	httpResponseJSON(w, map[string]any{"ok": true}, http.StatusOK)
}

// mediaBackend resolves the configured backend for a write. A disabled
// installation is a 503 with instructions, not a silent no-op.
func (s *Server) mediaBackend(w http.ResponseWriter, r *http.Request, store service.MediaStorer) (blob.Store, service.MediaSettings, bool) {
	settings, err := store.GetMediaSettings(r.Context())
	if err != nil {
		mediaStoreError(w, err)
		return nil, service.MediaSettings{}, false
	}
	if !settings.Enabled() {
		nativeError(w, http.StatusServiceUnavailable, "media storage is disabled; an administrator must configure a filesystem or s3 backend in media settings")
		return nil, *settings, false
	}
	target, err := blob.New(*settings)
	if err != nil {
		slog.Error("media storage is misconfigured", "backend", settings.Backend, "error", err.Error())
		nativeError(w, http.StatusServiceUnavailable, "media storage is misconfigured; an administrator must fix it in media settings")
		return nil, *settings, false
	}
	return target, *settings, true
}

// mediaStorageKey is always server generated: "<owner>/<ulid><ext>" under the
// configured prefix, with the extension derived from the SNIFFED content type.
// A client filename never reaches the storage layer.
func mediaStorageKey(settings service.MediaSettings, owner, ext string) string {
	key := owner + "/" + ulid.Make().String() + ext
	if settings.Backend == service.MediaBackendS3 {
		return service.NormalizeMediaPrefix(settings.S3.Prefix) + key
	}
	return key
}

// MediaUploadAPI handles POST /v1/media: a multipart upload of one image in
// the "file" field.
func (s *Server) MediaUploadAPI(w http.ResponseWriter, r *http.Request) {
	store, owner := s.mediaAccess(w, r, false)
	if store == nil {
		return
	}
	if r.Method != http.MethodPost {
		nativeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	target, settings, ok := s.mediaBackend(w, r, store)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, mediaUploadMaxBytes+mediaMultipartSlackBytes)
	if err := r.ParseMultipartForm(mediaMultipartMemoryBytes); err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			nativeError(w, http.StatusRequestEntityTooLarge, "image exceeds the 16 MiB limit")
			return
		}
		nativeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll() //nolint:errcheck // best effort
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		nativeError(w, http.StatusBadRequest, "file field is required")
		return
	}
	defer file.Close()
	// One extra byte distinguishes "exactly at the limit" from "over it".
	data, err := io.ReadAll(io.LimitReader(file, mediaUploadMaxBytes+1))
	if err != nil {
		nativeError(w, http.StatusBadRequest, "failed to read the uploaded file")
		return
	}
	if len(data) > mediaUploadMaxBytes {
		nativeError(w, http.StatusRequestEntityTooLarge, "image exceeds the 16 MiB limit")
		return
	}
	if len(data) == 0 {
		nativeError(w, http.StatusBadRequest, "uploaded file is empty")
		return
	}
	// The client's Content-Type is advisory at best and hostile at worst; the
	// bytes decide. Anything that is not a supported image is refused, so a
	// stored object can always be served back with a safe type.
	contentType := strings.TrimSpace(strings.Split(http.DetectContentType(data), ";")[0])
	ext, allowed := mediaAllowedContentTypes[contentType]
	if !allowed {
		nativeError(w, http.StatusUnsupportedMediaType, "only png, jpeg, gif and webp images can be stored")
		return
	}
	key := mediaStorageKey(settings, owner, ext)
	if err := target.Put(r.Context(), key, contentType, data); err != nil {
		// The raw error can name buckets and hosts, so it is logged rather
		// than returned to a user who may not administer the installation.
		slog.Error("media upload failed", "backend", settings.Backend, "key", key, "error", err.Error())
		nativeError(w, http.StatusBadGateway, "media storage rejected the upload")
		return
	}
	sum := sha256.Sum256(data)
	created, err := store.CreateMediaObject(r.Context(), service.MediaObject{
		OwnerUserID: owner,
		Backend:     settings.Backend,
		StorageKey:  key,
		ContentType: contentType,
		SizeBytes:   int64(len(data)),
		Checksum:    hex.EncodeToString(sum[:]),
	})
	if err != nil {
		// Without a row the blob is unreachable, so remove it instead of
		// leaking storage on every failed insert.
		if deleteErr := target.Delete(r.Context(), key); deleteErr != nil {
			slog.Error("orphaned media blob", "key", key, "error", deleteErr.Error())
		}
		mediaStoreError(w, err)
		return
	}
	httpResponseJSON(w, created, http.StatusCreated)
}

// MediaObjectAPI handles GET and DELETE /v1/media/{id}.
func (s *Server) MediaObjectAPI(w http.ResponseWriter, r *http.Request) {
	store, owner := s.mediaAccess(w, r, false)
	if store == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.mediaServe(w, r, store, owner)
	case http.MethodDelete:
		s.mediaDelete(w, r, store, owner)
	default:
		nativeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// mediaServe streams one object. Ownership is resolved before any blob read,
// and a foreign object answers 404, never 403: 403 would confirm that the
// identifier exists.
func (s *Server) mediaServe(w http.ResponseWriter, r *http.Request, store service.MediaStorer, owner string) {
	object, err := store.GetMediaObject(r.Context(), owner, r.PathValue("id"))
	if err != nil {
		mediaStoreError(w, err)
		return
	}
	target, settings, ok := s.mediaBackend(w, r, store)
	if !ok {
		return
	}
	// Objects pin the backend that holds them, so a reconfiguration cannot
	// make AT look for an old object in a new place.
	if object.Backend != settings.Backend {
		nativeError(w, http.StatusConflict, fmt.Sprintf("media object is stored on the %q backend while %q is configured", object.Backend, settings.Backend))
		return
	}
	reader, _, err := target.Get(r.Context(), object.StorageKey)
	if err != nil {
		slog.Error("media read failed", "backend", object.Backend, "key", object.StorageKey, "error", err.Error())
		nativeError(w, http.StatusBadGateway, "media storage could not return the object")
		return
	}
	defer reader.Close()
	// The type recorded at upload time is authoritative: it was sniffed from
	// the bytes and checked against the allowlist. The backend's own claim is
	// ignored, and nosniff stops the browser from inventing a third answer.
	w.Header().Set("Content-Type", object.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(object.SizeBytes, 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// An uploaded file must never run as an active document on the admin origin.
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Cache-Control", fmt.Sprintf("private, max-age=%d", mediaServeCacheSeconds))
	if _, err := io.Copy(w, reader); err != nil {
		slog.Warn("media stream interrupted", "id", object.ID, "error", err.Error())
	}
}

// mediaDelete removes the row first, then the blob: the row is the only thing
// that makes an object reachable, so a failed blob deletion leaves garbage
// rather than a dangling reference.
func (s *Server) mediaDelete(w http.ResponseWriter, r *http.Request, store service.MediaStorer, owner string) {
	object, err := store.DeleteMediaObject(r.Context(), owner, r.PathValue("id"))
	if err != nil {
		mediaStoreError(w, err)
		return
	}
	settings, err := store.GetMediaSettings(r.Context())
	if err == nil && settings.Backend == object.Backend {
		if target, buildErr := blob.New(*settings); buildErr == nil {
			if deleteErr := target.Delete(r.Context(), object.StorageKey); deleteErr != nil {
				slog.Error("media blob deletion failed", "key", object.StorageKey, "error", deleteErr.Error())
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
