<script lang="ts">
  import { Handle, HandleGroup, type NodeProps } from 'kaykay';
  import NodePreview from './NodePreview.svelte';
  import { nodeRunStates } from '@/lib/store/workflow-run.svelte';

  interface EmbeddingData {
    label?: string;
    provider?: string;
    model?: string;
    node_number?: number;
  }

  let { id, data, selected }: NodeProps<EmbeddingData> = $props();
  let runState = $derived(nodeRunStates[id]);
</script>

<div
  class={[
    'bg-white border border-gray-300 rounded-md min-w-45 max-w-60 text-xs shadow-sm select-none',
    selected && 'border-blue-500 ring-2 ring-blue-500/25'
  ]}
>
  <Handle id="input" type="input" port="text" accept={['text', 'data']} position="left" label="input" />
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-teal-50">
    <span class="inline-flex items-center leading-none text-[9px] font-bold px-1 py-1 rounded bg-teal-600 text-white tracking-wide">EMB</span>
    <span class="text-gray-900">{data.label || 'Embedding'}</span>
    {#if data.node_number != null}<span class="text-[9px] font-medium text-gray-400 ml-auto">#{data.node_number}</span>{/if}
  </div>
  <div class="px-2.5 py-1.5">
    {#if data.provider}
      <div class="flex gap-1 items-baseline mb-0.5">
        <span class="text-gray-400 text-[10px] shrink-0">Provider:</span>
        <span class="text-gray-700 font-mono text-[11px]">{data.provider}</span>
      </div>
    {/if}
    {#if data.model}
      <div class="flex gap-1 items-baseline mb-0.5">
        <span class="text-gray-400 text-[10px] shrink-0">Model:</span>
        <span class="text-gray-700 font-mono text-[11px]">{data.model}</span>
      </div>
    {/if}
    {#if !data.provider && !data.model}
      <div class="text-gray-400 text-[11px]">Configure provider & model</div>
    {/if}
  </div>
  <NodePreview state={runState} />
  <HandleGroup position="right" class="!gap-1">
    <Handle id="embedding" type="output" port="embedding" label="embedding" />
    <Handle id="data" type="output" port="data" label="data" />
  </HandleGroup>
</div>
