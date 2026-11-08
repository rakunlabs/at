# Running behind a reverse proxy with a base path

With `server.base_path: /at`, the application, the API and the gateway are all
served under `/at`.

MCP clients that sign in with OAuth also read three discovery documents. They
derive these addresses from the issuer `https://<host>/at` and from the MCP
server URL, as RFC 8414 §3.1 and RFC 9728 §3.1 require. The `.well-known`
segment goes **before** the base path, so the addresses are outside `/at`:

| Prefix | Document |
|---|---|
| `/.well-known/oauth-authorization-server/at` | OAuth authorization server metadata (RFC 8414) |
| `/.well-known/openid-configuration/at` | The same metadata, for clients that only look for OpenID configuration |
| `/.well-known/oauth-protected-resource/at` | Protected resource metadata of each MCP server, e.g. `.../at/gateway/v1/mcp/<name>` (RFC 9728) |

Route these three prefixes to AT in addition to `/at`. Without them, an MCP
client receives the 401 challenge but cannot find where to sign in.

Forward the path unchanged: do not strip `/at` or the `.well-known` prefix.
AT matches the full path.

## Turna (recommended)

[Turna](https://github.com/rakunlabs/turna) is our own reverse proxy and
service runner. Put this in `turna.yaml`:

```yaml
server:
  entrypoints:
    web:
      address: ":80"
  http:
    middlewares:
      at:
        service:
          loadbalancer:
            servers:
              - url: http://at:8080   # or http://localhost:8080
    routers:
      at:
        path:
          - /at
          - /at/*
          - /.well-known/oauth-authorization-server/at
          - /.well-known/openid-configuration/at
          - /.well-known/oauth-protected-resource/at
          - /.well-known/oauth-protected-resource/at/*
        middlewares:
          - at
```

Turna router paths match exactly, and `/x/*` matches only below `/x/`.
That is why `/at` and `/at/*` are both listed: AT redirects `/at` to `/at/`.
The protected resource metadata has one document per MCP server below
`/.well-known/oauth-protected-resource/at/`, which `/*` covers.

Turna keeps the path and the `Host` header by default, so no other
middleware is needed. Do not add `strip_prefix` to this router.

Run it with the binary (`turna` in the directory that holds `turna.yaml`, or
`CONFIG_FILE=/path/to/turna.yaml turna`) or with Docker:

```yaml
services:
  turna:
    image: ghcr.io/rakunlabs/turna:latest
    ports: ["80:80"]
    volumes:
      - ./turna.yaml:/turna.yaml:ro
  at:
    image: ghcr.io/rakunlabs/at:latest
    environment:
      AT_SERVER_BASE_PATH: /at
      AT_SERVER_TRUSTED_PROXIES: private
      AT_STORE_POSTGRES_DATASOURCE: postgres://user:pass@postgres:5432/at?sslmode=disable
```

For HTTPS, add `tls: {}` to the router and configure certificates or ACME
under `server.http.tls`; see the Turna documentation.

**Client addresses.** Turna appends the address it observed to
`X-Forwarded-For`. Tell AT to trust Turna and read that header:

- same host: `server.trusted_proxies: ["loopback"]`
- Docker or Kubernetes network: `server.trusted_proxies: ["private"]`
- keep `trusted_proxy_header` at its default, `X-Forwarded-For`. Do not use
  `X-Real-IP`: Turna passes a client-supplied value through.

When Turna faces the internet directly, you can also put its `real_ip`
middleware first in the chain. It removes forwarding headers that clients
send themselves.

## Traefik

```yaml
http:
  routers:
    at:
      rule: >-
        PathPrefix(`/at`) ||
        PathPrefix(`/.well-known/oauth-authorization-server/at`) ||
        PathPrefix(`/.well-known/openid-configuration/at`) ||
        PathPrefix(`/.well-known/oauth-protected-resource/at`)
      service: at
  services:
    at:
      loadBalancer:
        servers:
          - url: "http://at:8080"
```

## nginx

```nginx
location /at/ {
    proxy_pass http://at:8080;
}
location ~ ^/\.well-known/(oauth-authorization-server|openid-configuration|oauth-protected-resource)/at(/|$) {
    proxy_pass http://at:8080;
}
```

`proxy_pass` without a URI path forwards the request path unchanged.

## Related settings

- Set **Settings → System → Public URL** to the external origin, e.g.
  `https://example.com`. AT builds the issuer and the discovery addresses from
  it, so they stay correct whatever headers the proxy sends.
- Set `server.trusted_proxies` so sign-in history and rate limits see the
  real client address instead of the proxy's (see the Turna section).
- Optional: some clients also try the documents at the bare root
  (`/.well-known/oauth-authorization-server`,
  `/.well-known/openid-configuration`). AT answers those too; route them only
  if no other application on the host uses them.
