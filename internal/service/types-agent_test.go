package service

import (
	"encoding/json"
	"testing"
)

func TestSkillRef_MarshalLegacyString(t *testing.T) {
	// A skill with no connection overrides must marshal back as a bare string
	// so existing agent configs in the DB and downstream consumers that read
	// the agent's skills array keep working.
	ref := SkillRef{ID: "youtube_publish"}
	data, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(data) != `"youtube_publish"` {
		t.Errorf("got %s, want %q", string(data), "youtube_publish")
	}
}

func TestSkillRef_MarshalObjectWhenConnectionsSet(t *testing.T) {
	ref := SkillRef{
		ID: "youtube_publish",
		Connections: map[string]string{
			"youtube": "conn_01HV",
		},
	}
	data, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("marshalled output is not an object: %s", data)
	}
	if decoded["id"] != "youtube_publish" {
		t.Errorf("id: got %v", decoded["id"])
	}
	if m, ok := decoded["connections"].(map[string]any); !ok || m["youtube"] != "conn_01HV" {
		t.Errorf("connections: got %v", decoded["connections"])
	}
}

func TestSkillRef_UnmarshalString(t *testing.T) {
	var ref SkillRef
	if err := json.Unmarshal([]byte(`"youtube_publish"`), &ref); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if ref.ID != "youtube_publish" {
		t.Errorf("ID: got %q", ref.ID)
	}
	if ref.Connections != nil {
		t.Errorf("Connections should be nil, got %v", ref.Connections)
	}
}

func TestSkillRef_UnmarshalObject(t *testing.T) {
	var ref SkillRef
	body := []byte(`{"id":"youtube_publish","connections":{"youtube":"conn_1","google":"conn_2"}}`)
	if err := json.Unmarshal(body, &ref); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if ref.ID != "youtube_publish" {
		t.Errorf("ID: got %q", ref.ID)
	}
	if ref.Connections["youtube"] != "conn_1" {
		t.Errorf("youtube: got %q", ref.Connections["youtube"])
	}
	if ref.Connections["google"] != "conn_2" {
		t.Errorf("google: got %q", ref.Connections["google"])
	}
}

func TestAgentConfig_BackwardCompatibleSkills(t *testing.T) {
	// An AgentConfig stored before the change has skills as a plain string array.
	// It must unmarshal cleanly with no connection overrides.
	legacy := []byte(`{
		"provider": "anthropic",
		"skills": ["youtube_publish", "web_search"],
		"connections": {"youtube": "conn_01HV"}
	}`)
	var cfg AgentConfig
	if err := json.Unmarshal(legacy, &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(cfg.Skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(cfg.Skills))
	}
	if cfg.Skills[0].ID != "youtube_publish" || cfg.Skills[1].ID != "web_search" {
		t.Errorf("unexpected IDs: %+v", cfg.Skills)
	}
	if cfg.Connections["youtube"] != "conn_01HV" {
		t.Errorf("agent-level connection: got %v", cfg.Connections)
	}

	// Round-trip back to JSON: the two legacy skills should marshal back as bare strings.
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// They should be bare strings; quick substring check:
	s := string(out)
	if !containsAll(s, `"youtube_publish"`, `"web_search"`) {
		t.Errorf("expected bare-string skills in output, got: %s", s)
	}
}

func TestAgentConfig_MixedSkillRefs(t *testing.T) {
	body := []byte(`{
		"provider": "anthropic",
		"skills": [
			"web_search",
			{"id":"youtube_publish","connections":{"youtube":"conn_client_b"}}
		]
	}`)
	var cfg AgentConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(cfg.Skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(cfg.Skills))
	}
	if cfg.Skills[0].ID != "web_search" || len(cfg.Skills[0].Connections) != 0 {
		t.Errorf("first skill: %+v", cfg.Skills[0])
	}
	if cfg.Skills[1].ID != "youtube_publish" {
		t.Errorf("second id: got %q", cfg.Skills[1].ID)
	}
	if cfg.Skills[1].Connections["youtube"] != "conn_client_b" {
		t.Errorf("override: %v", cfg.Skills[1].Connections)
	}
}

func containsAll(haystack string, needles ...string) bool {
	for _, n := range needles {
		if !containsSubstr(haystack, n) {
			return false
		}
	}
	return true
}

func containsSubstr(haystack, needle string) bool {
	return len(needle) == 0 || indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
