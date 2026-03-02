<script lang="ts">
  import { listCollections, type RAGCollection } from '@/lib/api/rag';

  let { data }: { data: Record<string, any> } = $props();
  let collections = $state<RAGCollection[]>([]);

  // Load collections on mount
  listCollections().then((cols) => {
    collections = cols;
  });
</script>

<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Collection</span>
    <select
      bind:value={data.collection_id}
      class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400 bg-white"
    >
      <option value="">Select a collection...</option>
      {#each collections as c}
        <option value={c.id}>{c.name}</option>
      {/each}
    </select>
  </label>
</div>

<!-- Port descriptions -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Files output from git_fetch.">
        <span class="text-[11px] font-mono font-medium text-gray-700">data</span>
        <span class="text-[10px] text-gray-400 ml-1">— Files to ingest</span>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Ingestion stats.">
        <span class="text-[11px] font-mono font-medium text-gray-700">output</span>
        <span class="text-[10px] text-gray-400 ml-1">— Stats</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">
          {"{ chunks_added: 10, files_processed: 2, deleted_count: 0 }"}
        </div>
      </div>
    </div>
  </div>
</div>
