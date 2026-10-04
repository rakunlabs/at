package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// generateImageGatewayServer is a server whose generate_image calls arrive as
// they do from OpenCode: through the gateway MCP endpoint, under an execution
// identity, with media storage enabled.
func generateImageGatewayServer(t *testing.T) (*Server, *memoryMediaStore, context.Context, *service.ToolContentCollector) {
	t.Helper()
	s, media := artifactServer(t, &fileWritingProvider{}, true)
	s.providers["openai-codex"] = ProviderInfo{provider: &imageCaptureProvider{images: []service.GeneratedImage{{Base64: base64.StdEncoding.EncodeToString(generateImageTestPNG)}}}, providerType: "openai"}
	s.tokenStore = gatewayTestToken("at_good", service.APIToken{ID: "tok-1", WorkspaceID: "w1"})
	ctx := nonHostToolContext(t, "u1", "w1", "r1", "", "generate_image")
	ctx = contextWithGatewayToken(ctx, &service.APIToken{ID: "tok-1", WorkspaceID: "w1"})
	ctx = contextWithGatewayBaseURL(ctx, "https://at.example/at")
	ctx, collector := service.ContextWithToolContentCollector(ctx)
	return s, media, ctx, collector
}

func TestGenerateImageOverGatewayReturnsImageAndDownloadURL(t *testing.T) {
	s, media, ctx, collector := generateImageGatewayServer(t)

	raw, err := s.execGenerateImage(ctx, map[string]any{"provider": "openai-codex", "prompt": "a fox"})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Artifacts []gatewayArtifact `json:"artifacts"`
		Note      string            `json:"note"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Artifacts) != 1 {
		t.Fatalf("artifacts = %s", raw)
	}
	artifact := out.Artifacts[0]
	if artifact.DownloadURL != "https://at.example/at/gateway/v1/media/"+artifact.MediaID {
		t.Fatalf("download_url = %q", artifact.DownloadURL)
	}
	if stored := media.objects[artifact.MediaID]; stored.TokenID != "tok-1" || stored.OwnerUserID != "u1" {
		t.Fatalf("stored object = %+v", stored)
	}
	if !strings.Contains(out.Note, "curl") {
		t.Fatalf("note does not explain how to save the file: %q", out.Note)
	}
	content := collector.Content()
	if len(content) != 1 || content[0].Type != "image" || content[0].MimeType != "image/png" {
		t.Fatalf("image content = %+v", content)
	}
	if decoded, _ := base64.StdEncoding.DecodeString(content[0].Data); string(decoded) != string(generateImageTestPNG) {
		t.Fatal("inline image differs from the generated one")
	}
}

func TestGenerateImageWithoutGatewayHasNoDownloadURL(t *testing.T) {
	s, _ := artifactServer(t, &fileWritingProvider{}, true)
	s.providers["openai-codex"] = ProviderInfo{provider: &imageCaptureProvider{images: []service.GeneratedImage{{Base64: base64.StdEncoding.EncodeToString(generateImageTestPNG)}}}}
	ctx := nonHostToolContext(t, "u1", "w1", "r1", "", "generate_image")
	raw, err := s.execGenerateImage(ctx, map[string]any{"provider": "openai-codex", "prompt": "a fox"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "download_url") {
		t.Fatalf("browser call received a gateway download URL: %s", raw)
	}
}

func TestGatewayMediaDownload(t *testing.T) {
	s, media, ctx, _ := generateImageGatewayServer(t)
	raw, err := s.execGenerateImage(ctx, map[string]any{"provider": "openai-codex", "prompt": "a fox"})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Artifacts []gatewayArtifact `json:"artifacts"`
	}
	_ = json.Unmarshal([]byte(raw), &out)
	id := out.Artifacts[0].MediaID

	// A browser object in the same workspace has no token and is unreachable.
	browser := media.objects[id]
	browser.ID, browser.TokenID = "browser-object", ""
	media.objects[browser.ID] = browser

	tests := []struct {
		name   string
		token  string
		id     string
		status int
	}{
		{name: "owner_token", token: "at_good", id: id, status: http.StatusOK},
		{name: "missing_token", token: "", id: id, status: http.StatusUnauthorized},
		{name: "wrong_token", token: "at_bad", id: id, status: http.StatusUnauthorized},
		{name: "unknown_object", token: "at_good", id: "nope", status: http.StatusNotFound},
		{name: "browser_object", token: "at_good", id: "browser-object", status: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/gateway/v1/media/"+tt.id, nil)
			r.SetPathValue("id", tt.id)
			if tt.token != "" {
				r.Header.Set("Authorization", "Bearer "+tt.token)
			}
			w := httptest.NewRecorder()
			s.GatewayMediaAPI(w, r)
			if w.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", w.Code, tt.status, w.Body)
			}
			if tt.status == http.StatusOK {
				if w.Body.String() != string(generateImageTestPNG) || w.Header().Get("Content-Type") != "image/png" {
					t.Fatalf("download differs: %q %q", w.Header().Get("Content-Type"), w.Body)
				}
				if !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
					t.Fatal("download must not render inline")
				}
			}
		})
	}
}

func TestGatewayMediaDownloadOtherWorkspaceToken(t *testing.T) {
	s, _, ctx, _ := generateImageGatewayServer(t)
	raw, err := s.execGenerateImage(ctx, map[string]any{"provider": "openai-codex", "prompt": "a fox"})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Artifacts []gatewayArtifact `json:"artifacts"`
	}
	_ = json.Unmarshal([]byte(raw), &out)
	// Same token ID, another workspace binding.
	s.tokenStore = gatewayTestToken("at_good", service.APIToken{ID: "tok-1", WorkspaceID: "w2"})
	r := httptest.NewRequest(http.MethodGet, "/gateway/v1/media/x", nil)
	r.SetPathValue("id", out.Artifacts[0].MediaID)
	r.Header.Set("Authorization", "Bearer at_good")
	w := httptest.NewRecorder()
	s.GatewayMediaAPI(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

// The stored file must be the real image, so check the blob too.
func TestGatewayMediaStoredBlob(t *testing.T) {
	s, media, ctx, _ := generateImageGatewayServer(t)
	if _, err := s.execGenerateImage(ctx, map[string]any{"provider": "openai-codex", "prompt": "a fox"}); err != nil {
		t.Fatal(err)
	}
	for _, object := range media.objects {
		data, err := os.ReadFile(filepath.Join(media.settings.Filesystem.Root, filepath.FromSlash(object.StorageKey)))
		if err != nil || string(data) != string(generateImageTestPNG) {
			t.Fatalf("blob %s: %v", object.StorageKey, err)
		}
	}
}
