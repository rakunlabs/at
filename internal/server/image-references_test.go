package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func generatedArtifacts(t *testing.T, raw string) []gatewayArtifact {
	t.Helper()
	var out struct {
		Artifacts []gatewayArtifact `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || len(out.Artifacts) == 0 {
		t.Fatalf("result = %s (%v)", raw, err)
	}
	return out.Artifacts
}

func TestGenerateImageGatewayMarkdownDimensionsAndTTL(t *testing.T) {
	s, media, ctx, _ := generateImageGatewayServer(t)
	raw, err := s.execGenerateImage(ctx, map[string]any{"provider": "openai-codex", "prompt": "a fox", "expires_in_seconds": float64(900)})
	if err != nil {
		t.Fatal(err)
	}
	artifact := generatedArtifacts(t, raw)[0]
	if artifact.Width != 1 || artifact.Height != 1 {
		t.Fatalf("dimensions = %dx%d", artifact.Width, artifact.Height)
	}
	if artifact.Markdown != "!["+artifact.Name+"]("+artifact.DownloadURL+")" {
		t.Fatalf("markdown = %q", artifact.Markdown)
	}
	stored := media.objects[artifact.MediaID]
	if left := time.Until(stored.DownloadExpiresAt); left < 14*time.Minute || left > 15*time.Minute {
		t.Fatalf("key expires in %s, want 15 minutes", left)
	}
	if expires, err := time.Parse(time.RFC3339, artifact.ExpiresAt); err != nil || expires.Sub(stored.DownloadExpiresAt).Abs() > time.Second {
		t.Fatalf("expires_at = %q", artifact.ExpiresAt)
	}
	if !strings.Contains(raw, "15 minutes") || !strings.Contains(raw, "markdown") {
		t.Fatalf("note does not describe the link: %s", raw)
	}
}

func TestGatewayMediaTTLClamped(t *testing.T) {
	ctx := t.Context()
	tests := []struct {
		in   time.Duration
		want time.Duration
	}{
		{0, gatewayMediaKeyTTL},
		{time.Second, gatewayMediaKeyMinTTL},
		{30 * 24 * time.Hour, gatewayMediaKeyMaxTTL},
		{2 * time.Hour, 2 * time.Hour},
	}
	for _, tt := range tests {
		if got := gatewayMediaTTL(contextWithGatewayMediaTTL(ctx, tt.in)); got != tt.want {
			t.Errorf("ttl(%s) = %s, want %s", tt.in, got, tt.want)
		}
	}
	if got := gatewayMediaTTLText(7 * 24 * time.Hour); got != "7 days" {
		t.Errorf("text = %q", got)
	}
}

func TestGenerateImageResourceLinkOnlyForNewClients(t *testing.T) {
	for _, tt := range []struct {
		version string
		links   int
	}{{"2025-06-18", 1}, {"2025-03-26", 0}, {"", 0}} {
		t.Run("v"+tt.version, func(t *testing.T) {
			s, _, ctx, collector := generateImageGatewayServer(t)
			ctx = contextWithMCPProtocolVersion(ctx, tt.version)
			raw, err := s.execGenerateImage(ctx, map[string]any{"provider": "openai-codex", "prompt": "a fox"})
			if err != nil {
				t.Fatal(err)
			}
			artifact := generatedArtifacts(t, raw)[0]
			links := 0
			for _, c := range collector.Content() {
				if c.Type == "resource_link" {
					links++
					if c.URI != artifact.DownloadURL || c.MimeType != "image/png" || c.Name != artifact.Name || c.Size != artifact.SizeBytes {
						t.Fatalf("resource_link = %+v", c)
					}
				}
			}
			if links != tt.links {
				t.Fatalf("resource links = %d, want %d", links, tt.links)
			}
		})
	}
}

func TestGatewayMediaServesImagesInline(t *testing.T) {
	s, _, ctx, _ := generateImageGatewayServer(t)
	raw, err := s.execGenerateImage(ctx, map[string]any{"provider": "openai-codex", "prompt": "a fox"})
	if err != nil {
		t.Fatal(err)
	}
	artifact := generatedArtifacts(t, raw)[0]
	link, _ := url.Parse(artifact.DownloadURL)
	r := httptest.NewRequest(http.MethodGet, "/gateway/v1/media/x?"+link.RawQuery, nil)
	r.SetPathValue("id", artifact.MediaID)
	w := httptest.NewRecorder()
	s.GatewayMediaAPI(w, r)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline") {
		t.Fatalf("status %d disposition %q", w.Code, w.Header().Get("Content-Disposition"))
	}
	if w.Header().Get("Content-Security-Policy") != "sandbox" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("inline image lost its sandbox headers")
	}
}

func TestGenerateImageReferenceImages(t *testing.T) {
	s, _, ctx, _ := generateImageGatewayServer(t)
	provider := s.providers["openai-codex"].provider.(*imageCaptureProvider)
	raw, err := s.execGenerateImage(ctx, map[string]any{"provider": "openai-codex", "prompt": "a fox"})
	if err != nil {
		t.Fatal(err)
	}
	first := generatedArtifacts(t, raw)[0]
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(generateImageTestPNG)

	tests := []struct {
		name string
		ref  string
	}{
		{name: "media_id", ref: first.MediaID},
		{name: "own_download_url", ref: first.DownloadURL},
		{name: "data_url", ref: dataURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider.req = service.ImageGenerateRequest{}
			if _, err := s.execGenerateImage(ctx, map[string]any{"provider": "openai-codex", "prompt": "make it blue", "reference_images": []any{tt.ref}}); err != nil {
				t.Fatal(err)
			}
			refs := provider.req.ReferenceImages
			if len(refs) != 1 || !bytes.Equal(refs[0].Data, generateImageTestPNG) || refs[0].ContentType != "image/png" {
				t.Fatalf("references = %+v", refs)
			}
		})
	}

	errorCases := []struct {
		name string
		refs any
		want string
	}{
		{name: "unknown_media", refs: []any{"01UNKNOWN"}, want: "not found"},
		{name: "too_many", refs: []any{dataURL, dataURL, dataURL, dataURL, dataURL, dataURL}, want: "at most"},
		{name: "plain_http", refs: []any{"http://example.com/a.png"}, want: "https"},
		{name: "not_image", refs: []any{"data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("hello world"))}, want: "unsupported image type"},
		{name: "bad_type", refs: []any{float64(1)}, want: "list of strings"},
	}
	for _, tt := range errorCases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.execGenerateImage(ctx, map[string]any{"provider": "openai-codex", "prompt": "x", "reference_images": tt.refs})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

// A model must not be able to make the server read its own network through
// reference_images.
func TestPublicImageFetchRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(generateImageTestPNG)
	}))
	defer srv.Close()
	if _, err := fetchPublicImage(t.Context(), srv.URL+"/a.png"); err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("loopback fetch err = %v", err)
	}
	for _, ip := range []string{"10.0.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "::1", "fe80::1"} {
		if publicIP(net.ParseIP(ip)) {
			t.Errorf("%s treated as public", ip)
		}
	}
	if !publicIP(net.ParseIP("8.8.8.8")) {
		t.Error("public address refused")
	}
}

func TestImageDimensions(t *testing.T) {
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 320, 200)))
	if w, h := imageDimensions(buf.Bytes()); w != 320 || h != 200 {
		t.Fatalf("png = %dx%d", w, h)
	}
	// VP8L (lossless) header for a 640x480 image.
	bits := uint32(640-1) | uint32(480-1)<<14
	webp := []byte("RIFF\x00\x00\x00\x00WEBPVP8L\x00\x00\x00\x00\x2f")
	webp = append(webp, byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24))
	webp = append(webp, make([]byte, 8)...)
	if w, h := imageDimensions(webp); w != 640 || h != 480 {
		t.Fatalf("webp = %dx%d", w, h)
	}
	if w, h := imageDimensions([]byte("nope")); w != 0 || h != 0 {
		t.Fatal("garbage has dimensions")
	}
}
