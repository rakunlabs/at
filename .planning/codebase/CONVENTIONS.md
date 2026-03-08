# Coding Conventions

**Analysis Date:** 2026-03-08

## Naming Patterns

**Files:**
- Lowercase with hyphens: `api-tokens.go`, `http-request.go`, `gateway-rag-mcp.go`, `agent-call.go`
- Test files follow standard Go: `crypto_test.go`, `nodes_test.go`, `schema_test.go`
- One node type per file in `internal/service/workflow/nodes/`

**Functions:**
- PascalCase for exported: `ListProviders`, `ChatCompletions`, `RunWorkflowAPI`
- camelCase for unexported: `reachableNodes`, `parseGraph`, `topoSort`, `gatherInputs`
- HTTP handlers: method on `*Server` struct, named `XxxAPI`: `func (s *Server) InfoAPI(w http.ResponseWriter, r *http.Request)`

**Variables:**
- Short, idiomatic names: `ctx`, `err`, `cfg`, `w`, `r`, `q`, `reg`
- Loop variables: `i`, `k`, `v`, `tt` (table tests)
- Mutex names match protected field: `providerMu` protects `providers`

**Types:**
- Interfaces suffix with `-er`: `ProviderStorer`, `APITokenStorer`, `WorkflowStorer`, `KeyRotator`, `EncryptionKeyUpdater`
- Node interface: `Noder` (from chore pattern)
- DB row structs: lowercase unexported, suffix `Row`: `workflowRow`, `triggerRow`
- Domain types: PascalCase exported in `internal/service/at.go`: `ProviderRecord`, `APIToken`, `Workflow`
- Generic results: `ListResult[T]` with `ListMeta` for pagination

**Constants:**
- PascalCase for exported: `AccessModeAll`, `AccessModeNone`, `AccessModeList`
- Defined adjacent to the type they relate to in `internal/service/at.go`

**JSON Tags:**
- Always snake_case: `json:"api_key"`, `json:"created_at"`, `json:"token_prefix"`
- Use `omitempty` where appropriate: `json:"total,omitempty"`

**DB Tags:**
- snake_case `db:"column_name"` for row scanning structs: `db:"id"`, `db:"created_at"`, `db:"active_version"`

## Code Style

**Formatting:**
- Standard `gofmt` formatting
- No custom formatting rules detected

**Linting:**
- `golangci-lint` via `make lint`
- No `.golangci.yml` config file — uses default ruleset

**Section Comments:**
- Unicode box-drawing dividers for file sections:
  ```go
  // ─── Section Name ───
  ```
- Double-line separators for test group boundaries:
  ```go
  // ═══════════════════════════════════════════════════════════════════
  // conditional node tests
  // ═══════════════════════════════════════════════════════════════════
  ```

**Package Documentation:**
- Doc comment on `package` line for key packages:
  ```go
  // Package workflow implements a graph-based workflow execution engine.
  ```
- Inline doc comments above types and exported functions using `//` style

## Import Organization

**Order:**
1. Standard library (`"context"`, `"fmt"`, `"net/http"`, `"encoding/json"`)
2. Third-party (`"github.com/doug-martin/goqu/v9"`, `"github.com/oklog/ulid/v2"`, `"github.com/worldline-go/types"`)
3. Internal (`"github.com/rakunlabs/at/internal/service"`, `"github.com/rakunlabs/at/internal/config"`)

**Blank Lines:** Separate each group with a blank line.

**Blank Imports:** Used for side-effect registration:
```go
// Blank import to trigger init() registrations for all node types.
_ "github.com/rakunlabs/at/internal/service/workflow/nodes"
```

**Path Aliases:**
- None used. All imports use full paths.

## Error Handling

**Wrapping:** Always wrap with context using `%w`:
```go
return nil, fmt.Errorf("build list workflows query: %w", err)
return nil, fmt.Errorf("scan workflow row: %w", err)
return nil, fmt.Errorf("store operation: %w", err)
```

**Not-Found Pattern:** Store layer returns `nil, nil` for missing records:
```go
if errors.Is(err, sql.ErrNoRows) {
    return nil, nil
}
```

**Sentinel Errors:** Defined as package-level vars:
```go
var ErrStopBranch = errors.New("stop branch")
```
Check with `errors.Is(err, workflow.ErrStopBranch)`.

**Node Error Prefixes:** Error messages prefixed with node type:
```
"conditional: expression error: %w"
"http_request: failed to execute: %w"
"llm_call: no prompt provided"
```

**HTTP Error Responses:** Use helper functions from `internal/server/response.go`:
```go
httpResponse(w, "provider not found", http.StatusNotFound)
httpResponseJSON(w, data, http.StatusOK)
```

**Nil Guards:** HTTP handlers check for nil store before proceeding:
```go
if s.providerStore == nil {
    httpResponse(w, "store not configured", http.StatusServiceUnavailable)
    return
}
```

## Logging

**Framework:** Standard library `log/slog` + `rakunlabs/logi` for context extraction.

**Patterns:**
```go
slog.ErrorContext(ctx, "failed to reload provider", "error", err, "key", key)
slog.InfoContext(ctx, "provider reloaded", "key", key)
```

- Use `slog.ErrorContext`/`slog.InfoContext`/`slog.WarnContext` with `ctx` as first arg
- Structured key-value pairs after the message
- Use `"error"` as the key for error values
- Use `logi.Ctx(ctx)` when extracting logger from context

## Comments

**When to Comment:**
- Above all exported types and functions (GoDoc style)
- Section dividers using box-drawing characters
- Inline comments for non-obvious logic

**Style:**
```go
// InfoAPI handles GET /api/v1/info.
// Returns gateway status: registered providers, model counts, store type.
func (s *Server) InfoAPI(w http.ResponseWriter, r *http.Request) {
```

**No JSDoc/TSDoc:** This is a Go project — use standard `//` comments.

## Function Design

**Context:** Always pass `ctx context.Context` as the first parameter for I/O functions:
```go
func (s *SQLite) GetWorkflow(ctx context.Context, id string) (*service.Workflow, error)
```

**Return Values:**
- Store CRUD: `(*T, error)` for single items, `(*ListResult[T], error)` for lists
- Not-found returns `nil, nil` (not an error)
- Node execution: `(NodeResult, error)`

**HTTP Handlers:** Method on `*Server`, two params `(w http.ResponseWriter, r *http.Request)`, no return:
```go
func (s *Server) ListProvidersAPI(w http.ResponseWriter, r *http.Request) {
```

## Module Design

**Exports:**
- All domain types and interfaces centralized in `internal/service/at.go`
- Store implementations are internal to their packages (`sqlite3`, `postgres`, `memory`)
- Node types register themselves via `init()` — consumed via blank import

**Barrel Files:**
- `internal/service/workflow/nodes/register.go` — blank import trigger point for all node registrations

**Dependency Injection:**
- Functional: typed function types for lookups:
  ```go
  type ProviderLookup func(key string) (service.LLMProvider, string, error)
  type SkillLookup func(nameOrID string) (*service.Skill, error)
  type VarLookup func(key string) (string, error)
  ```
- `Registry` constructed with all lookup functions at workflow start

**ID Generation:**
- ULID via `oklog/ulid/v2` for all entity IDs
- Deterministic, sortable, URL-safe

**Nullable Fields:**
- Use `types.Null[T]` from `github.com/worldline-go/types` for optional DB fields:
  ```go
  ExpiresAt types.Null[types.Time] `json:"expires_at"`
  TotalTokenLimit types.Null[int64] `json:"total_token_limit"`
  ```
- Use `types.Slice[T]` for JSON array columns that need specific marshaling

**Query Building:**
- Use `goqu` query builder for SQL generation (both sqlite3 and postgres backends)
- Never raw SQL strings — always `goqu.From(table).Select(...).Where(...).ToSQL()`

**Pagination:**
- Use `github.com/rakunlabs/query` for parsing query parameters
- Return `ListResult[T]` with `ListMeta{Total, Offset, Limit}`

---

*Convention analysis: 2026-03-08*
