# AT — LLM Gateway + Workflow Engine

## What This Is

OpenAI-compatible LLM gateway that routes requests to multiple providers (OpenAI, Anthropic, Vertex AI, Gemini) through a single `/gateway/v1/chat/completions` endpoint. Includes a DAG-based workflow engine and Svelte admin UI.

Module: `github.com/rakunlabs/at` — Go 1.26

## Architecture

```
cmd/at/main.go          → bootstrap: config → store → providers → server.Start
internal/server/        → HTTP handlers, routing (ada framework), middleware, static UI
internal/service/       → domain types, store interfaces (at.go), LLM provider interfaces
internal/service/workflow/ → DAG engine: parse → topoSort → run (concurrent fan-out)
internal/service/workflow/nodes/ → 18 node types registered via init()
internal/service/llm/   → provider adapters: openai/, antropic/, gemini/, vertex/
internal/store/         → store factory → sqlite3 | postgres | memory
internal/crypto/        → AES-256-GCM credential encryption, key rotation
internal/cluster/       → alan-based distributed coordination
_ui/                    → Svelte 5 + Vite + TailwindCSS 4 SPA
```

## Key Contracts (internal/service/at.go)

- `LLMProvider` — `Chat(ctx, model, []Message, []Tool) (*LLMResponse, error)`
- `LLMStreamProvider` — `ChatStream(ctx, model, []Message, []Tool) (<-chan StreamChunk, error)`
- Store interfaces: `ProviderStorer`, `APITokenStorer`, `WorkflowStorer`, `TriggerStorer`, `SkillStorer`, `VariableStorer`, `NodeConfigStorer`
- `KeyRotator`, `EncryptionKeyUpdater` — optional store capabilities

## Provider Model

- Providers keyed by string (e.g. `"openai"`) → model format: `"provider_key/actual_model"`
- `ProviderFactory func(cfg config.LLMConfig) (service.LLMProvider, error)` — injected into server
- Hot-reload: DB create/update → `server.reloadProvider` → factory creates new instance in-memory
- Streaming: server checks `LLMStreamProvider` via type assertion; falls back to fake-stream

## Workflow Engine

- Workflows are DAGs: `WorkflowGraph` has `Nodes` + `Edges`
- Two-phase: `parseGraph` (validate nodes) → `topoSort` → `Run` (concurrent goroutine-per-branch)
- Three result types control routing: `NodeResult` (pass-through), `NodeResultSelection` (port routing), `NodeResultFanOut` (parallel branches)
- `Registry` holds shared lookups (provider, skill, var, nodeConfig) + collects outputs
- Scheduler: cron triggers via hardloop; reloads on trigger changes

## Conventions

- **Error wrapping**: `fmt.Errorf("context: %w", err)` — always wrap with context
- **Not-found**: `errors.Is(err, sql.ErrNoRows)` → return `nil, nil`
- **Logging**: `slog.*` structured logging with `logi.Ctx(ctx)` for contextual fields; key "error" for error values
- **Imports**: stdlib → third-party → internal, separated by blank lines
- **File names**: hyphen-separated (`api-tokens.go`, `http-request.go`)
- **Panics**: only in Goja JS VM layer (`vm.NewTypeError`); nowhere else
- **Tests**: standard `testing` package, table-driven where applicable; only 2 test files exist currently
- **No code generation**: no `go:generate` directives

## Build & Dev

### Common Commands
```sh
make env            # docker compose: postgres for local dev
make install-ui     # pnpm install in _ui/
make run-ui         # vite dev server (localhost:3000)
make run            # go run cmd/at/main.go
make test           # go test -v -race ./...
make lint           # golangci-lint run ./...
make build          # goreleaser build --snapshot
make build-container # docker build at:test
```

### Running Tests
- **All tests**: `make test`
- **Single test**: `go test -v -race -run TestName ./path/to/package`
  - Example: `go test -v -race -run TestGenerateHash ./internal/crypto`
- **Package tests**: `go test -v -race ./internal/service/...`

## Code Style & Guidelines

### Formatting & Imports
- **Formatting**: Run `gofmt` (or let your editor do it) on save.
- **Imports**: Group imports in this order, separated by blank lines:
  1. Standard library (`"fmt"`, `"context"`)
  2. Third-party libraries (`"github.com/worldline-go/types"`)
  3. Internal packages (`"github.com/rakunlabs/at/internal/..."`)

### Naming Conventions
- **Files**: Lowercase with hyphens (e.g., `api-tokens.go`, `http-request.go`).
- **Interfaces**: Suffix with `-er` where appropriate (e.g., `ProviderStorer`, `KeyRotator`).
- **Structs/Functions**: PascalCase for exported, camelCase for unexported.
- **Variables**: Short, descriptive names (e.g., `ctx`, `err`, `cfg`).
- **JSON Tags**: Snake_case (e.g., `json:"api_key"`).

### Types & Data Structures
- **Nullables**: Use `types.Null[T]` from `github.com/worldline-go/types` for optional database fields.
- **Slices**: Use `types.Slice[T]` for JSON arrays that need specific marshaling behavior if required, otherwise standard `[]T`.
- **Context**: Always pass `ctx context.Context` as the first argument to functions performing I/O or long-running operations.

### Error Handling
- **Wrapping**: Always wrap errors with context using `%w`.
  - `fmt.Errorf("failed to parse config: %w", err)`
- **Sentinel Errors**: Check for specific errors using `errors.Is`.
  - Specifically for database lookups: `if errors.Is(err, sql.ErrNoRows) { return nil, nil }`

## Store & Encryption

- Factory: `store.New(ctx, cfg)` → postgres > sqlite3 > memory (fallback)
- Credentials encrypted at rest: AES-256-GCM, key derived via SHA-256 from config passphrase
- `enc:` prefix on stored strings signals encrypted value
- Key rotation: `POST /api/v1/settings/rotate-key` (admin-token protected, cluster-aware)

## Middleware Chain (server.go)

recover → server → CORS → requestid → log → telemetry → [forward-auth on base group] → [admin-token on settings]
