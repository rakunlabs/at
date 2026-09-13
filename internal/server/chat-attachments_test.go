package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

const attachmentTestPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aL1kAAAAASUVORK5CYII="

func TestChatAttachmentValidation(t *testing.T) {
	text := func(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }
	for _, tt := range []struct {
		name     string
		files    []service.ChatAttachment
		wantType string
		invalid  bool
	}{
		{"sniff image instead of claimed type", []service.ChatAttachment{{Name: "photo.png", MediaType: "text/html", Data: attachmentTestPNG}}, "image/png", false},
		{"PDF", []service.ChatAttachment{{Name: "report.pdf", Data: text("%PDF-1.7\nbody")}}, "application/pdf", false},
		{"UTF8 source file", []service.ChatAttachment{{Name: "hello.go", Data: text("package main\n// Merhaba dünya\n")}}, "text/plain", false},
		{"empty", []service.ChatAttachment{{Name: "empty", Data: ""}}, "", true},
		{"invalid base64", []service.ChatAttachment{{Name: "bad", Data: "http://internal.example/file"}}, "", true},
		{"binary uses native file", []service.ChatAttachment{{Name: "file.bin", MediaType: "text/plain", Data: text("\x00\xffbinary")}}, "application/octet-stream", false},
		{"larger text accepted", []service.ChatAttachment{{Name: "file.txt", Data: text(strings.Repeat("a", chatTextAttachmentMaxBytes+1))}}, "text/plain", false},
		{"control in filename", []service.ChatAttachment{{Name: "bad\nname", Data: text("text")}}, "", true},
		{"too many", make([]service.ChatAttachment, 5), "", true},
		{"per file size", []service.ChatAttachment{{Name: "large.pdf", Data: strings.Repeat("A", base64.StdEncoding.EncodedLen(chatAttachmentMaxBytes)+4)}}, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateChatAttachments(tt.files)
			if (err != nil) != tt.invalid {
				t.Fatalf("validation: %v", err)
			}
			if !tt.invalid && tt.files[0].MediaType != tt.wantType {
				t.Fatalf("type: %s", tt.files[0].MediaType)
			}
		})
	}
	large := text("%PDF-1.7\n" + strings.Repeat("a", 5<<20-9))
	if err := validateChatAttachments([]service.ChatAttachment{{Name: "a.pdf", Data: large}, {Name: "b.pdf", Data: large}}); err == nil {
		t.Fatal("aggregate size not bounded")
	}
}

func TestChatAttachmentSendAndHistory(t *testing.T) {
	provider := &fakeObsProvider{responses: []*service.LLMResponse{{Content: "I see your files", Finished: true}, {Content: "Still available", Finished: true}}}
	s, _ := newObsTestServer(t, provider, &fakeLLMCallStore{}, map[string]*service.Agent{"a": {ID: "a", Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 2}}}, nil)
	store := &fakeChatSessionStore{session: service.ChatSession{ID: "session", AgentID: "a"}}
	s.chatSessionStore = store
	files := []service.ChatAttachment{
		{Name: "photo.png", Data: attachmentTestPNG},
		{Name: "note.txt", Data: base64.StdEncoding.EncodeToString([]byte("file content for the model"))},
		{Name: "report.pdf", Data: base64.StdEncoding.EncodeToString([]byte("%PDF-1.7\nbody"))},
	}
	body, _ := json.Marshal(sendChatMessageRequest{Attachments: files})
	r := httptest.NewRequest("POST", "/api/v1/chat/sessions/session/messages", bytes.NewReader(body)).WithContext(s.ctx)
	r.SetPathValue("id", "session")
	w := httptest.NewRecorder()
	s.SendChatMessageAPI(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"type":"done"`) {
		t.Fatalf("send: %d %s", w.Code, w.Body)
	}
	if len(store.messages[0].Data.Attachments) != 3 || store.messages[0].Data.Attachments[0].Data != attachmentTestPNG {
		t.Fatal("files not persisted")
	}
	// Round-trip like PostgreSQL's JSON data column, then execute a text-only turn.
	encoded, _ := json.Marshal(store.messages)
	if err := json.Unmarshal(encoded, &store.messages); err != nil {
		t.Fatal(err)
	}
	if err := s.RunAgenticLoop(s.ctx, "session", "Can you still see the files?", func(AgenticEvent) {}); err != nil {
		t.Fatal(err)
	}
	for turn, request := range provider.requests {
		var image, document, text bool
		for _, m := range request {
			if m.Role != "user" {
				continue
			}
			blocks, _ := m.Content.([]service.ContentBlock)
			for _, b := range blocks {
				if b.Type == "image" && b.Source != nil && b.Source.Data == attachmentTestPNG {
					image = true
				}
				if b.Type == "document" && b.Source != nil && b.Source.MediaType == "application/pdf" {
					document = true
				}
				if b.Type == "text" && b.Text == "file content for the model" {
					text = true
				}
			}
		}
		if !image || !document || !text {
			t.Fatalf("turn %d missing attachment content: image=%v PDF=%v text=%v", turn, image, document, text)
		}
	}
}

func TestChatAttachmentRequestRejectsInvalidBeforeInference(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		status     int
	}{
		{"empty", `{"content":" "}`, 400},
		{"URL injection", `{"attachments":[{"name":"secret","url":"file:///etc/passwd"}]}`, 400},
		{"trailing JSON", `{"content":"hello"}{}`, 400},
		{"body cap", `{"content":"` + strings.Repeat("x", chatMessageBodyMaxBytes) + `"}`, 413},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{chatSessionStore: &fakeChatSessionStore{}}
			r := httptest.NewRequest("POST", "/", strings.NewReader(tt.body))
			r.SetPathValue("id", "session")
			w := httptest.NewRecorder()
			s.SendChatMessageAPI(w, r)
			if w.Code != tt.status {
				t.Fatalf("got %d: %s", w.Code, w.Body)
			}
		})
	}
}
