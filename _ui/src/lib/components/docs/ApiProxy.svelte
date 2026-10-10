<script lang="ts">
  import { ArrowDown } from 'lucide-svelte';
  import DocsCodeBlock from './DocsCodeBlock.svelte';
  import DocsEndpoint from './DocsEndpoint.svelte';
  import DocsCallout from './DocsCallout.svelte';
  import { curlProxyExample } from './snippets';

  interface Props {
    baseUrl: string;
  }

  let { baseUrl }: Props = $props();
</script>

<DocsEndpoint method="ANY" path="/gateway/v1/providers/{'{provider}'}/{'{path}'}" {baseUrl} />

<div class="docs-prose">
  <p>
    Reach a provider’s own API when the OpenAI shape does not cover what you need — Gemini file
    search, Bedrock-specific operations, Cohere internals. AT authenticates your token, checks the
    token’s provider access, injects the provider’s stored credentials and forwards the request and
    response unchanged. Your browser cookies are never forwarded.
  </p>
</div>

<div class="border border-dark-border px-3 py-2.5 font-mono text-[12px]">
  <p class="break-all text-dark-text">GET {baseUrl}/gateway/v1/providers/gemini/v1beta/models</p>
  <p class="my-1.5 flex items-center gap-1.5 text-[11px] text-dark-text-muted">
    <ArrowDown size={12} aria-hidden="true" />
    forwarded as
  </p>
  <p class="break-all text-dark-text-secondary">GET https://generativelanguage.googleapis.com/v1beta/models</p>
</div>

<DocsCodeBlock code={curlProxyExample(baseUrl)} lang="bash" label="curl" copyLabel="Copy passthrough request" />

<DocsCallout>
  <p>
    Passthrough calls are recorded in Traces and count toward token spend limits. When the response
    format is recognised, its token usage is recorded; otherwise the call is logged with zero
    tokens rather than an estimate.
  </p>
</DocsCallout>

<div class="docs-prose">
  <p>
    The older unversioned path <code>/gateway/proxy/{'{provider}'}/…</code> still works and behaves
    identically.
  </p>
</div>
