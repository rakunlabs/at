<script lang="ts">
  import { Handle, type NodeProps } from 'kaykay';

  interface OutputData {
    label?: string;
    fields?: string[];
  }

  let { id, data, selected }: NodeProps<OutputData> = $props();
</script>

<div
  class={[
    'bg-white border border-gray-300 rounded-md min-w-40 text-xs shadow-sm select-none',
    selected && 'border-blue-500 ring-2 ring-blue-500/25'
  ]}
>
  <Handle id="input" type="input" port="data" position="left" accept={['data', 'text', 'llm_response']} label="in" />
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-red-50">
    <span class="text-[9px] font-bold px-1 py-px rounded bg-red-500 text-white tracking-wide">OUT</span>
    <span class="text-gray-900">{data.label || 'Output'}</span>
  </div>
  <div class="px-2.5 py-1.5">
    {#if data.fields && data.fields.length > 0}
      <div class="flex flex-col gap-0.5">
        {#each data.fields as field}
          <div class="text-gray-500 font-mono text-[11px]">{field}</div>
        {/each}
      </div>
    {:else}
      <div class="text-gray-400 text-[11px]">Workflow output data</div>
    {/if}
  </div>
</div>
