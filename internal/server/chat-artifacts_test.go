package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// memoryMediaStore is a MediaStorer over a filesystem backend in a temp dir.
type memoryMediaStore struct {
	*fixtureExecutionStore
	mu       sync.Mutex
	settings service.MediaSettings
	objects  map[string]service.MediaObject
}

func (m *memoryMediaStore) GetMediaSettings(context.Context) (*service.MediaSettings, error) {
	v := m.settings
	return &v, nil
}
func (m *memoryMediaStore) SaveMediaSettings(_ context.Context, s service.MediaSettings) (*service.MediaSettings, error) {
	m.settings = s
	return &s, nil
}
func (m *memoryMediaStore) CreateMediaObject(_ context.Context, o service.MediaObject) (*service.MediaObject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o.ID = ulid.Make().String()
	m.objects[o.ID] = o
	return &o, nil
}
func (m *memoryMediaStore) GetMediaObject(_ context.Context, _, _, id string) (*service.MediaObject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[id]
	if !ok {
		return nil, service.ErrMediaNotFound
	}
	return &o, nil
}
func (m *memoryMediaStore) DeleteMediaObject(_ context.Context, _, _, id string) (*service.MediaObject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.objects[id]
	delete(m.objects, id)
	return &o, nil
}

// fileWritingProvider stands in for a skill agent whose tool writes files:
// on its call it writes into the run's AT_WORK_DIR, then answers.
type fileWritingProvider struct {
	files map[string][]byte
}

func (p *fileWritingProvider) Chat(ctx context.Context, _ string, _ []service.Message, _ []service.Tool, _ *service.ChatOptions) (*service.LLMResponse, error) {
	dir := workflow.WorkDirFromContext(ctx)
	if dir == "" || !filepath.IsAbs(dir) {
		// Without a run directory a relative path would land in the package
		// source tree; fail instead so the test reports the regression.
		return &service.LLMResponse{Content: "no run directory", Finished: true}, nil
	}
	for name, data := range p.files {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return nil, err
		}
	}
	return &service.LLMResponse{Content: "Here is your drawing.", Finished: true}, nil
}

var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")

func artifactServer(t *testing.T, provider service.LLMProvider, mediaEnabled bool) (*Server, *memoryMediaStore) {
	t.Helper()
	agents := map[string]*service.Agent{
		"painter": {ID: "painter", Name: "Painter", Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 2}},
	}
	s, _ := newObsTestServer(t, &fakeObsProvider{}, &fakeLLMCallStore{}, agents, nil)
	s.providers["prov1"] = ProviderInfo{provider: provider, providerType: "openai", defaultModel: "m1"}
	s.skillStore = &fakeSkillStore{skills: map[string]*service.Skill{
		"skill-draw": {ID: "skill-draw", Name: "draw", Context: "fork", Agent: "painter"},
	}}
	settings := service.DefaultMediaSettings()
	if mediaEnabled {
		settings = service.MediaSettings{Version: 1, Backend: service.MediaBackendFilesystem, Filesystem: service.MediaFilesystemSettings{Root: filepath.Join(t.TempDir(), "media")}}
	}
	media := &memoryMediaStore{fixtureExecutionStore: s.store.(*fixtureExecutionStore), settings: settings, objects: map[string]service.MediaObject{}}
	s.store = media
	return s, media
}

func decodeArtifactResult(t *testing.T, resp builtinCallResponse) (map[string]any, []chatArtifact) {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(resp.Result), &payload); err != nil {
		t.Fatalf("result is not JSON: %q (%v)", resp.Result, err)
	}
	var artifacts []chatArtifact
	if raw, ok := payload["artifacts"]; ok {
		data, _ := json.Marshal(raw)
		_ = json.Unmarshal(data, &artifacts)
	}
	return payload, artifacts
}

func TestChatSkillRunDeliversProducedFiles(t *testing.T) {
	provider := &fileWritingProvider{files: map[string][]byte{
		"tree.png":         pngBytes,
		"report/tree.pdf":  []byte("%PDF-1.7\n1 0 obj\n"),
		"notes.csv":        []byte("a,b\n1,2\n"),
		"page.html":        []byte("<html><script>alert(1)</script></html>"),
		".hidden/skip.txt": []byte("internal"),
	}}
	s, media := artifactServer(t, provider, true)

	resp := postSkillRun(t, s, `{"skill":"draw","task":"draw a tree"}`)
	if resp.Error != "" {
		t.Fatalf("run failed: %s", resp.Error)
	}
	payload, artifacts := decodeArtifactResult(t, resp)
	if payload["result"] != "Here is your drawing." {
		t.Fatalf("agent result lost: %+v", payload)
	}
	types := map[string]string{}
	for _, a := range artifacts {
		types[a.Name] = a.ContentType
		if _, ok := media.objects[a.MediaID]; !ok {
			t.Fatalf("artifact %s was not stored", a.Name)
		}
	}
	want := map[string]string{"tree.png": "image/png", "tree.pdf": "application/pdf", "notes.csv": "text/csv", "page.html": "text/html"}
	for name, contentType := range want {
		if types[name] != contentType {
			t.Fatalf("%s: content type %q, want %q (all: %v)", name, types[name], contentType, types)
		}
	}
	if _, ok := types["skip.txt"]; ok {
		t.Fatal("hidden directory was delivered")
	}
	if mediaInlineContentType("text/html") {
		t.Fatal("HTML would be rendered inline on the application origin")
	}
}

func TestChatSkillRunReportsUndeliverableFiles(t *testing.T) {
	s, _ := artifactServer(t, &fileWritingProvider{files: map[string][]byte{"tree.png": pngBytes}}, false)

	resp := postSkillRun(t, s, `{"skill":"draw","task":"draw a tree"}`)
	payload, artifacts := decodeArtifactResult(t, resp)
	note, _ := payload["artifacts_note"].(string)
	if len(artifacts) != 0 || !strings.Contains(note, "storage is disabled") || !strings.Contains(note, "tree.png") {
		t.Fatalf("missing storage was not explained: %+v", payload)
	}
}
