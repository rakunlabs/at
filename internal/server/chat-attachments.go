package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rakunlabs/at/internal/service"
)

const (
	chatAttachmentCount        = 4
	chatAttachmentMaxBytes     = 5 << 20
	chatAttachmentsMaxBytes    = 8 << 20
	chatTextAttachmentMaxBytes = 256 << 10 // larger text files use native file input
	chatMessageBodyMaxBytes    = 12 << 20  // base64 envelope plus message text
)

func decodeChatMessage(w http.ResponseWriter, r *http.Request, req *sendChatMessageRequest) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, chatMessageBodyMaxBytes))
	d.DisallowUnknownFields()
	err := d.Decode(req)
	if err == nil {
		if trailing := d.Decode(new(any)); trailing != io.EOF {
			err = trailing
			if err == nil {
				err = fmt.Errorf("expected one message")
			}
		}
	}
	if err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			httpResponse(w, "message and attachments exceed the upload limit", http.StatusRequestEntityTooLarge)
		} else {
			httpResponse(w, "invalid message request", http.StatusBadRequest)
		}
		return false
	}
	if strings.TrimSpace(req.Content) == "" && len(req.Attachments) == 0 {
		httpResponse(w, "a message or attachment is required", http.StatusBadRequest)
		return false
	}
	if len(req.Content) > 256<<10 {
		httpResponse(w, "message text exceeds 256 KiB", http.StatusRequestEntityTooLarge)
		return false
	}
	if err := validateChatAttachments(req.Attachments); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func validateChatAttachments(attachments []service.ChatAttachment) error {
	if len(attachments) > chatAttachmentCount {
		return fmt.Errorf("attach at most 4 files per message")
	}
	total := 0
	for i := range attachments {
		a := &attachments[i]
		if a.Name == "" || len(a.Name) > 255 || !utf8.ValidString(a.Name) || strings.IndexFunc(a.Name, unicode.IsControl) >= 0 {
			return fmt.Errorf("attachment name must be 1–255 bytes without control characters")
		}
		a.Name = path.Base(strings.ReplaceAll(a.Name, "\\", "/"))
		if a.Name == "." || a.Name == ".." || a.Name == "/" {
			return fmt.Errorf("invalid attachment name")
		}
		if len(a.Data) > base64.StdEncoding.EncodedLen(chatAttachmentMaxBytes) {
			return fmt.Errorf("each attachment must be at most 5 MiB")
		}
		data, err := base64.StdEncoding.Strict().DecodeString(a.Data)
		if err != nil || len(data) == 0 {
			return fmt.Errorf("attachment %q has invalid or empty base64 data", a.Name)
		}
		total += len(data)
		if len(data) > chatAttachmentMaxBytes || total > chatAttachmentsMaxBytes {
			return fmt.Errorf("attachments must be at most 5 MiB each and 8 MiB in total")
		}
		// Derive media type from bytes: a client-provided MIME type is not proof.
		kind := strings.Split(http.DetectContentType(data), ";")[0]
		// Office/OpenDocument formats are ZIP containers. Retain their format
		// hint so adapters with native document support can recognize them.
		if kind == "application/zip" {
			switch strings.ToLower(path.Ext(a.Name)) {
			case ".docx", ".xlsx", ".pptx", ".odt", ".ods", ".odp":
				if format := mime.TypeByExtension(path.Ext(strings.ToLower(a.Name))); format != "" {
					kind = strings.Split(format, ";")[0]
				}
			}
		}
		switch kind {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
			a.MediaType = kind
		case "application/pdf":
			a.MediaType = kind
		default:
			if len(data) <= chatTextAttachmentMaxBytes && utf8.Valid(data) && bytes.IndexFunc(data, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) < 0 {
				a.MediaType = "text/plain"
			} else {
				a.MediaType = kind
			}
		}
	}
	return nil
}

// chatMessageContent uses typed canonical blocks on both the initial turn and
// history replay so every adapter applies its normal image/document mapping.
func chatMessageContent(data service.ChatMessageData) any {
	if len(data.Attachments) == 0 {
		return data.Content
	}
	var blocks []service.ContentBlock
	if text, ok := data.Content.(string); ok && text != "" {
		blocks = append(blocks, service.ContentBlock{Type: "text", Text: text})
	}
	for _, a := range data.Attachments {
		blocks = append(blocks, service.ContentBlock{Type: "text", Text: fmt.Sprintf("Attached file: %s", a.Name)})
		if a.MediaType == "text/plain" && len(a.Data) <= base64.StdEncoding.EncodedLen(chatTextAttachmentMaxBytes) {
			raw, _ := base64.StdEncoding.DecodeString(a.Data)
			blocks = append(blocks, service.ContentBlock{Type: "text", Text: string(raw)})
			continue
		}
		kind := "document"
		switch a.MediaType {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
			kind = "image"
		}
		blocks = append(blocks, service.ContentBlock{Type: kind, Source: &service.MediaSource{Type: "base64", MediaType: a.MediaType, Data: a.Data, Filename: a.Name}})
	}
	return blocks
}
