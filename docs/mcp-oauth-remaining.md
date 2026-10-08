# MCP OAuth — remaining work

Status: **foundation only.** Nothing is wired to the runtime or the UI yet, so a
user cannot connect a GitHub/GitLab MCP account today. Continue from here.

Goal: HTTP MCP upstreams (GitHub, GitLab, any spec-compliant server) authenticate
with OAuth 2.1 per the MCP authorization spec, and **different agents and
different AT users use different external accounts with the same MCP set**.

## Decisions already made (do not re-ask)

1. **Account selection is an ordered fallback list** on the upstream:
   `auth.accounts`, e.g. `["agent", "user"]`. The first source yielding a usable
   connection wins. A source not in the list is never consulted — in particular
   `shared` is never used unless listed. Empty list means `["user"]`.
   - `user` → the personal connection of the account the run executes as.
   - `agent` → the connection the running agent binds for `auth.provider`
     (`AgentConfig.Connections` / `SkillRef.Connections`, the existing
     `workflow.ContextWithAgentConnections`).
   - `shared` → `auth.shared_connection_id`, one workspace connection.
2. **Gateway MCP identity for `user`:** a *personal* API token
   (`token.OwnerUserID != ""`) uses the token owner's account; workspace tokens,
   bots and triggers use the Run as (`execution_service_bindings`) account.
   This only selects *whose credential* is used; it must never let the Run as
   principal read another user's personal connection, nor let the token owner
   gain the Run as principal's authority.
3. **Scope: everything** — backend, MCP Set/Server editor, personal connections
   on the Connections page, agent account binding, tests.

## Done (committed)

| Piece | File |
|---|---|
| Upstream config shape: `MCPUpstream.Auth`, `MCPUpstreamAuth`, `MCPAccountUser/Agent/Shared` | `internal/service/types-mcp.go` |
| Protocol client: PRM (RFC 9728) via 401 `WWW-Authenticate` + well-known, AS metadata (RFC 8414/OIDC), DCR (RFC 7591), PKCE S256, `resource` (RFC 8707), exchange, refresh, bearer-challenge parser, SSRF-safe HTTP client | `internal/service/mcpauth/mcpauth.go` (+ `_test.go`) |
| MCP HTTP client token injection: `WithMCPAccessToken(source)`; source called per request, on 401 called once with the rejected token, then one retry; 403 never refreshes; redirects refused; typed `MCPHTTPError` (status + `WWW-Authenticate`) | `internal/service/mcp-oauth-client.go` (+ `_test.go`), `internal/service/client.go` |
| Pending-authorization store: encrypted, 10 min TTL, single-use, bound to workspace + user + exact session, ≤8 pending per account, included in key rotation and workspace deletion | `internal/service/types-mcp-oauth.go`, `internal/store/postgres/mcp-oauth.go` (+ `_test.go`) |
| Migration 97: `connections.owner_user_id` (+ partial unique indexes), `mcp_oauth_pending` table | `internal/store/postgres/migrations/97_mcp_oauth.sql` |

Migration 97 is committed: **add new migrations (98+) instead of editing it.**

Protocol choices worth keeping:
- Discovery never guesses endpoints. No PRM and no explicit
  `auth.authorization_server` → error. AS metadata missing or without `S256` → error.
- PRM `resource` must equal the MCP URL (canonical form); issuer must match exactly.
- Token POSTs never follow redirects; endpoints must be https (http only when the
  MCP server itself is loopback/private); no ambient proxy (it would bypass the
  dial-time address check).
- Token-endpoint error descriptions are not surfaced (they can echo secrets).

## Remaining

### 1. Personal connections (store + API)
- `service.Connection` gets `OwnerUserID` + derived `scope` (`workspace` | `personal`),
  like personal tokens/agents. Owner is stamped from the authenticated principal,
  immutable, never taken from the request body.
- Store: personal rows visible only to their owner (plus platform admin for
  listing, **never** for credential reads used at runtime on someone else's
  behalf). Apply the predicate before pagination/counting, on get, and under the
  write transaction (see `tokenOwnershipPredicate`, `agentVisibilityScope`).
- Admission: personal connection CRUD for any member with a capability like
  `connections.use`/`.read` (decide; workspace connections keep `connections.write`).
  Update `workspaceBusinessPolicies`, `capabilityRoutes` in
  `_ui/src/lib/helper/navigation.ts`, and keep
  `TestUICapabilityRoutesAreCapabilityAdmitted` green.
- Account deletion: add personal connections to `authUserDeletionTables`.
- Fix known gaps found during investigation:
  - `connectionLookupFunc` uses the redacting `GetConnection`; runtime must use
    `ResolveConnectionForUse` (unredacted, admission-checked).
  - runtime revalidation of `connections.use` maps to `.read` — verify and fix.

### 2. Token storage + locked refresh
- Store OAuth results on the connection: access token, refresh token, expiry,
  client ID (+ DCR client secret), issuer, token endpoint, resource, scopes.
  Encrypted with the existing connection credential blob. Prefer explicit
  fields or a typed `Extra` sub-object over ad-hoc `Extra["<slug>_access_token"]`.
- `WithMCPOAuthTokens(ctx, connectionID, change)`: row lock (`SELECT … FOR UPDATE`)
  across reload → refresh → encrypted save, exactly like
  `WithClaudeOAuthTokens` in `internal/store/postgres/provider-oauth.go`.
  Inside the lock: if the stored access token differs from the rejected one or is
  still fresh, adopt it without refreshing (another replica already rotated).
- `invalid_grant` (`mcpauth.IsInvalidGrant`) → mark the connection as needing
  re-authorization and fail with an actionable error; never fall back to another
  account or to static headers.

### 3. Authorization endpoints
- `POST /api/v1/mcp/oauth/start` `{upstream ref (set id + index or server id), connection target: personal|shared, connection name}`:
  admission (`mcp.use` on the set; `connections.write` for a shared target),
  `mcpauth.Discover` → use `auth.client_id` or `mcpauth.Register` → `NewPKCE` →
  random 32-byte state, store `sha256(state)` via `SaveMCPOAuthPending` with the
  verifier, client credentials, metadata, upstream ref, target → return
  `AuthorizeURL`.
- `GET /api/v1/mcp/oauth/callback?code&state`: `TakeMCPOAuthPending(sha256(state))`
  (it enforces same user/workspace/session), `mcpauth.Exchange` with the stored
  verifier and `resource`, create/update the connection, render a popup result.
  It is a top-level navigation: add the path to the cross-site callback allowlist
  in `nativeauth.SameOrigin` (currently only `api/v1/oauth/callback` and
  `code-display`), and make sure workspace selection works without the
  `X-AT-Workspace-ID` header (state row carries the workspace; consider
  `nativeBlobReadPath`-style query selector or resolve from the state row).
- Redirect URI: `publicBaseURL(r) + "/api/v1/mcp/oauth/callback"` (respect
  `server.base_path`). It must be byte-identical at start and exchange; store it
  in the pending payload.
- Feature gate: map `mcp/oauth` in `featureKeyForRoute`.
- Optional: periodic sweep of expired `mcp_oauth_pending` rows (save already
  deletes expired rows opportunistically).

### 4. Runtime wiring
- One resolver: `resolveMCPUpstreamToken(ctx, upstream)` walking `auth.accounts`:
  - `user`: `service.ExecutionFromContext(ctx).UserID`, or the personal gateway
    token owner (decision 2). Look up that account's personal connection for
    `auth.provider` in the execution workspace.
  - `agent`: the agent connection binding for `auth.provider`.
  - `shared`: `auth.shared_connection_id`, checked with `connections.use`.
  - Each candidate passes resource admission before any secret is read.
  - Return `MCPAccessTokenSource` closing over the **connection ID**, not the token.
- Pass `service.WithMCPAccessToken(source)` in every HTTP upstream construction:
  `newExecutionMCPClient` (`runtime-mcp.go`), the legacy builder
  (`mcp-runtime.go`, `acquireMCPClient`), `agentMCPTools.Connect`
  (`agent-tools.go`), gateway (`gateway-mcp.go`), Chats MCP-set paths
  (`internal-mcp.go` — currently uses the legacy builder despite a bound
  execution context; fix), workflow `agent_call`.
- **Ordering bug:** Sessions (`chat-sessions.go`) and workflow `agent_call`
  connect MCP *before* `ContextWithAgentConnections`; org delegation and
  Developer Spaces do it after. The `agent` source needs the bindings installed
  before `Connect`. Move them.
- HTTP MCP clients are not pooled (fine). Stdio is pooled by command+args and
  must not get OAuth (spec: stdio uses env).
- Upstream HTTP headers do **not** resolve `{{var:...}}` today; don't rely on it.
- Traces: record the connection ID / source used on tool observations, never tokens.
- Missing account → clear tool error naming the provider and how to connect
  ("Connect your GitHub MCP account in Connections"), not a silent skip.

### 5. Config validation and redaction
- Validate `auth` on MCP set/server create/update: `type == "oauth2"`, HTTP
  upstream only (no `command`), `mcpauth.ValidProvider`, default
  `provider = mcpauth.ProviderForURL(url)`, accounts ⊆ {user, agent, shared}
  without duplicates, `shared` requires `shared_connection_id` that exists in the
  workspace and is not personal.
- An upstream with `auth` must not also carry a static `Authorization` header
  (or document precedence: OAuth wins, as the transport already does).

### 6. UI
- MCP Set / MCP Server editors (`_ui/src/pages/Mcps.svelte`,
  `McpServers.svelte`, `_ui/src/lib/api/mcp-servers.ts` `MCPUpstream` type):
  per-HTTP-upstream *Authentication* section — None / OAuth; provider key;
  ordered account sources (reorderable); shared connection picker; scopes;
  client ID; advanced issuer override; "Connect my account" / "Connect shared
  account" buttons (popup, like `helper/auth-popup.ts`) and connection status.
- Connections page (`Connections.svelte`): Personal vs Workspace sections,
  personal MCP accounts with reconnect/disconnect, "needs re-authorization" state.
- Agents page (`Agents.svelte`, connections block ~line 889): lets an agent bind
  a connection for an MCP provider (already keyed by provider, so MCP providers
  should just appear once connections exist for them).
- Follow `_ui/AGENTS.md` style (square, compact, no transitions).

### 7. Tests to add
- Postgres: personal connection isolation (other user, other workspace, admin
  cannot use another user's personal connection at runtime), owner immutability,
  account deletion sweep.
- Concurrent refresh across two store handles: exactly one refresh request,
  both callers end with the rotated token; stale rejected token adopts stored one.
- Resolver: fallback order, unlisted `shared` never used, missing account error,
  gateway personal token vs workspace token vs bot.
- End-to-end with a fake MCP + AS (`httptest`): start → callback → tool call →
  401 → refresh → success; invalid_grant → re-auth error.
- UI: `pnpm run check`, tests for the editor serialization.

### 8. Docs
- Add an *MCP OAuth* section to root `AGENTS.md` (decisions, security
  invariants, regression test names) and delete this file when done.

## Provider notes (verify before relying on them)
GitHub's and GitLab's MCP OAuth support (DCR availability, PRM, scopes) was
**not** checked against current docs. If a provider lacks DCR, `auth.client_id`
plus a client secret stored in an encrypted connection is required. Never put a
client secret in MCP set config (it is not encrypted there).

## How to test
```sh
make env   # postgres
go test -race ./internal/service/... ./internal/store/postgres/... ./internal/server/...
cd _ui && pnpm run check
```
