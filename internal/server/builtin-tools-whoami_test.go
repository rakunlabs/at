package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

type whoamiStore struct {
	service.ProviderStorer
	users map[string]*service.AuthUser
	links map[string][]service.AuthUserIdentity
}

func TestWhoamiDispatchRestricted(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		name := "allowed"
		if !allowed {
			name = "denied"
		}
		t.Run(name, func(t *testing.T) {
			s := &Server{store: &whoamiStore{users: map[string]*service.AuthUser{"u1": {ID: "u1", Username: "ada"}}}}
			policy := service.ExecutionPolicy{WorkspaceID: "w1", Mode: service.ExecutionRestricted}
			if allowed {
				policy.AllowedTools = []string{"whoami"}
			}
			ctx, err := service.BindExecution(t.Context(), service.ExecutionProvenance{RunID: "r", UserID: "u1", WorkspaceID: "w1", Source: "chat"}, t.TempDir(), func(_ context.Context, _ service.ExecutionProvenance, action service.ExecutionAction) (service.ExecutionValidation, error) {
				if action.Name == "execution.host" {
					t.Fatal("whoami must not request host authority")
				}
				return service.ExecutionValidation{Allowed: true, Policy: policy}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := s.dispatchBuiltinTool(ctx, "whoami", map[string]any{"user_id": "someone-else"})
			if !allowed {
				if !errors.Is(err, service.ErrExecutionDenied) {
					t.Fatalf("expected execution denial, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got whoamiResult
			if err := json.Unmarshal([]byte(raw), &got); err != nil {
				t.Fatal(err)
			}
			if got.UserID != "u1" || got.Username != "ada" {
				t.Fatalf("unexpected whoami: %+v", got)
			}
		})
	}
	if _, err := (&Server{}).dispatchBuiltinTool(context.Background(), "whoami", nil); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatalf("unbound whoami must be denied, got %v", err)
	}
}

func (w *whoamiStore) GetAuthUserByID(_ context.Context, id string) (*service.AuthUser, error) {
	return w.users[id], nil
}

func (w *whoamiStore) ListAuthUserIdentities(_ context.Context, ids []string) (map[string][]service.AuthUserIdentity, error) {
	out := map[string][]service.AuthUserIdentity{}
	for _, id := range ids {
		out[id] = w.links[id]
	}
	return out, nil
}

func (w *whoamiStore) ListAuthUserWorkspaces(context.Context, string) ([]service.AuthUserWorkspace, error) {
	return nil, nil
}

// whoami answers from the bound execution identity and the stored account. The
// generated external-<ulid> username is replaced by the provider's handle, the
// name and verified email come from the identity link, and an unverified
// address is never promoted to the top-level email.
func TestWhoamiReportsBoundAccount(t *testing.T) {
	store := &whoamiStore{
		users: map[string]*service.AuthUser{"u1": {ID: "u1", Username: "external-01abc"}},
		links: map[string][]service.AuthUserIdentity{"u1": {
			{ProviderID: "unverified", Username: "", Email: "spoof@example.test"},
			{ProviderID: "keycloak", Username: "ada", DisplayName: "Ada Lovelace", Email: "ada@example.test", EmailVerified: true},
		}},
	}
	s := &Server{store: store}

	ctx, err := service.BindExecution(t.Context(), service.ExecutionProvenance{RunID: "r", UserID: "u1", WorkspaceID: "w1", Source: "chat"}, t.TempDir(), func(context.Context, service.ExecutionProvenance, service.ExecutionAction) (service.ExecutionValidation, error) {
		return service.ExecutionValidation{Allowed: true, Policy: service.ExecutionPolicy{WorkspaceID: "w1", Mode: service.ExecutionRestricted, AllowedTools: []string{"whoami"}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.execWhoami(ctx, map[string]any{"user_id": "someone-else"})
	if err != nil {
		t.Fatal(err)
	}
	var got whoamiResult
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got.UserID != "u1" || got.Username != "ada" || got.Name != "Ada Lovelace" || got.Email != "ada@example.test" || got.WorkspaceID != "w1" {
		t.Fatalf("unexpected whoami: %+v", got)
	}
	if len(got.Identities) != 2 || got.Service != "" {
		t.Fatalf("unexpected identities/service: %+v", got)
	}

	if _, err := s.execWhoami(t.Context(), nil); err == nil {
		t.Fatal("whoami without a bound identity must fail")
	}
}
