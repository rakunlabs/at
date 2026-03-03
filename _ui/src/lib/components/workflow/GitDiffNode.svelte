<script lang="ts">
  import { Handle, HandleGroup, type NodeProps } from 'kaykay';

  interface GitDiffData {
    label?: string;
    file_pattern?: string;
    node_number?: number;
  }

  let { id, data, selected }: NodeProps<GitDiffData> = $props();
</script>

<div
  class={[
    'bg-white border border-gray-300 rounded-md min-w-45 max-w-60 text-xs shadow-sm select-none',
    selected && 'border-blue-500 ring-2 ring-blue-500/25'
  ]}
>
  <Handle id="data" type="input" port="data" accept={['data', 'text']} position="left" label="data" />
  
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-amber-50">
    <span class="inline-flex items-center leading-none text-[9px] font-bold px-1 py-1 rounded bg-amber-600 text-white tracking-wide">DIFF</span>
    <span class="text-gray-900">{data.label || 'Git Diff'}</span>
    {#if data.node_number != null}<span class="text-[9px] font-medium text-gray-400 ml-auto">#{data.node_number}</span>{/if}
  </div>
  <div class="px-2.5 py-1.5">
    {#if data.file_pattern}
      <div class="font-mono text-[10px] text-gray-500 whitespace-pre-wrap break-all leading-snug">{data.file_pattern}</div>
    {:else}
      <div class="text-gray-400 text-[11px]">Detect changed files</div>
    {/if}
  </div>
  <HandleGroup position="right" class="!gap-1">
    <Handle id="output" type="output" port="data" label="output" />
  </HandleGroup>
</div>
