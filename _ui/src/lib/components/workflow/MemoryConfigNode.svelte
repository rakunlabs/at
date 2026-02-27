<script lang="ts">
  import { Handle, HandleGroup, type NodeProps } from 'kaykay';

  interface MemoryConfigData {
    label?: string;
    node_number?: number;
  }

  let { id, data, selected }: NodeProps<MemoryConfigData> = $props();
</script>

<div
  class={[
    'bg-white border border-gray-300 rounded-md min-w-36 max-w-52 text-xs shadow-sm select-none',
    selected && 'border-teal-500 ring-2 ring-teal-500/25'
  ]}
>
  <Handle id="data" type="input" port="data" accept={['data', 'text']} label="data" position="left" />
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-teal-50">
    <span class="text-[9px] font-bold px-1 py-px rounded bg-teal-600 text-white tracking-wide">MEM</span>
    <span class="text-gray-900">{data.label || 'Memory'}</span>
    {#if data.node_number != null}<span class="text-[9px] font-medium text-gray-400 ml-auto">#{data.node_number}</span>{/if}
  </div>
  <div class="px-2.5 py-1.5">
    <div class="text-gray-400 text-[11px]">Passes data as memory context</div>
  </div>
  <HandleGroup position="top" class="!gap-1">
    <Handle id="memory" type="output" port="data" label="memory" />
  </HandleGroup>
</div>
