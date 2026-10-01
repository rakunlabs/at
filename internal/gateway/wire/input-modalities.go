package wire

import (
	"encoding/json"
	"mime"
	"path/filepath"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// RequestInputModalities reports the non-text input modalities an OpenAI-shape
// message list carries. Tool results are included: a browser screenshot in a
// tool result is an image the model has to read.
func RequestInputModalities(msgs []OpenAIMessage) []string {
	found := map[string]bool{}
	for _, msg := range msgs {
		if len(msg.Content) == 0 || msg.Content[0] != '[' {
			continue
		}
		var parts []map[string]any
		if err := json.Unmarshal(msg.Content, &parts); err != nil {
			continue
		}
		for _, part := range parts {
			if m := openAIPartModality(part); m != "" {
				found[m] = true
			}
		}
	}

	return sortedModalities(found)
}

func openAIPartModality(part map[string]any) string {
	typ, _ := part["type"].(string)
	switch typ {
	case "image_url", "input_image", "image":
		return service.ModalityImage
	case "input_audio", "audio":
		return service.ModalityAudio
	case "video_url", "video":
		return service.ModalityVideo
	case "document":
		return mimeModality(nestedString(part, "source", "media_type"))
	case "file", "input_file":
		file, _ := part["file"].(map[string]any)
		if file == nil {
			file = part
		}
		switch data := file["file_data"].(type) {
		case string:
			if mime, _ := parseDataURL(data); mime != "" {
				return mimeModality(mime)
			}
		case map[string]any:
			if mime, _ := data["mime_type"].(string); mime != "" {
				return mimeModality(mime)
			}
		}
		if name, _ := file["filename"].(string); name != "" {
			if byExt := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); byExt != "" {
				return mimeModality(byExt)
			}
		}
		// Chat Completions defines `file` parts as PDF only, so an opaque
		// file_id there is a PDF. A Responses `input_file` can be anything;
		// without a type it is not classified rather than guessed.
		if typ == "file" {
			return service.ModalityPDF
		}
		return ""
	}

	return ""
}

// AnthropicInputModalities is the same check for Anthropic-shape messages,
// including media nested in tool_result blocks.
func AnthropicInputModalities(msgs []AnthropicMessage) []string {
	found := map[string]bool{}
	var walk func(raw any)
	walk = func(raw any) {
		parts, ok := raw.([]any)
		if !ok {
			return
		}
		for _, p := range parts {
			part, ok := p.(map[string]any)
			if !ok {
				continue
			}
			if typ, _ := part["type"].(string); typ == "tool_result" {
				walk(part["content"])
				continue
			}
			if m := openAIPartModality(part); m != "" {
				found[m] = true
			}
		}
	}
	for _, msg := range msgs {
		var content any
		if err := json.Unmarshal(msg.Content, &content); err == nil {
			walk(content)
		}
	}

	return sortedModalities(found)
}

// mimeModality maps a MIME type to the input modality a model must declare
// to read it. Only formats providers parse natively are modalities: PDF,
// images, audio and video. Text files of any kind (txt, md, csv, sql, json,
// source code…) are not a capability — they are text and every model reads
// them. Other binary formats (docx, xlsx…) are not a modality any provider
// declares, so they are left unclassified and the provider decides.
func mimeModality(mimeType string) string {
	mimeType = strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	switch {
	case mimeType == "application/pdf":
		return service.ModalityPDF
	case strings.HasPrefix(mimeType, "image/"):
		return service.ModalityImage
	case strings.HasPrefix(mimeType, "audio/"):
		return service.ModalityAudio
	case strings.HasPrefix(mimeType, "video/"):
		return service.ModalityVideo
	}

	return ""
}

func nestedString(m map[string]any, keys ...string) string {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[k]
	}
	s, _ := cur.(string)

	return s
}

func sortedModalities(found map[string]bool) []string {
	out := []string{}
	for _, m := range service.InputModalities {
		if found[m] {
			out = append(out, m)
		}
	}

	return out
}
