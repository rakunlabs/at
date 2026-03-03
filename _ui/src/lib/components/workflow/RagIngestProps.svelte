<script lang="ts">
  import { listCollections, type RAGCollection } from '@/lib/api/rag';

  let { data }: { data: Record<string, any> } = $props();
  let collections = $state<RAGCollection[]>([]);

  // Load collections on mount
  listCollections().then((res) => {
    collections = res.data || [];
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

<!-- Description -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Description</span>
    <p class="mt-1 text-[11px] text-gray-500 leading-relaxed">
      Ingests files into a RAG collection for semantic search. Deletes stale chunks for modified/deleted files before re-ingesting. Updates a sync-state variable so the next run only processes new changes.
    </p>
  </div>
</div>

<!-- Port descriptions -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Output from a git_diff node (or any source producing the same shape).">
        <span class="text-[11px] font-mono font-medium text-gray-700">data</span>
        <span class="text-[10px] text-gray-400 ml-1">— From git_diff</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">
          {"{ files: [{path, content, status}], deleted_files: [string], commit_sha, repo_url, variable_key }"}
        </div>
      </div>
    </div>
  </div>

  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Input Fields</span>
    <div class="mt-1 space-y-1.5 text-[10px] text-gray-500">
      <div>
        <span class="font-mono font-medium text-gray-700">files</span>
        <span class="text-gray-400 ml-1">— Changed/added files to ingest</span>
        <div class="font-mono text-gray-400 ml-2 mt-0.5">[{"{ path, content, status }"}]</div>
      </div>
      <div>
        <span class="font-mono font-medium text-gray-700">deleted_files</span>
        <span class="text-gray-400 ml-1">— Files removed since last sync</span>
        <div class="font-mono text-gray-400 ml-2 mt-0.5">[string] — stale chunks are deleted</div>
      </div>
      <div>
        <span class="font-mono font-medium text-gray-700">commit_sha</span>
        <span class="text-gray-400 ml-1">— HEAD commit SHA, saved to variable_key</span>
      </div>
      <div>
        <span class="font-mono font-medium text-gray-700">repo_url</span>
        <span class="text-gray-400 ml-1">— Repo URL, used as source prefix for chunks</span>
      </div>
      <div>
        <span class="font-mono font-medium text-gray-700">variable_key</span>
        <span class="text-gray-400 ml-1">— Variable to update with commit_sha after sync</span>
      </div>
    </div>
  </div>

  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Ingestion statistics.">
        <span class="text-[11px] font-mono font-medium text-gray-700">output</span>
        <span class="text-[10px] text-gray-400 ml-1">— Ingestion stats</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">
          {"{ chunks_added, files_processed, deleted_count }"}
        </div>
      </div>
    </div>
  </div>
</div>
