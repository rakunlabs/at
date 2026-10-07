package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// A Chats command is a named prompt template run from the composer as
// `/name arguments`. The template is expanded in the browser ($ARGUMENTS,
// $1..$9) and sent as an ordinary user message, so a command never grants
// anything a typed message could not do. Personal commands live in
// user_preferences; workspace commands are shared rows whose creator alone
// may change them, the same split as Chats presets.

type ChatCommand struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Template    string `json:"template"`
	// Model optionally runs the command on another `provider/model` for that
	// one message; empty keeps the conversation's model.
	Model string `json:"model,omitempty"`

	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type WorkspaceChatCommand struct {
	ChatCommand

	WorkspaceID string `json:"workspace_id"`
	OwnerUserID string `json:"owner_user_id"`
	CanEdit     bool   `json:"can_edit"`
}

// WorkspaceChatCommandStorer persists workspace-shared commands. The actor and
// workspace come from the access principal on ctx, never from the body.
type WorkspaceChatCommandStorer interface {
	ListWorkspaceChatCommands(ctx context.Context, workspaceID string) ([]WorkspaceChatCommand, error)
	CreateWorkspaceChatCommand(ctx context.Context, command WorkspaceChatCommand) (*WorkspaceChatCommand, error)
	UpdateWorkspaceChatCommand(ctx context.Context, id string, command WorkspaceChatCommand) (*WorkspaceChatCommand, error)
	DeleteWorkspaceChatCommand(ctx context.Context, id string) error
}

var ErrChatCommandNameConflict = errors.New("a workspace command already uses that name")

const (
	ChatCommandMaxCount        = 100
	ChatCommandMaxDescription  = 200
	ChatCommandMaxTemplateSize = 32 << 10
)

var chatCommandName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

// ReservedChatCommands are the built-in Chats commands. A custom command may
// not shadow one: `/compact` must always mean compaction.
var ReservedChatCommands = []string{
	"compact", "new", "clear", "share", "model", "models", "preset", "presets",
	"effort", "tools", "workbench", "commands", "help",
}

// NormalizeChatCommands validates a submitted list, naming the first problem.
func NormalizeChatCommands(commands []ChatCommand) ([]ChatCommand, error) {
	if len(commands) > ChatCommandMaxCount {
		return nil, fmt.Errorf("at most %d commands are supported", ChatCommandMaxCount)
	}
	out := make([]ChatCommand, 0, len(commands))
	seenName := make(map[string]bool, len(commands))
	seenID := make(map[string]bool, len(commands))
	for i, c := range commands {
		c.Name = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(c.Name), "/"))
		if c.Name == "" {
			return nil, fmt.Errorf("command %d: name is required", i+1)
		}
		if !chatCommandName.MatchString(c.Name) {
			return nil, fmt.Errorf("command %q: use 1-32 lowercase letters, digits, '.', '_' or '-'", c.Name)
		}
		for _, reserved := range ReservedChatCommands {
			if c.Name == reserved {
				return nil, fmt.Errorf("command %q: name is reserved for a built-in command", c.Name)
			}
		}
		if seenName[c.Name] {
			return nil, fmt.Errorf("command %q: name is already used", c.Name)
		}
		seenName[c.Name] = true
		if c.ID != "" {
			if seenID[c.ID] {
				return nil, fmt.Errorf("command %q: duplicate id", c.Name)
			}
			seenID[c.ID] = true
		}
		c.Description = strings.TrimSpace(c.Description)
		if len([]rune(c.Description)) > ChatCommandMaxDescription {
			return nil, fmt.Errorf("command %q: description is too long", c.Name)
		}
		if strings.TrimSpace(c.Template) == "" {
			return nil, fmt.Errorf("command %q: template is required", c.Name)
		}
		if len(c.Template) > ChatCommandMaxTemplateSize {
			return nil, fmt.Errorf("command %q: template is too long", c.Name)
		}
		c.Model = strings.TrimSpace(c.Model)
		if len(c.Model) > 256 {
			return nil, fmt.Errorf("command %q: model is too long", c.Name)
		}
		out = append(out, c)
	}

	return out, nil
}
