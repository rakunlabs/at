<script lang="ts">
  import { Handle, type NodeProps } from 'kaykay';

  interface LoopData {
    label?: string;
    expression?: string;
    node_number?: number;
  }

  let { id, data, selected }: NodeProps<LoopData> = $props();

  let previewExpr = $derived(() => {
    if (!data.expression) return '';
    const maxLen = 60;
    return data.expression.length > maxLen ? data.expression.slice(0, maxLen) + '...' : data.expression;
  });
</script>

<div
  class={[
    'bg-white border border-gray-300 rounded-md min-w-45 max-w-60 text-xs shadow-sm select-none',
    selected && 'border-blue-500 ring-2 ring-blue-500/25'
  ]}
>
  <Handle id="input" type="input" port="data" accept={['data', 'text']} position="left" label="data" />
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-violet-50">
    <span class="text-[9px] font-bold px-1 py-px rounded bg-violet-600 text-white tracking-wide">LOOP</span>
    <span class="text-gray-900">{data.label || 'Loop'}</span>
    {#if data.node_number != null}<span class="text-[9px] font-medium text-gray-400 ml-auto">#{data.node_number}</span>{/if}
  </div>
  <div class="px-2.5 py-1.5">
    {#if data.expression}
      <div class="font-mono text-[10px] text-gray-500 whitespace-pre-wrap break-all leading-snug">{previewExpr()}</div>
    {:else}
      <div class="text-gray-400 text-[11px]">Set JS expression for items</div>
    {/if}
  </div>
  <Handle id="item" type="output" port="data" position="right" label="item" />
</div>
