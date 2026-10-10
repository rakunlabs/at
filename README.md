<img align="right" height="94" src="assets/favicon-128x128.png">

# AT

OpenAI-compatible LLM gateway with agents, workflows and an admin UI. Route
requests to many providers (OpenAI, Anthropic, Gemini, Vertex, Bedrock, Azure,
Cohere, …) through one endpoint.

> Under active development, expect breaking changes. Feedback and contributions are welcome!

## Quick start

AT requires PostgreSQL.

```sh
docker run -d --name at -p 8080:8080 \
  -e AT_STORE_POSTGRES_DATASOURCE="postgres://user:pass@host:5432/at?sslmode=disable" \
  ghcr.io/rakunlabs/at:latest
```

Open http://localhost:8080, create the first administrator, then add providers
and API tokens from the UI. Point any OpenAI client at:

```
http://localhost:8080/gateway/v1
```

## Configuration

Providers, API tokens, bots, authentication and system settings (server name,
public URL, log level, workspace retention, sandbox backend) are managed in the
UI and stored in PostgreSQL. The config file only holds what is needed before
the database is reachable or what is tied to the deployment itself.

`at.yaml` in the working directory is loaded automatically (or set
`AT_CONFIG_FILE`). Every key can also be set as an environment variable with
the `AT_` prefix, e.g. `AT_STORE_POSTGRES_DATASOURCE`.

```yaml
server:
  port: "8080"
  # base_path: /at                  # serve under a sub-path
  # trusted_proxies: ["private"]    # when behind a reverse proxy
  # workspace:
  #   root: /mnt/at-workspace       # task workspaces + persistent assets
store:
  # encryption_key: "change-me"     # encrypts stored credentials
  postgres:
    datasource: "postgres://user:pass@localhost:5432/at?sslmode=disable"
```

Changing `encryption_key` requires rotating it first in Settings → System → Encryption.

## Documentation

- [Native authentication](NATIVE_AUTH.md)
- [Chat sharing](docs/chat-sharing.md)
- [Developer Spaces on Kubernetes](docs/developer-spaces-kubernetes.md)
- [Mobile app (PWA)](_ui/README.md)
- API reference and guides are built in: open **Documentation** in the UI.

## Development

```sh
make env                   # start PostgreSQL (docker compose)
make run                   # run the server on :8080
make install-ui run-ui     # UI dev server on :3000
make test
```
