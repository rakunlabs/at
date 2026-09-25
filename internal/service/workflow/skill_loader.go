package workflow

import (
	"context"
	"fmt"
	pathpkg "path"
	"sort"
	"strings"
	"sync"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Lazy Skill Runtime (Progressive Disclosure) ───
//
// SkillRuntime resolves an agent's attached skills into an in-memory catalog
// once at the start of a run, but defers injecting their SystemPrompt and
// Markdown instructions into the LLM context until the LLM explicitly activates
// each skill via the `load_skill` meta-tool.
//
// This is the LLM-driven progressive disclosure pattern: the agent sees a
// short catalog ("you have these skills, call load_skill to activate one")
// instead of the union of every attached skill's instructions — saving
// tokens and letting the LLM decide what it actually needs.
//
// Add CatalogSystemPrompt and LoadSkillToolDef to an agentic loop, then pass
// HandleLoadSkill's prompt back as a system follow-up. Skills never add
// executable tools; code blocks and legacy handler definitions remain text.

// LoadSkillToolName is the meta-tool name the LLM calls to activate a skill.
const LoadSkillToolName = "load_skill"

// ReadSkillResourceToolName reads a text resource bundled with a loaded skill.
const ReadSkillResourceToolName = "read_skill_resource"

// SkillCatalogEntry is a single row in the catalog presented to the LLM.
type SkillCatalogEntry struct {
	Name        string
	Description string
	Context     string
	Agent       string
	Background  bool
}

type SkillForkRequest struct {
	Skill      *service.Skill
	Agent      string
	Task       string
	Context    string
	Background bool
}

// SkillRuntime is the per-call lazy skill state. Safe for concurrent reads,
// guards loadedSkills mutation with a mutex (a single agentic loop is
// sequential but defensive locking keeps misuse cheap).
type SkillRuntime struct {
	ctx context.Context
	mu  sync.Mutex

	// registry maps lookup key (name AND id, when both are known) to the full
	// resolved Skill. Populated once in NewSkillRuntime; never mutated.
	registry map[string]*service.Skill

	// catalog is the ordered list shown to the LLM. Populated once.
	catalog []SkillCatalogEntry

	// loadedSkills tracks which skills the LLM has activated via load_skill.
	// The key is the canonical skill name (preferred) or the id when name is
	// empty.
	loadedSkills map[string]bool
}

// NewSkillRuntime resolves every attached SkillRef once via lookup and
// returns a runtime ready for lazy activation. extraNames lets callers add
// skill names from edge inputs (e.g. workflow `skill_config` nodes) on top
// of the agent's persisted SkillRefs.
//
// Resolution failures (lookup error, not-found) are logged via the supplied
// warn callback if non-nil and otherwise swallowed silently — matching the
// previous best-effort behaviour of both eager paths.
func NewSkillRuntime(
	ctx context.Context,
	lookup SkillLookup,
	refs []service.SkillRef,
	extraNames []string,
	warn func(skill string, err error),
) (*SkillRuntime, error) {
	rt := &SkillRuntime{
		ctx:          ctx,
		registry:     map[string]*service.Skill{},
		loadedSkills: map[string]bool{},
	}

	if lookup == nil {
		return rt, nil
	}

	// De-duplicate the union of skill identifiers we're going to resolve.
	seen := map[string]bool{}
	var ordered []string
	for _, ref := range refs {
		key := strings.TrimSpace(ref.ID)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		ordered = append(ordered, key)
	}
	for _, n := range extraNames {
		key := strings.TrimSpace(n)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		ordered = append(ordered, key)
	}

	// Resolve each ref to a full skill; populate registry + catalog.
	catalogSeen := map[string]bool{}
	for _, key := range ordered {
		if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "skills.use", ResourceID: key}); err != nil {
			return nil, err
		}
		skill, err := lookup(key)
		if err != nil {
			if warn != nil {
				warn(key, err)
			}
			continue
		}
		if skill == nil {
			if warn != nil {
				warn(key, fmt.Errorf("skill not found"))
			}
			continue
		}
		if len(skill.Tools) > 0 {
			documentationSkill := *skill
			if err := service.NormalizeDocumentationSkill(&documentationSkill); err != nil {
				if warn != nil {
					warn(key, fmt.Errorf("normalize documentation skill: %w", err))
				}
				continue
			}
			skill = &documentationSkill
		}

		if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "skills.use", ResourceID: skill.ID}); err != nil {
			return nil, err
		}
		// Index by every plausible lookup key.
		if skill.ID != "" {
			rt.registry[skill.ID] = skill
		}
		if skill.Name != "" {
			rt.registry[skill.Name] = skill
		}
		// Also keep the original key (in case it differs in case/format).
		if _, ok := rt.registry[key]; !ok {
			rt.registry[key] = skill
		}

		// Catalog entry uses the canonical name (LLM-visible).
		canonical := skill.Name
		if canonical == "" {
			canonical = skill.ID
		}
		if canonical == "" || catalogSeen[canonical] {
			continue
		}
		catalogSeen[canonical] = true
		rt.catalog = append(rt.catalog, SkillCatalogEntry{
			Name:        canonical,
			Description: skill.Description,
			Context:     skill.Context,
			Agent:       skill.Agent,
			Background:  skill.Background,
		})
	}

	// Stable ordering for deterministic prompts.
	sort.SliceStable(rt.catalog, func(i, j int) bool {
		return rt.catalog[i].Name < rt.catalog[j].Name
	})

	return rt, nil
}

// HasSkills reports whether any skills are attached. When false, callers
// should skip injecting load_skill and the catalog block.
func (r *SkillRuntime) HasSkills() bool {
	return len(r.catalog) > 0
}

// HasResources reports whether any attached skill contains package resources.
func (r *SkillRuntime) HasResources() bool {
	for _, skill := range r.registry {
		if skill != nil && len(skill.Resources) > 0 {
			return true
		}
	}
	return false
}

func (r *SkillRuntime) HasBackgroundFork() bool {
	for _, entry := range r.catalog {
		if entry.Context == "fork" && entry.Background {
			return true
		}
	}
	return false
}

// Catalog returns the (read-only) list of catalog entries — useful for
// tests and logging. Callers must not mutate the returned slice.
func (r *SkillRuntime) Catalog() []SkillCatalogEntry {
	return r.catalog
}

// CatalogSystemPrompt renders the catalog block to be appended to the
// agent's system prompt. Empty string when no skills are attached.
func (r *SkillRuntime) CatalogSystemPrompt() string {
	if len(r.catalog) == 0 {
		return ""
	}
	r.mu.Lock()
	loaded := make(map[string]bool, len(r.loadedSkills))
	for name, value := range r.loadedSkills {
		loaded[name] = value
	}
	r.mu.Unlock()
	visible := make([]SkillCatalogEntry, 0, len(r.catalog))
	for _, entry := range r.catalog {
		if !loaded[entry.Name] {
			visible = append(visible, entry)
		}
	}
	if len(visible) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Available Skills\n\n")
	b.WriteString("You have access to the following documentation skills. Each skill provides domain-specific instructions, examples, and optional resources. ")
	b.WriteString("Skills are NOT loaded by default — you must call the `")
	b.WriteString(LoadSkillToolName)
	b.WriteString("` tool with the skill name to activate it. Once activated, its instructions become available for the rest of the conversation. Skills never add executable tools; use only capabilities already present in your tool list.\n\n")
	for _, e := range visible {
		desc := e.Description
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&b, "- `%s` — %s\n", e.Name, desc)
		if e.Context == "fork" {
			mode := "foreground"
			if e.Background {
				mode = "background"
			}
			fmt.Fprintf(&b, "  Runs in an isolated %s context using agent `%s`; pass a self-contained `task` when loading it.\n", mode, e.Agent)
		}
	}
	b.WriteString("\nCall `")
	b.WriteString(LoadSkillToolName)
	b.WriteString("` with `{\"skill_name\": \"<name>\"}` whenever your task requires functionality covered by one of the listed skills.")
	return b.String()
}

// LoadSkillToolDef returns the meta-tool definition exposed to the LLM.
// The enum on skill_name limits the LLM to attached skills.
func (r *SkillRuntime) LoadSkillToolDef() service.Tool {
	enum := make([]string, len(r.catalog))
	for i, e := range r.catalog {
		enum[i] = e.Name
	}
	return service.Tool{
		Name:        LoadSkillToolName,
		Description: "Activate an attached skill. Normal skills load into this context. A catalog entry marked isolated runs through its configured subagent and requires a self-contained task.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"skill_name": map[string]any{
					"type":        "string",
					"description": "The name of the skill to load. Must match one of the catalog entries listed in your system prompt.",
					"enum":        enum,
				},
				"task": map[string]any{
					"type":        "string",
					"description": "Task for an isolated skill. Required when the selected skill has context: fork.",
				},
				"context": map[string]any{
					"type":        "string",
					"description": "Optional background and constraints for the isolated skill run.",
				},
			},
			"required": []string{"skill_name"},
		},
	}
}

// ForkRequest recognizes a context: fork skill without activating it in the
// caller. The caller executes this request through its isolated-agent runner.
func (r *SkillRuntime) ForkRequest(args map[string]any) (SkillForkRequest, bool, error) {
	name, _ := args["skill_name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return SkillForkRequest{}, false, nil
	}
	skill := r.registry[name]
	if skill == nil || skill.Context != "fork" {
		return SkillForkRequest{}, false, nil
	}
	canonical := skill.Name
	if canonical == "" {
		canonical = skill.ID
	}
	r.mu.Lock()
	alreadyLoaded := r.loadedSkills[canonical]
	r.mu.Unlock()
	if alreadyLoaded {
		return SkillForkRequest{}, false, nil
	}
	if err := service.CheckExecution(r.ctx, service.ExecutionAction{Kind: "resource", Name: "skills.use", ResourceID: skill.ID}); err != nil {
		return SkillForkRequest{}, true, err
	}
	task, _ := args["task"].(string)
	task = strings.TrimSpace(task)
	if task == "" {
		return SkillForkRequest{}, true, fmt.Errorf("load_skill: task is required for forked skill %q", name)
	}
	extra, _ := args["context"].(string)
	return SkillForkRequest{
		Skill: skill, Agent: skill.Agent, Task: task,
		Context: strings.TrimSpace(extra), Background: skill.Background,
	}, true, nil
}

// ReadSkillResourceToolDef returns the on-demand package resource reader.
func (r *SkillRuntime) ReadSkillResourceToolDef() service.Tool {
	enum := make([]string, 0, len(r.catalog))
	seen := map[string]bool{}
	for _, entry := range r.catalog {
		skill := r.registry[entry.Name]
		if skill == nil || len(skill.Resources) == 0 || seen[entry.Name] {
			continue
		}
		seen[entry.Name] = true
		enum = append(enum, entry.Name)
	}
	return service.Tool{
		Name:        ReadSkillResourceToolName,
		Description: "Read one text file bundled with an already loaded skill. Use the exact relative path listed when the skill was activated.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"skill_name": map[string]any{"type": "string", "enum": enum},
				"path":       map[string]any{"type": "string", "description": "Resource path relative to SKILL.md"},
			},
			"required": []string{"skill_name", "path"},
		},
	}
}

// HandleLoadSkill processes a load_skill tool call and returns the text to
// surface back to the LLM as the tool_result content. The activated skill's
// SystemPrompt is embedded inline in this result so the LLM picks it up on
// its next turn — we deliberately do NOT inject a separate `system` message
// between the assistant tool_use and the user tool_result, which would
// violate Anthropic/OpenAI tool-call sequencing.
//
// err is only set for malformed arguments. Lookup failures and not-found
// cases map to a recoverable error message in resultText so the LLM can try
// again with a different skill name.
//
// Idempotent: a repeat call returns a short "already loaded" notice and
// does not re-emit the system prompt.
func (r *SkillRuntime) HandleLoadSkill(args map[string]any) (resultText string, err error) {
	name, _ := args["skill_name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("load_skill: missing skill_name argument")
	}

	skill, ok := r.registry[name]
	if !ok || skill == nil {
		return fmt.Sprintf("Error: skill %q is not attached to this agent. Available skills: %s", name, r.catalogNames()), nil
	}
	if err := service.CheckExecution(r.ctx, service.ExecutionAction{Kind: "resource", Name: "skills.use", ResourceID: skill.ID}); err != nil {
		return "", err
	}

	canonical := skill.Name
	if canonical == "" {
		canonical = skill.ID
	}

	r.mu.Lock()
	already := r.loadedSkills[canonical]
	r.loadedSkills[canonical] = true
	r.mu.Unlock()

	if already {
		return fmt.Sprintf("Skill %q is already loaded.", canonical), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Skill %q activated. It provides documentation only and adds no executable tools; use capabilities already available in your tool list.", canonical)
	if skill.SystemPrompt != "" {
		b.WriteString("\n\n=== Skill Instructions ===\n")
		b.WriteString(skill.SystemPrompt)
		b.WriteString("\n=== End Skill Instructions ===")
	}
	if len(skill.Resources) > 0 {
		paths := make([]string, 0, len(skill.Resources))
		for _, resource := range skill.Resources {
			paths = append(paths, resource.Path)
		}
		sort.Strings(paths)
		b.WriteString("\n\n=== Bundled Resources ===\n")
		b.WriteString("Read these only when needed with `")
		b.WriteString(ReadSkillResourceToolName)
		b.WriteString("`: ")
		b.WriteString(strings.Join(paths, ", "))
		b.WriteString("\n=== End Bundled Resources ===")
	}

	return b.String(), nil
}

// HandleReadSkillResource returns one resource from an already activated skill.
func (r *SkillRuntime) HandleReadSkillResource(args map[string]any) (string, error) {
	name, _ := args["skill_name"].(string)
	name = strings.TrimSpace(name)
	requested, _ := args["path"].(string)
	requested = strings.TrimSpace(strings.ReplaceAll(requested, "\\", "/"))
	if name == "" || requested == "" {
		return "", fmt.Errorf("%s: skill_name and path are required", ReadSkillResourceToolName)
	}
	clean := pathpkg.Clean(requested)
	if clean == ".." || strings.HasPrefix(clean, "../") || pathpkg.IsAbs(clean) {
		return "", fmt.Errorf("%s: path must stay inside the skill package", ReadSkillResourceToolName)
	}
	skill := r.registry[name]
	if skill == nil {
		return "", fmt.Errorf("%s: skill %q is not attached", ReadSkillResourceToolName, name)
	}
	canonical := skill.Name
	if canonical == "" {
		canonical = skill.ID
	}
	r.mu.Lock()
	loaded := r.loadedSkills[canonical]
	r.mu.Unlock()
	if !loaded {
		return "", fmt.Errorf("%s: load skill %q first", ReadSkillResourceToolName, canonical)
	}
	if err := service.CheckExecution(r.ctx, service.ExecutionAction{Kind: "resource", Name: "skills.use", ResourceID: skill.ID}); err != nil {
		return "", err
	}
	for _, resource := range skill.Resources {
		if resource.Path == clean {
			return resource.Content, nil
		}
	}
	return "", fmt.Errorf("%s: resource %q not found in skill %q", ReadSkillResourceToolName, clean, canonical)
}

// IsSkillLoaded reports whether a skill (by canonical name or any registry
// key) has been activated. Used in tests; not part of the dispatch fast-path.
func (r *SkillRuntime) IsSkillLoaded(nameOrID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loadedSkills[nameOrID] {
		return true
	}
	if skill, ok := r.registry[nameOrID]; ok && skill != nil {
		canonical := skill.Name
		if canonical == "" {
			canonical = skill.ID
		}
		return r.loadedSkills[canonical]
	}
	return false
}

func (r *SkillRuntime) catalogNames() string {
	names := make([]string, len(r.catalog))
	for i, e := range r.catalog {
		names[i] = e.Name
	}
	return strings.Join(names, ", ")
}
