package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func toolFeatureServer(flags map[string]bool) *Server {
	s := &Server{featureStore: &fakeFeatureStore{}}
	s.features.data.Store(&featureSnapshot{flags: flags, loadedAt: time.Now()})
	return s
}

func TestBuiltinToolFamilyCatalog(t *testing.T) {
	s := toolFeatureServer(map[string]bool{
		service.FeatureBuiltinScript: false,
		service.FeatureBuiltinHTTP:   false,
		service.FeatureBuiltinOther:  false,
	})
	for _, includeDisabled := range []bool{false, true} {
		path := "/api/v1/mcp/builtin-tools"
		if includeDisabled {
			path += "?include_disabled=true"
		}
		w := httptest.NewRecorder()
		s.BuiltinToolListAPI(w, httptest.NewRequest("GET", path, nil))
		var response struct{ Tools []builtinToolDef }
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if !includeDisabled && (len(response.Tools) != 1 || response.Tools[0].Name != "bash_execute") {
			t.Fatalf("shell-only discovery: %+v", response.Tools)
		}
		if includeDisabled {
			if len(response.Tools) != len(builtinTools) {
				t.Fatal("configuration metadata must retain disabled selections")
			}
			for _, tool := range response.Tools {
				if tool.Name != "bash_execute" && tool.DisabledBy == "" {
					t.Fatalf("disabled tool %s appeared available", tool.Name)
				}
			}
		}
	}
	// Parent availability does not rewrite any child's own switch.
	s.features.data.Store(&featureSnapshot{flags: map[string]bool{service.FeatureBuiltinTools: false}, loadedAt: time.Now()})
	w := httptest.NewRecorder()
	s.BuiltinToolListAPI(w, httptest.NewRequest("GET", "/api/v1/mcp/builtin-tools", nil))
	if !strings.Contains(w.Body.String(), `"tools":[]`) {
		t.Fatalf("master-off list: %s", w.Body.String())
	}
}

func TestBuiltinOtherPreservesResourceDependencies(t *testing.T) {
	for _, disabled := range []string{service.FeatureBuiltinTools, service.FeatureBuiltinOther, service.FeatureFiles} {
		s := toolFeatureServer(map[string]bool{disabled: false})
		if err := s.checkBuiltinToolFeatures(t.Context(), "file_read"); err == nil {
			t.Fatalf("file_read admitted with %s off", disabled)
		}
	}
	for _, name := range []string{"todo_read", "batch_execute", "provider_list", "file_read"} {
		s := toolFeatureServer(map[string]bool{service.FeatureBuiltinOther: false})
		if err := s.checkBuiltinToolFeatures(t.Context(), name); err == nil {
			t.Fatalf("other family admitted %s", name)
		}
	}
	for _, presetName := range []string{"minimal", "gateway_chat", "agent_platform", "full"} {
		preset, ok := featurePresetForKey(presetName)
		if !ok {
			t.Fatal(presetName)
		}
		want := presetName == "agent_platform" || presetName == "full"
		if got := featurePresetTargets(preset)[service.FeatureBuiltinOther]; got != want {
			t.Fatalf("%s: other=%v, want %v", presetName, got, want)
		}
	}
}

func TestBuiltinFeatureChangeBlocksDispatchAndBatch(t *testing.T) {
	ctx := runtimeTestContext(t, t.TempDir(), &atomic.Bool{})
	s := toolFeatureServer(map[string]bool{})
	if _, err := s.dispatchBuiltinTool(ctx, "file_list", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	s.features.data.Store(&featureSnapshot{flags: map[string]bool{service.FeatureFiles: false}, loadedAt: time.Now()})
	if _, err := s.dispatchBuiltinTool(ctx, "file_list", map[string]any{}); err == nil {
		t.Fatal("saved direct call bypassed feature disable")
	}
	result, err := s.dispatchBuiltinTool(ctx, "batch_execute", map[string]any{
		"tool_calls": []any{map[string]any{"name": "file_list", "arguments": map[string]any{}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "disabled") {
		t.Fatalf("nested call bypassed disable: %s", result)
	}
	s.features.data.Store(&featureSnapshot{flags: map[string]bool{service.FeatureBuiltinOther: false}, loadedAt: time.Now()})
	if _, err := s.dispatchBuiltinTool(ctx, "batch_execute", map[string]any{}); err == nil {
		t.Fatal("Other off must block batching itself")
	}
}

func TestBuiltinMCPFeatureFiltering(t *testing.T) {
	s := toolFeatureServer(map[string]bool{service.FeatureBuiltinOther: false})
	b := &mcpRuntimeBuilder{server: s}
	runtime := newMCPRuntime()
	b.addBuiltins(t.Context(), runtime, service.MCPServerConfig{EnabledBuiltinTools: []string{"bash_execute", "file_read", "todo_read"}})
	tools := runtime.Tools()
	if len(tools) != 1 || tools[0].Name != "bash_execute" {
		t.Fatalf("MCP leaked disabled tools: %+v", tools)
	}
}
