<script lang="ts">
  import DocsCodeBlock from './DocsCodeBlock.svelte';
  import DocsParamTable from './DocsParamTable.svelte';
  import type { DocsParam } from './docs-types';
  import { errorEnvelopeExample } from './snippets';

  const statuses: DocsParam[] = [
    { name: '400', description: 'Invalid request: a malformed body, an unsupported value (for example n > 1) or a missing field. param names the field.' },
    { name: '401', description: 'Missing, unknown or paused API token.' },
    { name: '403', description: 'The token may not use this provider or model, or your account is blocked from a provider budget.' },
    { name: '404', description: 'Unknown model (model_not_found), a disabled provider, or an unknown path (unknown_endpoint — usually a wrong base URL).' },
    { name: '409', description: 'The provider budget requires pricing for this model and none is configured (provider_pricing_required).' },
    { name: '429', description: 'Rate limited: the token’s spend limit, a provider or user budget, or the provider itself. Respect Retry-After.' },
    { name: '502', description: 'The provider could not be reached, or rejected AT’s own credentials (upstream_auth_failed).' },
    { name: '503 / 5xx', description: 'The provider failed. Retryable — a fallback chain moves on to the next target.' },
  ];
</script>

<div class="docs-prose">
  <p>
    Errors use the OpenAI error envelope, with <code>param</code> set when a single field is at
    fault. <code>/gateway/v1/messages</code> uses the Anthropic envelope instead, so Anthropic
    clients display errors natively.
  </p>
</div>

<DocsCodeBlock code={errorEnvelopeExample} lang="json" label="error body" copyLabel="Copy error example" />

<DocsParamTable params={statuses} nameLabel="Status" showType={false} />

<div class="docs-prose">
  <h3>Getting a web page instead of JSON?</h3>
  <p>
    The base URL is wrong. Clients need <code>…/gateway/v1</code> (OpenAI) or
    <code>…/gateway</code> (Anthropic), including any sub-path AT is deployed under. Requests to
    unknown gateway paths answer <code>404 unknown_endpoint</code> naming the correct base URL.
  </p>
</div>
