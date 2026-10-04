package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/openai"
	"github.com/rakunlabs/at/internal/service/workflow"
)

const generateImageMaxCount = 4

// execGenerateImage lets any agent — whatever model it runs on — create images
// through a provider that supports image generation (an OpenAI API key, a
// ChatGPT/Codex subscription, MiniMax). Image bytes never enter the
// conversation: they are written to the run's work directory, where the
// artifact collector delivers them, or stored as media for the caller.
func (s *Server) execGenerateImage(ctx context.Context, args map[string]any) (string, error) {
	ref, _ := args["provider"].(string)
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("provider is required (a provider that supports image generation, optionally as provider/model)")
	}
	prompt, _ := args["prompt"].(string)
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("prompt is required")
	}
	providerKey, model := ref, ""
	if i := strings.Index(ref, "/"); i > 0 {
		providerKey, model = ref[:i], ref[i+1:]
	}
	if m, _ := args["model"].(string); strings.TrimSpace(m) != "" {
		model = strings.TrimSpace(m)
	}
	if model == "" {
		model = openai.CodexDefaultImageModel
	}
	n := 1
	if v, ok := args["n"].(float64); ok && v >= 1 {
		n = min(int(v), generateImageMaxCount)
	}
	size, _ := args["size"].(string)
	quality, _ := args["quality"].(string)
	background, _ := args["background"].(string)

	info, err := s.getExecutionProviderInfo(ctx, providerKey)
	if err != nil {
		return "", fmt.Errorf("provider %q: %w", providerKey, err)
	}
	generator, ok := info.provider.(service.ImageProvider)
	if !ok {
		return "", fmt.Errorf("provider %q does not support image generation", providerKey)
	}
	resp, err := generator.GenerateImage(ctx, service.ImageGenerateRequest{
		Prompt: prompt, Model: model, N: n, Size: size, Quality: quality, Background: background,
	})
	if errors.Is(err, service.ErrUnsupportedOperation) {
		return "", fmt.Errorf("provider %q does not support image generation; use an OpenAI (API key or ChatGPT auth) or MiniMax provider", providerKey)
	}
	if err != nil {
		return "", fmt.Errorf("generate image: %w", err)
	}

	images, revised, err := decodeGeneratedImages(ctx, resp.Images)
	if err != nil {
		return "", err
	}
	if len(images) == 0 {
		return "", fmt.Errorf("provider returned no images")
	}

	out := map[string]any{"model": providerKey + "/" + model}
	if len(revised) > 0 {
		out["revised_prompts"] = revised
	}
	if dir := workflow.WorkDirFromContext(ctx); dir != "" {
		files, err := writeGeneratedImages(dir, images)
		if err != nil {
			return "", err
		}
		out["files"] = files
		out["note"] = "Images were saved in the run's work directory; mention them by file name in your answer."
	} else {
		delivered, note, err := s.storeGeneratedImages(ctx, images)
		if err != nil {
			return "", err
		}
		out["artifacts"] = gatewayArtifacts(ctx, delivered)
		if note != "" {
			out["artifacts_note"] = note
		}
		out["note"] = "The images are shown to the user automatically; do not repeat their content."
		if gatewayTokenFromContext(ctx) != nil {
			out["note"] = "Save the images into the project by downloading each artifact's download_url with the same API token (Authorization: Bearer …), e.g. curl -fsSL -H \"Authorization: Bearer $TOKEN\" -o <file> <download_url>."
			if offerGeneratedImagesInline(ctx, images) {
				out["note"] = "The images are attached to this result. " + out["note"].(string)
			}
		}
	}
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode result: %w", err)
	}
	return string(encoded), nil
}

type generatedImageFile struct {
	data []byte
	ext  string
}

// gatewayArtifact adds the token download address to a stored artifact when
// the call came through the gateway.
type gatewayArtifact struct {
	chatArtifact
	DownloadURL string `json:"download_url,omitempty"`
}

func gatewayArtifacts(ctx context.Context, delivered []chatArtifact) []gatewayArtifact {
	out := make([]gatewayArtifact, 0, len(delivered))
	for _, artifact := range delivered {
		out = append(out, gatewayArtifact{chatArtifact: artifact, DownloadURL: gatewayMediaURL(ctx, artifact.MediaID)})
	}
	return out
}

// Inline image content is bounded so one call cannot turn into a response the
// client refuses; larger images remain downloadable.
const generatedImageInlineMaxBytes = 8 << 20

// offerGeneratedImagesInline attaches images as MCP image content when the
// caller can deliver it, so the calling model sees what it generated.
func offerGeneratedImagesInline(ctx context.Context, images []generatedImageFile) bool {
	if service.ToolContentCollectorFromContext(ctx) == nil {
		return false
	}
	total := 0
	added := false
	for _, image := range images {
		total += len(image.data)
		if total > generatedImageInlineMaxBytes {
			break
		}
		mimeType := http.DetectContentType(image.data)
		added = service.AddToolContent(ctx, service.ToolContent{Type: "image", MimeType: mimeType, Data: base64.StdEncoding.EncodeToString(image.data)}) || added
	}
	return added
}

// decodeGeneratedImages accepts base64 results and fetches URL results, so
// every provider ends up as bytes the caller owns. Only image types the media
// store serves inline are accepted.
func decodeGeneratedImages(ctx context.Context, images []service.GeneratedImage) ([]generatedImageFile, []string, error) {
	var files []generatedImageFile
	var revised []string
	for _, image := range images {
		var data []byte
		switch {
		case image.Base64 != "":
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(image.Base64))
			if err != nil {
				return nil, nil, fmt.Errorf("provider returned invalid base64 image data")
			}
			data = decoded
		case image.URL != "":
			fetched, err := fetchGeneratedImage(ctx, image.URL)
			if err != nil {
				return nil, nil, err
			}
			data = fetched
		default:
			continue
		}
		contentType := http.DetectContentType(data)
		ext, ok := mediaAllowedContentTypes[contentType]
		if !ok {
			return nil, nil, fmt.Errorf("provider returned unsupported image type %q", contentType)
		}
		files = append(files, generatedImageFile{data: data, ext: ext})
		if image.RevisedPrompt != "" {
			revised = append(revised, image.RevisedPrompt)
		}
	}
	return files, revised, nil
}

const generatedImageFetchMaxBytes = 64 << 20

func fetchGeneratedImage(ctx context.Context, rawURL string) ([]byte, error) {
	if !strings.HasPrefix(rawURL, "https://") {
		return nil, fmt.Errorf("provider returned a non-HTTPS image URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build image download: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download generated image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download generated image: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, generatedImageFetchMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("download generated image: %w", err)
	}
	if len(data) > generatedImageFetchMaxBytes {
		return nil, fmt.Errorf("generated image exceeds %d bytes", generatedImageFetchMaxBytes)
	}
	return data, nil
}

// writeGeneratedImages saves images into dir with fresh names; it never
// overwrites a file the run already produced.
func writeGeneratedImages(dir string, images []generatedImageFile) ([]string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open work directory: %w", err)
	}
	defer root.Close()
	var paths []string
	for _, image := range images {
		name := "image-" + strings.ToLower(ulid.Make().String()) + image.ext
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return paths, fmt.Errorf("save %s: %w", name, err)
		}
		_, writeErr := file.Write(image.data)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			_ = root.Remove(name)
			return paths, fmt.Errorf("save %s: %w", name, errors.Join(writeErr, closeErr))
		}
		paths = append(paths, filepath.Join(dir, name))
	}
	return paths, nil
}

// storeGeneratedImages stores images as media owned by the run's account, the
// same path Chats uses for skill-run artifacts.
func (s *Server) storeGeneratedImages(ctx context.Context, images []generatedImageFile) ([]chatArtifact, string, error) {
	dir, err := os.MkdirTemp("", "at-generated-image-")
	if err != nil {
		return nil, "", fmt.Errorf("create staging directory: %w", err)
	}
	defer os.RemoveAll(dir)
	paths, err := writeGeneratedImages(dir, images)
	if err != nil {
		return nil, "", err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, "", fmt.Errorf("open staging directory: %w", err)
	}
	defer root.Close()
	pending := make([]pendingArtifact, 0, len(paths))
	for _, p := range paths {
		pending = append(pending, pendingArtifact{name: filepath.Base(p)})
	}
	delivered, note := s.storeChatArtifacts(ctx, root, pending)
	if len(delivered) == 0 {
		if note == "" {
			note = "generated images could not be stored"
		}
		return nil, "", errors.New(note)
	}
	return delivered, note, nil
}
