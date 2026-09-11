package server

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
)

func TestRuntimeAuditSpillsPreserveConcurrentRoots(t *testing.T) {
	s := &Server{}
	var wg sync.WaitGroup
	for _, label := range []string{"one", "two"} {
		root := t.TempDir()
		ctx := executiontest.WithRoot(t, root)
		wg.Add(1)
		go func() {
			defer wg.Done()
			payload := []byte(strings.Repeat(label, service.LLMCallBodyMaxBytes))
			_, truncated, ref := s.clipOrSpill("same-observation", "response", payload, ctx)
			if !truncated || !strings.HasPrefix(ref, root+string(filepath.Separator)) {
				t.Errorf("spill lost root: %q", ref)
				return
			}
			got, err := os.ReadFile(ref)
			if err != nil || string(got) != string(payload) {
				t.Errorf("mixed spill payload: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestRuntimeAuditAttributionDoesNotMutateCallerMetadata(t *testing.T) {
	ctx := executiontest.Context(t)
	metadata := map[string]any{"execution": "untrusted override", "iteration": 1}
	call := (&Server{}).buildLLMCall(ctx, llmAuditParams{source: "workflow", metadata: metadata})
	if metadata["execution"] != "untrusted override" {
		t.Fatal("recorder mutated caller metadata")
	}
	bound, ok := call.Metadata["execution"].(map[string]any)
	if !ok || bound["workspace_id"] != "legacy-default" || bound["initiator_user_id"] != "test-user" {
		t.Fatalf("missing execution provenance: %+v", call.Metadata)
	}
}
