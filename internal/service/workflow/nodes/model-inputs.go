package nodes

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rakunlabs/at/internal/service"
)

// LLM Call and Agent Call accept an "attachments" input: anything upstream produced
// (an HTTP Request download, an Exec output, a generated image, a run path,
// inline base64 or a data: URL) is sent to the model as native content blocks
// next to the prompt. Each provider adapter maps image/document/audio/video
// blocks to its own wire form; a format a model cannot read surfaces as a
// provider error.
//
// Remote URLs are refused rather than fetched: these are non-host nodes, and
// fetching would let a restricted workflow reach the server's network. HTTP
// Request (save_response) downloads a file under its own admission.

const (
	maxModelInputFiles      = 10
	maxModelInputTotalBytes = 20 << 20
	maxModelTextFileBytes   = 256 << 10
)

// modelInput is one resolved file sent inline to the model.
type modelInput struct {
	name        string
	contentType string
	content     []byte
}

// resolveModelInputs accepts every shape Email's attachments port does, plus
// data: URLs. No run workspace is needed for inline data.
func resolveModelInputs(ctx context.Context, value any) ([]modelInput, error) {
	var inline []modelInput
	var specs []attachmentSpec
	var runDir string
	var collect func(v any) error
	collect = func(v any) error {
		switch v := v.(type) {
		case nil:
			return nil
		case []any:
			for _, item := range v {
				if err := collect(item); err != nil {
					return err
				}
			}
			return nil
		case []string:
			for _, item := range v {
				if err := collect(item); err != nil {
					return err
				}
			}
			return nil
		case string:
			for _, line := range strings.FieldsFunc(v, func(r rune) bool { return r == '\n' }) {
				line = strings.TrimSpace(line)
				switch {
				case line == "":
				case strings.HasPrefix(line, "data:"):
					data, err := decodeImageBase64(line)
					if err != nil {
						return fmt.Errorf("data URL: %w", err)
					}
					inline = append(inline, modelInput{name: "file", contentType: dataURIMimeType(line), content: data})
				case strings.HasPrefix(line, "https://"), strings.HasPrefix(line, "http://"):
					return fmt.Errorf("remote URL %q: download it with an HTTP Request node (save response) and connect its file", line)
				default:
					if err := ensureRunDir(ctx, &runDir); err != nil {
						return err
					}
					parsed, err := parseAttachmentField(runDir, line)
					if err != nil {
						return err
					}
					specs = append(specs, parsed...)
				}
			}
			return nil
		case map[string]any:
			if _, inline := v["content_base64"]; !inline {
				if err := ensureRunDir(ctx, &runDir); err != nil {
					return err
				}
			}
			parsed, err := parseAttachmentObject(runDir, v)
			if err != nil {
				return err
			}
			specs = append(specs, parsed...)
			return nil
		default:
			return fmt.Errorf("unsupported file value of type %T", v)
		}
	}
	if err := collect(value); err != nil {
		return nil, err
	}
	if len(specs)+len(inline) > maxModelInputFiles {
		return nil, fmt.Errorf("too many files: %d (maximum %d)", len(specs)+len(inline), maxModelInputFiles)
	}

	var total int64
	out := make([]modelInput, 0, len(specs)+len(inline))
	for _, spec := range specs {
		content := spec.inline
		if !spec.hasInline {
			var err error
			content, err = readWorkspaceFile(ctx, spec.workspacePath, maxModelInputTotalBytes-total)
			if err != nil {
				return nil, fmt.Errorf("file %q: %w", spec.name, err)
			}
		}
		total += int64(len(content))
		if total > maxModelInputTotalBytes {
			return nil, fmt.Errorf("files exceed %d bytes in total", maxModelInputTotalBytes)
		}
		out = append(out, modelInput{name: spec.name, contentType: modelInputContentType(spec.contentType, spec.name, content), content: content})
	}
	for _, f := range inline {
		total += int64(len(f.content))
		if total > maxModelInputTotalBytes {
			return nil, fmt.Errorf("files exceed %d bytes in total", maxModelInputTotalBytes)
		}
		f.contentType = modelInputContentType(f.contentType, f.name, f.content)
		out = append(out, f)
	}
	return out, nil
}

func ensureRunDir(ctx context.Context, runDir *string) error {
	if *runDir != "" {
		return nil
	}
	dir, err := runWorkspaceDir(ctx)
	if err != nil {
		return fmt.Errorf("files need a workflow run workspace: %w", err)
	}
	*runDir = dir
	return nil
}

// modelInputContentType trusts the bytes for media and PDFs; small UTF-8
// text is sent as text whatever its extension says.
func modelInputContentType(explicit, name string, content []byte) string {
	sniffed := strings.TrimSpace(strings.Split(http.DetectContentType(content), ";")[0])
	switch {
	case strings.HasPrefix(sniffed, "image/"), strings.HasPrefix(sniffed, "audio/"), strings.HasPrefix(sniffed, "video/"), sniffed == "application/pdf":
		return sniffed
	}
	if isPlainText(content) {
		return "text/plain"
	}
	return attachmentContentType(explicit, name, content)
}

func isPlainText(content []byte) bool {
	return len(content) <= maxModelTextFileBytes && utf8.Valid(content) &&
		bytes.IndexFunc(content, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) < 0
}

// modelContentBlocks builds the user message: the prompt followed by one
// labelled block per file.
func modelContentBlocks(prompt string, files []modelInput) []service.ContentBlock {
	blocks := []service.ContentBlock{{Type: "text", Text: prompt}}
	for _, f := range files {
		blocks = append(blocks, service.ContentBlock{Type: "text", Text: "Attached file: " + f.name})
		if f.contentType == "text/plain" {
			blocks = append(blocks, service.ContentBlock{Type: "text", Text: string(f.content)})
			continue
		}
		kind := "document"
		switch {
		case strings.HasPrefix(f.contentType, "image/"):
			kind = "image"
		case strings.HasPrefix(f.contentType, "audio/"):
			kind = "audio"
		case strings.HasPrefix(f.contentType, "video/"):
			kind = "video"
		}
		blocks = append(blocks, service.ContentBlock{Type: kind, Source: &service.MediaSource{
			Type: "base64", MediaType: f.contentType, Filename: f.name,
			Data: base64.StdEncoding.EncodeToString(f.content),
		}})
	}
	return blocks
}

// userMessageContent returns the plain prompt when no files are connected, so
// existing workflows send exactly what they did before.
func userMessageContent(ctx context.Context, prompt string, filesInput any) (any, error) {
	if filesInput == nil {
		return prompt, nil
	}
	files, err := resolveModelInputs(ctx, filesInput)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return prompt, nil
	}
	return modelContentBlocks(prompt, files), nil
}
