<script lang="ts">
  import DocsCodeBlock from './DocsCodeBlock.svelte';
  import DocsCallout from './DocsCallout.svelte';
  import DocsParamTable from './DocsParamTable.svelte';
  import type { DocsParam } from './docs-types';

  const headers = `Authorization: Bearer at_your_token_here
# or, for Anthropic-native clients:
x-api-key: at_your_token_here`;

  const controls: DocsParam[] = [
    {
      name: 'Ownership',
      description:
        'Workspace tokens belong to the workspace. Personal tokens belong to you and can also use your personal providers; their spend counts against your own allowance.',
    },
    {
      name: 'Provider / model access',
      description:
        'Restrict a token to chosen providers or models. Requests for anything else fail with 403 even through a routing profile.',
    },
    {
      name: 'Spend limit',
      description:
        'A cost ceiling over a period. Once reached, requests answer 429 rate_limit_exceeded until the period resets.',
    },
    {
      name: 'Pause',
      description:
        'Stops new requests immediately (401) without deleting the token. Requests already in flight finish.',
    },
    {
      name: 'Rotate',
      description:
        'Issues a new secret in place. The old secret stops working at once; restrictions, limits and usage history are kept.',
    },
  ];
</script>

<div class="docs-prose">
  <p>
    Every gateway request carries an API token. Send it as a bearer token, or as
    <code>x-api-key</code> for clients that only speak the Anthropic convention:
  </p>
</div>

<DocsCodeBlock code={headers} lang="http" label="request headers" copyLabel="Copy authorization header" />

<div class="docs-prose">
  <p>
    Create and manage tokens on <a href="#/settings/tokens">Settings → API tokens</a>. Each token
    has the following controls:
  </p>
</div>

<DocsParamTable params={controls} nameLabel="Control" showType={false} />

<DocsCallout tone="warning" title="Secrets are shown once">
  <p>
    AT stores only a hash of each token. If a secret is lost, rotate the token instead of creating
    a new one — that keeps its usage history and every reference to it.
  </p>
</DocsCallout>

<div class="docs-prose">
  <p>
    <code>GET /gateway/v1/health</code> is the only gateway route that needs no token. Public MCP
    servers can also be called anonymously; see <a href="#/docs?section=mcp-configuration">MCP servers</a>.
  </p>
</div>
