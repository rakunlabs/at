package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

type whoamiIdentity struct {
	Provider      string `json:"provider"`
	Username      string `json:"username,omitempty"`
	Name          string `json:"name,omitempty"`
	Email         string `json:"email,omitempty"`
	EmailVerified bool   `json:"email_verified,omitempty"`
}

type whoamiResult struct {
	UserID        string           `json:"user_id"`
	Username      string           `json:"username,omitempty"`
	Name          string           `json:"name,omitempty"`
	Email         string           `json:"email,omitempty"`
	WorkspaceID   string           `json:"workspace_id,omitempty"`
	WorkspaceName string           `json:"workspace_name,omitempty"`
	Role          string           `json:"role,omitempty"`
	PlatformAdmin bool             `json:"platform_admin,omitempty"`
	Identities    []whoamiIdentity `json:"identities,omitempty"`
	// Service is set when the run executes under a bot/MCP/trigger binding: the
	// account above is then the binding's run-as identity, not necessarily the
	// person talking to the agent.
	Service     string `json:"service,omitempty"`
	ChatUserID  string `json:"chat_user_id,omitempty"`
	Explanation string `json:"note,omitempty"`
}

// execWhoami reports the account this run executes as. Everything comes from
// the bound execution identity and the stored account, never from arguments,
// so the model cannot be talked into describing somebody else.
func (s *Server) execWhoami(ctx context.Context, _ map[string]any) (string, error) {
	prov, _, ok := service.ExecutionFromContext(ctx)
	if !ok || prov.UserID == "" {
		return "", fmt.Errorf("no authenticated user is bound to this run")
	}
	out := whoamiResult{UserID: prov.UserID, WorkspaceID: prov.WorkspaceID}

	if p, ok := service.AccessPrincipalFromContext(ctx); ok && p.UserID == prov.UserID {
		out.Role, out.PlatformAdmin = p.Role, p.PlatformAdmin
	}
	if users, ok := s.store.(interface {
		GetAuthUserByID(context.Context, string) (*service.AuthUser, error)
	}); ok {
		if u, err := users.GetAuthUserByID(ctx, prov.UserID); err == nil && u != nil {
			out.Username = u.Username
		}
	}
	if dir, ok := s.store.(service.AuthUserDirectory); ok {
		if links, err := dir.ListAuthUserIdentities(ctx, []string{prov.UserID}); err == nil {
			for _, l := range links[prov.UserID] {
				out.Identities = append(out.Identities, whoamiIdentity{Provider: l.ProviderID, Username: l.Username, Name: l.DisplayName, Email: l.Email, EmailVerified: l.EmailVerified})
				if out.Name == "" {
					out.Name = l.DisplayName
				}
				if out.Email == "" && l.EmailVerified {
					out.Email = l.Email
				}
			}
		}
	}
	// A generated `external-<ulid>` username names nobody; prefer the handle
	// the provider reports.
	if strings.HasPrefix(out.Username, "external-") {
		for _, l := range out.Identities {
			if l.Username != "" {
				out.Username = l.Username
				break
			}
		}
	}
	// The workspace name needs workspace.read; without it the ID is enough.
	if ws, ok := s.store.(service.WorkspaceStorer); ok {
		if w, err := ws.GetWorkspace(ctx); err == nil && w != nil && w.ID == prov.WorkspaceID {
			out.WorkspaceName = w.Name
		}
	}
	if prov.ServiceID != "" {
		out.Service = prov.Source
		out.ChatUserID = sessionUserIDFromContext(ctx)
		out.Explanation = "This run executes under a service binding; user_id is its run-as account. chat_user_id, when set, identifies the person in the chat channel."
	}

	data, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("marshal whoami: %w", err)
	}
	return string(data), nil
}
