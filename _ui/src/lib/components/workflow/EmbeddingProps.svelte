<script lang="ts">
  let { data = $bindable(), providers = [], workflow }: { data: Record<string, any>; providers?: any[]; workflow?: any } = $props();
</script>

<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Provider</span>
  <select
    bind:value={data.provider}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
  >
    <option value="">Select provider</option>
    {#each providers as p}
      <option value={p.key}>{p.key}</option>
    {/each}
  </select></label>
</div>
<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Model</span>
  <input
    type="text"
    bind:value={data.model}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
    placeholder="text-embedding-3-small"
  /></label>
</div>
<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Input type</span>
    <select
      bind:value={data.input_type}
      class="mt-0.5 w-full px-2 py-1 text-xs border focus:outline-none focus:ring-1 focus:ring-dark-border-subtle border-dark-border bg-dark-surface"
    >
      <option value="">Provider default</option>
      <option value="search_document">Search document</option>
      <option value="search_query">Search query</option>
      <option value="classification">Classification</option>
      <option value="clustering">Clustering</option>
    </select>
  </label>
  <p class="mt-1 text-[10px] text-dark-text-muted">Use matching document/query modes for semantic search.</p>
</div>
<!-- Port descriptions -->
<div class="border-t border-dark-border pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[11px] font-medium text-dark-text-muted">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Text content to generate embeddings for.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">text</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Text to embed (required)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">string | string[]</div>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[11px] font-medium text-dark-text-muted">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Embedding vectors for the input text.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">output</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Embedding vectors</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">{"{ embeddings: number[][], model: string, usage: { total_tokens: number } }"}</div>
      </div>
    </div>
  </div>
</div>
