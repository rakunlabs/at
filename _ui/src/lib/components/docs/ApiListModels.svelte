<script lang="ts">
  import DocsCodeBlock from './DocsCodeBlock.svelte';
  import { curlModelsExample } from './snippets';

  interface Props {
    baseUrl: string;
  }

  let { baseUrl }: Props = $props();

  const code = $derived(curlModelsExample(baseUrl));
</script>

<p class="text-sm leading-relaxed text-gray-600 dark:text-dark-text-secondary">
  Enumerate every model this gateway exposes, in the OpenAI
  <code
    class="font-mono bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 text-[12px] text-gray-800 dark:text-dark-text-secondary"
    >/v1/models</code
  > shape. Chat and embedding models are both listed.
</p>

<DocsCodeBlock {code} lang="bash" label="curl" copyLabel="Copy list-models request" />
