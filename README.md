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

Providers can be configured via YAML config file or the web UI (stored in SQLite, Postgres). Database entries override YAML.

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
    redirect_url: "https://login.example.com?rd={url}" # default empty (no redirect). Supports `{url}` placeholder which will be replaced with the original request URL. Only applied for GET/HEAD requests.
    redirect_code: 302 # default 302
    redirect_status_codes: # default [401]
      - 401
```

When `forward_auth` is set, all management API requests are forwarded to the specified authentication service for verification before being handled. If the auth service returns a 2xx response the request proceeds; otherwise it is rejected or redirected.

### Store configuration

Providers and API tokens can be managed through the web UI and persisted in a database. If no store is configured, an **in-memory** store is used automatically (data will not survive restarts).

#### SQLite (recommended for single-instance deployments)

```yaml
store:
  sqlite:
    datasource: "at.db"
    # table_prefix: "at_"  # optional, defaults to "at_"
```

WAL mode and foreign keys are enabled automatically.

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

#### In-memory (default)

When neither `sqlite` nor `postgres` is configured, the store falls back to an in-memory backend. All CRUD APIs work normally but data is lost on restart.

## Development

```sh
## build-ui or in the UI pnpm run dev to start UI in development mode
make install-ui run-ui
## run at server
make run
```

Open http://localhost:3000 to access the web UI for UI development mode.
