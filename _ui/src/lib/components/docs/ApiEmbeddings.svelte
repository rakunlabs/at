<script lang="ts">
  import DocsCodeBlock from './DocsCodeBlock.svelte';

  interface Props {
    baseUrl: string;
    model: string;
  }

  let { baseUrl, model }: Props = $props();

  const code = $derived(`curl ${baseUrl}/gateway/v1/embeddings \\
  -H "Authorization: Bearer at_your_token_here" \\
  -H "Content-Type: application/json" \\
  -d '${JSON.stringify({
    model,
    input: ['First document', 'Second document'],
    input_type: 'search_document',
    encoding_format: 'base64',
  }, null, 2)}'`);
</script>

<div class="space-y-4 text-sm leading-relaxed text-dark-text-secondary">
  <p>
    Create one or many embeddings in a synchronous request. Results retain the input order and expose the
    original position in each <code class="font-mono bg-dark-elevated px-1 py-0.5 text-xs">index</code>.
  </p>

  <DocsCodeBlock {code} lang="bash" label="Batch embeddings" copyLabel="Copy embeddings request" />

  <div class="border border-dark-border p-4 space-y-2">
    <h3 class="font-medium text-dark-text">Options and limits</h3>
    <ul class="list-disc pl-5 space-y-1">
      <li><code class="font-mono text-xs">input</code> accepts a string or an array of strings. Token-ID arrays are not supported.</li>
      <li><code class="font-mono text-xs">input_type</code> is an AT extension: <code class="font-mono text-xs">search_document</code>, <code class="font-mono text-xs">search_query</code>, <code class="font-mono text-xs">classification</code>, or <code class="font-mono text-xs">clustering</code>.</li>
      <li><code class="font-mono text-xs">dimensions</code> is forwarded when the model supports shortened vectors.</li>
      <li><code class="font-mono text-xs">encoding_format: base64</code> substantially reduces large response payloads.</li>
      <li>A provider may define an optional maximum input count. Blank means AT adds no batch limit; upstream model and API limits still apply.</li>
      <li>Cohere batches larger than 96 texts are split into ordered upstream calls. A failed chunk fails the whole request, although completed chunks may already have incurred usage.</li>
    </ul>
  </div>

  <p>
    This endpoint is real-time batching, not an asynchronous batch job. When upstream usage is unavailable,
    AT returns an estimate and marks the response with
    <code class="font-mono bg-dark-elevated px-1 py-0.5 text-xs">at_usage_estimated: true</code>.
  </p>
</div>
