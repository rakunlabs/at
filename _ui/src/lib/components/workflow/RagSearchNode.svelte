<script lang="ts">
  import { Handle, HandleGroup, type NodeProps } from 'kaykay';
  import NodePreview from './NodePreview.svelte';
  import { nodeRunStates } from '@/lib/store/workflow-run.svelte';

  interface RagSearchData {
    label?: string;
    query?: string;
    node_number?: number;
  }

  let { id, data, selected }: NodeProps<RagSearchData> = $props();
  let runState = $derived(nodeRunStates[id]);

  let previewQuery = $derived(() => {
    if (!data.query) return '';
    const maxLen = 30;
    return data.query.length > maxLen ? data.query.slice(0, maxLen) + '...' : data.query;
  });
</script>

<div
  class={[
    'bg-white border border-gray-300 rounded-md min-w-45 max-w-60 text-xs shadow-sm select-none',
    selected && 'border-blue-500 ring-2 ring-blue-500/25'
  ]}
>
  <Handle id="query" type="input" port="text" accept={['text', 'data']} position="left" label="query" />
  
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-emerald-50">
    <span class="inline-flex items-center leading-none text-[9px] font-bold px-1 py-1 rounded bg-emerald-600 text-white tracking-wide">RAG</span>
    <span class="text-gray-900">{data.label || 'RAG Search'}</span>
    {#if data.node_number != null}<span class="text-[9px] font-medium text-gray-400 ml-auto">#{data.node_number}</span>{/if}
  </div>
  <div class="px-2.5 py-1.5">
    {#if data.query}
      <div class="font-mono text-[10px] text-gray-500 whitespace-pre-wrap break-all leading-snug">{previewQuery()}</div>
    {:else}
      <div class="text-gray-400 text-[11px]">Query RAG collection</div>
    {/if}
  </div>
  <NodePreview state={runState} />
  <HandleGroup position="right" class="!gap-1">
    <Handle id="results" type="output" port="data" label="results" />
    <Handle id="text" type="output" port="text" label="text" />
  </HandleGroup>
</div>
