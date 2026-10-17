<script lang="ts">
  import DocsCallout from './DocsCallout.svelte';
  import DocsCodeBlock from './DocsCodeBlock.svelte';

  interface Props {
    model: string;
    fallbackModel: string;
  }

  let { model, fallbackModel }: Props = $props();

  const requestBody = $derived(
    JSON.stringify(
      {
        model,
        at_fallbacks: [fallbackModel],
        messages: [{ role: 'user', content: 'Hello!' }],
      },
      null,
      2,
    ),
  );
</script>

<div class="docs-prose">
  <p>AT decides which provider serves a request in three layers, from most to least explicit.</p>

  <h3>1. Per-request fallbacks</h3>
  <p>
    <code>at_fallbacks</code> lists models to try in order when the previous one fails with a
    <strong>retryable</strong> error: 429, 529, any 5xx, a timeout, or a provider/user budget being
    exhausted. Other 4xx errors — a bad request, a missing model — are returned immediately, because
    another provider would reject the same request.
  </p>
</div>

<DocsCodeBlock code={requestBody} lang="json" label="request body" copyLabel="Copy request body" />

<div class="docs-prose">
  <h3>2. Routing profiles</h3>
  <p>
    Many clients cannot add fields to the request body. A
    <a href="#/routing-profiles">routing profile</a> is a stored name bound to an ordered list of
    <code>provider/model</code> targets. A request whose <code>model</code> has no slash and no
    <code>at_fallbacks</code> is expanded to that chain. Profiles appear in
    <code>/gateway/v1/models</code> so they show up in client model pickers.
  </p>
  <ul>
    <li>A profile grants routing, never access: each target still passes the token’s model restrictions, and targets it may not use are skipped.</li>
    <li>The profile that served the request is reported in <code>x-at-routing-profile</code>.</li>
  </ul>
  <p>A profile’s <strong>strategy</strong> decides which target is tried first. The rest stay in the chain as fallbacks.</p>
  <ul>
    <li><code>priority</code>: the listed order (the default).</li>
    <li><code>round_robin</code>: rotates the first target on each request.</li>
    <li><code>weighted</code>: picks the order at random by weight. A weight of 0 makes a target fallback-only.</li>
    <li><code>least_cost</code>: cheapest priced model first. Models without a price follow in listed order.</li>
    <li><code>sticky</code>: keeps one session (<code>x-at-session-id</code>, <code>x-claude-code-session-id</code>, …) or, without one, one API token on the same target, so the provider’s prompt cache stays warm.</li>
  </ul>

  <h3>3. Provider cooldown and circuit breaker</h3>
  <p>
    When a provider reports an empty rate-limit bucket (via <code>Retry-After</code> or the
    Anthropic/OpenAI rate-limit headers), AT moves it to the back of every chain until the reset
    time. After five consecutive outage errors (5xx, upstream timeouts, refused connections) its
    circuit opens and it is moved back for 30 seconds, doubling up to 5 minutes while it keeps
    failing. The first request after that window is the probe, and one success closes the circuit.
    A provider is never removed: a chain whose targets are all cooling or open is still attempted.
  </p>

  <h3>Streaming</h3>
  <p>
    Streaming requests fall back too, up to the first byte. If a target fails before its stream
    opens, the next one is tried transparently. Once the first chunk has been sent, a failure is
    reported on the open stream instead.
  </p>
</div>

<DocsCallout>
  <p>
    <code>x-at-model-used</code> always names the model that actually answered, so a client can
    tell whether a fallback happened. <code>x-at-route-decision</code> explains the choice, for example
    <code>profile=stack; strategy=weighted; served=b/m; attempt=2/3; order=a/m,b/m,c/m; deferred=c/m:circuit_open</code>.
    The entries of <code>order</code> before <code>attempt</code> failed. <code>deferred</code> lists targets
    moved back because of a <code>cooldown</code> or an open circuit (<code>circuit_open</code>).
  </p>
</DocsCallout>
