package server

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestBuiltinRegistryComplete(t *testing.T) {
	if builtinToolRegistryErr != nil {
		t.Fatal(builtinToolRegistryErr)
	}
	if len(builtinToolRegistry.byName) != len(builtinToolSchemas) || !reflect.DeepEqual(builtinTools, builtinToolSchemas) {
		t.Fatal("discovery schemas/order differ from the validated registry")
	}
	nonHost := []string{"file_read", "file_write", "file_list", "batch_execute", "todo_read", "todo_write", "current_time", "whoami", "get_user_preferences", "set_user_preference", "guide_list", "guide_get", "agent_run", "agent_run_status", "agent_run_cancel", "decide", "generate_image", "telegram_notify", "run_log"}
	hostFiles := []string{"file_edit", "file_multiedit", "file_patch", "file_glob", "file_grep"}
	workflowDefs := (&Server{}).builtinToolDefsForWorkflow()
	for i, def := range builtinTools {
		t.Run(def.Name, func(t *testing.T) {
			entry, ok := builtinToolRegistry.byName[def.Name]
			if !ok || !isKnownBuiltinTool(def.Name) || entry.Execute == nil || !reflect.DeepEqual(entry.builtinToolDef, def) {
				t.Fatal("missing or inconsistent runtime registration")
			}
			want := builtinHost
			if slices.Contains(nonHost, def.Name) {
				want = builtinNonHost
			}
			if slices.Contains(hostFiles, def.Name) {
				want = builtinHostFiles
			}
			host, known := service.ExecutionToolClass(def.Name)
			if entry.Class != want || !known || host != (want != builtinNonHost) {
				t.Fatalf("execution classification changed: entry=%v, host=%v known=%v, want=%v", entry.Class, host, known, want)
			}
			if workflowDefs[i].Name != def.Name || !reflect.DeepEqual(workflowDefs[i].InputSchema, def.InputSchema) {
				t.Fatal("workflow discovery differs from registry")
			}
			ctx := nonHostToolContext(t, "u1", "w1", "r1", "s1", def.Name)
			err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "tool", Name: def.Name})
			if want == builtinNonHost && err != nil {
				t.Fatal(err)
			}
			if want != builtinNonHost && !errors.Is(err, service.ErrExecutionDenied) {
				t.Fatalf("host tool admitted in Restricted mode: %v", err)
			}
		})
	}
	if isKnownBuiltinTool("unknown_tool") {
		t.Fatal("unknown tool advertised")
	}
	if _, err := (&Server{}).dispatchBuiltinTool(t.Context(), "unknown_tool", nil); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatalf("unknown dispatch: %v", err)
	}
	wire, err := json.Marshal(builtinTools)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{`"Execute"`, `"Class"`, `"execute"`, `"class"`} {
		if strings.Contains(string(wire), private) {
			t.Fatalf("private runtime metadata leaked: %s", private)
		}
	}
}

func TestBuiltinRegistryRejectsIncompleteEntries(t *testing.T) {
	def := builtinToolDef{Name: "test", Description: "test tool", InputSchema: map[string]any{"type": "object"}}
	exec := func(*Server, context.Context, map[string]any) (string, error) { return "ok", nil }
	binding := builtinToolBinding{Name: "test", Execute: exec, Class: builtinNonHost}
	for _, tc := range []struct {
		name     string
		defs     []builtinToolDef
		bindings []builtinToolBinding
	}{
		{"missing executor", []builtinToolDef{def}, nil},
		{"missing schema", nil, []builtinToolBinding{binding}},
		{"duplicate schema", []builtinToolDef{def, def}, []builtinToolBinding{binding}},
		{"duplicate executor", []builtinToolDef{def}, []builtinToolBinding{binding, binding}},
		{"nil executor", []builtinToolDef{def}, []builtinToolBinding{{Name: "test", Class: builtinNonHost}}},
		{"unset class", []builtinToolDef{def}, []builtinToolBinding{{Name: "test", Execute: exec}}},
		{"invalid class", []builtinToolDef{def}, []builtinToolBinding{{Name: "test", Execute: exec, Class: 255}}},
		{"incomplete schema", []builtinToolDef{{Name: "test"}}, []builtinToolBinding{binding}},
		{"unnamed executor", []builtinToolDef{def}, []builtinToolBinding{{Execute: exec, Class: builtinHost}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry, err := newBuiltinRegistry(tc.defs, tc.bindings)
			if err == nil || len(registry.byName) != 0 || len(registry.definitions) != 0 {
				t.Fatalf("invalid registry not rejected atomically: %+v, %v", registry, err)
			}
		})
	}
}

func TestBuiltinRegistryHostFileGuard(t *testing.T) {
	for _, name := range []string{"file_edit", "file_multiedit", "file_patch", "file_glob", "file_grep"} {
		t.Run(name, func(t *testing.T) {
			ctx, err := service.BindExecution(t.Context(), service.ExecutionProvenance{RunID: "r1", UserID: "u1", WorkspaceID: "w1", Source: "chat"}, t.TempDir(), func(context.Context, service.ExecutionProvenance, service.ExecutionAction) (service.ExecutionValidation, error) {
				return service.ExecutionValidation{Allowed: true, Policy: service.ExecutionPolicy{WorkspaceID: "w1", Mode: service.ExecutionTrustedHost, GrantedBy: "admin", AllowedTools: []string{name}}}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := (&Server{}).dispatchBuiltinTool(ctx, name, nil); !errors.Is(err, service.ErrExecutionDenied) {
				t.Fatalf("host-file tool admitted non-platform user: %v", err)
			}
		})
	}
}
