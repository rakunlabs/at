package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/blob"
)

// Reference images are bounded per image (the OpenAI edit endpoints accept up
// to 50 MB; Codex decodes at most 32 MiB) and in total, because every byte is
// held in memory and re-encoded into the upstream request.
const (
	referenceImageMaxBytes   = 32 << 20
	referenceImagesMaxBytes  = 64 << 20
	referenceImageFetchLimit = 30 * time.Second
)

// resolveReferenceImages turns the reference_images argument into image
// bytes. Accepted forms, in order of preference:
//
//   - a media_id the caller can read: an object owned by the run's account,
//     or one produced through the gateway by the calling API token;
//   - this installation's own /gateway/v1/media/{id}?key= download link,
//     resolved locally with the same key check as a download;
//   - a data:image/...;base64 URL;
//   - a public https URL, fetched with private, loopback and link-local
//     destinations refused (also after redirects), so a model cannot make the
//     server read its own network.
func (s *Server) resolveReferenceImages(ctx context.Context, raw any) ([]service.ReferenceImage, error) {
	refs, err := referenceImageArgs(raw)
	if err != nil || len(refs) == 0 {
		return nil, err
	}
	if len(refs) > generateImageMaxReferences {
		return nil, fmt.Errorf("reference_images accepts at most %d images", generateImageMaxReferences)
	}
	out := make([]service.ReferenceImage, 0, len(refs))
	total := 0
	for i, ref := range refs {
		data, err := s.referenceImageBytes(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("reference_images[%d]: %w", i, err)
		}
		total += len(data)
		if total > referenceImagesMaxBytes {
			return nil, fmt.Errorf("reference_images exceed %d MiB in total", referenceImagesMaxBytes>>20)
		}
		contentType := http.DetectContentType(data)
		ext, ok := mediaAllowedContentTypes[contentType]
		if !ok {
			return nil, fmt.Errorf("reference_images[%d]: unsupported image type %q (use PNG, JPEG, GIF or WebP)", i, contentType)
		}
		out = append(out, service.ReferenceImage{Data: data, ContentType: contentType, Name: fmt.Sprintf("reference-%d%s", i+1, ext)})
	}
	return out, nil
}

func referenceImageArgs(raw any) ([]string, error) {
	var refs []string
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case string:
		refs = []string{v}
	case []any:
		for _, item := range v {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("reference_images must be a list of strings")
			}
			refs = append(refs, text)
		}
	case []string:
		refs = v
	default:
		return nil, fmt.Errorf("reference_images must be a list of strings")
	}
	out := refs[:0:0]
	for _, ref := range refs {
		if ref = strings.TrimSpace(ref); ref != "" {
			out = append(out, ref)
		}
	}
	return out, nil
}

func (s *Server) referenceImageBytes(ctx context.Context, ref string) ([]byte, error) {
	switch {
	case strings.HasPrefix(ref, "data:"):
		return decodeImageDataURL(ref)
	case strings.HasPrefix(ref, "https://") || strings.HasPrefix(ref, "http://"):
		if id, key, ok := s.ownGatewayMediaLink(ctx, ref); ok {
			return s.readGatewayMediaByKey(ctx, id, key)
		}
		if !strings.HasPrefix(ref, "https://") {
			return nil, fmt.Errorf("only https URLs are accepted")
		}
		return fetchPublicImage(ctx, ref)
	default:
		return s.readReferenceMedia(ctx, strings.TrimPrefix(ref, service.MediaRefScheme))
	}
}

func decodeImageDataURL(ref string) ([]byte, error) {
	header, payload, ok := strings.Cut(ref, ",")
	if !ok || !strings.HasSuffix(header, ";base64") || !strings.HasPrefix(header, "data:image/") {
		return nil, fmt.Errorf("data URLs must be data:image/<type>;base64,<data>")
	}
	if base64.StdEncoding.DecodedLen(len(payload)) > referenceImageMaxBytes {
		return nil, fmt.Errorf("image exceeds %d MiB", referenceImageMaxBytes>>20)
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	if err != nil {
		return nil, fmt.Errorf("invalid base64 image data")
	}
	return data, nil
}

// ownGatewayMediaLink recognizes a download_url this installation issued.
// It is resolved locally: fetching it over the network would need the server
// to reach its own public address, which many deployments cannot.
func (s *Server) ownGatewayMediaLink(ctx context.Context, ref string) (id, key string, ok bool) {
	base, _ := ctx.Value(gatewayBaseURLContextKey{}).(string)
	link, err := url.Parse(ref)
	if err != nil || link.Query().Get("key") == "" {
		return "", "", false
	}
	prefix := "/gateway/v1/media/"
	if base != "" {
		baseURL, err := url.Parse(base)
		if err != nil || !strings.EqualFold(baseURL.Host, link.Host) {
			return "", "", false
		}
		prefix = strings.TrimSuffix(baseURL.Path, "/") + prefix
	}
	rest, found := strings.CutPrefix(link.Path, prefix)
	if !found || rest == "" || strings.Contains(rest, "/") {
		return "", "", false
	}
	return rest, link.Query().Get("key"), true
}

func (s *Server) readGatewayMediaByKey(ctx context.Context, id, key string) ([]byte, error) {
	gatewayStore, ok := s.store.(service.GatewayMediaStorer)
	if !ok {
		return nil, fmt.Errorf("media storage is not available")
	}
	object, err := gatewayStore.GetGatewayMediaObjectByKey(ctx, id, gatewayMediaKeyHash(key))
	if err != nil {
		if errors.Is(err, service.ErrMediaNotFound) {
			return nil, fmt.Errorf("image not found or its link expired")
		}
		return nil, fmt.Errorf("look up image: %w", err)
	}
	return s.readMediaObject(ctx, object)
}

// readReferenceMedia resolves a media_id with the caller's own visibility:
// the run's account, or the calling API token for gateway media. A foreign or
// unknown ID is reported as not found, never as forbidden.
func (s *Server) readReferenceMedia(ctx context.Context, id string) ([]byte, error) {
	store, ok := s.store.(service.MediaStorer)
	if !ok {
		return nil, fmt.Errorf("media storage is not available")
	}
	var object *service.MediaObject
	err := service.ErrMediaNotFound
	if provenance, _, bound := service.ExecutionFromContext(ctx); bound && provenance.WorkspaceID != "" && provenance.UserID != "" {
		object, err = store.GetMediaObject(ctx, provenance.WorkspaceID, provenance.UserID, id)
	}
	if errors.Is(err, service.ErrMediaNotFound) {
		if token := gatewayTokenFromContext(ctx); token != nil && token.WorkspaceID != "" {
			if gatewayStore, ok := s.store.(service.GatewayMediaStorer); ok {
				object, err = gatewayStore.GetGatewayMediaObject(ctx, token.WorkspaceID, token.ID, id)
			}
		}
	}
	if errors.Is(err, service.ErrMediaNotFound) {
		return nil, fmt.Errorf("media %q not found; pass a media_id from an earlier generate_image result, a download_url, an https URL or a data: URL", id)
	}
	if err != nil {
		return nil, fmt.Errorf("look up media %q: %w", id, err)
	}
	return s.readMediaObject(ctx, object)
}

func (s *Server) readMediaObject(ctx context.Context, object *service.MediaObject) ([]byte, error) {
	store, ok := s.store.(service.MediaStorer)
	if !ok {
		return nil, fmt.Errorf("media storage is not available")
	}
	if object.SizeBytes > referenceImageMaxBytes {
		return nil, fmt.Errorf("image exceeds %d MiB", referenceImageMaxBytes>>20)
	}
	settings, err := store.GetMediaSettings(ctx)
	if err != nil || settings == nil || !settings.Enabled() {
		return nil, fmt.Errorf("media storage is disabled")
	}
	if object.Backend != settings.Backend {
		return nil, fmt.Errorf("image is stored on the %q backend while %q is configured", object.Backend, settings.Backend)
	}
	target, err := blob.New(*settings)
	if err != nil {
		return nil, fmt.Errorf("media storage is misconfigured")
	}
	reader, _, err := target.Get(ctx, object.StorageKey)
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}
	defer reader.Close()
	return readBoundedImage(reader)
}

func readBoundedImage(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, referenceImageMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}
	if len(data) > referenceImageMaxBytes {
		return nil, fmt.Errorf("image exceeds %d MiB", referenceImageMaxBytes>>20)
	}
	return data, nil
}

// publicImageClient refuses non-public destinations at connect time, which
// also covers redirects and DNS answers that change between lookup and dial.
var publicImageClient = &http.Client{
	Timeout: referenceImageFetchLimit,
	Transport: &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second,
			Control: func(_, address string, _ syscall.RawConn) error {
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return err
				}
				if ip := net.ParseIP(host); ip == nil || !publicIP(ip) {
					return fmt.Errorf("refusing to fetch from non-public address %s", host)
				}
				return nil
			},
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		if req.URL.Scheme != "https" {
			return fmt.Errorf("redirect to a non-https URL")
		}
		return nil
	},
}

func publicIP(ip net.IP) bool {
	return !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() &&
		!ip.IsInterfaceLocalMulticast() && !ip.IsMulticast() && !ip.IsUnspecified() && !cgnat.Contains(ip)
}

var _, cgnat, _ = net.ParseCIDR("100.64.0.0/10")

func fetchPublicImage(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid URL")
	}
	resp, err := publicImageClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download image: status %d", resp.StatusCode)
	}
	return readBoundedImage(resp.Body)
}

// imageDimensions reads only the header. The standard library has no WebP
// decoder, so WebP headers are parsed directly rather than adding a module.
func imageDimensions(data []byte) (int, int) {
	if w, h, ok := webpDimensions(data); ok {
		return w, h
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// webpDimensions handles the three WebP bitstream headers (VP8X extended,
// VP8 lossy, VP8L lossless).
func webpDimensions(b []byte) (int, int, bool) {
	if len(b) < 30 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return 0, 0, false
	}
	u24 := func(p []byte) int { return int(p[0]) | int(p[1])<<8 | int(p[2])<<16 }
	switch string(b[12:16]) {
	case "VP8X":
		return 1 + u24(b[24:27]), 1 + u24(b[27:30]), true
	case "VP8 ":
		if b[23] != 0x9d || b[24] != 0x01 || b[25] != 0x2a {
			return 0, 0, false
		}
		return int(b[26]) | int(b[27]&0x3f)<<8, int(b[28]) | int(b[29]&0x3f)<<8, true
	case "VP8L":
		if b[20] != 0x2f {
			return 0, 0, false
		}
		bits := uint32(b[21]) | uint32(b[22])<<8 | uint32(b[23])<<16 | uint32(b[24])<<24
		return int(bits&0x3fff) + 1, int(bits>>14&0x3fff) + 1, true
	}
	return 0, 0, false
}
