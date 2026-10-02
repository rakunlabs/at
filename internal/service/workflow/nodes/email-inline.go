package nodes

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/gomarkdown/markdown"
	mdhtml "github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
	"github.com/wneessen/go-mail"
)

// Inline images are embedded as multipart/related parts and referenced from
// an HTML body with src="cid:<id>". Mail clients block data: URIs and remote
// images by default, so this is the form that reliably shows a picture in the
// message itself.

// inlineImage is one embedded image and the Content-ID the body refers to.
type inlineImage struct {
	emailAttachment
	contentID string
}

var contentIDUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// resolveInlineImages loads the inline_images field and input port. Only image
// types are accepted, because the parts are rendered inside the body.
func (n *emailNode) resolveInlineImages(ctx context.Context, inputs map[string]any, tmplCtx map[string]any, funcs map[string]any) ([]inlineImage, error) {
	rendered, err := renderEmailTemplate("inline_images", n.inlineTmpl, tmplCtx, funcs)
	if err != nil {
		return nil, err
	}
	portValue, hasPort := inputs["inline_images"]
	if strings.TrimSpace(rendered) == "" && (!hasPort || portValue == nil) {
		return nil, nil
	}

	runDir, err := runWorkspaceDir(ctx)
	if err != nil {
		return nil, fmt.Errorf("email: inline images need a workflow run workspace: %w", err)
	}
	specs, err := parseAttachmentField(runDir, rendered)
	if err != nil {
		return nil, fmt.Errorf("email: inline images: %w", err)
	}
	if hasPort {
		fromPort, err := parseAttachmentInput(runDir, portValue)
		if err != nil {
			return nil, fmt.Errorf("email: inline images input: %w", err)
		}
		specs = append(specs, fromPort...)
	}
	loaded, err := loadAttachments(ctx, specs)
	if err != nil {
		return nil, fmt.Errorf("email: inline images: %w", err)
	}

	images := make([]inlineImage, 0, len(loaded))
	used := make(map[string]bool, len(loaded))
	for i, a := range loaded {
		if !strings.HasPrefix(a.contentType, "image/") {
			return nil, fmt.Errorf("email: inline image %q is %s, not an image", a.name, a.contentType)
		}
		id := contentIDUnsafe.ReplaceAllString(a.name, "_")
		if id == "" || used[id] {
			id = fmt.Sprintf("image-%d-%s", i+1, id)
		}
		used[id] = true
		images = append(images, inlineImage{emailAttachment: a, contentID: id + "@at"})
	}
	return images, nil
}

// inlineImageFuncs adds {{cid "name"}} / {{cid 0}} for referencing an embedded
// image from the body template, and exposes the list as .inline_images.
func inlineImageFuncs(images []inlineImage, tmplCtx map[string]any, funcs map[string]any) {
	list := make([]any, len(images))
	for i, img := range images {
		list[i] = map[string]any{"name": img.name, "cid": "cid:" + img.contentID, "content_type": img.contentType}
	}
	tmplCtx["inline_images"] = list
	funcs["cid"] = func(key any) (string, error) {
		switch k := key.(type) {
		case int:
			if k >= 0 && k < len(images) {
				return "cid:" + images[k].contentID, nil
			}
			return "", fmt.Errorf("cid: no inline image at index %d", k)
		case string:
			for _, img := range images {
				if img.name == k {
					return "cid:" + img.contentID, nil
				}
			}
			return "", fmt.Errorf("cid: no inline image named %q", k)
		default:
			return "", fmt.Errorf("cid: expected a file name or index, got %T", key)
		}
	}
}

// appendUnreferencedImages adds an <img> for every embedded image the body does
// not reference, so connecting an image is enough to show it.
func appendUnreferencedImages(body string, images []inlineImage) string {
	var b strings.Builder
	for _, img := range images {
		if strings.Contains(body, "cid:"+img.contentID) {
			continue
		}
		fmt.Fprintf(&b, "\n<p><img src=\"cid:%s\" alt=\"%s\" style=\"max-width:100%%;height:auto\"></p>", img.contentID, html.EscapeString(img.name))
	}
	if b.Len() == 0 {
		return body
	}
	if i := strings.LastIndex(strings.ToLower(body), "</body>"); i >= 0 {
		return body[:i] + b.String() + "\n" + body[i:]
	}
	return body + b.String()
}

func embedInMessage(m *mail.Msg, images []inlineImage) error {
	for _, img := range images {
		if err := m.EmbedReader(img.name, bytes.NewReader(img.content),
			mail.WithFileContentType(mail.ContentType(img.contentType)),
			mail.WithFileContentID("<"+img.contentID+">"),
		); err != nil {
			return fmt.Errorf("embed %q: %w", img.name, err)
		}
	}
	return nil
}

// markdownToHTML renders model-written Markdown for an HTML email body. Raw
// HTML in the input is dropped, because the text comes from a model or another
// upstream node rather than from the person who wrote the template.
func markdownToHTML(text string) string {
	p := parser.NewWithExtensions(parser.CommonExtensions | parser.AutoHeadingIDs)
	r := mdhtml.NewRenderer(mdhtml.RendererOptions{Flags: mdhtml.SkipHTML | mdhtml.Safelink | mdhtml.HrefTargetBlank})
	return string(markdown.ToHTML([]byte(text), p, r))
}
