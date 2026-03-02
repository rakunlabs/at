<script lang="ts">
  import { listCollections, type RAGCollection } from '@/lib/api/rag';

  let { data }: { data: Record<string, any> } = $props();
  let collections = $state<RAGCollection[]>([]);

  listCollections().then((cols) => {
    collections = cols;
  });
</script>

<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Query</span>
  <textarea
    bind:value={data.query}
    rows={3}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
    placeholder="Search query (supports {'{{.var}}'})"
  ></textarea></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Collections</span>
    <select
      multiple
      bind:value={data.collection_ids}
      class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400 bg-white min-h-[80px]"
    >
      {#each collections as c}
        <option value={c.id}>{c.name}</option>
      {/each}
    </select>
  </label>
  <div class="mt-0.5 text-[10px] text-gray-400">Hold Ctrl/Cmd to select multiple. Empty = search all.</div>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Num Results</span>
  <input
    type="number"
    bind:value={data.num_results}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
    min="1"
    max="20"
    placeholder="5"
  /></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Score Threshold</span>
  <input
    type="number"
    bind:value={data.score_threshold}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
    min="0"
    max="1"
    step="0.01"
    placeholder="0.0"
  /></label>
</div>

<!-- Port descriptions -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Data for query template.">
        <span class="text-[11px] font-mono font-medium text-gray-700">data</span>
        <span class="text-[10px] text-gray-400 ml-1">— Template context</span>
      </div>
      <div title="Dynamic override for query.">
        <span class="text-[11px] font-mono font-medium text-gray-700">query</span>
        <span class="text-[10px] text-gray-400 ml-1">— Override query</span>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Search results.">
        <span class="text-[11px] font-mono font-medium text-gray-700">output</span>
        <span class="text-[10px] text-gray-400 ml-1">— Results</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">
          {"[{ content, metadata, score, collection_id }]"}
        </div>
      </div>
    </div>
  </div>
</div>
