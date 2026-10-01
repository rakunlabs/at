package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

type chatReferenceProvider struct{ messages []service.Message }

func (p *chatReferenceProvider) Chat(_ context.Context, _ string, messages []service.Message, _ []service.Tool, _ *service.ChatOptions) (*service.LLMResponse, error) {
	p.messages = messages
	return &service.LLMResponse{Content: "answer", FinishReason: "stop"}, nil
}
func (p *chatReferenceProvider) Proxy(http.ResponseWriter, *http.Request, string) error { return nil }

func TestChatSavedMessageReferences(t *testing.T) {
	s, _, tokens := playgroundFixture(t)
	p := &chatReferenceProvider{}
	s.providerMu.Lock()
	s.providers = make(map[string]ProviderInfo)
	s.providers["reference"] = ProviderInfo{provider: p, providerType: "openai", models: []string{"model"}}
	s.providerMu.Unlock()
	c := playgroundNewConversation(t, s, tokens[0])
	appendResponse := playgroundRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"messages":[{"client_id":"stable","role":"user","data":{"content":"saved question"}},{"role":"assistant","data":{"content":"saved answer","tool_calls":[{"id":"call-1","type":"function","function":{"name":"test","arguments":"{}"}}]}},{"role":"tool","data":{"content":"tool result","tool_call_id":"call-1"}}]}`)
	if appendResponse.Code != 201 {
		t.Fatalf("append: %d %s", appendResponse.Code, appendResponse.Body)
	}
	m := playgroundMessages(t, appendResponse)
	body := fmt.Sprintf(`{"model":"reference/model","at_conversation_id":%q,"messages":[{"role":"system","content":"current prompt"},{"at_message_id":%q,"tool_calls":[{"id":"injected","type":"function","function":{"name":"injected","arguments":"{}"}}]},{"at_message_id":%q},{"at_message_id":%q},{"role":"user","content":"unsaved question"}]}`, c.ID, m[0].ID, m[1].ID, m[2].ID)
	request := func(token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/at/api/v1/chats/completions", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-AT-Workspace-ID", service.DefaultWorkspaceID)
		w := httptest.NewRecorder()
		s.server.ServeHTTP(w, r)
		return w
	}
	w := request(tokens[0], body)
	if w.Code != 200 {
		t.Fatalf("completion: %d %s", w.Code, w.Body)
	}
	encoded, _ := json.Marshal(p.messages)
	if strings.Contains(string(encoded), "injected") {
		t.Fatalf("reference retained caller fields: %s", encoded)
	}
	for _, want := range []string{"current prompt", "saved question", "saved answer", "tool result", "call-1", "unsaved question"} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("missing %q: %s", want, encoded)
		}
	}
	if w := request(tokens[1], body); w.Code != 404 {
		t.Fatalf("foreign history: %d %s", w.Code, w.Body)
	}
	if w := request(tokens[0], `{"model":"reference/model","messages":[{"at_message_id":42}]}`); w.Code != 400 {
		t.Fatalf("invalid reference: %d %s", w.Code, w.Body)
	}
	if w := playgroundRequest(s, tokens[0], "DELETE", "/"+c.ID+"/messages?from_sequence=2", ""); w.Code != 200 && w.Code != 204 {
		t.Fatalf("truncate: %d %s", w.Code, w.Body)
	}
	if w := request(tokens[0], body); w.Code != 409 {
		t.Fatalf("stale history: %d %s", w.Code, w.Body)
	}
}

func TestChatLazyHistoryIncludesUnseenPrefix(t *testing.T) {
	s, _, tokens := playgroundFixture(t)
	p := &chatReferenceProvider{}
	s.providerMu.Lock()
	s.providers = map[string]ProviderInfo{"reference": {provider: p, providerType: "openai", models: []string{"model"}}}
	s.providerMu.Unlock()
	c := playgroundNewConversation(t, s, tokens[0])
	w := playgroundRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"messages":[{"role":"user","data":{"content":"unseen first question"}},{"role":"assistant","data":{"content":"unseen first answer"}},{"role":"user","data":{"content":"visible question"}}]}`)
	if w.Code != 201 {
		t.Fatalf("append: %d %s", w.Code, w.Body)
	}
	items := playgroundMessages(t, w)
	body := fmt.Sprintf(`{"model":"reference/model","at_conversation_id":%q,"at_history_before":%q,"messages":[{"role":"system","content":"current prompt"},{"at_message_id":%q}]}`, c.ID, items[2].ID, items[2].ID)
	r := httptest.NewRequest("POST", "/at/api/v1/chats/completions", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tokens[0])
	r.Header.Set("X-AT-Workspace-ID", service.DefaultWorkspaceID)
	w = httptest.NewRecorder()
	s.server.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("completion: %d %s", w.Code, w.Body)
	}
	encoded, _ := json.Marshal(p.messages)
	for _, text := range []string{"current prompt", "unseen first question", "unseen first answer", "visible question"} {
		if strings.Count(string(encoded), text) != 1 {
			t.Fatalf("context missing/duplicated %q: %s", text, encoded)
		}
	}
}

func TestChatStoredMediaParts(t *testing.T) {
	for _, tt := range []struct{ kind, mime, want string }{
		{"image", "image/png", "image_url"},
		{"file", "application/pdf", "file"},
		{"file", "audio/mpeg", "input_audio"},
		{"file", "video/mp4", "video_url"},
	} {
		t.Run(tt.want, func(t *testing.T) {
			part := chatStoredMediaPart(tt.kind, "example", tt.mime, "data:"+tt.mime+";base64,AAAA")
			if part["type"] != tt.want {
				t.Fatalf("part: %+v", part)
			}
			if tt.want == "input_audio" {
				audio := part["input_audio"].(map[string]string)
				if audio["data"] != "AAAA" || audio["format"] != "mp3" {
					t.Fatalf("audio: %+v", audio)
				}
			}
		})
	}
}

func TestChatReferencesResolveOnlyOwnedMedia(t *testing.T) {
	s, _, tokens := mediaFixture(t)
	if w := mediaRequest(t, s, tokens[0], "PUT", "/settings", mediaFilesystemBody(1, t.TempDir())); w.Code != 200 {
		t.Fatalf("settings: %d %s", w.Code, w.Body)
	}
	payload := mediaPNG(t)
	objects := make([]service.MediaObject, 2)
	for i := range objects {
		w := mediaUpload(t, s, tokens[i], "image.png", payload, "image/png")
		if w.Code != 201 {
			t.Fatalf("upload: %d %s", w.Code, w.Body)
		}
		if err := json.Unmarshal(w.Body.Bytes(), &objects[i]); err != nil {
			t.Fatal(err)
		}
	}
	p := &chatReferenceProvider{}
	s.providerMu.Lock()
	s.providers = make(map[string]ProviderInfo)
	s.providers["reference"] = ProviderInfo{provider: p, providerType: "openai", models: []string{"model"}}
	s.providerMu.Unlock()
	c := playgroundNewConversation(t, s, tokens[0])
	for i, object := range objects {
		w := playgroundRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", fmt.Sprintf(`{"messages":[{"role":"user","data":{"content":[{"type":"image","media_id":%q,"name":"image.png"}]}}]}`, object.ID))
		if w.Code != 201 {
			t.Fatalf("append: %d %s", w.Code, w.Body)
		}
		m := playgroundMessages(t, w)[0]
		r := httptest.NewRequest("POST", "/at/api/v1/chats/completions", strings.NewReader(fmt.Sprintf(`{"model":"reference/model","at_conversation_id":%q,"messages":[{"at_message_id":%q}]}`, c.ID, m.ID)))
		r.Header.Set("Authorization", "Bearer "+tokens[0])
		r.Header.Set("X-AT-Workspace-ID", service.DefaultWorkspaceID)
		w = httptest.NewRecorder()
		s.server.ServeHTTP(w, r)
		if i == 0 {
			if w.Code != 200 {
				t.Fatalf("owned media: %d %s", w.Code, w.Body)
			}
			encoded, _ := json.Marshal(p.messages)
			if !strings.Contains(string(encoded), base64.StdEncoding.EncodeToString(payload)) {
				t.Fatalf("media not hydrated: %s", encoded)
			}
		} else if w.Code != 409 {
			t.Fatalf("foreign media admitted: %d %s", w.Code, w.Body)
		}
	}
}
