# External Integrations

**Analysis Date:** 2026-03-08

## APIs & External Services

### LLM Providers

**OpenAI-Compatible:**
- OpenAI, Groq, DeepSeek, Mistral, Together AI, Ollama, vLLM, GitHub Models — all via unified OpenAI adapter
  - SDK/Client: Custom HTTP client via `worldline-go/klient` (`internal/service/llm/openai/openai.go`)
  - Auth: API key from provider config in DB (encrypted at rest), or GitHub Copilot device flow OAuth
  - Base URL: Configurable per provider (`base_url` field in `service.Provider`)
  - Proxy: HTTP/HTTPS/SOCKS5 proxy support per provider (`proxy_url` field)
  - Models: `provider_key/actual_model` format, e.g. `openai/gpt-4o`

**Anthropic Claude:**
- Anthropic API (Messages API)
  - SDK/Client: Custom HTTP client (`internal/service/llm/antropic/antropic.go`)
  - Auth: API key or Claude Code OAuth PKCE flow with auto-refresh (`internal/service/llm/antropic/auth.go`)
  - Endpoints: `/v1/messages` (chat), `/v1/models` (model listing)

**Google Vertex AI:**
- Google Cloud Vertex AI (Gemini models via Vertex)
  - SDK/Client: Custom HTTP client (`internal/service/llm/vertex/vertex.go`)
  - Auth: Google Application Default Credentials (ADC) — service account or user credentials
  - Endpoint: `https://{location}-aiplatform.googleapis.com/v1/projects/{project}/locations/{location}/publishers/google/models/{model}:generateContent`
  - Config: Requires `project_id` and `location` in provider config

**Google Gemini:**
- Google AI Studio (Gemini API via API key)
  - SDK/Client: Custom HTTP client (`internal/service/llm/gemini/gemini.go`)
  - Auth: API key from provider config
  - Endpoint: `https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent`

### RAG / Vector Stores

**Embedding APIs:**
- OpenAI-compatible embeddings endpoint (`internal/service/rag/embedder.go`)
  - Endpoint: `{base_url}/v1/embeddings`
  - Auth: API key header
- Gemini batch embeddings (`internal/service/rag/embedder.go`)
  - Endpoint: `https://generativelanguage.googleapis.com/v1beta/models/{model}:batchEmbedContents`
  - Auth: API key query param

**Vector Store Backends (6 supported):**
- **pgvector** — PostgreSQL extension (`internal/service/rag/vectorstore.go`)
  - Connection: Reuses main PostgreSQL connection string
  - Client: `tmc/langchaingo/vectorstores/pgvector`
- **Chroma** — ChromaDB server (`internal/service/rag/vectorstore.go`)
  - Connection: `chroma_url` in RAG config
  - Client: `tmc/langchaingo/vectorstores/chroma`
- **Qdrant** — Qdrant vector DB (`internal/service/rag/vectorstore.go`)
  - Connection: `qdrant_url` + optional `qdrant_api_key`
  - Client: `tmc/langchaingo/vectorstores/qdrant`
- **Weaviate** — Weaviate vector DB (`internal/service/rag/vectorstore.go`)
  - Connection: `weaviate_scheme` + `weaviate_host` + optional `weaviate_api_key`
  - Client: `tmc/langchaingo/vectorstores/weaviate`
- **Pinecone** — Pinecone cloud (`internal/service/rag/vectorstore.go`)
  - Connection: `pinecone_api_key` + `pinecone_host` + optional `pinecone_namespace`
  - Client: `tmc/langchaingo/vectorstores/pinecone`
- **Milvus** — Milvus vector DB (`internal/service/rag/vectorstore.go`)
  - Connection: `milvus_url`
  - Client: `tmc/langchaingo/vectorstores/milvus`

### Bot Platforms

**Discord:**
- Discord Bot API via WebSocket gateway
  - SDK/Client: `bwmarrin/discordgo` v0.28.1 (`internal/server/bot-discord.go`)
  - Auth: Bot token from config (`Config.Bots.Discord.Token`)
  - Features: Message handling, thread management, typing indicators, message chunking for long responses
  - Channel filtering: Whitelist/blocklist by channel ID

**Telegram:**
- Telegram Bot API via long polling
  - SDK/Client: `go-telegram-bot-api/telegram-bot-api/v5` (`internal/server/bot-telegram.go`)
  - Auth: Bot token from config (`Config.Bots.Telegram.Token`)
  - Features: Message handling, markdown formatting, message chunking (4096 char limit)
  - Chat filtering: Whitelist/blocklist by chat ID

### MCP (Model Context Protocol)

**MCP Servers:**
- Stdio-based MCP server management (`internal/service/stdio-manager.go`)
  - Spawns subprocess MCP servers via stdin/stdout
  - Supports environment variable injection and working directory config
  - Lifecycle: initialize → list tools → call tool → shutdown
- SSE-based MCP server connections (`internal/service/client.go`)
  - Connects to remote MCP servers via HTTP SSE transport
  - Auth: Bearer token or custom headers

**MCP Proxy:**
- Proxies MCP tool calls to configured server sets (`internal/server/mcp-proxy.go`, `internal/server/mcp-sets.go`)
- MCP server sets: named groups of MCP servers with shared configuration
- Templates: parameterized MCP server configs (`internal/server/mcp-template.go`)

## Data Storage

**Databases:**
- **PostgreSQL** (primary, recommended)
  - Connection: DSN from config (`Config.Store.DSN`)
  - Client: `jackc/pgx/v5` driver + `doug-martin/goqu/v9` query builder
  - Store: `internal/store/postgres/`
  - Features: Full CRUD, migrations via `internal/store/postgres/migrations/`, pgvector extension for RAG
- **SQLite** (lightweight alternative)
  - Connection: File path from config
  - Client: `modernc.org/sqlite` (pure Go, no CGO)
  - Store: `internal/store/sqlite3/`
  - Features: Full CRUD, auto-migrations
- **In-Memory** (fallback)
  - Connection: Automatic when no DSN configured
  - Store: `internal/store/memory/`
  - Features: Map-based, data lost on restart

**File Storage:**
- Local filesystem only (no cloud object storage integration detected)

**Caching:**
- No dedicated caching layer (Redis, Memcached, etc.)
- In-memory provider instances cached in `server.providers` map with mutex (`internal/server/server.go`)
- MCP client connections cached in `StdioManager` (`internal/service/stdio-manager.go`)

## Authentication & Identity

**API Token Auth (Primary):**
- Custom token-based auth for gateway access (`internal/server/gateway.go`)
- Tokens stored in DB with scoped permissions (provider access, model access, RAG access)
- `Authorization: Bearer <token>` header
- Token lookup: `APITokenStorer` interface in `internal/service/at.go`
- Budget/rate limiting per token (`TokenBudget` in `internal/service/at.go`)

**Admin Token Auth:**
- Static admin token from config for settings/admin endpoints (`internal/server/server.go`)
- `Authorization: Bearer <admin-token>` header
- Protects: key rotation, cluster ops, admin-only APIs

**Forward Auth (External):**
- Optional external auth service middleware (`internal/server/server.go`)
- Configured via `Config.Server.ForwardAuth` with URL, headers, and response header mapping
- Applied to base route group before API handlers

**OAuth2 Flows:**
- Generic OAuth2 endpoint support (`internal/server/oauth.go`)
  - Authorization code flow with configurable provider
  - Token exchange and refresh
- GitHub Copilot Device Flow (`internal/server/auth-device.go`, `internal/service/llm/openai/auth.go`)
  - Device authorization → user code → poll for token → exchange for Copilot JWT
  - Auto-refresh via `CopilotTokenSource`
- Claude Code OAuth PKCE (`internal/service/llm/antropic/auth.go`)
  - PKCE authorization code flow with auto-refresh
  - Stores refresh token, auto-renews access token

## Monitoring & Observability

**Telemetry:**
- OpenTelemetry SDK (`go.opentelemetry.io/otel` v1.35.0) (`internal/server/server.go`)
- Traces: OTLP/gRPC exporter via `rakunlabs/tell`
- Metrics: OTLP/gRPC exporter via `rakunlabs/tell`
- Middleware: telemetry middleware in HTTP chain adds spans per request

**Error Tracking:**
- No dedicated error tracking service (Sentry, Bugsnag, etc.)
- Errors logged via `slog` structured logging

**Logs:**
- Structured logging via `slog` + `rakunlabs/logi` (`logi.Ctx(ctx)` for contextual fields)
- Request logging middleware in HTTP chain
- Log key convention: `"error"` key for error values

**Health/Heartbeat:**
- `HeartbeatStorer` interface for agent heartbeat tracking (`internal/service/at.go`)
- No dedicated `/health` or `/ready` endpoint detected in routing

## CI/CD & Deployment

**Hosting:**
- Docker images published to `ghcr.io/rakunlabs/at` (GitHub Container Registry)
- Multi-arch: `linux/amd64`, `linux/arm64`, `linux/arm/v7`
- Base image: Alpine 3.23.3

**CI Pipeline:**
- GitHub Actions (`.github/workflows/`)
  - `test.yml` — Runs on PR/push: `go test -v -race ./...`, `golangci-lint`
  - `tag.yml` — Runs on tag push: GoReleaser build + Docker publish

**Build Artifacts:**
- Binary: `at` (single static binary, CGO_ENABLED=0)
- Docker: `ghcr.io/rakunlabs/at:{tag}`
- Cross-platform: linux/darwin/windows × amd64/arm64/arm

## Environment Configuration

**Required env vars (minimum):**
- Store DSN (PostgreSQL or SQLite path) — or falls back to in-memory
- Encryption passphrase for credential storage

**Optional env vars:**
- `AT_` prefix for all config fields (chu convention)
- LLM provider API keys (stored encrypted in DB, not env vars)
- Bot tokens (Discord, Telegram) via config
- OpenTelemetry exporter endpoint
- Consul/Vault addresses for config loading

**Secrets location:**
- LLM provider credentials: Encrypted in database (AES-256-GCM, `enc:` prefix)
- Bot tokens: Config file or env vars
- Admin token: Config file or env var
- Encryption key: Config passphrase (env var or config file)
- `.env` files present — existence noted, contents not read

## Webhooks & Callbacks

**Incoming:**
- `POST /api/v1/webhooks/{id}` — Token-scoped webhook endpoint (`internal/server/server.go`)
  - Auth: Token in URL or header
  - Triggers workflow execution based on webhook configuration
- `POST /api/v1/triggers/webhook/{id}` — Workflow trigger webhook (`internal/server/server.go`)
- MCP SSE callbacks — Server-sent events for MCP tool responses

**Outgoing:**
- HTTP Request workflow node — Arbitrary HTTP calls from workflows (`internal/service/workflow/nodes/http-request.go`)
- Email workflow node — SMTP email sending (`internal/service/workflow/nodes/email.go`)
- Bot message responses — Discord/Telegram reply messages
- No dedicated webhook dispatch system for event notifications

## Marketplace / External Content

**Skill Marketplace:**
- External HTTP sources for importing skills/templates (`internal/server/marketplace.go`)
- Configured via `Config.Gateway.Marketplace` with URL list
- Fetches skill definitions from remote endpoints

## Cluster / Distribution

**Peer Discovery:**
- `rakunlabs/alan` — UDP-based peer discovery (`internal/cluster/cluster.go`)
- Used for: key rotation broadcast, cluster-aware operations
- Config: `Config.Cluster` with bind address, peers, encryption key

---

*Integration audit: 2026-03-08*
