# Native Management Authentication

Native authentication is enabled by default. Installation administration is
**administrator-only**; workspace authorization is a separate layer.
Administrators control the entire AT installation, including secrets, files, tools,
agents, gateway tokens, and every organization. Non-admin accounts may sign in,
inspect their own identity, change their password, manage their own passkeys, and sign out, but cannot use
management APIs or the app.
Do not expose AT as a shared untrusted-user service on the strength of this slice.

## Activation

Start AT with its usual PostgreSQL, bind, encryption and telemetry configuration,
then open the app to create the first local administrator and sign in. There is no
required `native_auth.enabled`, origin YAML, or bootstrap-token copy/paste step.
Authentication product settings live in PostgreSQL and are managed in Settings.
Administrator sessions protect Settings; `admin_token` and forwarded user headers
are not alternative login credentials. Gateway tokens remain separate.

```yaml
server:
  base_path: /at  # optional; omit for the root. No trailing slash.
  external_url: https://at.example.com/at  # optional pre-setup deployment anchor
store:
  postgres:
    datasource: postgres://at:REPLACE@postgres/at?sslmode=require
```

For automation migrating an existing bootstrap-token deployment, the optional
`POST /auth/bootstrap` API remains available when its legacy token is configured
(at least 32 bytes). The normal first-run UI uses `POST /auth/setup` instead.

All replicas must share the database and BasePath. They load the canonical origin
and product policy from the database on every authentication request.
Run behind HTTPS, redirect plaintext to HTTPS at the edge, and keep the backend
listener private. Cookies are always Secure in HTTPS mode, even behind TLS
termination. Setup requires an exact browser Origin matching Host and any
configured deployment anchor. Reverse proxies must preserve the intended Host;
`X-Forwarded-*` never establishes trust. After setup the persisted origin is
authoritative. HTTP is allowed only on loopback for development, without an extra
flag. With Vite, preserve `Host: localhost:3000` when proxying setup, or perform
setup on the final deployment origin. Set the backend bind host to loopback locally.

Build the UI into the binary with the normal `make build-ui` / release build
pipeline when deploying this change. `pnpm run build` alone writes `_ui/dist`,
not the Go embed directory. Visit `https://at.example.com/at/` with the final slash.
The static SPA remains public, while management data is authenticated server-side.

## First Administrator

The public status endpoint reports `setup_required:true` only while the durable
bootstrap latch is unclaimed. Open the Create administrator screen and supply a
username and password. The UI posts `{username,password,origin}` to `/auth/setup`,
which atomically pins the canonical origin/settings and creates a LOCAL installation
administrator. A 201 response returns the new identity; sign in afterward through
the normal password/MFA flow. Setup itself does not issue a session.

Passwords must have 8 or more
characters and at most 1024 UTF-8 bytes, matching NIST SP 800-63B's minimum for
user-chosen secrets. That floor is deliberately low, so enrol a second factor on
administrator accounts rather than relying on length. Usernames normalize to lowercase, trim
surrounding whitespace, and allow 3-128 ASCII letters, digits, or `._@+-`.
For example, use `operator@example.com` as the username. Choose a unique generated
password or long passphrase, not an example password from documentation.

For legacy token-based automation only, supply a private JSON credential file:

```sh
curl --fail-with-body https://at.example.com/at/auth/bootstrap \
  -H 'Origin: https://at.example.com' \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $AT_SERVER_NATIVE_AUTH_BOOTSTRAP_TOKEN" \
  --data-binary @/secure/at-first-admin.json
```

This creates the first administrator and returns its stable `subject` ID; it does
not sign in automatically. Open the web app and sign in. Remove the bootstrap
secret from every replica and restart after success. A missing bootstrap secret
disables bootstrap, not ordinary login. Protect/delete the credential file and
avoid putting real passwords into shell history or command-line arguments.

Bootstrap is a database transaction: an atomic update claims a singleton latch
and inserts the administrator. Exactly one concurrent request can win across all
replicas. A failed insert rolls the latch back. Deleting or disabling users does
**not** reopen bootstrap. There is no public registration/count-then-create path.

Migration 37 stores versioned auth settings. Existing configured native deployments
import their origin and session TTLs once; keep those values for the first upgraded
startup. Subsequent YAML changes do not overwrite DB settings. Old claimed stores
without an origin fail closed until one is provided for import. Old nonnative
installations enter setup-required mode with management APIs gated, preserving
their data. Plan the operator's first upgraded visit to claim the installation.
`enabled:false` no longer disables authentication or restores anonymous management.

Settings exposes session and remembered lifetimes, local primary login, invitation
or approval admission, and display title. Identity providers use the existing
`/auth/identity-providers` APIs. `PUT /auth/settings` requires the current `version`;
stale edits return 409. The canonical origin is pinned and cannot be changed there.
Enrolled MFA is always required and the 20-session ceiling cannot be bypassed.
Disabling local login requires a linked, active installation administrator on an
enabled external provider. The DB also prevents later provider/link/admin changes
from removing that last usable external primary while local login is disabled.

Migration `25_native_auth.sql` is additive and uses AT's configured table prefix.
Back up the database normally; do not reset the bootstrap latch as a recovery
mechanism. Before relying on native auth, create a second securely held admin
account. A live administrator can reset passwords and re-enable disabled accounts.
There is no unauthenticated recovery flow; recovery after losing access to every
administrator requires a separate reviewed operator procedure.
Use the authenticated recovery flow or operator recovery command after losing
access; resetting the bootstrap latch or disabling auth is not a recovery path.

## API

Paths below are relative to `server.base_path`. Auth request bodies accept JSON
only and are limited to 4 KiB (WebAuthn finish bodies: 64 KiB). Unsafe cookie-authenticated requests, including
login/bootstrap, require the exact configured `Origin` header. Browser fetches
send it automatically; scripts must set it explicitly. Do not use GET for writes.

| Endpoint | Access | Body / result |
| --- | --- | --- |
| `GET /auth/status` | Public | `{ "enabled": true, "setup_required": false, "local_login": true, "display_title": "AT", "signup_admission": "invite_only", "passkeys": true, "remember_me": true, "passkey_login": "username-first", "mobile_auth": {...} }`; DB failure returns 503 |
| `POST /auth/setup` | Unclaimed installation + exact Origin/Host | `{ "username": "...", "password": "...", "origin": "https://at.example.com" }`; local first admin, 201; no session |
| `GET /auth/settings` | Platform admin | Versioned auth product policy |
| `PUT /auth/settings` | Platform admin + Origin | Full policy with expected `version`; 200 new policy, stale/lockout 409 |
| `POST /auth/bootstrap` | Operator bearer secret + Origin | `{ "username": "...", "password": "..." }`; first admin only, 201 |
| `POST /auth/login` | Public + Origin | `{ "username": "...", "password": "...", "remember_me": false }`; 200 identity + two HttpOnly cookies |
| `GET /auth/me` | Live web access cookie OR mobile access bearer, never both | 200 identity and nonsecret session metadata; expired access returns 401 without refreshing |
| `POST /auth/refresh` | Refresh cookie + Origin; no access cookie required | `{}`; 200 identity + rotated access/refresh cookies |
| `POST /auth/logout` | Origin; no live access cookie required | `{}` (or empty body); revokes presented credentials' families, clears both cookies, 204 |
| `POST /auth/password` | Live user session + Origin | `{ "current_password": "...", "new_password": "..." }`; changes password, revokes every session and clears this browser cookie, 204 |
| `POST /auth/users` | Admin + Origin | `{ "username": "...", "password": "...", "admin": false }`; identity, 201 |
| `GET /auth/users?limit=50&after=...` | Admin | Bounded keyset page, exact DTO below |
| `POST /auth/users/{id}/disable` | Admin + Origin | Disables user and invalidates all sessions, 204 |
| `POST /auth/users/{id}/enable` | Admin + Origin | No body required; enables user and invalidates all sessions, 204 |
| `POST /auth/users/{id}/password` | Admin + Origin | `{ "password": "..." }`; replaces password and invalidates all sessions, 204 |
| `POST /auth/users/{id}/revoke-sessions` | Admin + Origin | Invalidates all sessions without disabling, 204 |
| `/api/*` | Admin | All management APIs, including settings |
| `/internal/*` | Admin | Former unauthenticated internal MCP routes |

Use the stable `subject` from creation or `me`, or `id` from listing, as `{id}`.
There is no role-edit API. Self-disable is rejected. The store also
serializes admin disables so concurrent requests cannot disable the last active
administrator. You may revoke your own sessions, which requires signing in again.
Non-admins receive JSON 403 for management. Missing/expired/revoked/disabled
sessions receive JSON 401, never a login redirect. Store errors fail closed.

### Access and Refresh Contract

Migration **27_auth_refresh.sql** deliberately deletes all v25/v26 opaque
sessions, forcing a fresh login. It does not modify migrations 25 or 26, user
passwords, passkeys, the bootstrap latch, or connector credentials. Existing
ceremonies tied to deleted sessions cannot complete. The old opaque credential is
never interpreted as a refresh credential. Deploy this migration and the new
backend together during a maintenance window; do not run old and new native-auth
issuers concurrently. The one-time legacy invalidation is a migration transaction,
not the recurring bounded janitor. Back up normally before upgrading.

Access and refresh credentials are independent random 256-bit, unpadded base64url
strings (43 characters); only SHA-256 hashes enter the database. The retained
`auth_sessions.hash` column is now a **nonsecret stable session/family ID**, not a
credential hash. `auth_credentials` holds the bearer hashes and has a cascading
foreign key only to its owning auth family. A session ID cannot authenticate,
refresh, or revoke a session by itself.

Password login, passkey login, refresh, and `GET /auth/me` return the same identity
shape. No token values, credential hashes, or user-version guards appear in JSON:

```json
{
  "subject": "01...",
  "name": "operator@example.com",
  "provider": "local",
  "roles": ["admin"],
  "expires_at": "2026-09-07T12:10:00Z",
  "claims": {
    "session_id": "<64-character nonsecret family ID>",
    "session_expires_at": "2026-09-07T20:00:00Z",
    "remember_me": false
  }
}
```

`roles` is omitted for non-admins. Timestamps are RFC3339 with optional fractional
seconds. `expires_at` is access expiry; `claims.session_expires_at` is the immutable
absolute deadline. `claims.session_id` remains stable across rotations and changes
on every fresh login, including login as the same user. There is no top-level
`session_id` field. Bootstrap/user creation returns an identity without session
metadata because it does not sign the created user in.

- Access expiry is `min(now + 10 minutes, absolute session deadline)`.
- `remember_me` omitted/false selects Settings `session_ttl_seconds`, default
  `28800` (8h); valid values are 600 through 86400, inclusive.
- `remember_me: true` selects Settings `remember_ttl_seconds`, default 2592000
  (30 days); it must be at least the ordinary session lifetime and at most 30 days.
- Invalid durations are rejected by the settings API. Legacy YAML/environment
  duration fields are import-only. Login clients cannot supply arbitrary TTLs.
- Neither requests nor rotations move the absolute deadline. Changing config does
  not rewrite existing sessions. Both modes survive server restarts; browser
  session restoration can restore nonpersistent cookies, so the database deadline
  remains the security boundary.

**Cookies:** HTTPS uses `__Secure-at_session` (access) and `__Secure-at_refresh`.
Loopback insecure development uses `at_session` and `at_refresh`. Both are
host-only (no Domain), `HttpOnly`, `SameSite=Lax`, and `Path=<base_path>/` (`/` at
root). Both are `Secure` except in explicitly permitted loopback HTTP mode. For
`remember_me: false`, neither cookie has Expires nor Max-Age. For true, each has
Expires equal to its own credential deadline and Max-Age rounded down to remaining
whole seconds, never extending the deadline. Thus the persisted access cookie is
at most 600 seconds, while refresh is at most the remaining absolute session
lifetime, not a new 30-day window. Logout/password change/passkey deletion expire
both cookie names with the same attributes, empty values, and negative Max-Age.

**Refresh request:** `POST <base_path>/auth/refresh`, `Content-Type:
application/json`, body `{}`, exact configured `Origin`, browser cookies enabled.
There is no Authorization, token, TTL, remember choice, or session ID in its body.
Missing, duplicate, malformed, unknown, expired, revoked, or disabled-account
refresh credentials are denied. A successful transaction consumes the presented
refresh, deletes the previous access credential, and inserts one new pair, keeping
the family ID and deadline. Reusing a consumed refresh **deletes the entire
family**, including its latest access/refresh credentials. Two simultaneous uses
have at most one successful rotation, but the losing replay revokes that winner's
family too. There is no grace period or replay bypass.

All responses below have `Cache-Control: no-store`. Errors are JSON objects with
exactly the `message` field; they never redirect or set/clear credential cookies.
Successful refresh returns 200 with the identity above and two Set-Cookie headers.

| Endpoint / status | Exact message or response |
| --- | --- |
| `GET /auth/me` 200 | Identity above; no Set-Cookie |
| `GET /auth/me` 401 | `{"message":"authentication required"}` for missing, duplicate, malformed, expired, revoked access, absent family, or disabled account |
| `GET /auth/me` 503 | `{"message":"authentication unavailable"}` on storage failure |
| Refresh 400 | `{"message":"invalid authentication request"}` for invalid JSON, null, unknown fields, or trailing data |
| Refresh 401 | `{"message":"refresh rejected; sign in again"}` for invalid credentials, including detected replay |
| Refresh 413 | `{"message":"invalid authentication request"}` when the 4 KiB body cap is exceeded |
| Refresh 415 | `{"message":"application/json required"}` |
| Refresh 429 | `{"message":"refresh too early; retry after the indicated delay"}` with `Retry-After: <positive integer seconds>` |
| Refresh 503 | `{"message":"authentication unavailable"}` on storage/generation failure or the 10-second rotation deadline |
| Me/refresh/logout 403 | `{"message":"same-origin request required"}` on failed Origin/Fetch Metadata checks (me authenticates before the origin check) |
| Logout 204 | No body; clears both cookies, including when no cookies are present or they are already unknown |
| Logout 503 | `{"message":"authentication unavailable"}`; cookies are not cleared because revocation was not confirmed |

Logout accepts `{}` or an empty body and requires the exact Origin even without
cookies. It does **not** require access to be live: a refresh cookie, including a
consumed one still retained by the database, identifies the current-device family.
It revokes all families identified by the presented access/refresh cookies, never
other devices merely because they share a user. User-wide revoke/password changes,
disable/enable, and passkey deletion retain their transactional user-version guards
and invalidate both kinds of credentials. In-flight requests/streams admitted
before revocation are not generally cancelled.

**Bounds and cleanup:** the first refresh after login may happen immediately.
Subsequent successful rotations must be at least five minutes apart, enforced by
database wall time under the family lock across replicas. Early use of the current
refresh returns 429 without consuming it, changing any expiry, or creating rows.
Consumed-token replay is checked **before** this guard and still revokes. Issuance
also serializes on the user and caps live families at 20; exceeding it returns 429
`{"message":"maximum 20 active sessions; sign out a device or revoke sessions"}`
on password/passkey login. No automatic eviction silently signs out other devices.
These bounds limit refresh-tombstone growth; perimeter request-rate limiting is
still required against request floods and repeated invalid-credential lookups.

The server-context janitor makes one initial asynchronous sweep and one sweep every
five minutes, each capped at five seconds. A sweep deletes at most 500 expired
credential rows, then at most 500 expired **empty** families using `SKIP LOCKED`.
It never drains an unlimited backlog at startup or lets a large cascade exceed
the credential batch. Expired access can be purged early. Refresh rows, including
consumed tombstones, have the immutable family deadline as their expiry and are
kept until that deadline; purging an old access never removes replay evidence.
Revocation/replay may delete the entire family immediately: subsequently unknown
credentials deny authentication just as safely. Concurrent replica janitors are
safe, cancellation stops the worker/query, and cleanup errors are logged without
killing the gateway. Authentication checks deadlines even if cleanup falls behind.

### Browser Coordination

Do not implement silent refresh in a generic retry-on-any-error loop. Use a
single-flight promise per tab plus an origin-wide browser Web Lock keyed by the
AT base path for refresh, login, logout, and identity transitions. Cookies are
shared across tabs and refresh has **no** replay grace window.

1. On a protected-request 401, acquire the Web Lock and call `GET /auth/me` again
   with cookies. Another tab may already have refreshed; if me succeeds, use that
   identity without refreshing again.
2. Only if me still returns 401, send `POST /auth/refresh` with `{}` while holding
   the lock. On 200, update the identity and retry the original request at most
   once, only if its body is replayable and the operation permits it.
3. Compare `claims.session_id` with the identity under which the original request
   was made. Never replay an old action under a different login/session. A refresh
   keeps this ID; login changes it even for the same subject. Do not persist or
   broadcast credentials; only nonsecret identity/session metadata may be shared.
4. Refresh 401 is terminal for that attempt. A 429 does not sign out or consume
   credentials; honor Retry-After and do not spin. A 503 or transport failure is
   **not proof of logout** and may have an ambiguous commit outcome. Re-check me
   before any later attempt; never blindly retry refresh. A lost rotation response
   can require reauthentication because replay revokes the family.
5. Exclude login, bootstrap, refresh, logout, password actions and single-use
   passkey finishes from generic automatic retries. Check/refresh me before
   completing an enrollment, and hold the same Web Lock through its finish request
   so another tab cannot rotate mid-finish. This prevents expired access consuming its single-use
   ceremony. Invalid ceremony attempts retain the prior consume-on-attempt
   semantics; a terminal ceremony failure means restart that ceremony.

Web Locks serialize only cooperating browsers, not hostile callers. If unavailable,
disable automatic cross-tab refresh or require explicit reauthentication rather
than assuming an in-memory promise coordinates every tab. OAuth top-level return
navigations cannot use a fetch interceptor: ensure access is refreshed before
departure where needed, or restart authorization if the return arrives after
access expiry. No middleware silently refreshes on a GET, callback, or WebSocket.

### Stable Ceremony Binding

Connector OAuth state, manual PKCE keys, passkey enrollment and passkey deletion
now bind to the stable family ID, not the rotating access hash. After an explicit
refresh, the same browser session can complete a still-valid ceremony. A fresh
login (even as the same user), another device, replay revocation, logout, user
disable, or user-version change cannot inherit the binding. Already admitted
management requests keep their family identity while still checking that the
family is live, so rotating during an action does not substitute a new session.
Enrollment/deletion transactions retain user-first/family locking and post-lock
database-wall-time checks, including a second deadline check after credential
writes; login admission retains both passkey ceremony deadline checks. Refresh
never extends the five-minute passkey or ten-minute connector-state deadline.
Connector OAuth state/PKCE remain process-local as before: sticky routing is still
required for those pending flows, and restarting the issuer requires restarting
pending connector authorization. Auth credentials and passkey ceremonies remain
database-backed across replicas/restarts.

### User Management Contracts

The list response is always an object with these fields (not an identity object):

```json
{
  "data": [
    { "id": "01...", "username": "operator@example.com", "admin": true, "disabled": false }
  ],
  "next_cursor": ""
}
```

`limit` defaults to 50 and accepts integers from 1 through 100. Results are ordered
by immutable `id` ascending and include disabled users. To continue, supply the
nonempty `next_cursor` as `after`; an empty cursor means there is no further page
at the time of the query. `after` is an opaque ID of at most 128 bytes. Empty pages
use `data: []`. Unknown/duplicate query parameters and malformed queries return
400. Pagination is not a snapshot across concurrent account creation.

Both password endpoints require JSON, reject unknown fields, and retain the
4 KiB body cap and password policy (8 characters minimum, 1024 UTF-8 bytes
maximum). Self-change requires the current password, not just a session; an admin
reset does not require the target's old password. A reset does not change roles
or enable a disabled user. Enable does not change the password or role. Even
enabling an already enabled account revokes its sessions. No operation returns
passwords, hashes, session versions, session credentials, or a generated password.

Successful password changes and admin resets return **204 with no body**. All
target sessions are deleted in the same transaction as the password hash update
and version increment. Resetting your own account also expires the browser cookie;
the UI must discard its identity and return to login. Resetting another account
does not sign the administrator out. A password change never automatically signs
the user back in, and old sessions cannot refresh or become valid after re-enable.

Self-change captures the account version before password verification and uses a
conditional update. A concurrent reset, disable, or revocation causes 409 rather
than overwriting the newer account state. Session issuance locks the same user row
and checks that version, so a login verified before a password change cannot mint
a usable session after it. The last-active-admin disable guard is unchanged;
password reset and enable cannot demote or disable an administrator.

Errors use `{ "message": "..." }` with `Cache-Control: no-store`:

| Status | Meaning |
| --- | --- |
| 400 | Invalid body/query, unknown fields, or password policy failure |
| 401 | Missing/invalid session, or incorrect current password on self-change |
| 403 | Non-admin management request or failed same-origin/CSRF check |
| 404 | Admin mutation target not found |
| 409 | Self-disable/last-admin protection, or concurrent self-change conflict |
| 413 / 415 | Request exceeds 4 KiB / content type is not `application/json` |
| 429 | Password derivation slots exhausted, or self-change rate limit exceeded |
| 503 | Store unavailable; no successful mutation or new session is claimed |

Self-change shares the existing login rate limiter (and its `Retry-After: 6`
response); admin resets share the two-slot password derivation bound. Management
operations remain native-session/admin-only; gateway tokens and bootstrap tokens
do not authorize them. Migration 25 already contains the necessary user fields;
this user-management extension requires no additional schema migration.

**Internal MCP compatibility:** native auth intentionally closes `/internal/*`.
Agent clients relying on unauthenticated loopback HTTP MCP URLs will no longer
work with those URLs. Do not exempt these endpoints or distribute admin browser
cookies to agents. Keep native auth off behind existing perimeter protection until
those workflows are moved to supported in-process resolution or a separately
reviewed machine-credential channel is implemented. Gateway MCP access remains
governed by its existing gateway token/public-server policy.

## Security Properties

- Ada auth `v0.5.1` supplies identity context, cookie options, WebAuthn, and password
  hashing. The published version includes the refresh response leak fix. No local
  module replacement is used. Pika's strategy/identity separation informed this
  integration; its bootstrap and storage implementation were not copied.
- Passwords use salted PBKDF2-HMAC-SHA256 with 600,000 iterations, 16-byte random
  salts, and 32-byte derived keys via Ada's published hasher. Password input is
  bounded; unknown users pay a dummy derivation and get the same credential error.
- Access and refresh credentials contain independent 256-bit randomness and are
  stored only as SHA-256 hashes. Stable session IDs are nonsecret identity metadata.
  Raw credentials travel only in host-only HttpOnly cookies, never JSON or
  localStorage. Login mints a new family and revokes presented old credentials.
- AT's explicit middleware checks short access credentials; only POST refresh
  rotates. Absolute session deadlines, remember-cookie policy, replay revocation,
  and bounded periodic cleanup are defined in the contract above. Ada's automatic
  server-held-token refresh protocol is deliberately disabled for this issuer.
- Every protected request resolves current user/session state from Postgres.
  Revocation increments a user version; issuance locks the user row and checks
  the version captured during password verification. Disable/revoke therefore
  cannot be bypassed by a login already in flight. Previously admitted requests
  and running streams/tasks are not cancelled by revocation.
- CSRF protection uses an exact configured Origin check for unsafe requests,
  rejects any supplied foreign Origin (including WebSocket handshakes), rejects
  cross-site Fetch Metadata except top-level GET navigations (`navigate` /
  `document`) to the exact connector `/api/v1/oauth/callback` and
  `/api/v1/oauth/code-display` paths under BasePath, and uses SameSite=Lax cookies.
  These returns still require a live administrator session and reject a supplied
  foreign Origin. There is no blanket GET, iframe, fetch, or POST exemption.
- In native mode, both connector return handlers require opaque 256-bit state,
  bound to the initiating session, provider/destination and return endpoint.
  State expires after ten minutes and is atomically consumed before processing
  either success or provider errors; missing, forged, expired and replayed state
  is rejected. Manual PKCE verifiers are also session-scoped. Redirect URIs use
  the configured origin rather than Host/forwarded headers. Manual token exchange
  remains a same-origin administrator POST. Legacy mode retains its existing
  state encoding and flow; it does not gain these state guarantees.
- Pending connector state is process-local (at most 1024 entries, expired entries
  pruned on initiation), like the existing PKCE cache. Route initiation and return
  to the same replica (sticky routing). Restarting or switching replicas requires
  restarting authorization; native login sessions themselves remain persistent.
- Public login/bootstrap share a bounded per-process limiter: burst 5, one new
  attempt per 6 seconds. At most two password derivations run concurrently.
  This coarse global limit intentionally avoids trusting client IP headers and
  unbounded per-IP maps. It can throttle legitimate admins under attack. Use an
  edge/network rate limit for internet exposure and shared limits across replicas;
  restarting a process resets its in-memory limit.
- Gateway credentials remain separate: management cookies cannot authenticate
  gateway calls, gateway bearer/API keys cannot authenticate management, and native
  mode removes Cookie headers from gateway requests before upstream passthrough.
  Webhook and public gateway-MCP policies are unchanged and must be reviewed by
  the operator. The old caller-controlled `user_header` is ignored for native
  management attribution; the database user ID is used instead.

## Passkeys: Backend Contract

Passkeys are available automatically with native auth, PostgreSQL migration
`26_auth_passkeys.sql`, and a canonical DNS origin. The RP ID is **exactly the
configured origin hostname**, with no parent-domain override and no request-Host
or forwarded-header inference. The sole WebAuthn origin is the configured exact
origin, including its port. `http://localhost:3000` works with `insecure_http: true`;
IP-literal origins retain password login but advertise `passkeys: false` and do
not register passkey routes. Public-suffix-only or malformed DNS RPs fail startup.
All replicas must use the same origin/BasePath. An origin/RP change can make old
keys unusable; retain password recovery and re-enroll at the intended origin.

This is **web-first passwordless authentication**, not a second factor added to
password login. Ada auth's published `v0.5.1` WebAuthn engine performs all
attestation and assertion verification. AT requests and enforces user verification
(PIN/biometric) for enrollment and login, and requests `residentKey: preferred`.
Login is username-first, including for discoverable keys, so older non-resident
security keys remain usable. There is no anonymous/discoverable account chooser
endpoint in this slice. Unknown, disabled, or keyless accounts receive the same
401 fallback message; success/options reveal that the supplied username has keys.
This is not an account-enumeration-resistant discovery API.

All paths below include the configured BasePath in production, e.g.
`/at/auth/passkeys/login/begin`. All POSTs require exact `Origin` and
`Content-Type: application/json`. Browser requests must retain cookies
(`credentials: 'same-origin'`). No body contains a ceremony token, password hash,
session token, private key, session version, or server session data in responses.

| Endpoint | Request | Success |
| --- | --- | --- |
| `GET /auth/passkeys` | Live own session; no body | `200 {"items":[{"id":"01...","name":"Laptop","created_at":"RFC3339 timestamp","last_used_at":null}]}`; last-used becomes an RFC3339 timestamp after successful assertion verification/CAS |
| `POST /auth/passkeys/enroll/begin` | Live own session; `{"name":"Laptop","current_password":"..."}` | `200 {"publicKey": <PublicKeyCredentialCreationOptions JSON>}` and ceremony cookie |
| `POST /auth/passkeys/enroll/finish` | Same live session and ceremony cookie; **raw WebAuthn registration credential JSON**, not a wrapper | `204`, no body; session remains unchanged |
| `POST /auth/passkeys/login/begin` | `{"username":"operator@example.com","remember_me":false}`; remember defaults false | `200 {"publicKey": <PublicKeyCredentialRequestOptions JSON>}` and ceremony cookie |
| `POST /auth/passkeys/login/finish` | Ceremony cookie; **raw WebAuthn assertion credential JSON**, not a wrapper | `200` same identity DTO as password login (`subject`, `name`, `provider`, optional `roles`), plus fresh session cookie |
| `POST /auth/passkeys/{id}/delete` | Live own session; `{"current_password":"..."}` | `204`; increments account version, revokes **all** that user's sessions and clears this browser's session cookie; UI must return to login |

List is bounded to 20 keys and uses `items: []` when empty. `id` is an opaque AT
record ID, **not** the WebAuthn credential's `id`/`rawId`. Names are trimmed, 1-80
UTF-8 bytes, with no control characters. No rename endpoint is shipped. Enroll a
replacement before deleting an old key when practical; deleting the final key is
allowed because current-password authentication remains available. Neither admin
privileges, gateway tokens nor a client-supplied user ID grant access to another
user's keys. Password reset/revocation invalidates pending ceremonies and sessions,
but does not delete existing keys. To retire a key, use the own-key delete route.

### Browser Wire Shapes

The options use WebAuthn's lower-camel-case names. All binary fields are unpadded
base64url JSON strings: creation `challenge`, `user.id`, excluded credential IDs;
request `challenge`, allowed credential IDs. `user.id` encodes the stable user ULID
bytes, not the username. Use the browser's
`PublicKeyCredential.parseCreationOptionsFromJSON` /
`parseRequestOptionsFromJSON` when available, or equivalent ArrayBuffer decoding,
then call `navigator.credentials.create({publicKey})` /
`navigator.credentials.get({publicKey})`. Serialize the resulting credential with
`toJSON()` where available or equivalent base64url encoding. Do not JSON-stringify
ArrayBuffers directly. Typical finish bodies:

```json
{
  "id": "base64url-credential-id",
  "rawId": "base64url-credential-id",
  "type": "public-key",
  "response": {
    "clientDataJSON": "base64url-client-data",
    "attestationObject": "base64url-attestation",
    "transports": ["internal"]
  },
  "authenticatorAttachment": "platform"
}
```

```json
{
  "id": "base64url-credential-id",
  "rawId": "base64url-credential-id",
  "type": "public-key",
  "response": {
    "clientDataJSON": "base64url-client-data",
    "authenticatorData": "base64url-authenticator-data",
    "signature": "base64url-signature",
    "userHandle": "base64url-user-ulid"
  }
}
```

`userHandle` may be omitted or null for non-resident assertions; any nonempty
handle must match the stored credential owner. Browser extension output fields
are tolerated by Ada. `id` and `rawId` must agree, and enrollment also checks them
against the ID inside the attested authenticator data. Login finish accepts no
effective username/version/remember override: these choices come exclusively from
the persisted begin record. Unknown fields on small begin/delete/password DTOs
are rejected; additional WebAuthn response fields never change AT policy.

### Ceremony Lifecycle

The random 256-bit HttpOnly, host-only ceremony cookie is named
`__Secure-at_session_ceremony` (local HTTP: `at_session_ceremony`), uses
`SameSite=Strict`, is scoped to `<BasePath>/auth/passkeys/`, and lasts five minutes.
Only its SHA-256 hash is stored. One browser cookie serves both ceremony purposes;
start only one ceremony at a time. Restarting begin replaces the cookie and leaves
the abandoned record to expire. No native/Flutter token handoff, associated-domain
configuration or Android app-origin verification is implemented or implied.

PostgreSQL stores the exact Ada challenge/policy plus purpose, user/version,
server-selected remember choice, and (for enrollment) the stable session ID and name
whose current password was checked at **begin**. Completion atomically consumes
the record before checking the body, purpose, Origin or session, including invalid,
expired and oversized attempts. A failed finish requires a new begin; cookie
deletion alone is not the replay defense. A wrong browser cannot consume or use
the other browser's unknown cookie-bound record. Session/version validity is
checked again at enrollment commit. The persisted login ceremony deadline is
passed through counter advancement and subsequent session issuance. Both admission
transactions check PostgreSQL `clock_timestamp()` after acquiring locks and again
after writes, before commit; expiration rolls back the transaction. Enrollment
uses the same database-clock checks against the earlier of its ceremony and live
session deadlines. A lock wait cannot extend either lifetime, and no context
timeout alone is relied upon. Logout, disable, password change and key deletion
serialize against enrollment/session admission using database row locks.

Keys have globally unique credential IDs, at most 20 per user, public COSE key
material only, and BIGINT sign counters constrained to the full uint32 range.
Successful assertions compare-and-swap the counter observed during verification;
stable zero is valid for counterless authenticators, while nonzero counters must
strictly advance. Key deletion increments the user version and revokes sessions
in one transaction, preventing stale in-flight login from obtaining a usable
session. Last-used records accepted assertions even if a later concurrent
revocation or ceremony expiration blocks session issuance. The ceremony admission
deadline does not shorten the issued session's eight-hour or 30-day expiry.

Challenge capacity is at most **4096 total**, serialized across replicas.
Authenticated enrollment is limited to **five pending enrollment challenges per
user**; anonymous login records do not count toward that limit. Anonymous login
has **no per-user quota** and is limited to **3840 pending login challenges
globally**, leaving at least 256 slots that anonymous begins cannot allocate.
Consequently, five or more anonymous requests naming a victim cannot exhaust a
per-account login/enrollment quota. Global pool exhaustion and process-rate-limit
pressure can still affect availability; these are not per-account reservations.
Expired rows are pruned during begin and expired submitted records are consumed
during finish. No cleanup goroutine or in-memory ceremony map is used.
Login/enrollment/delete share the existing bounded process-wide authentication
rate limiter; password reauthentication shares the two PBKDF2 slots. Deploy edge
rate limits for internet exposure. Capacity exhaustion is 429; invalid ceremony,
signature, UV or binding is 401; failed HTTP Origin is 403; invalid enrollment
state/duplicate key/deletion conflict is 409. Body/content-type errors are
400/413/415. Infrastructure errors fail closed with generic 503 errors, not raw
cryptographic or SQL details. After an ambiguous commit failure, reload the own-key
list or restart authentication rather than assuming the mutation did not happen.

### Backend Verification

Validated against an isolated PostgreSQL 17 instance with all migrations,
including migration 26, applied under unique test table prefixes:

```sh
AT_TEST_POSTGRES_DSN='postgres://postgres@127.0.0.1:PORT/postgres?sslmode=disable' \
  go test -race ./internal/server ./internal/store/postgres ./internal/config -count=1
go test ./... -run '^$'
```

The full three-package race run and repository-wide compilation pass. The
production BasePath signed enrollment/login test also passed 12 consecutive
race-enabled runs. `native-auth-passkeys_test.go` uses an ES256 software
authenticator with real packed self-attestation and assertion signatures and the
published Ada verifier, not a mocked cryptographic result. It covers replay,
wrong browser/challenge/user/session/origin/RP/purpose, missing UV, bad signatures,
expired/oversized/malformed replies, reauthentication, own-key access, password
fallback/re-enrollment, concurrent signed counters, and cookie/server lifetime
agreement. Store tests cover atomic cross-instance consumption, pool bounds and
pruning, full uint32 CAS/stable-zero counters, global credential uniqueness,
binary IDs containing every byte value, credential limits, and repeated
deletion-versus-counter/session-issuance races. Review regressions cover six
anonymous victim login begins followed by successful own login and enrollment,
anonymous-pool saturation with enrollment headroom, and deterministic expiration
while waiting on counter-user, counter-credential, and second-admission session
issuance locks. A signed HTTP regression also verifies that the same server-held
deadline reaches both admissions and prevents a session cookie when it expires
between them.

The web login includes a default-unchecked remember-me control and a separate
username-first passkey button. Own passkeys can be enrolled, listed and deleted
alongside password management on the Users page, or on the non-admin account
screen. Enrollment/deletion requires the current password; deletion signs the
user out everywhere. Unsupported browser contexts retain password login.

Chrome virtual-authenticator smoke tests exercised enrollment/assertion,
BasePath cookie requests, cancellation, unmount abort and deletion logout with
mocked HTTP responses. UI helper/API tests cover exact binary serialization and
remember-me request contracts. These are separate from the signed PostgreSQL
backend tests, which use AT's real route/middleware assembly through `httptest`.
Physical security keys, Safari/Firefox, mobile platform authenticators and the
combined deployed TLS/reverse-proxy flow remain acceptance checks. No Flutter
client or browser-to-native handoff has been implemented.

## Remaining Scope

External OAuth2/OIDC **user login**, account linking and verified-email policy,
second-factor MFA, unauthenticated password recovery, shared login
abuse controls, fine-grained permissions, resource/organization ownership checks,
audited authorization policy, machine credentials for internal HTTP MCP, Flutter
self-host server selection, mobile handoff/PKCE, and revocation of active streams
are follow-up work. Existing OAuth connectors are external-service credentials,
not native user-login providers. No non-admin tenant isolation is claimed here.

### External Login Blocker (Ada v0.5.1)

External login/provider management is **not implemented or advertised** by this
extension. There are no new external-provider CRUD, identity-linking, browser-login,
callback, or login-options endpoints, and no external-login migration. Existing
connector OAuth endpoints retain their separate external-service-credential role.

Mock-upstream investigation reproduced two issues with published
`github.com/rakunlabs/ada/middleware/auth v0.5.1`:

- An authorization-code flow configured with an OIDC issuer, `openid` scope,
  discovery JWKS, nonce, and PKCE returns an identity from userinfo when the token
  response contains **no ID token**. No ID-token signature, issuer, audience, or
  nonce verification occurs. This can be valid generic OAuth2 userinfo semantics,
  but cannot be advertised as verified OIDC.
- Discovery accepts a document whose `issuer` differs from the requested issuer;
  the strategy adopts that different issuer rather than rejecting discovery.

The relevant Ada code is `strategy/oauth2/oauth2.go` (`fetchClaims`,
`verifyIDToken`, `NewWithContext`) and `strategy/oauth2/discovery.go` (`Discover`).
The published public identity result has no trusted indication that an ID token
was verified, and v0.5.1 has no strict-OIDC option requiring one.

Both issues are fixed in the sibling Ada checkout, with security regressions in
`middleware/auth/strategy/oauth2/strict_test.go`. The new explicit
`Config.RequireIDToken` option requires verified issuer, audience, nonce, expiry
and subject. The patch also rejects critical JWT headers and validates signed
UserInfo issuer/audience and subject consistency while preserving generic OAuth
profile mapping. These changes are not yet published. AT remains on published
v0.5.1 and builds without a local module replacement; do not enable external
login until the patched dependency and AT integration are available.

A published Ada fix/strict-OIDC contract is needed before this integration can
use that strategy as verified OIDC without intercepting protocol internals or
duplicating a verifier. Also review raw upstream error-body logging and flow-cookie-only
replay/expiry behavior. AT must add bounded, server-expiring, atomically consumed,
browser-bound flow records rather than rely on cookie deletion as replay defense.
No local module replacement was added to AT; Pika was not modified.

The remaining external slice still needs encrypted runtime provider CRUD and key
rotation integration, a next additive PostgreSQL migration, immutable provider-ID
plus subject links approved by administrators, safe public login discovery, and
the callback/session race and token-leak tests. It must never automatically link
email addresses, provision users, import upstream roles, retain upstream tokens,
or bypass native session-version issuance. These contracts are intentionally not
promised to the UI before the backend exists.

## Mobile PKCE Handoff V1

This backend contract is implemented independently of external OIDC. It brokers
the existing **local password/passkey browser login**, not a provider token and
not a gateway API key. The browser approval page and Flutter client are separate
integration work; these instructions are their exact wire contract. No UI or
Flutter implementation is included in this backend change.

Migration `28_auth_mobile.sql` adds bounded persisted authorization requests and
`auth_sessions.transport` (`web` or `mobile`). Existing v27 families and their
credentials remain valid and default to `web`; this migration does not force
another logout. Apply migrations using normal AT startup. No new configuration
knob is required: native auth on PostgreSQL enables the mobile descriptor and
endpoints. Keep all replicas on the same configured origin, base path, and DB.

### Instance Discovery

The client selects one HTTPS instance, including any base path, before beginning.
For `server.native_auth.origin: https://at.example.com` and `base_path: /at`, its
stable issuer is `https://at.example.com/at` (no trailing slash). With no base
path, issuer is the origin alone. `Host`, forwarded headers, and client-supplied
return URLs never define the issuer or authorization destination.

`GET <issuer>/auth/status` retains all previous fields and adds this descriptor
only when native mobile auth is available:

```json
{
  "enabled": true,
  "passkeys": true,
  "remember_me": true,
  "passkey_login": "username-first",
  "mobile_auth": {
    "version": 1,
    "issuer": "https://at.example.com/at",
    "callback_uri": "atmobile://auth/callback",
    "code_challenge_methods_supported": ["S256"],
    "request_expires_in": 300,
    "code_expires_in": 60,
    "begin_endpoint": "https://at.example.com/at/auth/mobile/begin",
    "token_endpoint": "https://at.example.com/at/auth/mobile/token",
    "refresh_endpoint": "https://at.example.com/at/auth/mobile/refresh",
    "logout_endpoint": "https://at.example.com/at/auth/mobile/logout",
    "me_endpoint": "https://at.example.com/at/auth/me"
  }
}
```

`passkeys` still reflects actual passkey availability. When native auth is off,
`mobile_auth` is absent, not an empty object. Clients must reject unsupported
versions or an issuer different from their selected canonical instance. Do not
follow token-endpoint redirects to another host or silently adopt a different
issuer from discovery. HTTPS is mandatory outside the existing explicitly enabled
loopback development exception; TLS termination is configured by the operator.

### Begin From Flutter

Generate **independent cryptographically random 32-byte** verifier and state.
Encode each using canonical unpadded base64url: exactly 43 characters. Compute
`code_challenge = base64url_no_padding(SHA256(ASCII(code_verifier)))`. V1 deliberately
requires this canonical 32-byte encoding for verifier and state, rather than the
entire RFC 7636 verifier character/length range. S256 is the only method. AT's
published Ada version has only a private PKCE generator; this broker uses standard
library SHA-256 and checks the RFC 7636 test vector without adding a dependency.

`POST <issuer>/auth/mobile/begin`, `Content-Type: application/json`, **no Cookie or
Authorization header**:

```json
{
  "code_challenge": "<43-character S256 challenge>",
  "code_challenge_method": "S256",
  "state": "<43-character random state>",
  "remember_me": false,
  "device_name": "Ray's phone"
}
```

`remember_me` is optional, default false. `device_name` is optional, default empty,
at most 128 UTF-8 bytes, valid UTF-8 with no control characters. It is an untrusted
display label, not verified device identity. No `redirect_uri`, `return_uri`, user,
session ID, issuer override, arbitrary TTL, or credential is accepted in this body.
Begin never authenticates or assigns a user, even if called from an already logged
in browser. Cookie/Authorization headers are explicitly rejected.

200 response:

```json
{
  "authorization_url": "https://at.example.com/at/#/mobile-authorize?request_id=<43-character-request-id>",
  "expires_in": 300
}
```

Keep `(issuer, state, verifier)` locally as one pending attempt. Validate the
authorization URL belongs to that issuer and open it in the external system
browser, not an embedded credential-collecting WebView. `expires_in` is the
nominal five-minute server transaction lifetime, not permission to extend it
after network delay. The server is authoritative about expiry.

### Browser Approval DTOs

The SPA route is exactly `/#/mobile-authorize?request_id=...` under its base path.
Preserve that route through the existing login gate. Both non-admins and admins
may approve their own identity; do not send non-admins to the management app
instead of showing the approval page. No automatic approval on mount, GET, login,
or passkey completion is permitted. Display the current `/auth/me` identity, the
canonical instance, device label, remember choice, and an explicit Approve/Deny
choice. Escape device text; never render it as HTML or treat it as verified.

`GET <issuer>/auth/mobile/requests/{request_id}` requires a live **web access
cookie**, not mobile bearer. 200 response (all listed fields are present):

```json
{
  "request_id": "<43-character-request-id>",
  "device_name": "Ray's phone",
  "remember_me": false,
  "created_at": "2026-09-07T12:00:00Z",
  "expires_at": "2026-09-07T12:05:00Z",
  "issuer": "https://at.example.com/at",
  "callback_uri": "atmobile://auth/callback"
}
```

No challenge, verifier, code, state, user-version guard, or web-family binding is
returned. A pending request is intentionally unassigned until the user clicks
Approve; the account authenticated on the approval POST is the one authorized.
The UI must invalidate its displayed account on browser identity transitions and
re-read identity/request before offering approval again. There is no client-sent
user ID for an attacker to substitute.

`POST <issuer>/auth/mobile/approve` and `POST <issuer>/auth/mobile/deny` require a
live web access cookie, JSON, and exact configured **Origin**, with body:

```json
{"request_id":"<43-character-request-id>"}
```

Approval requires live access at request admission and a still-live stable web
family under the transaction lock. It does not impose a separate password-age or
step-up requirement. Use the existing web refresh flow on an expired access
cookie, or sign in again, before explicitly approving. Refresh preserves the web
family and does not invalidate an approval. Password changes, user revocation,
disable/enable, passkey deletion, web logout, or family expiry before redemption
prevent the code exchange.

200 approval response:

```json
{"redirect_url":"atmobile://auth/callback?code=<43-character-code>&state=<original-state>"}
```

200 denial response:

```json
{"redirect_url":"atmobile://auth/callback?error=access_denied&state=<original-state>"}
```

Query ordering is unspecified. The browser navigates to this returned URL after
the explicit decision; the API itself does not redirect. The callback is a fixed
provisional allowlist constant, not a runtime/client option. Approval codes contain
32 random bytes, expire in **at most 60 seconds**, and are additionally capped by
both the original request deadline and approving web-family deadline. Denial
deletes the request; approval makes further GET/decision requests unavailable.
The code is persisted only as its SHA-256 hash. No token ever enters these URLs.

### Callback And Exchange

Flutter must match the exact callback scheme `atmobile`, host `auth`, and path
`/callback`, reject duplicate/ambiguous parameters, and compare `state` with the
pending attempt **before** exchanging anything or acting on denial. Reject
unsolicited, expired, mismatched, or already completed callbacks. There is no
`state` parameter on the token endpoint: callback correlation is the client's
responsibility, whereas the server proves possession of the stored PKCE challenge.
Never select an instance or account from callback-supplied data. Custom URI schemes
can be claimed by another app; interception alone cannot redeem without the
verifier. Verified app links would require a separately reviewed callback contract.

`POST <selected issuer>/auth/mobile/token`, JSON, no Cookie/Authorization:

```json
{"code":"<callback-code>","code_verifier":"<locally-held-verifier>"}
```

200 token response, also the exact response shape for mobile refresh:

```json
{
  "token_type": "Bearer",
  "access_token": "<43-character-opaque-access>",
  "refresh_token": "<43-character-opaque-refresh>",
  "access_expires_at": "2026-09-07T12:10:30Z",
  "session_expires_at": "2026-09-07T20:00:30Z",
  "session_id": "<64-character-nonsecret-family-id>",
  "remember_me": false,
  "issuer": "https://at.example.com/at",
  "identity": {
    "subject": "01...",
    "name": "operator@example.com",
    "provider": "local",
    "roles": ["admin"]
  }
}
```

`roles` is omitted for non-admins; nested token-response `identity` has no claims
or timestamps. The top-level timestamps are the authority for this response.
All timestamp fields use RFC3339 with optional fractional seconds; parse as
instants, never as local wall-clock strings. There is no `expires_in`, scope, ID
token, user version, or credential hash in this token response. Check issuer
equality again, then atomically persist the pair and metadata in OS secure storage
namespaced by issuer. Remove the pending verifier/state on completion or rejection.
Do not log callback URLs, request/response bodies, or Authorization headers.

Code consumption and mobile-family issuance are one database transaction with
user -> web family -> request lock order. Exactly one concurrent valid redemption
can succeed. An identified code with a wrong or malformed verifier is consumed
without issuing a family. Invalid/disabled/revoked/expired bindings also consume
the identified record and fail closed. Replays fail; they do not revoke a mobile
family already issued by a successful exchange. Storage failures roll back; a
session-cap refusal also rolls back, allowing a deliberate retry before expiry
after freeing capacity. Do not blindly retry an exchange after losing its response:
start a new handoff if its outcome is unknown.

### Bearer, Refresh, Logout

Send `Authorization: Bearer <mobile access_token>` without any Cookie header to
`GET <issuer>/auth/me` and native management endpoints. Me returns the existing
identity shape documented above, including `expires_at` and nonsecret `claims`.
Only `mobile` access credentials authenticate as bearers; `web` credentials in a
Bearer header, mobile credentials in web cookies, gateway keys, stable family IDs,
refresh credentials, and mixed cookie+bearer requests fail authentication.

Management remains installation-wide **administrator-only**. Non-admin me works
but management returns 403. Mobile login creates no tenant scope. Browser-only
self password/passkey routes and handoff approval reject bearer authentication.
Admin management routes, including `/auth/users`, accept mobile bearers without
Origin; browser cookie writes retain Origin protection. Native mobile tokens do
not authenticate `/gateway/*`; use gateway API keys only on gateway routes.

`POST <issuer>/auth/mobile/refresh`, JSON, no Cookie/Authorization:

```json
{"refresh_token":"<current-mobile-refresh-token>"}
```

The 200 response is the token DTO above. Rotation changes both tokens, not the
family ID, transport, remember choice, user binding, or absolute deadline. Access
lasts at most ten minutes; initial mobile family lifetime uses the same configured
`session_ttl`/`remember_ttl` as web login, beginning at successful redemption. The
first refresh can occur immediately; subsequent rotations are at least five
minutes apart. Retrying a consumed refresh revokes the entire family, even within
that interval. Serialize refresh across every request/isolate for this issuer and
atomically replace stored credentials. Respect 429 `Retry-After`; do not parallel
refresh or automatically replay on ambiguous network failure.

`POST <issuer>/auth/mobile/logout`, JSON, accepts exactly one credential source:

```json
{"refresh_token":"<mobile-refresh-token>"}
```

Alternatively send body `{}` and `Authorization: Bearer <live-mobile-access>`.
Cookie headers are rejected. Providing both nonempty refresh and bearer returns
400 before revoking anything. A consumed refresh still identifies its retained
mobile family. An unknown, expired/cleaned, or wrong-transport refresh is an
idempotent no-op. The refresh body does not accept an access credential as a
revocation credential. Successful logout returns **204 with no body and no
Set-Cookie**, revoking only the selected mobile family. An invalid/expired bearer
returns 401; prefer the refresh-token form when access has expired. Repeated
refresh-token logout returns 204. Browser logout does not revoke already-issued
mobile families; use user-wide revoke for all devices. Mobile refresh/logout never
set or clear browser cookies.

### Mobile Errors And Bounds

All auth responses use `Cache-Control: no-store`. Mobile public endpoints also set
`Referrer-Policy: no-referrer`; approval/denial responses do too. Errors are the
existing UI-compatible **`{"message":"..."}`**, without a `code` field. These are
JSON errors, never login redirects. POST bodies are capped at 4096 bytes, require
`application/json`, and reject unknown fields and trailing JSON. Native begin,
exchange, refresh, and logout each retain a ten-second request deadline.

**Begin-only admission:** each socket peer IP has a per-process bucket of 30
initial requests and one replenished request per second. Invalid begin bodies also
spend this quota. Source ports are ignored and IPv4-mapped IPv6 addresses normalize
to IPv4. Only `RemoteAddr` is trusted, never `X-Forwarded-For`, `X-Real-IP`, or
`Forwarded`. Behind a reverse proxy, clients share that proxy peer's bucket; there
is no trusted-proxy override in this limiter. The source map is capped at 1024
entries. New sources are rejected while it is full; admission of a new source
prunes entries idle for at least five minutes, without evicting live buckets or
resetting their quota. Invalid peer addresses share a single fallback bucket.

Exchange, refresh, and logout do **not** consume begin quota or a shared
unauthenticated rate budget. Exhausting begin admission cannot block valid
credential rotation or revocation. Refresh retains its persisted five-minute
per-family interval and replay revocation; exchange retains one-use codes and
session admission bounds. Body/deadline limits still apply to credential endpoints.
Add reverse-proxy connection/concurrency and abuse controls for deployment-wide
load and invalid-credential floods, with separate begin and credential-operation
budgets so unauthenticated begin traffic cannot exhaust logout/refresh capacity.

**Consent frame protection:** native mode adds `Content-Security-Policy:
frame-ancestors 'none'` and `X-Frame-Options: DENY` to responses from the SPA file
handler, including the base-path root, `index.html` redirects, and SPA fallback
paths such as `/ui`. A separate CSP policy is appended rather than overwriting
existing policies; policies are enforced together, and the added directive does
not restrict scripts, styles, images, or inline resources. Registered gateway
routes and legacy (native-auth-disabled) mode do not receive these headers from
this protection. The consent document cannot be framed, even by the same origin.
Reverse proxies/CDNs must preserve both headers and all CSP policies, including on
cached HTML. If another server serves the SPA, configure equivalent headers there;
API response headers alone cannot protect a separately hosted consent document.

| Endpoint / HTTP | Exact message |
| --- | --- |
| Public mobile 400, credential headers | `native endpoint does not accept browser cookies or unrelated credentials` |
| Begin 400, parameters | `invalid mobile authorization parameters` |
| Details/decision 400, ID syntax | `invalid request_id` |
| Details 404, absent/expired/already decided | `authorization request unavailable` |
| Decision/token 400, invalid binding/expired/consumed/wrong PKCE | `authorization request expired, consumed, or rejected` |
| Token 400, code syntax | `invalid authorization code` |
| Browser-only route 401, bearer supplied | `browser authentication required` |
| Me/protected route/bearer logout 401 | `authentication required` |
| Cookie Origin/Fetch Metadata check 403 | `same-origin request required` |
| Non-admin management 403 | `management access is administrator-only` |
| Refresh 401, malformed/unknown/expired/wrong transport/replay | `refresh rejected; sign in again` |
| Logout 400, two credential sources | `provide bearer or refresh_token, not both` |
| Logout 400, missing/malformed refresh with no bearer | `refresh_token required` |
| JSON 400 or body-cap 413 | `invalid authentication request` |
| Media type 415 | `application/json required` |
| Begin 429, peer quota or source-map capacity | `mobile authentication rate limit exceeded` (`Retry-After: 1`) |
| Begin/token 429, persisted request/session cap | `authentication capacity reached; sign out a device or retry later` |
| Refresh 429, current credential too early | `refresh too early; retry after the indicated delay` (positive integer `Retry-After`) |
| Store/randomness/request timeout 503 | `authentication unavailable` |

The database caps all pending/approved request rows at **1000 across replicas**,
including expired rows not yet swept. Begin inserts serialize on the existing
bootstrap latch but never change its claimed state. The auth janitor removes at
most 500 expired mobile request rows per five-minute sweep using `SKIP LOCKED`,
sharing its bounded sweep context with credential cleanup. Expired requests are
rejected even before deletion. There is no unbounded startup drain. Approved codes
can expire before their request; their rows remain only until consumption or
request cleanup. Request records are not refresh-replay tombstones and cleanup
does not touch refresh replay evidence. Existing family/tombstone bounds and the
20-live-family cap apply jointly to web and mobile sessions.

Regression coverage is in `internal/server/native-auth-mobile_test.go` and
`internal/store/postgres/auth-mobile_test.go`, alongside existing native/passkey/
refresh tests. It exercises real PostgreSQL begin/approval/exchange/me/rotation/
logout, production routing and gateway separation, fixed canonical URLs under a
foreign Host, PKCE consumption, denial, ownership and live web binding, revocation,
concurrent redemption, deadlines after lock waits, transport isolation, bounded
cleanup/session admission, and preservation of v27 web sessions through migration.
