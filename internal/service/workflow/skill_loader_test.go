package workflow

import (
	"errors"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
)

// makeLookup builds an in-memory SkillLookup over a name->skill map. It also
// resolves by skill.ID when present.
func makeLookup(skills map[string]*service.Skill) SkillLookup {
	return func(nameOrID string) (*service.Skill, error) {
		if sk, ok := skills[nameOrID]; ok {
			return sk, nil
		}
		// Fall back to ID lookup.
		for _, sk := range skills {
			if sk != nil && sk.ID == nameOrID {
				return sk, nil
			}
		}
		return nil, nil
	}
}

func TestSkillRuntime_EmptyAttachment(t *testing.T) {
	rt, err := NewSkillRuntime(executiontest.Context(t), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rt.HasSkills() {
		t.Errorf("HasSkills() = true, want false")
	}
	if got := rt.CatalogSystemPrompt(); got != "" {
		t.Errorf("CatalogSystemPrompt() = %q, want empty", got)
	}
}

func TestSkillRuntime_CatalogContainsAttachedSkills(t *testing.T) {
	skills := map[string]*service.Skill{
		"youtube": {
			ID:           "skill_yt",
			Name:         "youtube",
			Description:  "Publish videos to YouTube.",
			SystemPrompt: "You are a YouTube publisher.",
			Tools: []service.Tool{
				{Name: "yt_upload", Description: "Upload a video", Handler: "return 'ok'", HandlerType: "js"},
			},
		},
		"slack": {
			ID:           "skill_sl",
			Name:         "slack",
			Description:  "Post messages to Slack.",
			SystemPrompt: "You are a Slack poster.",
			Tools: []service.Tool{
				{Name: "slack_post", Description: "Post a message", Handler: "return 'posted'", HandlerType: "js"},
			},
		},
	}

	refs := []service.SkillRef{{ID: "youtube"}, {ID: "slack"}}
	rt, err := NewSkillRuntime(executiontest.Context(t), makeLookup(skills), refs, nil, nil)
	if err != nil {
		t.Fatalf("NewSkillRuntime: %v", err)
	}

	if !rt.HasSkills() {
		t.Fatalf("HasSkills() = false, want true")
	}

	cat := rt.Catalog()
	if len(cat) != 2 {
		t.Fatalf("catalog len = %d, want 2", len(cat))
	}
	// Catalog is sorted by name.
	if cat[0].Name != "slack" || cat[1].Name != "youtube" {
		t.Errorf("catalog order = %q,%q want slack,youtube", cat[0].Name, cat[1].Name)
	}

	prompt := rt.CatalogSystemPrompt()
	for _, want := range []string{"youtube", "slack", "Publish videos", "Post messages", "load_skill"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("CatalogSystemPrompt missing %q: %q", want, prompt)
		}
	}
}

func TestSkillRuntime_LoadSkillToolDef(t *testing.T) {
	skills := map[string]*service.Skill{
		"a": {ID: "a", Name: "a", Description: "first"},
		"b": {ID: "b", Name: "b", Description: "second"},
	}
	rt, _ := NewSkillRuntime(executiontest.Context(t), makeLookup(skills),
		[]service.SkillRef{{ID: "a"}, {ID: "b"}}, nil, nil)

	def := rt.LoadSkillToolDef()
	if def.Name != LoadSkillToolName {
		t.Errorf("def.Name = %q, want %q", def.Name, LoadSkillToolName)
	}

	props, ok := def.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("InputSchema.properties not a map: %T", def.InputSchema["properties"])
	}
	skillNameProp, ok := props["skill_name"].(map[string]any)
	if !ok {
		t.Fatalf("properties.skill_name not a map")
	}
	enum, ok := skillNameProp["enum"].([]string)
	if !ok {
		t.Fatalf("skill_name.enum not []string: %T", skillNameProp["enum"])
	}
	if len(enum) != 2 {
		t.Errorf("enum len = %d, want 2", len(enum))
	}
}

func TestSkillRuntime_HandleLoadSkill_Activates(t *testing.T) {
	yt := &service.Skill{
		ID:           "skill_yt",
		Name:         "youtube",
		Description:  "YouTube",
		SystemPrompt: "You are a YouTube publisher.",
		Tools: []service.Tool{
			{Name: "yt_upload", Description: "Upload", Handler: "return 'ok'", HandlerType: "js"},
		},
	}
	rt, _ := NewSkillRuntime(executiontest.Context(t),
		makeLookup(map[string]*service.Skill{"youtube": yt}),
		[]service.SkillRef{{ID: "youtube"}}, nil, nil)

	if rt.IsSkillLoaded("youtube") {
		t.Errorf("IsSkillLoaded(youtube) = true before load, want false")
	}

	result, err := rt.HandleLoadSkill(map[string]any{"skill_name": "youtube"})
	if err != nil {
		t.Fatalf("HandleLoadSkill: %v", err)
	}
	for _, want := range []string{"youtube", "documentation only", "You are a YouTube publisher"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q: %q", want, result)
		}
	}

	if !rt.IsSkillLoaded("youtube") {
		t.Errorf("IsSkillLoaded(youtube) = false after load, want true")
	}

	if !strings.Contains(result, service.LegacySkillToolsHeading) || !strings.Contains(result, "return 'ok'") {
		t.Fatalf("legacy tool was not preserved as documentation: %q", result)
	}
}

func TestSkillRuntime_HandleLoadSkill_Idempotent(t *testing.T) {
	yt := &service.Skill{ID: "skill_yt", Name: "youtube", SystemPrompt: "X"}
	rt, _ := NewSkillRuntime(executiontest.Context(t),
		makeLookup(map[string]*service.Skill{"youtube": yt}),
		[]service.SkillRef{{ID: "youtube"}}, nil, nil)

	first, err := rt.HandleLoadSkill(map[string]any{"skill_name": "youtube"})
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if !strings.Contains(first, "X") {
		t.Errorf("first load missing system prompt: %q", first)
	}

	second, err := rt.HandleLoadSkill(map[string]any{"skill_name": "youtube"})
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if !strings.Contains(second, "already loaded") {
		t.Errorf("second load = %q, want 'already loaded'", second)
	}
	if strings.Contains(second, "X") {
		t.Errorf("second load duplicated system prompt: %q", second)
	}
}

func TestSkillRuntime_HandleLoadSkill_UnknownSkill(t *testing.T) {
	rt, _ := NewSkillRuntime(executiontest.Context(t),
		makeLookup(map[string]*service.Skill{"a": {ID: "a", Name: "a"}}),
		[]service.SkillRef{{ID: "a"}}, nil, nil)

	result, err := rt.HandleLoadSkill(map[string]any{"skill_name": "bogus"})
	if err != nil {
		t.Fatalf("err = %v, want nil (unknown should be recoverable)", err)
	}
	if !strings.Contains(result, "Error") || !strings.Contains(result, "bogus") {
		t.Errorf("unknown skill result = %q", result)
	}
}

func TestSkillRuntime_HandleLoadSkill_MissingArg(t *testing.T) {
	rt, _ := NewSkillRuntime(executiontest.Context(t), nil, nil, nil, nil)
	_, err := rt.HandleLoadSkill(map[string]any{})
	if err == nil {
		t.Errorf("expected error for missing skill_name")
	}
}

func TestSkillRuntime_ReadBundledResource(t *testing.T) {
	skill := &service.Skill{
		ID: "skill_docs", Name: "docs", SystemPrompt: "Use the guide.",
		Resources: []service.SkillResource{{Path: "references/guide.md", Content: "Detailed guide"}},
	}
	rt, err := NewSkillRuntime(executiontest.Context(t), func(name string) (*service.Skill, error) {
		return skill, nil
	}, []service.SkillRef{{ID: skill.ID}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !rt.HasResources() {
		t.Fatal("HasResources = false")
	}
	if _, err := rt.HandleReadSkillResource(map[string]any{"skill_name": "docs", "path": "references/guide.md"}); err == nil {
		t.Fatal("read before load should fail")
	}
	loaded, err := rt.HandleLoadSkill(map[string]any{"skill_name": "docs"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded, "references/guide.md") {
		t.Fatalf("load result does not list resource: %q", loaded)
	}
	got, err := rt.HandleReadSkillResource(map[string]any{"skill_name": "docs", "path": "references/guide.md"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "Detailed guide" {
		t.Fatalf("resource = %q", got)
	}
	if _, err := rt.HandleReadSkillResource(map[string]any{"skill_name": "docs", "path": "../secret"}); err == nil {
		t.Fatal("expected traversal error")
	}
}

func TestSkillRuntime_ExtraNamesMergedAndDeduplicated(t *testing.T) {
	a := &service.Skill{ID: "a", Name: "a", Description: "A"}
	b := &service.Skill{ID: "b", Name: "b", Description: "B"}
	rt, err := NewSkillRuntime(executiontest.Context(t),
		makeLookup(map[string]*service.Skill{"a": a, "b": b}),
		[]service.SkillRef{{ID: "a"}},
		[]string{"b", "a", "  ", ""}, // edge inputs: includes "a" which is dup
		nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got := len(rt.Catalog()); got != 2 {
		t.Errorf("catalog len = %d, want 2 (deduped)", got)
	}
}

func TestSkillRuntime_LookupErrorsAreReportedAndSwallowed(t *testing.T) {
	lookup := func(nameOrID string) (*service.Skill, error) {
		if nameOrID == "broken" {
			return nil, errors.New("db boom")
		}
		if nameOrID == "missing" {
			return nil, nil
		}
		return &service.Skill{ID: nameOrID, Name: nameOrID, Description: "ok"}, nil
	}

	var warnedNames []string
	warn := func(name string, err error) {
		warnedNames = append(warnedNames, name)
	}

	rt, err := NewSkillRuntime(executiontest.Context(t), lookup,
		[]service.SkillRef{{ID: "good"}, {ID: "broken"}, {ID: "missing"}},
		nil, warn)
	if err != nil {
		t.Fatalf("NewSkillRuntime: %v", err)
	}
	if got := len(rt.Catalog()); got != 1 {
		t.Errorf("catalog len = %d, want 1 (only 'good' resolved)", got)
	}
	if len(warnedNames) != 2 {
		t.Errorf("warned names = %v, want 2 entries (broken + missing)", warnedNames)
	}
}
