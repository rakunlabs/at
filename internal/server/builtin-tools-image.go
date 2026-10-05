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
	"slices"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/openai"
	"github.com/rakunlabs/at/internal/service/workflow"
)

const (
	generateImageMaxCount      = 4
	generateImageMaxReferences = 5
)

// generateImageUsage is the tool description shared by the plain built-in
// and the pinned-endpoint variant, so a calling model learns how to use the
// result wherever it reaches the tool.
const generateImageUsage = "Generate images from a text description, or edit/combine existing images when reference_images are given, with a provider that supports image generation: an OpenAI provider (API key, or ChatGPT/Codex subscription auth — billed to the subscription), or MiniMax (generation only). Use it whenever the user asks for a picture, illustration, diagram, logo, mockup or other visual. Write a detailed prompt (subject, style, composition, colours, text to render). " + generateImageResultUsage

const generateImageResultUsage = "Image bytes never appear in the text result; it is JSON describing where the images went (with width/height), and its \"note\" field says what to do next. In an AT chat or agent run the images are delivered to the user automatically (\"artifacts\" with media_id, or \"files\" in the run's work directory): do not repeat their content. Through an external MCP client (OpenCode, Claude Code, an IDE) the user does not see them automatically: each artifact carries a \"download_url\" that needs no credentials (24 hours by default; expires_in_seconds changes it) and a \"markdown\" snippet, and a downscaled preview may be attached so you can check the result. To show an image, put its markdown in your answer; to use it in a project, save the full-resolution file, e.g. curl -fsSL -o assets/hero.png '<download_url>', and reference that path. To refine an image, call again with its media_id in reference_images. Never paste image data or base64 into your answer."

// imageProviderTypes are the provider types whose adapters implement
// service.ImageProvider.
var imageProviderTypes = map[string]bool{"openai": true, "minimax": true}

type imageProviderCandidate struct {
	key, providerType string
}

// imageProviderCandidates lists the providers the caller could generate
// images with, so an omitted provider can be resolved (exactly one) or the
// error can name the valid choices instead of leaving the model to guess.
func (s *Server) imageProviderCandidates(ctx context.Context) []imageProviderCandidate {
	var out []imageProviderCandidate
	if catalogStore, ok := s.store.(service.WorkspaceProviderCatalogStorer); ok {
		catalog, err := catalogStore.ListWorkspaceProviderCatalog(ctx)
		if err == nil {
			for _, entry := range catalog {
				if !imageProviderTypes[entry.Type] {
					continue
				}
				key := entry.Key
				if entry.Reference != "" {
					key = entry.Reference
				}
				out = append(out, imageProviderCandidate{key: key, providerType: entry.Type})
			}
			slices.SortFunc(out, func(a, b imageProviderCandidate) int { return strings.Compare(a.key, b.key) })
			return out
		}
	}
	for key, info := range s.providers {
		if _, ok := info.provider.(service.ImageProvider); ok && !info.disabled {
			out = append(out, imageProviderCandidate{key: key, providerType: info.providerType})
		}
	}
	slices.SortFunc(out, func(a, b imageProviderCandidate) int { return strings.Compare(a.key, b.key) })
	return out
}

func missingImageProviderError(candidates []imageProviderCandidate) error {
	if len(candidates) == 0 {
		return fmt.Errorf("no image-capable provider is available; configure an OpenAI (API key or ChatGPT auth) or MiniMax provider, or pin one under Image generation in the MCP server settings")
	}
	keys := make([]string, 0, len(candidates))
	for _, c := range candidates {
		keys = append(keys, c.key)
	}
	return fmt.Errorf("provider is required; choose one of: %s (or pin one under Image generation in the MCP server settings so callers need not choose)", strings.Join(keys, ", "))
}

// defaultImageModel picks the model when the caller named none. Sending the
// OpenAI default to MiniMax failed every call that omitted the model.
func (s *Server) defaultImageModel(ctx context.Context, providerKey string) string {
	for _, c := range s.imageProviderCandidates(ctx) {
		if c.key == providerKey && c.providerType == "minimax" {
			return "image-01"
		}
	}
	return openai.CodexDefaultImageModel
}

// generateImageToolForConfig adapts the advertised generate_image definition
// to an endpoint's pinned configuration: pinned fields leave the schema, so
// the calling model cannot pick (or be confused by) a provider it does not
// control.
func generateImageToolForConfig(tool service.Tool, cfg *service.ImageGenerationConfig) service.Tool {
	if cfg == nil || (cfg.Provider == "" && cfg.Model == "") {
		return tool
	}
	schema := cloneJSONMap(tool.InputSchema)
	props, _ := schema["properties"].(map[string]any)
	props = cloneJSONMap(props)
	if cfg.Provider != "" {
		delete(props, "provider")
	}
	if cfg.Model != "" {
		delete(props, "model")
	}
	schema["properties"] = props
	schema["required"] = []string{"prompt"}
	tool.InputSchema = schema
	target := cfg.Provider
	if cfg.Model != "" {
		target += " (" + cfg.Model + ")"
	}
	tool.Description = "Generate images from a text description, or edit/combine existing images when reference_images are given. Use it whenever the user asks for a picture, illustration, diagram, logo, mockup or other visual. Write a detailed prompt (subject, style, composition, colours, text to render). This endpoint generates with " + strings.TrimSpace(target) + ". " + generateImageResultUsage
	return tool
}

// applyImageGenerationConfig enforces pinned fields and fills defaults the
// caller left empty. It never mutates the caller's map.
func applyImageGenerationConfig(args map[string]any, cfg *service.ImageGenerationConfig) map[string]any {
	if cfg == nil {
		return args
	}
	out := make(map[string]any, len(args)+4)
	for k, v := range args {
		out[k] = v
	}
	if cfg.Provider != "" {
		out["provider"] = cfg.Provider
	}
	if cfg.Model != "" {
		out["model"] = cfg.Model
	}
	for key, value := range map[string]string{"size": cfg.Size, "quality": cfg.Quality, "background": cfg.Background} {
		if current, _ := out[key].(string); value != "" && strings.TrimSpace(current) == "" {
			out[key] = value
		}
	}
	return out
}

func cloneJSONMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// execGenerateImage lets any agent — whatever model it runs on — create images
// through a provider that supports image generation (an OpenAI API key, a
// ChatGPT/Codex subscription, MiniMax). Image bytes never enter the
// conversation: they are written to the run's work directory, where the
// artifact collector delivers them, or stored as media for the caller.
func (s *Server) execGenerateImage(ctx context.Context, args map[string]any) (string, error) {
	prompt, _ := args["prompt"].(string)
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("prompt is required")
	}
	ref, _ := args["provider"].(string)
	ref = strings.TrimSpace(ref)
	if ref == "" {
		candidates := s.imageProviderCandidates(ctx)
		if len(candidates) != 1 {
			return "", missingImageProviderError(candidates)
		}
		ref = candidates[0].key
	}
	providerKey, model := ref, ""
	if i := strings.Index(ref, "/"); i > 0 {
		providerKey, model = ref[:i], ref[i+1:]
	}
	if m, _ := args["model"].(string); strings.TrimSpace(m) != "" {
		model = strings.TrimSpace(m)
	}
	if model == "" {
		model = s.defaultImageModel(ctx, providerKey)
	}
	n := 1
	if v, ok := args["n"].(float64); ok && v >= 1 {
		n = min(int(v), generateImageMaxCount)
	}
	size, _ := args["size"].(string)
	quality, _ := args["quality"].(string)
	background, _ := args["background"].(string)
	if v, ok := args["expires_in_seconds"].(float64); ok && v > 0 {
		ctx = contextWithGatewayMediaTTL(ctx, time.Duration(v)*time.Second)
	}
	references, err := s.resolveReferenceImages(ctx, args["reference_images"])
	if err != nil {
		return "", err
	}

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
		ReferenceImages: references,
	})
	if errors.Is(err, service.ErrUnsupportedOperation) {
		if len(references) > 0 {
			return "", fmt.Errorf("provider %q cannot edit reference images; use an OpenAI provider (API key or ChatGPT auth)", providerKey)
		}
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
		described := make([]generatedImageEntry, 0, len(files))
		for i, file := range files {
			described = append(described, generatedImageEntry{Path: file, Width: images[i].width, Height: images[i].height})
		}
		out["images"] = described
		out["note"] = "Images were saved in the run's work directory; mention them by file name in your answer."
	} else {
		delivered, note, err := s.storeGeneratedImages(ctx, images)
		if err != nil {
			return "", err
		}
		artifacts := gatewayArtifacts(ctx, delivered, images)
		out["artifacts"] = artifacts
		if note != "" {
			out["artifacts_note"] = note
		}
		out["note"] = "The images are shown to the user automatically; do not repeat their content."
		if gatewayTokenFromContext(ctx) != nil {
			ttl := gatewayMediaTTLText(gatewayMediaTTL(ctx))
			out["note"] = fmt.Sprintf("The user does not see these images yet. To show one, put its \"markdown\" in your answer. To save one into the project, download its download_url (no credentials needed; valid for %s), e.g. curl -fsSL -o <file> '<download_url>'. To refine one, call generate_image again with its media_id in reference_images.", ttl)
			if offerGeneratedImagesInline(ctx, images) {
				out["note"] = "A preview of each image is attached to this result; the download is the full-resolution file. " + out["note"].(string)
			}
			offerGeneratedImageLinks(ctx, artifacts, ttl)
		}
	}
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode result: %w", err)
	}
	return string(encoded), nil
}

type generatedImageFile struct {
	data          []byte
	ext           string
	contentType   string
	width, height int
}

// generatedImageEntry describes an image saved into a run's work directory.
type generatedImageEntry struct {
	Path   string `json:"path"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// gatewayArtifact adds dimensions and, through the gateway, the download
// address and a ready-to-paste Markdown image to a stored artifact.
type gatewayArtifact struct {
	chatArtifact
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	DownloadURL string `json:"download_url,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	Markdown    string `json:"markdown,omitempty"`
}

func gatewayArtifacts(ctx context.Context, delivered []chatArtifact, images []generatedImageFile) []gatewayArtifact {
	out := make([]gatewayArtifact, 0, len(delivered))
	for i, artifact := range delivered {
		entry := gatewayArtifact{chatArtifact: artifact}
		// storeChatArtifacts keeps order and only drops failures at the end
		// of a partial batch, so the index still matches when lengths agree.
		if len(delivered) == len(images) {
			entry.Width, entry.Height = images[i].width, images[i].height
		}
		entry.DownloadURL = gatewayMediaURL(ctx, artifact.MediaID, artifact.downloadKey)
		if entry.DownloadURL != "" {
			if !artifact.downloadExpires.IsZero() {
				entry.ExpiresAt = artifact.downloadExpires.UTC().Format(time.RFC3339)
			}
			entry.Markdown = fmt.Sprintf("![%s](%s)", markdownAltText(artifact.Name), entry.DownloadURL)
		}
		out = append(out, entry)
	}
	return out
}

func markdownAltText(name string) string {
	return strings.NewReplacer("[", "", "]", "", "\n", " ").Replace(name)
}

// offerGeneratedImageLinks attaches each download as an MCP resource_link,
// which clients on protocol 2025-06-18 or later can fetch or display
// themselves. Older clients only get the JSON text, since an unknown content
// type may fail their whole result.
func offerGeneratedImageLinks(ctx context.Context, artifacts []gatewayArtifact, ttl string) {
	if !mcpSupportsResourceLinks(ctx) {
		return
	}
	for _, artifact := range artifacts {
		if artifact.DownloadURL == "" {
			continue
		}
		service.AddToolContent(ctx, service.ToolContent{
			Type:        "resource_link",
			URI:         artifact.DownloadURL,
			Name:        artifact.Name,
			MimeType:    artifact.ContentType,
			Size:        artifact.SizeBytes,
			Description: "Generated image (full resolution); the link needs no credentials and expires in " + ttl + ".",
		})
	}
}

// Inline image content is bounded so one call cannot turn into a response the
// client refuses; larger images remain downloadable.
const generatedImageInlineMaxBytes = 8 << 20

// offerGeneratedImagesInline attaches images as MCP image content when the
// caller can deliver it, so the calling model sees what it generated. The
// attachment is a downscaled preview: it enters the caller's context and is
// resent on every later turn, so a full-resolution PNG (about 1 MB of base64)
// would cost far more than seeing the result requires.
func offerGeneratedImagesInline(ctx context.Context, images []generatedImageFile) bool {
	if service.ToolContentCollectorFromContext(ctx) == nil {
		return false
	}
	total := 0
	added := false
	for _, image := range images {
		data, mimeType := imagePreview(image.data)
		total += len(data)
		if total > generatedImageInlineMaxBytes {
			break
		}
		added = service.AddToolContent(ctx, service.ToolContent{Type: "image", MimeType: mimeType, Data: base64.StdEncoding.EncodeToString(data)}) || added
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
		file := generatedImageFile{data: data, ext: ext, contentType: contentType}
		file.width, file.height = imageDimensions(data)
		files = append(files, file)
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
