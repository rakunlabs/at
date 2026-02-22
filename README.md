<img align="right" height="94" src="assets/at.png">

# AT

LLM gateway with an OpenAI-compatible API. Route requests to multiple providers through a single endpoint.

> Highly on development stage, expect breaking changes. Feedback and contributions are very welcome!

## Usage

```sh
## create test environment (postgres)
make env
## run at server
make run
```

## Configuration

Providers can be configured via YAML config file or the web UI (stored in PostgreSQL). Database entries override YAML.

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

### Supported provider types

| Type        | Description                                                                                                     |
| ----------- | --------------------------------------------------------------------------------------------------------------- |
| `openai`    | OpenAI and all OpenAI-compatible APIs (Groq, DeepSeek, Mistral, Together AI, Ollama, vLLM, GitHub Models, etc.) |
| `anthropic` | Anthropic Claude API                                                                                            |
| `vertex`    | Google Vertex AI via OpenAI-compatible endpoint with automatic ADC authentication                               |
| `gemini`    | Google AI (Gemini) via generativelanguage.googleapis.com with API key                                           |

### Proxy support

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
