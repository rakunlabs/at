# Technology Stack

**Analysis Date:** 2026-03-08

## Languages

**Primary:**
- Go 1.26 - Backend gateway, workflow engine, store layer, all server-side code (`cmd/at/`, `internal/`)
- Svelte 5 (JavaScript/TypeScript) - Admin UI SPA (`_ui/`)

**Secondary:**
- JavaScript (Goja) - Embedded JS runtime for workflow script nodes (`internal/service/workflow/goja.go`)
- SQL - Database migrations and queries (`internal/store/postgres/`, `internal/store/sqlite3/`)

## Runtime

**Environment:**
- Go 1.26 (specified in `go.mod` line 3)
- Node.js (for UI dev tooling, version managed by pnpm)
- CGO_ENABLED=0 for production builds (pure Go, no C dependencies)

**Package Manager:**
- Go modules (`go.mod`, `go.sum`)
- pnpm 10.x for UI (`_ui/package.json`, `_ui/pnpm-lock.yaml`)
- Lockfiles: both present and committed

## Frameworks

**Core:**
- `rakunlabs/ada` v0.9.5 - HTTP routing framework with middleware chain, group-based routing (`internal/server/server.go`)
- `rakunlabs/chu` v0.4.7 - Configuration loading with env vars (prefix `AT_`), Consul, and Vault support (`cmd/at/main.go`)
- `rakunlabs/logi` v0.5.3 - Structured logging wrapper over `slog` (`internal/server/server.go`)
- `rakunlabs/tell` v0.2.3 - OpenTelemetry integration for traces and metrics (`internal/server/server.go`)

**Testing:**
- Standard `testing` package - Go test framework
- No additional assertion libraries detected

**Build/Dev:**
- GoReleaser v2 - Cross-platform binary builds + Docker images (`.goreleaser.yaml`)
- Vite 6.1.0 - UI dev server and bundler (`_ui/package.json`)
- TailwindCSS 4.0.9 - UI styling (`_ui/package.json`)
- Make - Build orchestration (`Makefile`)

## Key Dependencies

**Critical (Go):**
- `jackc/pgx/v5` v5.7.5 - PostgreSQL driver (connection pooling, query execution)
- `doug-martin/goqu/v9` v9.19.0 - SQL query builder used across all store backends
- `modernc.org/sqlite` v1.37.1 - Pure-Go SQLite driver (no CGO required)
- `tmc/langchaingo` v0.1.13-pre.0 - RAG embeddings and vector store abstraction (`internal/service/rag/`)
- `dop251/goja` v0.0.0-20250309171923 - JavaScript runtime for workflow script nodes
- `go-git/go-git/v5` v5.16.0 - Git operations in workflow nodes
- `bwmarrin/discordgo` v0.28.1 - Discord bot integration
- `go-telegram-bot-api/telegram-bot-api/v5` v5.5.1 - Telegram bot integration
- `robfig/cron/v3` v3.0.1 - Cron scheduling for workflow triggers

**Infrastructure (Go):**
- `worldline-go/klient` v0.9.4 - HTTP client with proxy support (HTTP/HTTPS/SOCKS5)
- `worldline-go/types` v0.2.0 - Nullable types (`types.Null[T]`, `types.Slice[T]`) for DB fields
- `rakunlabs/alan` v0.1.0 - UDP peer discovery for cluster coordination
- `golang.org/x/oauth2` v0.28.0 - OAuth2 flows (GitHub Copilot, Claude Code)
- `go.opentelemetry.io/otel` v1.35.0 - OpenTelemetry SDK for traces/metrics

**Critical (UI):**
- `svelte` 5.22.4 - Component framework (`_ui/package.json`)
- `axios` 1.8.1 - HTTP client for API calls
- `svelte-spa-router` 4.0.1 - Client-side routing
- `@tailwindcss/vite` 4.0.9 - TailwindCSS Vite plugin
- `codemirror` 6.0.1 + extensions - Code editor in admin UI
- `marked` 15.0.7 - Markdown rendering
- `dompurify` 3.2.5 - HTML sanitization

## Configuration

**Environment:**
- Config loaded via `rakunlabs/chu` with environment variable prefix `AT_` (`cmd/at/main.go`)
- Supports Consul and Vault config loaders as plugins
- `.env` files present — contain environment configuration (not read for security)
- Key config struct: `internal/config/config.go` defines `Config`, `Server`, `Gateway`, `Store`, `LLMConfig`, `Bots`

**Key Configuration Areas:**
- `Config.Server` - HTTP listen address, TLS, CORS settings
- `Config.Store` - Database DSN, encryption passphrase, store type selection
- `Config.Gateway` - Provider configurations, default model, token settings
- `Config.Bots` - Discord/Telegram bot tokens and channel configs
- `Config.Cluster` - Alan UDP discovery settings

**Build:**
- `.goreleaser.yaml` - GoReleaser v2 config: cross-compile for linux/darwin/windows (amd64/arm64/arm), Docker multi-arch images
- `Makefile` - 15+ targets: `env`, `install-ui`, `run-ui`, `run`, `test`, `lint`, `build`, `build-ui`, `build-container`
- `ci/Dockerfile` - Alpine 3.23.3 base with git, curl, ripgrep, bash, openssh-client
- `_ui/vite.config.ts` - Vite config with Svelte plugin and TailwindCSS

## Platform Requirements

**Development:**
- Go 1.26+
- Node.js + pnpm (for UI development)
- Docker + Docker Compose (for local PostgreSQL via `make env` / `env/compose.yaml`)
- golangci-lint (for `make lint`)
- GoReleaser v2 (for `make build`)

**Production:**
- Single static binary (CGO_ENABLED=0), no runtime dependencies
- UI embedded via `//go:embed dist/*` in `internal/server/server.go`
- Docker image: `ghcr.io/rakunlabs/at` (Alpine-based, multi-arch)
- PostgreSQL recommended (SQLite or in-memory as fallbacks)
- Optional: Consul/Vault for config, external vector store for RAG

## Build Commands

```bash
make env              # Start local PostgreSQL via docker compose
make install-ui       # pnpm install in _ui/
make run-ui           # Vite dev server (localhost:3000)
make build-ui         # Production UI build → _ui/dist/
make run              # go run cmd/at/main.go
make test             # go test -v -race ./...
make lint             # golangci-lint run ./...
make build            # GoReleaser snapshot build
make build-container  # Docker build at:test
```

## UI Embedding

The Svelte SPA is built to `_ui/dist/` and embedded into the Go binary at compile time:
- `internal/server/server.go` uses `//go:embed dist/*` directive
- Production binary serves UI from embedded filesystem at root path
- Dev mode: run `make run-ui` separately for hot-reload on port 3000

---

*Stack analysis: 2026-03-08*
