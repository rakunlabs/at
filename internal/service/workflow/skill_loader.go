package workflow

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Lazy Skill Runtime (Progressive Disclosure) ───
//
// SkillRuntime resolves an agent's attached skills into an in-memory catalog
// once at the start of a run, but defers injecting their SystemPrompt and
// tool definitions into the LLM context until the LLM explicitly activates
// each skill via the `load_skill` meta-tool.
//
// This is the LLM-driven progressive disclosure pattern: the agent sees a
// short catalog ("you have these skills, call load_skill to activate one")
// instead of the union of every attached skill's prompt + tools — saving
// tokens and letting the LLM decide what it actually needs.
//
// Usage from an agentic loop:
//
//  1. rt, _ := NewSkillRuntime(ctx, reg.SkillLookup, agent.Config.Skills, refs)
//  2. systemPrompt += rt.CatalogSystemPrompt()
//  3. baseTools = append(baseTools, rt.LoadSkillToolDef())
//  4. each iteration:
//       llmTools = append(baseTools, rt.ActiveSkillTools()...)
//       resp = provider.Chat(...)
//       for _, tc := range resp.ToolCalls:
//         if tc.Name == "load_skill":
//             text, prompt, _ := rt.HandleLoadSkill(tc.Arguments)
//             // append `prompt` as a system follow-up message if non-empty
//             // append `text` as the tool_result
//             continue
//         if hi, ok := rt.HandlerFor(tc.Name); ok { ... dispatch ... }

// LoadSkillToolName is the meta-tool name the LLM calls to activate a skill.
const LoadSkillToolName = "load_skill"

// SkillCatalogEntry is a single row in the catalog presented to the LLM.
type SkillCatalogEntry struct {
	Name        string
	Description string
}

// SkillToolHandlerInfo captures a tool handler resolved from a loaded skill.
// It is the workflow-package counterpart of the per-node toolHandlerInfo
// structs that were previously duplicated in agent-call.go and
// chat-sessions.go.
type SkillToolHandlerInfo struct {
	Handler     string
	HandlerType string // "js" (default) | "bash" | "builtin" | "workflow" | "agent"
	SkillID     string
}

// SkillRuntime is the per-call lazy skill state. Safe for concurrent reads,
// guards loadedSkills mutation with a mutex (a single agentic loop is
// sequential but defensive locking keeps misuse cheap).
type SkillRuntime struct {
	mu sync.Mutex

	// registry maps lookup key (name AND id, when both are known) to the full
	// resolved Skill. Populated once in NewSkillRuntime; never mutated.
	registry map[string]*service.Skill

	// catalog is the ordered list shown to the LLM. Populated once.
	catalog []SkillCatalogEntry

	// loadedSkills tracks which skills the LLM has activated via load_skill.
	// The key is the canonical skill name (preferred) or the id when name is
	// empty. ActiveSkillTools enumerates entries here on each iteration.
	loadedSkills map[string]bool

	// connOverrides maps skillID -> per-skill connection bindings declared on
	// the agent's SkillRef entries. Caller reads via SkillConnOverrides.
	connOverrides map[string]map[string]string
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
		registry:      map[string]*service.Skill{},
		loadedSkills:  map[string]bool{},
		connOverrides: map[string]map[string]string{},
	}

	if lookup == nil {
		return rt, nil
	}

	// Collect raw connection overrides keyed by SkillRef.ID. We will also
	// re-key them by the resolved skill.ID below, so dispatch (which sees
	// the resolved ID via SkillToolHandlerInfo.SkillID) finds them.
	rawOverrides := map[string]map[string]string{}
	for _, ref := range refs {
		if ref.ID != "" && len(ref.Connections) > 0 {
			rawOverrides[ref.ID] = ref.Connections
			rt.connOverrides[ref.ID] = ref.Connections
		}
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

		// Mirror connection overrides under the resolved skill.ID so that
		// dispatch (which carries the resolved ID via the handler info) can
		// find them, regardless of whether the user attached by name or ID.
		if override, ok := rawOverrides[key]; ok && skill.ID != "" {
			rt.connOverrides[skill.ID] = override
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
	var b strings.Builder
	b.WriteString("## Available Skills\n\n")
	b.WriteString("You have access to the following skills. Each skill bundles a domain-specific prompt and a set of tools. ")
	b.WriteString("Skills are NOT loaded by default — you must call the `")
	b.WriteString(LoadSkillToolName)
	b.WriteString("` tool with the skill name to activate it. Once activated, the skill's instructions and tools become available for the rest of the conversation.\n\n")
	for _, e := range r.catalog {
		desc := e.Description
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&b, "- `%s` — %s\n", e.Name, desc)
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
		Description: "Activate one of your attached skills so its instructions and tools become available. Call this once per skill, when you decide the task needs that skill's capabilities.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"skill_name": map[string]any{
					"type":        "string",
					"description": "The name of the skill to load. Must match one of the catalog entries listed in your system prompt.",
					"enum":        enum,
				},
			},
			"required": []string{"skill_name"},
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

	toolNames := make([]string, 0, len(skill.Tools))
	for _, t := range skill.Tools {
		toolNames = append(toolNames, t.Name)
	}

	var b strings.Builder
	if len(toolNames) == 0 {
		fmt.Fprintf(&b, "Skill %q activated. This skill provides no additional tools.", canonical)
	} else {
		fmt.Fprintf(&b, "Skill %q activated. The following tools are now callable: %s.",
			canonical, strings.Join(toolNames, ", "))
	}
	if skill.SystemPrompt != "" {
		b.WriteString("\n\n=== Skill Instructions ===\n")
		b.WriteString(skill.SystemPrompt)
		b.WriteString("\n=== End Skill Instructions ===")
	}

	return b.String(), nil
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

// ActiveSkillTools returns the tool definitions of every loaded skill
// (handler stripped). The caller appends these to the iteration's llmTools
// alongside MCP tools, builtin tools, etc.
func (r *SkillRuntime) ActiveSkillTools() []service.Tool {
	r.mu.Lock()
	loaded := make([]string, 0, len(r.loadedSkills))
	for k := range r.loadedSkills {
		loaded = append(loaded, k)
	}
	r.mu.Unlock()
	sort.Strings(loaded)

	var out []service.Tool
	seen := map[string]bool{}
	for _, name := range loaded {
		skill := r.registry[name]
		if skill == nil {
			continue
		}
		for _, t := range skill.Tools {
			if t.Name == "" || seen[t.Name] {
				continue
			}
			seen[t.Name] = true
			out = append(out, service.Tool{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			})
		}
	}
	return out
}

// HandlerFor looks up a tool handler for a given tool name across loaded
// skills. Tools from unloaded skills are intentionally invisible — the LLM
// should not see them, and a defensive lookup miss surfaces as a "no
// handler" error to the LLM (existing behaviour).
func (r *SkillRuntime) HandlerFor(toolName string) (SkillToolHandlerInfo, bool) {
	r.mu.Lock()
	loaded := make([]string, 0, len(r.loadedSkills))
	for k := range r.loadedSkills {
		loaded = append(loaded, k)
	}
	r.mu.Unlock()

	for _, name := range loaded {
		skill := r.registry[name]
		if skill == nil {
			continue
		}
		for _, t := range skill.Tools {
			if t.Name != toolName {
				continue
			}
			if t.Handler == "" {
				continue
			}
			return SkillToolHandlerInfo{
				Handler:     t.Handler,
				HandlerType: t.HandlerType,
				SkillID:     skill.ID,
			}, true
		}
	}
	return SkillToolHandlerInfo{}, false
}

// SkillConnOverrides returns the per-skill connection map declared on the
// owning SkillRef, or nil when none. Used by the dispatch loop to layer
// per-skill overrides on top of agent-level connection bindings.
func (r *SkillRuntime) SkillConnOverrides(skillID string) map[string]string {
	if skillID == "" {
		return nil
	}
	return r.connOverrides[skillID]
}

func (r *SkillRuntime) catalogNames() string {
	names := make([]string, len(r.catalog))
	for i, e := range r.catalog {
		names[i] = e.Name
	}
	return strings.Join(names, ", ")
}
