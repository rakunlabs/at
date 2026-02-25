<script lang="ts">
  import { Handle, type NodeProps } from 'kaykay';

  interface CronTriggerData {
    label?: string;
    schedule?: string;
    payload?: Record<string, any>;
  }

  let { id, data, selected }: NodeProps<CronTriggerData> = $props();
</script>

<div
  class={[
    'bg-white border border-gray-300 rounded-md min-w-44 text-xs shadow-sm select-none',
    selected && 'border-blue-500 ring-2 ring-blue-500/25'
  ]}
>
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-orange-50">
    <span class="text-[9px] font-bold px-1 py-px rounded bg-orange-500 text-white tracking-wide">CRON</span>
    <span class="text-gray-900">{data.label || 'Cron Trigger'}</span>
  </div>
  <div class="px-2.5 py-1.5">
    {#if data.schedule}
      <div class="flex gap-1 items-baseline mb-0.5">
        <span class="text-gray-400 text-[10px] shrink-0">Schedule:</span>
        <span class="text-gray-700 font-mono text-[11px]">{data.schedule}</span>
      </div>
    {:else}
      <div class="text-gray-400 text-[11px]">Set cron schedule</div>
    {/if}
    {#if data.payload && Object.keys(data.payload).length > 0}
      <div class="flex gap-1 items-baseline">
        <span class="text-gray-400 text-[10px] shrink-0">Payload:</span>
        <span class="text-gray-500 text-[10px]">{Object.keys(data.payload).length} fields</span>
      </div>
    {/if}
  </div>
  <Handle id="output" type="output" port="data" position="right" label="out" />
</div>
