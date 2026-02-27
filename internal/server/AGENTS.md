# internal/server — HTTP Layer

## Purpose

HTTP server built on ada framework. Handles routing, middleware, auth, and serves the embedded Svelte UI.

## Key Files

- `server.go` — Server struct, middleware chain, route registration, provider registry, scheduler init, UI static serving
- `gateway.go` (729 lines) — OpenAI-compatible `/gateway/v1/chat/completions` and `/v1/models`; streaming handler, token auth, model parsing
- `translate.go` (596 lines) — request/response translation between OpenAI format and internal types
- `workflows.go` — workflow CRUD + `RunWorkflowAPI` (sync/async), trigger sync on save
- `triggers.go` — trigger CRUD + `WebhookAPI` (`POST /webhooks/{id}`), auth checks
- `provider.go` — provider CRUD, hot-reload via `reloadProvider`, Info API, credential redaction
- `api-tokens.go` — token CRUD, SHA-256 hashing, prefix convention
- `node-configs.go` — node config CRUD, sensitive field redaction
- `chat.go` — admin chat endpoint (`AdminChatCompletions`)
- `auth-device.go` (393 lines) — device auth flow (OAuth device code grant)
- `discover.go` (357 lines) — provider/model discovery endpoint
- `response.go` — JSON response helpers

## Route Map

```
/gateway/v1/chat/completions  POST  → ChatCompletions (gateway.go)
/gateway/v1/models            GET   → ListModels (gateway.go)
/webhooks/{id}                POST  → WebhookAPI (triggers.go)
/api/v1/providers[/{key}]           → provider CRUD
/api/v1/api-tokens[/{id}]          → token CRUD
/api/v1/workflows[/{id}]           → workflow CRUD
/api/v1/workflows/run/{id}   POST  → RunWorkflowAPI
/api/v1/workflows/{id}/triggers    → trigger CRUD per workflow
/api/v1/triggers/{id}              → trigger by ID
/api/v1/skills, /variables, /node-configs, /runs, /settings
/api/v1/chat/completions     POST  → AdminChatCompletions
```

## Auth Model

- **Gateway**: `authenticateRequest` checks config tokens first, then DB tokens via `tokenStore.GetAPITokenByHash(sha256(token))`
- **Token scoping**: `AllowedProviders`, `AllowedModels`, `AllowedWebhooks` on APIToken
- **Admin routes**: `adminAuthMiddleware` enforces `Authorization: Bearer <admin_token>` on `/api/v1/settings/*`
- **Forward auth**: optional `mforwardauth` middleware on base group when configured

## Patterns

- Provider registry: `Server.providers map[string]ProviderInfo` — in-memory, hot-reloaded
- `ProviderInfo`: wraps `service.LLMProvider` + type/defaultModel/models metadata
- Streaming: type-assert `LLMStreamProvider` → true SSE; fallback → fake-stream from `Chat()` result
- Model format: `"provider_key/actual_model"` parsed by `parseModelID`
- Trigger sync: saving workflow auto-syncs trigger DB records + reloads scheduler
- Static UI: `//go:embed dist/*` served via ada folder handler at base path
