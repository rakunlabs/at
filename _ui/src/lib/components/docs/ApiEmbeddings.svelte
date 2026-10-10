<script lang="ts">
  import DocsCodeBlock from './DocsCodeBlock.svelte';
  import DocsEndpoint from './DocsEndpoint.svelte';
  import DocsParamTable from './DocsParamTable.svelte';
  import DocsCallout from './DocsCallout.svelte';
  import type { DocsParam } from './docs-types';

  interface Props {
    baseUrl: string;
    model: string;
  }

  let { baseUrl, model }: Props = $props();

  const code = $derived(`curl ${baseUrl}/gateway/v1/embeddings \\
  -H "Authorization: Bearer at_your_token_here" \\
  -H "Content-Type: application/json" \\
  -d '${JSON.stringify(
    {
      model,
      input: ['First document', 'Second document'],
      input_type: 'search_document',
      encoding_format: 'base64',
    },
    null,
    2,
  )}'`);

  const params: DocsParam[] = [
    { name: 'model', type: 'string', required: true, description: 'An embedding model, as provider/model.' },
    { name: 'input', type: 'string | string[]', required: true, description: 'One text or a batch. Token-id arrays are not supported.' },
    { name: 'input_type', type: 'string', description: 'AT extension: search_document, search_query, classification or clustering. Used by providers that distinguish them.' },
    { name: 'dimensions', type: 'integer', description: 'Forwarded when the model supports shortened vectors.' },
    { name: 'encoding_format', type: 'string', description: 'float (default) or base64. base64 makes large responses much smaller.' },
  ];
</script>

<DocsEndpoint method="POST" path="/gateway/v1/embeddings" {baseUrl} />

<div class="docs-prose">
  <p>
    Create one or many embeddings in a single synchronous request. Results keep the input order and
    report each original position in <code>index</code>. Backed by OpenAI, Cohere and Gemini
    providers.
  </p>
</div>

<DocsParamTable {params} />

<DocsCodeBlock {code} lang="bash" label="Batch embeddings" copyLabel="Copy embeddings request" />

<div class="docs-prose">
  <h3>Limits</h3>
  <ul>
    <li>A provider may set a maximum batch size. Without one AT adds no limit; upstream limits still apply.</li>
    <li>Cohere batches over 96 texts are split into ordered upstream calls. One failed chunk fails the whole request, although completed chunks may already have been billed.</li>
  </ul>
</div>

<DocsCallout>
  <p>
    This is real-time batching, not an asynchronous batch job. When a provider reports no usage, AT
    estimates it and marks the response with <code>at_usage_estimated: true</code>.
  </p>
</DocsCallout>
