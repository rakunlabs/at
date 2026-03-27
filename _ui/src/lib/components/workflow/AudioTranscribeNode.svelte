<script lang="ts">
  import { Handle, HandleGroup, type NodeProps } from 'kaykay';
  import NodePreview from './NodePreview.svelte';
  import { workflowRun } from '@/lib/store/workflow-run.svelte';

  interface AudioTranscribeData {
    label?: string;
    provider?: string;
    model?: string;
    language?: string;
    response_format?: string;
    node_number?: number;
  }

  let { id, data, selected }: NodeProps<AudioTranscribeData> = $props();
  let runState = $derived(workflowRun.nodeRunStates[id]);
</script>

<div
  class={[
    'bg-white border border-gray-300 rounded-md min-w-45 max-w-60 text-xs shadow-sm select-none',
    selected && 'border-blue-500 ring-2 ring-blue-500/25'
  ]}
>
  <Handle id="audio" type="input" port="audio" accept={['audio', 'text', 'data']} position="left" label="audio" />
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-orange-50">
    <span class="inline-flex items-center leading-none text-[9px] font-bold px-1 py-1 rounded bg-orange-500 text-white tracking-wide">STT</span>
    <span class="text-gray-900">{data.label || 'Audio Transcribe'}</span>
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
    {#if data.language}
      <div class="flex gap-1 items-baseline mb-0.5">
        <span class="text-gray-400 text-[10px] shrink-0">Language:</span>
        <span class="text-gray-700 font-mono text-[11px]">{data.language}</span>
      </div>
    {/if}
    {#if data.response_format}
      <div class="flex gap-1 items-baseline mb-0.5">
        <span class="text-gray-400 text-[10px] shrink-0">Format:</span>
        <span class="text-gray-700 font-mono text-[11px]">{data.response_format}</span>
      </div>
    {/if}
    {#if !data.provider && !data.model}
      <div class="text-gray-400 text-[11px]">Configure provider & model</div>
    {/if}
  </div>
  <NodePreview state={runState} />
  <HandleGroup position="right" class="!gap-1">
    <Handle id="text" type="output" port="text" label="text" />
    <Handle id="segments" type="output" port="data" label="segments" />
  </HandleGroup>
</div>
