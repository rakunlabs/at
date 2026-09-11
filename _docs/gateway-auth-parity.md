# Gateway and authentication parity review

Reviewed 2026-09-10 against the AT checkout, `../pika`, and LiteLLM's public
documentation. This is a gateway/product comparison, not a claim that every
provider/model/parameter combination has been exercised against live services.

## LiteLLM vs AT

| Area | AT today | Gap / next step |
| --- | --- | --- |
| Provider coverage | Nine provider types: OpenAI-compatible, Azure, Anthropic, Bedrock, Vertex, Vertex-Gemini, Gemini, Cohere, MiniMax, as defined in `SupportedProviderTypes`. OpenAI-compatible services share an adapter. | LiteLLM advertises 100+ LLM integrations; adapter counts are not comparable to the number of usable services. Add native adapters in response to required provider capabilities. |
| OpenAI-style endpoints | Chat Completions, Responses, embeddings, image generation, speech, transcription, moderation, model discovery; also rerank and native passthrough. | Responses is a translated subset, including no `previous_response_id` state. Full Files/Batch/fine-tuning/Realtime API parity is not established by the current gateway routes. |
| Request translation | Tool choice, structured-output translation, reasoning options, streaming usage, provider-native `extra_body`. | Semantics remain adapter-dependent; Anthropic JSON instructions are not strict schema enforcement. Token-array embeddings are not supported by the gateway's string/string-array input contract. |
| Retry | Bounded three-attempt Chat Completions/Responses retry; stream-open retry for Chat Completions. | This review adds typed `UpstreamError` 408/425/429/5xx retry alongside `RateLimitError`. Embeddings/media do not automatically inherit this helper. No configurable LiteLLM-style per-error retry policy yet. |
| Fallback | Opt-in `at_fallbacks`, model access checks, shared request timeout, used-model header for synchronous chat/responses. | No automatic streaming fallback, context-window-specific fallback policy, or deployment cooldown router. Cancellation now stops the chain. |
| Routing / load balancing | Explicit `provider/model` selection. | No LiteLLM-style model group with multiple deployments, weighted/least-busy routing, health cooldown and failover policy. This is the largest operational routing gap. |
| Virtual keys / budgets | Gateway API keys, expiry, provider/model/MCP/webhook allowlists, token and spend limits, reset windows, cost tracking. | Provider RPM/input-TPM/concurrency limits are process-local. No equivalent per-key/team distributed RPM/TPM admission model or human team ownership in `APIToken`. |
| Caching | Provider prompt caching and five-minute in-process idempotency response replay. | These are distinct from a general response/semantic cache; no shared multi-replica response cache is provided here. |
| Observability | Trace/observation DB, tool hierarchy, costs, OTEL export, retention, UI. | Not a drop-in implementation of LiteLLM's integration catalog. |
| Guardrails | Provider-native settings and workflow/tool composition. | No common gateway pre/post-call PII/DLP/guardrail policy layer comparable to LiteLLM's guardrail integrations. |
| Agents / MCP | DAG workflows, agent delegation, MCP gateway, scoped gateway tokens. | Strong existing AT functionality; it does not replace missing gateway routing or human authorization. |

Implementation references: `internal/server/gateway*.go`,
`internal/server/translate.go`, `internal/service/types-token.go`,
`internal/service/types-llm.go`, `internal/service/llm/`,
`internal/service/ratelimit/limiter.go`.

### Reliability changes in this review

- Retry wrapped typed upstream 408/425/429 and 500–599 failures using the
  existing attempt count, backoff floor, and per-provider wait cap.
- Preserve immediate failure for ordinary 400/401/403/404/422 errors and untyped
  errors; do not infer retry eligibility from error-message text.
- Check cancellation before calling a provider and stop during backoff. A dead
  request does not start the next fallback target. A provider-local error may
  still trigger fallback while the enclosing request remains alive.
- Round `Retry-After` upward to whole seconds, so 1.5 seconds becomes `2`, not `1`.
- Stream retries apply only to opening the upstream connection, never to replaying
  a partially emitted response. Transient failures can now wait through the
  existing default 10s/20s backoffs before falling back; `timeout_ms` still bounds
  the entire request.

## Pika vs AT authentication

**The Pika-inspired AT authentication foundation already exists.** See
[`NATIVE_AUTH.md`](../NATIVE_AUTH.md) for activation and endpoint contracts, and
[`native-user-auth/tasks.md`](../openspec/changes/native-user-auth/tasks.md) for
the existing implementation backlog.

| Capability | Pika | AT |
| --- | --- | --- |
| Local credentials | Local login and bcrypt password storage | Local login and Ada PBKDF2 password storage; operator-token transactional first-admin bootstrap |
| User administration | Users, disable, session controls | User listing/creation, disable/re-enable, admin password reset, self password change, session revocation |
| Browser sessions | Database-backed issuer/session integration | Hashed opaque credentials; short access lifetime, rotating refresh families, replay revocation, remember-me |
| Passkeys | Passkey strategy and account security controls | Username-first login and password-reauthenticated enrollment/removal; real-authenticator/browser verification remains on the backlog |
| TOTP | TOTP and MFA strategy | No TOTP flow in the current native-auth implementation |
| External identity | OAuth2/OIDC and header strategies; identity linking | Legacy forward-auth mode is available, but cannot be combined with native auth. Connector OAuth is for external-service credentials, not human SSO. Native external login/linking remains pending. |
| Authorization | Capability mappings, provider-qualified permissions, token capability controls and per-user deny overlay | Native management access is administrator-only. Non-admin accounts do not have general management access. Agent organizations are not human authorization scopes. |
| Runtime configuration | Stored auth settings/strategies | Native auth is opt-in bootstrap configuration under `server.native_auth` |
| Mobile | Not evaluated in this review | Device approval, PKCE, mobile bearer access/refresh and Flutter integration already present; real-device verification remains pending |

Pika references: `internal/service/settings-auth.go`,
`internal/service/auth.go`, `internal/server/authx/{oauth2,header,passkey,totp,caps,caps_token,login_guard}.go`.
AT references: `internal/server/native-auth*.go`,
`internal/service/types-auth.go`, `internal/store/postgres/auth*.go`.

### Recommended implementation order

1. **Routing:** introduce an explicit model-group/deployment contract and store,
   then load balancing, cooldown, fallback access checks, and per-attempt traces.
2. **Human authorization:** define capabilities, membership and resource ownership;
   enforce them in resource queries, files/media and tool/agent execution before
   granting ordinary users application access.
3. **External login:** add encrypted provider settings, issuer/subject-bound identity
   links and explicit account linking. Follow-up inspection confirmed that published
   Ada auth v0.5.3 is already pinned and its `TestRequireIDToken*` tests pass; the
   old release prerequisite is resolved.
4. **Operational parity:** distributed per-key/team quotas and budget admission;
   add caching, guardrails and further endpoints according to actual consumers.
5. **Account security:** TOTP/recovery policy and physical-device/browser validation.

This review does not enable native auth in deployment configuration or implement
the remaining routing, SSO or resource-authorization phases.

Follow-up design: [`workspace-identity-access`](../openspec/changes/workspace-identity-access/proposal.md)
records the user's choices of separate human workspaces and invitation/admin approval,
with authorization, external identity, TOTP and recovery specs and implementation tasks.

## Verification

- Passed: `go test -race ./internal/server ./internal/service/llm/... ./internal/config ./internal/store/postgres/...`.
- Added regression coverage for wrapped transient/non-transient errors,
  recovery/exhaustion, cancellation before/during calls and during backoff, and
  fractional `Retry-After` values.
- PostgreSQL-dependent tests skipped because the test database at localhost:5432
  was unavailable; the successful package command is not evidence of DB integration
  verification. No live provider, external IdP, browser or mobile-device test was run.

## External references

- [LiteLLM overview](https://docs.litellm.ai/docs/)
- [Reliability: retries and fallbacks](https://docs.litellm.ai/docs/completion/reliable_completions)
- [Routing and load balancing](https://docs.litellm.ai/docs/routing-load-balancing)
- [Supported endpoints](https://docs.litellm.ai/docs/supported_endpoints)
