<img align="right" height="94" src="assets/at.png">

# AT

LLM gateway with an OpenAI-compatible API. Route requests to multiple providers through a single endpoint.

> Highly on development stage, expect breaking changes. Feedback and contributions are very welcome!

```sh
docker run -d --name at -p 8080:8080 ghcr.io/rakunlabs/at:latest
```

## Configuration

> `at.yaml` config file is automatically loaded if present in the current working directory or use `AT_CONFIG_FILE`.
> Uses [chu](https://github.com/rakunlabs/chu) loader which supports get config from multiple sources.

### Providers

Providers can be configured via YAML config file or the web UI (stored in Postgres). Database entries override YAML.

```yaml
providers:
  openai:
    type: openai
    api_key: "sk-..."
    model: "gpt-4o"
  anthropic:
    type: anthropic
    api_key: "sk-ant-..."
    model: "claude-haiku-4-5"
  groq:
    type: openai
    api_key: "gsk_..."
    base_url: "https://api.groq.com/openai/v1/chat/completions"
    model: "llama-3.3-70b-versatile"
  ollama:
    type: openai
    base_url: "http://localhost:11434/v1/chat/completions"
    model: "llama3.2"
  vertex:
    type: vertex
    base_url: "https://us-central1-aiplatform.googleapis.com/v1/projects/my-project/locations/us-central1/endpoints/openapi/chat/completions"
    model: "google/gemini-2.5-flash"
  gemini:
    type: gemini
    api_key: "AIzaSy..."
    model: "gemini-2.5-flash"
```

#### Supported provider types

| Type        | Description                                                                                                     |
| ----------- | --------------------------------------------------------------------------------------------------------------- |
| `openai`    | OpenAI and all OpenAI-compatible APIs (Groq, DeepSeek, Mistral, Together AI, Ollama, vLLM, GitHub Models, etc.) |
| `anthropic` | Anthropic Claude API                                                                                            |
| `vertex`    | Google Vertex AI via OpenAI-compatible endpoint with automatic ADC authentication                               |
| `gemini`    | Google AI (Gemini) via generativelanguage.googleapis.com with API key                                           |

#### Native provider gateway

The OpenAI-compatible gateway remains available at `/gateway/v1/chat/completions`. Provider-native APIs are also proxied at `/gateway/v1/providers/{provider}/*`, so clients can use provider-specific endpoints such as embeddings without OpenAI-format conversion. AT still applies gateway token auth, provider/model restrictions, and provider credential injection.

For example, a Claude Code compatible Anthropic provider can be exposed with:

```sh
ANTHROPIC_BASE_URL=https://at.example.com/gateway/v1/providers/anthropic
ANTHROPIC_API_KEY=<AT gateway token>
```

#### Audio transcription (Whisper)

An OpenAI provider also exposes the OpenAI-compatible transcription endpoint.
The gateway model keeps the `provider/model` form; AT strips the provider prefix
before forwarding the request upstream:

```sh
curl https://at.example.com/gateway/v1/audio/transcriptions \
  -H "Authorization: Bearer <AT gateway token>" \
  -F "file=@recording.wav" \
  -F "model=openai/whisper-1" \
  -F "language=tr" \
  -F "response_format=json"
```

The `openai` provider must have an OpenAI API key with available billing quota,
and `whisper-1` must be included in its configured models when a model allowlist
is used. `auth_type: chatgpt` uses the Codex OAuth backend from a ChatGPT
subscription and cannot call `/audio/transcriptions`; keep it as a separate
provider from the API-key-backed provider, for example `chatgpt/...` for Codex
and `openai-api/whisper-1` for transcription. Upstream quota errors are returned
as HTTP 429 rather than a generic gateway 502.

#### Proxy support

All provider types support routing requests through an HTTP, HTTPS, or SOCKS5 proxy:

```yaml
providers:
  openai:
    # ....
    proxy: "http://proxy.example.com:8080"
  anthropic:
    # ....
    proxy: "socks5://127.0.0.1:1080"
```

### Server configuration

The server can be configured with a custom host, port, base path, and forward authentication:

```yaml
server:
  host: "0.0.0.0"
  port: "8080"
  base_path: "/at" # default is empty string, set to a non-empty value to serve API under a subpath (e.g. /at)
  admin_token: "my-secret-admin-token" # protects /api/v1/settings/* endpoints; if not set, settings endpoints are disabled
  # example forward_auth config, default not set (disabled)
  # based on https://rakunlabs.github.io/ada/guide/middleware/forwardauth.html
  forward_auth:
    address: "https://auth.example.com/verify"
    auth_request_headers: # default empty (forward all headers).
      - "Authorization"
      - "Cookie"
    auth_response_headers: # default empty (don't copy any headers from auth response to original request).
      - "X-User"
      - "X-Email"
    auth_response_headers_regex: "^X-Custom-" # default empty
    trust_forward_header: false
    insecure_skip_verify: false
    timeout: "30s" # default 30s
    request_method: "GET" # default GET, can be set to POST or other HTTP methods supported by the auth service
    redirect_url: "https://login.example.com?rd={url}" # default empty (no redirect). Supports `{url} or {uri}` placeholder which will be replaced with the original request URL. Only applied for GET/HEAD requests.
    redirect_code: 302 # default 302
    redirect_status_codes: # default [401]
      - 401
```

When `forward_auth` is set, all management API requests are forwarded to the specified authentication service for verification before being handled. If the auth service returns a 2xx response the request proceeds; otherwise it is rejected or redirected.

When `admin_token` is set, all `/api/v1/settings/*` endpoints require an `Authorization: Bearer <admin_token>` header. If no `admin_token` is configured, settings endpoints respond with `403 Forbidden` -- this forces explicit opt-in. The admin token only protects settings endpoints; regular management APIs (providers, tokens) are unaffected.

### Store configuration

Providers and API tokens are managed through the web UI and persisted in PostgreSQL — the only supported backend. Startup fails with a descriptive error when `store.postgres.datasource` is not configured.

#### PostgreSQL

```yaml
store:
  postgres:
    datasource: "postgres://user:pass@localhost:5432/at?sslmode=disable"
    # schema: "public"              # optional
    # table_prefix: "at_"           # optional, defaults to "at_"
    # conn_max_lifetime: "15m"      # optional
    # max_idle_conns: 3             # optional
    # max_open_conns: 3             # optional
```

A Docker Compose file is provided for local development:

```sh
make env
```

#### Credential encryption

Provider credentials (`api_key` and `extra_headers` values) stored in the database can be encrypted at rest using AES-256-GCM. Add an `encryption_key` to the store configuration:

```yaml
store:
  encryption_key: "your-secret-key-here"
  postgres:
    datasource: "postgres://user:pass@localhost:5432/at?sslmode=disable"
```

The key can be any non-empty string (it is hashed with SHA-256 internally to derive a 32-byte AES key). When set, sensitive fields are encrypted before being written to the database and decrypted when loaded into memory. In-memory provider data always stays in plaintext so there is no runtime overhead on gateway requests.

Providers loaded from the YAML config file are not affected -- they are never written to the database unless created via the UI/API.

If no `encryption_key` is set, credentials are stored in plaintext (backward compatible).

##### Key rotation

If you need to change the encryption key, use the key rotation API endpoint. This re-encrypts all provider credentials atomically within a database transaction:

```sh
curl -X POST http://localhost:8080/api/v1/settings/rotate-key \
  -H "Authorization: Bearer my-secret-admin-token" \
  -H "Content-Type: application/json" \
  -d '{"encryption_key": "new-secret-key"}'
```

After rotating, update the `encryption_key` in your `at.yaml` to match the new value. To disable encryption entirely, send an empty key (`"encryption_key": ""`), which decrypts all credentials back to plaintext.

> **Important:** Do not change the `encryption_key` in the config file without calling the rotation endpoint first -- the application will fail to decrypt existing credentials on startup.

### Clustering

Multiple AT instances can coordinate encryption key rotation using distributed peer discovery via the [alan](https://github.com/rakunlabs/alan) library. When clustering is enabled, key rotation acquires a distributed lock and broadcasts the new encryption key to all peers after the DB transaction commits.

```yaml
server:
  admin_token: "my-secret-admin-token"
  alan:
    dns_addr: "at-headless.default.svc.cluster.local" # DNS name for peer discovery
    bind_addr: "0.0.0.0"                               # local bind address (default: 0.0.0.0)
    port: 5000                                          # UDP port (must be same for all peers)
    replicas: 3                                          # expected cluster size (for quorum)
    security:
      enabled: true
      key: "my-cluster-secret"                           # any length, derived via Argon2id internally
```

When clustering is **not** configured (no `alan` section), AT operates in single-instance mode and all features work as before -- there is no alan dependency overhead.

**How key rotation works with clustering:**

1. The admin calls `POST /api/v1/settings/rotate-key` on any instance.
2. That instance acquires a distributed lock (`encryption-key-rotation`).
3. It re-encrypts all provider credentials in the DB within a transaction.
4. It broadcasts the new derived AES key (base64-encoded, encrypted by alan's ChaCha20) to all peers.
5. Peers update their in-memory encryption key. No provider reload is needed since in-memory provider configs already hold plaintext.
6. The lock is released.

## Development

```sh
## build-ui or in the UI pnpm run dev to start UI in development mode
make install-ui run-ui
## run at server
make run
```

Open http://localhost:3000 to access the web UI for UI development mode.
