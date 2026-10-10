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

  <h3>3. Provider cooldown</h3>
  <p>
    When a provider reports an empty rate-limit bucket (via <code>Retry-After</code> or the
    Anthropic/OpenAI rate-limit headers), AT moves it to the back of every chain until the reset
    time. It is never removed: a chain whose targets are all cooling is still attempted.
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
    tell whether a fallback happened.
  </p>
</DocsCallout>
