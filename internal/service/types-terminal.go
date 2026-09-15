package service

import "context"

// Terminals are installation-admin-owned host resources, independent of workspaces.
type TerminalSession struct {
	ID         string `json:"id" db:"id"`
	OwnerID    string `json:"-" db:"owner_id"`
	TargetID   string `json:"target_id" db:"target_id"`
	TargetName string `json:"target_name" db:"target_name"`
	Username   string `json:"username" db:"username"`
	Title      string `json:"title" db:"title"`
	Position   int    `json:"position" db:"position"`
}

type TerminalPreferences struct {
	ActiveID     string            `json:"active_id"`
	DefaultUsers map[string]string `json:"default_users"`
	// Appearance selects the terminal palette independently of the page theme:
	// "dark", "light" or "system". Empty means dark, so existing owners keep a
	// dark terminal without a migration.
	Appearance string `json:"appearance,omitempty"`
	// FontFamily is a client-side font name. The glyphs must be installed on the
	// device running the browser; the host font set is irrelevant. Empty uses the
	// built-in monospace stack, which also backs any missing font.
	FontFamily string `json:"font_family,omitempty"`
	// FontSize is in CSS pixels; empty means the 14px default.
	FontSize int `json:"font_size,omitempty"`
	// KeyBar controls the touch key row: "on", "off", or "auto"/empty, which
	// shows it only on touch devices. One account can use both a phone and a
	// desktop, so the stored value stays device-independent.
	KeyBar string `json:"key_bar,omitempty"`
}

type TerminalStorer interface {
	ListTerminals(context.Context, string) ([]TerminalSession, error)
	GetTerminal(context.Context, string, string) (*TerminalSession, error)
	CreateTerminal(context.Context, TerminalSession) error
	UpdateTerminal(context.Context, string, string, string, int) error
	DeleteTerminal(context.Context, string, string) error
	// OperateTerminal locks and rechecks an owned row through the host lifecycle
	// operation. Removal commits under the same lock, preventing restart/delete races.
	OperateTerminal(context.Context, string, string, bool, func(TerminalSession) error) error
	GetTerminalPreferences(context.Context, string) (TerminalPreferences, error)
	SetTerminalPreferences(context.Context, string, TerminalPreferences) error
}
