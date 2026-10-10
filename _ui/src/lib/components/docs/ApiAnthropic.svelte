<script lang="ts">
  import DocsCodeTabs from './DocsCodeTabs.svelte';
  import DocsCallout from './DocsCallout.svelte';
  import DocsEndpoint from './DocsEndpoint.svelte';
  import { anthropicPythonExample, claudeCodeEnv } from './snippets';

  interface Props {
    baseUrl: string;
    model: string;
  }

  let { baseUrl, model }: Props = $props();

  let active = $state('claude-code');

  const tabs = $derived([
    { id: 'claude-code', label: 'Claude Code', lang: 'bash', code: claudeCodeEnv(baseUrl, model) },
    { id: 'python', label: 'Python SDK', lang: 'python', code: anthropicPythonExample(baseUrl, model) },
  ]);
</script>

<DocsEndpoint method="POST" path="/gateway/v1/messages" {baseUrl} />

<div class="docs-prose">
  <p>
    The native Anthropic Messages API, sync and streaming. Anthropic-only clients — Claude Code,
    Cline, Roo, Kilo and the Anthropic SDKs — need nothing but a base URL change to get AT’s
    routing, fallbacks, budgets and tracing. The request can still be served by any provider: AT
    translates between the Anthropic and OpenAI shapes when the target is not Anthropic.
  </p>
  <p>
    Set the base URL to <code>{baseUrl}/gateway</code> (without <code>/v1</code>; the client
    appends it). Both <code>x-api-key</code> and <code>Authorization: Bearer</code> are accepted.
  </p>
</div>

<DocsCodeTabs {tabs} bind:active name="anthropic" />

<DocsCallout title="Fixed model names">
  <p>
    Claude Code sends names like <code>claude-sonnet-4-5</code> that contain no provider key.
    Create a <a href="#/routing-profiles">routing profile</a> with exactly that name, pointing at
    one or more <code>provider/model</code> targets, and every request for it is routed — with
    fallback — without changing the client.
  </p>
</DocsCallout>

<div class="docs-prose">
  <p>Errors on this endpoint use the Anthropic error envelope, so the client reports them natively.</p>
</div>
