# Personal free-tier providers

AT's **Providers → Free-tier setup and workspace policy** helps a signed-in
account connect its own official API accounts. AT does not supply upstream
credits, create third-party accounts, pool users' secrets, or promise a monthly
token grant. No external catalog subscription or model-list feed is needed.

## Setup

Select providers, follow their account/key links, enter eligible model IDs, and
copy limits from the account console. The catalog contains links, dated review
metadata and privacy/eligibility notes; an unreviewed entry explicitly says to
check current terms. A model hint is not proof of eligibility. The wizard does
not guess a model's price or add zero-price pricing rows.

Each saved provider is personal. The generated **priority** routing profile is
private to the same account and selected workspace. Free targets precede an
optional, explicitly chosen **existing paid fallback**. Quota exhaustion without
a usable fallback returns an error. No implicit paid fallback is added. A paid
account/model selected as a "free" target can still incur charges upstream.

With `tokens.write` and API tokens enabled, the wizard can also create a personal
gateway token restricted to the selected model references, no MCP/webhooks,
expiring after 30 days. Its plaintext is shown once. Without that permission,
use an existing personal token or ask an administrator. A shared workspace token
cannot resolve a private profile or borrow its owner's personal providers.

Setup uses the existing resource APIs sequentially, not a distributed
transaction. If a step fails, saved providers/profile remain. No write is
automatically retried and nothing is automatically deleted. **Continue setup**
checks stable provider keys and reuses completed steps before resuming profile
or token creation; a profile-name conflict can be corrected without recreating
providers. Review **My providers**, the profile and API Tokens first. If a token-create
response was lost, rotate that token rather than expecting to retrieve it.

## Durable quotas

`config.quota` is independent of USD budgets and the existing per-minute limiter:

```json
{
  "requests_per_day": 100,
  "tokens_per_month": 1000000,
  "models": {
    "my-model": {"tokens_per_day": 50000}
  }
}
```

These are illustrative operator limits, not the limits of any provider. All
fields are optional; zero means no local ceiling. A provider-wide pool is shared
by all models, users and virtual aliases that resolve to that physical provider
row. Model pools are additional limits; they do not multiply the account pool.

The database serializes reservations on the physical provider row, including
across replicas. Day/month windows use the database clock and reset in UTC.
Before admission, AT reserves estimated prompt tokens plus the requested maximum
output allowance (32,000 output tokens if omitted); no generation cap is
injected. If that reservation does not fit, use a smaller explicit output
allowance or revise the local ceiling. Reported usage settles the reservation.
If usage is missing, the estimate is retained conservatively. A failed upstream
attempt still consumes one request; rejection by the money budget before the
upstream call refunds the quota admission.

A process crash can leave a reservation charged until window rollover. Late or
duplicate settlements cannot mutate a newer window or charge twice. Old receipts
are pruned on the provider's next reservation. Quota usage shown in the editor
distinguishes settled tokens from reserved tokens. Opaque passthrough/media
requests without a token estimate cannot use a token ceiling; use request
ceilings instead. Opaque calls with an unresolved model are refused when model
subpools exist. With token ceilings, `extra_body` is refused because it can
overwrite input/output fields after admission. With request-only ceilings it
may not override the model. Conflicting output-limit fields reserve the larger
positive allowance, independent of an adapter's precedence.

At exhaustion AT returns **429 `provider_quota_exceeded`** with `reset_at` and
`Retry-After`. A configured fallback chain advances without calling the
exhausted upstream. Local quotas do not trip the outage circuit breaker.

**This is not an upstream balance or a no-charge guarantee.** Calls outside AT
are invisible. Two provider records using keys from the same upstream account
are not automatically merged; use one physical AT provider record for a shared
upstream pool. Tokenization, cache billing and actual output can differ from
estimates. Disable upstream automatic top-up/paid usage where available.

## Workspace policy

`GET/PUT /api/v1/personal-providers/policy` reads/updates the selected workspace's
policy. Reads require workspace access; writes require `workspace.write`, use
version CAS and bump the execution policy version. Defaults preserve existing
behaviour:

* `block_free_tier: false`: allow catalog setup unless other admission refuses it.
* `block_free_tier: true`: refuse marked personal providers and known catalog
  endpoint hosts, even when the caller removes the catalog marker. This also
  conservatively blocks paid accounts on those same hosts.
* `allowed_personal_hosts: null`: no host allowlist.
* `allowed_personal_hosts: []`: refuse every personal endpoint.
* A nonempty list permits only exact named HTTPS hosts, without proxies,
  insecure TLS or URL credentials. No wildcard/suffix matching.

Policy is checked on personal create/update and every scoped runtime resolution;
blocked providers disappear from the selected-workspace model catalog. Existing
credentials are retained, so allowing them again restores access. One workspace
cannot change another's policy. The permission bundle remains the way to deny
`personal_providers.manage` altogether.

This is not DLP, DNS pinning or a network sandbox. Arbitrary host tools,
browser-local providers, approved endpoint relays and server/operator network
access need their own controls. It does not disable administrator-configured
workspace providers. Any shared use must be checked against the provider's
current plan, API and model terms; "free" alone neither permits nor prohibits
sharing.

## Existing aggregators

Self-hosted FreeLLMAPI or OmniRoute can be added as a custom OpenAI-compatible
personal provider using that installation's base URL and unified key. AT sees
aggregate usage, not that aggregator's underlying provider quota state. In Chats,
a browser-local provider can reach an aggregator on the user's own machine;
server-side quotas and this personal-provider policy do not govern those calls.

## Implementation and tests

Migrations 108–110 add quota counters/receipts, personal routing-profile ownership
and workspace provider-use policy. Account deletion sweeps personal profiles;
provider deletion cascades quota rows. Workspace deletion removes its policy and
profiles. No existing provider or profile changes ownership on upgrade.

Regression tests: `provider-quota_test.go`, `free-tier_test.go` in the store and
server, `internal/service/free-tier_test.go`, `_ui/tests/free-tier.test.mjs`.
