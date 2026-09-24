package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/blob"
)

func TestBuildChatShareSnapshotPortableOptions(t *testing.T) {
	conversation := &service.PlaygroundConversation{Title: "Source", SystemPrompt: "private prompt", ProviderKey: "provider:private", Model: "secret-model", Config: map[string]any{"mcp_headers": map[string]any{"authorization": "secret"}}}
	messages := []service.PlaygroundMessage{
		{Sequence: 1, Role: "user", Data: map[string]any{"content": []any{map[string]any{"type": "text", "text": "look"}, map[string]any{"type": "image", "media_id": "media-source"}}, "private": "drop me"}},
		{Sequence: 2, Role: "assistant", Data: map[string]any{"content": "checking", "tool_calls": []any{map[string]any{"id": "call-1", "type": "function", "function": map[string]any{"name": "lookup", "arguments": "{}"}}}}},
		{Sequence: 3, Role: "tool", Data: map[string]any{"tool_call_id": "call-1", "name": "lookup", "content": "tool secret"}},
		{Sequence: 4, Role: "assistant", Data: map[string]any{"content": "done", "usage": map[string]any{"tokens": 99}}},
	}

	minimal, media, err := buildChatShareSnapshot(conversation, messages, service.ChatShareOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if minimal.SystemPrompt != "" || len(media) != 0 || len(minimal.Messages) != 3 {
		t.Fatalf("minimal snapshot: %+v media=%v", minimal, media)
	}
	if _, exists := minimal.Messages[1].Data["tool_calls"]; exists {
		t.Fatal("tool calls survived while tool outputs were excluded")
	}
	encoded := mustJSON(t, minimal)
	for _, forbidden := range []string{"authorization", "drop me", "usage", "tool secret", "media-source"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("snapshot leaked %q: %s", forbidden, encoded)
		}
	}

	full, media, err := buildChatShareSnapshot(conversation, messages, service.ChatShareOptions{IncludeSystemPrompt: true, IncludeToolOutputs: true, IncludeAttachments: true})
	if err != nil {
		t.Fatal(err)
	}
	if full.SystemPrompt != "private prompt" || len(full.Messages) != 4 || len(media) != 1 || media[0] != "media-source" {
		t.Fatalf("full snapshot: %+v media=%v", full, media)
	}
}

func TestChatShareMediaCopiesOutliveSourceAndRevocation(t *testing.T) {
	s, owners, tokens := playgroundFixture(t)
	mediaStore := s.store.(service.MediaStorer)
	settings, err := mediaStore.SaveMediaSettings(t.Context(), service.MediaSettings{Version: 1, Backend: service.MediaBackendFilesystem, Filesystem: service.MediaFilesystemSettings{Root: filepath.Join(t.TempDir(), "media")}})
	if err != nil {
		t.Fatal(err)
	}
	target, err := blob.New(*settings)
	if err != nil {
		t.Fatal(err)
	}
	sourceKey := mediaStorageKey(*settings, owners[0], ".png")
	sourceBytes := []byte("portable-image")
	if err := target.Put(t.Context(), sourceKey, "image/png", sourceBytes); err != nil {
		t.Fatal(err)
	}
	source, err := mediaStore.CreateMediaObject(t.Context(), service.MediaObject{OwnerUserID: owners[0], Backend: settings.Backend, StorageKey: sourceKey, ContentType: "image/png", SizeBytes: int64(len(sourceBytes)), Checksum: "sum"})
	if err != nil {
		t.Fatal(err)
	}
	conversation := playgroundNewConversation(t, s, tokens[0])
	body := fmt.Sprintf(`{"messages":[{"role":"user","data":{"content":[{"type":"image","media_id":%q}]}},{"role":"assistant","data":{"content":"seen"}}]}`, source.ID)
	if response := playgroundRequest(s, tokens[0], "POST", "/"+conversation.ID+"/messages", body); response.Code != 201 {
		t.Fatal(response.Code, response.Body)
	}
	published := chatShareRequestHTTP(s, tokens[0], "POST", "conversations/"+conversation.ID+"/shares", `{"through_sequence":2,"options":{"include_attachments":true}}`)
	if published.Code != 201 {
		t.Fatalf("publish: %d %s", published.Code, published.Body)
	}
	var share service.ChatShare
	if err := json.Unmarshal(published.Body.Bytes(), &share); err != nil {
		t.Fatal(err)
	}
	sharedMedia := collectSnapshotMediaIDs(share.Payload)
	if len(sharedMedia) != 1 || sharedMedia[0] == source.ID {
		t.Fatalf("media was not copied: source=%s shared=%v", source.ID, sharedMedia)
	}
	deleted, err := mediaStore.DeleteMediaObject(t.Context(), owners[0], source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Delete(t.Context(), deleted.StorageKey); err != nil {
		t.Fatal(err)
	}
	served := chatShareRequestHTTP(s, tokens[1], "GET", "shares/"+share.ID+"/media/"+sharedMedia[0], "")
	if served.Code != 200 || served.Body.String() != string(sourceBytes) {
		t.Fatalf("share media: %d %q", served.Code, served.Body.String())
	}
	imported := chatShareRequestHTTP(s, tokens[1], "POST", "shares/"+share.ID+"/import", `{"version":1}`)
	if imported.Code != 201 {
		t.Fatalf("import: %d %s", imported.Code, imported.Body)
	}
	var copy service.PlaygroundConversation
	if err := json.Unmarshal(imported.Body.Bytes(), &copy); err != nil {
		t.Fatal(err)
	}
	messages := playgroundMessages(t, playgroundRequest(s, tokens[1], "GET", "/"+copy.ID+"/messages", ""))
	recipientMedia := map[string]struct{}{}
	for _, message := range messages {
		collectChatMediaIDs(message.Data, recipientMedia)
	}
	if len(recipientMedia) != 1 {
		t.Fatalf("imported media: %+v", messages)
	}
	if response := chatShareRequestHTTP(s, tokens[0], "DELETE", "shares/"+share.ID, ""); response.Code != 204 {
		t.Fatal(response.Code, response.Body)
	}
	for id := range recipientMedia {
		r := httptest.NewRequest("GET", "/at/api/v1/media/"+id, nil)
		r.Header.Set("Authorization", "Bearer "+tokens[1])
		r.Header.Set("X-AT-Workspace-ID", service.DefaultWorkspaceID)
		w := httptest.NewRecorder()
		s.server.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != string(sourceBytes) {
			t.Fatalf("recipient media after revoke: %d %q", w.Code, w.Body.String())
		}
	}
}

func chatShareRequestHTTP(s *Server, token, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/at/api/v1/chats/"+path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-AT-Workspace-ID", service.DefaultWorkspaceID)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.server.ServeHTTP(w, r)
	return w
}

func TestChatShareHTTPPublishImportRevoke(t *testing.T) {
	s, owners, tokens := playgroundFixture(t)
	conversation := playgroundNewConversation(t, s, tokens[0])
	appendResponse := playgroundRequest(s, tokens[0], "POST", "/"+conversation.ID+"/messages", `{"messages":[{"role":"user","provider_key":"provider:private","model":"private-model","data":{"content":"question","secret":"drop"}},{"role":"assistant","provider_key":"provider:private","model":"private-model","data":{"content":"answer"}}]}`)
	if appendResponse.Code != 201 {
		t.Fatalf("append: %d %s", appendResponse.Code, appendResponse.Body)
	}

	preview := chatShareRequestHTTP(s, tokens[0], "POST", "conversations/"+conversation.ID+"/shares/preview", `{"through_sequence":2,"options":{}}`)
	if preview.Code != 200 || strings.Contains(preview.Body.String(), "bash_execute") || strings.Contains(preview.Body.String(), "drop") {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body)
	}
	published := chatShareRequestHTTP(s, tokens[0], "POST", "conversations/"+conversation.ID+"/shares", `{"through_sequence":2,"options":{}}`)
	if published.Code != 201 {
		t.Fatalf("publish: %d %s", published.Code, published.Body)
	}
	var share service.ChatShare
	if err := json.Unmarshal(published.Body.Bytes(), &share); err != nil {
		t.Fatal(err)
	}

	view := chatShareRequestHTTP(s, tokens[1], "GET", "shares/"+share.ID, "")
	if view.Code != 200 || !strings.Contains(view.Body.String(), "answer") {
		t.Fatalf("view: %d %s", view.Code, view.Body)
	}
	privateImport := chatShareRequestHTTP(s, tokens[1], "POST", "shares/"+share.ID+"/import", `{"version":1,"provider_key":"provider:private","model":"private-model"}`)
	if privateImport.Code != 403 {
		t.Fatalf("private provider transferred: %d %s", privateImport.Code, privateImport.Body)
	}
	imported := chatShareRequestHTTP(s, tokens[1], "POST", "shares/"+share.ID+"/import", `{"version":1,"title":"My copy"}`)
	if imported.Code != 201 {
		t.Fatalf("import: %d %s", imported.Code, imported.Body)
	}
	var copy service.PlaygroundConversation
	if err := json.Unmarshal(imported.Body.Bytes(), &copy); err != nil {
		t.Fatal(err)
	}
	if copy.OwnerUserID != owners[1] || copy.ProviderKey != "" || copy.Config == nil || len(copy.Config) != 0 || copy.ImportedFromShareID != share.ID {
		t.Fatalf("imported copy: %+v", copy)
	}
	if foreign := chatShareRequestHTTP(s, tokens[1], "DELETE", "shares/"+share.ID, ""); foreign.Code != 404 {
		t.Fatalf("foreign revoke: %d %s", foreign.Code, foreign.Body)
	}
	if revoked := chatShareRequestHTTP(s, tokens[0], "DELETE", "shares/"+share.ID, ""); revoked.Code != 204 {
		t.Fatalf("revoke: %d %s", revoked.Code, revoked.Body)
	}
	if denied := chatShareRequestHTTP(s, tokens[1], "GET", "shares/"+share.ID, ""); denied.Code != 404 {
		t.Fatalf("view after revoke: %d %s", denied.Code, denied.Body)
	}
	if kept := playgroundRequest(s, tokens[1], "GET", "/"+copy.ID, ""); kept.Code != 200 {
		t.Fatalf("copy did not survive revoke: %d %s", kept.Code, kept.Body)
	}
	republished := chatShareRequestHTTP(s, tokens[0], "POST", "conversations/"+conversation.ID+"/shares", `{"through_sequence":2,"options":{}}`)
	if republished.Code != 201 {
		t.Fatalf("republish after revoke: %d %s", republished.Code, republished.Body)
	}
}

func TestChatShareHTTPSourceOwnershipAndFeatureIntersection(t *testing.T) {
	s, _, tokens := playgroundFixture(t)
	conversation := playgroundNewConversation(t, s, tokens[0])
	if response := playgroundRequest(s, tokens[0], "POST", "/"+conversation.ID+"/messages", `{"messages":[{"role":"assistant","data":{"content":"done"}}]}`); response.Code != 201 {
		t.Fatal(response.Code, response.Body)
	}
	if foreign := chatShareRequestHTTP(s, tokens[1], "POST", "conversations/"+conversation.ID+"/shares", `{"through_sequence":1,"options":{}}`); foreign.Code != 404 {
		t.Fatalf("foreign publish: %d %s", foreign.Code, foreign.Body)
	}
	for _, feature := range []string{service.FeaturePlayground, service.FeatureChatSharing} {
		s.features.data.Store(&featureSnapshot{flags: map[string]bool{feature: false}, loadedAt: time.Now()})
		response := chatShareRequestHTTP(s, tokens[0], "POST", "conversations/"+conversation.ID+"/shares/preview", `{"through_sequence":1,"options":{}}`)
		if response.Code != 404 {
			t.Fatalf("feature %s: %d %s", feature, response.Code, response.Body)
		}
	}
}

func TestChatShareHTTPRevalidatesRecipientMembership(t *testing.T) {
	s, owners, tokens := playgroundFixture(t)
	workspaceStore := s.store.(service.WorkspaceStorer)
	adminCtx := service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: owners[0], WorkspaceID: service.DefaultWorkspaceID, PlatformAdmin: true})
	if err := workspaceStore.SetWorkspaceMember(adminCtx, service.WorkspaceMembership{WorkspaceID: service.DefaultWorkspaceID, UserID: owners[2], Role: "member", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	conversation := playgroundNewConversation(t, s, tokens[0])
	if response := playgroundRequest(s, tokens[0], "POST", "/"+conversation.ID+"/messages", `{"messages":[{"role":"assistant","data":{"content":"workspace snapshot"}}]}`); response.Code != 201 {
		t.Fatal(response.Code, response.Body)
	}
	published := chatShareRequestHTTP(s, tokens[0], "POST", "conversations/"+conversation.ID+"/shares", `{"through_sequence":1,"options":{}}`)
	if published.Code != 201 {
		t.Fatal(published.Code, published.Body)
	}
	var share service.ChatShare
	if err := json.Unmarshal(published.Body.Bytes(), &share); err != nil {
		t.Fatal(err)
	}
	if response := chatShareRequestHTTP(s, tokens[2], "GET", "shares/"+share.ID, ""); response.Code != 200 {
		t.Fatalf("active member: %d %s", response.Code, response.Body)
	}
	if err := workspaceStore.SetWorkspaceMember(adminCtx, service.WorkspaceMembership{WorkspaceID: service.DefaultWorkspaceID, UserID: owners[2], Role: "member", Status: "revoked"}); err != nil {
		t.Fatal(err)
	}
	if response := chatShareRequestHTTP(s, tokens[2], "GET", "shares/"+share.ID, ""); response.Code != 403 {
		t.Fatalf("revoked member: %d %s", response.Code, response.Body)
	}
}

func TestBuildChatShareSnapshotRequiresCompletedPairedPrefix(t *testing.T) {
	conversation := &service.PlaygroundConversation{Title: "Source"}
	for name, messages := range map[string][]service.PlaygroundMessage{
		"ends with user": {{Sequence: 1, Role: "user", Data: map[string]any{"content": "pending"}}},
		"pending tool": {
			{Sequence: 1, Role: "assistant", Data: map[string]any{"tool_calls": []any{map[string]any{"id": "call-1"}}}},
			{Sequence: 2, Role: "assistant", Data: map[string]any{"content": "done"}},
		},
		"orphan tool": {
			{Sequence: 1, Role: "tool", Data: map[string]any{"tool_call_id": "missing", "content": "x"}},
			{Sequence: 2, Role: "assistant", Data: map[string]any{"content": "done"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := buildChatShareSnapshot(conversation, messages, service.ChatShareOptions{IncludeToolOutputs: true})
			if !errors.Is(err, service.ErrChatShareConflict) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestBuildChatShareSnapshotRejectsOversize(t *testing.T) {
	_, _, err := buildChatShareSnapshot(&service.PlaygroundConversation{}, []service.PlaygroundMessage{{Sequence: 1, Role: "assistant", Data: map[string]any{"content": strings.Repeat("x", service.ChatShareMaxBytes)}}}, service.ChatShareOptions{})
	if !errors.Is(err, service.ErrChatShareTooLarge) {
		t.Fatalf("error=%v", err)
	}
}

func TestChatShareMediaIsNativeBlobReadPath(t *testing.T) {
	if !nativeBlobReadPath("/at/", "/at/api/v1/chats/shares/share-id/media/media-id") {
		t.Fatal("share media must accept the workspace query selector used by img elements")
	}
	if nativeBlobReadPath("/at/", "/at/api/v1/chats/shares/share-id/import") {
		t.Fatal("JSON share routes must not accept blob query selection")
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
