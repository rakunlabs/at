package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/rakunlabs/query"

	"github.com/rakunlabs/at/internal/service"
)

func nonHostToolContext(t *testing.T, user, workspace, run, session string, tools ...string) context.Context {
	t.Helper()
	ctx, err := service.BindExecution(t.Context(), service.ExecutionProvenance{
		RunID: run, UserID: user, WorkspaceID: workspace, Source: "chat",
	}, t.TempDir(), func(_ context.Context, _ service.ExecutionProvenance, action service.ExecutionAction) (service.ExecutionValidation, error) {
		if action.Name == "execution.host" {
			t.Fatal("non-host tool requested host authority")
		}
		return service.ExecutionValidation{Allowed: true, Policy: service.ExecutionPolicy{
			WorkspaceID: workspace, Mode: service.ExecutionRestricted, AllowedTools: tools,
		}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return contextWithSessionID(ctx, session)
}

func TestNonHostBuiltinAuthorization(t *testing.T) {
	for _, name := range []string{"current_time", "whoami", "todo_read", "todo_write", "get_user_preferences", "set_user_preference", "guide_list", "guide_get"} {
		t.Run(name, func(t *testing.T) {
			if host, known := service.ExecutionToolClass(name); host || !known {
				t.Fatalf("tool class: host=%v known=%v", host, known)
			}
			ctx := nonHostToolContext(t, "u1", "w1", "r1", "s1", name)
			if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "tool", Name: name}); err != nil {
				t.Fatal(err)
			}
			denied := nonHostToolContext(t, "u1", "w1", "r1", "s1")
			if _, err := (&Server{}).dispatchBuiltinTool(denied, name, nil); !errors.Is(err, service.ErrExecutionDenied) {
				t.Fatalf("missing tool permission: %v", err)
			}
			for _, feature := range []string{service.FeatureBuiltinTools, service.FeatureBuiltinOther} {
				s := toolFeatureServer(map[string]bool{feature: false})
				if _, err := s.dispatchBuiltinTool(ctx, name, nil); err == nil {
					t.Fatalf("disabled feature %s admitted tool", feature)
				}
			}
		})
	}
}

func TestNonHostTodoIsolation(t *testing.T) {
	s := &Server{todos: newTodoStore()}
	tools := []string{"todo_read", "todo_write"}
	owner := nonHostToolContext(t, "u1", "w1", "r1", "shared", tools...)
	args := map[string]any{"todos": []todoItem{{Content: "private todo", Status: "pending", Priority: "low"}}}
	if _, err := s.dispatchBuiltinTool(owner, "todo_write", args); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		user, workspace, run, session string
		found                         bool
	}{
		{"u1", "w1", "r2", "shared", true},
		{"u2", "w1", "r1", "shared", false},
		{"u1", "w2", "r1", "shared", false},
		{"u1", "w1", "r1", "other", false},
		{"u1", "w1", "r1", "", false},
	} {
		ctx := nonHostToolContext(t, tc.user, tc.workspace, tc.run, tc.session, tools...)
		raw, err := s.dispatchBuiltinTool(ctx, "todo_read", nil)
		if err != nil || strings.Contains(raw, "private todo") != tc.found {
			t.Fatalf("scope %+v: %s, %v", tc, raw, err)
		}
	}
	noSession := nonHostToolContext(t, "u1", "w1", "r1", "", tools...)
	if _, err := s.dispatchBuiltinTool(noSession, "todo_write", args); err != nil {
		t.Fatal(err)
	}
	otherRun := nonHostToolContext(t, "u1", "w1", "r2", "", tools...)
	if raw, err := s.dispatchBuiltinTool(otherRun, "todo_read", nil); err != nil || strings.Contains(raw, "private todo") {
		t.Fatalf("sessionless runs shared todos: %s, %v", raw, err)
	}
	if _, err := s.execTodoRead(t.Context(), nil); err == nil {
		t.Fatal("unbound todo read admitted")
	}
}

type personalPreferenceStore struct {
	service.UserPreferenceStorer
	user   string
	prefs  []service.UserPreference
	writes []service.UserPreference
}

func (s *personalPreferenceStore) ListUserPreferences(_ context.Context, user string) ([]service.UserPreference, error) {
	s.user = user
	return s.prefs, nil
}

func (s *personalPreferenceStore) SetPublicUserPreference(_ context.Context, pref service.UserPreference) error {
	for _, p := range s.prefs {
		if p.UserID == pref.UserID && p.Key == pref.Key && p.Secret {
			return service.ErrAccessDenied
		}
	}
	s.writes = append(s.writes, pref)
	return nil
}

func TestNonHostPersonalPreferences(t *testing.T) {
	store := &personalPreferenceStore{prefs: []service.UserPreference{
		{UserID: "u1", Key: "timezone", Value: json.RawMessage(`"Europe/Istanbul"`)},
		{UserID: "u1", Key: "location", Value: json.RawMessage(`"secret"`), Secret: true},
		{UserID: "u1", Key: "developer_home", Value: json.RawMessage(`{"enabled":true}`)},
		{UserID: "u1", Key: "playground_presets", Value: json.RawMessage(`[]`)},
	}}
	s := &Server{userPrefStore: store}
	ctx := nonHostToolContext(t, "u1", "w1", "r1", "s1", "get_user_preferences", "set_user_preference")
	ctx = contextWithSessionUserID(ctx, "someone-else")
	raw, err := s.dispatchBuiltinTool(ctx, "get_user_preferences", map[string]any{"user_id": "someone-else"})
	if err != nil {
		t.Fatal(err)
	}
	if store.user != "u1" || raw != `{"timezone":"Europe/Istanbul"}` {
		t.Fatalf("unexpected preferences for %s: %s", store.user, raw)
	}
	if _, err := s.dispatchBuiltinTool(ctx, "set_user_preference", map[string]any{"key": "language", "value": "tr", "user_id": "someone-else"}); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) != 1 || store.writes[0].UserID != "u1" || store.writes[0].Secret {
		t.Fatalf("unexpected writes: %+v", store.writes)
	}
	for _, key := range []string{"developer_home", "local_mcp_servers", "playground_presets", "location", "arbitrary"} {
		if _, err := s.dispatchBuiltinTool(ctx, "set_user_preference", map[string]any{"key": key, "value": "overwrite"}); err == nil {
			t.Fatalf("unsafe key %s admitted", key)
		}
	}
	if len(store.writes) != 1 {
		t.Fatal("unsafe writes reached store")
	}
	if _, err := s.execGetUserPreferences(t.Context(), nil); err == nil {
		t.Fatal("unbound preference read admitted")
	}
}

type nonHostGuideStore struct {
	service.GuideStorer
}

func (*nonHostGuideStore) ListGuides(context.Context, *query.Query) (*service.ListResult[service.Guide], error) {
	return &service.ListResult[service.Guide]{Data: []service.Guide{{ID: "guide1", Title: "Help"}}}, nil
}

func (*nonHostGuideStore) GetGuide(_ context.Context, id string) (*service.Guide, error) {
	if id != "guide1" {
		return nil, nil
	}
	return &service.Guide{ID: id, Title: "Help"}, nil
}

func TestNonHostGuideDispatch(t *testing.T) {
	s := &Server{guideStore: &nonHostGuideStore{}}
	ctx := nonHostToolContext(t, "u1", "w1", "r1", "s1", "guide_list", "guide_get")
	for _, name := range []string{"guide_list", "guide_get"} {
		if raw, err := s.dispatchBuiltinTool(ctx, name, map[string]any{"id": "guide1"}); err != nil || !strings.Contains(raw, "Help") {
			t.Fatalf("%s: %s, %v", name, raw, err)
		}
	}
	if _, err := s.dispatchBuiltinTool(ctx, "guide_get", map[string]any{"id": "missing"}); err == nil {
		t.Fatal("missing guide admitted")
	}
	s = toolFeatureServer(map[string]bool{service.FeatureGuides: false})
	if _, err := s.dispatchBuiltinTool(ctx, "guide_list", nil); err == nil {
		t.Fatal("disabled documentation admitted")
	}
}
