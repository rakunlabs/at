package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// A Chats preset is a named, per-account workbench setup: the model, system
// prompt and the four tool selections the Chats page persists per conversation.
//
// It is the same payload the singleton `/chats/defaults` preset carries, which
// is why both share ChatWorkbenchSetup rather than declaring the fields twice.
// A preset is "a default with a name": if the two wire shapes could drift, a
// setup saved through one would come back incomplete through the other.

// ChatWorkbenchSetup is the selection half of a Chats preset.
//
// The server does not interpret these beyond bounding them — the Chats tool
// loop runs in the browser, and validating a tool name here would mean
// re-implementing that resolution twice, in a place that cannot see the
// caller's skills or MCP sets.
type ChatWorkbenchSetup struct {
	Model        string   `json:"model,omitempty"`
	SystemPrompt string   `json:"system_prompt,omitempty"`
	MCPSets      []string `json:"mcp_sets,omitempty"`
	Skills       []string `json:"skills,omitempty"`
	BuiltinTools []string `json:"builtin_tools,omitempty"`
	// FrontendTools are browser-only helpers (todo bookkeeping, the question
	// prompt). They have no server-side equivalent and are stored purely so a
	// restored setup keeps the same switches.
	// Preserve an explicit empty selection; nil means use the UI defaults.
	FrontendTools []string `json:"frontend_tools,omitzero"`
}

// ChatPreset is one named setup. Unlike the singleton default — which only
// seeds a *new* conversation — a preset is applied on demand, including to the
// conversation already open, because switching between setups mid-session is
// the reason for having more than one.
type ChatPreset struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	ChatWorkbenchSetup

	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// WorkspaceChatPreset is a setup shared with every member of one workspace.
// Everyone admitted to Chats may apply it; only the account that created it may
// update or delete it. CanEdit is derived for the current caller and is never
// persisted.
type WorkspaceChatPreset struct {
	ChatPreset

	WorkspaceID string `json:"workspace_id"`
	OwnerUserID string `json:"owner_user_id"`
	CanEdit     bool   `json:"can_edit"`
}

// WorkspaceChatPresetStorer persists workspace-shared Chats presets. The actor
// and selected workspace come from the access principal on ctx; implementations
// must not trust ownership supplied by a request body.
type WorkspaceChatPresetStorer interface {
	ListWorkspaceChatPresets(ctx context.Context, workspaceID string) ([]WorkspaceChatPreset, error)
	CreateWorkspaceChatPreset(ctx context.Context, preset WorkspaceChatPreset) (*WorkspaceChatPreset, error)
	UpdateWorkspaceChatPreset(ctx context.Context, id string, preset WorkspaceChatPreset) (*WorkspaceChatPreset, error)
	DeleteWorkspaceChatPreset(ctx context.Context, id string) error
}

var ErrChatPresetNameConflict = errors.New("a workspace preset already uses that name")

// Bounds. A personal preset list is a handful of entries; these exist so a
// client cannot store an unbounded blob in a preference row.
const (
	ChatPresetMaxCount        = 30
	ChatPresetMaxNameRunes    = 80
	ChatPresetMaxPromptBytes  = 32 << 10
	ChatPresetMaxSelection    = 200
	ChatPresetMaxSelectionLen = 256
)

// NormalizeChatPresets validates and canonicalizes a submitted preset list.
// It returns the first problem found, naming the offending entry, because the
// caller is a person editing a form.
//
// Names are unique case-insensitively: the list is a picker, and two entries a
// reader cannot tell apart are a defect rather than a choice.
func NormalizeChatPresets(presets []ChatPreset) ([]ChatPreset, error) {
	if len(presets) > ChatPresetMaxCount {
		return nil, fmt.Errorf("at most %d presets are supported", ChatPresetMaxCount)
	}

	out := make([]ChatPreset, 0, len(presets))
	seenID := make(map[string]bool, len(presets))
	seenName := make(map[string]bool, len(presets))

	for i := range presets {
		p := presets[i]
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			return nil, fmt.Errorf("preset %d: name is required", i+1)
		}
		if len([]rune(p.Name)) > ChatPresetMaxNameRunes {
			return nil, fmt.Errorf("preset %q: name is too long", p.Name)
		}
		lower := strings.ToLower(p.Name)
		if seenName[lower] {
			return nil, fmt.Errorf("preset %q: name is already used", p.Name)
		}
		seenName[lower] = true

		if p.ID != "" {
			if seenID[p.ID] {
				return nil, fmt.Errorf("preset %q: duplicate id", p.Name)
			}
			seenID[p.ID] = true
		}

		if len(p.SystemPrompt) > ChatPresetMaxPromptBytes {
			return nil, fmt.Errorf("preset %q: system prompt is too long", p.Name)
		}
		p.Model = strings.TrimSpace(p.Model)

		var err error
		if p.MCPSets, err = normalizePresetSelection(p.Name, "MCP sets", p.MCPSets); err != nil {
			return nil, err
		}
		if p.Skills, err = normalizePresetSelection(p.Name, "skills", p.Skills); err != nil {
			return nil, err
		}
		if p.BuiltinTools, err = normalizePresetSelection(p.Name, "built-in tools", p.BuiltinTools); err != nil {
			return nil, err
		}
		if p.FrontendTools, err = normalizePresetSelection(p.Name, "chat tools", p.FrontendTools); err != nil {
			return nil, err
		}

		out = append(out, p)
	}

	return out, nil
}

// normalizePresetSelection trims, drops blanks and de-duplicates one selection
// list while preserving order.
//
// A nil list stays nil and a non-nil one stays non-nil even when every entry
// was blank: for FrontendTools the difference between "not configured" and
// "explicitly none" is the whole meaning of the field, and collapsing an empty
// slice to nil would silently re-enable the shipped browser tools.
func normalizePresetSelection(preset, label string, values []string) ([]string, error) {
	if values == nil {
		return nil, nil
	}
	if len(values) > ChatPresetMaxSelection {
		return nil, fmt.Errorf("preset %q: at most %d %s are supported", preset, ChatPresetMaxSelection, label)
	}

	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if len(trimmed) > ChatPresetMaxSelectionLen {
			return nil, fmt.Errorf("preset %q: a %s entry is too long", preset, label)
		}
		if seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}

	return out, nil
}
