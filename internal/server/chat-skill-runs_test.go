package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func chatSkillRunServer(t *testing.T, provider *scriptedAgentProvider) *Server {
	t.Helper()
	agents := map[string]*service.Agent{
		"painter": {ID: "painter", Name: "Painter", Config: service.AgentConfig{
			Provider: "prov1", Model: "m1", MaxIterations: 2, SystemPrompt: "CHILD",
		}},
	}
	s, _ := newObsTestServer(t, &fakeObsProvider{}, &fakeLLMCallStore{}, agents, nil)
	s.providers["prov1"] = ProviderInfo{provider: provider, providerType: "openai", defaultModel: "m1"}
	s.skillStore = &fakeSkillStore{skills: map[string]*service.Skill{
		"skill-draw": {ID: "skill-draw", Name: "draw-image", Context: "fork", Agent: "painter", SystemPrompt: "Draw carefully."},
		"skill-docs": {ID: "skill-docs", Name: "style-guide", SystemPrompt: "Use short sentences."},
	}}
	return s
}

func postSkillRun(t *testing.T, s *Server, body string) builtinCallResponse {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/chats/skill-runs", strings.NewReader(body)).WithContext(s.ctx)
	rec := httptest.NewRecorder()
	s.ChatSkillRunAPI(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp builtinCallResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestChatSkillRunForegroundReturnsAgentResult(t *testing.T) {
	provider := &scriptedAgentProvider{child: &service.LLMResponse{Content: "image saved to cat.png", Finished: true}}
	s := chatSkillRunServer(t, provider)

	resp := postSkillRun(t, s, `{"skill":"draw-image","task":"draw a cat"}`)
	if resp.Error != "" || !strings.Contains(resp.Result, "image saved to cat.png") {
		t.Fatalf("foreground run = %+v", resp)
	}
}

func TestChatSkillRunBackgroundCanBeAwaited(t *testing.T) {
	gate := make(chan struct{})
	provider := &scriptedAgentProvider{child: &service.LLMResponse{Content: "done: dog.png", Finished: true}, childGate: gate}
	s := chatSkillRunServer(t, provider)

	resp := postSkillRun(t, s, `{"skill":"draw-image","task":"draw a dog","background":true}`)
	var started struct {
		RunID string `json:"run_id"`
	}
	if resp.Error != "" || json.Unmarshal([]byte(resp.Result), &started) != nil || started.RunID == "" {
		t.Fatalf("background start = %+v", resp)
	}
	time.AfterFunc(100*time.Millisecond, func() { close(gate) })

	req := httptest.NewRequest("GET", "/api/v1/chats/skill-runs/"+started.RunID+"?wait=5", nil).WithContext(s.ctx)
	req.SetPathValue("id", started.RunID)
	rec := httptest.NewRecorder()
	s.ChatSkillRunStatusAPI(rec, req)
	var status struct {
		Status string `json:"status"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status %q: %v", rec.Body.String(), err)
	}
	if status.Status != "completed" || status.Result != "done: dog.png" {
		t.Fatalf("long-poll did not return the finished result: %+v", status)
	}
}

func TestChatSkillRunRejectsDocumentationSkill(t *testing.T) {
	s := chatSkillRunServer(t, &scriptedAgentProvider{})
	resp := postSkillRun(t, s, `{"skill":"style-guide","task":"anything"}`)
	if !strings.Contains(resp.Error, "not bound to an agent") {
		t.Fatalf("documentation skill was run: %+v", resp)
	}
}
