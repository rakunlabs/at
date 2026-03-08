# Testing Patterns

**Analysis Date:** 2026-03-08

## Test Framework

**Runner:**
- Go standard `testing` package
- No third-party test runner (no testify, no gomock, no gocheck)
- Config: none — uses `go test` defaults

**Assertion Library:**
- Manual assertions via `t.Fatalf`, `t.Errorf`, `t.Fatal`, `t.Error`
- No assertion libraries (no testify/assert, no gomega)

**Run Commands:**
```bash
make test               # Run all tests: go test -v -race ./...
go test -v -race ./internal/crypto         # Single package
go test -v -race -run TestIsEncrypted ./internal/crypto  # Single test
```

## Test File Organization

**Location:**
- Co-located with source files (same directory)
- No separate `test/` or `testdata/` directories

**Naming:**
- Standard Go: `*_test.go`
- Named after the source file or feature: `crypto_test.go`, `schema_test.go`, `engine_test.go`, `nodes_test.go`, `parse_test.go`, `gateway-rag-mcp_test.go`

**Current Test Files (6 total):**
```
internal/crypto/crypto_test.go                          # 238 lines — encryption round-trip, key derivation, LLMConfig helpers
internal/service/schema_test.go                         # 401 lines — JSON schema sanitization
internal/service/workflow/engine_test.go                # 161 lines — reachableNodes graph traversal
internal/service/workflow/nodes/nodes_test.go           # 850 lines — conditional, loop, script, llm_call, agent_call, exec nodes
internal/skillmd/parse_test.go                          # 114 lines — markdown frontmatter parsing
internal/server/gateway-rag-mcp_test.go                 # 261 lines — URL parsing helpers (splitSourceToRepoAndPath, isSSHSource, hashCacheKey)
```

## Test Structure

**Suite Organization:**
- Group related tests with section dividers:
```go
// ═══════════════════════════════════════════════════════════════════
// conditional node tests
// ═══════════════════════════════════════════════════════════════════

func TestConditional_TrueExpression(t *testing.T) { ... }
func TestConditional_FalseExpression(t *testing.T) { ... }
func TestConditional_EmptyExpression_ValidateError(t *testing.T) { ... }
```

**Test Naming Convention:**
- `Test{Component}_{Scenario}` format:
  - `TestEncryptDecryptRoundTrip`
  - `TestConditional_TrueExpression`
  - `TestLLMCall_HappyPath`
  - `TestLoop_EmptyArray_StopBranch`
  - `TestExec_AllowInputOverride_DefaultFalse`
  - `TestSanitizeSchema_StripsUnsupportedKeys`
  - `TestParse_ValidSkillMD`

**Patterns:**
- Setup: inline within each test function (no shared `TestMain` or setup/teardown)
- Teardown: not needed — tests use in-memory constructs
- Assertions: `if got != want { t.Fatalf(...) }` or `t.Errorf(...)` pattern

## Table-Driven Tests

**Standard Pattern:**
```go
func TestIsEncrypted(t *testing.T) {
    tests := []struct {
        value string
        want  bool
    }{
        {"enc:abc123", true},
        {"enc:", true},
        {"ENC:abc", false},
        {"plaintext", false},
        {"", false},
    }

    for _, tt := range tests {
        if got := IsEncrypted(tt.value); got != tt.want {
            t.Errorf("IsEncrypted(%q) = %v, want %v", tt.value, got, tt.want)
        }
    }
}
```

**With Subtests:**
```go
func TestSplitSourceToRepoAndPath(t *testing.T) {
    tests := []struct {
        name     string
        source   string
        wantRepo string
        wantPath string
    }{
        {
            name:     "ssh scp-style with .git",
            source:   "git@github.com:user/repo.git/path/to/file.md",
            wantRepo: "git@github.com:user/repo.git",
            wantPath: "path/to/file.md",
        },
        // ... more cases with section comments like:
        // ── SSH SCP-style with .git suffix ──
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            gotRepo, gotPath := splitSourceToRepoAndPath(tt.source)
            if gotRepo != tt.wantRepo {
                t.Errorf("splitSourceToRepoAndPath(%q) repo = %q, want %q", tt.source, gotRepo, tt.wantRepo)
            }
        })
    }
}
```

**Table test struct conventions:**
- Use `name` field for subtest description
- Use `want` prefix for expected values: `wantRepo`, `wantPath`, `want`
- Section comments within test table for grouping related cases

## Mocking

**Framework:** Hand-rolled mocks — no mock generation tools (no gomock, no mockgen, no testify/mock).

**Pattern — Struct with function fields:**
```go
type mockProvider struct {
    chatFunc func(ctx context.Context, model string, messages []service.Message, tools []service.Tool) (*service.LLMResponse, error)
}

func (m *mockProvider) Chat(ctx context.Context, model string, messages []service.Message, tools []service.Tool) (*service.LLMResponse, error) {
    if m.chatFunc != nil {
        return m.chatFunc(ctx, model, messages, tools)
    }
    return &service.LLMResponse{Content: "mock response", Finished: true}, nil
}
```

**Usage — Capture and verify calls:**
```go
var capturedModel string
mp := &mockProvider{
    chatFunc: func(ctx context.Context, model string, messages []service.Message, tools []service.Tool) (*service.LLMResponse, error) {
        capturedModel = model
        return &service.LLMResponse{Content: "ok", Finished: true}, nil
    },
}
// ... run test ...
if capturedModel != "default-model" {
    t.Fatalf("expected default-model, got %s", capturedModel)
}
```

**What to Mock:**
- LLM providers (`service.LLMProvider` interface) — use `mockProvider` struct
- Registry lookup functions — pass `nil` or custom functions to `workflow.NewRegistry()`

**What NOT to Mock:**
- Goja JS VM — tests run real JavaScript expressions
- Shell execution (`/bin/sh -c`) — exec node tests run actual commands

## Test Helpers

**Helper Functions:**
```go
// internal/crypto/crypto_test.go
func testKey() []byte {
    key, _ := DeriveKey("test-encryption-key-for-unit-tests")
    return key
}

// internal/service/workflow/nodes/nodes_test.go
func newTestRegistry() *workflow.Registry {
    return workflow.NewRegistry(
        nil, // providerLookup
        nil, // skillLookup
        nil, // varLookup
        // ... all nil for minimal registry
    )
}

func newTestRegistryWithProvider(mp *mockProvider) *workflow.Registry {
    return workflow.NewRegistry(
        func(key string) (service.LLMProvider, string, error) {
            if key == "test-provider" {
                return mp, "default-model", nil
            }
            return nil, "", errors.New("provider not found: " + key)
        },
        nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
    )
}

func makeNode(t *testing.T, typeName string, data map[string]any) workflow.Noder {
    t.Helper()
    factory := workflow.GetNodeFactory(typeName)
    if factory == nil {
        t.Fatalf("node type %q not registered", typeName)
    }
    noder, err := factory(service.WorkflowNode{
        ID:   "test-node",
        Type: typeName,
        Data: data,
    })
    if err != nil {
        t.Fatalf("factory(%q): %v", typeName, err)
    }
    return noder
}
```

**Conventions:**
- Use `t.Helper()` in helper functions for correct line reporting
- Helper functions defined at top of test file, before test functions
- Registry helpers accept nil for unused lookups

## Fixtures and Factories

**Test Data:**
- Inline within each test — no shared fixtures
- String literals, map literals, and struct literals constructed in-test:
```go
original := config.LLMConfig{
    Type:   "openai",
    APIKey: "sk-secret-key",
    ExtraHeaders: map[string]string{
        "X-Custom-Auth": "bearer-token-123",
    },
    BaseURL: "https://api.openai.com/v1/chat/completions",
    Model:   "gpt-4o",
}
```

**Location:**
- No `testdata/` directories
- No fixture files
- No factory packages
- All test data is inline

## Coverage

**Requirements:** None enforced — no coverage thresholds configured.

**View Coverage:**
```bash
go test -v -race -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

**Current State:** Very sparse — only 6 test files across the entire codebase. Major gaps exist (see Test Coverage Gaps below).

## Test Types

**Unit Tests:**
- All existing tests are unit tests
- Test individual functions in isolation
- No database or network calls

**Integration Tests:**
- None exist
- No tests for HTTP handlers (`internal/server/`)
- No tests for store backends (`internal/store/sqlite3/`, `postgres/`, `memory/`)
- No tests for LLM provider adapters (`internal/service/llm/`)

**E2E Tests:**
- Not used
- No test framework for end-to-end testing

## Common Patterns

**Async Testing:**
- Not used in current tests — all tests are synchronous
- Workflow engine concurrency tested indirectly through node Run() calls

**Error Testing:**
```go
// Expect an error
_, err := node.Run(context.Background(), reg, map[string]any{})
if err == nil {
    t.Fatal("expected error when no prompt is provided")
}

// Expect a specific sentinel error
_, err := node.Run(context.Background(), reg, map[string]any{"data": []any{}})
if !errors.Is(err, workflow.ErrStopBranch) {
    t.Fatalf("expected ErrStopBranch, got %v", err)
}

// Expect validation error
if err := node.Validate(context.Background(), reg); err == nil {
    t.Fatal("expected validation error for empty expression")
}
```

**Result Type Assertion Pattern:**
```go
result, err := node.Run(context.Background(), reg, inputs)
if err != nil {
    t.Fatalf("Run: %v", err)
}

sel, ok := result.(workflow.NodeResultSelection)
if !ok {
    t.Fatal("expected NodeResultSelection")
}

if sel.Selection()[0] != "true" {
    t.Fatalf("expected true, got %v", sel.Selection())
}
```

**Package Testing Styles:**
- White-box (same package): `package crypto`, `package service`, `package workflow`, `package skillmd`, `package server`
  - Files: `crypto_test.go`, `schema_test.go`, `engine_test.go`, `parse_test.go`, `gateway-rag-mcp_test.go`
- Black-box (separate `_test` package): `package nodes_test`
  - Files: `nodes_test.go` — uses blank import for init() registration

**Blank Import for Node Registration (required in black-box tests):**
```go
package nodes_test

import (
    _ "github.com/rakunlabs/at/internal/service/workflow/nodes"
)
```

## Test Coverage Gaps

**HTTP Handlers (`internal/server/`):**
- No tests for any API endpoint handlers (provider CRUD, workflow CRUD, gateway, auth)
- No tests for middleware chain
- No tests for streaming responses

**Store Backends (`internal/store/`):**
- No tests for SQLite, PostgreSQL, or memory store implementations
- No tests for CRUD operations, pagination, or migration logic

**LLM Provider Adapters (`internal/service/llm/`):**
- No tests for OpenAI, Anthropic, Gemini, or Vertex adapters
- No tests for request/response translation

**Workflow Engine (`internal/service/workflow/engine.go`):**
- `reachableNodes` is tested, but `parseGraph`, `topoSort`, and full `Engine.Run` are not
- No tests for concurrent fan-out execution
- No tests for early output channel behavior

**Config Loading (`internal/config/`):**
- No tests for config parsing or validation

**Cluster Coordination (`internal/cluster/`):**
- No tests for distributed key rotation or coordination

---

*Testing analysis: 2026-03-08*
