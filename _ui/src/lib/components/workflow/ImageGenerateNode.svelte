<script lang="ts">
  import { Handle, HandleGroup, type NodeProps } from 'kaykay';
  import NodePreview from './NodePreview.svelte';
  import { workflowRun } from '@/lib/store/workflow-run.svelte';

  interface ImageGenerateData {
    label?: string;
    provider?: string;
    model?: string;
    size?: string;
    quality?: string;
    style?: string;
    node_number?: number;
  }

  let { id, data, selected }: NodeProps<ImageGenerateData> = $props();
  let runState = $derived(workflowRun.nodeRunStates[id]);
</script>

<div
  class={[
    'bg-white border border-gray-300 rounded-md min-w-45 max-w-60 text-xs shadow-sm select-none',
    selected && 'border-blue-500 ring-2 ring-blue-500/25'
  ]}
>
  <Handle id="prompt" type="input" port="text" accept={['text', 'data']} position="left" label="prompt" />
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-green-50">
    <span class="inline-flex items-center leading-none text-[9px] font-bold px-1 py-1 rounded bg-green-600 text-white tracking-wide">IMG</span>
    <span class="text-gray-900">{data.label || 'Image Generate'}</span>
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
    {#if data.size}
      <div class="flex gap-1 items-baseline mb-0.5">
        <span class="text-gray-400 text-[10px] shrink-0">Size:</span>
        <span class="text-gray-700 font-mono text-[11px]">{data.size}</span>
      </div>
    {/if}
    {#if data.quality}
      <div class="flex gap-1 items-baseline mb-0.5">
        <span class="text-gray-400 text-[10px] shrink-0">Quality:</span>
        <span class="text-gray-700 font-mono text-[11px]">{data.quality}</span>
      </div>
    {/if}
    {#if data.style}
      <div class="flex gap-1 items-baseline mb-0.5">
        <span class="text-gray-400 text-[10px] shrink-0">Style:</span>
        <span class="text-gray-700 font-mono text-[11px]">{data.style}</span>
      </div>
    {/if}
    {#if !data.provider && !data.model}
      <div class="text-gray-400 text-[11px]">Configure provider & model</div>
    {/if}
  </div>
  <NodePreview state={runState} />
  <HandleGroup position="right" class="!gap-1">
    <Handle id="image" type="output" port="image" label="image" />
    <Handle id="images" type="output" port="data" label="images" />
    <Handle id="metadata" type="output" port="data" label="metadata" />
  </HandleGroup>
</div>
