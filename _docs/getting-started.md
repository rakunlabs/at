# Getting Started

AT is an AI agent platform that provides chat, bot integrations, workflow automation, and an OpenAI-compatible gateway.

## Quick Start

### Using Go

```bash
go install github.com/rakunlabs/at/cmd/at@latest
at
```

### Configuration

AT is configured via `at.yaml` or environment variables prefixed with `AT_`.

```yaml
# at.yaml
store:
  sqlite:
    datasource: "at.db"

server:
  port: "8080"

providers:
  openai:
    type: openai
    api_key: "sk-..."
    model: "gpt-4o"
```

The UI is available at `http://localhost:8080` after starting.

## Core Concepts

- **Providers** — LLM API connections (OpenAI, Anthropic, Gemini, Vertex, or any OpenAI-compatible API)
- **Agents** — AI configurations with a provider, model, system prompt, and optional tools/skills
- **Sessions** — Chat conversations with message history
- **Skills** — Reusable prompt templates that agents can use
- **Variables** — Key-value pairs for dynamic prompt content
- **MCP Servers** — Model Context Protocol servers for external tool integration
- **RAG** — Retrieval-Augmented Generation with document indexing
- **Bots** — Discord and Telegram integrations that connect agents to messaging platforms
- **Workflows** — Visual automation pipelines with triggers and node-based execution
- **Tokens** — API gateway authentication tokens with scoping

## Storage

AT supports three storage backends:

| Backend | Use Case |
|---------|----------|
| **SQLite** | Default, single-instance deployments |
| **PostgreSQL** | Production, multi-instance deployments |
| **Memory** | Testing and development |
