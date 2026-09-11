package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/password"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

// mediaFixture returns a server, the shared store and the bearer tokens of
// "admin-a", "admin-b" and the nonadministrator "reader".
func mediaFixture(t *testing.T) (*Server, *postgres.Postgres, []string) {
	t.Helper()
	p := postgrestest.New(t, nil)
	cfg := nativeTestConfig()
	cfg.Workspace = &config.Workspace{Root: t.TempDir(), TTLHours: -1}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	s, err := New(ctx, cfg, nil, p, "postgres", nil, nil, "test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	tokens := []string{}
	for i, name := range []string{"admin-a", "admin-b", "reader"} {
		// The first administrator claims the installation.
		u, err := p.CreateAuthUser(ctx, service.AuthUser{Username: name, Admin: name != "reader", PasswordHash: password.Dummy}, i == 0)
		if err != nil {
			t.Fatal(err)
		}
		access, refresh, err := nativeCredentialPair()
		if err != nil {
			t.Fatal(err)
		}
		if err := p.CreateAuthSession(ctx, service.AuthSession{Hash: name, UserID: u.ID, Version: u.SessionVersion, Transport: "mobile", AccessHash: nativeSessionHash(access), RefreshHash: nativeSessionHash(refresh), ExpiresAt: time.Now().Add(time.Hour), AccessExpiresAt: time.Now().Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, access)
	}
	return s, p, tokens
}

// mediaNoLeak fails the test if a response body carries the S3 secret. It runs
// on every single response the media tests observe, so redaction cannot regress
// on one endpoint while the others stay clean.
func mediaNoLeak(t *testing.T, w *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	if strings.Contains(w.Body.String(), mediaTestSecret) {
		t.Fatalf("response body leaked the secret access key: %s", w.Body)
	}
	return w
}

func mediaRequest(t *testing.T, s *Server, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "/at/api/v1/media"+path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.server.ServeHTTP(w, r)
	return mediaNoLeak(t, w)
}

func mediaUpload(t *testing.T, s *Server, token, filename string, data []byte, declaredType string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	header := make(map[string][]string)
	header["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="file"; filename=%q`, filename)}
	if declaredType != "" {
		header["Content-Type"] = []string{declaredType}
	}
	part, err := form.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/at/api/v1/media", bytes.NewReader(buf.Bytes()))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()
	s.server.ServeHTTP(w, r)
	return mediaNoLeak(t, w)
}

func mediaPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mediaDecodeSettings(t *testing.T, w *httptest.ResponseRecorder) mediaSettingsResponse {
	t.Helper()
	var out mediaSettingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err, w.Body.String())
	}
	return out
}

func mediaFilesystemBody(version int64, root string) string {
	return fmt.Sprintf(`{"version":%d,"backend":"filesystem","filesystem":{"root":%q},"s3":{}}`, version, root)
}

const mediaTestSecret = "do-not-leak-this-secret"

func mediaS3Body(version int64, bucket, secret string) string {
	return fmt.Sprintf(`{"version":%d,"backend":"s3","filesystem":{},"s3":{"endpoint":"https://s3.example.test","region":"us-east-1","bucket":%q,"prefix":"/playground/","access_key_id":"AKIAEXAMPLE","secret_access_key":%q,"use_path_style":true}}`, version, bucket, secret)
}

func TestMediaSettingsHTTPContract(t *testing.T) {
	s, store, tokens := mediaFixture(t)

	for _, tc := range []struct {
		name, token string
		status      int
	}{{"anonymous", "", 401}, {"nonadministrator", tokens[2], 403}} {
		t.Run(tc.name, func(t *testing.T) {
			if w := mediaRequest(t, s, tc.token, "GET", "/settings", ""); w.Code != tc.status {
				t.Fatal(w.Code, w.Body)
			}
		})
	}

	w := mediaRequest(t, s, tokens[0], "GET", "/settings", "")
	settings := mediaDecodeSettings(t, w)
	if w.Code != 200 || settings.Backend != service.MediaBackendDisabled || settings.Version != 1 || settings.SecretAccessKeySet {
		t.Fatalf("default settings: %d %s", w.Code, w.Body)
	}

	for name, body := range map[string]string{
		"relative filesystem root": `{"version":1,"backend":"filesystem","filesystem":{"root":"data/media"},"s3":{}}`,
		"empty filesystem root":    `{"version":1,"backend":"filesystem","filesystem":{},"s3":{}}`,
		"unknown backend":          `{"version":1,"backend":"gcs","filesystem":{},"s3":{}}`,
		"s3 without bucket":        `{"version":1,"backend":"s3","filesystem":{},"s3":{"endpoint":"https://s3.example.test","region":"us-east-1","access_key_id":"k","secret_access_key":"s"}}`,
		"s3 without region":        `{"version":1,"backend":"s3","filesystem":{},"s3":{"endpoint":"https://s3.example.test","bucket":"b","access_key_id":"k","secret_access_key":"s"}}`,
		"s3 bad endpoint scheme":   `{"version":1,"backend":"s3","filesystem":{},"s3":{"endpoint":"ftp://s3.example.test","region":"us-east-1","bucket":"b","access_key_id":"k","secret_access_key":"s"}}`,
		"s3 endpoint with path":    `{"version":1,"backend":"s3","filesystem":{},"s3":{"endpoint":"https://s3.example.test/bucket","region":"us-east-1","bucket":"b","access_key_id":"k","secret_access_key":"s"}}`,
		"s3 without credentials":   `{"version":1,"backend":"s3","filesystem":{},"s3":{"endpoint":"https://s3.example.test","region":"us-east-1","bucket":"b"}}`,
		"unknown field":            `{"version":1,"backend":"filesystem","nonsense":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			if w := mediaRequest(t, s, tokens[0], "PUT", "/settings", body); w.Code != 400 {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
		})
	}

	// A valid write bumps the version and redacts the secret.
	w = mediaRequest(t, s, tokens[0], "PUT", "/settings", mediaS3Body(1, "first-bucket", mediaTestSecret))
	saved := mediaDecodeSettings(t, w)
	if w.Code != 200 || saved.Version != 2 || !saved.SecretAccessKeySet || saved.S3.SecretAccessKey != "" {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	// The prefix is normalised.
	if saved.S3.Prefix != "playground/" {
		t.Fatalf("prefix %q", saved.S3.Prefix)
	}

	w = mediaRequest(t, s, tokens[0], "GET", "/settings", "")
	read := mediaDecodeSettings(t, w)
	if read.Version != 2 || !read.SecretAccessKeySet || read.S3.SecretAccessKey != "" || read.S3.Bucket != "first-bucket" {
		t.Fatalf("read back: %s", w.Body)
	}

	// An empty incoming secret keeps the stored one; the store still holds the
	// real value for the uploader.
	w = mediaRequest(t, s, tokens[0], "PUT", "/settings", mediaS3Body(2, "second-bucket", ""))
	if w.Code != 200 {
		t.Fatalf("keep secret: %d %s", w.Code, w.Body)
	}
	stored, err := store.GetMediaSettings(t.Context())
	if err != nil || stored.S3.SecretAccessKey != mediaTestSecret || stored.S3.Bucket != "second-bucket" || stored.Version != 3 {
		t.Fatalf("stored: %+v %v", stored, err)
	}

	// The document GET returns can be PUT back verbatim.
	body, err := json.Marshal(read)
	if err != nil {
		t.Fatal(err)
	}
	patched := map[string]any{}
	if err := json.Unmarshal(body, &patched); err != nil {
		t.Fatal(err)
	}
	patched["version"] = 3
	round, err := json.Marshal(patched)
	if err != nil {
		t.Fatal(err)
	}
	if w = mediaRequest(t, s, tokens[0], "PUT", "/settings", string(round)); w.Code != 200 {
		t.Fatalf("round trip: %d %s", w.Code, w.Body)
	}

	// A stale version loses.
	if w = mediaRequest(t, s, tokens[0], "PUT", "/settings", mediaS3Body(2, "third-bucket", "")); w.Code != 409 {
		t.Fatalf("stale version: %d %s", w.Code, w.Body)
	}

}

func TestMediaSettingsTest(t *testing.T) {
	s, _, tokens := mediaFixture(t)
	root := filepath.Join(t.TempDir(), "media")

	// A writable filesystem root passes the probe without being saved first.
	w := mediaRequest(t, s, tokens[0], "POST", "/settings/test", mediaFilesystemBody(1, root))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("filesystem probe: %d %s", w.Code, w.Body)
	}
	// The probe must not be saved as a side effect.
	if got := mediaDecodeSettings(t, mediaRequest(t, s, tokens[0], "GET", "/settings", "")); got.Version != 1 || got.Backend != "" {
		t.Fatalf("probe persisted settings: %+v", got)
	}

	// An unreachable S3 endpoint returns the real failure, not a generic one.
	w = mediaRequest(t, s, tokens[0], "POST", "/settings/test",
		`{"version":1,"backend":"s3","filesystem":{},"s3":{"endpoint":"http://127.0.0.1:1","region":"us-east-1","bucket":"b","access_key_id":"k","secret_access_key":"s","use_path_style":true}}`)
	if w.Code != 502 || !strings.Contains(w.Body.String(), "s3 HEAD /b") {
		t.Fatalf("s3 probe: %d %s", w.Code, w.Body)
	}

	// Disabled storage has nothing to probe.
	if w = mediaRequest(t, s, tokens[0], "POST", "/settings/test", `{"version":1,"backend":"","filesystem":{},"s3":{}}`); w.Code != 400 {
		t.Fatalf("disabled probe: %d %s", w.Code, w.Body)
	}
	// An invalid configuration fails validation before any network call.
	if w = mediaRequest(t, s, tokens[0], "POST", "/settings/test", mediaFilesystemBody(1, "relative/media")); w.Code != 400 {
		t.Fatalf("invalid probe: %d %s", w.Code, w.Body)
	}
	if w = mediaRequest(t, s, tokens[2], "POST", "/settings/test", mediaFilesystemBody(1, root)); w.Code != 403 {
		t.Fatalf("nonadministrator probe: %d %s", w.Code, w.Body)
	}
}

func TestMediaObjectsHTTPContract(t *testing.T) {
	s, _, tokens := mediaFixture(t)
	payload := mediaPNG(t)

	// With no backend configured the upload endpoint must say so instead of
	// dropping the image.
	w := mediaUpload(t, s, tokens[0], "shot.png", payload, "image/png")
	if w.Code != 503 || !strings.Contains(w.Body.String(), "media storage is disabled") {
		t.Fatalf("disabled upload: %d %s", w.Code, w.Body)
	}

	root := filepath.Join(t.TempDir(), "media")
	if w = mediaRequest(t, s, tokens[0], "PUT", "/settings", mediaFilesystemBody(1, root)); w.Code != 200 {
		t.Fatalf("configure: %d %s", w.Code, w.Body)
	}

	// The sniffed type decides, never the client's claim.
	if w = mediaUpload(t, s, tokens[0], "payload.png", []byte("#!/bin/sh\necho not an image\n"), "image/png"); w.Code != 415 {
		t.Fatalf("non-image upload: %d %s", w.Code, w.Body)
	}
	// An SVG is an active document, so it is not in the allowlist either.
	if w = mediaUpload(t, s, tokens[0], "x.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), "image/svg+xml"); w.Code != 415 {
		t.Fatalf("svg upload: %d %s", w.Code, w.Body)
	}
	if w = mediaUpload(t, s, tokens[0], "empty.png", nil, "image/png"); w.Code != 400 {
		t.Fatalf("empty upload: %d %s", w.Code, w.Body)
	}
	oversize := append(append([]byte{}, payload...), bytes.Repeat([]byte("a"), mediaUploadMaxBytes+1)...)
	if w = mediaUpload(t, s, tokens[0], "huge.png", oversize, "image/png"); w.Code != 413 {
		t.Fatalf("oversize upload: %d %s", w.Code, w.Body)
	}
	if w = mediaUpload(t, s, tokens[0], "shot.png", payload, "application/octet-stream"); w.Code != 201 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	var object service.MediaObject
	if err := json.Unmarshal(w.Body.Bytes(), &object); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	if object.ID == "" || object.Backend != service.MediaBackendFilesystem || object.ContentType != "image/png" ||
		object.SizeBytes != int64(len(payload)) || object.Checksum != hex.EncodeToString(sum[:]) || object.CreatedAt == "" {
		t.Fatalf("object: %+v", object)
	}
	// The key is server generated from the owner and a ULID; the client
	// filename never reaches storage.
	if !strings.HasPrefix(object.StorageKey, object.OwnerUserID+"/") || !strings.HasSuffix(object.StorageKey, ".png") || strings.Contains(object.StorageKey, "shot") {
		t.Fatalf("storage key %q", object.StorageKey)
	}
	if _, err := os.Stat(filepath.Join(root, object.StorageKey)); err != nil {
		t.Fatalf("blob not written: %v", err)
	}

	// The owner reads it back with hardened headers.
	w = mediaRequest(t, s, tokens[0], "GET", "/"+object.ID, "")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), payload) {
		t.Fatalf("serve: %d %d bytes", w.Code, w.Body.Len())
	}
	for header, want := range map[string]string{
		"Content-Type":            "image/png",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "sandbox",
		"Content-Disposition":     "inline",
		"Cache-Control":           "private, max-age=300",
	} {
		if got := w.Header().Get(header); got != want {
			t.Fatalf("%s: got %q, want %q", header, got, want)
		}
	}

	// Another user's object is missing, not forbidden: 403 would confirm the
	// identifier exists.
	for _, method := range []string{"GET", "DELETE"} {
		if w = mediaRequest(t, s, tokens[1], method, "/"+object.ID, ""); w.Code != 404 {
			t.Fatalf("foreign %s: %d %s", method, w.Code, w.Body)
		}
	}
	if w = mediaRequest(t, s, tokens[0], "GET", "/01HZZZZZZZZZZZZZZZZZZZZZZZ", ""); w.Code != 404 {
		t.Fatalf("unknown object: %d %s", w.Code, w.Body)
	}
	// The foreign delete must not have removed anything.
	if _, err := os.Stat(filepath.Join(root, object.StorageKey)); err != nil {
		t.Fatalf("foreign delete removed the blob: %v", err)
	}

	if w = mediaRequest(t, s, tokens[0], "DELETE", "/"+object.ID, ""); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(root, object.StorageKey)); !os.IsNotExist(err) {
		t.Fatalf("blob survived deletion: %v", err)
	}
	if w = mediaRequest(t, s, tokens[0], "GET", "/"+object.ID, ""); w.Code != 404 {
		t.Fatalf("after delete: %d %s", w.Code, w.Body)
	}
	if w = mediaRequest(t, s, tokens[0], "DELETE", "/"+object.ID, ""); w.Code != 404 {
		t.Fatalf("double delete: %d %s", w.Code, w.Body)
	}
}
